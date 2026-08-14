package controller

import (
	"bytes"
	"context"
	"errors"
	"io"
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

func TestMaterialContentStreamsPreviewWithoutRedirect(t *testing.T) {
	originalPresign := presignGeneratedImageObject
	originalOpen := openMaterialObject
	t.Cleanup(func() {
		presignGeneratedImageObject = originalPresign
		openMaterialObject = originalOpen
	})
	presignGeneratedImageObject = func(_ context.Context, materialID, fileName string) (*service.GeneratedImageRedirect, error) {
		require.Equal(t, "signed-preview", materialID)
		require.Equal(t, "material.jpg", fileName)
		return nil, errors.New("material is not a generated image")
	}
	openMaterialObject = func(_ context.Context, materialID, fileName string) (*service.MaterialObject, error) {
		require.Equal(t, "signed-preview", materialID)
		require.Equal(t, "material.jpg", fileName)
		payload := []byte("jpeg-preview")
		return &service.MaterialObject{
			MaterialObjectMetadata: service.MaterialObjectMetadata{
				ContentType:   "image/jpeg",
				ContentLength: int64(len(payload)),
				FileName:      "material.jpg",
			},
			Body: io.NopCloser(bytes.NewReader(payload)),
		}, nil
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/materials/content/signed-preview/material.jpg", nil)
	ctx.Params = gin.Params{
		{Key: "material_id", Value: "signed-preview"},
		{Key: "file_name", Value: "material.jpg"},
	}

	MaterialContent(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Empty(t, recorder.Header().Get("Location"))
	require.Equal(t, "image/jpeg", recorder.Header().Get("Content-Type"))
	require.Equal(t, "inline; filename=\"material.jpg\"", recorder.Header().Get("Content-Disposition"))
	require.Equal(t, "jpeg-preview", recorder.Body.String())
}
