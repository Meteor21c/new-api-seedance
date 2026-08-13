package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func updateOpenAIImageCount(info *relaycommon.RelayInfo, count int64) {
	if info == nil || !info.PriceData.UsePrice || count <= 0 || count > int64(dto.MaxImageN) {
		return
	}
	info.PriceData.AddOtherRatio("n", float64(count))
}

// OpenaiImageHandler handles non-streaming OpenAI image responses
// (generations/edits), returning the parsed usage for billing.
func OpenaiImageHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}

	// Some upstream OpenAI-compatible image providers return a very long,
	// short-lived URL whose path contains an embedded data:image payload. Such
	// URLs commonly exceed browser/proxy limits and later expire with 404. Turn
	// that provider-specific wrapper into b64_json before forwarding it. The
	// normalizer is deliberately a no-op for ordinary URLs and preserves all
	// response fields (including usage).
	responseBody = normalizeOpenAIImageResponseEnvelope(responseBody)
	responseBody = normalizeOpenAIImageContentBody(responseBody)
	if imageRequestWantsBase64(info) {
		responseBody, err = inlineOpenAIImageURLs(c.Request.Context(), responseBody)
		if err != nil {
			logger.LogError(c, fmt.Sprintf("failed to preserve generated image response: %v", err))
			return nil, types.NewOpenAIError(
				fmt.Errorf("generated image could not be preserved because the upstream image URL was unavailable or invalid"),
				types.ErrorCodeBadResponseBody,
				http.StatusBadGateway,
			)
		}
	}

	var usageResp dto.SimpleResponse
	err = common.Unmarshal(responseBody, &usageResp)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if oaiError := openAIImageResponseError(&usageResp); oaiError != nil {
		return nil, types.WithOpenAIError(*oaiError, normalizedUpstreamImageErrorStatus(resp.StatusCode))
	}
	if !hasOpenAIImagePayload(responseBody, imageRequestWantsBase64(info)) {
		shape := describeOpenAIImageResponseShape(responseBody)
		logger.LogError(c, "upstream image response has no deliverable image payload: "+shape)
		return nil, types.NewOpenAIError(
			fmt.Errorf("upstream image response did not contain a supported image payload (%s)", shape),
			types.ErrorCodeBadResponseBody,
			http.StatusBadGateway,
		)
	}

	updateOpenAIImageCount(info, gjson.GetBytes(responseBody, "data.#").Int())

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

	normalizeOpenAIUsage(&usageResp.Usage)
	applyUsagePostProcessing(info, &usageResp.Usage, responseBody)
	return &usageResp.Usage, nil
}

// normalizeOpenAIUsage maps the OpenAI Images usage shape (input_tokens /
// output_tokens / input_tokens_details) onto the canonical prompt/completion
// fields. It is used only on the OpenAI image relay paths (generations/edits,
// streaming and non-streaming): the image API never returns prompt_tokens /
// completion_tokens, so the overwrite (=) semantics here are equivalent to the
// previous additive (+=) behavior while avoiding any future double-counting if
// both field sets are ever populated. Do not reuse this on chat/embedding paths
// without revisiting the overwrite semantics.
func normalizeOpenAIUsage(usage *dto.Usage) {
	if usage == nil {
		return
	}
	if usage.InputTokens != 0 {
		usage.PromptTokens = usage.InputTokens
	}
	if usage.OutputTokens != 0 {
		usage.CompletionTokens = usage.OutputTokens
	}
	if usage.InputTokensDetails != nil {
		usage.PromptTokensDetails.CachedTokens = usage.InputTokensDetails.CachedTokens
		usage.PromptTokensDetails.CachedCreationTokens = usage.InputTokensDetails.CachedCreationTokens
		usage.PromptTokensDetails.CacheWriteTokens = usage.InputTokensDetails.CacheWriteTokens
		usage.PromptTokensDetails.ImageTokens = usage.InputTokensDetails.ImageTokens
		usage.PromptTokensDetails.TextTokens = usage.InputTokensDetails.TextTokens
		usage.PromptTokensDetails.AudioTokens = usage.InputTokensDetails.AudioTokens
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
}

func OpenaiImageStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, "invalid image stream response")
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return OpenaiImageHandler(c, info, resp)
	}
	if !strings.Contains(contentType, "text/event-stream") {
		return openaiImageJSONAsStreamHandler(c, info, resp)
	}
	// Reuse the shared streaming engine (helper.StreamScannerHandler) so the
	// image streaming path gets the same ping keepalive, streaming-timeout
	// watchdog, client-disconnect detection, panic recovery and goroutine
	// cleanup as every other relay stream. The scanner delivers only the
	// "data:" payload, so the SSE "event:" line is rebuilt from the JSON "type"
	// field (real OpenAI image events keep event == type).
	usage := &dto.Usage{}
	var lastStreamData []byte
	var completedImages int64

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		raw := common.StringToByteSlice(data)
		raw = normalizeOpenAIImageContentBody(raw)
		lastStreamData = raw
		if isOpenAIImageStreamErrorEvent(raw) {
			// Record the error as a soft error; the scanner drives the final
			// EndReason. HasErrors() flags the failure for logging/handling.
			sr.Error(fmt.Errorf("%s", extractOpenAIImageStreamErrorMessage(raw)))
		}
		var chunk struct {
			Type  string    `json:"type"`
			Usage dto.Usage `json:"usage"`
		}
		if err := common.Unmarshal(raw, &chunk); err == nil {
			normalizeOpenAIUsage(&chunk.Usage)
			if service.ValidUsage(&chunk.Usage) {
				usage = &chunk.Usage
			}
			if chunk.Type == "image_generation.completed" || chunk.Type == "image_edit.completed" {
				completedImages++
			}
		}
		if err := writeOpenaiImageStreamChunk(c, raw); err != nil {
			sr.Stop(err)
		}
	})

	// StreamScannerHandler consumes the upstream [DONE]; re-emit it so the
	// client still receives a terminal data: [DONE].
	if info.StreamStatus != nil && info.StreamStatus.EndReason == relaycommon.StreamEndReasonDone {
		helper.Done(c)
	}

	applyUsagePostProcessing(info, usage, lastStreamData)
	// Only trust completedImages when upstream finished the stream (done/eof).
	// On client-side aborts (client_gone, or handler_stop from a failed client
	// write) the counter undercounts what upstream actually generated and
	// charged, so keep the requested n — otherwise a client could pay for one
	// image by disconnecting right after the first completed event. The abort
	// guard only blocks lowering the charge: if completed events already
	// exceed the recorded n, bill the higher actual count regardless.
	if info.StreamStatus != nil {
		upstreamFinished := info.StreamStatus.EndReason == relaycommon.StreamEndReasonDone ||
			info.StreamStatus.EndReason == relaycommon.StreamEndReasonEOF
		requestedN := 1.0
		if n, ok := info.PriceData.OtherRatios()["n"]; ok {
			requestedN = n
		}
		if upstreamFinished || float64(completedImages) > requestedN {
			updateOpenAIImageCount(info, completedImages)
		}
	}
	return usage, nil
}

// writeOpenaiImageStreamChunk rebuilds the SSE frame for an image stream chunk:
// it emits an "event:" line derived from the JSON "type" field (when present)
// followed by the verbatim "data:" payload, mirroring helper.ResponseChunkData.
func writeOpenaiImageStreamChunk(c *gin.Context, data []byte) error {
	var payload struct {
		Type string `json:"type"`
	}
	_ = common.Unmarshal(data, &payload)
	if eventName := strings.TrimSpace(payload.Type); eventName != "" {
		return helper.ResponseChunkData(c, dto.ResponsesStreamResponse{Type: eventName}, string(data))
	}
	return helper.StringData(c, string(data))
}

// isOpenAIImageStreamErrorEvent detects upstream error chunks by JSON content
// only ("type" of error/upstream_error, or a non-empty "error" field). The SSE
// "event:" line is not available here: StreamScannerHandler delivers only the
// "data:" payload. A payload carrying just a "message" key is deliberately NOT
// treated as an error to avoid false positives.
func isOpenAIImageStreamErrorEvent(data []byte) bool {
	if !json.Valid(data) {
		return false
	}
	var payload struct {
		Type  string          `json:"type"`
		Error json.RawMessage `json:"error"`
	}
	if err := common.Unmarshal(data, &payload); err != nil {
		return false
	}
	payloadType := strings.ToLower(strings.TrimSpace(payload.Type))
	return payloadType == "error" || payloadType == "upstream_error" || len(payload.Error) > 0
}

func extractOpenAIImageStreamErrorMessage(data []byte) string {
	if len(data) == 0 || !json.Valid(data) {
		return "upstream image stream returned error event"
	}
	var payload struct {
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if err := common.Unmarshal(data, &payload); err != nil {
		return "upstream image stream returned error event"
	}
	if msg := strings.TrimSpace(payload.Message); msg != "" {
		return msg
	}
	if len(payload.Error) > 0 {
		var nested struct {
			Message string `json:"message"`
		}
		if err := common.Unmarshal(payload.Error, &nested); err == nil {
			if msg := strings.TrimSpace(nested.Message); msg != "" {
				return msg
			}
		}
		if msg := strings.TrimSpace(common.JsonRawMessageToString(payload.Error)); msg != "" {
			return msg
		}
	}
	return "upstream image stream returned error event"
}

func openaiImageJSONAsStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}

	responseBody = normalizeOpenAIImageResponseEnvelope(responseBody)
	responseBody = normalizeOpenAIImageContentBody(responseBody)
	if imageRequestWantsBase64(info) {
		responseBody, err = inlineOpenAIImageURLs(c.Request.Context(), responseBody)
		if err != nil {
			logger.LogError(c, fmt.Sprintf("failed to preserve generated image response: %v", err))
			return nil, types.NewOpenAIError(
				fmt.Errorf("generated image could not be preserved because the upstream image URL was unavailable or invalid"),
				types.ErrorCodeBadResponseBody,
				http.StatusBadGateway,
			)
		}
	}

	// Only decode usage/error. Do not Unmarshal data[] into dto.ImageResponse —
	// b64_json values are large and would be copied into Go strings then
	// re-marshaled for each SSE event.
	var usageResp dto.SimpleResponse
	if err := common.Unmarshal(responseBody, &usageResp); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := openAIImageResponseError(&usageResp); oaiError != nil {
		return nil, types.WithOpenAIError(*oaiError, normalizedUpstreamImageErrorStatus(resp.StatusCode))
	}
	if !hasOpenAIImagePayload(responseBody, imageRequestWantsBase64(info)) {
		shape := describeOpenAIImageResponseShape(responseBody)
		logger.LogError(c, "upstream image response has no deliverable image payload: "+shape)
		return nil, types.NewOpenAIError(
			fmt.Errorf("upstream image response did not contain a supported image payload (%s)", shape),
			types.ErrorCodeBadResponseBody,
			http.StatusBadGateway,
		)
	}
	normalizeOpenAIUsage(&usageResp.Usage)
	applyUsagePostProcessing(info, &usageResp.Usage, responseBody)

	imageCount := gjson.GetBytes(responseBody, "data.#").Int()
	updateOpenAIImageCount(info, imageCount)

	helper.SetEventStreamHeaders(c)
	c.Status(http.StatusOK)

	created := gjson.GetBytes(responseBody, "created").Int()
	if created == 0 {
		created = time.Now().Unix()
	}
	if info != nil {
		info.SetFirstResponseTime()
	}

	validUsage := service.ValidUsage(&usageResp.Usage)
	var usageJSON []byte
	if validUsage {
		usageJSON, err = common.Marshal(usageResp.Usage)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
	}

	for i := int64(0); i < imageCount; i++ {
		image := gjson.GetBytes(responseBody, "data."+strconv.FormatInt(i, 10))
		payload := []byte(`{"type":"image_generation.completed"}`)
		payload, err = sjson.SetBytes(payload, "created_at", created)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		if validUsage {
			payload, err = sjson.SetRawBytes(payload, "usage", usageJSON)
			if err != nil {
				return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
			}
		}
		// b64_json goes last: every sjson.Set* reallocates the whole payload,
		// so inserting the large blob after all small fields avoids re-copying
		// multi-MB buffers.
		for _, field := range []string{"url", "revised_prompt", "b64_json"} {
			value := image.Get(field)
			if value.Type != gjson.String || value.Raw == `""` {
				continue
			}
			raw := []byte(value.Raw)
			if value.Index > 0 {
				raw = responseBody[value.Index : value.Index+len(value.Raw)]
			}
			payload, err = sjson.SetRawBytes(payload, field, raw)
			if err != nil {
				return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
			}
		}
		if writeErr := helper.ResponseChunkData(c, dto.ResponsesStreamResponse{Type: "image_generation.completed"}, string(payload)); writeErr != nil {
			if info != nil && info.StreamStatus != nil {
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, writeErr)
			}
			return &usageResp.Usage, nil
		}
	}
	if err := writeOpenaiImageStreamDone(c); err != nil {
		if info != nil && info.StreamStatus != nil {
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, err)
		}
		return &usageResp.Usage, nil
	}
	if info != nil {
		info.ReceivedResponseCount += int(imageCount)
		if info.StreamStatus == nil {
			info.StreamStatus = relaycommon.NewStreamStatus()
		}
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	}
	return &usageResp.Usage, nil
}

// normalizeOpenAIImageContentBody replaces the provider-specific
// /v1/images/content/<token> wrapper with b64_json. The wrapper token is a
// base64url-encoded JSON envelope containing a data:image/... URI. Returning
// the bytes inline avoids exposing a multi-megabyte URL that browsers,
// reverse proxies, or an expiring upstream endpoint cannot reliably serve.
//
// Only this exact content endpoint shape and a data:image base64 payload are
// accepted; ordinary external URLs are left untouched.
func normalizeOpenAIImageContentBody(body []byte) []byte {
	if len(body) == 0 {
		return body
	}

	var envelope map[string]json.RawMessage
	if err := common.Unmarshal(body, &envelope); err != nil {
		return body
	}
	changed := false

	if rawData, ok := envelope["data"]; ok {
		var items []map[string]json.RawMessage
		if err := common.Unmarshal(rawData, &items); err == nil {
			for _, item := range items {
				if normalizeOpenAIImageContentItem(item) {
					changed = true
				}
			}
			if changed {
				if normalized, err := common.Marshal(items); err == nil {
					envelope["data"] = normalized
				}
			}
		}
	}

	// Image SSE providers may emit a single image object rather than a
	// top-level data array. Normalize that shape as well.
	if normalizeOpenAIImageContentItem(envelope) {
		changed = true
	}
	if !changed {
		return body
	}
	normalized, err := common.Marshal(envelope)
	if err != nil {
		return body
	}
	return normalized
}

// normalizeOpenAIImageResponseEnvelope keeps the relay strict enough to avoid
// walking arbitrary provider JSON while accepting a small set of common
// OpenAI-compatible wrappers and field aliases. It uses RawMessage so large
// base64 payloads are not decoded and re-encoded as Go strings.
func normalizeOpenAIImageResponseEnvelope(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	var envelope map[string]json.RawMessage
	if err := common.Unmarshal(body, &envelope); err != nil {
		return body
	}

	rawItems, found := envelope["data"]
	sourceRoot := "data"
	if !found {
		if images, ok := envelope["images"]; ok {
			rawItems, found = images, true
			sourceRoot = "images"
		} else {
			for _, path := range [][2]string{{"result", "data"}, {"result", "images"}, {"output", "data"}, {"output", "images"}, {"response", "data"}} {
				var wrapper map[string]json.RawMessage
				if rawWrapper, ok := envelope[path[0]]; ok && common.Unmarshal(rawWrapper, &wrapper) == nil {
					if nested, ok := wrapper[path[1]]; ok {
						rawItems, found = nested, true
						sourceRoot = path[0]
						break
					}
				}
			}
		}
	}
	if !found {
		return body
	}

	normalizedItems, changed := normalizeOpenAIImageItems(rawItems)
	if !changed && sourceRoot == "data" {
		return body
	}
	envelope["data"] = normalizedItems
	if sourceRoot != "data" {
		// Avoid duplicating a potentially multi-megabyte base64 payload in both
		// the provider wrapper and the canonical top-level data field.
		delete(envelope, sourceRoot)
	}
	normalized, err := common.Marshal(envelope)
	if err != nil {
		return body
	}
	return normalized
}

func normalizeOpenAIImageItems(rawItems json.RawMessage) (json.RawMessage, bool) {
	var rawValues []json.RawMessage
	if err := common.Unmarshal(rawItems, &rawValues); err != nil {
		return rawItems, false
	}
	items := make([]map[string]json.RawMessage, 0, len(rawValues))
	changed := false
	for _, rawValue := range rawValues {
		var item map[string]json.RawMessage
		if common.Unmarshal(rawValue, &item) != nil {
			var value string
			if common.Unmarshal(rawValue, &value) != nil || strings.TrimSpace(value) == "" {
				return rawItems, false
			}
			key := "b64_json"
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "https://") {
				key = "url"
			}
			encoded, err := common.Marshal(value)
			if err != nil {
				return rawItems, false
			}
			item = map[string]json.RawMessage{key: encoded}
			changed = true
		}
		if _, ok := item["b64_json"]; !ok {
			for _, alias := range []string{"base64", "image_base64"} {
				if value, exists := item[alias]; exists {
					item["b64_json"] = value
					delete(item, alias)
					changed = true
					break
				}
			}
		}
		if _, ok := item["url"]; !ok {
			if value, exists := item["image_url"]; exists {
				var nested map[string]json.RawMessage
				if common.Unmarshal(value, &nested) == nil && nested["url"] != nil {
					value = nested["url"]
				}
				item["url"] = value
				delete(item, "image_url")
				changed = true
			}
		}
		items = append(items, item)
	}
	normalized, err := common.Marshal(items)
	if err != nil {
		return rawItems, false
	}
	return normalized, changed
}

func openAIImageResponseError(response *dto.SimpleResponse) *types.OpenAIError {
	if response == nil {
		return nil
	}
	openAIError := response.GetOpenAIError()
	if openAIError == nil {
		return nil
	}
	if strings.TrimSpace(openAIError.Type) == "" && strings.TrimSpace(openAIError.Message) == "" && openAIError.Code == nil {
		return nil
	}
	return openAIError
}

func normalizedUpstreamImageErrorStatus(status int) int {
	if status >= http.StatusBadRequest {
		return status
	}
	return http.StatusBadGateway
}

func hasOpenAIImagePayload(body []byte, requireBase64 bool) bool {
	var envelope map[string]json.RawMessage
	if common.Unmarshal(body, &envelope) != nil {
		return false
	}
	var items []map[string]json.RawMessage
	if common.Unmarshal(envelope["data"], &items) != nil || len(items) == 0 {
		return false
	}
	for _, item := range items {
		if hasNonEmptyImageString(item["b64_json"]) {
			return true
		}
		if !requireBase64 && hasNonEmptyImageString(item["url"]) {
			return true
		}
	}
	return false
}

func hasNonEmptyImageString(raw json.RawMessage) bool {
	var value string
	return common.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != ""
}

func describeOpenAIImageResponseShape(body []byte) string {
	var envelope map[string]json.RawMessage
	if common.Unmarshal(body, &envelope) != nil {
		return "invalid_json"
	}
	topKeys := make([]string, 0, len(envelope))
	for key := range envelope {
		topKeys = append(topKeys, key)
	}
	sort.Strings(topKeys)
	itemKeys := []string{}
	var items []map[string]json.RawMessage
	if common.Unmarshal(envelope["data"], &items) == nil && len(items) > 0 {
		for key := range items[0] {
			itemKeys = append(itemKeys, key)
		}
		sort.Strings(itemKeys)
	}
	return fmt.Sprintf("top_keys=%v,data_count=%d,first_item_keys=%v", topKeys, len(items), itemKeys)
}

func normalizeOpenAIImageContentItem(item map[string]json.RawMessage) bool {
	rawURL, ok := item["url"]
	if !ok {
		return false
	}
	var imageURL string
	if err := common.Unmarshal(rawURL, &imageURL); err != nil {
		return false
	}
	b64, ok := decodeWrappedImageURL(imageURL)
	if !ok {
		return false
	}
	encoded, err := common.Marshal(b64)
	if err != nil {
		return false
	}
	item["b64_json"] = encoded
	delete(item, "url")
	return true
}

const (
	maxGeneratedImageFetchBytes int64 = 10 * 1024 * 1024
	maxGeneratedImageTotalBytes int64 = 24 * 1024 * 1024
	maxGeneratedImageURLCount         = 4
)

var fetchPublicGeneratedImage = service.FetchPublicGeneratedImage

func imageRequestWantsBase64(info *relaycommon.RelayInfo) bool {
	if info == nil {
		return false
	}
	request, ok := info.Request.(*dto.ImageRequest)
	return ok && request != nil && strings.EqualFold(strings.TrimSpace(request.ResponseFormat), "b64_json")
}

// inlineOpenAIImageURLs converts ordinary short-lived upstream image URLs to
// b64_json when the downstream explicitly requested b64_json. The fetch is
// bounded and SSRF-protected by service.FetchPublicGeneratedImage. Failing the
// response is intentional: reporting success with a dead URL loses a paid
// result and leaves the browser with nothing it can persist.
func inlineOpenAIImageURLs(ctx context.Context, body []byte) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}

	var envelope map[string]json.RawMessage
	if err := common.Unmarshal(body, &envelope); err != nil {
		return body, nil
	}
	changed := false
	fetchedCount := 0
	var totalBytes int64

	inlineItem := func(item map[string]json.RawMessage) error {
		if rawB64, ok := item["b64_json"]; ok {
			var existing string
			if common.Unmarshal(rawB64, &existing) == nil && strings.TrimSpace(existing) != "" {
				return nil
			}
		}
		rawURL, ok := item["url"]
		if !ok {
			return nil
		}
		var imageURL string
		if err := common.Unmarshal(rawURL, &imageURL); err != nil || strings.TrimSpace(imageURL) == "" {
			return nil
		}
		if fetchedCount >= maxGeneratedImageURLCount {
			return fmt.Errorf("upstream returned more than %d remote images", maxGeneratedImageURLCount)
		}
		encoded, err := fetchPublicGeneratedImage(ctx, imageURL, maxGeneratedImageFetchBytes)
		if err != nil {
			return fmt.Errorf("fetch upstream image %d: %w", fetchedCount+1, err)
		}
		decodedBytes := int64(base64.StdEncoding.DecodedLen(len(encoded)))
		if totalBytes+decodedBytes > maxGeneratedImageTotalBytes {
			return fmt.Errorf("generated image response exceeds the %d byte total limit", maxGeneratedImageTotalBytes)
		}
		totalBytes += decodedBytes
		fetchedCount++
		encodedJSON, err := common.Marshal(encoded)
		if err != nil {
			return fmt.Errorf("encode upstream image %d: %w", fetchedCount, err)
		}
		item["b64_json"] = encodedJSON
		delete(item, "url")
		changed = true
		return nil
	}

	if rawData, ok := envelope["data"]; ok {
		var items []map[string]json.RawMessage
		if err := common.Unmarshal(rawData, &items); err == nil {
			for _, item := range items {
				if err := inlineItem(item); err != nil {
					return nil, err
				}
			}
			if changed {
				normalized, err := common.Marshal(items)
				if err != nil {
					return nil, err
				}
				envelope["data"] = normalized
			}
		}
	}

	if err := inlineItem(envelope); err != nil {
		return nil, err
	}
	if !changed {
		return body, nil
	}
	normalized, err := common.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

func decodeWrappedImageURL(imageURL string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(imageURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	const marker = "/v1/images/content/"
	idx := strings.Index(parsed.Path, marker)
	if idx < 0 {
		return "", false
	}
	token := strings.Trim(strings.TrimPrefix(parsed.Path[idx+len(marker):], "/"), "/")
	if token == "" || strings.Contains(token, "/") {
		return "", false
	}
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	var envelope struct {
		Kind string `json:"kind"`
		URL  string `json:"url"`
	}
	if err := common.Unmarshal(payload, &envelope); err != nil || envelope.Kind != "upstream" {
		return "", false
	}
	dataURL := strings.TrimSpace(envelope.URL)
	if !strings.HasPrefix(dataURL, "data:image/") {
		return "", false
	}
	comma := strings.IndexByte(dataURL, ',')
	if comma <= 0 || !strings.Contains(strings.ToLower(dataURL[:comma]), ";base64") {
		return "", false
	}
	imagePayload := dataURL[comma+1:]
	decoded, err := base64.StdEncoding.DecodeString(imagePayload)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(imagePayload)
		if err != nil {
			return "", false
		}
	}
	return base64.StdEncoding.EncodeToString(decoded), true
}

func writeOpenaiImageStreamPayload(c *gin.Context, eventName string, payload any) error {
	data, err := common.Marshal(payload)
	if err != nil {
		return err
	}
	if eventName != "" {
		if _, err := fmt.Fprintf(c.Writer, "event: %s\n", eventName); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", data); err != nil {
		return err
	}
	return helper.FlushWriter(c)
}
func writeOpenaiImageStreamDone(c *gin.Context) error {
	return helper.StringData(c, "[DONE]")
}
