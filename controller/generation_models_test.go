package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type generationModelsTestResponse struct {
	Success  bool              `json:"success"`
	Data     []generationModel `json:"data"`
	Fallback bool              `json:"fallback"`
}

func insertGenerationModelBinding(
	t *testing.T,
	channelID int,
	channelType int,
	modelName string,
	modelMapping string,
) {
	t.Helper()
	priority := int64(0)
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:           channelID,
		Type:         channelType,
		Key:          "test-key",
		Status:       common.ChannelStatusEnabled,
		Name:         modelName,
		Models:       modelName,
		Group:        "default",
		ModelMapping: &modelMapping,
		Priority:     &priority,
	}).Error)
	require.NoError(t, model.DB.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: channelID,
		Enabled:   true,
		Priority:  &priority,
	}).Error)
}

func requestGenerationModels(t *testing.T, kind string) generationModelsTestResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodGet,
		"/api/user/generation_models?type="+kind,
		nil,
	)
	context.Set("id", 1)
	GetUserGenerationModels(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response generationModelsTestResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	return response
}

func TestGetUserGenerationModelsUsesChannelNamesAndMappedVideoTiers(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       1,
		Username: "video-user",
		Password: "password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}).Error)

	insertGenerationModelBinding(
		t,
		1,
		constant.ChannelTypeFZYingheVideo,
		"seedance-2.0-fast",
		`{"seedance-2.0-fast":"cheap-seedance-2.0-fast"}`,
	)
	insertGenerationModelBinding(
		t,
		2,
		constant.ChannelTypeFZYingheVideo,
		"seedace-2.0-mini",
		`{"seedace-2.0-mini":"cheap-seedance-2.0-mini"}`,
	)
	insertGenerationModelBinding(
		t,
		3,
		constant.ChannelTypeOpenAI,
		"gpt-5",
		"",
	)

	response := requestGenerationModels(t, "video")
	require.Equal(t, []generationModel{
		{ID: "seedace-2.0-mini", Tier: "mini"},
		{ID: "seedance-2.0-fast", Tier: "fast"},
	}, response.Data)
}

func TestGetUserGenerationModelsReportsKlingCapabilities(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       1,
		Username: "kling-user",
		Password: "password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}).Error)

	insertGenerationModelBinding(
		t,
		1,
		constant.ChannelTypeFZYingheVideo,
		"kling-v3",
		"",
	)
	insertGenerationModelBinding(
		t,
		2,
		constant.ChannelTypeFZYingheVideo,
		"kling-v3-omni",
		"",
	)

	response := requestGenerationModels(t, "video")
	require.Equal(t, []generationModel{
		{ID: "kling-v3", Tier: "standard", Kind: "kling-v3"},
		{ID: "kling-v3-omni", Tier: "standard", Kind: "kling-v3-omni"},
	}, response.Data)
}

func TestGetUserGenerationModelsReportsTokenBillingMetadata(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       1,
		Username: "token-video-user",
		Password: "password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}).Error)

	insertGenerationModelBinding(
		t,
		1,
		constant.ChannelTypeFZYingheVideo,
		"my-seedance-2.5",
		`{"my-seedance-2.5":"doubao-seedance-2.5"}`,
	)

	response := requestGenerationModels(t, "video")
	require.Equal(t, []generationModel{{
		ID:          "my-seedance-2.5",
		Tier:        "standard",
		BillingMode: "per-token",
		Resolutions: []string{"480p", "720p"},
	}}, response.Data)
}

func TestGetUserGenerationModelsIncludesOnlyXAIVideoModels(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       1,
		Username: "grok-video-user",
		Password: "password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}).Error)

	insertGenerationModelBinding(t, 1, constant.ChannelTypeXai, "grok-imagine-video", "")
	insertGenerationModelBinding(t, 2, constant.ChannelTypeXai, "grok-4-1-fast", "")

	response := requestGenerationModels(t, "video")
	require.Len(t, response.Data, 1)
	item := response.Data[0]
	require.Equal(t, "grok-imagine-video", item.ID)
	require.Equal(t, "standard", item.Tier)
	require.Equal(t, "grok-video", item.Kind)
	require.Equal(t, "per-second", item.BillingMode)
	require.Equal(t, []string{"480p", "720p"}, item.Resolutions)
	require.NotNil(t, item.PricePerSecond)
	require.InDelta(t, 0.2, *item.PricePerSecond, 0.000001)
}

func TestGetUserGenerationModelsFiltersMappedImageModels(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       1,
		Username: "image-user",
		Password: "password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}).Error)

	insertGenerationModelBinding(
		t,
		1,
		constant.ChannelTypeOpenAI,
		"my-image",
		`{"my-image":"gpt-image-1"}`,
	)
	insertGenerationModelBinding(
		t,
		2,
		constant.ChannelTypeOpenAI,
		"my-chat",
		`{"my-chat":"gpt-5"}`,
	)
	insertGenerationModelBinding(
		t,
		3,
		constant.ChannelTypeOpenAI,
		"gpt-image-2-pro",
		"",
	)

	response := requestGenerationModels(t, "image")
	require.Equal(t, []generationModel{
		{ID: "gpt-image-2-pro"},
		{ID: "my-image"},
	}, response.Data)
	require.False(t, response.Fallback)
}

func TestGetUserGenerationModelsDoesNotFallBackToNonImageModels(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       1,
		Username: "chat-only-user",
		Password: "password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}).Error)

	insertGenerationModelBinding(
		t,
		1,
		constant.ChannelTypeOpenAI,
		"claude-sonnet-5",
		"",
	)
	insertGenerationModelBinding(
		t,
		2,
		constant.ChannelTypeFZYingheVideo,
		"seedance-2.0",
		`{"seedance-2.0":"cheap-seedance-2.0"}`,
	)

	response := requestGenerationModels(t, "image")
	require.Empty(t, response.Data)
	require.False(t, response.Fallback)
}
