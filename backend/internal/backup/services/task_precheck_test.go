package services

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
)

func TestBuildLogicalToolInspectionScriptWithUserConfigUsesTargetHomeAndLoginLikeSudo(t *testing.T) {
	ctx := &backupTaskPrecheckContext{
		Instance: &commonModels.InsightInstances{
			Hostname: "192.168.1.99",
			Port:     5433,
			DbType:   commonModels.EnumType("PostgreSQL"),
		},
		ServerVersion: "16.2",
		LogicalConfig: logicalBackupDriverConfig{
			OSUser:   "postgres",
			ToolPath: "pg_dump",
		},
	}

	script := buildLogicalToolInspectionScriptWithUserConfig(ctx)
	if !strings.Contains(script, `TARGET_HOME=$(awk -F: -v u="$TARGET_TOOL_USER" '$1==u{print $6; exit}' /etc/passwd 2>/dev/null || true)`) {
		t.Fatalf("expected script to resolve target home, got: %s", script)
	}
	if strings.Contains(script, `sudo -n -H -u "$TARGET_TOOL_USER" -- env HOME="$TARGET_HOME" /bin/sh -lc "$SHELL_INIT && $CMD"`) {
		t.Fatalf("expected old sudo invocation to be removed, got: %s", script)
	}
	if strings.Contains(script, `SHELL_INIT="$SHELL_INIT" CMD="$CMD" /bin/sh -lc 'eval "$SHELL_INIT"; eval "$CMD"'`) {
		t.Fatalf("expected env/eval wrapper to be removed, got: %s", script)
	}
	if !strings.Contains(script, `cat <<'__GOINSIGHT_LOGICAL_TOOL__' >"$TARGET_SCRIPT_FILE"`) {
		t.Fatalf("expected script to materialize target script file before execution, got: %s", script)
	}
	if !strings.Contains(script, `chmod 644 "$TARGET_SCRIPT_FILE"`) {
		t.Fatalf("expected target script file to be readable by target user, got: %s", script)
	}
	if !strings.Contains(script, `sudo -n -H -u "$TARGET_TOOL_USER" -- env HOME="$TARGET_HOME" PHASE_FILE="$PHASE_FILE" /bin/sh "$TARGET_SCRIPT_FILE" >"$TOOL_OUTPUT_FILE" 2>&1`) {
		t.Fatalf("expected script to switch user with target HOME and execute target script file, got: %s", script)
	}
	if strings.Contains(script, `run_as_target_user >"$TOOL_OUTPUT_FILE" 2>&1`) {
		t.Fatalf("expected old run_as_target_user redirection pattern to be gone, got: %s", script)
	}
}

func TestBuildLogicalToolInspectionScriptWithUserConfigUsesToolLookupSnippetForNamedCommand(t *testing.T) {
	ctx := &backupTaskPrecheckContext{
		Instance: &commonModels.InsightInstances{
			Hostname: "192.168.1.99",
			Port:     5433,
			DbType:   commonModels.EnumType("PostgreSQL"),
		},
		ServerVersion: "16.2",
		LogicalConfig: logicalBackupDriverConfig{
			OSUser:   "postgres",
			ToolPath: "pg_dump",
		},
	}

	script := buildLogicalToolInspectionScriptWithUserConfig(ctx)
	if !strings.Contains(script, `INPUT_TOOL_PATH='pg_dump'`) {
		t.Fatalf("expected script to preserve raw input tool path, got: %s", script)
	}
	if !strings.Contains(script, `TOOL_NAME="$INPUT_TOOL_PATH"`) {
		t.Fatalf("expected named tool to reuse lookup snippet, got: %s", script)
	}
	if !strings.Contains(script, `"/usr/local/pgsql/bin/$TOOL_NAME"`) {
		t.Fatalf("expected postgres lookup candidates to be included for named tool, got: %s", script)
	}
	if strings.Contains(script, `RESOLVED_TOOL_PATH=$(command -v "$TOOL_PATH" 2>/dev/null || true)`) {
		t.Fatalf("expected legacy command -v only branch to be removed for named tool input, got: %s", script)
	}
}

func TestBuildLogicalSourceRuntimeInspectionScriptUsesProcessHomeAndLoginLikeSudo(t *testing.T) {
	ctx := &backupTaskPrecheckContext{
		Instance: &commonModels.InsightInstances{
			Hostname: "192.168.1.99",
			Port:     5433,
			DbType:   commonModels.EnumType("PostgreSQL"),
		},
	}

	script := buildLogicalSourceRuntimeInspectionScript(ctx)
	if !strings.Contains(script, `PROCESS_HOME=$(awk -F: -v u="$PROCESS_USER" '$1==u{print $6; exit}' /etc/passwd 2>/dev/null || true)`) {
		t.Fatalf("expected script to resolve process user home, got: %s", script)
	}
	if strings.Contains(script, `sudo -n -H -u "$PROCESS_USER" -- env HOME="$PROCESS_HOME" /bin/sh -lc "$SHELL_INIT && $CMD"`) {
		t.Fatalf("expected old process-user sudo invocation to be removed, got: %s", script)
	}
	if !strings.Contains(script, `sudo -n -H -u "$PROCESS_USER" -- env HOME="$PROCESS_HOME" SHELL_INIT="$SHELL_INIT" CMD="$CMD" /bin/sh -lc 'eval "$SHELL_INIT"; eval "$CMD"'`) {
		t.Fatalf("expected script to switch process user with target HOME and deferred shell expansion, got: %s", script)
	}
}

func TestBuildLogicalToolInspectionScriptWithUserConfigExecutesForCurrentUser(t *testing.T) {
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
			ToolPath: "pg_dump",
		},
	}

	script := buildLogicalToolInspectionScriptWithUserConfig(ctx)
	scriptPath := filepath.Join(tempDir, "logical-tool-check.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write generated script: %v", err)
	}

	cmd := exec.Command("/bin/sh", scriptPath)
	cmd.Env = append(os.Environ(), "PATH="+tempDir+":"+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("execute generated script: %v\noutput:\n%s", err, string(output))
	}
	out := string(output)
	if !strings.Contains(out, "DEBUG_TOOL_STATUS=0") {
		t.Fatalf("expected successful debug tool status, got:\n%s", out)
	}
	if !strings.Contains(out, "TOOL_PATH="+fakeToolPath) {
		t.Fatalf("expected resolved fake tool path, got:\n%s", out)
	}
	if !strings.Contains(out, "VERSION_LINE=pg_dump (PostgreSQL) 16.2-test") {
		t.Fatalf("expected fake version line, got:\n%s", out)
	}
	if !strings.Contains(out, "DEBUG_PHASE_TRACE=") {
		t.Fatalf("expected phase trace in output, got:\n%s", out)
	}
}

func TestDebugRunLogicalToolInspectionScriptAgainstRemote(t *testing.T) {
	if os.Getenv("GOINSIGHT_DEBUG_REMOTE") != "1" {
		t.Skip("set GOINSIGHT_DEBUG_REMOTE=1 to run remote debug test")
	}

	ctx := &backupTaskPrecheckContext{
		Instance: &commonModels.InsightInstances{
			Hostname: "192.168.1.99",
			Port:     5433,
			DbType:   commonModels.EnumType("PostgreSQL"),
		},
		ServerVersion: "16.2",
		LogicalConfig: logicalBackupDriverConfig{
			OSUser:   "postgres",
			ToolPath: "pg_dump",
		},
	}
	runner, err := newSSHRunner(&backupNodeRemoteConfig{
		Host:        "192.168.1.99",
		Port:        22,
		SSHUser:     "root",
		// 密码不写进仓库，改从环境变量读取（本测试默认 skip，仅调试时手动开启）
		SSHPassword: os.Getenv("BACKUP_DEBUG_SSH_PASS"),
	})
	if err != nil {
		t.Fatalf("new ssh runner: %v", err)
	}
	defer runner.Close()

	traceID := logicalToolDebugTraceID(ctx)
	phaseFile := logicalToolDebugPhaseFile(traceID)
	script := buildLogicalToolInspectionScriptWithUserConfig(ctx)
	output, runErr := runner.Run(script)
	phaseTrace, _ := readRemoteDebugTraceFile(runner, phaseFile)
	t.Logf("trace_id=%s", traceID)
	t.Logf("phase_file=%s", phaseFile)
	t.Logf("run_err=%v", runErr)
	t.Logf("output=%s", output)
	t.Logf("phase_trace=%s", phaseTrace)
}
