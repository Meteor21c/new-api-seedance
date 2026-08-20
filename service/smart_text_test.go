package service

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
)

func TestNormalizeSmartTextTokenUsesDynamicAutoRouting(t *testing.T) {
	token := &model.Token{
		SmartText:          true,
		Group:              "default",
		CrossGroupRetry:    false,
		AutoGroups:         `["default"]`,
		ModelLimitsEnabled: true,
		ModelLimits:        "gpt-only",
		SmartRoutePolicy:   "QUALITY",
	}

	NormalizeSmartTextToken(token)

	assert.Equal(t, "auto", token.Group)
	assert.True(t, token.CrossGroupRetry)
	assert.JSONEq(t, `["default"]`, token.AutoGroups)
	assert.False(t, token.ModelLimitsEnabled)
	assert.Empty(t, token.ModelLimits)
	assert.Equal(t, SmartRoutePolicyQuality, token.SmartRoutePolicy)
}

func TestIsSmartTextRequestAllowsTextProtocolsAndRejectsMedia(t *testing.T) {
	tests := []struct {
		method  string
		path    string
		allowed bool
	}{
		{method: http.MethodPost, path: "/v1/chat/completions", allowed: true},
		{method: http.MethodPost, path: "/v1/responses", allowed: true},
		{method: http.MethodPost, path: "/v1/responses/compact", allowed: true},
		{method: http.MethodPost, path: "/v1/messages", allowed: true},
		{method: http.MethodPost, path: "/v1/alpha/search", allowed: true},
		{method: http.MethodGet, path: "/v1/models", allowed: true},
		{method: http.MethodGet, path: "/v1/models/gpt-5", allowed: true},
		{method: http.MethodPost, path: "/v1/images/generations"},
		{method: http.MethodPost, path: "/v1/audio/speech"},
		{method: http.MethodPost, path: "/v1/videos"},
		{method: http.MethodPost, path: "/mcp/image"},
	}

	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			assert.Equal(t, test.allowed, IsSmartTextRequest(test.method, test.path))
		})
	}
}
