package controller

import "strings"

// The Codex client uses a different wire contract from the regular OpenAI
// /v1/models endpoint. Keep this descriptor local to the controller so the
// compatibility layer does not change the public OpenAI DTOs or add a cache.
type codexModelsResponse struct {
	Models []codexModelInfo `json:"models"`
}

type codexModelInfo struct {
	Slug                    string                `json:"slug"`
	DisplayName             string                `json:"display_name"`
	Description             string                `json:"description"`
	DefaultReasoningLevel   *string               `json:"default_reasoning_level,omitempty"`
	SupportedReasoning      []codexReasoningLevel `json:"supported_reasoning_levels"`
	ShellType               string                `json:"shell_type"`
	Visibility              string                `json:"visibility"`
	SupportedInAPI          bool                  `json:"supported_in_api"`
	Priority                int                   `json:"priority"`
	AdditionalSpeedTiers    []string              `json:"additional_speed_tiers"`
	ServiceTiers            []codexServiceTier    `json:"service_tiers"`
	DefaultServiceTier      interface{}           `json:"default_service_tier"`
	AvailabilityNUX         interface{}           `json:"availability_nux"`
	Upgrade                 interface{}           `json:"upgrade"`
	ModelMessages           codexModelMessages    `json:"model_messages"`
	IncludeSkillsUsage      bool                  `json:"include_skills_usage_instructions"`
	IncludePluginUsage      bool                  `json:"include_plugin_usage_instructions"`
	IncludeAppsUsage        bool                  `json:"include_apps_usage_instructions"`
	SupportsReasoning       bool                  `json:"supports_reasoning_summary_parameter"`
	DefaultReasoningSummary string                `json:"default_reasoning_summary"`
	SupportVerbosity        bool                  `json:"support_verbosity"`
	DefaultVerbosity        interface{}           `json:"default_verbosity"`
	ApplyPatchToolType      interface{}           `json:"apply_patch_tool_type"`
	WebSearchToolType       string                `json:"web_search_tool_type"`
	TruncationPolicy        codexTruncationPolicy `json:"truncation_policy"`
	SupportsImageDetail     bool                  `json:"supports_image_detail_original"`
	SupportsParallelTools   bool                  `json:"supports_parallel_tool_calls"`
	ContextWindow           int64                 `json:"context_window"`
	MaxContextWindow        int64                 `json:"max_context_window"`
	AutoCompactTokenLimit   interface{}           `json:"auto_compact_token_limit"`
	CompHash                interface{}           `json:"comp_hash"`
	EffectiveContextWindow  int64                 `json:"effective_context_window_percent"`
	ExperimentalTools       []string              `json:"experimental_supported_tools"`
	InputModalities         []string              `json:"input_modalities"`
	SupportsSearchTool      bool                  `json:"supports_search_tool"`
	UseResponsesLite        bool                  `json:"use_responses_lite"`
	NodeREPLAutoReview      bool                  `json:"node_repl_auto_review_required"`
	NodeREPLDisabled        bool                  `json:"node_repl_disabled"`
	AutoReviewModelOverride interface{}           `json:"auto_review_model_override"`
	ModelSpecialty          interface{}           `json:"model_specialty"`
	ToolMode                interface{}           `json:"tool_mode"`
	MultiAgentVersion       interface{}           `json:"multi_agent_version"`
	MultiAgentReasoning     interface{}           `json:"multi_agent_reasoning_effort"`
	BaseInstructions        string                `json:"base_instructions"`
}

type codexReasoningLevel struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

type codexServiceTier struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type codexModelMessages struct {
	InstructionsTemplate  string      `json:"instructions_template"`
	InstructionsVariables interface{} `json:"instructions_variables"`
	Approvals             interface{} `json:"approvals"`
	CollaborationModes    interface{} `json:"collaboration_modes"`
	AutoReview            interface{} `json:"auto_review"`
	Permissions           interface{} `json:"permissions"`
	MultiAgent            interface{} `json:"multi_agent"`
	TokenBudget           interface{} `json:"token_budget"`
	GuardianV2            interface{} `json:"guardian_v2"`
}

type codexTruncationPolicy struct {
	Mode  string `json:"mode"`
	Limit int64  `json:"limit"`
}

const codexBaseInstructions = "You are Codex, an AI coding assistant. Work with the user to complete the requested task accurately and safely."

var codexReasoningDescriptions = map[string]string{
	"low":    "Fast responses with lighter reasoning",
	"medium": "Balanced reasoning for most coding tasks",
	"high":   "Greater reasoning depth for complex tasks",
	"xhigh":  "Extra high reasoning depth for complex tasks",
	"max":    "Maximum reasoning depth for the hardest tasks",
	"ultra":  "Maximum reasoning with automatic task delegation",
}

// buildCodexModels converts the already-filtered, request-scoped model list to
// the ModelInfo shape Codex expects. It intentionally performs no I/O and keeps
// no state, so model changes are visible on the next request and memory use is
// proportional only to the response being written.
func buildCodexModels(models []string) []codexModelInfo {
	result := make([]codexModelInfo, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, rawModel := range models {
		modelID := strings.TrimSpace(rawModel)
		if modelID == "" {
			continue
		}
		if _, exists := seen[modelID]; exists {
			continue
		}
		seen[modelID] = struct{}{}

		levels, defaultLevel := codexReasoningLevels(modelID)
		supportsReasoning := len(levels) > 0 && levels[0].Effort != "none"
		result = append(result, codexModelInfo{
			Slug:                    modelID,
			DisplayName:             codexDisplayName(modelID),
			Description:             "Model routed through New API.",
			DefaultReasoningLevel:   defaultLevel,
			SupportedReasoning:      levels,
			ShellType:               "unified_exec",
			Visibility:              "list",
			SupportedInAPI:          true,
			Priority:                50,
			AdditionalSpeedTiers:    []string{},
			ServiceTiers:            []codexServiceTier{},
			DefaultServiceTier:      nil,
			AvailabilityNUX:         nil,
			Upgrade:                 nil,
			ModelMessages:           codexModelMessages{InstructionsTemplate: codexBaseInstructions},
			IncludeSkillsUsage:      false,
			IncludePluginUsage:      false,
			IncludeAppsUsage:        false,
			SupportsReasoning:       supportsReasoning,
			DefaultReasoningSummary: "auto",
			SupportVerbosity:        false,
			DefaultVerbosity:        nil,
			ApplyPatchToolType:      nil,
			WebSearchToolType:       "text",
			TruncationPolicy:        codexTruncationPolicy{Mode: "bytes", Limit: 10000},
			SupportsImageDetail:     false,
			SupportsParallelTools:   true,
			ContextWindow:           272000,
			MaxContextWindow:        272000,
			AutoCompactTokenLimit:   nil,
			CompHash:                nil,
			EffectiveContextWindow:  95,
			ExperimentalTools:       []string{},
			InputModalities:         []string{"text"},
			SupportsSearchTool:      false,
			UseResponsesLite:        false,
			NodeREPLAutoReview:      false,
			NodeREPLDisabled:        false,
			AutoReviewModelOverride: nil,
			ModelSpecialty:          nil,
			ToolMode:                nil,
			MultiAgentVersion:       nil,
			MultiAgentReasoning:     nil,
			BaseInstructions:        codexBaseInstructions,
		})
	}
	return result
}

func codexDisplayName(modelID string) string {
	switch strings.ToLower(modelID) {
	case "gpt-6-astra":
		return "GPT-6-Astra"
	case "gpt-5.6-sol":
		return "GPT-5.6-Sol"
	case "gpt-5.6-terra":
		return "GPT-5.6-Terra"
	case "gpt-5.6-luna":
		return "GPT-5.6-Luna"
	case "gpt-5.5":
		return "GPT-5.5"
	case "gpt-5.4":
		return "GPT-5.4"
	case "gpt-5.4-mini":
		return "GPT-5.4-Mini"
	case "codex-auto-review":
		return "Codex Auto Review"
	}
	return modelID
}

func codexReasoningLevels(modelID string) ([]codexReasoningLevel, *string) {
	modelID = strings.ToLower(strings.TrimSpace(modelID))
	if !strings.Contains(modelID, "gpt-5") && !strings.Contains(modelID, "gpt-6") && !strings.Contains(modelID, "codex") {
		return []codexReasoningLevel{{Effort: "none", Description: "No additional reasoning"}}, stringPointer("none")
	}

	efforts := []string{"low", "medium", "high", "xhigh"}
	if strings.Contains(modelID, "gpt-5.6") || strings.Contains(modelID, "gpt-6") || strings.Contains(modelID, "codex-auto-review") {
		efforts = append(efforts, "max")
	}
	if strings.Contains(modelID, "gpt-5.6-sol") || strings.Contains(modelID, "gpt-5.6-terra") || strings.Contains(modelID, "gpt-6") {
		efforts = append(efforts, "ultra")
	}
	levels := make([]codexReasoningLevel, 0, len(efforts))
	for _, effort := range efforts {
		levels = append(levels, codexReasoningLevel{Effort: effort, Description: codexReasoningDescriptions[effort]})
	}
	defaultLevel := "medium"
	if strings.Contains(modelID, "gpt-5.6-sol") || strings.Contains(modelID, "gpt-6") {
		defaultLevel = "low"
	}
	return levels, stringPointer(defaultLevel)
}

func stringPointer(value string) *string {
	return &value
}
