package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetResultURLNeverReturnsFailureReason(t *testing.T) {
	failed := &Task{
		Status:     TaskStatusFailure,
		FailReason: "upstream rejected the request",
		PrivateData: TaskPrivateData{
			ResultURL: "upstream rejected the request",
		},
	}
	assert.Empty(t, failed.GetResultURL())

	legacySuccess := &Task{
		Status:     TaskStatusSuccess,
		FailReason: "https://cdn.example.com/legacy.mp4",
	}
	assert.Equal(t, legacySuccess.FailReason, legacySuccess.GetResultURL())

	currentSuccess := &Task{
		Status: TaskStatusSuccess,
		PrivateData: TaskPrivateData{
			ResultURL: "https://cdn.example.com/current.mp4",
		},
	}
	assert.Equal(t, currentSuccess.PrivateData.ResultURL, currentSuccess.GetResultURL())

	inProgress := &Task{
		Status: TaskStatusInProgress,
		PrivateData: TaskPrivateData{
			ResultURL: "https://cdn.example.com/incomplete.mp4",
		},
	}
	assert.Empty(t, inProgress.GetResultURL())
}

func TestHasActiveVideoTaskForUser(t *testing.T) {
	truncateTables(t)
	now := time.Now().Unix()

	insertTask(t, &Task{
		TaskID:     "video-active",
		UserId:     42,
		Platform:   constant.TaskPlatform("59"),
		Status:     TaskStatusInProgress,
		SubmitTime: now,
	})
	insertTask(t, &Task{
		TaskID:     "video-stale",
		UserId:     45,
		Platform:   constant.TaskPlatform("59"),
		Status:     TaskStatusInProgress,
		SubmitTime: now - 3600,
	})
	insertTask(t, &Task{
		TaskID:     "suno-active",
		UserId:     43,
		Platform:   constant.TaskPlatformSuno,
		Status:     TaskStatusInProgress,
		SubmitTime: now,
	})
	insertTask(t, &Task{
		TaskID:     "video-finished",
		UserId:     44,
		Platform:   constant.TaskPlatform("59"),
		Status:     TaskStatusSuccess,
		SubmitTime: now,
	})

	active, err := HasActiveVideoTaskForUser(42, now-60)
	require.NoError(t, err)
	assert.True(t, active)

	active, err = HasActiveVideoTaskForUser(43, now-60)
	require.NoError(t, err)
	assert.False(t, active)

	active, err = HasActiveVideoTaskForUser(44, now-60)
	require.NoError(t, err)
	assert.False(t, active)

	active, err = HasActiveVideoTaskForUser(45, now-60)
	require.NoError(t, err)
	assert.False(t, active)
}
