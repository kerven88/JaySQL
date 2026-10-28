package services

// 实例创建人自动授权：
//
// 【2026-10-15 收口】之后数据源创建后对所有人不可见（无默认授权、无超管旁路），
// 但「创建人自己加的数据源自己不能选」违背直觉——创建动作本身就是一次
// 明确的访问意图，故在实例创建事务内自动给创建人写一条实例级 allow 授权。
// 其他人（包括 admin）仍需经数据访问管理显式授权后才可见。
//
// 走系统来源（grant_source=system）+ 审计流水，事后可追溯、可撤销。

import (
	"errors"
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/pkg/utils"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/das/models"
	"gorm.io/gorm"
)

// creatorAutoGrantActions 创建人自动授权的操作集，与管理页手工授权的常用口径一致，
// 保证创建人建完即可查询/导出/提交 DML 工单。
const creatorAutoGrantActions = DataActionAll

// AutoGrantInstanceToCreator 给实例创建人写一条实例级 allow 授权（幂等）。
//
// 必须在实例创建的同一事务内调用：授权写入失败时实例创建一并回滚，
// 避免「实例建好了但创建人看不到」这种比创建失败更难排查的半成品状态。
//
// creatorUsername 为空（如批量导入无登录上下文）时静默跳过，
// 此类实例沿用「显式授权后才可见」的收口语义。
func AutoGrantInstanceToCreator(tx *gorm.DB, instanceID, creatorUsername string) error {
	if tx == nil {
		return errors.New("自动授权缺少事务上下文")
	}
	instanceID = strings.TrimSpace(instanceID)
	creatorUsername = strings.TrimSpace(creatorUsername)
	if instanceID == "" || creatorUsername == "" {
		return nil
	}

	// 创建人必须是在职有效用户，取 uid 作为 principal_id（与新授权表的主体口径一致）
	var user struct {
		UID      uint64
		Username string
	}
	if err := tx.Table("insight_users").Select("uid, username").
		Where("username = ? AND is_deleted = ? AND is_active = ?", creatorUsername, false, true).
		Take(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 创建人已不存在/被停用：不阻塞实例创建，交由管理员显式授权
			return nil
		}
		return fmt.Errorf("查询实例创建人失败: %w", err)
	}

	// 幂等：同主体 + 同资源 + 同效力的有效授权已存在则跳过（重试/导入场景）
	var exists int64
	if err := tx.Table("insight_das_data_grants").
		Where("principal_type = ? AND principal_id = ? AND instance_id = ? AND schema_name = '' AND table_name = ? AND effect = ? AND status = ?",
			dasPrincipalUser, user.UID, instanceID, "", DataEffectAllow, DataGrantStatusActive).
		Count(&exists).Error; err != nil {
		return fmt.Errorf("查询既有授权失败: %w", err)
	}
	if exists > 0 {
		return nil
	}

	grant := &models.InsightDasDataGrant{
		Model:         &commonModels.Model{},
		GrantNo:       utils.GenerateDataGrantID(),
		PrincipalType: dasPrincipalUser,
		PrincipalID:   user.UID,
		PrincipalName: user.Username,
		ResourceLevel: DataResourceLevelInstance,
		InstanceID:    instanceID,
		Actions:       creatorAutoGrantActions,
		Effect:        DataEffectAllow,
		// 生效/失效时间留空：查询侧 NULL 即「立即生效、永不过期」
		GrantorUsername: creatorUsername,
		GrantorName:     user.Username,
		GrantSource:     DataGrantSourceSystem,
		Status:          DataGrantStatusActive,
		Reason:          "实例创建人自动授权",
	}
	if err := tx.Create(grant).Error; err != nil {
		return fmt.Errorf("写入创建人自动授权失败: %w", err)
	}
	writeGrantAudit(tx, &models.InsightDasGrantAudit{
		Model:            &commonModels.Model{},
		GrantNo:          grant.GrantNo,
		GrantID:          uint64(grant.Model.ID),
		Action:           DataGrantAuditCreate,
		OperatorUsername: creatorUsername,
		OperatorName:     user.Username,
		PrincipalType:    grant.PrincipalType,
		PrincipalID:      grant.PrincipalID,
		PrincipalName:    grant.PrincipalName,
		ResourceDesc:     describeDataResource(grant.InstanceID, "", ""),
		Actions:          grant.Actions,
		AfterSnapshot:    snapshotGrant(grant),
		Reason:           grant.Reason,
		SourceIP:         "",
	})
	return nil
}
