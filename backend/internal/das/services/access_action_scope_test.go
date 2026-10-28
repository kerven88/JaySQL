package services

import (
	"testing"

	"github.com/simajiong/jaysql/internal/das/models"
)

// TestActionNeedsAccessGrant 三类接口口径：只有查询需要数据访问授权。
//
// 这条口径是「同一件事不审两遍」的落点：变更 / 导出由 SQL 工单的审批流程覆盖，
// 若这里回退成 true，工单执行链路会重新被数据授权拦住。
func TestActionNeedsAccessGrant(t *testing.T) {
	cases := []struct {
		action string
		want   bool
		scope  string
	}{
		{DataActionSelect, true, AccessAPIScopeQuery},
		{DataActionDML, false, AccessAPIScopeChange},
		{DataActionExport, false, AccessAPIScopeExport},
		// 未知值/空值必须按查询处理（默认收紧），否则新增维度会被静默放行
		{"", true, AccessAPIScopeQuery},
		{"ddl", false, AccessAPIScopeChange},
		{"UNKNOWN", true, AccessAPIScopeQuery},
		{" SELECT ", true, AccessAPIScopeQuery},
	}
	for _, c := range cases {
		if got := ActionNeedsAccessGrant(c.action); got != c.want {
			t.Fatalf("ActionNeedsAccessGrant(%q) = %v, want %v", c.action, got, c.want)
		}
		if got := AccessAPIScopeOfAction(c.action); got != c.scope {
			t.Fatalf("AccessAPIScopeOfAction(%q) = %q, want %q", c.action, got, c.scope)
		}
	}
}

// TestBuildAccessRequestPermissions 详情页按钮可用性矩阵。
func TestBuildAccessRequestPermissions(t *testing.T) {
	newReq := func(status, applicant, approver string) *models.InsightDasAccessRequest {
		return &models.InsightDasAccessRequest{Status: status, ApplicantUsername: applicant, ApproverUsername: approver}
	}

	// 申请人视角（审批中）：撤回/终止/类似创建可用，审批三连灰置
	p := buildAccessRequestPermissions(newReq(AccessRequestStatusPending, "zhangsan", "lisi"), "zhangsan", true, false)
	if !p.IsApplicant || p.IsCurrentApprover {
		t.Fatalf("身份判定错误: %+v", p)
	}
	if p.CanApprove || p.CanReject || p.CanForward {
		t.Fatalf("申请人不能审批自己的单: %+v", p)
	}
	if !p.CanWithdraw || !p.CanTerminate || !p.CanCreateSimilar {
		t.Fatalf("申请人应可撤回/终止/类似创建: %+v", p)
	}

	// 非当前处理人的审批人：审批三连同样灰置（转交后原审批人失权）
	p = buildAccessRequestPermissions(newReq(AccessRequestStatusForwarded, "zhangsan", "zhaoliu"), "lisi", true, false)
	if p.CanApprove || p.CanReject || p.CanForward || p.CanWithdraw || p.CanTerminate {
		t.Fatalf("非当前处理人不应有任何处理权: %+v", p)
	}
	if !p.CanCreateSimilar {
		t.Fatalf("类似创建应恒可用: %+v", p)
	}

	// 当前处理人：审批三连可用，撤回/终止不可用
	p = buildAccessRequestPermissions(newReq(AccessRequestStatusPending, "zhangsan", "lisi"), "lisi", true, false)
	if !p.CanApprove || !p.CanReject || !p.CanForward {
		t.Fatalf("当前处理人应可审批: %+v", p)
	}
	if p.CanWithdraw || p.CanTerminate {
		t.Fatalf("审批人不能撤回/终止他人的申请: %+v", p)
	}

	// 待执行授权：有审批权的人可执行授权，但申请人自己不行
	p = buildAccessRequestPermissions(newReq(AccessRequestStatusWaitGrant, "zhangsan", "lisi"), "lisi", true, false)
	if !p.CanGrant {
		t.Fatalf("待执行授权时审批人应可执行授权: %+v", p)
	}
	p = buildAccessRequestPermissions(newReq(AccessRequestStatusWaitGrant, "zhangsan", "lisi"), "zhangsan", true, false)
	if p.CanGrant {
		t.Fatalf("申请人不能给自己的单执行授权: %+v", p)
	}

	// 管理员旁路：可处理他人的单，但仍不能批自己的
	p = buildAccessRequestPermissions(newReq(AccessRequestStatusPending, "zhangsan", "lisi"), "admin", true, true)
	if !p.CanApprove || !p.CanForward {
		t.Fatalf("管理员应可处理他人申请: %+v", p)
	}
	p = buildAccessRequestPermissions(newReq(AccessRequestStatusPending, "admin", "lisi"), "admin", true, true)
	if p.CanApprove {
		t.Fatalf("管理员也不能批自己的申请: %+v", p)
	}
}
