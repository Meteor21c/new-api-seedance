package service

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/model"
)

const (
	SmartRoutePolicyEconomy = "economy"
	SmartRoutePolicyQuality = "quality"
)

// NormalizeSmartTextToken keeps smart text keys on the existing Auto routing
// path and removes stale restrictions that would narrow their dynamic model
// pool. Media endpoints are enforced separately at authentication time.
func NormalizeSmartTextToken(token *model.Token) {
	if token == nil || !token.SmartText {
		return
	}
	token.Group = "auto"
	token.CrossGroupRetry = true
	token.ModelLimitsEnabled = false
	token.ModelLimits = ""
	token.SmartRoutePolicy = NormalizeSmartRoutePolicy(token.SmartRoutePolicy)
}

func NormalizeSmartRoutePolicy(policy string) string {
	if strings.EqualFold(strings.TrimSpace(policy), SmartRoutePolicyQuality) {
		return SmartRoutePolicyQuality
	}
	return SmartRoutePolicyEconomy
}

// IsSmartTextRequest defines the public protocol surface of a smart text key.
// Tool definitions and tool outputs stay inside the allowed text protocols and
// are intentionally passed through without filtering.
func IsSmartTextRequest(method string, path string) bool {
	if method == http.MethodGet {
		switch path {
		case "/v1/models", "/v1beta/models", "/v1beta/openai/models",
			"/dashboard/billing/subscription", "/v1/dashboard/billing/subscription",
			"/dashboard/billing/usage", "/v1/dashboard/billing/usage":
			return true
		default:
			return strings.HasPrefix(path, "/v1/models/")
		}
	}

	if method != http.MethodPost {
		return false
	}
	switch path {
	case "/v1/completions", "/v1/chat/completions", "/v1/responses",
		"/v1/responses/compact", "/v1/messages", "/v1/alpha/search":
		return true
	default:
		return false
	}
}
