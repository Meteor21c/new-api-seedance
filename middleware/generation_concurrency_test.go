package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalGenerationLockIsOwnerSafe(t *testing.T) {
	key := "generation:test:1"
	assert.True(t, acquireLocalGenerationLock(key, "first"))
	assert.False(t, acquireLocalGenerationLock(key, "second"))

	releaseLocalGenerationLock(key, "second")
	assert.False(t, acquireLocalGenerationLock(key, "third"))

	releaseLocalGenerationLock(key, "first")
	assert.True(t, acquireLocalGenerationLock(key, "third"))
	releaseLocalGenerationLock(key, "third")
}

func TestGenerationConcurrencyRejectsSecondImageRequest(t *testing.T) {
	originalRedisEnabled := common.RedisEnabled
	originalRDB := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = originalRedisEnabled
		common.RDB = originalRDB
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("id", 901)
		c.Next()
	})
	router.Use(GenerationConcurrency("image"))
	entered := make(chan struct{})
	release := make(chan struct{})
	router.POST("/generate", func(c *gin.Context) {
		if c.GetHeader("X-Block") == "true" {
			close(entered)
			<-release
		}
		c.Status(http.StatusNoContent)
	})

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(http.MethodPost, "/generate", nil)
		request.Header.Set("X-Block", "true")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		firstDone <- recorder
	}()
	<-entered

	second := httptest.NewRecorder()
	router.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/generate", nil))
	assert.Equal(t, http.StatusConflict, second.Code)
	assert.Contains(t, second.Body.String(), i18n.MsgGenerateAlreadyInProgress)

	close(release)
	first := <-firstDone
	require.Equal(t, http.StatusNoContent, first.Code)

	third := httptest.NewRecorder()
	router.ServeHTTP(third, httptest.NewRequest(http.MethodPost, "/generate", nil))
	assert.Equal(t, http.StatusNoContent, third.Code)
}

func TestGenerationConcurrencySkipsTaskFetch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("id", 902)
		c.Next()
	})
	router.Use(GenerationConcurrency("video"))
	router.GET("/task", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/task", nil))
	assert.Equal(t, http.StatusNoContent, recorder.Code)
}
