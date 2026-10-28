package services

import "testing"

func TestRestorePhysicalRiskLevel(t *testing.T) {
	if got := restorePhysicalRiskLevel("EXISTING_INSTANCE"); got != "high" {
		t.Fatalf("覆盖已有实例应为 high，实际 %s", got)
	}
	if got := restorePhysicalRiskLevel("NEW_INSTANCE"); got != "medium" {
		t.Fatalf("新建实例应为 medium，实际 %s", got)
	}
}

// TestPrecheckRestoreRunServiceRejectsEmptyForm 守卫：空请求体必须显式报错，
// 不能因为解引用 nil 让 gin.Recovery 断连（表现为前端「连接已断开」）。
func TestPrecheckRestoreRunServiceRejectsEmptyForm(t *testing.T) {
	var svc *PrecheckRestoreRunService
	if _, err := svc.Run(); err == nil {
		t.Fatal("nil service 应返回错误")
	}
	empty := &PrecheckRestoreRunService{}
	if _, err := empty.Run(); err == nil {
		t.Fatal("空表单应返回错误")
	}
}

