package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func withSidebarModules(t *testing.T, raw string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	previous, hadPrevious := common.OptionMap["SidebarModulesAdmin"]
	common.OptionMap["SidebarModulesAdmin"] = raw
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if hadPrevious {
			common.OptionMap["SidebarModulesAdmin"] = previous
		} else {
			delete(common.OptionMap, "SidebarModulesAdmin")
		}
	})
}

func performSidebarModuleRequest(role int) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET(
		"/",
		func(c *gin.Context) {
			c.Set("role", role)
			c.Next()
		},
		SidebarModuleAuth("chat", "video"),
		func(c *gin.Context) {
			c.Status(http.StatusNoContent)
		},
	)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	return recorder
}

func TestSidebarModuleAuthHidesDisabledModuleFromRegularUsers(t *testing.T) {
	withSidebarModules(t, `{"chat":{"enabled":true,"video":false}}`)
	recorder := performSidebarModuleRequest(common.RoleCommonUser)
	require.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestSidebarModuleAuthAlwaysAllowsAdministrators(t *testing.T) {
	withSidebarModules(t, `{"chat":{"enabled":false,"video":false}}`)
	recorder := performSidebarModuleRequest(common.RoleAdminUser)
	require.Equal(t, http.StatusNoContent, recorder.Code)
}

func TestSidebarModuleAuthAllowsEnabledModule(t *testing.T) {
	withSidebarModules(t, `{"chat":{"enabled":true,"video":true}}`)
	recorder := performSidebarModuleRequest(common.RoleCommonUser)
	require.Equal(t, http.StatusNoContent, recorder.Code)
}
