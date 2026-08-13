package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMCPTestContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Request.Header.Set("Authorization", "Bearer sk-test")
	context.Set("id", 42)
	return context, recorder
}

func useMCPMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	previousRedisEnabled := common.RedisEnabled
	previousRedisClient := common.RDB
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	require.NoError(t, client.Ping(context.Background()).Err())
	common.RedisEnabled = true
	common.RDB = client
	t.Cleanup(func() {
		_ = client.Close()
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedisClient
	})
	return server
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
	assert.Contains(t, recorder.Body.String(), `"create_material_upload"`)

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
	originalStore := storeMCPGeneratedImage
	t.Cleanup(func() {
		mcpInternalHandler = originalHandler
		storeMCPGeneratedImage = originalStore
	})
	storeMCPGeneratedImage = func(_ context.Context, userID int, payload []byte, mimeType string) (*service.GeneratedImageAsset, error) {
		assert.Equal(t, 42, userID)
		assert.NotEmpty(t, payload)
		assert.Equal(t, "image/png", mimeType)
		return &service.GeneratedImageAsset{
			URL:       "https://oss.example/generated/original.png?signature=test",
			ExpiresAt: time.Now().Add(24 * time.Hour).Unix(),
			MimeType:  mimeType,
		}, nil
	}

	mcpInternalHandler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodPost, request.Method)
		assert.Equal(t, "/v1/images/generations", request.URL.Path)
		assert.Equal(t, "Bearer sk-test", request.Header.Get("Authorization"))
		requestBody, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		assert.Contains(t, string(requestBody), `"model":"gpt-image-1"`)
		assert.Contains(t, string(requestBody), `"response_format":"b64_json"`)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"created":1,"data":[{"b64_json":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="}]}`))
	})

	context, recorder := newMCPTestContext(
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"create_image","arguments":{"model":"gpt-image-1","prompt":"A sunrise"}}}`,
	)
	MCPImage(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"type":"resource_link"`)
	assert.Contains(t, recorder.Body.String(), `"download_url":"https://oss.example/generated/original.png?signature=test"`)
	assert.Contains(t, recorder.Body.String(), `"type":"image"`)
	assert.Contains(t, recorder.Body.String(), `"mimeType":"image/jpeg"`)
	assert.Contains(t, recorder.Body.String(), `"data":"/9j/`)
	assert.Contains(t, recorder.Body.String(), `"status":"SUCCESS"`)
	assert.Contains(t, recorder.Body.String(), `"delivery_status":"READY"`)
	assert.Contains(t, recorder.Body.String(), `"final_response_required":true`)
	assert.Contains(t, recorder.Body.String(), `"final_response_markdown":"![Generated image 1]`)
	assert.Contains(t, recorder.Body.String(), `[Open or download original image 1]`)
	assert.Contains(t, recorder.Body.String(), `FINAL USER-VISIBLE RESULT`)
	assert.Contains(t, recorder.Body.String(), `BEGIN GENERATED IMAGE MARKDOWN`)
	assert.Less(t,
		strings.Index(recorder.Body.String(), `FINAL USER-VISIBLE RESULT`),
		strings.Index(recorder.Body.String(), `Image generation succeeded`),
		"the final-response delivery block must be the first MCP content item",
	)
	assert.Contains(t, recorder.Body.String(), `"provider_request_performed":true`)
	assert.Contains(t, recorder.Body.String(), `"must_not_retry":true`)
	assert.NotContains(t, recorder.Body.String(), `"b64_json"`)
	assert.NotContains(t, recorder.Body.String(), `"isError":true`)
}

func TestMCPCreateImageRejectsMissingBase64Data(t *testing.T) {
	originalHandler := mcpInternalHandler
	t.Cleanup(func() {
		mcpInternalHandler = originalHandler
	})

	mcpInternalHandler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"created":1,"data":[{"url":"https://example.com/temporary.png"}]}`))
	})

	context, recorder := newMCPTestContext(
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"create_image","arguments":{"model":"gpt-image-1","prompt":"A sunrise"}}}`,
	)
	MCPImage(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `none contained valid data[].b64_json`)
	assert.Contains(t, recorder.Body.String(), `"status":"DELIVERY_FAILURE"`)
	assert.Contains(t, recorder.Body.String(), `"must_not_retry":true`)
	assert.NotContains(t, recorder.Body.String(), `"isError":true`)
}

func TestMCPCreateImageWithReferenceUsesImageEdit(t *testing.T) {
	originalHandler := mcpInternalHandler
	originalOpen := openMCPMaterialObject
	originalStore := storeMCPGeneratedImage
	t.Cleanup(func() {
		mcpInternalHandler = originalHandler
		openMCPMaterialObject = originalOpen
		storeMCPGeneratedImage = originalStore
	})

	openMCPMaterialObject = func(_ context.Context, userID int, materialID string) (*service.MaterialObject, error) {
		assert.Equal(t, 42, userID)
		assert.Equal(t, "material-reference", materialID)
		return &service.MaterialObject{
			MaterialObjectMetadata: service.MaterialObjectMetadata{
				ContentType:   "image/png",
				ContentLength: 3,
				FileName:      "reference.png",
			},
			Body: io.NopCloser(bytes.NewReader([]byte("png"))),
		}, nil
	}
	storeMCPGeneratedImage = func(_ context.Context, _ int, _ []byte, mimeType string) (*service.GeneratedImageAsset, error) {
		return &service.GeneratedImageAsset{URL: "https://oss.example/generated/edit.png", ExpiresAt: time.Now().Add(time.Hour).Unix(), MimeType: mimeType}, nil
	}
	mcpInternalHandler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodPost, request.Method)
		assert.Equal(t, "/v1/images/edits", request.URL.Path)
		require.NoError(t, request.ParseMultipartForm(1<<20))
		assert.Equal(t, "gpt-image-2-plus", request.FormValue("model"))
		assert.Equal(t, "Keep the hamster's appearance", request.FormValue("prompt"))
		files := request.MultipartForm.File["image"]
		require.Len(t, files, 1)
		assert.Equal(t, "reference.png", files[0].Filename)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"created":1,"data":[{"b64_json":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="}]}`))
	})

	context, recorder := newMCPTestContext(
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"create_image","arguments":{"model":"gpt-image-2-plus","prompt":"Keep the hamster's appearance","reference_material_ids":["material-reference"]}}}`,
	)
	MCPImage(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"download_url":"https://oss.example/generated/edit.png"`)
	assert.NotContains(t, recorder.Body.String(), `"isError":true`)
}

func TestMCPCreateImageDeduplicatesRecentIdenticalRequest(t *testing.T) {
	useMCPMiniRedis(t)
	originalHandler := mcpInternalHandler
	originalStore := storeMCPGeneratedImage
	t.Cleanup(func() {
		mcpInternalHandler = originalHandler
		storeMCPGeneratedImage = originalStore
	})

	providerCalls := 0
	mcpInternalHandler = http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		providerCalls++
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"created":1,"data":[{"b64_json":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="}]}`))
	})
	storeMCPGeneratedImage = func(_ context.Context, _ int, _ []byte, mimeType string) (*service.GeneratedImageAsset, error) {
		return &service.GeneratedImageAsset{URL: "https://oss.example/generated/once.png", ExpiresAt: time.Now().Add(time.Hour).Unix(), MimeType: mimeType}, nil
	}
	body := `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"create_image","arguments":{"model":"gpt-image-2-plus","prompt":"A hamster eating watermelon"}}}`

	context, first := newMCPTestContext(body)
	MCPImage(context)
	context, second := newMCPTestContext(body)
	MCPImage(context)
	forceNewBody := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"create_image","arguments":{"model":"gpt-image-2-plus","prompt":"A hamster eating watermelon","force_new":true}}}`
	context, third := newMCPTestContext(forceNewBody)
	MCPImage(context)

	assert.Equal(t, 2, providerCalls, "only an explicit force_new request may call the provider again")
	assert.Contains(t, first.Body.String(), `"provider_request_performed":true`)
	assert.Contains(t, second.Body.String(), `"deduplicated":true`)
	assert.Contains(t, second.Body.String(), `"provider_request_performed":false`)
	assert.Contains(t, second.Body.String(), `No new provider request was made`)
	assert.Contains(t, second.Body.String(), `final_response_markdown`)
	assert.Contains(t, third.Body.String(), `"deduplicated":false`)
	assert.Contains(t, third.Body.String(), `"provider_request_performed":true`)
}

func TestMCPImageCacheKeysAreVersioned(t *testing.T) {
	assert.Equal(t, "mcp:image:result:v2:request", mcpImageCacheKey("request"))
	assert.Equal(t, "mcp:image:inflight:v2:request", mcpImageInflightKey("request"))
}

func TestMakeMCPImagePreviewBoundsLargePayload(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 1200, 800))
	state := uint32(1)
	for y := 0; y < source.Bounds().Dy(); y++ {
		for x := 0; x < source.Bounds().Dx(); x++ {
			state = state*1664525 + 1013904223
			source.SetNRGBA(x, y, color.NRGBA{
				R: uint8(state >> 24),
				G: uint8(state >> 16),
				B: uint8(state >> 8),
				A: 255,
			})
		}
	}
	var original bytes.Buffer
	require.NoError(t, png.Encode(&original, source))

	previewData, mimeType, err := makeMCPImagePreview(original.Bytes())
	require.NoError(t, err)
	assert.Equal(t, "image/jpeg", mimeType)
	assert.Less(t, len(previewData), 500_000, "MCP preview must remain small enough for reliable client transport")
	decoded, err := base64.StdEncoding.DecodeString(previewData)
	require.NoError(t, err)
	preview, _, err := image.Decode(bytes.NewReader(decoded))
	require.NoError(t, err)
	assert.LessOrEqual(t, preview.Bounds().Dx(), 640)
	assert.LessOrEqual(t, preview.Bounds().Dy(), 640)
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
