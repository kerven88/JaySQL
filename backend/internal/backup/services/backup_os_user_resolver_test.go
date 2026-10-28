package services

import (
	"testing"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"gorm.io/datatypes"
)

func TestResolveRemoteExecutionOSUserForPrecheckFallsBackToInstanceSSHUser(t *testing.T) {
	ctx := &backupTaskPrecheckContext{
		Instance: &commonModels.InsightInstances{
			ExtraConfig: datatypes.JSON(`{"ssh_user":"root"}`),
		},
	}
	gotUser, _, gotSource, err := resolveRemoteExecutionOSUserForPrecheck(ctx, "")
	if err != nil {
		t.Fatalf("resolveRemoteExecutionOSUserForPrecheck error = %v", err)
	}
	if gotUser != "root" {
		t.Fatalf("user = %q want root", gotUser)
	}
	if gotSource != "instance_ssh_user" {
		t.Fatalf("source = %q want instance_ssh_user", gotSource)
	}
}

func TestResolveLocalExecutionOSUserFallsBackToInstanceSSHUser(t *testing.T) {
	gotUser, gotSource := resolveLocalExecutionOSUser("", map[string]string{"ssh_user": "root"}, "mysql")
	if gotUser != "root" {
		t.Fatalf("user = %q want root", gotUser)
	}
	if gotSource != "instance_ssh_user" {
		t.Fatalf("source = %q want instance_ssh_user", gotSource)
	}
}
