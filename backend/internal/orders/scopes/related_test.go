package scopes

import (
	"strings"
	"testing"
)

func TestRelatedConditionArgsAreAllUsername(t *testing.T) {
	condition, args := RelatedCondition("alice")
	if condition == "" {
		t.Fatal("condition 不应为空")
	}
	// 占位符数量必须与参数个数一致，否则 gorm 会报 "sql: expected N arguments"。
	if got, want := strings.Count(condition, "?"), len(args); got != want {
		t.Fatalf("占位符数量 %d 与参数个数 %d 不一致", got, want)
	}
	if len(args) != 5 {
		t.Fatalf("期望 5 个参数（applicant/executor/cc/approver/log 操作人），实际 %d", len(args))
	}
	for i, arg := range args {
		if arg != "alice" {
			t.Fatalf("第 %d 个参数期望 alice，实际 %v", i, arg)
		}
	}
}

func TestRelatedConditionCoversAllFiveRoles(t *testing.T) {
	condition, _ := RelatedCondition("alice")
	mustContain := []string{
		"a.applicant = ?",
		"a.executor = ?",
		"JSON_CONTAINS(IFNULL(a.cc",
		"insight_approval_records ar2",
		// 兜底：历史操作人。修掉「审批人转交/驳回重提交后从与我相关消失」。
		"insight_order_logs l2",
	}
	for _, fragment := range mustContain {
		if !strings.Contains(condition, fragment) {
			t.Errorf("条件片段缺失: %s", fragment)
		}
	}
	if strings.Count(condition, "EXISTS") != 2 {
		t.Errorf("期望 2 个 EXISTS 子查询（审批记录 + 操作日志），实际 %d", strings.Count(condition, "EXISTS"))
	}
}

func TestRelatedConditionTrimsUsername(t *testing.T) {
	_, args := RelatedCondition("  bob  ")
	for i, arg := range args {
		if arg != "bob" {
			t.Fatalf("第 %d 个参数期望去掉首尾空格的 bob，实际 %v", i, arg)
		}
	}
}

func TestRelatedConditionEmptyUsernameIsNeverTrue(t *testing.T) {
	condition, args := RelatedCondition("   ")
	if condition != "1 = 0" {
		t.Fatalf("空用户名应返回恒假条件，实际 %q", condition)
	}
	if len(args) != 0 {
		t.Fatalf("恒假条件不应带参数，实际 %v", args)
	}
}

func TestScopeConstants(t *testing.T) {
	cases := map[Scope]string{
		ScopeAll:                "ALL",
		ScopeMyRelated:          "MY_RELATED",
		ScopeMySubmitted:        "MY_SUBMITTED",
		ScopeMyCC:               "MY_CC",
		ScopePendingMyApproval:  "PENDING_MY_APPROVAL",
		ScopeMyApproved:         "MY_APPROVED",
		ScopePendingMyExecution: "PENDING_MY_EXECUTION",
		ScopeMyExecuted:         "MY_EXECUTED",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("作用域常量值漂移：期望 %s，实际 %s", want, string(got))
		}
	}
}
