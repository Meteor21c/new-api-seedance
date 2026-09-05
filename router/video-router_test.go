package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestVideoSignedContentRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetVideoRouter(engine)

	methods := map[string]bool{}
	for _, route := range engine.Routes() {
		if route.Path == "/v1/videos/:task_id/content/:access_token" {
			methods[route.Method] = true
		}
	}
	require.True(t, methods[http.MethodGet])
	require.True(t, methods[http.MethodHead])

	// The public route must be reachable without an Authorization header. Its
	// own task-bound signature check should reject an invalid token with 401.
	request := httptest.NewRequest(http.MethodGet, "/v1/videos/task_example/content/invalid", nil)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}
