package services

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
)

func TestBuildLogicalToolResolveProbeScriptUsesLookupSnippetForNamedPostgresCommand(t *testing.T) {
	script := buildLogicalToolResolveProbeScript("PostgreSQL", "16.2", "pg_dump")
	if !strings.Contains(script, `INPUT_TOOL_PATH='pg_dump'`) {
		t.Fatalf("expected raw input tool path in probe script, got: %s", script)
	}
	if !strings.Contains(script, `TOOL_NAME="$INPUT_TOOL_PATH"`) {
		t.Fatalf("expected named command to reuse lookup snippet, got: %s", script)
	}
	if !strings.Contains(script, `"/usr/local/pgsql/bin/$TOOL_NAME"`) {
		t.Fatalf("expected postgres lookup candidates in probe script, got: %s", script)
	}
}

func TestBuildRemoteLogicalToolResolveScriptRunsTargetScriptDirectly(t *testing.T) {
	script := buildRemoteLogicalToolResolveScript("postgres", "echo TOOL_PATH=/usr/local/pgsql/bin/pg_dump")
	if !strings.Contains(script, `cat <<'__GOINSIGHT_LOGICAL_TOOL__' >"$TARGET_SCRIPT_FILE"`) {
		t.Fatalf("expected remote wrapper to materialize target script file, got: %s", script)
	}
	if !strings.Contains(script, `sudo -n -H -u "$TARGET_TOOL_USER" -- env HOME="$TARGET_HOME" /bin/sh "$TARGET_SCRIPT_FILE"`) {
		t.Fatalf("expected remote wrapper to execute target script directly via sudo, got: %s", script)
	}
	if strings.Contains(script, `TOOL_OUTPUT_FILE`) {
		t.Fatalf("expected old file replay wrapper to be removed, got: %s", script)
	}
	if !strings.Contains(script, `if [ -n "$TARGET_TOOL_USER" ] && id -u "$TARGET_TOOL_USER" >/dev/null 2>&1; then TARGET_USER_EXISTS=1; fi`) {
		t.Fatalf("expected remote wrapper to validate target user existence, got: %s", script)
	}
	if !strings.Contains(script, `append_output "TOOL_ERROR=target_tool_user_not_found"`) {
		t.Fatalf("expected remote wrapper to emit explicit missing-user error, got: %s", script)
	}
	if !strings.Contains(script, `append_output() { printf '%s\n' "$1" >>"$TARGET_OUTPUT_FILE"; }`) {
		t.Fatalf("expected remote wrapper to persist output before replay, got: %s", script)
	}
	if !strings.Contains(script, `cat "$TARGET_OUTPUT_FILE" 2>/dev/null || true`) {
		t.Fatalf("expected remote wrapper to replay output file at the end, got: %s", script)
	}
	if !strings.Contains(script, `if [ ! -s "$TARGET_OUTPUT_FILE" ]; then append_output "TOOL_ERROR=remote_probe_output_empty"; fi`) {
		t.Fatalf("expected remote wrapper to emit explicit empty-output marker, got: %s", script)
	}
}

func TestResolveLogicalBackupToolLocallyUsesCurrentUserConfiguredPath(t *testing.T) {
	currentUser := strings.TrimSpace(currentOSUser())
	if currentUser == "" {
		t.Fatal("expected current os user to be available")
	}

	tempDir := t.TempDir()
	fakeToolPath := filepath.Join(tempDir, "pg_dump")
	fakeTool := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo 'pg_dump (PostgreSQL) 16.2-test'\n  exit 0\nfi\nexit 0\n"
	if err := os.WriteFile(fakeToolPath, []byte(fakeTool), 0o755); err != nil {
		t.Fatalf("write fake pg_dump: %v", err)
	}

	resolution, err := resolveLogicalBackupToolLocally(context.Background(), "PostgreSQL", "16.2", logicalBackupDriverConfig{
		OSUser:   currentUser,
		ToolPath: fakeToolPath,
	})
	if err != nil {
		t.Fatalf("resolve logical backup tool locally: %v", err)
	}
	if resolution == nil || resolution.Spec == nil {
		t.Fatalf("expected resolved tool spec, got: %#v", resolution)
	}
	if got := strings.TrimSpace(resolution.Spec.Path); got != fakeToolPath {
		t.Fatalf("expected resolved path %s, got %s", fakeToolPath, got)
	}
	if got := strings.TrimSpace(resolution.ToolUser); got != currentUser {
		t.Fatalf("expected tool user %s, got %s", currentUser, got)
	}
	if got := strings.TrimSpace(resolution.VersionLine); got != "pg_dump (PostgreSQL) 16.2-test" {
		t.Fatalf("unexpected version line: %s", got)
	}
}

func TestResolveLogicalBackupToolForPrecheckUsesLocalResolverForLocalHost(t *testing.T) {
	currentUser := strings.TrimSpace(currentOSUser())
	if currentUser == "" {
		t.Fatal("expected current os user to be available")
	}

	tempDir := t.TempDir()
	fakeToolPath := filepath.Join(tempDir, "pg_dump")
	fakeTool := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo 'pg_dump (PostgreSQL) 16.2-test'\n  exit 0\nfi\nexit 0\n"
	if err := os.WriteFile(fakeToolPath, []byte(fakeTool), 0o755); err != nil {
		t.Fatalf("write fake pg_dump: %v", err)
	}

	ctx := &backupTaskPrecheckContext{
		Instance: &commonModels.InsightInstances{
			Hostname: "127.0.0.1",
			Port:     5433,
			DbType:   commonModels.EnumType("PostgreSQL"),
		},
		ServerVersion: "16.2",
		LogicalConfig: logicalBackupDriverConfig{
			OSUser:   currentUser,
			ToolPath: fakeToolPath,
		},
	}

	resolution, err := resolveLogicalBackupToolForPrecheck(ctx, ctx.LogicalConfig)
	if err != nil {
		t.Fatalf("resolve logical backup tool for local precheck: %v", err)
	}
	if resolution == nil || resolution.Spec == nil {
		t.Fatalf("expected resolved tool spec, got: %#v", resolution)
	}
	if got := strings.TrimSpace(resolution.Spec.Path); got != fakeToolPath {
		t.Fatalf("expected resolved path %s, got %s", fakeToolPath, got)
	}
}
