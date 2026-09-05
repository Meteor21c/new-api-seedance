package xai

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"

	relaydto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

const (
	ChannelName       = "xai-video"
	requestContextKey = "xai_video_request"
	defaultDuration   = 5
	maxDuration       = 15
	maxReferences     = 3
)

var ModelList = []string{"grok-imagine-video"}

var allowedResolutions = map[string]bool{
	"480p": true,
	"720p": true,
}

var allowedAspectRatios = map[string]bool{
	"1:1":  true,
	"16:9": true,
	"9:16": true,
	"4:3":  true,
	"3:4":  true,
	"3:2":  true,
	"2:3":  true,
}

type inputOptions struct {
	AspectRatio          string   `json:"aspect_ratio,omitempty"`
	Resolution           string   `json:"resolution,omitempty"`
	ReferenceImages      []string `json:"reference_images,omitempty"`
	ReferenceMaterialIDs []string `json:"reference_material_ids,omitempty"`
	Image                string   `json:"image,omitempty"`
	StartImageURL        string   `json:"start_image_url,omitempty"`
	EndImageURL          string   `json:"end_image_url,omitempty"`
	StartMaterialID      string   `json:"start_material_id,omitempty"`
	EndMaterialID        string   `json:"end_material_id,omitempty"`
}

type mediaURL struct {
	URL string `json:"url"`
}

type requestPayload struct {
	Model           string     `json:"model"`
	Prompt          string     `json:"prompt"`
	Duration        int        `json:"duration,omitempty"`
	AspectRatio     string     `json:"aspect_ratio,omitempty"`
	Resolution      string     `json:"resolution,omitempty"`
	Image           *mediaURL  `json:"image,omitempty"`
	ReferenceImages []mediaURL `json:"reference_images,omitempty"`
}

type upstreamError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type upstreamVideo struct {
	URL      string `json:"url,omitempty"`
	Duration int    `json:"duration,omitempty"`
}

type upstreamResponse struct {
	RequestID string            `json:"request_id,omitempty"`
	TaskID    string            `json:"task_id,omitempty"`
	ID        string            `json:"id,omitempty"`
	Status    string            `json:"status,omitempty"`
	Model     string            `json:"model,omitempty"`
	URL       string            `json:"url,omitempty"`
	Video     upstreamVideo     `json:"video,omitempty"`
	Error     *upstreamError    `json:"error,omitempty"`
	Data      *upstreamResponse `json:"data,omitempty"`
}

type TaskAdaptor struct {
	taskcommon.BaseBilling
	apiKey  string
	baseURL string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.apiKey = info.ApiKey
	a.baseURL = strings.TrimRight(info.ChannelBaseUrl, "/")
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *taskdto.TaskError {
	if taskErr := relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate); taskErr != nil {
		return taskErr
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if strings.TrimSpace(req.Model) == "" {
		return service.TaskErrorWrapperLocal(errors.New("model field is required"), "missing_model", http.StatusBadRequest)
	}

	var options inputOptions
	if err := common.UnmarshalBodyReusable(c, &options); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if err := taskcommon.UnmarshalMetadata(req.Metadata, &options); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if err := resolveMaterialOptions(c, &options); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_material", http.StatusBadRequest)
	}

	payload, err := normalizeRequest(req, options)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	c.Set(requestContextKey, payload)
	return nil
}

func resolveMaterialOptions(c *gin.Context, options *inputOptions) error {
	userID := c.GetInt("id")
	objectKeys := make([]string, 0, len(options.ReferenceMaterialIDs)+2)

	referenceURLs, keys, err := service.ResolveMaterialIDs(c.Request.Context(), userID, options.ReferenceMaterialIDs)
	if err != nil {
		return fmt.Errorf("resolve reference materials: %w", err)
	}
	options.ReferenceImages = append(options.ReferenceImages, referenceURLs...)
	objectKeys = append(objectKeys, keys...)

	if materialID := strings.TrimSpace(options.StartMaterialID); materialID != "" {
		urls, resolvedKeys, resolveErr := service.ResolveMaterialIDs(c.Request.Context(), userID, []string{materialID})
		if resolveErr != nil {
			return fmt.Errorf("resolve start frame material: %w", resolveErr)
		}
		options.StartImageURL = urls[0]
		objectKeys = append(objectKeys, resolvedKeys...)
	}
	if materialID := strings.TrimSpace(options.EndMaterialID); materialID != "" {
		urls, resolvedKeys, resolveErr := service.ResolveMaterialIDs(c.Request.Context(), userID, []string{materialID})
		if resolveErr != nil {
			return fmt.Errorf("resolve end frame material: %w", resolveErr)
		}
		options.EndImageURL = urls[0]
		objectKeys = append(objectKeys, resolvedKeys...)
	}
	if len(objectKeys) > 0 {
		c.Set(service.MaterialObjectKeysContextKey, objectKeys)
	}
	return nil
}

func normalizeRequest(req relaycommon.TaskSubmitReq, options inputOptions) (requestPayload, error) {
	duration := req.Duration
	if duration == 0 && strings.TrimSpace(req.Seconds) != "" {
		duration, _ = strconv.Atoi(strings.TrimSpace(req.Seconds))
	}
	if duration == 0 {
		duration = defaultDuration
	}
	if duration < 1 || duration > maxDuration {
		return requestPayload{}, fmt.Errorf("duration must be between 1 and %d seconds", maxDuration)
	}

	resolution := strings.ToLower(strings.TrimSpace(options.Resolution))
	if resolution == "" {
		resolution = strings.ToLower(strings.TrimSpace(req.Size))
	}
	if resolution == "" || strings.Contains(resolution, "x") {
		resolution = "480p"
	}
	if !allowedResolutions[resolution] {
		return requestPayload{}, fmt.Errorf("resolution %q is not supported by grok-imagine-video", resolution)
	}

	aspectRatio := strings.TrimSpace(options.AspectRatio)
	if aspectRatio == "" {
		aspectRatio = aspectRatioFromSize(req.Size)
	}
	if aspectRatio == "" {
		aspectRatio = "16:9"
	}
	if !allowedAspectRatios[aspectRatio] {
		return requestPayload{}, fmt.Errorf("unsupported aspect_ratio %q", aspectRatio)
	}

	if strings.TrimSpace(options.EndImageURL) != "" {
		return requestPayload{}, errors.New("grok-imagine-video does not support an end frame; use text with references")
	}

	imageURL := firstNonEmpty(options.StartImageURL, options.Image)
	referenceURLs := append([]string{}, req.Images...)
	referenceURLs = append(referenceURLs, options.ReferenceImages...)
	referenceURLs = cleanReferenceURLs(referenceURLs)
	if strings.TrimSpace(imageURL) != "" && len(referenceURLs) > 0 {
		return requestPayload{}, errors.New("image and reference_images cannot be used together")
	}
	if len(referenceURLs) > maxReferences {
		return requestPayload{}, fmt.Errorf("grok-imagine-video supports at most %d reference images", maxReferences)
	}
	if imageURL != "" {
		if err := validateMediaURL(imageURL); err != nil {
			return requestPayload{}, fmt.Errorf("invalid image: %w", err)
		}
	}
	for index, rawURL := range referenceURLs {
		if err := validateMediaURL(rawURL); err != nil {
			return requestPayload{}, fmt.Errorf("invalid reference_images[%d]: %w", index, err)
		}
	}

	payload := requestPayload{
		Model:       strings.TrimSpace(req.Model),
		Prompt:      strings.TrimSpace(req.Prompt),
		Duration:    duration,
		AspectRatio: aspectRatio,
		Resolution:  resolution,
	}
	if imageURL != "" {
		payload.Image = &mediaURL{URL: imageURL}
	}
	for _, rawURL := range referenceURLs {
		payload.ReferenceImages = append(payload.ReferenceImages, mediaURL{URL: rawURL})
	}
	return payload, nil
}

func aspectRatioFromSize(size string) string {
	switch strings.ToLower(strings.TrimSpace(size)) {
	case "1280x720", "1792x1024":
		return "16:9"
	case "720x1280", "1024x1792":
		return "9:16"
	case "1024x1024":
		return "1:1"
	default:
		return ""
	}
}

func cleanReferenceURLs(values []string) []string {
	cleaned := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		for _, prefix := range []string{"reference:", "start:", "end:"} {
			if strings.HasPrefix(strings.ToLower(value), prefix) {
				value = strings.TrimSpace(value[len(prefix):])
				break
			}
		}
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		cleaned = append(cleaned, value)
	}
	return cleaned
}

func validateMediaURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "data:image/") {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("URL must use http or https")
	}
	if parsed.Host == "" {
		return errors.New("URL host is required")
	}
	return nil
}

func getNormalizedRequest(c *gin.Context) (requestPayload, error) {
	value, exists := c.Get(requestContextKey)
	if !exists {
		return requestPayload{}, errors.New("normalized xAI video request not found")
	}
	payload, ok := value.(requestPayload)
	if !ok {
		return requestPayload{}, errors.New("invalid normalized xAI video request")
	}
	return payload, nil
}

func (a *TaskAdaptor) EstimateBilling(c *gin.Context, _ *relaycommon.RelayInfo) map[string]float64 {
	payload, err := getNormalizedRequest(c)
	if err != nil {
		return nil
	}
	return map[string]float64{"seconds": float64(payload.Duration)}
}

func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return a.baseURL + "/v1/videos/generations", nil
}

func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	payload, err := getNormalizedRequest(c)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(info.UpstreamModelName) != "" {
		payload.Model = info.UpstreamModelName
	}
	body, err := common.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(body), nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *taskdto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}
	_ = resp.Body.Close()

	response, err := parseResponse(responseBody)
	if err != nil {
		return "", nil, service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
	}
	upstreamTaskID := firstNonEmpty(response.RequestID, response.TaskID, response.ID)
	if upstreamTaskID == "" {
		return "", nil, service.TaskErrorWrapper(errors.New("request_id is empty"), "invalid_response", http.StatusInternalServerError)
	}

	video := relaydto.NewOpenAIVideo()
	video.ID = info.PublicTaskID
	video.TaskID = info.PublicTaskID
	video.Model = info.OriginModelName
	video.CreatedAt = time.Now().Unix()
	c.JSON(http.StatusOK, video)
	return upstreamTaskID, responseBody, nil
}

func (a *TaskAdaptor) FetchTask(baseURL, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok || strings.TrimSpace(taskID) == "" {
		return nil, errors.New("invalid task_id")
	}
	requestURL := strings.TrimRight(baseURL, "/") + "/v1/videos/" + url.PathEscape(taskID)
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func parseResponse(body []byte) (*upstreamResponse, error) {
	var response upstreamResponse
	if err := common.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	for response.Data != nil {
		nested := response.Data
		if nested.RequestID == "" {
			nested.RequestID = response.RequestID
		}
		response = *nested
	}
	return &response, nil
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	response, err := parseResponse(respBody)
	if err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}
	result := &relaycommon.TaskInfo{Code: 0}
	switch strings.ToLower(strings.TrimSpace(response.Status)) {
	case "pending", "queued", "submitted":
		result.Status = model.TaskStatusQueued
		result.Progress = taskcommon.ProgressQueued
	case "processing", "in_progress", "running":
		result.Status = model.TaskStatusInProgress
		result.Progress = taskcommon.ProgressInProgress
	case "done", "completed", "succeeded", "success":
		result.Status = model.TaskStatusSuccess
		result.Progress = taskcommon.ProgressComplete
		result.Url = firstNonEmpty(response.Video.URL, response.URL)
	case "failed", "expired", "cancelled", "canceled":
		result.Status = model.TaskStatusFailure
		result.Progress = taskcommon.ProgressComplete
		result.Reason = upstreamErrorMessage(response)
	default:
		return result, nil
	}
	return result, nil
}

func upstreamErrorMessage(response *upstreamResponse) string {
	if response != nil && response.Error != nil {
		if response.Error.Message != "" {
			return response.Error.Message
		}
		if response.Error.Code != "" {
			return response.Error.Code
		}
	}
	return "video generation failed"
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	video := relaydto.NewOpenAIVideo()
	video.ID = originTask.TaskID
	video.TaskID = originTask.TaskID
	video.Status = originTask.Status.ToVideoStatus()
	video.SetProgressStr(originTask.Progress)
	video.CreatedAt = int64(originTask.CreatedAt)
	video.CompletedAt = originTask.UpdatedAt
	video.Model = originTask.Properties.OriginModelName
	if resultURL := originTask.GetResultURL(); resultURL != "" {
		video.SetMetadata("url", resultURL)
	}
	if originTask.Status == model.TaskStatusFailure {
		video.Error = &relaydto.OpenAIVideoError{
			Message: firstNonEmpty(originTask.FailReason, "video generation failed"),
			Code:    "video_generation_failed",
		}
	}
	return common.Marshal(video)
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
