package service

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVideoContentAccessTokenRoundTrip(t *testing.T) {
	previousSecret := common.CryptoSecret
	common.CryptoSecret = "video-access-test-secret"
	t.Cleanup(func() { common.CryptoSecret = previousSecret })

	now := time.Unix(1_800_000_000, 0)
	token, err := createVideoContentAccessToken("task_example", 42, now.Add(time.Hour))
	require.NoError(t, err)

	userID, err := VerifyVideoContentAccessToken("task_example", token, now)
	require.NoError(t, err)
	assert.Equal(t, 42, userID)

	_, err = VerifyVideoContentAccessToken("task_other", token, now)
	assert.ErrorContains(t, err, "invalid")
	_, err = VerifyVideoContentAccessToken("task_example", token+"tampered", now)
	assert.ErrorContains(t, err, "invalid")
	_, err = VerifyVideoContentAccessToken("task_example", token, now.Add(2*time.Hour))
	assert.ErrorContains(t, err, "expired")
}

func TestBuildSignedVideoContentURL(t *testing.T) {
	previousSecret := common.CryptoSecret
	previousAddress := system_setting.ServerAddress
	common.CryptoSecret = "video-access-test-secret"
	system_setting.ServerAddress = "https://api.example.com/"
	t.Cleanup(func() {
		common.CryptoSecret = previousSecret
		system_setting.ServerAddress = previousAddress
	})

	signedURL, err := BuildSignedVideoContentURL("task_example", 42)
	require.NoError(t, err)
	parsed, err := url.Parse(signedURL)
	require.NoError(t, err)
	assert.Equal(t, "api.example.com", parsed.Host)
	assert.True(t, strings.HasPrefix(parsed.EscapedPath(), "/v1/videos/task_example/content/"))

	encoded := strings.TrimPrefix(parsed.Path, "/v1/videos/task_example/content/")
	userID, err := VerifyVideoContentAccessToken("task_example", encoded, time.Now())
	require.NoError(t, err)
	assert.Equal(t, 42, userID)
}
