package controller

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const maxTaskPresentationRequestBytes = 1 << 20

// enrichTaskPropertiesFromRequest keeps only the small, user-facing portion
// of a video request. It lets the customer workspace show the original prompt
// and generation parameters without exposing internal task identifiers.
func enrichTaskPropertiesFromRequest(c *gin.Context, task *model.Task) {
	if c == nil || task == nil || !strings.Contains(c.ContentType(), "json") {
		return
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil || storage.Size() <= 0 || storage.Size() > maxTaskPresentationRequestBytes {
		return
	}
	body, err := storage.Bytes()
	if err != nil {
		return
	}

	var fields map[string]any
	if err := common.Unmarshal(body, &fields); err != nil {
		return
	}

	properties := &task.Properties
	properties.Input = firstTaskString(fields, "prompt", "input")
	properties.Resolution = firstTaskString(fields, "resolution", "size")
	properties.Duration = firstTaskInt(fields, "duration", "duration_seconds", "seconds")
	properties.AspectRatio = firstTaskString(fields, "aspect_ratio", "ratio")
	properties.Mode = firstTaskString(fields, "mode")
	properties.Audio = firstTaskBool(fields, "audio", "generate_audio")
	if properties.Audio == nil {
		switch strings.ToLower(firstTaskString(fields, "sound")) {
		case "on":
			value := true
			properties.Audio = &value
		case "off":
			value := false
			properties.Audio = &value
		}
	}
}

func firstTaskString(fields map[string]any, names ...string) string {
	for _, name := range names {
		value, ok := fields[name].(string)
		if ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstTaskInt(fields map[string]any, names ...string) int {
	for _, name := range names {
		switch value := fields[name].(type) {
		case float64:
			if value > 0 {
				return int(value)
			}
		case string:
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err == nil && parsed > 0 {
				return parsed
			}
		}
	}
	return 0
}

func firstTaskBool(fields map[string]any, names ...string) *bool {
	for _, name := range names {
		if value, ok := fields[name].(bool); ok {
			return &value
		}
	}
	return nil
}
