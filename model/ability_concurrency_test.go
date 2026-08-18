package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGetChannelExcludingFallsBackToAvailablePriorityWithoutMemoryCache(t *testing.T) {
	originalDB := DB
	originalGroupColumn := commonGroupCol
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))
	DB = db
	commonGroupCol = "`group`"
	t.Cleanup(func() {
		DB = originalDB
		commonGroupCol = originalGroupColumn
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})

	const modelName = "channel-concurrency-db-model"
	highPriority := int64(10)
	lowPriority := int64(0)
	weight := uint(100)
	channels := []*Channel{
		{Id: 2301, Type: constant.ChannelTypeOpenAI, Key: "high", Status: common.ChannelStatusEnabled, Name: "high", Weight: &weight, Models: modelName, Group: "default", Priority: &highPriority},
		{Id: 2302, Type: constant.ChannelTypeOpenAI, Key: "low", Status: common.ChannelStatusEnabled, Name: "low", Weight: &weight, Models: modelName, Group: "default", Priority: &lowPriority},
	}
	for _, channel := range channels {
		require.NoError(t, db.Create(channel).Error)
		require.NoError(t, db.Create(&Ability{
			Group:     "default",
			Model:     modelName,
			ChannelId: channel.Id,
			Enabled:   true,
			Priority:  channel.Priority,
			Weight:    weight,
		}).Error)
	}

	selected, err := GetChannelExcluding(
		"default",
		modelName,
		0,
		"/v1/messages",
		map[int]struct{}{2301: {}},
	)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, 2302, selected.Id)
}
