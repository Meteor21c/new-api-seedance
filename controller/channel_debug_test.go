package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateChannelDebugRequest(t *testing.T) {
	request := &channelDebugRequest{
		Model: " gpt-test ",
		Messages: []channelDebugMessage{
			{Role: " USER ", Content: " hello "},
		},
	}

	require.NoError(t, validateChannelDebugRequest(request))
	assert.Equal(t, "gpt-test", request.Model)
	assert.Equal(t, "user", request.Messages[0].Role)
	assert.Equal(t, "hello", request.Messages[0].Content)
	assert.Equal(t, channelDebugDefaultMaxTokens, request.MaxTokens)

	request.EndpointType = "unsupported"
	assert.ErrorContains(t, validateChannelDebugRequest(request), "endpoint_type")
}

func TestBuildChannelDebugRequestKeepsConversation(t *testing.T) {
	request := channelDebugRequest{
		Model:     "gpt-test",
		MaxTokens: 256,
		Messages: []channelDebugMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
			{Role: "user", Content: "remember me"},
		},
	}

	built, endpointType, err := buildChannelDebugRequest(
		request,
		&model.Channel{Type: constant.ChannelTypeOpenAI},
		true,
	)
	require.NoError(t, err)
	assert.Equal(t, string(constant.EndpointTypeOpenAI), endpointType)
	chatRequest, ok := built.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Len(t, chatRequest.Messages, 3)
	assert.Equal(t, "remember me", chatRequest.Messages[2].Content)
	assert.True(t, *chatRequest.Stream)
	assert.True(t, chatRequest.StreamOptions.IncludeUsage)
	assert.Equal(t, uint(256), *chatRequest.MaxTokens)
}

func TestBuildChannelDebugRequestUsesResponsesForCodex(t *testing.T) {
	request := channelDebugRequest{
		Model:     "gpt-5-codex",
		MaxTokens: 128,
		Messages:  []channelDebugMessage{{Role: "user", Content: "hello"}},
	}

	built, endpointType, err := buildChannelDebugRequest(
		request,
		&model.Channel{Type: constant.ChannelTypeCodex},
		true,
	)
	require.NoError(t, err)
	assert.Equal(t, string(constant.EndpointTypeOpenAIResponse), endpointType)
	responsesRequest, ok := built.(*dto.OpenAIResponsesRequest)
	require.True(t, ok)
	assert.Contains(t, string(responsesRequest.Input), `"content":"hello"`)
	assert.Equal(t, uint(128), *responsesRequest.MaxOutputTokens)
}

func TestExtractChannelDebugText(t *testing.T) {
	nonStream := []byte(`{"choices":[{"message":{"content":"hello world"}}]}`)
	assert.Equal(t, "hello world", extractChannelDebugText(nonStream, false))

	stream := []byte("event: response.output_text.delta\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello \"}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"world\"}}]}\n\n" +
		"data: [DONE]\n")
	assert.Equal(t, "hello world", extractChannelDebugText(stream, true))
}
