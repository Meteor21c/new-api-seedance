package fzyinghe

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
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

func TestNormalizeKlingV3UsesKlingRangeAndDefaults(t *testing.T) {
	audio := false
	payload, err := normalizeRequest(relaycommon.TaskSubmitReq{
		Model:    "kling-v3",
		Prompt:   "A cinematic sunrise",
		Duration: 3,
	}, inputOptions{Resolution: "1080P", Audio: &audio})

	require.NoError(t, err)
	assert.Equal(t, "1080p", payload.Resolution)
	assert.Equal(t, "16:9", payload.AspectRatio)
	assert.False(t, payload.Audio)
	assert.Equal(t, "pro", payload.Mode)
}

func TestNormalizeKlingDefaultsToSilent(t *testing.T) {
	payload, err := normalizeRequest(relaycommon.TaskSubmitReq{
		Model:    "kling-v3-omni",
		Prompt:   "A cinematic sunrise",
		Duration: 5,
	}, inputOptions{})

	require.NoError(t, err)
	assert.False(t, payload.Audio)
	assert.Equal(t, "1080p", payload.Resolution)
}

func TestBuildKlingV3RequestUsesOfficialFieldNames(t *testing.T) {
	body, err := buildKlingRequest(requestPayload{
		Model:           "kling-v3",
		Input:           "A girl smiles",
		Resolution:      "720p",
		DurationSeconds: 5,
		Audio:           true,
		StartImageURL:   "https://cdn.example.com/start.png",
	})
	require.NoError(t, err)
	encoded, err := common.Marshal(body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"model_name":"kling-v3","prompt":"A girl smiles","image":"https://cdn.example.com/start.png","duration":5,"mode":"std","sound":"on"}`, string(encoded))
}

func TestBuildSeedanceTokenRequestUsesVendorNativeContent(t *testing.T) {
	body, err := buildSeedanceTokenRequest(requestPayload{
		Model:           "doubao-seedance-2.0",
		Input:           "Use every reference",
		Resolution:      "1080p",
		AspectRatio:     "16:9",
		DurationSeconds: 5,
		Audio:           true,
		ReferenceImages: []string{
			"reference:https://cdn.example.com/style.png",
			"reference:https://cdn.example.com/motion.mp4",
			"reference:https://cdn.example.com/music.mp3",
		},
	})
	require.NoError(t, err)
	encoded, err := common.Marshal(body)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"model":"doubao-seedance-2.0",
		"content":[
			{"type":"text","text":"Use every reference"},
			{"type":"image_url","image_url":{"url":"https://cdn.example.com/style.png"},"role":"reference_image"},
			{"type":"video_url","video_url":{"url":"https://cdn.example.com/motion.mp4"},"role":"reference_video"},
			{"type":"audio_url","audio_url":{"url":"https://cdn.example.com/music.mp3"},"role":"reference_audio"}
		],
		"generate_audio":true,
		"ratio":"16:9",
		"resolution":"1080p",
		"duration":5,
		"watermark":false
	}`, string(encoded))
}

func TestBuildRequestBodyKeepsV3ForMappedDoubaoModel(t *testing.T) {
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Set(requestContextKey, requestPayload{
		Model:           "doubao-seedance-2.0",
		Input:           "A short establishing shot",
		Resolution:      "720p",
		AspectRatio:     "16:9",
		DurationSeconds: 4,
		Audio:           true,
	})
	info := &relaycommon.RelayInfo{
		OriginModelName: "doubao-seedance-2.0",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "doubao-seedance-2-0-260128",
			IsModelMapped:     true,
		},
	}

	body, err := (&TaskAdaptor{}).BuildRequestBody(ginContext, info)
	require.NoError(t, err)
	encoded, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"model":"doubao-seedance-2-0-260128",
		"content":[{"type":"text","text":"A short establishing shot"}],
		"generate_audio":true,
		"ratio":"16:9",
		"resolution":"720p",
		"duration":4,
		"watermark":false
	}`, string(encoded))
}

func TestNormalizeKlingOfficialImageDoesNotBecomeBothFrames(t *testing.T) {
	payload, err := normalizeRequest(relaycommon.TaskSubmitReq{
		Model:  "kling-v3",
		Prompt: "Animate this image",
		Image:  "https://cdn.example.com/start.png",
	}, inputOptions{Image: "https://cdn.example.com/start.png"})
	require.NoError(t, err)

	body, err := buildKlingRequest(payload)
	require.NoError(t, err)
	encoded, err := common.Marshal(body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"model_name":"kling-v3","prompt":"Animate this image","image":"https://cdn.example.com/start.png","duration":5,"mode":"std","sound":"off"}`, string(encoded))
}

func TestBuildKlingV3OmniMapsReferenceVideoAndTurnsIntoOfficialLists(t *testing.T) {
	body, err := buildKlingRequest(requestPayload{
		Model:           "kling-v3-omni",
		Input:           "A cinematic shot",
		Resolution:      "1080p",
		AspectRatio:     "16:9",
		DurationSeconds: 5,
		Audio:           false,
		ReferenceImages: []string{"reference:https://cdn.example.com/style.png", "reference:https://cdn.example.com/motion.mp4"},
	})
	require.NoError(t, err)
	encoded, err := common.Marshal(body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"model_name":"kling-v3-omni","prompt":"A cinematic shot","image_list":[{"image_url":"https://cdn.example.com/style.png","type":"reference"}],"video_list":[{"video_url":"https://cdn.example.com/motion.mp4","refer_type":"feature","keep_original_sound":"yes"}],"duration":5,"mode":"pro","aspect_ratio":"16:9","sound":"off"}`, string(encoded))
}

func TestKlingV3OmniAllowsPricedAudioWithReferenceVideo(t *testing.T) {
	audio := true
	payload, err := normalizeRequest(relaycommon.TaskSubmitReq{
		Model:    "kling-v3-omni",
		Prompt:   "A cinematic shot",
		Duration: 5,
		Images:   []string{"reference:https://cdn.example.com/motion.mp4"},
	}, inputOptions{Audio: &audio})
	require.NoError(t, err)
	assert.True(t, payload.Audio)
}

func TestMappedRequestModelResolvesChannelAliases(t *testing.T) {
	mapped, err := mappedRequestModel(
		"seedace-2.0-mini",
		`{"seedace-2.0-mini":"cheap-seedance-2.0-mini"}`,
	)
	require.NoError(t, err)
	assert.Equal(t, "cheap-seedance-2.0-mini", mapped)
}

func TestValidateDoubaoModelBeforeApplyingOfficialV3Mapping(t *testing.T) {
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewBufferString(`{
		"model":"doubao-seedance-2.0",
		"prompt":"A calm landscape",
		"duration":4,
		"resolution":"720p",
		"aspect_ratio":"16:9"
	}`))
	ginContext.Request.Header.Set("Content-Type", "application/json")
	ginContext.Set("model_mapping", `{"doubao-seedance-2.0":"doubao-seedance-2-0-260128"}`)
	defer common.CleanupBodyStorage(ginContext)

	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(ginContext, info)
	require.Nil(t, taskErr)
	payload, err := getNormalizedRequest(ginContext)
	require.NoError(t, err)
	assert.Equal(t, "doubao-seedance-2.0", payload.Model)
	assert.Equal(t, seedanceV3APIVersion, info.TaskAPIVersion)
}

func TestMappedRequestModelRejectsCycles(t *testing.T) {
	_, err := mappedRequestModel(
		"seedance-a",
		`{"seedance-a":"seedance-b","seedance-b":"seedance-a"}`,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
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

func TestNormalizeRequestAddsMissingSeedanceImageMentions(t *testing.T) {
	payload, err := normalizeRequest(relaycommon.TaskSubmitReq{
		Model:    "cheap-seedance-2.0-mini",
		Prompt:   "让仓鼠自然地吃西瓜",
		Duration: 5,
		Images: []string{
			"reference:https://api.example.com/material-one.jpg",
			"https://api.example.com/material-two.webp",
		},
	}, inputOptions{Resolution: "480p"})
	require.NoError(t, err)
	assert.Contains(t, payload.Input, "@image1")
	assert.Contains(t, payload.Input, "@image2")

	payload, err = normalizeRequest(relaycommon.TaskSubmitReq{
		Model:    "cheap-seedance-2.0-mini",
		Prompt:   "使用 @image1 的主体生成视频",
		Duration: 5,
		Images:   []string{"https://api.example.com/material.jpg"},
	}, inputOptions{Resolution: "480p"})
	require.NoError(t, err)
	assert.Equal(t, "使用 @image1 的主体生成视频", payload.Input)
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

func TestEstimateBillingTokenModelsUseOnlySceneRatio(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set(requestContextKey, requestPayload{
		Model:           "doubao-seedance-2.0",
		Resolution:      "1080p",
		DurationSeconds: 8,
		ReferenceImages: []string{"reference:https://cdn.example.com/motion.mp4"},
	})

	ratios := (&TaskAdaptor{}).EstimateBilling(context, &relaycommon.RelayInfo{})

	assert.InDelta(t, 31.0/46.0, ratios["token_scene"], 0.000001)
	assert.NotContains(t, ratios, "duration")
	assert.NotContains(t, ratios, "resolution")
}

func TestSeedanceTokenScenePriceMatrix(t *testing.T) {
	tests := []struct {
		model      string
		resolution string
		inputVideo bool
		basePrice  float64
		wantPrice  float64
	}{
		{"doubao-seedance-2.0", "480p", false, 46, 46},
		{"doubao-seedance-2.0", "1080p", false, 46, 51},
		{"doubao-seedance-2.0", "4K", false, 46, 26},
		{"doubao-seedance-2.0", "720p", true, 46, 28},
		{"doubao-seedance-2.0", "1080p", true, 46, 31},
		{"doubao-seedance-2.0", "4K", true, 46, 16},
		{"doubao-seedance-2.0-fast", "720p", false, 37, 37},
		{"doubao-seedance-2.0-fast", "720p", true, 37, 22},
		{"doubao-seedance-2.0-mini", "480p", false, 23, 23},
		{"doubao-seedance-2.0-mini", "480p", true, 23, 14},
		{"doubao-seedance-2.5", "720p", false, 70, 70},
		{"doubao-seedance-2.5", "720p", true, 70, 42},
	}

	for _, test := range tests {
		t.Run(test.model+"/"+test.resolution, func(t *testing.T) {
			payload := requestPayload{Model: test.model, Resolution: test.resolution}
			if test.inputVideo {
				payload.ReferenceImages = []string{"https://cdn.example.com/input.mp4"}
			}
			assert.InDelta(t, test.wantPrice, test.basePrice*seedanceTokenSceneRatio(payload), 0.000001)
		})
	}
}

func TestEstimateBillingUsesKlingAudioAndReferenceVideoRatios(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set(requestContextKey, requestPayload{
		Model:           "kling-v3-omni",
		Resolution:      "720p",
		DurationSeconds: 5,
		Audio:           false,
		ReferenceImages: []string{"reference:https://cdn.example.com/motion.mp4"},
	})

	ratios := (&TaskAdaptor{}).EstimateBilling(context, &relaycommon.RelayInfo{})
	assert.Equal(t, 5.0, ratios["duration"])
	assert.Equal(t, 1.5, ratios["reference_video"])
	assert.Equal(t, 1.0, ratios["audio"])
}

func TestDefaultRetailPricesIncludeTwentyPercentMarkup(t *testing.T) {
	basePrices := ratio_setting.GetDefaultModelPriceMap()
	expectedPerSecond := map[string]map[string]float64{
		"cheap-seedance-2.0": {
			"480p":  0.36,
			"720p":  0.72,
			"1080p": 1.8,
			"4K":    3.6,
		},
		"cheap-seedance-2.0-fast": {
			"480p": 0.288,
			"720p": 0.576,
		},
		"cheap-seedance-2.0-mini": {
			"480p": 0.18,
			"720p": 0.36,
		},
		"kling-v3": {
			"720p":  0.468,
			"1080p": 0.624,
			"4K":    2.34,
		},
		"kling-v3-omni": {
			"720p":  0.468,
			"1080p": 0.624,
			"4K":    2.34,
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

func TestDoResponseAcceptsSeedanceStringTimestampAndSuccessCode(t *testing.T) {
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(
			`{"code":200,"msg":"操作成功","data":{"taskId":"job-seedance-1","status":"queued","createdAt":"1785516592"}}`,
		)),
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "cheap-seedance-2.0-mini",
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"},
	}

	taskID, _, taskErr := (&TaskAdaptor{}).DoResponse(ginContext, response, info)

	require.Nil(t, taskErr)
	assert.Equal(t, "job-seedance-1", taskID)
}

func TestDoResponseReadsKlingEnvelope(t *testing.T) {
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	response := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(
			`{"code":0,"message":"SUCCEED","data":{"task_id":"kling-123","task_status":"submitted"}}`,
		)),
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "kling-v3",
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"},
	}

	taskID, _, taskErr := (&TaskAdaptor{}).DoResponse(ginContext, response, info)

	require.Nil(t, taskErr)
	assert.Equal(t, "kling-123", taskID)
}

func TestParseKlingTaskResultMapsSuccessAndNestedVideoURL(t *testing.T) {
	result, err := (&TaskAdaptor{}).ParseTaskResult([]byte(
		`{"code":0,"data":{"task_id":"kling-123","task_status":"succeed","task_result":{"videos":[{"url":"https://cdn.example.com/kling.mp4"}]}}}`,
	))
	require.NoError(t, err)
	assert.EqualValues(t, model.TaskStatusSuccess, result.Status)
	assert.Equal(t, "https://cdn.example.com/kling.mp4", result.Url)
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

func TestParseTaskResultReturnsTokenUsage(t *testing.T) {
	result, err := (&TaskAdaptor{}).ParseTaskResult([]byte(
		`{"code":200,"data":{"taskId":"upstream-usage","status":"SUCCESS","createdAt":"2026-05-15T10:00:00Z","resultUrl":"https://cdn.example.com/result.mp4","tokenUsage":{"inputTokens":1200,"outputTokens":300,"totalTokens":1500}}}`,
	))
	require.NoError(t, err)
	assert.Equal(t, 300, result.CompletionTokens)
	assert.Equal(t, 1500, result.TotalTokens)
}

func TestParseTaskResultReadsV3ContentAndUsage(t *testing.T) {
	result, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{
		"id":"cgt-v3-1",
		"model":"doubao-seedance-2-0-260128",
		"status":"succeeded",
		"content":{
			"video_url":"https://cdn.example.com/v3-result.mp4",
			"last_frame_url":"https://cdn.example.com/v3-last-frame.png"
		},
		"usage":{"completion_tokens":60682,"total_tokens":60682},
		"created_at":1784876400,
		"updated_at":1784877603
	}`))
	require.NoError(t, err)
	assert.EqualValues(t, model.TaskStatusSuccess, result.Status)
	assert.Equal(t, "https://cdn.example.com/v3-result.mp4", result.Url)
	assert.Equal(t, 60682, result.CompletionTokens)
	assert.Equal(t, 60682, result.TotalTokens)
}

func TestBuildRequestURLUsesV3OnlyForDoubaoSeedance(t *testing.T) {
	adaptor := &TaskAdaptor{baseURL: "https://api-aigc.fzyinghe.com"}

	v3URL, err := adaptor.BuildRequestURL(&relaycommon.RelayInfo{
		OriginModelName: "doubao-seedance-2.0-mini",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://api-aigc.fzyinghe.com/v3/video/tasks", v3URL)

	v1URL, err := adaptor.BuildRequestURL(&relaycommon.RelayInfo{
		OriginModelName: "cheap-seedance-2.0-mini",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://api-aigc.fzyinghe.com/video/generation/tasks", v1URL)
}

func TestFetchTaskUsesPersistedV3Protocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/v3/video/tasks/cgt-v3-1", request.URL.Path)
		assert.Equal(t, "Bearer test-key", request.Header.Get("Authorization"))
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte(`{"id":"cgt-v3-1","status":"running"}`))
	}))
	defer server.Close()

	response, err := (&TaskAdaptor{}).FetchTask(server.URL, "test-key", map[string]any{
		"task_id":     "cgt-v3-1",
		"api_version": seedanceV3APIVersion,
	}, "")
	require.NoError(t, err)
	defer response.Body.Close()
	assert.Equal(t, http.StatusOK, response.StatusCode)
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
	assert.Equal(t,
		"https://api-aigc.fzyinghe.com/v3/video/tasks",
		tasksEndpointForVersion("https://api-aigc.fzyinghe.com/video/generation/tasks/", seedanceV3APIVersion),
	)
}
