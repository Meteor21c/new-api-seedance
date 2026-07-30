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
	Model           string   `json:"model,omitempty"`
	Prompt          string   `json:"prompt"`
	Duration        int      `json:"duration,omitempty"`
	Resolution      string   `json:"resolution,omitempty"`
	AspectRatio     string   `json:"aspect_ratio,omitempty"`
	Mode            string   `json:"mode,omitempty"`
	Audio           *bool    `json:"audio,omitempty"`
	ReferenceImages []string `json:"reference_images,omitempty"`
	StartImageURL   string   `json:"start_image_url,omitempty"`
	EndImageURL     string   `json:"end_image_url,omitempty"`
}

type mcpGetVideoArgs struct {
	TaskID string `json:"task_id"`
}

func SetMCPInternalHandler(handler http.Handler) {
	mcpInternalHandler = handler
}

func MCP(c *gin.Context) {
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
				"name":    mcpServerName,
				"version": common.Version,
			},
			"instructions": "Create asynchronous Seedance video tasks with create_video, then poll get_video until SUCCESS or FAILURE.",
		})
	case "ping":
		writeMCPResult(c, request.ID, map[string]any{})
	case "tools/list":
		writeMCPResult(c, request.ID, map[string]any{
			"tools": videoMCPTools(),
		})
	case "tools/call":
		handleMCPToolCall(c, request)
	default:
		writeMCPError(c, request.ID, -32601, "method not found")
	}
}

func handleMCPToolCall(c *gin.Context, request mcpRequest) {
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

	var result mcpToolResult
	switch params.Name {
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
	if args.Resolution == "" {
		args.Resolution = "720p"
	}
	if args.AspectRatio == "" {
		args.AspectRatio = "9:16"
	}
	if args.Mode == "" {
		args.Mode = "text_with_reference"
	}
	audio := true
	if args.Audio != nil {
		audio = *args.Audio
	}

	payload := map[string]any{
		"model":            args.Model,
		"prompt":           args.Prompt,
		"duration":         args.Duration,
		"resolution":       args.Resolution,
		"aspect_ratio":     args.AspectRatio,
		"mode":             args.Mode,
		"audio":            audio,
		"reference_images": args.ReferenceImages,
		"start_image_url":  strings.TrimSpace(args.StartImageURL),
		"end_image_url":    strings.TrimSpace(args.EndImageURL),
	}
	return callVideoAPI(c, http.MethodPost, "/v1/video/generations", payload)
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
	return callVideoAPI(
		c,
		http.MethodGet,
		"/v1/video/generations/"+url.PathEscape(args.TaskID),
		nil,
	)
}

func callVideoAPI(c *gin.Context, method, requestPath string, payload map[string]any) mcpToolResult {
	if mcpInternalHandler == nil {
		return newMCPToolError("internal video API is unavailable")
	}

	var body io.Reader
	if payload != nil {
		requestBody, err := common.Marshal(payload)
		if err != nil {
			return newMCPToolError("failed to encode video request")
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
		return newMCPToolError("failed to read internal video API response")
	}
	if response.StatusCode >= http.StatusBadRequest {
		return newMCPToolError(fmt.Sprintf(
			"video API returned HTTP %d: %s",
			response.StatusCode,
			strings.TrimSpace(string(responseBody)),
		))
	}

	var structured map[string]any
	if err := common.Unmarshal(responseBody, &structured); err != nil {
		return newMCPToolError("video API returned an invalid response")
	}
	textBody, err := common.Marshal(structured)
	if err != nil {
		return newMCPToolError("failed to encode video tool result")
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

func videoMCPTools() []mcpTool {
	stringSchema := func(description string) map[string]any {
		return map[string]any{
			"type":        "string",
			"description": description,
		}
	}
	return []mcpTool{
		{
			Name:        "create_video",
			Description: "Create an asynchronous Seedance video generation task. The result returns a task_id; use get_video to poll it.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model": map[string]any{
						"type":        "string",
						"description": "Seedance model. Defaults to cheap-seedance-2.0-fast.",
						"enum": []string{
							"cheap-seedance-2.0",
							"cheap-seedance-2.0-fast",
							"cheap-seedance-2.0-mini",
						},
					},
					"prompt": stringSchema("Video prompt, up to 1300 characters."),
					"duration": map[string]any{
						"type":        "integer",
						"description": "Output duration in seconds. Defaults to 5.",
						"minimum":     4,
						"maximum":     15,
					},
					"resolution": map[string]any{
						"type":        "string",
						"description": "Output resolution. Fast and mini support only 480p and 720p.",
						"enum":        []string{"480p", "720p", "1080p", "4K"},
					},
					"aspect_ratio": map[string]any{
						"type":        "string",
						"description": "Output aspect ratio. Defaults to 9:16.",
						"enum":        []string{"16:9", "9:16", "1:1", "4:3", "3:4", "21:9"},
					},
					"mode": map[string]any{
						"type":        "string",
						"description": "Generation mode. start_end_frame requires both start_image_url and end_image_url.",
						"enum":        []string{"text_with_reference", "start_end_frame"},
					},
					"audio": map[string]any{
						"type":        "boolean",
						"description": "Generate audio. Defaults to true.",
					},
					"reference_images": map[string]any{
						"type":        "array",
						"description": "Public HTTP/HTTPS reference asset URLs. Prefixes reference:, start:, and end: are supported.",
						"items":       stringSchema("Public reference asset URL."),
					},
					"start_image_url": stringSchema("Public URL for the start frame image."),
					"end_image_url":   stringSchema("Public URL for the end frame image."),
				},
				"required":             []string{"prompt"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "get_video",
			Description: "Get a Seedance video task by task_id. Poll until status is SUCCESS or FAILURE. On success, data.result_url is the signed video URL.",
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
