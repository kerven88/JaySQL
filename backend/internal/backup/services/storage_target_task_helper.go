package services

import (
	"errors"
	"fmt"
	"strings"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	"github.com/simajiong/jaysql/internal/global"

	"gorm.io/gorm"
)

const (
	StorageBackendStorageTargetRef = "STORAGE_TARGET_REF"
)

func resolveAndValidateStorageTargetForTask(driverType string, storageTargetID uint64) (uint64, string, error) {
	if storageTargetID <= 0 {
		return 0, "", fmt.Errorf("请选择备份存储目标（StorageTarget），所有备份文件均需明确存储位置；如果还没有目标，请先在「备份恢复→存储目标」中创建服务器/S3 桶")
	}
	var target backupModels.InsightBackupStorageTargets
	if err := global.App.DB.Where("id = ?", storageTargetID).First(&target).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, "", fmt.Errorf("存储目标不存在，请重新选择")
		}
		return 0, "", fmt.Errorf("读取存储目标信息失败: %w", err)
	}
	tp := strings.ToUpper(strings.TrimSpace(string(target.Type)))
	if tp == backupModels.StorageTargetTypeLocalServer {
		if !target.FingerprintConfirmed {
			return 0, "", fmt.Errorf("存储目标 %s 还未完成 SSH 指纹确认，请先连通测试并确认指纹后再使用", target.Name)
		}
	} else {
		if isPhase2StorageType(tp) {
			return 0, "", fmt.Errorf("存储目标类型 %s 为二期规划能力，当前版本暂不支持生产使用", tp)
		}
	}
	return storageTargetID, StorageBackendStorageTargetRef, nil
}
