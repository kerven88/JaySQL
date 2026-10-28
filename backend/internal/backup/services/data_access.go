package services

// ---------------------------------------------------------------------------
// 备份链路的数据授权校验
//
// 收口前：备份集的「下载」与「恢复」只受 RBAC 功能权限约束（能进备份模块就能干），
// 与数据授权完全脱节。后果是显而易见的——一个只被授权看 A 库的人，
// 只要拿到备份模块权限，就能把整实例的备份集下载走，或者把它恢复到任意实例上。
// 备份集是数据的完整副本，绕过数据授权等于绕过一切。
//
// 收口后：
//   - 下载备份集  → 需要「源实例/源库」的 export 授权（把数据带走）；
//   - 恢复备份集  → 需要「源实例」的 export 授权 + 「目标实例」的 dml 授权（把数据写进去）。
//
// 备份集里的实例是数字 ID（insight_instances.id），而授权表用业务 ID（instance_id char(15)），
// 因此这里统一做一次转换；源实例已被删除的历史备份集无法判定资源，跳过校验
// （这类备份集本来也取不到数据）。
// ---------------------------------------------------------------------------

import (
	"strings"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	dasServices "github.com/simajiong/jaysql/internal/das/services"

	"github.com/gin-gonic/gin"
)

// CurrentUsername 暴露给 views 层：下载/恢复这类 view 里发起的校验需要操作人身份。
func CurrentUsername(c *gin.Context) string {
	return currentUsername(c)
}

// resolveInstanceBusinessID 数字实例 ID → 业务 instance_id（授权表用的口径）。
// 查不到（实例已删除）返回空串，由调用方决定跳过还是报错。
func resolveInstanceBusinessID(numericID uint64) string {
	if numericID == 0 {
		return ""
	}
	instance, err := loadInstanceByID(numericID)
	if err != nil || instance == nil {
		return ""
	}
	return strings.TrimSpace(instance.InstanceID)
}

// CheckInstanceAccessByID 校验操作人对「数字实例 ID + 目标库」是否有指定操作的授权。
func CheckInstanceAccessByID(username string, numericInstanceID uint64, schema, action string) error {
	if strings.TrimSpace(username) == "" {
		return nil
	}
	instanceID := resolveInstanceBusinessID(numericInstanceID)
	if instanceID == "" {
		return nil
	}
	return dasServices.CheckDataAccess(dasServices.DataAccessRequest{
		Username:   username,
		InstanceID: instanceID,
		Schema:     strings.TrimSpace(schema),
		Action:     action,
	})
}

// CheckBackupSetAccess 校验操作人对「备份集源实例/源库」是否有指定操作的授权。
func CheckBackupSetAccess(username string, set *backupModels.InsightBackupSets, action string) error {
	if set == nil || strings.TrimSpace(username) == "" {
		return nil
	}
	instanceID := resolveInstanceBusinessID(set.SourceInstanceID)
	if instanceID == "" {
		return nil
	}
	return dasServices.CheckDataAccess(dasServices.DataAccessRequest{
		Username:   username,
		InstanceID: instanceID,
		Schema:     strings.TrimSpace(set.SourceDBName),
		Action:     action,
	})
}

// CheckRestoreAccess 恢复动作的完整校验：源备份集要 export，目标实例要 dml。
//
// 两个方向缺一不可：只看源，等于允许把授权范围内的数据写到任意实例；
// 只看目标，等于允许把任意（未授权的）备份集写进自己有权限的实例。
func CheckRestoreAccess(username string, backupSetID uint64, targetInstanceID uint64, targetSchema string) error {
	if strings.TrimSpace(username) == "" {
		return nil
	}
	set, err := LoadBackupSet(backupSetID)
	if err != nil {
		// 备份集不存在时交给后续流程报更准确的错，这里不抢先失败
		return nil
	}
	if err := CheckBackupSetAccess(username, set, dasServices.DataActionExport); err != nil {
		return err
	}
	return CheckInstanceAccessByID(username, targetInstanceID, targetSchema, dasServices.DataActionDML)
}
