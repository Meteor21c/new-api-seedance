package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMaterialPublicContentRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetMaterialRouter(engine)

	methods := map[string]bool{}
	for _, route := range engine.Routes() {
		if route.Path == "/v1/materials/content/:material_id/:file_name" {
			methods[route.Method] = true
		}
	}
	require.True(t, methods[http.MethodGet])
	require.True(t, methods[http.MethodHead])
	require.True(t, methods[http.MethodOptions])

	optionsRequest := httptest.NewRequest(http.MethodOptions, "/v1/materials/content/material/file.png", nil)
	optionsRecorder := httptest.NewRecorder()
	engine.ServeHTTP(optionsRecorder, optionsRequest)
	require.Equal(t, http.StatusNoContent, optionsRecorder.Code)
	require.Equal(t, "*", optionsRecorder.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, "cross-origin", optionsRecorder.Header().Get("Cross-Origin-Resource-Policy"))

	request := httptest.NewRequest(http.MethodGet, "/v1/materials/content/invalid/material.png", nil)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusNotFound, recorder.Code)
}
