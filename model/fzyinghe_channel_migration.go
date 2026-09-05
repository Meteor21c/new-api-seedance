package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"gorm.io/gorm"
)

const (
	fzyingheChannelTypeMigrationKey = "migration.fzyinghe_channel_type_61"
	legacyFZYingheChannelType       = 59
)

// migrateFZYingheChannelType moves channels created by the Seedance fork
// before rc.24 away from type 59, which rc.24 now reserves for Sub2API.
//
// The extra predicates deliberately avoid converting legitimate Sub2API
// channels when an existing rc.24 database is first run with this fork.
func migrateFZYingheChannelType(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var marker Option
		err := tx.Where(&Option{Key: fzyingheChannelTypeMigrationKey}).First(&marker).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		result := tx.Model(&Channel{}).
			Where("type = ?", legacyFZYingheChannelType).
			Where(`LOWER(COALESCE(base_url, '')) LIKE ? OR LOWER(COALESCE(models, '')) LIKE ? OR LOWER(COALESCE(models, '')) LIKE ? OR LOWER(COALESCE(models, '')) LIKE ? OR LOWER(COALESCE(name, '')) LIKE ?`,
				"%fzyinghe%", "%seedance%", "%seedace%", "%kling-v3%", "%盈合%").
			Update("type", constant.ChannelTypeFZYingheVideo)
		if result.Error != nil {
			return result.Error
		}

		if err := tx.Create(&Option{Key: fzyingheChannelTypeMigrationKey, Value: "done"}).Error; err != nil {
			return err
		}
		if result.RowsAffected > 0 {
			common.SysLog("migrated legacy FZYinghe video channels from type 59 to 61")
		}
		return nil
	})
}
