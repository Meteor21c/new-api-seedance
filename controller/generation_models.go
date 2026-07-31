package controller

import (
	"net/http"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

type generationModel struct {
	ID   string `json:"id"`
	Tier string `json:"tier,omitempty"`
}

func usableGroupNames(userGroup string) []string {
	groups := service.GetUserUsableGroups(userGroup)
	names := make([]string, 0, len(groups))
	for group := range groups {
		names = append(names, group)
	}
	sort.Strings(names)
	return names
}

func mappedGenerationModel(binding model.EnabledChannelModel) string {
	if strings.TrimSpace(binding.ModelMapping) == "" {
		return binding.Model
	}
	var mapping map[string]string
	if err := common.Unmarshal([]byte(binding.ModelMapping), &mapping); err != nil {
		return binding.Model
	}

	current := binding.Model
	visited := map[string]bool{current: true}
	for {
		next := strings.TrimSpace(mapping[current])
		if next == "" || next == current {
			return current
		}
		if visited[next] {
			return binding.Model
		}
		visited[next] = true
		current = next
	}
}

func videoModelTier(modelName string) string {
	normalized := strings.ToLower(strings.TrimSpace(modelName))
	switch {
	case strings.HasSuffix(normalized, "-mini"):
		return "mini"
	case strings.HasSuffix(normalized, "-fast"):
		return "fast"
	default:
		return "standard"
	}
}

func supportsImageGeneration(modelName string) bool {
	if common.IsImageGenerationModel(modelName) {
		return true
	}
	for _, endpoint := range model.GetModelSupportEndpointTypes(modelName) {
		if endpoint == constant.EndpointTypeImageGeneration {
			return true
		}
	}
	return false
}

func GetUserGenerationModels(c *gin.Context) {
	user, err := model.GetUserCache(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	groups := usableGroupNames(user.Group)

	kind := strings.ToLower(strings.TrimSpace(c.Query("type")))
	var bindings []model.EnabledChannelModel
	switch kind {
	case "video":
		bindings, err = model.GetEnabledChannelModelsForGroupsByType(
			groups,
			constant.ChannelTypeFZYingheVideo,
		)
	case "image":
		bindings, err = model.GetEnabledChannelModelsForGroups(groups)
	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "type must be image or video",
		})
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}

	// Populate endpoint metadata before image capability checks.
	if kind == "image" {
		model.GetPricing()
	}

	items := make([]generationModel, 0)
	seen := make(map[string]struct{})
	for _, binding := range bindings {
		name := strings.TrimSpace(binding.Model)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}

		upstreamName := mappedGenerationModel(binding)
		item := generationModel{ID: name}
		if kind == "video" {
			item.Tier = videoModelTier(upstreamName)
			items = append(items, item)
			seen[name] = struct{}{}
			continue
		}

		if supportsImageGeneration(name) || supportsImageGeneration(upstreamName) {
			items = append(items, item)
			seen[name] = struct{}{}
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		return items[i].ID < items[j].ID
	})

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"message":  "",
		"data":     items,
		"fallback": false,
	})
}
