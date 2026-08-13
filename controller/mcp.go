package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	mcpProtocolVersion = "2025-06-18"
	mcpServerName      = "new-api-video"
	mcpImageServerName = "new-api-image"
)

type mcpToolProfile string

const (
	mcpToolProfileAll   mcpToolProfile = "all"
	mcpToolProfileImage mcpToolProfile = "image"
	mcpToolProfileVideo mcpToolProfile = "video"
)

var mcpInternalHandler http.Handler

type mcpRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string            `json:"jsonrpc"`
	ID      any               `json:"id,omitempty"`
	Result  any               `json:"result,omitempty"`
	Error   *mcpResponseError `json:"error,omitempty"`
}

type mcpResponseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type mcpCallToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type mcpContent struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Data        string `json:"data,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
	URI         string `json:"uri,omitempty"`
	Name        string `json:"name,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

type mcpToolResult struct {
	Content           []mcpContent   `json:"content"`
	StructuredContent map[string]any `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
}

type mcpCreateVideoArgs struct {
	Model                string   `json:"model,omitempty"`
	Prompt               string   `json:"prompt"`
	Duration             int      `json:"duration,omitempty"`
	Resolution           string   `json:"resolution,omitempty"`
	AspectRatio          string   `json:"aspect_ratio,omitempty"`
	Mode                 string   `json:"mode,omitempty"`
	Audio                *bool    `json:"audio,omitempty"`
	ReferenceImages      []string `json:"reference_images,omitempty"`
	ReferenceMaterialIDs []string `json:"reference_material_ids,omitempty"`
	StartImageURL        string   `json:"start_image_url,omitempty"`
	EndImageURL          string   `json:"end_image_url,omitempty"`
	StartMaterialID      string   `json:"start_material_id,omitempty"`
	EndMaterialID        string   `json:"end_material_id,omitempty"`
}

type mcpGetVideoArgs struct {
	TaskID string `json:"task_id"`
}

type mcpCreateImageArgs struct {
	Model                string   `json:"model"`
	Prompt               string   `json:"prompt"`
	N                    int      `json:"n,omitempty"`
	Size                 string   `json:"size,omitempty"`
	Quality              string   `json:"quality,omitempty"`
	ReferenceMaterialIDs []string `json:"reference_material_ids,omitempty"`
	ForceNew             bool     `json:"force_new,omitempty"`
}

type mcpCreateMaterialUploadArgs struct {
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

const mcpImageResultCacheTTL = 30 * time.Minute

var (
	storeMCPGeneratedImage = service.StoreGeneratedImage
	openMCPMaterialObject  = service.OpenMaterialObjectForUser
)

func SetMCPInternalHandler(handler http.Handler) {
	mcpInternalHandler = handler
}

func MCP(c *gin.Context) {
	handleMCP(c, mcpToolProfileAll)
}

func MCPImage(c *gin.Context) {
	handleMCP(c, mcpToolProfileImage)
}

func MCPVideo(c *gin.Context) {
	handleMCP(c, mcpToolProfileVideo)
}

func handleMCP(c *gin.Context, profile mcpToolProfile) {
	var request mcpRequest
	if err := common.UnmarshalBodyReusable(c, &request); err != nil {
		writeMCPError(c, nil, -32700, "invalid JSON-RPC request")
		return
	}
	if request.JSONRPC != "2.0" || strings.TrimSpace(request.Method) == "" {
		writeMCPError(c, request.ID, -32600, "invalid JSON-RPC request")
		return
	}

	if request.ID == nil {
		c.Status(http.StatusAccepted)
		return
	}

	switch request.Method {
	case "initialize":
		writeMCPResult(c, request.ID, map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{
					"listChanged": false,
				},
			},
			"serverInfo": map[string]any{
				"name":    mcpServerNameForProfile(profile),
				"version": common.Version,
			},
			"instructions": mcpInstructionsForProfile(profile),
		})
	case "ping":
		writeMCPResult(c, request.ID, map[string]any{})
	case "tools/list":
		writeMCPResult(c, request.ID, map[string]any{
			"tools": mcpToolsForProfile(profile),
		})
	case "tools/call":
		handleMCPToolCall(c, request, profile)
	default:
		writeMCPError(c, request.ID, -32601, "method not found")
	}
}

func handleMCPToolCall(c *gin.Context, request mcpRequest, profile mcpToolProfile) {
	paramsBytes, err := common.Marshal(request.Params)
	if err != nil {
		writeMCPError(c, request.ID, -32602, "invalid tool parameters")
		return
	}
	var params mcpCallToolParams
	if err := common.Unmarshal(paramsBytes, &params); err != nil {
		writeMCPError(c, request.ID, -32602, "invalid tool parameters")
		return
	}
	if !mcpToolAllowed(profile, params.Name) {
		writeMCPError(c, request.ID, -32602, "tool is not available on this MCP endpoint")
		return
	}

	var result mcpToolResult
	switch params.Name {
	case "create_image":
		result = callCreateImageTool(c, params.Arguments)
	case "create_material_upload":
		result = callCreateMaterialUploadTool(c, params.Arguments)
	case "create_video":
		result = callCreateVideoTool(c, params.Arguments)
	case "get_video":
		result = callGetVideoTool(c, params.Arguments)
	default:
		writeMCPError(c, request.ID, -32602, "unknown tool")
		return
	}
	writeMCPResult(c, request.ID, result)
}

func callCreateMaterialUploadTool(c *gin.Context, arguments map[string]any) mcpToolResult {
	var args mcpCreateMaterialUploadArgs
	if err := decodeMCPArguments(arguments, &args); err != nil {
		return newMCPToolError("invalid create_material_upload arguments: " + err.Error())
	}
	args.FileName = strings.TrimSpace(args.FileName)
	args.ContentType = strings.TrimSpace(args.ContentType)
	if args.FileName == "" {
		return newMCPToolError("file_name is required")
	}
	if args.ContentType == "" {
		return newMCPToolError("content_type is required")
	}
	if args.SizeBytes <= 0 {
		return newMCPToolError("size_bytes must be greater than zero")
	}
	return callInternalAPI(c, http.MethodPost, "/v1/materials/uploads", map[string]any{
		"file_name":    args.FileName,
		"content_type": args.ContentType,
		"size_bytes":   args.SizeBytes,
	})
}

func callCreateImageTool(c *gin.Context, arguments map[string]any) mcpToolResult {
	var args mcpCreateImageArgs
	if err := decodeMCPArguments(arguments, &args); err != nil {
		return newMCPToolError("invalid create_image arguments: " + err.Error())
	}
	args.Model = strings.TrimSpace(args.Model)
	args.Prompt = strings.TrimSpace(args.Prompt)
	if args.Model == "" {
		return newMCPToolError("model is required")
	}
	if args.Prompt == "" {
		return newMCPToolError("prompt is required")
	}
	if args.N == 0 {
		args.N = 1
	}
	if args.N < 1 || args.N > 4 {
		return newMCPToolError("n must be between 1 and 4")
	}
	if len(args.ReferenceMaterialIDs) > 3 {
		return newMCPToolError("reference_material_ids supports at most 3 images")
	}
	args.ReferenceMaterialIDs = normalizeMCPMaterialIDs(args.ReferenceMaterialIDs)

	requestHash := mcpImageRequestHash(c.GetInt("id"), args)
	if !args.ForceNew {
		if cached, ok := getCachedMCPImageResult(requestHash); ok {
			markMCPImageResultCached(&cached, requestHash)
			return cached
		}
	}
	releaseInflight, acquired := acquireMCPImageRequest(requestHash)
	if !acquired {
		return mcpToolResult{
			Content: []mcpContent{{
				Type: "text",
				Text: "An identical image request is already running. Do not call create_image again. Wait for the original tool call to finish; no new provider request was made.",
			}},
			StructuredContent: map[string]any{
				"status":                     "IN_PROGRESS",
				"request_hash":               requestHash,
				"deduplicated":               true,
				"provider_request_performed": false,
				"must_not_retry":             true,
			},
		}
	}
	defer releaseInflight()

	payload := map[string]any{
		"model":           args.Model,
		"prompt":          args.Prompt,
		"n":               args.N,
		"response_format": "b64_json",
	}
	if size := strings.TrimSpace(args.Size); size != "" && size != "auto" {
		payload["size"] = size
	}
	if quality := strings.TrimSpace(args.Quality); quality != "" && quality != "auto" {
		payload["quality"] = quality
	}
	var result mcpToolResult
	if len(args.ReferenceMaterialIDs) == 0 {
		result = callInternalAPI(c, http.MethodPost, "/v1/images/generations", payload)
	} else {
		result = callImageEditInternalAPI(c, args, payload)
	}
	if result.IsError {
		return result
	}
	result = newMCPImageResult(c.Request.Context(), c.GetInt("id"), result.StructuredContent)
	result.StructuredContent["request_hash"] = requestHash
	result.StructuredContent["deduplicated"] = false
	result.StructuredContent["provider_request_performed"] = true
	result.StructuredContent["must_not_retry"] = true
	cacheMCPImageResult(requestHash, result)
	return result
}

func newMCPImageResult(ctx context.Context, userID int, structured map[string]any) mcpToolResult {
	responseData, ok := structured["data"].([]any)
	if !ok || len(responseData) == 0 {
		return newMCPImageDeliveryFailure("The provider accepted the request but did not return data[].b64_json. Do not submit the same request again automatically; ask the user or administrator to inspect the provider response.")
	}

	contents := []mcpContent{
		{
			Type: "text",
			Text: "Image generation succeeded. A compact preview and a temporary original-file link are returned below. Do not call create_image again to display, save, decode, or recover this result; use the existing download_url instead.",
		},
	}
	imageMetadata := make([]map[string]any, 0, len(responseData))
	for _, rawItem := range responseData {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		encoded, ok := item["b64_json"].(string)
		if !ok {
			continue
		}
		imageData, mimeType, ok := normalizeMCPImageData(encoded)
		if !ok {
			continue
		}
		original, err := base64.StdEncoding.DecodeString(imageData)
		if err != nil || len(original) == 0 {
			continue
		}
		metadata := map[string]any{
			"index":     len(imageMetadata) + 1,
			"mime_type": mimeType,
		}
		asset, storeErr := storeMCPGeneratedImage(ctx, userID, original, mimeType)
		if storeErr == nil && asset != nil && strings.TrimSpace(asset.URL) != "" {
			metadata["download_url"] = asset.URL
			metadata["expires_at"] = asset.ExpiresAt
			contents = append(contents, mcpContent{
				Type: "text",
				Text: fmt.Sprintf(
					"Original image %d download_url (expires at %s): %s\nTo save the image, download this exact URL. Do not call create_image again.",
					len(imageMetadata)+1,
					time.Unix(asset.ExpiresAt, 0).Format(time.RFC3339),
					asset.URL,
				),
			})
			contents = append(contents, mcpContent{
				Type:        "resource_link",
				URI:         asset.URL,
				Name:        fmt.Sprintf("generated-image-%d", len(imageMetadata)+1),
				Title:       fmt.Sprintf("Generated image %d (original)", len(imageMetadata)+1),
				Description: "Temporary signed original-file URL. Download or open this resource; never regenerate solely because a preview or save step failed.",
				MimeType:    mimeType,
			})
		} else if storeErr != nil {
			metadata["delivery_warning"] = storeErr.Error()
		}
		previewData, previewMimeType, previewErr := makeMCPImagePreview(original)
		if previewErr == nil {
			contents = append(contents, mcpContent{
				Type:     "image",
				Data:     previewData,
				MimeType: previewMimeType,
			})
			metadata["preview_mime_type"] = previewMimeType
		} else if _, hasURL := metadata["download_url"]; !hasURL {
			// Last-resort delivery when OSS is unavailable. This may be large, but
			// it still avoids charging for a second provider request.
			contents = append(contents, mcpContent{Type: "image", Data: imageData, MimeType: mimeType})
			metadata["preview_mime_type"] = mimeType
		}
		if revisedPrompt, ok := item["revised_prompt"].(string); ok && strings.TrimSpace(revisedPrompt) != "" {
			metadata["revised_prompt"] = revisedPrompt
		}
		imageMetadata = append(imageMetadata, metadata)
	}
	if len(imageMetadata) == 0 {
		return newMCPImageDeliveryFailure("The provider returned image entries, but none contained valid data[].b64_json. Do not resubmit automatically; ask the user or administrator to inspect the response.")
	}

	compact := map[string]any{
		"status": "SUCCESS",
		"count":  len(imageMetadata),
		"images": imageMetadata,
	}
	if len(imageMetadata) == 1 {
		if downloadURL, ok := imageMetadata[0]["download_url"]; ok {
			compact["download_url"] = downloadURL
		}
		if expiresAt, ok := imageMetadata[0]["expires_at"]; ok {
			compact["expires_at"] = expiresAt
		}
	}
	if created, ok := structured["created"]; ok {
		compact["created"] = created
	}
	return mcpToolResult{
		Content:           contents,
		StructuredContent: compact,
	}
}

func newMCPImageDeliveryFailure(message string) mcpToolResult {
	return mcpToolResult{
		Content: []mcpContent{{Type: "text", Text: message}},
		StructuredContent: map[string]any{
			"status":         "DELIVERY_FAILURE",
			"must_not_retry": true,
		},
	}
}

func makeMCPImagePreview(original []byte) (string, string, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(original))
	if err != nil {
		return "", "", err
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		return "", "", fmt.Errorf("image dimensions exceed the preview safety limit")
	}
	source, _, err := image.Decode(bytes.NewReader(original))
	if err != nil {
		return "", "", err
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return "", "", fmt.Errorf("invalid image dimensions")
	}
	const maxDimension = 640
	if width > maxDimension || height > maxDimension {
		ratio := float64(maxDimension) / float64(width)
		if height > width {
			ratio = float64(maxDimension) / float64(height)
		}
		width = max(1, int(float64(width)*ratio))
		height = max(1, int(float64(height)*ratio))
	}
	destination := image.NewRGBA(image.Rect(0, 0, width, height))
	stddraw.Draw(destination, destination.Bounds(), &image.Uniform{C: color.White}, image.Point{}, stddraw.Src)
	xdraw.CatmullRom.Scale(destination, destination.Bounds(), source, bounds, stddraw.Over, nil)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, destination, &jpeg.Options{Quality: 78}); err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(encoded.Bytes()), "image/jpeg", nil
}

func normalizeMCPMaterialIDs(materialIDs []string) []string {
	normalized := make([]string, 0, len(materialIDs))
	seen := make(map[string]struct{}, len(materialIDs))
	for _, materialID := range materialIDs {
		materialID = strings.TrimSpace(materialID)
		if materialID == "" {
			continue
		}
		if _, exists := seen[materialID]; exists {
			continue
		}
		seen[materialID] = struct{}{}
		normalized = append(normalized, materialID)
	}
	return normalized
}

func mcpImageRequestHash(userID int, args mcpCreateImageArgs) string {
	args.ForceNew = false
	encoded, _ := common.Marshal(struct {
		UserID int                `json:"user_id"`
		Args   mcpCreateImageArgs `json:"args"`
	}{UserID: userID, Args: args})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func mcpImageCacheKey(requestHash string) string {
	return "mcp:image:result:" + requestHash
}

func mcpImageInflightKey(requestHash string) string {
	return "mcp:image:inflight:" + requestHash
}

func getCachedMCPImageResult(requestHash string) (mcpToolResult, bool) {
	if !common.RedisEnabled || common.RDB == nil || requestHash == "" {
		return mcpToolResult{}, false
	}
	cached, err := common.RedisGet(mcpImageCacheKey(requestHash))
	if err != nil || cached == "" {
		return mcpToolResult{}, false
	}
	var result mcpToolResult
	if err := common.Unmarshal([]byte(cached), &result); err != nil || len(result.Content) == 0 {
		return mcpToolResult{}, false
	}
	return result, true
}

func cacheMCPImageResult(requestHash string, result mcpToolResult) {
	if !common.RedisEnabled || common.RDB == nil || requestHash == "" || result.IsError {
		return
	}
	encoded, err := common.Marshal(result)
	if err != nil {
		return
	}
	_ = common.RedisSet(mcpImageCacheKey(requestHash), string(encoded), mcpImageResultCacheTTL)
}

func markMCPImageResultCached(result *mcpToolResult, requestHash string) {
	if result.StructuredContent == nil {
		result.StructuredContent = map[string]any{}
	}
	result.StructuredContent["request_hash"] = requestHash
	result.StructuredContent["deduplicated"] = true
	result.StructuredContent["provider_request_performed"] = false
	result.StructuredContent["must_not_retry"] = true
	result.Content = append([]mcpContent{{
		Type: "text",
		Text: "This is the cached result of an identical recent request. No new provider request was made and no new generation charge was incurred. Use the existing preview or download_url; do not call create_image again for delivery or saving problems.",
	}}, result.Content...)
}

func acquireMCPImageRequest(requestHash string) (func(), bool) {
	if !common.RedisEnabled || common.RDB == nil || requestHash == "" {
		return func() {}, true
	}
	key := mcpImageInflightKey(requestHash)
	acquired, err := common.RDB.SetNX(context.Background(), key, "1", 10*time.Minute).Result()
	if err != nil {
		return func() {}, true
	}
	return func() { _ = common.RedisDel(key) }, acquired
}

func callImageEditInternalAPI(c *gin.Context, args mcpCreateImageArgs, payload map[string]any) mcpToolResult {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for field, rawValue := range payload {
		value := fmt.Sprint(rawValue)
		if err := writer.WriteField(field, value); err != nil {
			return newMCPToolError("failed to prepare image edit request")
		}
	}
	userID := c.GetInt("id")
	for index, materialID := range args.ReferenceMaterialIDs {
		material, err := openMCPMaterialObject(c.Request.Context(), userID, materialID)
		if err != nil {
			return newMCPToolError(fmt.Sprintf("reference_material_ids[%d]: %s", index, err.Error()))
		}
		fieldName := "image"
		if len(args.ReferenceMaterialIDs) > 1 {
			fieldName = "image[]"
		}
		part, createErr := writer.CreateFormFile(fieldName, material.FileName)
		if createErr == nil {
			_, createErr = io.Copy(part, material.Body)
		}
		_ = material.Body.Close()
		if createErr != nil {
			return newMCPToolError("failed to read a reference image")
		}
	}
	if err := writer.Close(); err != nil {
		return newMCPToolError("failed to finalize image edit request")
	}
	return callInternalRequest(c, http.MethodPost, "/v1/images/edits", writer.FormDataContentType(), bytes.NewReader(body.Bytes()))
}

func normalizeMCPImageData(encoded string) (string, string, bool) {
	imageData := strings.TrimSpace(encoded)
	mimeType := ""
	if strings.HasPrefix(strings.ToLower(imageData), "data:") {
		comma := strings.IndexByte(imageData, ',')
		if comma <= len("data:") {
			return "", "", false
		}
		header := imageData[len("data:"):comma]
		if semicolon := strings.IndexByte(header, ';'); semicolon >= 0 {
			header = header[:semicolon]
		}
		if strings.HasPrefix(strings.ToLower(header), "image/") {
			mimeType = strings.ToLower(header)
		}
		imageData = strings.TrimSpace(imageData[comma+1:])
	}
	if imageData == "" {
		return "", "", false
	}

	prefixLength := len(imageData)
	if prefixLength > 256 {
		prefixLength = 256
	}
	prefixLength -= prefixLength % 4
	if prefixLength == 0 {
		return "", "", false
	}
	sample, err := base64.StdEncoding.DecodeString(imageData[:prefixLength])
	if err != nil || len(sample) == 0 {
		return "", "", false
	}
	if detected := http.DetectContentType(sample); strings.HasPrefix(detected, "image/") {
		mimeType = detected
	}
	if mimeType == "" {
		mimeType = "image/png"
	}
	return imageData, mimeType, true
}

func callCreateVideoTool(c *gin.Context, arguments map[string]any) mcpToolResult {
	var args mcpCreateVideoArgs
	if err := decodeMCPArguments(arguments, &args); err != nil {
		return newMCPToolError("invalid create_video arguments: " + err.Error())
	}
	args.Prompt = strings.TrimSpace(args.Prompt)
	if args.Prompt == "" {
		return newMCPToolError("prompt is required")
	}
	if args.Model == "" {
		args.Model = "cheap-seedance-2.0-fast"
	}
	if args.Duration == 0 {
		args.Duration = 5
	}

	payload := map[string]any{
		"model":                  args.Model,
		"prompt":                 args.Prompt,
		"duration":               args.Duration,
		"reference_images":       args.ReferenceImages,
		"reference_material_ids": args.ReferenceMaterialIDs,
		"start_image_url":        strings.TrimSpace(args.StartImageURL),
		"end_image_url":          strings.TrimSpace(args.EndImageURL),
		"start_material_id":      strings.TrimSpace(args.StartMaterialID),
		"end_material_id":        strings.TrimSpace(args.EndMaterialID),
	}
	if args.Resolution != "" {
		payload["resolution"] = args.Resolution
	}
	if args.AspectRatio != "" {
		payload["aspect_ratio"] = args.AspectRatio
	}
	if args.Mode != "" {
		payload["mode"] = args.Mode
	}
	if args.Audio != nil {
		payload["audio"] = *args.Audio
	}
	return callInternalAPI(c, http.MethodPost, "/v1/video/generations", payload)
}

func callGetVideoTool(c *gin.Context, arguments map[string]any) mcpToolResult {
	var args mcpGetVideoArgs
	if err := decodeMCPArguments(arguments, &args); err != nil {
		return newMCPToolError("invalid get_video arguments: " + err.Error())
	}
	args.TaskID = strings.TrimSpace(args.TaskID)
	if args.TaskID == "" {
		return newMCPToolError("task_id is required")
	}
	return callInternalAPI(
		c,
		http.MethodGet,
		"/v1/video/generations/"+url.PathEscape(args.TaskID),
		nil,
	)
}

func callInternalAPI(c *gin.Context, method, requestPath string, payload map[string]any) mcpToolResult {
	if mcpInternalHandler == nil {
		return newMCPToolError("internal API is unavailable")
	}

	var body io.Reader
	if payload != nil {
		requestBody, err := common.Marshal(payload)
		if err != nil {
			return newMCPToolError("failed to encode request")
		}
		body = bytes.NewReader(requestBody)
	}
	return callInternalRequest(c, method, requestPath, "application/json", body)
}

func callInternalRequest(c *gin.Context, method, requestPath, contentType string, body io.Reader) mcpToolResult {
	if mcpInternalHandler == nil {
		return newMCPToolError("internal API is unavailable")
	}
	request := httptest.NewRequest(method, requestPath, body)
	request.Header.Set("Authorization", c.GetHeader("Authorization"))
	request.Header.Set("Content-Type", contentType)
	if userHeader := c.GetHeader("New-Api-User"); userHeader != "" {
		request.Header.Set("New-Api-User", userHeader)
	}

	recorder := httptest.NewRecorder()
	mcpInternalHandler.ServeHTTP(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return newMCPToolError("failed to read internal API response")
	}
	if response.StatusCode >= http.StatusBadRequest {
		return newMCPToolError(fmt.Sprintf(
			"internal API returned HTTP %d: %s",
			response.StatusCode,
			strings.TrimSpace(string(responseBody)),
		))
	}

	var structured map[string]any
	if err := common.Unmarshal(responseBody, &structured); err != nil {
		return newMCPToolError("internal API returned an invalid response")
	}
	textBody, err := common.Marshal(structured)
	if err != nil {
		return newMCPToolError("failed to encode tool result")
	}
	return mcpToolResult{
		Content: []mcpContent{
			{Type: "text", Text: string(textBody)},
		},
		StructuredContent: structured,
	}
}

func decodeMCPArguments(arguments map[string]any, target any) error {
	encoded, err := common.Marshal(arguments)
	if err != nil {
		return err
	}
	return common.Unmarshal(encoded, target)
}

func newMCPToolError(message string) mcpToolResult {
	return mcpToolResult{
		Content: []mcpContent{
			{Type: "text", Text: message},
		},
		IsError: true,
	}
}

func writeMCPResult(c *gin.Context, id any, result any) {
	c.Header("Content-Type", "application/json")
	c.JSON(http.StatusOK, mcpResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
}

func writeMCPError(c *gin.Context, id any, code int, message string) {
	c.Header("Content-Type", "application/json")
	c.JSON(http.StatusOK, mcpResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &mcpResponseError{
			Code:    code,
			Message: message,
		},
	})
}

func mediaMCPTools() []mcpTool {
	stringSchema := func(description string) map[string]any {
		return map[string]any{
			"type":        "string",
			"description": description,
		}
	}
	return []mcpTool{
		{
			Name:        "create_image",
			Description: "Generate or edit images through a configured image model. Returns a compact native preview plus a temporary original-file download URL. A recent identical request is deduplicated to prevent repeated provider charges. Never call again merely because display, decode, save, or export failed.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model":  stringSchema("Exact image model name configured in New API."),
					"prompt": stringSchema("Description of the image to generate."),
					"n": map[string]any{
						"type":        "integer",
						"description": "Number of images. Defaults to 1.",
						"minimum":     1,
						"maximum":     4,
					},
					"size":    stringSchema("Optional provider-supported size, for example 1024x1024."),
					"quality": stringSchema("Optional provider-supported quality, for example standard, hd, low, medium, or high."),
					"reference_material_ids": map[string]any{
						"type":        "array",
						"description": "Up to 3 temporary local reference-image IDs returned by create_material_upload. When provided, create_image uses the image-edit endpoint.",
						"items":       stringSchema("Temporary material ID."),
						"maxItems":    3,
					},
					"force_new": map[string]any{
						"type":        "boolean",
						"description": "Request a deliberately new variation even when all other parameters are identical. Use only when the user explicitly asks for another variation. Never use to recover from preview, download, display, decoding, or saving problems.",
					},
				},
				"required":             []string{"model", "prompt"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "create_material_upload",
			Description: "Create a short-lived direct OSS upload for a local reference image. Upload the exact bytes with HTTP PUT to upload_url using every returned header, then pass material_id to create_image (up to 3 references) or create_video. The New API server does not proxy the upload bytes.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_name":    stringSchema("Original local file name, including .jpg, .png, or .webp."),
					"content_type": stringSchema("Exact MIME type: image/jpeg, image/png, or image/webp."),
					"size_bytes": map[string]any{
						"type":        "integer",
						"description": "Exact local file size in bytes. Defaults are limited to 10 MiB.",
						"minimum":     1,
					},
				},
				"required":             []string{"file_name", "content_type", "size_bytes"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "create_video",
			Description: "Create an asynchronous video generation task using the exact model configured in the user's New API channels. The result returns a task_id; use get_video to poll it.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model": map[string]any{
						"type":        "string",
						"description": "Exact video model ID configured in the user's New API channels, including Seedance or Kling V3 models. Use the model ID exposed by the video page; do not invent or replace aliases. Defaults to cheap-seedance-2.0-fast only for backward compatibility when omitted.",
					},
					"prompt": stringSchema("Video prompt, up to 1300 characters."),
					"duration": map[string]any{
						"type":        "integer",
						"description": "Output duration in seconds. Defaults to 5.",
						"minimum":     3,
						"maximum":     15,
					},
					"resolution": map[string]any{
						"type":        "string",
						"description": "Output resolution. Seedance fast/mini support only 480p and 720p; Kling supports 720p, 1080p, and 4K. Omit to use the selected model's default.",
						"enum":        []string{"480p", "720p", "1080p", "4K"},
					},
					"aspect_ratio": map[string]any{
						"type":        "string",
						"description": "Output aspect ratio. Omit to use the selected model's default (Seedance 9:16, Kling 16:9).",
						"enum":        []string{"16:9", "9:16", "1:1", "4:3", "3:4", "21:9"},
					},
					"mode": map[string]any{
						"type":        "string",
						"description": "Seedance uses text_with_reference or start_end_frame; Kling uses std, pro, or 4k. Omit to derive the mode from resolution. start_end_frame requires both start and end frames for Seedance.",
						"enum":        []string{"text_with_reference", "start_end_frame", "std", "pro", "4k"},
					},
					"audio": map[string]any{
						"type":        "boolean",
						"description": "Generate audio. Omit to use the provider adapter default (Seedance on, Kling off).",
					},
					"reference_images": map[string]any{
						"type":        "array",
						"description": "Public HTTP/HTTPS reference asset URLs. Prefixes reference:, start:, and end: are supported; MP4 URLs are treated as reference videos for kling-v3-omni.",
						"items":       stringSchema("Public reference image or video URL."),
					},
					"reference_material_ids": map[string]any{
						"type":        "array",
						"description": "Temporary material IDs returned by create_material_upload for reference images. Preserve their order. For Seedance, reference them in the prompt as @image1, @image2, and so on.",
						"items":       stringSchema("Temporary material ID."),
					},
					"start_image_url":   stringSchema("Public URL for the start frame image."),
					"end_image_url":     stringSchema("Public URL for the end frame image."),
					"start_material_id": stringSchema("Temporary material ID for the start frame."),
					"end_material_id":   stringSchema("Temporary material ID for the end frame."),
				},
				"required":             []string{"prompt"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "get_video",
			Description: "Get a video task by task_id. Poll until status is SUCCESS or FAILURE. On success, data.result_url is the signed video URL.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": stringSchema("Public task ID returned by create_video."),
				},
				"required":             []string{"task_id"},
				"additionalProperties": false,
			},
		},
	}
}

func mcpServerNameForProfile(profile mcpToolProfile) string {
	if profile == mcpToolProfileImage {
		return mcpImageServerName
	}
	return mcpServerName
}

func mcpInstructionsForProfile(profile mcpToolProfile) string {
	switch profile {
	case mcpToolProfileImage:
		return "Use create_image with the exact image model ID exposed by the user's New API drawing channels. Authenticate with a New API user token that can route to the drawing group; do not ask for OPENAI_API_KEY. For local reference images, call create_material_upload, PUT the exact bytes with every signed header, then pass up to 3 returned material IDs as reference_material_ids. Each successful result includes a compact preview and temporary original download_url. If preview, decoding, download, display, or saving fails, do not call create_image again: use the existing download_url or report the delivery error. Identical recent calls are deduplicated; set force_new only when the user explicitly asks for a new variation."
	case mcpToolProfileVideo:
		return "Use the exact video model IDs exposed by the user's New API video channels. Authenticate with a New API user token that can route to the video group. For a local reference image, call create_material_upload, upload the exact file bytes with HTTP PUT using every returned signed header, then pass the returned material_id to create_video. Poll get_video until SUCCESS or FAILURE."
	default:
		return "Use the exact model IDs exposed by the user's New API channels. Authenticate with a New API user token whose group can route to the requested media model; do not ask for OPENAI_API_KEY. create_image returns a compact preview and temporary original download_url; never regenerate because display, decode, download, or saving failed, and use force_new only for an explicitly requested new variation. For a local reference image, call create_material_upload, PUT the exact bytes with every signed header, then pass material IDs to create_image or create_video. Poll get_video until SUCCESS or FAILURE."
	}
}

func mcpToolsForProfile(profile mcpToolProfile) []mcpTool {
	tools := mediaMCPTools()
	if profile == mcpToolProfileAll {
		return tools
	}
	filtered := make([]mcpTool, 0, len(tools))
	for _, tool := range tools {
		if mcpToolAllowed(profile, tool.Name) {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func mcpToolAllowed(profile mcpToolProfile, name string) bool {
	switch profile {
	case mcpToolProfileImage:
		return name == "create_image" || name == "create_material_upload"
	case mcpToolProfileVideo:
		return name == "create_video" || name == "get_video" || name == "create_material_upload"
	default:
		return name == "create_image" || name == "create_video" || name == "get_video" || name == "create_material_upload"
	}
}
