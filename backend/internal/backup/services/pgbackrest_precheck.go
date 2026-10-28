package services

import (
	"fmt"
	"path/filepath"
	"strings"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
)

func validatePGBackRestFixedChecks(instance *commonModels.InsightInstances, task *backupModels.InsightBackupTasks, executorNode *backupModels.InsightBackupNodes, repositoryNode *backupModels.InsightBackupNodes, serverVersion string, repoRemote bool) error {
	if executorNode == nil {
		return fmt.Errorf("执行节点不存在")
	}
	if !strings.EqualFold(strings.TrimSpace(string(executorNode.Status)), "ONLINE") {
		return newPhysicalPrepareError(
			"executor_online",
			"PG 物理备份执行节点离线",
			"请先恢复对应备份节点 Agent 在线，再重新点击“立即执行”。",
		).WithHost(strings.TrimSpace(executorNode.Host)).
			WithDetail(fmt.Sprintf("executor_node_id=%d host=%s status=%s", executorNode.ID, strings.TrimSpace(executorNode.Host), strings.TrimSpace(string(executorNode.Status))))
	}
	if err := validatePGBackRestExecutorPrecheck(instance, task, executorNode, serverVersion); err != nil {
		return err
	}
	if repoRemote && repositoryNode != nil && !sameBackupNodeHost(executorNode, repositoryNode) {
		if err := validatePGBackRestRepositoryToolPrecheck(repositoryNode, serverVersion); err != nil {
			return err
		}
	}
	_ = instance
	return nil
}

func validatePGBackRestExecutorPrecheck(instance *commonModels.InsightInstances, task *backupModels.InsightBackupTasks, executorNode *backupModels.InsightBackupNodes, serverVersion string) error {
	cfg, err := buildExecutorNodeRemoteConfigWithInstance(executorNode, instance)
	if err != nil {
		return newPhysicalPrepareError(
			"executor_config",
			"无法读取 PG 物理备份执行节点 SSH 配置",
			"请在「系统管理 -> 备份节点」中检查执行节点的 SSH 地址、用户名和密码/私钥配置。",
		).WithHost(strings.TrimSpace(executorNode.Host)).WithRaw(err)
	}
	runner, err := newSSHRunner(cfg)
	if err != nil {
		return newPhysicalPrepareError(
			"executor_connect",
			"无法连接 PG 物理备份执行节点",
			"请检查执行节点网络、SSH 端口和登录凭据是否正确。",
		).WithHost(strings.TrimSpace(executorNode.Host)).WithRaw(err)
	}
	defer runner.Close()

	if output, runErr := runner.Run(strings.Join([]string{
		"set -e",
		"sudo -n true",
		"echo PRECHECK_OK:sudo",
	}, "\n")); runErr != nil {
		return newPhysicalPrepareError(
			"executor_sudo",
			"执行节点缺少 passwordless sudo 权限",
			"请为执行节点 SSH 用户配置 sudo NOPASSWD；PG 物理备份需要通过 sudo 切换 root/postgres 并修正运行目录权限。",
		).WithHost(strings.TrimSpace(executorNode.Host)).
			WithDetail(strings.TrimSpace(output)).
			WithRaw(runErr)
	}

	if err := verifyRemotePostgresTool(runner, strings.TrimSpace(executorNode.Host), defaultBackupNodeToolsDir(executorNode.ToolsDir), serverVersion, "pgbackrest", "executor_tool_pgbackrest",
		"执行节点缺少 pgBackRest 工具",
		"请在执行节点安装 PostgreSQL 对应版本的 pgBackRest，或发布到 tools/postgres/<major>/ 目录后再重试。"); err != nil {
		return err
	}
	if err := verifyRemotePostgresTool(runner, strings.TrimSpace(executorNode.Host), defaultBackupNodeToolsDir(executorNode.ToolsDir), serverVersion, "psql", "executor_tool_psql",
		"执行节点缺少 psql 工具",
		"请在执行节点安装 PostgreSQL 对应版本的 psql 客户端，或发布到 tools/postgres/<major>/ 目录后再重试。"); err != nil {
		return err
	}

	outputBaseDir := strings.TrimSpace(task.OutputDir)
	if outputBaseDir == "" {
		return nil
	}
	scratchDir := filepath.Join(
		resolveBackupStorageBaseDir(outputBaseDir, "PG_BACKREST", "PostgreSQL"),
		fmt.Sprintf("%d", task.SourceInstanceID),
		sanitizePathSegment(task.TaskID, "task"),
		"__precheck__",
	)
	script := strings.Join([]string{
		"set -e",
		"sudo -n true",
		"CURRENT_USER=$(id -un 2>/dev/null || echo " + shellQuote(strings.TrimSpace(cfg.SSHUser)) + ")",
		"CURRENT_GROUP=$(id -gn \"$CURRENT_USER\" 2>/dev/null || echo \"$CURRENT_USER\")",
		"DB_USER=$(id -un postgres 2>/dev/null || echo postgres)",
		"DB_GROUP=$(id -gn \"$DB_USER\" 2>/dev/null || echo \"$DB_USER\")",
		"mkdir -p " + shellQuote(scratchDir),
		"sudo -n chown \"${DB_USER}:${CURRENT_GROUP}\" " + shellQuote(scratchDir) + " 2>/dev/null || sudo -n chown \"${DB_USER}:${DB_GROUP}\" " + shellQuote(scratchDir),
		"sudo -n chmod 2775 " + shellQuote(scratchDir),
		"touch " + shellQuote(filepath.Join(scratchDir, ".agent_write")),
		"rm -f " + shellQuote(filepath.Join(scratchDir, ".agent_write")),
		"sudo -n -u \"$DB_USER\" /bin/sh -lc " + shellQuote("touch "+shellQuote(filepath.Join(scratchDir, ".db_write"))+" && rm -f "+shellQuote(filepath.Join(scratchDir, ".db_write"))),
		"echo PRECHECK_OK:runtime_dir",
	}, "\n")
	if output, runErr := runner.Run(script); runErr != nil {
		return newPhysicalPrepareError(
			"executor_runtime_dir",
			"执行节点 PG 物理备份运行目录权限预检失败",
			"请检查输出目录是否可创建，并确保 Agent 用户与 postgres 用户都能在该目录下协作写入。",
		).WithHost(strings.TrimSpace(executorNode.Host)).
			WithDetail(fmt.Sprintf("scratch_dir=%s output=%s", scratchDir, strings.TrimSpace(output))).
			WithRaw(runErr)
	}
	return nil
}

func validatePGBackRestRepositoryToolPrecheck(repositoryNode *backupModels.InsightBackupNodes, serverVersion string) error {
	cfg, err := buildStoredBackupNodeRemoteConfig(repositoryNode)
	if err != nil {
		return newPhysicalPrepareError(
			"repository_tool_config",
			"无法读取仓库节点工具配置",
			"请检查仓库节点 SSH 信息和工具目录配置是否完整。",
		).WithHost(strings.TrimSpace(repositoryNode.Host)).WithRaw(err)
	}
	runner, err := newSSHRunner(cfg)
	if err != nil {
		return newPhysicalPrepareError(
			"repository_tool_connect",
			"无法连接仓库节点检查 pgBackRest 工具",
			"请检查仓库节点 SSH 地址、端口和凭据是否正确。",
		).WithHost(strings.TrimSpace(repositoryNode.Host)).WithRaw(err)
	}
	defer runner.Close()

	if err := verifyRemotePostgresTool(runner, strings.TrimSpace(repositoryNode.Host), defaultBackupNodeToolsDir(repositoryNode.ToolsDir), serverVersion, "pgbackrest", "repository_tool_pgbackrest",
		"仓库节点缺少可用的 pgBackRest 工具",
		"请在仓库节点的 tools/postgres/<major>/ 目录中补齐 pgBackRest 及其依赖库后重试。"); err != nil {
		return err
	}
	return nil
}

func verifyRemotePostgresTool(runner *sshRunner, host string, toolsDir string, serverVersion string, toolName string, stage string, title string, suggestion string) error {
	candidates := remotePostgresToolCandidates(toolsDir, serverVersion, toolName)
	toolToken := "__PATH_LOOKUP__:" + toolName
	quotedCandidates := make([]string, 0, len(candidates)+1)
	for _, candidate := range candidates {
		quotedCandidates = append(quotedCandidates, shellQuote(candidate))
	}
	quotedCandidates = append(quotedCandidates, shellQuote(toolToken))
	script := strings.Join([]string{
		"set -e",
		"FOUND=''",
		"for candidate in " + strings.Join(quotedCandidates, " ") + "; do",
		"  if [ \"$candidate\" = " + shellQuote(toolToken) + " ]; then",
		"    if command -v " + shellQuote(toolName) + " >/dev/null 2>&1; then FOUND=$(command -v " + shellQuote(toolName) + "); break; fi",
		"  elif [ -x \"$candidate\" ]; then",
		"    FOUND=$candidate; break",
		"  fi",
		"done",
		"if [ -z \"$FOUND\" ]; then echo " + shellQuote("candidates="+strings.Join(candidates, ",")) + "; exit 41; fi",
		"LIB_PATHS=''",
		"for libdir in \"$(dirname \"$FOUND\")/lib\" \"$(dirname \"$(dirname \"$FOUND\")\")/lib\" \"$(dirname \"$(dirname \"$(dirname \"$FOUND\")\")\")/lib\"; do",
		"  if [ -d \"$libdir\" ]; then",
		"    if [ -n \"$LIB_PATHS\" ]; then LIB_PATHS=\"$LIB_PATHS:$libdir\"; else LIB_PATHS=\"$libdir\"; fi",
		"  fi",
		"done",
		"if [ -n \"$LIB_PATHS\" ]; then env LD_LIBRARY_PATH=\"$LIB_PATHS\" \"$FOUND\" --version >/dev/null 2>&1; else \"$FOUND\" --version >/dev/null 2>&1; fi",
		"echo FOUND=$FOUND",
	}, "\n")
	if output, runErr := runner.Run(script); runErr != nil {
		detail := strings.TrimSpace(output)
		if provisionSummary, provisionErr := ensureRemoteBundledPostgresTools(runner, toolsDir, serverVersion, []string{toolName}); provisionErr == nil {
			verifyOutput, verifyErr := runner.Run(script)
			if verifyErr == nil {
				return nil
			}
			detail = strings.TrimSpace(strings.Join([]string{
				strings.TrimSpace(output),
				"auto_provision=" + provisionSummary,
				"post_verify=" + strings.TrimSpace(verifyOutput),
			}, "\n"))
			runErr = fmt.Errorf("%w; auto provision succeeded but post-verify failed: %v", runErr, verifyErr)
		} else {
			detail = strings.TrimSpace(strings.Join([]string{
				strings.TrimSpace(output),
				"auto_provision_error=" + provisionErr.Error(),
			}, "\n"))
		}
		return newPhysicalPrepareError(stage, title, suggestion).
			WithHost(strings.TrimSpace(host)).
			WithDetail(detail).
			WithRaw(runErr)
	}
	return nil
}

func remotePostgresToolCandidates(toolsDir string, serverVersion string, toolName string) []string {
	versions := postgresClientVersionCandidates(serverVersion)
	if requiresPostgresExactMajor(toolName) {
		if exact := postgresExactClientVersion(serverVersion); exact != "" {
			versions = []string{exact}
		}
	}
	out := make([]string, 0, 16)
	seen := map[string]struct{}{}
	add := func(path string) {
		v := strings.TrimSpace(path)
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	base := defaultBackupNodeToolsDir(toolsDir)
	for _, version := range versions {
		for _, candidate := range toolCandidatePaths(base, "postgres", version, toolName) {
			add(candidate)
		}
	}
	for _, version := range versions {
		for _, candidate := range systemToolCandidatePaths("postgres", version, toolName) {
			add(candidate)
		}
	}
	return out
}
