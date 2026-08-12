package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMCPTestContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Request.Header.Set("Authorization", "Bearer sk-test")
	return context, recorder
}

func TestMCPInitialize(t *testing.T) {
	context, recorder := newMCPTestContext(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
	)

	MCP(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"protocolVersion":"2025-06-18"`)
	assert.Contains(t, recorder.Body.String(), `"name":"new-api-video"`)
}

func TestMCPToolsList(t *testing.T) {
	context, recorder := newMCPTestContext(
		`{"jsonrpc":"2.0","id":"tools","method":"tools/list","params":{}}`,
	)

	MCP(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"create_video"`)
	assert.Contains(t, recorder.Body.String(), `"get_video"`)
	assert.Contains(t, recorder.Body.String(), `"create_image"`)
	assert.Contains(t, recorder.Body.String(), `"create_material_upload"`)
}

func TestMCPImageOnlyListsAndCallsImageTool(t *testing.T) {
	context, recorder := newMCPTestContext(
		`{"jsonrpc":"2.0","id":"tools","method":"tools/list","params":{}}`,
	)

	MCPImage(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"create_image"`)
	assert.NotContains(t, recorder.Body.String(), `"create_video"`)
	assert.NotContains(t, recorder.Body.String(), `"get_video"`)
	assert.NotContains(t, recorder.Body.String(), `"create_material_upload"`)

	context, recorder = newMCPTestContext(
		`{"jsonrpc":"2.0","id":"call","method":"tools/call","params":{"name":"create_video","arguments":{"prompt":"A sunrise"}}}`,
	)
	MCPImage(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"code":-32602`)
	assert.Contains(t, recorder.Body.String(), `tool is not available on this MCP endpoint`)
}

func TestMCPVideoOnlyListsVideoTools(t *testing.T) {
	context, recorder := newMCPTestContext(
		`{"jsonrpc":"2.0","id":"tools","method":"tools/list","params":{}}`,
	)

	MCPVideo(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), `"create_image"`)
	assert.Contains(t, recorder.Body.String(), `"create_video"`)
	assert.Contains(t, recorder.Body.String(), `"get_video"`)
	assert.Contains(t, recorder.Body.String(), `"create_material_upload"`)
}

func TestMCPCreateMaterialUploadCallsInternalAPI(t *testing.T) {
	originalHandler := mcpInternalHandler
	t.Cleanup(func() {
		mcpInternalHandler = originalHandler
	})

	mcpInternalHandler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodPost, request.Method)
		assert.Equal(t, "/v1/materials/uploads", request.URL.Path)
		assert.Equal(t, "Bearer sk-test", request.Header.Get("Authorization"))
		requestBody, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		assert.Contains(t, string(requestBody), `"file_name":"start.png"`)
		assert.Contains(t, string(requestBody), `"size_bytes":1234`)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"material_id":"signed-material","upload_url":"https://example.com/upload","method":"PUT","headers":{"Content-Type":"image/png"}}`))
	})

	context, recorder := newMCPTestContext(
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"create_material_upload","arguments":{"file_name":"start.png","content_type":"image/png","size_bytes":1234}}}`,
	)
	MCP(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"material_id":"signed-material"`)
	assert.NotContains(t, recorder.Body.String(), `"isError":true`)
}

func TestMCPCreateImageCallsInternalAPI(t *testing.T) {
	originalHandler := mcpInternalHandler
	t.Cleanup(func() {
		mcpInternalHandler = originalHandler
	})

	mcpInternalHandler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodPost, request.Method)
		assert.Equal(t, "/v1/images/generations", request.URL.Path)
		assert.Equal(t, "Bearer sk-test", request.Header.Get("Authorization"))
		requestBody, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		assert.Contains(t, string(requestBody), `"model":"gpt-image-1"`)
		assert.Contains(t, string(requestBody), `"response_format":"b64_json"`)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"created":1,"data":[{"b64_json":"aW1hZ2U="}]}`))
	})

	context, recorder := newMCPTestContext(
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"create_image","arguments":{"model":"gpt-image-1","prompt":"A sunrise"}}}`,
	)
	MCPImage(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"b64_json":"aW1hZ2U="`)
	assert.NotContains(t, recorder.Body.String(), `"isError":true`)
}

func TestMCPCreateVideoCallsInternalAPI(t *testing.T) {
	originalHandler := mcpInternalHandler
	t.Cleanup(func() {
		mcpInternalHandler = originalHandler
	})

	mcpInternalHandler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodPost, request.Method)
		assert.Equal(t, "/v1/video/generations", request.URL.Path)
		assert.Equal(t, "Bearer sk-test", request.Header.Get("Authorization"))
		requestBody, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		assert.Contains(t, string(requestBody), `"model":"cheap-seedance-2.0-fast"`)
		assert.Contains(t, string(requestBody), `"duration":5`)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"task_public","task_id":"task_public"}`))
	})

	context, recorder := newMCPTestContext(
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"create_video","arguments":{"prompt":"A sunrise"}}}`,
	)
	MCP(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"task_id":"task_public"`)
	assert.NotContains(t, recorder.Body.String(), `"isError":true`)
}

func TestMCPGetVideoReturnsToolErrorFromInternalAPI(t *testing.T) {
	originalHandler := mcpInternalHandler
	t.Cleanup(func() {
		mcpInternalHandler = originalHandler
	})

	mcpInternalHandler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/v1/video/generations/task_missing", request.URL.Path)
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte(`{"error":{"message":"task not found"}}`))
	})

	context, recorder := newMCPTestContext(
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_video","arguments":{"task_id":"task_missing"}}}`,
	)
	MCP(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"isError":true`)
	assert.Contains(t, recorder.Body.String(), `HTTP 404`)
}
