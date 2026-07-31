package fzyinghe

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

const (
	ChannelName          = "fzyinghe-video"
	requestContextKey    = "fzyinghe_video_request"
	defaultResolution    = "720p"
	defaultAspectRatio   = "9:16"
	defaultDuration      = 15
	defaultMode          = "text_with_reference"
	minDuration          = 4
	maxDuration          = 15
	maxPromptCharacters  = 1300
	maxReferenceImages   = 9
	maxReferenceAudio    = 3
	maxReferenceVideos   = 3
	taskEndpointPath     = "/video/generation/tasks"
	referencePrefix      = "reference:"
	startReferencePrefix = "start:"
	endReferencePrefix   = "end:"
)

var ModelList = []string{
	"cheap-seedance-2.0",
	"cheap-seedance-2.0-fast",
	"cheap-seedance-2.0-mini",
}

var allowedAspectRatios = map[string]bool{
	"16:9": true,
	"9:16": true,
	"1:1":  true,
	"4:3":  true,
	"3:4":  true,
	"21:9": true,
}

var allowedResolutions = map[string]map[string]float64{
	"cheap-seedance-2.0": {
		"480p":  0.5,
		"720p":  1,
		"1080p": 2.5,
		"4K":    5,
	},
	"cheap-seedance-2.0-fast": {
		"480p": 0.5,
		"720p": 1,
	},
	"cheap-seedance-2.0-mini": {
		"480p": 0.5,
		"720p": 1,
	},
}

var allowedModes = map[string]bool{
	"text_with_reference": true,
	"start_end_frame":     true,
}

type inputOptions struct {
	Name                 string   `json:"name,omitempty"`
	AspectRatio          string   `json:"aspect_ratio,omitempty"`
	Resolution           string   `json:"resolution,omitempty"`
	Audio                *bool    `json:"audio,omitempty"`
	ReferenceImages      []string `json:"reference_images,omitempty"`
	ReferenceMaterialIDs []string `json:"reference_material_ids,omitempty"`
	StartImageURL        string   `json:"start_image_url,omitempty"`
	EndImageURL          string   `json:"end_image_url,omitempty"`
	StartMaterialID      string   `json:"start_material_id,omitempty"`
	EndMaterialID        string   `json:"end_material_id,omitempty"`
}

type requestPayload struct {
	Model           string         `json:"model"`
	Input           string         `json:"input"`
	Name            string         `json:"name,omitempty"`
	AspectRatio     string         `json:"aspect_ratio"`
	Resolution      string         `json:"resolution"`
	DurationSeconds int            `json:"duration_seconds"`
	Mode            string         `json:"mode"`
	Audio           bool           `json:"audio"`
	ReferenceImages []string       `json:"reference_images,omitempty"`
	StartImageURL   string         `json:"start_image_url,omitempty"`
	EndImageURL     string         `json:"end_image_url,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type upstreamError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type upstreamTask struct {
	TaskID       string         `json:"taskId,omitempty"`
	ID           string         `json:"id,omitempty"`
	Status       string         `json:"status,omitempty"`
	CreatedAt    int64          `json:"createdAt,omitempty"`
	ResultURL    string         `json:"resultUrl,omitempty"`
	URL          string         `json:"url,omitempty"`
	ThumbnailURL string         `json:"thumbnailUrl,omitempty"`
	FailReason   string         `json:"failReason,omitempty"`
	URLExpiresAt string         `json:"url_expires_at,omitempty"`
	Error        *upstreamError `json:"error,omitempty"`
}

type upstreamEnvelope struct {
	Message string       `json:"msg,omitempty"`
	Data    upstreamTask `json:"data,omitempty"`
}

type TaskAdaptor struct {
	taskcommon.BaseBilling
	apiKey  string
	baseURL string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	if taskErr := relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate); taskErr != nil {
		return taskErr
	}

	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	req.Model, err = mappedRequestModel(req.Model, c.GetString("model_mapping"))
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_model_mapping", http.StatusBadRequest)
	}

	var options inputOptions
	if err := common.UnmarshalBodyReusable(c, &options); err != nil {
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

func mappedRequestModel(modelName string, rawMapping string) (string, error) {
	if strings.TrimSpace(rawMapping) == "" || strings.TrimSpace(rawMapping) == "{}" {
		return modelName, nil
	}
	var mapping map[string]string
	if err := common.Unmarshal([]byte(rawMapping), &mapping); err != nil {
		return "", errors.Wrap(err, "parse model mapping")
	}

	current := modelName
	visited := map[string]bool{current: true}
	for {
		next := strings.TrimSpace(mapping[current])
		if next == "" || next == current {
			return current, nil
		}
		if visited[next] {
			return "", errors.New("model mapping contains a cycle")
		}
		visited[next] = true
		current = next
	}
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

func (a *TaskAdaptor) EstimateBilling(c *gin.Context, _ *relaycommon.RelayInfo) map[string]float64 {
	payload, err := getNormalizedRequest(c)
	if err != nil {
		return nil
	}
	resolutionRatio := allowedResolutions[payload.Model][payload.Resolution]
	return map[string]float64{
		"duration":   float64(payload.DurationSeconds),
		"resolution": resolutionRatio,
	}
}

func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return tasksEndpoint(a.baseURL), nil
}

func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	if info.PublicTaskID != "" {
		req.Header.Set("Idempotency-Key", info.PublicTaskID)
	}
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	payload, err := getNormalizedRequest(c)
	if err != nil {
		return nil, err
	}
	if info.IsModelMapped {
		payload.Model = info.UpstreamModelName
	} else {
		info.UpstreamModelName = payload.Model
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

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	task, err := parseUpstreamTask(responseBody)
	if err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}
	upstreamTaskID := task.TaskID
	if upstreamTaskID == "" {
		upstreamTaskID = task.ID
	}
	if upstreamTaskID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
		return
	}

	video := dto.NewOpenAIVideo()
	video.ID = info.PublicTaskID
	video.TaskID = info.PublicTaskID
	video.CreatedAt = time.Now().Unix()
	video.Model = info.OriginModelName
	c.JSON(http.StatusOK, video)
	return upstreamTaskID, responseBody, nil
}

func (a *TaskAdaptor) FetchTask(baseURL, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok || strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("invalid task_id")
	}
	requestURL := tasksEndpoint(baseURL) + "/" + url.PathEscape(taskID)
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

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	task, err := parseUpstreamTask(respBody)
	if err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	result := &relaycommon.TaskInfo{Code: 0}
	switch strings.ToUpper(task.Status) {
	case "PENDING", "QUEUED", "SUBMITTED":
		result.Status = model.TaskStatusQueued
		result.Progress = taskcommon.ProgressQueued
	case "RUNNING", "PROCESSING", "IN_PROGRESS":
		result.Status = model.TaskStatusInProgress
		result.Progress = "50%"
	case "SUCCESS", "SUCCEEDED", "COMPLETED":
		result.Status = model.TaskStatusSuccess
		result.Progress = taskcommon.ProgressComplete
		result.Url = firstNonEmpty(task.ResultURL, task.URL)
	case "FAILED", "FAILURE", "CANCELLED", "CANCELED":
		result.Status = model.TaskStatusFailure
		result.Progress = taskcommon.ProgressComplete
		result.Reason = firstNonEmpty(task.FailReason, upstreamErrorMessage(task.Error), "video generation failed")
	default:
		result.Status = model.TaskStatusInProgress
		result.Progress = taskcommon.ProgressInProgress
	}
	return result, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	video := dto.NewOpenAIVideo()
	video.ID = originTask.TaskID
	video.TaskID = originTask.TaskID
	video.Status = originTask.Status.ToVideoStatus()
	video.SetProgressStr(originTask.Progress)
	video.CreatedAt = originTask.CreatedAt
	video.CompletedAt = originTask.UpdatedAt
	video.Model = originTask.Properties.OriginModelName
	if resultURL := originTask.GetResultURL(); resultURL != "" {
		video.SetMetadata("url", resultURL)
	}
	if originTask.Status == model.TaskStatusFailure {
		video.Error = &dto.OpenAIVideoError{
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

func normalizeRequest(req relaycommon.TaskSubmitReq, options inputOptions) (requestPayload, error) {
	modelName := strings.TrimSpace(req.Model)
	if _, ok := allowedResolutions[modelName]; !ok {
		return requestPayload{}, fmt.Errorf("unsupported model %q", modelName)
	}
	prompt := strings.TrimSpace(req.Prompt)
	if utf8.RuneCountInString(prompt) > maxPromptCharacters {
		return requestPayload{}, fmt.Errorf("prompt must not exceed %d characters", maxPromptCharacters)
	}

	duration, err := requestDuration(req)
	if err != nil {
		return requestPayload{}, err
	}
	if duration < minDuration || duration > maxDuration {
		return requestPayload{}, fmt.Errorf("duration must be between %d and %d seconds", minDuration, maxDuration)
	}

	resolution := normalizeResolution(options.Resolution)
	if resolution == "" {
		resolution = normalizeResolution(req.Size)
	}
	if resolution == "" {
		resolution = defaultResolution
	}
	if _, ok := allowedResolutions[modelName][resolution]; !ok {
		return requestPayload{}, fmt.Errorf("resolution %q is not supported by model %q", resolution, modelName)
	}

	aspectRatio := strings.TrimSpace(options.AspectRatio)
	if aspectRatio == "" {
		aspectRatio = defaultAspectRatio
	}
	if !allowedAspectRatios[aspectRatio] {
		return requestPayload{}, fmt.Errorf("unsupported aspect_ratio %q", aspectRatio)
	}

	references := append([]string{}, req.Images...)
	references = append(references, options.ReferenceImages...)
	hasStartReference, hasEndReference, err := validateReferences(references)
	if err != nil {
		return requestPayload{}, err
	}
	if options.StartImageURL != "" {
		if err := validateReferenceURL(options.StartImageURL, true); err != nil {
			return requestPayload{}, fmt.Errorf("invalid start_image_url: %w", err)
		}
		hasStartReference = true
	}
	if options.EndImageURL != "" {
		if err := validateReferenceURL(options.EndImageURL, true); err != nil {
			return requestPayload{}, fmt.Errorf("invalid end_image_url: %w", err)
		}
		hasEndReference = true
	}

	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = defaultMode
	}
	if hasStartReference || hasEndReference {
		mode = "start_end_frame"
	}
	if !allowedModes[mode] {
		return requestPayload{}, fmt.Errorf("unsupported mode %q", mode)
	}
	if mode == "start_end_frame" && (!hasStartReference || !hasEndReference) {
		return requestPayload{}, fmt.Errorf("start_end_frame mode requires both start and end images")
	}

	audio := true
	if options.Audio != nil {
		audio = *options.Audio
	}

	return requestPayload{
		Model:           modelName,
		Input:           prompt,
		Name:            strings.TrimSpace(options.Name),
		AspectRatio:     aspectRatio,
		Resolution:      resolution,
		DurationSeconds: duration,
		Mode:            mode,
		Audio:           audio,
		ReferenceImages: references,
		StartImageURL:   strings.TrimSpace(options.StartImageURL),
		EndImageURL:     strings.TrimSpace(options.EndImageURL),
		Metadata:        req.Metadata,
	}, nil
}

func requestDuration(req relaycommon.TaskSubmitReq) (int, error) {
	if req.Duration != 0 {
		return req.Duration, nil
	}
	if strings.TrimSpace(req.Seconds) == "" {
		return defaultDuration, nil
	}
	duration, err := strconv.Atoi(req.Seconds)
	if err != nil {
		return 0, fmt.Errorf("duration must be an integer")
	}
	return duration, nil
}

func normalizeResolution(raw string) string {
	value := strings.TrimSpace(raw)
	if strings.EqualFold(value, "4k") {
		return "4K"
	}
	return strings.ToLower(value)
}

func validateReferences(references []string) (hasStart bool, hasEnd bool, err error) {
	imageCount := 0
	audioCount := 0
	videoCount := 0
	for _, rawReference := range references {
		prefix, referenceURL := splitReference(rawReference)
		if referenceURL == "" {
			return false, false, fmt.Errorf("reference URL is required")
		}
		if err := validateReferenceURL(referenceURL, prefix == "start" || prefix == "end"); err != nil {
			return false, false, fmt.Errorf("invalid reference %q: %w", rawReference, err)
		}
		switch prefix {
		case "start":
			hasStart = true
		case "end":
			hasEnd = true
		}

		extension := referenceExtension(referenceURL)
		switch extension {
		case ".jpg", ".jpeg", ".png", ".webp":
			imageCount++
		case ".mp3", ".wav":
			audioCount++
		case ".mp4":
			videoCount++
		default:
			return false, false, fmt.Errorf("unsupported reference file type %q", extension)
		}
	}
	if imageCount > maxReferenceImages {
		return false, false, fmt.Errorf("reference images must not exceed %d", maxReferenceImages)
	}
	if audioCount > maxReferenceAudio {
		return false, false, fmt.Errorf("reference audio files must not exceed %d", maxReferenceAudio)
	}
	if videoCount > maxReferenceVideos {
		return false, false, fmt.Errorf("reference videos must not exceed %d", maxReferenceVideos)
	}
	return hasStart, hasEnd, nil
}

func splitReference(raw string) (prefix string, referenceURL string) {
	value := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(value, referencePrefix):
		return "reference", strings.TrimSpace(strings.TrimPrefix(value, referencePrefix))
	case strings.HasPrefix(value, startReferencePrefix):
		return "start", strings.TrimSpace(strings.TrimPrefix(value, startReferencePrefix))
	case strings.HasPrefix(value, endReferencePrefix):
		return "end", strings.TrimSpace(strings.TrimPrefix(value, endReferencePrefix))
	default:
		return "reference", value
	}
}

func validateReferenceURL(raw string, requireImage bool) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return err
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return fmt.Errorf("must be a public http/https URL")
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") || strings.HasSuffix(hostname, ".local") {
		return fmt.Errorf("must not point to a local host")
	}
	if ip := net.ParseIP(hostname); ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
		return fmt.Errorf("must not point to a private IP address")
	}
	if requireImage {
		switch referenceExtension(raw) {
		case ".jpg", ".jpeg", ".png", ".webp":
		default:
			return fmt.Errorf("must be a jpg, jpeg, png, or webp image")
		}
	}
	return nil
}

func referenceExtension(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(path.Ext(parsed.Path))
}

func tasksEndpoint(baseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(trimmed, taskEndpointPath) {
		return trimmed
	}
	return trimmed + taskEndpointPath
}

func getNormalizedRequest(c *gin.Context) (requestPayload, error) {
	value, exists := c.Get(requestContextKey)
	if !exists {
		return requestPayload{}, fmt.Errorf("normalized request not found in context")
	}
	payload, ok := value.(requestPayload)
	if !ok {
		return requestPayload{}, fmt.Errorf("invalid normalized request type")
	}
	return payload, nil
}

func parseUpstreamTask(body []byte) (upstreamTask, error) {
	var envelope upstreamEnvelope
	if err := common.Unmarshal(body, &envelope); err != nil {
		return upstreamTask{}, err
	}
	if envelope.Data.TaskID != "" || envelope.Data.ID != "" || envelope.Data.Status != "" {
		return envelope.Data, nil
	}
	var task upstreamTask
	if err := common.Unmarshal(body, &task); err != nil {
		return upstreamTask{}, err
	}
	return task, nil
}

func upstreamErrorMessage(upstreamErr *upstreamError) string {
	if upstreamErr == nil {
		return ""
	}
	return upstreamErr.Message
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
