package model

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMigrateFZYingheChannelType(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Channel{}, &Option{}))

	fzyBaseURL := "https://api-aigc.fzyinghe.com"
	require.NoError(t, db.Create(&Channel{Type: legacyFZYingheChannelType, Name: "video", BaseURL: &fzyBaseURL, Models: "seedance-2.0,kling-v3"}).Error)
	require.NoError(t, db.Create(&Channel{Type: legacyFZYingheChannelType, Name: "sub2api", Models: "gpt-5"}).Error)

	require.NoError(t, migrateFZYingheChannelType(db))
	require.NoError(t, migrateFZYingheChannelType(db))

	var channels []Channel
	require.NoError(t, db.Order("id").Find(&channels).Error)
	require.Len(t, channels, 2)
	require.Equal(t, constant.ChannelTypeFZYingheVideo, channels[0].Type)
	require.Equal(t, constant.ChannelTypeSub2API, channels[1].Type)

	var marker Option
	require.NoError(t, db.Where(&Option{Key: fzyingheChannelTypeMigrationKey}).First(&marker).Error)
	require.Equal(t, "done", marker.Value)
}
