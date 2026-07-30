package fzyinghe

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeRequestAppliesDefaults(t *testing.T) {
	payload, err := normalizeRequest(relaycommon.TaskSubmitReq{
		Model:  "cheap-seedance-2.0",
		Prompt: "A cinematic sunrise",
	}, inputOptions{})

	require.NoError(t, err)
	assert.Equal(t, defaultDuration, payload.DurationSeconds)
	assert.Equal(t, defaultResolution, payload.Resolution)
	assert.Equal(t, defaultAspectRatio, payload.AspectRatio)
	assert.Equal(t, defaultMode, payload.Mode)
	assert.True(t, payload.Audio)
}

func TestNormalizeRequestPreservesAudioFalseAndMetadata(t *testing.T) {
	audio := false
	payload, err := normalizeRequest(relaycommon.TaskSubmitReq{
		Model:    "cheap-seedance-2.0-fast",
		Prompt:   "A quiet landscape",
		Duration: 6,
		Metadata: map[string]any{"order_id": "order-1"},
	}, inputOptions{
		Resolution:  "480P",
		AspectRatio: "16:9",
		Audio:       &audio,
	})

	require.NoError(t, err)
	assert.Equal(t, "480p", payload.Resolution)
	assert.Equal(t, 6, payload.DurationSeconds)
	assert.False(t, payload.Audio)
	assert.Equal(t, "order-1", payload.Metadata["order_id"])
}

func TestNormalizeRequestValidatesModelResolutionMatrix(t *testing.T) {
	_, err := normalizeRequest(relaycommon.TaskSubmitReq{
		Model:    "cheap-seedance-2.0-mini",
		Prompt:   "Test",
		Duration: 5,
	}, inputOptions{Resolution: "1080p"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
}

func TestNormalizeRequestValidatesDurationAndPrompt(t *testing.T) {
	_, err := normalizeRequest(relaycommon.TaskSubmitReq{
		Model:    "cheap-seedance-2.0",
		Prompt:   "Test",
		Duration: minDuration - 1,
	}, inputOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duration")

	_, err = normalizeRequest(relaycommon.TaskSubmitReq{
		Model:    "cheap-seedance-2.0",
		Prompt:   strings.Repeat("界", maxPromptCharacters+1),
		Duration: minDuration,
	}, inputOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prompt")
}

func TestNormalizeRequestRequiresBothStartAndEndFrames(t *testing.T) {
	_, err := normalizeRequest(relaycommon.TaskSubmitReq{
		Model:    "cheap-seedance-2.0",
		Prompt:   "Transition between frames",
		Duration: 5,
		Images:   []string{"start:https://cdn.example.com/start.png"},
	}, inputOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both start and end")

	payload, err := normalizeRequest(relaycommon.TaskSubmitReq{
		Model:    "cheap-seedance-2.0",
		Prompt:   "Transition between frames",
		Duration: 5,
		Images: []string{
			"start:https://cdn.example.com/start.png",
			"end:https://cdn.example.com/end.webp",
		},
	}, inputOptions{})
	require.NoError(t, err)
	assert.Equal(t, "start_end_frame", payload.Mode)
}

func TestNormalizeRequestRejectsPrivateAndUnsupportedReferences(t *testing.T) {
	_, err := normalizeRequest(relaycommon.TaskSubmitReq{
		Model:    "cheap-seedance-2.0",
		Prompt:   "Reference",
		Duration: 5,
		Images:   []string{"http://127.0.0.1/reference.png"},
	}, inputOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "private")

	_, err = normalizeRequest(relaycommon.TaskSubmitReq{
		Model:    "cheap-seedance-2.0",
		Prompt:   "Reference",
		Duration: 5,
		Images:   []string{"https://cdn.example.com/reference.gif"},
	}, inputOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported")
}

func TestEstimateBillingUsesDurationAndResolution(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set(requestContextKey, requestPayload{
		Model:           "cheap-seedance-2.0",
		Resolution:      "1080p",
		DurationSeconds: 8,
	})

	ratios := (&TaskAdaptor{}).EstimateBilling(context, &relaycommon.RelayInfo{})

	assert.Equal(t, 8.0, ratios["duration"])
	assert.Equal(t, 2.5, ratios["resolution"])
}

func TestDefaultRetailPricesIncludeTwentyFivePercentMarkup(t *testing.T) {
	basePrices := ratio_setting.GetDefaultModelPriceMap()
	expectedPerSecond := map[string]map[string]float64{
		"cheap-seedance-2.0": {
			"480p":  0.375,
			"720p":  0.75,
			"1080p": 1.875,
			"4K":    3.75,
		},
		"cheap-seedance-2.0-fast": {
			"480p": 0.30,
			"720p": 0.60,
		},
		"cheap-seedance-2.0-mini": {
			"480p": 0.1875,
			"720p": 0.375,
		},
	}

	for modelName, resolutionPrices := range expectedPerSecond {
		for resolution, expected := range resolutionPrices {
			actual := basePrices[modelName] * allowedResolutions[modelName][resolution]
			assert.InDelta(t, expected, actual, 0.000001, "%s %s", modelName, resolution)
		}
	}
}

func TestBuildRequestHeaderUsesPublicTaskIDAsIdempotencyKey(t *testing.T) {
	adaptor := &TaskAdaptor{apiKey: "upstream-key"}
	request := httptest.NewRequest(http.MethodPost, "https://example.com", nil)

	err := adaptor.BuildRequestHeader(nil, request, &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"},
	})

	require.NoError(t, err)
	assert.Equal(t, "Bearer upstream-key", request.Header.Get("Authorization"))
	assert.Equal(t, "task_public", request.Header.Get("Idempotency-Key"))
}

func TestDoResponseReadsStandardEnvelope(t *testing.T) {
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(
			`{"code":0,"msg":"ok","data":{"taskId":"upstream-123","status":"PENDING"}}`,
		)),
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "cheap-seedance-2.0",
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"},
	}

	taskID, _, taskErr := (&TaskAdaptor{}).DoResponse(ginContext, response, info)

	require.Nil(t, taskErr)
	assert.Equal(t, "upstream-123", taskID)
	assert.Contains(t, recorder.Body.String(), "task_public")
	assert.NotContains(t, recorder.Body.String(), "upstream-123")
}

func TestParseTaskResultMapsTerminalStates(t *testing.T) {
	adaptor := &TaskAdaptor{}
	success, err := adaptor.ParseTaskResult([]byte(
		`{"data":{"taskId":"upstream-1","status":"SUCCESS","resultUrl":"https://cdn.example.com/result.mp4"}}`,
	))
	require.NoError(t, err)
	assert.EqualValues(t, model.TaskStatusSuccess, success.Status)
	assert.Equal(t, "https://cdn.example.com/result.mp4", success.Url)

	failed, err := adaptor.ParseTaskResult([]byte(
		`{"data":{"taskId":"upstream-2","status":"CANCELLED","failReason":"cancelled by user"}}`,
	))
	require.NoError(t, err)
	assert.EqualValues(t, model.TaskStatusFailure, failed.Status)
	assert.Equal(t, "cancelled by user", failed.Reason)
}

func TestTasksEndpointDoesNotDuplicatePath(t *testing.T) {
	assert.Equal(t,
		"https://api-aigc.fzyinghe.com/video/generation/tasks",
		tasksEndpoint("https://api-aigc.fzyinghe.com"),
	)
	assert.Equal(t,
		"https://api-aigc.fzyinghe.com/video/generation/tasks",
		tasksEndpoint("https://api-aigc.fzyinghe.com/video/generation/tasks/"),
	)
}
