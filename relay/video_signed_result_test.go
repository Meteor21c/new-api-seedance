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

func TestTaskModel2DtoExposesTokenUsageAndSettledAmount(t *testing.T) {
	task := &model.Task{
		TaskID: "task_token_usage",
		Quota:  int(float64(common.QuotaPerUnit) * 2.5),
		Data:   []byte(`{"code":200,"data":{"tokenUsage":{"inputTokens":1200,"outputTokens":300,"totalTokens":1500}}}`),
	}

	result := TaskModel2Dto(task)
	require.Equal(t, 1200, result.InputTokens)
	require.Equal(t, 300, result.OutputTokens)
	require.Equal(t, 1500, result.TotalTokens)
	require.InDelta(t, 2.5, result.BillingAmount, 0.000001)
}
