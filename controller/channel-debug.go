package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/tidwall/gjson"
)

const (
	channelDebugMaxMessages      = 40
	channelDebugMaxMessageRunes  = 32_000
	channelDebugDefaultMaxTokens = 512
	channelDebugMaxTokens        = 4_096
)

type channelDebugMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type channelDebugRequest struct {
	Model        string                `json:"model"`
	Messages     []channelDebugMessage `json:"messages"`
	Stream       *bool                 `json:"stream,omitempty"`
	MaxTokens    int                   `json:"max_tokens,omitempty"`
	EndpointType string                `json:"endpoint_type,omitempty"`
}

type channelDebugUsage struct {
	PromptTokens        int    `json:"prompt_tokens"`
	CompletionTokens    int    `json:"completion_tokens"`
	TotalTokens         int    `json:"total_tokens"`
	CachedTokens        int    `json:"cached_tokens"`
	CacheCreationTokens int    `json:"cache_creation_tokens"`
	ReasoningTokens     int    `json:"reasoning_tokens"`
	UsageSource         string `json:"usage_source,omitempty"`
}

type channelDebugTiming struct {
	FirstResponseMs int64 `json:"first_response_ms"`
	TotalMs         int64 `json:"total_ms"`
}

type channelDebugBilling struct {
	Quota int     `json:"quota"`
	USD   float64 `json:"usd"`
}

func validateChannelDebugRequest(request *channelDebugRequest) error {
	if request == nil {
		return errors.New("request is required")
	}
	request.Model = strings.TrimSpace(request.Model)
	request.EndpointType = strings.TrimSpace(request.EndpointType)
	if request.Model == "" {
		return errors.New("model is required")
	}
	if len(request.Messages) == 0 {
		return errors.New("at least one message is required")
	}
	if len(request.Messages) > channelDebugMaxMessages {
		return errors.New("too many context messages")
	}
	for index := range request.Messages {
		message := &request.Messages[index]
		message.Role = strings.ToLower(strings.TrimSpace(message.Role))
		message.Content = strings.TrimSpace(message.Content)
		if message.Role != "system" && message.Role != "user" && message.Role != "assistant" {
			return errors.New("message role must be system, user, or assistant")
		}
		if message.Content == "" {
			return errors.New("message content is required")
		}
		if utf8.RuneCountInString(message.Content) > channelDebugMaxMessageRunes {
			return errors.New("message content is too long")
		}
	}
	if request.MaxTokens == 0 {
		request.MaxTokens = channelDebugDefaultMaxTokens
	}
	if request.MaxTokens < 1 || request.MaxTokens > channelDebugMaxTokens {
		return errors.New("max_tokens must be between 1 and 4096")
	}
	if request.EndpointType != "" &&
		request.EndpointType != string(constant.EndpointTypeOpenAI) &&
		request.EndpointType != string(constant.EndpointTypeOpenAIResponse) {
		return errors.New("endpoint_type must be empty, openai, or openai_response")
	}
	return nil
}

func buildChannelDebugRequest(request channelDebugRequest, channel *model.Channel, stream bool) (dto.Request, string, error) {
	endpointType := request.EndpointType
	if endpointType == "" {
		endpointType = normalizeChannelTestEndpoint(channel, "")
	}
	if endpointType == "" && strings.Contains(strings.ToLower(request.Model), "codex") {
		endpointType = string(constant.EndpointTypeOpenAIResponse)
	}

	if endpointType == string(constant.EndpointTypeOpenAIResponse) {
		input, err := common.Marshal(request.Messages)
		if err != nil {
			return nil, "", err
		}
		return &dto.OpenAIResponsesRequest{
			Model:           request.Model,
			Input:           json.RawMessage(input),
			Stream:          lo.ToPtr(stream),
			StreamOptions:   &dto.StreamOptions{IncludeUsage: true},
			MaxOutputTokens: lo.ToPtr(uint(request.MaxTokens)),
		}, endpointType, nil
	}

	messages := make([]dto.Message, 0, len(request.Messages))
	for _, message := range request.Messages {
		messages = append(messages, dto.Message{
			Role:    message.Role,
			Content: message.Content,
		})
	}
	openAIRequest := &dto.GeneralOpenAIRequest{
		Model:    request.Model,
		Messages: messages,
		Stream:   lo.ToPtr(stream),
	}
	if stream {
		openAIRequest.StreamOptions = &dto.StreamOptions{IncludeUsage: true}
	}
	if dto.IsOpenAIReasoningOModel(request.Model) {
		openAIRequest.MaxCompletionTokens = lo.ToPtr(uint(request.MaxTokens))
	} else {
		openAIRequest.MaxTokens = lo.ToPtr(uint(request.MaxTokens))
	}
	if endpointType == "" {
		endpointType = string(constant.EndpointTypeOpenAI)
	}
	return openAIRequest, endpointType, nil
}

func firstDebugString(data []byte, paths ...string) string {
	for _, path := range paths {
		value := gjson.GetBytes(data, path)
		if value.Type == gjson.String {
			if text := value.String(); text != "" {
				return text
			}
		}
	}
	return ""
}

func extractChannelDebugText(responseBody []byte, stream bool) string {
	if !stream {
		if text := firstDebugString(
			responseBody,
			"choices.0.message.content",
			"choices.0.text",
			"output_text",
			"content.0.text",
		); text != "" {
			return text
		}
		var builder strings.Builder
		gjson.GetBytes(responseBody, "output").ForEach(func(_, output gjson.Result) bool {
			output.Get("content").ForEach(func(_, content gjson.Result) bool {
				if text := content.Get("text").String(); text != "" {
					builder.WriteString(text)
				}
				return true
			})
			return true
		})
		return builder.String()
	}

	var builder strings.Builder
	for _, line := range strings.Split(string(responseBody), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" || !json.Valid([]byte(payload)) {
			continue
		}
		if text := firstDebugString(
			[]byte(payload),
			"choices.0.delta.content",
			"choices.0.text",
			"delta",
			"delta.text",
			"content_block.delta.text",
		); text != "" {
			builder.WriteString(text)
		}
	}
	return builder.String()
}

func DebugChannel(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil || channelID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid channel id"})
		return
	}

	var request channelDebugRequest
	if err := common.UnmarshalBodyReusable(c, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid request"})
		return
	}
	if err := validateChannelDebugRequest(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	channel, err := model.GetChannelById(channelID, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	stream := true
	if request.Stream != nil {
		stream = *request.Stream
	}
	debugRequest, endpointType, err := buildChannelDebugRequest(request, channel, stream)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	testUserID, err := resolveChannelTestUserID(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	result := testChannelWithRequest(
		c.Request.Context(),
		channel,
		testUserID,
		request.Model,
		endpointType,
		stream,
		debugRequest,
	)
	if result.localErr != nil {
		response := gin.H{
			"success": false,
			"message": result.localErr.Error(),
		}
		if result.newAPIError != nil {
			response["error_code"] = result.newAPIError.GetErrorCode()
		}
		c.JSON(http.StatusOK, response)
		return
	}

	usage := result.usage
	if usage == nil {
		usage = &dto.Usage{}
	}
	cacheCreationTokens := usage.PromptTokensDetails.CacheCreationTokensTotal()
	if usage.InputTokensDetails != nil {
		cacheCreationTokens = max(cacheCreationTokens, usage.InputTokensDetails.CacheCreationTokensTotal())
	}
	cachedTokens := max(usage.PromptCacheHitTokens, usage.PromptTokensDetails.CachedTokens)
	if usage.InputTokensDetails != nil {
		cachedTokens = max(cachedTokens, usage.InputTokensDetails.CachedTokens)
	}
	upstreamModel := request.Model
	mapped := false
	if result.info != nil {
		upstreamModel = result.info.UpstreamModelName
		mapped = result.info.IsModelMapped
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"content":      extractChannelDebugText(result.responseBody, stream),
			"raw_response": string(result.responseBody),
			"channel": gin.H{
				"id":   channel.Id,
				"name": channel.Name,
				"type": channel.Type,
			},
			"model": gin.H{
				"requested": request.Model,
				"upstream":  upstreamModel,
				"mapped":    mapped,
			},
			"endpoint_type": endpointType,
			"stream":        stream,
			"timing": channelDebugTiming{
				FirstResponseMs: result.firstResponseMs,
				TotalMs:         result.totalTimeMs,
			},
			"usage": channelDebugUsage{
				PromptTokens:        usage.PromptTokens,
				CompletionTokens:    usage.CompletionTokens,
				TotalTokens:         usage.TotalTokens,
				CachedTokens:        cachedTokens,
				CacheCreationTokens: cacheCreationTokens,
				ReasoningTokens:     usage.CompletionTokenDetails.ReasoningTokens,
				UsageSource:         usage.UsageSource,
			},
			"billing": channelDebugBilling{
				Quota: result.quota,
				USD:   float64(result.quota) / common.QuotaPerUnit,
			},
		},
	})
}
