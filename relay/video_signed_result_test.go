package relay

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/require"
)

func withVideoAccessTestSettings(t *testing.T) {
	t.Helper()
	previousSecret := common.CryptoSecret
	previousAddress := system_setting.ServerAddress
	common.CryptoSecret = "relay-video-access-test-secret"
	system_setting.ServerAddress = "https://api.example.com"
	t.Cleanup(func() {
		common.CryptoSecret = previousSecret
		system_setting.ServerAddress = previousAddress
	})
}

func TestVideoTaskModel2DtoReturnsPortableSignedURL(t *testing.T) {
	withVideoAccessTestSettings(t)
	task := &model.Task{
		TaskID: "task_example",
		UserId: 42,
		Status: model.TaskStatusSuccess,
	}

	result := videoTaskModel2Dto(task)
	require.Contains(t, result.ResultURL, "/v1/videos/task_example/content/")
	token := result.ResultURL[len("https://api.example.com/v1/videos/task_example/content/"):]
	userID, err := service.VerifyVideoContentAccessToken(task.TaskID, token, time.Now())
	require.NoError(t, err)
	require.Equal(t, task.UserId, userID)
}

func TestReplaceOpenAIVideoResultURL(t *testing.T) {
	withVideoAccessTestSettings(t)
	task := &model.Task{
		TaskID: "task_openai",
		UserId: 7,
		Status: model.TaskStatusSuccess,
	}
	raw := []byte(`{"id":"task_openai","status":"completed","metadata":{"url":"https://provider.example/video.mp4"}}`)

	rewritten := replaceOpenAIVideoResultURL(raw, task)
	var response map[string]any
	require.NoError(t, common.Unmarshal(rewritten, &response))
	metadata, ok := response["metadata"].(map[string]any)
	require.True(t, ok)
	signedURL, ok := metadata["url"].(string)
	require.True(t, ok)
	require.Contains(t, signedURL, "/v1/videos/task_openai/content/")
}

func TestVideoTaskModel2DtoDoesNotSignUnfinishedTask(t *testing.T) {
	withVideoAccessTestSettings(t)
	task := &model.Task{
		TaskID: "task_pending",
		UserId: 42,
		Status: model.TaskStatusInProgress,
	}

	result := videoTaskModel2Dto(task)
	require.Empty(t, result.ResultURL)
}
