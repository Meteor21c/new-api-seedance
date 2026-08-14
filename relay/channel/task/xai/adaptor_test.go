package xai

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBuildOfficialXAIVideoRequestAndEstimateBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/pg/video/generations", bytes.NewBufferString(`{
		"model":"grok-imagine-video",
		"prompt":"A meteor over a quiet lake",
		"duration":5,
		"resolution":"720p",
		"aspect_ratio":"9:16",
		"mode":"text_with_reference",
		"audio":true,
		"reference_images":["https://cdn.example.com/style.png"]
	}`))
	c.Request.Header.Set("Content-Type", "application/json")
	defer common.CleanupBodyStorage(c)

	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	info.ChannelMeta = &relaycommon.ChannelMeta{
		ChannelBaseUrl: "https://image.example.com",
		ChannelType:    constant.ChannelTypeXai,
	}
	info.ApiKey = "upstream-key"
	info.UpstreamModelName = "grok-imagine-video"
	adaptor := &TaskAdaptor{}
	adaptor.Init(info)

	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	require.Equal(t, constant.TaskActionGenerate, info.Action)
	require.Equal(t, map[string]float64{"seconds": 5}, adaptor.EstimateBilling(c, info))

	requestURL, err := adaptor.BuildRequestURL(info)
	require.NoError(t, err)
	require.Equal(t, "https://image.example.com/v1/videos/generations", requestURL)

	body, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(body)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"model":"grok-imagine-video",
		"prompt":"A meteor over a quiet lake",
		"duration":5,
		"resolution":"720p",
		"aspect_ratio":"9:16",
		"reference_images":[{"url":"https://cdn.example.com/style.png"}]
	}`, string(encoded))
}

func TestXAIVideoRejectsUnsupportedEndFrame(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/pg/video/generations", bytes.NewBufferString(`{
		"model":"grok-imagine-video",
		"prompt":"A moving portrait",
		"duration":4,
		"resolution":"480p",
		"start_image_url":"https://cdn.example.com/start.png",
		"end_image_url":"https://cdn.example.com/end.png"
	}`))
	c.Request.Header.Set("Content-Type", "application/json")
	defer common.CleanupBodyStorage(c)

	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info)
	require.NotNil(t, taskErr)
	require.Contains(t, taskErr.Message, "does not support an end frame")
}

func TestParseXAIVideoTaskResult(t *testing.T) {
	adaptor := &TaskAdaptor{}

	pending, err := adaptor.ParseTaskResult([]byte(`{"status":"pending"}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusQueued, pending.Status)

	done, err := adaptor.ParseTaskResult([]byte(`{
		"status":"done",
		"video":{"url":"https://vidgen.example.com/result.mp4","duration":5},
		"model":"grok-imagine-video"
	}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusSuccess, done.Status)
	require.Equal(t, "https://vidgen.example.com/result.mp4", done.Url)

	failed, err := adaptor.ParseTaskResult([]byte(`{
		"status":"failed",
		"error":{"code":"permission_denied","message":"video access is not enabled"}
	}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusFailure, failed.Status)
	require.Equal(t, "video access is not enabled", failed.Reason)
}

func TestXAIVideoSubmitResponseUsesPublicTaskID(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	info.PublicTaskID = "task_public"
	info.OriginModelName = "grok-imagine-video"
	response := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(`{"request_id":"request_upstream"}`)),
	}

	taskID, data, taskErr := (&TaskAdaptor{}).DoResponse(c, response, info)
	require.Nil(t, taskErr)
	require.Equal(t, "request_upstream", taskID)
	require.JSONEq(t, `{"request_id":"request_upstream"}`, string(data))
	require.Contains(t, recorder.Body.String(), `"id":"task_public"`)
	require.NotContains(t, recorder.Body.String(), "request_upstream")
}
