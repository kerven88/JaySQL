package services

import (
	"testing"

	"gorm.io/gorm"
)

// ============================================================================
// 新建数据源：不再自动授予任何人访问权限
//
// 历史 bug（两轮修复）：
//   1. CreateDefaultInstanceGrants / grantInstanceImmediateAccess 会给
//      code='admin' 的角色写一条实例级 allow + 全操作授权，等于 admin 角色
//      自动获得每个新增实例的全量访问权；
//   2. 掐掉角色后仍保留「创建人自动授权」，而 admin 是唯一创建人，
//      两个身份重合，界面上表现为「admin 还是自动拥有新实例的权限」。
//
// 【2026-10-15 最终口径】两条自动授权路径全部删除：数据源创建后对所有人
// 都不可见（含创建人本人与 admin 角色成员），必须由授权人显式授予后才可访问。
// 下面两个用例分别锁住「角色维度」与「创建人维度」不再有任何自动授权。
// ============================================================================

const (
	// 模拟「新建但从未被授权」的数据源
	newInstanceID = "ds-brandnew0001"
)

// seedInstanceGrantActor 准备一个「既是创建人、又是 admin 角色成员」的用户。
// 这两个身份重合正是历史 bug 在界面上难以分辨的根源，测试必须覆盖。
func seedInstanceGrantActor(t *testing.T, db *gorm.DB) {
	t.Helper()
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS insight_user_roles (uid bigint, role_id bigint)`,
		`DELETE FROM insight_users`,
		`DELETE FROM insight_roles`,
		`DELETE FROM insight_user_roles`,
		`INSERT INTO insight_users (uid, username, is_deleted, is_active) VALUES (100, 'tester', 0, 1)`,
		`INSERT INTO insight_roles (id, code, name, is_deleted) VALUES (12, 'admin', '管理员', 0)`,
		// 关键：tester 属于 admin 角色
		`INSERT INTO insight_user_roles (uid, role_id) VALUES (100, 12)`,
	}
	for _, s := range stmts {
		if err := db.Exec(s).Error; err != nil {
			t.Fatalf("seed actor failed, sql=%s err=%v", s, err)
		}
	}
}

// TestNewInstanceHasNoAutomaticGrantForAdminRole admin 角色成员对新建实例无任何自动授权
func TestNewInstanceHasNoAutomaticGrantForAdminRole(t *testing.T) {
	db := setupDataGrantTestDB(t)
	seedInstanceGrantActor(t, db)
	// 给另一个实例插一条授权，让新模型处于「已启用」状态
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelInstance, InstanceID: "ds-other0000001"})

	// 新建实例后，授权表里不应出现任何指向它的记录
	var total int64
	if err := db.Table("insight_das_data_grants").
		Where("instance_id = ?", newInstanceID).Count(&total).Error; err != nil {
		t.Fatalf("count grants: %v", err)
	}
	if total != 0 {
		t.Fatalf("新建实例不应产生任何自动授权，实际 %d 条", total)
	}

	// admin 角色成员（tester）的可见实例里不能包含它
	ids, _, err := GetAccessibleInstanceIDsByUsername("tester")
	if err != nil {
		t.Fatalf("GetAccessibleInstanceIDsByUsername: %v", err)
	}
	for _, id := range ids {
		if id == newInstanceID {
			t.Fatalf("admin 角色成员不应看见未授权的新实例 %s（可见列表：%v）", newInstanceID, ids)
		}
	}
}

// TestNewInstanceDeniedForCreatorWithoutGrant 创建人本人在显式授权前同样被拒（含 DML）
func TestNewInstanceDeniedForCreatorWithoutGrant(t *testing.T) {
	db := setupDataGrantTestDB(t)
	seedInstanceGrantActor(t, db)
	seedGrant(t, db, seedGrantOpt{Level: DataResourceLevelInstance, InstanceID: "ds-other0000001"})

	// 创建人 = tester，同时是 admin 角色成员；查询仍应被拒
	// （变更 / 导出自 2026-10-23 起由工单审批覆盖，不再要求数据授权，见下）
	if err := CheckDataAccess(DataAccessRequest{
		Username:   "tester",
		InstanceID: newInstanceID,
		Action:     DataActionSelect,
	}); err == nil {
		t.Fatal("未显式授权时，创建人执行 select 应被拒绝")
	}
	// 变更 / 导出：不再要求数据授权（工单自身的审批流程覆盖）
	for _, action := range []string{DataActionExport, DataActionDML} {
		if err := CheckDataAccess(DataAccessRequest{
			Username:   "tester",
			InstanceID: newInstanceID,
			Action:     action,
		}); err != nil {
			t.Fatalf("变更 / 导出不应要求数据授权，%s 被拒：%v", action, err)
		}
	}

	// 显式授权后立即放行，证明上面的拒绝来自「没有授权」而非判定链坏了
	seedGrant(t, db, seedGrantOpt{
		Level:      DataResourceLevelInstance,
		InstanceID: newInstanceID,
		Actions:    DataActionSelect + "," + DataActionExport + "," + DataActionDML,
	})
	if err := CheckDataAccess(DataAccessRequest{
		Username:   "tester",
		InstanceID: newInstanceID,
		Action:     DataActionSelect,
	}); err != nil {
		t.Fatalf("显式授权后 select 应放行，实际被拒：%v", err)
	}
}
