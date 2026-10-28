package services

import (
	"testing"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/das/models"
)

// ============================================================================
// 整实例申请的执行授权回归
//
// 背景：申请范围选「整个实例」时，前端没有库 / 模式可圈选，
// resource_spec 落的是空结构 "{}"（历史单为 NULL），resource_level=instance。
// grantAccessForRequest 早前把「空 spec」直接判成「申请单没有可授权的资源」，
// 于是所有整实例申请单都卡在最后一步（审批通过 → 执行授权必失败）。
// 与数据库类型无关：SQLServer / Oracle / MySQL 的整实例申请一律踩中。
// ============================================================================

func instanceLevelRequest(spec string) *models.InsightDasAccessRequest {
	return &models.InsightDasAccessRequest{
		Model:            &commonModels.Model{},
		RequestNo:        "ra-instance0001",
		ApplicantID:      testScope().UID,
		ApplicantName:    "admin",
		InstanceID:       "ds-sqlserver001",
		ResourceLevel:    DataResourceLevelInstance,
		ResourceSpec:     spec,
		Actions:          DataActionSelect,
		ApproverUsername: "maoti",
	}
}

// 整实例申请（"{}" 与历史 NULL 两种快照）必须能落成一条 instance 级授权，
// 且该授权要覆盖实例下任意库表的查询。
func TestGrantAccessForInstanceLevelRequest(t *testing.T) {
	db := setupDataGrantTestDB(t)

	for _, tc := range []struct {
		name string
		spec string
	}{
		{name: "空结构 JSON", spec: "{}"},
		{name: "历史单 NULL", spec: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := instanceLevelRequest(tc.spec)
			req.RequestNo = "ra-" + tc.name
			grant, err := grantAccessForRequest(db, req, nil, "maoti", "", "127.0.0.1")
			if err != nil {
				t.Fatalf("整实例申请应可落地授权，实际报错：%v", err)
			}
			if grant == nil {
				t.Fatal("未返回授权记录")
			}
			if grant.ResourceLevel != DataResourceLevelInstance {
				t.Fatalf("授权层级应为 instance，实际 %q", grant.ResourceLevel)
			}
			if grant.SchemaName != "" || grant.Table != "" {
				t.Fatalf("整实例授权不应带库表：schema=%q table=%q", grant.SchemaName, grant.Table)
			}
			if !grantAllowsAction(grant.Actions, DataActionSelect) {
				t.Fatalf("授权动作应含 %s，实际 %q", DataActionSelect, grant.Actions)
			}

			// 落地后判定层要真的放行：库级、表级请求都应命中这条实例授权
			for _, res := range []struct{ schema, table string }{
				{"", ""},
				{"jaysql", ""},
				{"jaysql", "insight_users"},
			} {
				ok, err := HasDataGrant(testScope(), req.InstanceID, res.schema, res.table, DataActionSelect)
				if err != nil {
					t.Fatalf("HasDataGrant(%s,%s): %v", res.schema, res.table, err)
				}
				if !ok {
					t.Fatalf("整实例授权未覆盖 %s.%s", res.schema, res.table)
				}
			}
		})
	}
}

// 非整实例（表级 / 库级）却拿到空快照，属于脏数据，仍必须报错，
// 不能因为放宽整实例就把「什么都圈不到」的单子放行。
func TestGrantAccessRejectsEmptySpecForNonInstanceLevel(t *testing.T) {
	db := setupDataGrantTestDB(t)

	for _, level := range []string{DataResourceLevelTable, DataResourceLevelDatabase, DataResourceLevelSchema} {
		req := instanceLevelRequest("")
		req.RequestNo = "ra-" + level
		req.ResourceLevel = level
		if _, err := grantAccessForRequest(db, req, nil, "maoti", "", "127.0.0.1"); err == nil {
			t.Fatalf("%s 级申请缺资源时不应通过校验", level)
		}
	}
}
