package services

import (
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/global"
)

// ============================================================================
// 自助授权的「子集约束」回归
//
// 自助授权解决的是「有数据访问管理权限的人能给自己补一条更精确的授权」，
// 但绝不能变成提权通道：申请的每一项操作，都必须已经在自己的授权范围内。
// 这里锁死两条边界：
//   1. 自己只有实例级 select → 派生库级 select 可以，派生 dml 不行；
//   2. 自己只有库 A 的授权 → 申请库 B 被拒（跨资源不是子集）。
// ============================================================================

func TestSelfGrantAllowsSubsetOfOwnGrant(t *testing.T) {
	setupDataGrantTestDB(t)
	inst := "ds-self00000001"
	// 自己有实例级 select+export
	seedGrant(t, global.App.DB, seedGrantOpt{
		Level: DataResourceLevelInstance, InstanceID: inst,
		Actions: DataActionSelect + "," + DataActionExport,
	})

	// 派生一个库级 select：属于自己已有授权的子集，应放行
	if err := ensureSelfGrantWithinOwnScope(testScope(), inst, "db1", "", DataActionSelect, DataEffectAllow); err != nil {
		t.Fatalf("库级 select 应属于子集，实际被拒: %v", err)
	}
	// 派生一个表级 export：同样属于子集
	if err := ensureSelfGrantWithinOwnScope(testScope(), inst, "db1", "t1", DataActionExport, DataEffectAllow); err != nil {
		t.Fatalf("表级 export 应属于子集，实际被拒: %v", err)
	}
}

func TestSelfGrantRejectsActionsBeyondOwnGrant(t *testing.T) {
	setupDataGrantTestDB(t)
	inst := "ds-self00000002"
	seedGrant(t, global.App.DB, seedGrantOpt{
		Level: DataResourceLevelInstance, InstanceID: inst, Actions: DataActionSelect,
	})

	err := ensureSelfGrantWithinOwnScope(testScope(), inst, "db1", "", DataActionDML, DataEffectAllow)
	if err == nil {
		t.Fatal("自己只有 select，自助申请 dml 属于提权，应被拒绝")
	}
	if !strings.Contains(err.Error(), "dml") {
		t.Fatalf("错误应点明缺少哪个操作，实际: %v", err)
	}
}

func TestSelfGrantRejectsResourceOutsideOwnGrant(t *testing.T) {
	setupDataGrantTestDB(t)
	inst := "ds-self00000003"
	// 只有 db1 的库级授权
	seedGrant(t, global.App.DB, seedGrantOpt{
		Level: DataResourceLevelDatabase, InstanceID: inst, Schema: "db1", Actions: DataActionSelect,
	})

	err := ensureSelfGrantWithinOwnScope(testScope(), inst, "db2", "", DataActionSelect, DataEffectAllow)
	if err == nil {
		t.Fatal("跨库自助授权不属于子集，应被拒绝")
	}
	// 同库内派生表级则允许
	if err := ensureSelfGrantWithinOwnScope(testScope(), inst, "db1", "t1", DataActionSelect, DataEffectAllow); err != nil {
		t.Fatalf("同库内派生表级应放行，实际: %v", err)
	}
}

// deny 是自己给自己加限制，不涉及提权，不受子集约束
func TestSelfGrantDenyIsNotRestricted(t *testing.T) {
	setupDataGrantTestDB(t)
	inst := "ds-self00000004"
	// 一条授权都不给自己
	seedGrant(t, global.App.DB, seedGrantOpt{Level: DataResourceLevelInstance, InstanceID: "ds-other0000009"})

	if err := ensureSelfGrantWithinOwnScope(testScope(), inst, "db1", "t1", DataActionDML, DataEffectDeny); err != nil {
		t.Fatalf("deny 不涉及提权，不应被子集约束拦下: %v", err)
	}
}
