package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMaterialContentRedirectsGeneratedImageToOSS(t *testing.T) {
	originalPresign := presignGeneratedImageObject
	t.Cleanup(func() { presignGeneratedImageObject = originalPresign })
	presignGeneratedImageObject = func(_ context.Context, materialID, fileName string) (*service.GeneratedImageRedirect, error) {
		require.Equal(t, "signed-generated", materialID)
		require.Equal(t, "material.png", fileName)
		return &service.GeneratedImageRedirect{
			URL: "https://oss.example/generated.png?signature=short-lived",
			MaterialObjectMetadata: service.MaterialObjectMetadata{
				ContentType:   "image/png",
				ContentLength: 1234,
				FileName:      "material.png",
			},
		}, nil
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/materials/content/signed-generated/material.png", nil)
	ctx.Params = gin.Params{
		{Key: "material_id", Value: "signed-generated"},
		{Key: "file_name", Value: "material.png"},
	}

	MaterialContent(ctx)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "https://oss.example/generated.png?signature=short-lived", recorder.Header().Get("Location"))
	require.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, "*", recorder.Header().Get("Access-Control-Allow-Origin"))
}
