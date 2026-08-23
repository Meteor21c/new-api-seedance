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
	"sort"
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

var mcpGPTImage2ProSizes = []string{
	"auto",
	"1024x1024",
	"1024x1536",
	"1536x1024",
	"1024x1792",
	"1792x1024",
}

var (
	storeMCPGeneratedImage = service.StoreGeneratedImage
	openMCPMaterialObject  = service.OpenMaterialObjectForUser
	fetchMCPGeneratedImage = service.FetchPublicGeneratedImage
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
	normalizeMCPImageCompatibility(&args)
	if validationError := validateMCPImageArguments(args); validationError != nil {
		return mcpToolResult{
			Content: []mcpContent{{Type: "text", Text: validationError.Error()}},
			StructuredContent: map[string]any{
				"status":          "INVALID_ARGUMENT",
				"parameter":       "size",
				"model":           args.Model,
				"supported_sizes": mcpGPTImage2ProSizes,
				"auto_behavior":   "omit size and let the upstream choose the output canvas; it does not preserve the reference image dimensions",
			},
			IsError: true,
		}
	}

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
				"internal_request_performed": false,
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
		failure := mcpToolResult{
			Content: append(result.Content, mcpContent{
				Type: "text",
				Text: "The image request did not produce a deliverable result. Do not automatically submit the same request again because the provider outcome may be unknown. Report this result and request_hash to the user or administrator.",
			}),
			StructuredContent: map[string]any{
				"status":                     "REQUEST_FAILURE",
				"request_hash":               requestHash,
				"deduplicated":               false,
				"internal_request_performed": true,
				"provider_outcome":           "unknown",
				"must_not_retry":             true,
			},
		}
		cacheMCPImageResult(requestHash, failure)
		return failure
	}
	result = newMCPImageResult(c.Request.Context(), c.GetInt("id"), result.StructuredContent)
	result.StructuredContent["request_hash"] = requestHash
	result.StructuredContent["deduplicated"] = false
	result.StructuredContent["internal_request_performed"] = true
	if result.StructuredContent["status"] == "SUCCESS" {
		result.StructuredContent["provider_request_performed"] = true
		result.StructuredContent["provider_outcome"] = "success"
	} else {
		result.StructuredContent["provider_outcome"] = "unknown"
	}
	result.StructuredContent["must_not_retry"] = true
	cacheMCPImageResult(requestHash, result)
	return result
}

func newMCPImageResult(ctx context.Context, userID int, structured map[string]any) mcpToolResult {
	responseData, responsePath := findMCPImageResponseData(structured)
	responseShape := describeMCPImageResponseShape(structured, responseData, responsePath)
	if len(responseData) == 0 {
		return newMCPImageDeliveryFailure(
			"The internal image request completed, but its response did not contain a supported image payload. The provider outcome and billing state are unknown. Do not submit the same request again automatically; inspect response_shape first.",
			responseShape,
		)
	}

	deliveryContents := []mcpContent{
		{
			Type: "text",
			Text: "Image generation succeeded and the provider request is complete. Native MCP image content blocks in this result are the authoritative inline preview. Original-file links are download fallbacks only. Do not call create_image again to display, save, decode, recover, or re-deliver this result.",
		},
	}
	imageContents := make([]mcpContent, 0, len(responseData))
	imageMetadata := make([]map[string]any, 0, len(responseData))
	finalResponseMarkdown := make([]string, 0, len(responseData))
	var fetchedBytes int64
	for _, rawItem := range responseData {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		encoded, source, fetchErr := resolveMCPImageData(ctx, item, &fetchedBytes)
		if fetchErr != nil {
			responseShape["delivery_error"] = fetchErr.Error()
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
			"source":    strings.TrimSuffix(source, "_json"),
		}
		previewData, previewMimeType, previewErr := makeMCPImagePreview(original)
		var previewBytes []byte
		if previewErr == nil {
			previewBytes, previewErr = base64.StdEncoding.DecodeString(previewData)
		}
		asset, storeErr := storeMCPGeneratedImage(ctx, userID, original, mimeType, previewBytes, previewMimeType)
		if storeErr == nil && asset != nil && strings.TrimSpace(asset.URL) != "" {
			metadata["download_url"] = asset.URL
			metadata["expires_at"] = asset.ExpiresAt
			inlineURL := asset.URL
			if strings.TrimSpace(asset.PreviewURL) != "" {
				inlineURL = asset.PreviewURL
				metadata["preview_url"] = asset.PreviewURL
			}
			if strings.TrimSpace(asset.PreviewWarning) != "" {
				metadata["preview_warning"] = asset.PreviewWarning
			}
			imageNumber := len(imageMetadata) + 1
			finalResponseMarkdown = append(finalResponseMarkdown, fmt.Sprintf(
				"![Generated image %d](%s)\n\n[Open or download original image %d](%s)",
				imageNumber,
				inlineURL,
				imageNumber,
				asset.URL,
			))
			deliveryContents = append(deliveryContents, mcpContent{
				Type: "text",
				Text: fmt.Sprintf(
					"Original image %d (available until %s):\n[Open or download original image %d](%s)\nUse this existing link. Do not call create_image again.",
					imageNumber,
					time.Unix(asset.ExpiresAt, 0).Format(time.RFC3339),
					imageNumber,
					asset.URL,
				),
			})
			deliveryContents = append(deliveryContents, mcpContent{
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
		if previewErr == nil {
			imageContents = append(imageContents, mcpContent{
				Type:     "image",
				Data:     previewData,
				MimeType: previewMimeType,
			})
			metadata["preview_mime_type"] = previewMimeType
		} else if _, hasURL := metadata["download_url"]; !hasURL {
			// Last-resort delivery when OSS is unavailable. This may be large, but
			// it still avoids charging for a second provider request.
			imageContents = append(imageContents, mcpContent{Type: "image", Data: imageData, MimeType: mimeType})
			metadata["preview_mime_type"] = mimeType
		}
		if revisedPrompt, ok := item["revised_prompt"].(string); ok && strings.TrimSpace(revisedPrompt) != "" {
			metadata["revised_prompt"] = revisedPrompt
		}
		imageMetadata = append(imageMetadata, metadata)
	}
	if len(imageMetadata) == 0 {
		return newMCPImageDeliveryFailure(
			"The internal image request returned image entries, but none could be delivered as valid image data. The provider outcome and billing state are unknown. Do not resubmit automatically; inspect response_shape first.",
			responseShape,
		)
	}
	// Native MCP image blocks must be first. Codex renders these blocks directly
	// from their base64 payload; putting explanatory text or a remote Markdown
	// image first can cause the normal assistant response to prefer the temporary
	// URL and leave the actual image hidden inside the collapsed tool card.
	contents := append(imageContents, deliveryContents...)

	compact := map[string]any{
		"status":                  "SUCCESS",
		"delivery_status":         "READY",
		"count":                   len(imageMetadata),
		"images":                  imageMetadata,
		"final_response_required": true,
	}
	if len(finalResponseMarkdown) > 0 {
		markdown := strings.Join(finalResponseMarkdown, "\n\n")
		compact["final_response_markdown"] = markdown
		contents = append(contents, mcpContent{
			Type: "text",
			Text: "FINAL USER-VISIBLE RESULT (required): Copy the complete Markdown between BEGIN and END verbatim into the assistant's normal final response, outside the MCP tool card. It contains both an inline image and a fallback original-file link. Do not download it first and do not call create_image again if either renderer fails.\n\n---BEGIN GENERATED IMAGE MARKDOWN---\n" + markdown + "\n---END GENERATED IMAGE MARKDOWN---",
		})
	} else {
		compact["delivery_status"] = "PREVIEW_ONLY"
		compact["final_response_markdown"] = "The image was generated successfully and is available in the existing MCP preview. The original-file link could not be created. Do not generate it again merely to obtain another delivery format."
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

const (
	maxMCPGeneratedImageFetchBytes int64 = 10 * 1024 * 1024
	maxMCPGeneratedImageTotalBytes int64 = 24 * 1024 * 1024
)

func findMCPImageResponseData(structured map[string]any) ([]any, string) {
	paths := [][]string{
		{"data"},
		{"images"},
		{"result", "data"},
		{"result", "images"},
		{"output", "data"},
		{"output", "images"},
		{"response", "data"},
	}
	for _, path := range paths {
		var current any = structured
		for _, segment := range path {
			object, ok := current.(map[string]any)
			if !ok {
				current = nil
				break
			}
			current = object[segment]
		}
		if items, ok := current.([]any); ok && len(items) > 0 {
			return items, strings.Join(path, ".")
		}
	}
	return nil, ""
}

func resolveMCPImageData(ctx context.Context, item map[string]any, fetchedBytes *int64) (string, string, error) {
	for _, key := range []string{"b64_json", "base64", "image_base64"} {
		if encoded, ok := item[key].(string); ok && strings.TrimSpace(encoded) != "" {
			return encoded, key, nil
		}
	}

	for _, key := range []string{"url", "image_url"} {
		rawURL, ok := item[key].(string)
		if !ok || strings.TrimSpace(rawURL) == "" {
			continue
		}
		encoded, err := fetchMCPGeneratedImage(ctx, rawURL, maxMCPGeneratedImageFetchBytes)
		if err != nil {
			return "", "", fmt.Errorf("fetch supported image URL: %w", err)
		}
		decodedBytes := int64(base64.StdEncoding.DecodedLen(len(encoded)))
		if fetchedBytes != nil {
			if *fetchedBytes+decodedBytes > maxMCPGeneratedImageTotalBytes {
				return "", "", fmt.Errorf("remote images exceed the %d byte total limit", maxMCPGeneratedImageTotalBytes)
			}
			*fetchedBytes += decodedBytes
		}
		return encoded, key, nil
	}
	return "", "", fmt.Errorf("image item did not contain a supported base64 or HTTPS URL field")
}

func describeMCPImageResponseShape(structured map[string]any, items []any, responsePath string) map[string]any {
	topLevelKeys := sortedMCPMapKeys(structured)
	shape := map[string]any{
		"top_level_keys": topLevelKeys,
		"image_path":     responsePath,
		"image_count":    len(items),
	}
	if len(items) > 0 {
		if first, ok := items[0].(map[string]any); ok {
			shape["first_image_keys"] = sortedMCPMapKeys(first)
		} else {
			shape["first_image_type"] = fmt.Sprintf("%T", items[0])
		}
	}
	if _, exists := structured["error"]; exists {
		shape["has_error"] = true
	}
	if _, exists := structured["message"]; exists {
		shape["has_message"] = true
	}
	return shape
}

func sortedMCPMapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func newMCPImageDeliveryFailure(message string, responseShape map[string]any) mcpToolResult {
	return mcpToolResult{
		Content: []mcpContent{{Type: "text", Text: message}},
		StructuredContent: map[string]any{
			"status":         "DELIVERY_FAILURE",
			"must_not_retry": true,
			"response_shape": responseShape,
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

// gpt-image-2-pro is mapped to the site's Adobe image route, whose fixed-price
// API accepts the standard quality label. Codex clients commonly infer high
// from the "pro" suffix; normalize only this exact public alias so the request
// is accepted without adding a provider retry or changing other image models.
func normalizeMCPImageCompatibility(args *mcpCreateImageArgs) {
	if args == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(args.Model), "gpt-image-2-pro") {
		args.Quality = "standard"
	}
}

func validateMCPImageArguments(args mcpCreateImageArgs) error {
	if !strings.EqualFold(strings.TrimSpace(args.Model), "gpt-image-2-pro") {
		return nil
	}
	size := strings.ToLower(strings.TrimSpace(args.Size))
	if size == "" || size == "auto" {
		return nil
	}
	for _, supportedSize := range mcpGPTImage2ProSizes[1:] {
		if size == supportedSize {
			return nil
		}
	}
	return fmt.Errorf(
		`size %q is not supported for gpt-image-2-pro. Use one of %s; use auto (or omit size) to let the upstream choose, and do not use the reference image's original dimensions as the output size.`,
		args.Size,
		strings.Join(mcpGPTImage2ProSizes, ", "),
	)
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
	// v6 also includes the normalized gpt-image-2-pro quality in the request
	// hash, so earlier rejected parameter combinations cannot remain cached.
	return "mcp:image:result:v6:" + requestHash
}

func mcpImageInflightKey(requestHash string) string {
	return "mcp:image:inflight:v6:" + requestHash
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
	result.StructuredContent["internal_request_performed"] = false
	result.StructuredContent["provider_request_performed"] = false
	result.StructuredContent["must_not_retry"] = true
	if result.StructuredContent["status"] == "SUCCESS" {
		result.Content = append(result.Content, mcpContent{
			Type: "text",
			Text: "This is the cached result of an identical recent request. No new provider request was made and no new generation charge was incurred. The native image block earlier in this result remains the inline preview. Copy structuredContent.final_response_markdown verbatim as the inline image and fallback link; do not call create_image again for delivery or saving problems.",
		})
	} else {
		result.Content = append(result.Content, mcpContent{
			Type: "text",
			Text: "This is the cached delivery failure from an identical recent request. No new internal or provider request was made. Do not automatically submit it again; report the existing response_shape and request_hash for diagnosis.",
		})
	}
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
			Description: "Generate or edit images through a configured image model. Use exactly the model requested by the user; never substitute or fall back to another model after an error unless the user explicitly asks for it. A SUCCESS is final and billable: native image content blocks provide an immediate preview, and structuredContent.final_response_markdown MUST be copied verbatim into the final assistant response to show the image with an original-file fallback link. A recent identical request is deduplicated. Never call again merely because preview, display, download, decode, save, or export failed.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model":  stringSchema("Exact image model name requested by the user and configured in New API. Do not substitute another model after an error."),
					"prompt": stringSchema("Description of the image to generate."),
					"n": map[string]any{
						"type":        "integer",
						"description": "Number of images. Defaults to 1.",
						"minimum":     1,
						"maximum":     4,
					},
					"size":    stringSchema("For gpt-image-2-pro use auto, 1024x1024, 1024x1536, 1536x1024, 1024x1792, or 1792x1024. auto (or omission) lets the upstream choose the output canvas; it does not preserve reference-image dimensions."),
					"quality": stringSchema("Optional provider-supported quality, for example standard, hd, low, medium, or high. gpt-image-2-pro is normalized to standard for upstream compatibility."),
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
				"allOf": []map[string]any{
					{
						"if": map[string]any{
							"properties": map[string]any{
								"model": map[string]any{"const": "gpt-image-2-pro"},
							},
						},
						"then": map[string]any{
							"properties": map[string]any{
								"size": map[string]any{"enum": mcpGPTImage2ProSizes},
							},
						},
					},
				},
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
		return "IMPORTANT: A SUCCESS from create_image is final and billable. Native image content blocks provide the immediate inline preview. The assistant's normal final response MUST also contain structuredContent.final_response_markdown verbatim, including its Markdown image and original-file fallback link; never leave the result only inside the MCP tool card. Never call create_image again because preview, display, download, decode, save, or export failed; reuse the existing result or report the delivery error. Set force_new only when the user explicitly requests a new variation. Use the exact image model ID requested by the user and exposed by the user's New API drawing channels; never substitute or fall back to another image model after an error unless the user explicitly requests it. For gpt-image-2-pro, supported output sizes are auto, 1024x1024, 1024x1536, 1536x1024, 1024x1792, and 1792x1024; auto or omitted size lets the upstream choose the canvas and does not preserve reference-image dimensions. Do not send the reference image's original dimensions such as 800x800 as the output size. Authenticate with a New API user token that can route to the drawing group; do not ask for OPENAI_API_KEY. For local reference images, call create_material_upload, PUT the exact bytes with every signed header, then pass up to 3 returned material IDs as reference_material_ids."
	case mcpToolProfileVideo:
		return "Use the exact video model IDs exposed by the user's New API video channels. Authenticate with a New API user token that can route to the video group. For a local reference image, call create_material_upload, upload the exact file bytes with HTTP PUT using every returned signed header, then pass the returned material_id to create_video. Poll get_video until SUCCESS or FAILURE."
	default:
		return "IMPORTANT: A SUCCESS from create_image is final and billable. Native image content blocks provide the immediate inline preview. The assistant's normal final response MUST also contain structuredContent.final_response_markdown verbatim, including its Markdown image and original-file fallback link; never leave the result only inside the MCP tool card. Never regenerate because preview, display, download, decode, save, or export failed; use force_new only for an explicitly requested new variation. Use the exact model IDs requested by the user and exposed by the user's New API channels; never substitute or fall back to another image model after an error unless the user explicitly requests it. For gpt-image-2-pro, supported output sizes are auto, 1024x1024, 1024x1536, 1536x1024, 1024x1792, and 1792x1024; auto or omitted size lets the upstream choose the canvas and does not preserve reference-image dimensions. Do not send the reference image's original dimensions such as 800x800 as the output size. Authenticate with a New API user token whose group can route to the requested media model; do not ask for OPENAI_API_KEY. For local reference images, call create_material_upload, PUT the exact bytes with every signed header, then pass material IDs to create_image or create_video. Poll get_video until SUCCESS or FAILURE."
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
