package controller

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newPlaygroundImageEditContext(t *testing.T, files []struct {
	name        string
	contentType string
}) *gin.Context {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-1"))
	require.NoError(t, writer.WriteField("prompt", "edit these images"))
	for _, file := range files {
		header := make(textproto.MIMEHeader)
		header["Content-Disposition"] = []string{`form-data; name="image[]"; filename="` + file.name + `"`}
		header["Content-Type"] = []string{file.contentType}
		part, err := writer.CreatePart(header)
		require.NoError(t, err)
		_, err = part.Write([]byte("image bytes"))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/pg/images/edits", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	return c
}

func TestValidatePlaygroundImageEditAcceptsThreeStaticImages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := newPlaygroundImageEditContext(t, []struct {
		name        string
		contentType string
	}{
		{name: "one.jpg", contentType: "image/jpeg"},
		{name: "two.png", contentType: "image/png"},
		{name: "three.webp", contentType: "image/webp"},
	})

	form, err := validatePlaygroundImageEdit(c)
	require.NoError(t, err)
	require.NotNil(t, form)
	require.NoError(t, form.RemoveAll())
}

func TestValidatePlaygroundImageEditRejectsTooManyOrAnimatedImages(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("more than three", func(t *testing.T) {
		c := newPlaygroundImageEditContext(t, []struct {
			name        string
			contentType string
		}{
			{name: "one.jpg", contentType: "image/jpeg"},
			{name: "two.jpg", contentType: "image/jpeg"},
			{name: "three.jpg", contentType: "image/jpeg"},
			{name: "four.jpg", contentType: "image/jpeg"},
		})

		form, err := validatePlaygroundImageEdit(c)
		require.ErrorContains(t, err, "no more than 3")
		require.NotNil(t, form)
		require.NoError(t, form.RemoveAll())
	})

	t.Run("gif", func(t *testing.T) {
		c := newPlaygroundImageEditContext(t, []struct {
			name        string
			contentType string
		}{{name: "animated.gif", contentType: "image/gif"}})

		form, err := validatePlaygroundImageEdit(c)
		require.ErrorContains(t, err, "static JPG, PNG, or WEBP")
		require.NotNil(t, form)
		require.NoError(t, form.RemoveAll())
	})
}
