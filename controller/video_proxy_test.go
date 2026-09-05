package controller

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteVideoDataURLSupportsByteRanges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	payload := []byte("0123456789")
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/video", nil)
	context.Request.Header.Set("Range", "bytes=2-5")

	err := writeVideoDataURL(
		context,
		"data:video/mp4;base64,"+base64.StdEncoding.EncodeToString(payload),
	)

	require.NoError(t, err)
	assert.Equal(t, http.StatusPartialContent, recorder.Code)
	assert.Equal(t, "2345", recorder.Body.String())
	assert.Equal(t, "bytes 2-5/10", recorder.Header().Get("Content-Range"))
	assert.Equal(t, "bytes", recorder.Header().Get("Accept-Ranges"))
}

func TestWriteVideoDataURLSupportsHead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	payload := []byte("0123456789")
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodHead, "/video", nil)

	err := writeVideoDataURL(
		context,
		"data:video/mp4;base64,"+base64.StdEncoding.EncodeToString(payload),
	)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Empty(t, recorder.Body.String())
	assert.Equal(t, "10", recorder.Header().Get("Content-Length"))
}

func TestIsHopByHopVideoHeader(t *testing.T) {
	assert.True(t, isHopByHopVideoHeader("Transfer-Encoding"))
	assert.True(t, isHopByHopVideoHeader("connection"))
	assert.False(t, isHopByHopVideoHeader("Content-Range"))
}
