package services

import (
	"errors"
	"fmt"
	"strings"

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
	if strings.EqualFold(string(instance.DbType), "Redis") {
		return fmt.Errorf("Redis 当前仅支持实例管理与连接探测，不提供 SQL 元数据同步")
	}
	return commonTasks.SyncDBMetaForInstance(instance)
}
