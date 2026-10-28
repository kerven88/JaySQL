package services

import (
	"context"
	"testing"
)

func TestBuildUnifiedBackupExecutionPlanForPrecheckRejectsNilContext(t *testing.T) {
	plan, err := buildUnifiedBackupExecutionPlanForPrecheck(nil)
	if err == nil {
		t.Fatalf("expected error, got nil plan=%v", plan)
	}
}

func TestBuildUnifiedBackupExecutionPlanForRunRejectsUnsupportedDriver(t *testing.T) {
	plan, err := buildUnifiedBackupExecutionPlanForRun(context.TODO(), BackupExecutionInput{
		DBType:        "MySQL",
		DriverType:    "UNKNOWN",
		BackupType:    "FULL",
		ObjectMode:    "ALL",
		OutputBaseDir: "/tmp/goinsight-test",
	})
	if err == nil {
		t.Fatalf("expected error, got nil plan=%v", plan)
	}
}

func TestMarshalAnyJSONNil(t *testing.T) {
	if data := marshalAnyJSON(nil); data != nil {
		t.Fatalf("expected nil json, got %q", string(data))
	}
}
