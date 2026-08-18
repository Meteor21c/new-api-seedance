package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	appI18n "github.com/QuantumNous/new-api/i18n"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetChannelWithUserConcurrencyRejectsAndReleasesSpecificChannel(t *testing.T) {
	require.NoError(t, appI18n.Init())
	originalRedisEnabled := common.RedisEnabled
	originalRDB := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = originalRedisEnabled
		common.RDB = originalRDB
	})

	const channelID = 7301
	const userID = 8301
	blocker, acquired := service.AcquireChannelUserConcurrency(t.Context(), channelID, userID, 1)
	require.True(t, acquired)
	require.NotNil(t, blocker)
	t.Cleanup(blocker.Release)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set("id", userID)
	c.Set("specific_channel_id", "7301")
	common.SetContextKey(c, constant.ContextKeyChannelId, channelID)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelName, "limited")
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{UserConcurrencyLimit: 1})

	retry := 0
	info := &relaycommon.RelayInfo{OriginModelName: "gpt-test"}
	channel, lease, channelErr := getChannelWithUserConcurrency(c, info, &service.RetryParam{Retry: &retry}, types.RelayFormatOpenAIResponses)
	assert.Nil(t, channel)
	assert.Nil(t, lease)
	require.NotNil(t, channelErr)
	assert.Equal(t, http.StatusTooManyRequests, channelErr.StatusCode)
	assert.Equal(t, types.ErrorCodeChannelUserConcurrencyLimit, channelErr.GetErrorCode())

	blocker.Release()
	channel, lease, channelErr = getChannelWithUserConcurrency(c, info, &service.RetryParam{Retry: &retry}, types.RelayFormatOpenAIResponses)
	require.Nil(t, channelErr)
	require.NotNil(t, channel)
	assert.Equal(t, channelID, channel.Id)
	require.NotNil(t, lease)
	lease.Release()
}

func TestRelayUserConcurrencyScope(t *testing.T) {
	limited := []types.RelayFormat{
		types.RelayFormatOpenAI,
		types.RelayFormatOpenAIResponses,
		types.RelayFormatOpenAIResponsesCompaction,
		types.RelayFormatClaude,
		types.RelayFormatGemini,
		types.RelayFormatOpenAIRealtime,
	}
	for _, relayFormat := range limited {
		assert.True(t, relayUsesChannelUserConcurrency(relayFormat), relayFormat)
	}

	unlimited := []types.RelayFormat{
		types.RelayFormatOpenAIImage,
		types.RelayFormatOpenAIAudio,
		types.RelayFormatEmbedding,
		types.RelayFormatRerank,
		types.RelayFormatTask,
	}
	for _, relayFormat := range unlimited {
		assert.False(t, relayUsesChannelUserConcurrency(relayFormat), relayFormat)
	}
}
