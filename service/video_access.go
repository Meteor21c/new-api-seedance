package service

import (
	"crypto/hmac"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

const defaultVideoContentAccessTTL = 24 * time.Hour

type videoContentAccessToken struct {
	Version   int    `json:"v"`
	UserID    int    `json:"user_id"`
	TaskID    string `json:"task_id"`
	ExpiresAt int64  `json:"expires_at"`
}

// BuildSignedVideoContentURL returns a short-lived URL that can be opened
// without putting the user's New API key in a query string. The opaque token
// is bound to both the task and its owner.
func BuildSignedVideoContentURL(taskID string, userID int) (string, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || userID <= 0 {
		return "", errors.New("invalid video content access subject")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(system_setting.ServerAddress), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New("server address is not configured")
	}

	token, err := createVideoContentAccessToken(taskID, userID, time.Now().Add(defaultVideoContentAccessTTL))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"%s/v1/videos/%s/content/%s",
		baseURL,
		url.PathEscape(taskID),
		url.PathEscape(token),
	), nil
}

// VerifyVideoContentAccessToken authenticates a public video content request
// and returns the task owner whose identity must be used for the database
// lookup. It deliberately does not accept a New API key in the URL.
func VerifyVideoContentAccessToken(taskID, encoded string, now time.Time) (int, error) {
	parts := strings.Split(encoded, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return 0, errors.New("invalid video access token")
	}
	expectedSignature := common.GenerateHMAC("video-content:v1:" + parts[0])
	if !hmac.Equal([]byte(parts[1]), []byte(expectedSignature)) {
		return 0, errors.New("invalid video access token")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, errors.New("invalid video access token")
	}
	var token videoContentAccessToken
	if err = common.Unmarshal(payload, &token); err != nil {
		return 0, errors.New("invalid video access token")
	}
	if token.Version != 1 || token.UserID <= 0 || token.TaskID == "" || token.TaskID != strings.TrimSpace(taskID) {
		return 0, errors.New("invalid video access token")
	}
	if token.ExpiresAt < now.Unix() {
		return 0, errors.New("video access token has expired")
	}
	return token.UserID, nil
}

func createVideoContentAccessToken(taskID string, userID int, expiresAt time.Time) (string, error) {
	payload, err := common.Marshal(videoContentAccessToken{
		Version:   1,
		UserID:    userID,
		TaskID:    strings.TrimSpace(taskID),
		ExpiresAt: expiresAt.Unix(),
	})
	if err != nil {
		return "", err
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signature := common.GenerateHMAC("video-content:v1:" + encodedPayload)
	return encodedPayload + "." + signature, nil
}
