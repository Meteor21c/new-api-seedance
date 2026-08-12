package controller

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
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
	Type string `json:"type"`
	Text string `json:"text"`
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
	Model   string `json:"model"`
	Prompt  string `json:"prompt"`
	N       int    `json:"n,omitempty"`
	Size    string `json:"size,omitempty"`
	Quality string `json:"quality,omitempty"`
}

type mcpCreateMaterialUploadArgs struct {
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

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
	return callInternalAPI(c, http.MethodPost, "/v1/images/generations", payload)
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
	request := httptest.NewRequest(method, requestPath, body)
	request.Header.Set("Authorization", c.GetHeader("Authorization"))
	request.Header.Set("Content-Type", "application/json")
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
			Description: "Generate images synchronously through a configured OpenAI-compatible image model.",
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
				},
				"required":             []string{"model", "prompt"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "create_material_upload",
			Description: "Create a short-lived direct OSS upload for a local reference image. After this tool returns, upload the exact local file with HTTP PUT to upload_url using every returned header. Then pass material_id to create_video. The New API server does not proxy the file bytes.",
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
		return "Use create_image with the exact image model ID exposed by the user's New API drawing channels. Authenticate with a New API user token that can route to the drawing group. Decode data[].b64_json and save it as a local image file before replying."
	case mcpToolProfileVideo:
		return "Use the exact video model IDs exposed by the user's New API video channels. Authenticate with a New API user token that can route to the video group. For a local reference image, call create_material_upload, upload the exact file bytes with HTTP PUT using every returned signed header, then pass the returned material_id to create_video. Poll get_video until SUCCESS or FAILURE."
	default:
		return "Use the exact model IDs exposed by the user's New API channels. Use create_image for synchronous image generation and create_video for asynchronous video generation, including Seedance and Kling V3. Use a New API user token whose group can route to the requested media model. For a local video reference image, call create_material_upload, upload the exact file bytes with HTTP PUT using every returned signed header, then pass the returned material_id to create_video. Poll get_video until SUCCESS or FAILURE."
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
		return name == "create_image"
	case mcpToolProfileVideo:
		return name == "create_video" || name == "get_video" || name == "create_material_upload"
	default:
		return name == "create_image" || name == "create_video" || name == "get_video" || name == "create_material_upload"
	}
}
