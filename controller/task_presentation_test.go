package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestEnrichTaskPropertiesFromVideoRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest(http.MethodPost, "/pg/video/generations", bytes.NewBufferString(`{
		"model":"doubao-seedance-2.0-fast",
		"prompt":"A hamster eats watermelon",
		"duration":5,
		"resolution":"480p",
		"aspect_ratio":"9:16",
		"mode":"text_with_reference",
		"audio":false
	}`))
	request.Header.Set("Content-Type", "application/json")
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request
	task := &model.Task{}

	enrichTaskPropertiesFromRequest(context, task)

	assert.Equal(t, "A hamster eats watermelon", task.Properties.Input)
	assert.Equal(t, 5, task.Properties.Duration)
	assert.Equal(t, "480p", task.Properties.Resolution)
	assert.Equal(t, "9:16", task.Properties.AspectRatio)
	assert.Equal(t, "text_with_reference", task.Properties.Mode)
	assert.NotNil(t, task.Properties.Audio)
	assert.False(t, *task.Properties.Audio)
}
