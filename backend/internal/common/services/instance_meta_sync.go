package services

import (
	"errors"
	"fmt"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	commonTasks "github.com/simajiong/jaysql/internal/common/tasks"
	"github.com/simajiong/jaysql/internal/global"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type AdminSyncInstanceMetaService struct {
	C  *gin.Context
	ID uint64
}

func (s *AdminSyncInstanceMetaService) Run() error {
	var instance commonModels.InsightInstances
	err := global.App.DB.Table("insight_instances").Where("id=?", s.ID).Take(&instance).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("实例不存在")
		}
		return err
	}
	return commonTasks.SyncDBMetaForInstance(instance)
}
