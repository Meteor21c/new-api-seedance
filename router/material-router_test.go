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

	request := httptest.NewRequest(http.MethodGet, "/v1/materials/content/invalid/material.png", nil)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusNotFound, recorder.Code)
}
