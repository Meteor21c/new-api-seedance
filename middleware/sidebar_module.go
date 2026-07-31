package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
)

func sidebarModuleEnabled(section string, module string) bool {
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap["SidebarModulesAdmin"]
	common.OptionMapRWMutex.RUnlock()
	if strings.TrimSpace(raw) == "" {
		return true
	}

	var config map[string]map[string]any
	if err := common.Unmarshal([]byte(raw), &config); err != nil {
		return true
	}
	sectionConfig, exists := config[section]
	if !exists {
		return true
	}
	if enabled, exists := sectionConfig["enabled"]; exists &&
		!parseHeaderNavBool(enabled, true) {
		return false
	}
	if enabled, exists := sectionConfig[module]; exists &&
		!parseHeaderNavBool(enabled, true) {
		return false
	}
	return true
}

// SidebarModuleAuth protects dashboard-only feature endpoints. API-token
// routes remain independent from sidebar visibility. Administrators always
// retain access so they can test a feature after hiding it from regular users.
// UserAuth must run before this middleware.
func SidebarModuleAuth(section string, module string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetInt("role") >= common.RoleAdminUser {
			c.Next()
			return
		}
		if sidebarModuleEnabled(section, module) {
			c.Next()
			return
		}

		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": fmt.Sprintf("%s is disabled", module),
		})
		c.Abort()
	}
}
