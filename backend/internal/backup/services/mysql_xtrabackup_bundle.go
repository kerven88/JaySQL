package services

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type mysqlXtraBackupBundleManifest struct {
	Version    string   `json:"version"`
	TargetOS   string   `json:"target_os"`
	TargetArch string   `json:"target_arch"`
	Libc       string   `json:"libc"`
	Tools      []string `json:"tools"`
}

type mysqlXtraBackupBundle struct {
	Version      string
	RootDir      string
	ManifestPath string
	Manifest     mysqlXtraBackupBundleManifest
	Source       string
}

func mysqlPhysicalSupportedVersionFamily(serverVersion string) (string, error) {
	info := parseDBVersionInfo(serverVersion)
	if info.IsMariaDB {
		return "", fmt.Errorf("当前仅支持 MySQL 物理备份，不支持 MariaDB")
	}
	switch {
	case info.Major == 5 && info.Minor == 7:
		return "5.7", nil
	case info.Major == 8 && info.Minor == 0:
		return "8.0", nil
	case info.Major == 8 && info.Minor == 4:
		return "8.4", nil
	default:
		return "", fmt.Errorf("当前仅支持 MySQL 5.7 / 8.0 / 8.4 物理备份，实际版本=%s", strings.TrimSpace(serverVersion))
	}
}

func xtraBackupCompatibleServerFamily(toolVersion string) string {
	info := parseDBVersionInfo(toolVersion)
	switch {
	case info.Major == 2 && info.Minor == 4:
		return "5.7"
	case info.Major == 8 && info.Minor == 0:
		return "8.0"
	case info.Major == 8 && info.Minor == 4:
		return "8.4"
	default:
		return ""
	}
}

func validateMySQLPhysicalToolCompatibility(spec *localToolSpec, serverVersion string) error {
	if spec == nil {
		return fmt.Errorf("xtrabackup 工具信息为空")
	}
	expectedFamily, err := mysqlPhysicalSupportedVersionFamily(serverVersion)
	if err != nil {
		return err
	}
	versionLine := extractXtraBackupVersionLine(spec.Version)
	if versionLine == "" {
		versionLine = extractXtraBackupVersionLine(probeCommandVersion(spec.Path))
	}
	actualFamily := xtraBackupCompatibleServerFamily(versionLine)
	if actualFamily == "" {
		return fmt.Errorf("无法识别 xtrabackup 版本，工具=%s 原始版本=%s", strings.TrimSpace(spec.Path), strings.TrimSpace(versionLine))
	}
	if actualFamily != expectedFamily {
		return fmt.Errorf("xtrabackup 版本不兼容：服务端=%s，需要家族=%s，实际工具=%s（兼容家族=%s，路径=%s）", strings.TrimSpace(serverVersion), expectedFamily, strings.TrimSpace(versionLine), actualFamily, strings.TrimSpace(spec.Path))
	}
	return nil
}

func selectBundledMySQLXtraBackupBundle(serverVersion string, profile remoteRuntimeProfile, requiredTools []string) (*mysqlXtraBackupBundle, error) {
	version, err := mysqlPhysicalSupportedVersionFamily(serverVersion)
	if err != nil {
		return nil, err
	}
	bases := backupToolBaseDirs()
	searched := []string{}
	for _, base := range bases {
		versionRoot := filepath.Join(base, "mysql", version)
		candidateDirs := bundledPostgresToolCandidateDirs(versionRoot, profile)
		for _, candidateDir := range candidateDirs {
			searched = append(searched, candidateDir)
			bundle, ok := loadBundledMySQLXtraBackupBundle(candidateDir, versionRoot, version, profile, requiredTools)
			if ok {
				return bundle, nil
			}
		}
	}
	return nil, fmt.Errorf(
		"未找到 MySQL xtrabackup 工具包：xtrabackup_version=%s profile=%s required_tools=%s searched=%s",
		version,
		displayOrDefault(profile.BundleKey(), "generic"),
		strings.Join(uniqueStrings(requiredTools), ","),
		strings.Join(uniqueStrings(searched), ","),
	)
}

func loadBundledMySQLXtraBackupBundle(candidateDir string, versionRoot string, version string, profile remoteRuntimeProfile, requiredTools []string) (*mysqlXtraBackupBundle, bool) {
	info, err := os.Stat(candidateDir)
	if err != nil || !info.IsDir() {
		return nil, false
	}
	manifestPath := filepath.Join(candidateDir, "manifest.json")
	manifest, hasManifest, err := readMySQLXtraBackupBundleManifest(manifestPath)
	if err != nil {
		return nil, false
	}
	if hasManifest && !profileMatchesMySQLBundleManifest(profile, manifest) {
		return nil, false
	}
	if !hasManifest && candidateDir != versionRoot {
		base := filepath.Base(candidateDir)
		if base != profile.BundleKey() && base != profile.BundleKeyNoLibc() {
			return nil, false
		}
	}
	if !bundleContainsTools(candidateDir, requiredTools) {
		return nil, false
	}
	source := fmt.Sprintf("bundle:%s", candidateDir)
	if hasManifest {
		source = fmt.Sprintf("bundle:%s (%s/%s/%s)", candidateDir, displayOrDefault(manifest.TargetOS, "*"), displayOrDefault(manifest.TargetArch, "*"), displayOrDefault(manifest.Libc, "*"))
	}
	return &mysqlXtraBackupBundle{
		Version:      version,
		RootDir:      candidateDir,
		ManifestPath: manifestPath,
		Manifest:     manifest,
		Source:       source,
	}, true
}

func readMySQLXtraBackupBundleManifest(path string) (mysqlXtraBackupBundleManifest, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return mysqlXtraBackupBundleManifest{}, false, nil
		}
		return mysqlXtraBackupBundleManifest{}, false, err
	}
	var manifest mysqlXtraBackupBundleManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return mysqlXtraBackupBundleManifest{}, false, err
	}
	manifest.TargetOS = normalizeRuntimeOS(manifest.TargetOS)
	manifest.TargetArch = normalizeRuntimeArch(manifest.TargetArch)
	manifest.Libc = normalizeRuntimeLibc(manifest.Libc)
	return manifest, true, nil
}

func profileMatchesMySQLBundleManifest(profile remoteRuntimeProfile, manifest mysqlXtraBackupBundleManifest) bool {
	match := func(expected string, actual string) bool {
		expected = strings.TrimSpace(strings.ToLower(expected))
		actual = strings.TrimSpace(strings.ToLower(actual))
		return expected == "" || expected == "*" || expected == actual
	}
	if !match(manifest.TargetOS, profile.OS) {
		return false
	}
	if !match(manifest.TargetArch, profile.Arch) {
		return false
	}
	if !match(manifest.Libc, profile.Libc) {
		return false
	}
	return true
}

func ensureRemoteBundledMySQLXtraBackupTools(runner *sshRunner, remoteCfg *backupNodeRemoteConfig, serverVersion string, requiredTools []string) (string, error) {
	if runner == nil {
		return "", fmt.Errorf("SSH runner 不能为空")
	}
	if remoteCfg == nil {
		return "", fmt.Errorf("远端配置不能为空")
	}
	profile, err := detectRemoteRuntimeProfile(runner)
	if err != nil {
		return "", err
	}
	bundle, err := selectBundledMySQLXtraBackupBundle(serverVersion, profile, requiredTools)
	if err != nil {
		return "", err
	}
	if err := syncBundledMySQLXtraBackupBundle(runner, remoteCfg, bundle); err != nil {
		return "", err
	}
	return fmt.Sprintf("bundle=%s profile=%s target_version=%s required_tools=%s", bundle.Source, displayOrDefault(profile.BundleKey(), "generic"), bundle.Version, strings.Join(uniqueStrings(requiredTools), ",")), nil
}

func syncBundledMySQLXtraBackupBundle(runner *sshRunner, remoteCfg *backupNodeRemoteConfig, bundle *mysqlXtraBackupBundle) error {
	if runner == nil {
		return fmt.Errorf("SSH runner 不能为空")
	}
	if remoteCfg == nil {
		return fmt.Errorf("远端配置不能为空")
	}
	if bundle == nil {
		return fmt.Errorf("工具包不能为空")
	}
	archiveBytes, err := archiveDirectoryToTarGz(bundle.RootDir)
	if err != nil {
		return fmt.Errorf("打包 MySQL xtrabackup 工具包失败: %w", err)
	}
	toolsBase := defaultBackupNodeToolsDir(remoteCfg.ToolsDir)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	stagingParent := filepath.ToSlash(filepath.Join("/tmp", "goinsight-tools-staging", "mysql"))
	stagingDir := filepath.ToSlash(filepath.Join(stagingParent, bundle.Version+"-"+stamp))
	targetDir := filepath.ToSlash(filepath.Join(toolsBase, "mysql", bundle.Version))
	targetTmpDir := targetDir + ".tmp-" + stamp
	archivePath := filepath.ToSlash(filepath.Join(stagingParent, "bundle-"+bundle.Version+"-"+stamp+".tar.gz"))
	prepareScript := strings.Join([]string{
		"set -e",
		"mkdir -p " + shellQuote(stagingParent),
		"rm -rf " + shellQuote(stagingDir),
	}, "\n")
	if _, err := runner.Run(prepareScript); err != nil {
		return fmt.Errorf("准备远端 MySQL 工具目录失败: %w", err)
	}
	if err := runner.Upload(archivePath, archiveBytes); err != nil {
		return fmt.Errorf("上传 MySQL xtrabackup 工具包失败: %w", err)
	}
	installScript := strings.Join([]string{
		"set -e",
		"command -v tar >/dev/null 2>&1",
		"mkdir -p " + shellQuote(stagingDir),
		"tar -xzf " + shellQuote(archivePath) + " -C " + shellQuote(stagingDir),
		"[ -d " + shellQuote(stagingDir) + " ]",
		"mkdir -p " + shellQuote(filepath.ToSlash(filepath.Dir(targetDir))),
		"rm -rf " + shellQuote(targetTmpDir),
		"mv " + shellQuote(stagingDir) + " " + shellQuote(targetTmpDir),
		"rm -rf " + shellQuote(targetDir),
		"mv " + shellQuote(targetTmpDir) + " " + shellQuote(targetDir),
		"chmod -R a+rX " + shellQuote(targetDir),
		"rm -f " + shellQuote(archivePath),
		"echo INSTALLED=" + shellQuote(targetDir),
	}, "\n")
	if output, err := runner.Run(wrapRemoteRootScript(remoteCfg, installScript)); err != nil {
		return fmt.Errorf("安装 MySQL xtrabackup 工具包失败: %w output=%s", err, strings.TrimSpace(output))
	}
	return nil
}

func remoteMySQLPhysicalToolCandidates(toolsDir string, serverVersion string, toolName string) []string {
	version, err := mysqlPhysicalSupportedVersionFamily(serverVersion)
	if err != nil {
		return nil
	}
	return toolCandidatePaths(defaultBackupNodeToolsDir(toolsDir), "mysql", version, toolName)
}

func verifyRemoteMySQLPhysicalTool(runner *sshRunner, remoteCfg *backupNodeRemoteConfig, serverVersion string, cfg mysqlPhysicalDriverConfig, toolName string) (*localToolSpec, string, string, error) {
	if runner == nil {
		return nil, "", "", fmt.Errorf("SSH runner 不能为空")
	}
	if remoteCfg == nil {
		return nil, "", "", fmt.Errorf("远端配置不能为空")
	}
	requiredVersion, err := mysqlPhysicalSupportedVersionFamily(serverVersion)
	if err != nil {
		return nil, "", "", err
	}
	probeScript := buildMySQLPhysicalToolResolveProbeScript(serverVersion, cfg.ToolPath, remoteMySQLPhysicalToolCandidates(remoteCfg.ToolsDir, serverVersion, toolName), toolName)
	remoteScript := buildRemoteLogicalToolResolveScript(strings.TrimSpace(cfg.OSUser), probeScript)
	output, runErr := runner.Run(remoteScript)
	if runErr != nil && !strings.EqualFold(strings.TrimSpace(remoteCfg.SSHUser), "root") {
		output, runErr = runner.Run(wrapRemoteRootScript(remoteCfg, remoteScript))
	}
	spec, toolUser, resolveErr := finalizeRemoteMySQLPhysicalToolResolution(serverVersion, cfg, output, runErr)
	if resolveErr != nil && strings.TrimSpace(output) == "" {
		if diag := diagnoseRemoteMySQLPhysicalToolProbe(runner, remoteCfg, cfg, toolName); diag != "" {
			resolveErr = fmt.Errorf("%w detail=%s", resolveErr, diag)
		}
	}
	autoProvisionSummary := ""
	if resolveErr != nil && shouldAutoProvisionMySQLPhysicalTool(output, runErr, cfg) {
		autoProvisionSummary, err = ensureRemoteBundledMySQLXtraBackupTools(runner, remoteCfg, serverVersion, []string{toolName})
		if err != nil {
			detail := strings.TrimSpace(strings.Join([]string{
				strings.TrimSpace(output),
				"auto_provision_error=" + err.Error(),
			}, "\n"))
			return nil, autoProvisionSummary, toolUser, fmt.Errorf("远端未检测到可用 xtrabackup 工具: %v detail=%s", resolveErr, detail)
		}
		output, runErr = runner.Run(remoteScript)
		if runErr != nil && !strings.EqualFold(strings.TrimSpace(remoteCfg.SSHUser), "root") {
			output, runErr = runner.Run(wrapRemoteRootScript(remoteCfg, remoteScript))
		}
		spec, toolUser, resolveErr = finalizeRemoteMySQLPhysicalToolResolution(serverVersion, cfg, output, runErr)
		if resolveErr != nil && strings.TrimSpace(output) == "" {
			if diag := diagnoseRemoteMySQLPhysicalToolProbe(runner, remoteCfg, cfg, toolName); diag != "" {
				resolveErr = fmt.Errorf("%w detail=%s", resolveErr, diag)
			}
		}
		if resolveErr != nil {
			detail := strings.TrimSpace(strings.Join([]string{
				strings.TrimSpace(output),
				"auto_provision=" + autoProvisionSummary,
			}, "\n"))
			return nil, autoProvisionSummary, toolUser, fmt.Errorf("自动下发 xtrabackup 成功，但复检失败: %v detail=%s", resolveErr, detail)
		}
	}
	if resolveErr != nil {
		return nil, autoProvisionSummary, toolUser, resolveErr
	}
	if spec == nil {
		return nil, autoProvisionSummary, toolUser, fmt.Errorf("远端 xtrabackup 工具探测未返回工具信息")
	}
	toolsBase := defaultBackupNodeToolsDir(remoteCfg.ToolsDir)
	if strings.HasPrefix(filepath.Clean(spec.Path), filepath.Clean(filepath.Join(toolsBase, "mysql", requiredVersion))) {
		spec.Source = fmt.Sprintf("bundle:mysql/%s", requiredVersion)
	}
	return spec, autoProvisionSummary, toolUser, nil
}

func diagnoseRemoteMySQLPhysicalToolProbe(runner *sshRunner, remoteCfg *backupNodeRemoteConfig, cfg mysqlPhysicalDriverConfig, toolName string) string {
	if runner == nil || remoteCfg == nil {
		return ""
	}
	diagnoseScript := strings.Join([]string{
		"set +e",
		shellProfileInitSnippet(),
		`echo DIAG_USER=$(id -un 2>/dev/null || true)`,
		`echo DIAG_PATH=${PATH}`,
		`if command -v ` + shellQuote(strings.TrimSpace(firstNonEmpty(toolName, "xtrabackup"))) + ` >/dev/null 2>&1; then`,
		`  echo DIAG_TOOL=$(command -v ` + shellQuote(strings.TrimSpace(firstNonEmpty(toolName, "xtrabackup"))) + `)`,
		`else`,
		`  echo DIAG_TOOL=not_found`,
		`fi`,
		`echo DIAG_DONE=1`,
	}, "\n")
	remoteScript := buildRemoteLogicalToolResolveScript(strings.TrimSpace(cfg.OSUser), diagnoseScript)
	output, runErr := runner.Run(remoteScript)
	if runErr != nil && !strings.EqualFold(strings.TrimSpace(remoteCfg.SSHUser), "root") {
		output, runErr = runner.Run(wrapRemoteRootScript(remoteCfg, remoteScript))
	}
	if runErr != nil {
		if strings.TrimSpace(output) != "" {
			return strings.TrimSpace(output + "\nDIAG_ERROR=" + runErr.Error())
		}
		return strings.TrimSpace("DIAG_ERROR=" + runErr.Error())
	}
	return strings.TrimSpace(output)
}

func extractXtraBackupVersionLine(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if isXtraBackupVersionLine(trimmed) {
			return trimmed
		}
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "xtrabackup") && strings.Contains(lower, "based on mysql server") {
			return trimmed
		}
	}
	return text
}

func finalizeRemoteMySQLPhysicalToolResolution(serverVersion string, cfg mysqlPhysicalDriverConfig, output string, runErr error) (*localToolSpec, string, error) {
	parsed := parseKeyValueOutput(output)
	toolUser := strings.TrimSpace(parsed["TOOL_USER"])
	toolPath := strings.TrimSpace(parsed["TOOL_PATH"])
	toolError := strings.TrimSpace(parsed["TOOL_ERROR"])
	versionLine := extractXtraBackupVersionLine(parsed["VERSION_LINE"])
	if !isXtraBackupVersionLine(versionLine) {
		versionLine = extractXtraBackupVersionLine(output)
	}
	if !isXtraBackupVersionLine(versionLine) {
		versionLine = ""
	}
	if toolPath != "" {
		spec := &localToolSpec{
			Path:    toolPath,
			Name:    filepath.Base(toolPath),
			Version: versionLine,
			Source:  mysqlPhysicalToolSource(cfg.ToolPath),
			LibDirs: detectToolLibDirs(toolPath),
		}
		if err := validateMySQLPhysicalToolCompatibility(spec, serverVersion); err != nil {
			return nil, toolUser, err
		}
		return spec, toolUser, nil
	}
	if runErr != nil {
		return nil, toolUser, fmt.Errorf("远端 xtrabackup 工具探测失败: %s", mysqlPhysicalToolErrorMessage(toolError, cfg.ToolPath, output, runErr))
	}
	if strings.TrimSpace(output) == "" {
		return nil, toolUser, fmt.Errorf("远端 xtrabackup 工具探测未返回任何输出")
	}
	if toolError != "" {
		return nil, toolUser, fmt.Errorf("远端 xtrabackup 工具探测失败: %s", mysqlPhysicalToolErrorMessage(toolError, cfg.ToolPath, output, runErr))
	}
	return nil, toolUser, fmt.Errorf("未检测到可用的 xtrabackup 工具")
}

func shouldAutoProvisionMySQLPhysicalTool(output string, runErr error, cfg mysqlPhysicalDriverConfig) bool {
	if strings.TrimSpace(cfg.ToolPath) != "" {
		return false
	}
	if runErr == nil && strings.TrimSpace(output) == "" {
		return false
	}
	parsed := parseKeyValueOutput(output)
	switch strings.TrimSpace(parsed["TOOL_ERROR"]) {
	case "tool_not_found", "configured_tool_not_found", "remote_probe_output_empty":
		return true
	}
	if runErr == nil {
		return false
	}
	return strings.Contains(strings.ToLower(runErr.Error()), "status 41")
}

func mysqlPhysicalToolSource(configuredToolPath string) string {
	if strings.TrimSpace(configuredToolPath) != "" {
		return "user_config:path"
	}
	return "auto_lookup"
}

func mysqlPhysicalToolErrorMessage(toolError string, configuredToolPath string, output string, runErr error) string {
	switch strings.TrimSpace(toolError) {
	case "target_tool_user_empty":
		return "未识别到数据库进程用户，无法切换到对应操作系统用户探测 xtrabackup"
	case "target_tool_user_not_found":
		return "目标操作系统用户不存在"
	case "configured_tool_not_executable":
		if strings.TrimSpace(configuredToolPath) != "" {
			return "用户填写的 xtrabackup 路径不可执行"
		}
		return "xtrabackup 工具不可执行"
	case "configured_tool_not_found":
		if strings.TrimSpace(configuredToolPath) != "" {
			return "用户填写的 xtrabackup 路径未解析到可执行文件"
		}
		return "未检测到可用的 xtrabackup 工具"
	case "tool_not_found":
		return "未检测到可用的 xtrabackup 工具"
	case "sudo_missing":
		return "目标机器未安装 sudo，无法切换到数据库操作系统用户探测 xtrabackup"
	case "remote_probe_output_empty":
		return "远端 xtrabackup 工具探测未返回任何输出"
	default:
		if runErr != nil && strings.TrimSpace(output) == "" {
			return runErr.Error()
		}
		return strings.TrimSpace(firstNonEmpty(toolError, output))
	}
}

func buildMySQLPhysicalToolResolveProbeScript(serverVersion string, toolPath string, candidates []string, toolName string) string {
	versionFamily, _ := mysqlPhysicalSupportedVersionFamily(serverVersion)
	quotedCandidates := make([]string, 0, len(candidates))
	for _, candidate := range uniqueStrings(candidates) {
		quotedCandidates = append(quotedCandidates, shellQuote(candidate))
	}
	candidateExpr := strings.Join(quotedCandidates, " ")
	buildLookup := func(targetToolName string) []string {
		lines := []string{
			`find_first_executable() { for candidate in "$@"; do if [ -n "$candidate" ] && [ -x "$candidate" ]; then printf "%s\n" "$candidate"; return 0; fi; done; return 1; }`,
			`find_tool_by_name() {`,
			`  TOOL_NAME="$1"`,
			`  TOOL_PATH=""`,
			`  export PATH="${PROCESS_BIN_DIR:+$PROCESS_BIN_DIR:}/usr/local/goinsight/tools/mysql/` + versionFamily + `/bin:/usr/local/goinsight/tools/mysql/` + versionFamily + `:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:$PATH"`,
			`  if command -v "$TOOL_NAME" >/dev/null 2>&1; then TOOL_PATH=$(command -v "$TOOL_NAME"); fi`,
		}
		if candidateExpr != "" {
			lines = append(lines, `  if [ -z "$TOOL_PATH" ]; then TOOL_PATH=$(find_first_executable `+candidateExpr+` 2>/dev/null || true); fi`)
		}
		lines = append(lines, `}`)
		if strings.TrimSpace(targetToolName) != "" {
			lines = append(lines, `find_tool_by_name `+shellQuote(strings.TrimSpace(targetToolName)))
		}
		return lines
	}

	lines := []string{
		"set +e",
		shellProfileInitSnippet(),
		"INPUT_TOOL_PATH=" + shellQuote(strings.TrimSpace(toolPath)),
		`PROCESS_BIN_DIR=${PROCESS_BIN_DIR:-}`,
		`TOOL_PATH=""`,
		`VERSION_LINE=""`,
		`TOOL_ERROR=""`,
		`STATUS=0`,
	}
	if strings.TrimSpace(toolPath) != "" {
		lines = append(lines, buildLookup(strings.TrimSpace(toolPath))...)
		lines = append(lines,
			`if [ "${INPUT_TOOL_PATH#/}" != "$INPUT_TOOL_PATH" ] || [ "${INPUT_TOOL_PATH#./}" != "$INPUT_TOOL_PATH" ] || [ "${INPUT_TOOL_PATH#../}" != "$INPUT_TOOL_PATH" ]; then`,
			`  if [ ! -x "$INPUT_TOOL_PATH" ]; then TOOL_ERROR="configured_tool_not_executable"; STATUS=41; else TOOL_PATH="$INPUT_TOOL_PATH"; fi`,
			`elif [ -z "$TOOL_PATH" ]; then TOOL_ERROR="configured_tool_not_found"; STATUS=41; fi`,
			`fi`,
		)
	} else {
		lines = append(lines, buildLookup(strings.TrimSpace(firstNonEmpty(toolName, "xtrabackup")))...)
		lines = append(lines, `if [ -z "$TOOL_PATH" ]; then TOOL_ERROR="tool_not_found"; STATUS=41; fi`)
	}
	lines = append(lines,
		`if [ -n "$TOOL_PATH" ]; then`,
		`  LIB_PATHS=""`,
		`  for libdir in "$(dirname "$TOOL_PATH")/lib" "$(dirname "$(dirname "$TOOL_PATH")")/lib" "$(dirname "$(dirname "$(dirname "$TOOL_PATH")")")/lib"; do`,
		`    if [ -d "$libdir" ]; then if [ -n "$LIB_PATHS" ]; then LIB_PATHS="$LIB_PATHS:$libdir"; else LIB_PATHS="$libdir"; fi; fi`,
		`  done`,
		`  if [ -n "$LIB_PATHS" ]; then VERSION_OUTPUT=$(env LD_LIBRARY_PATH="$LIB_PATHS${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}" "$TOOL_PATH" --version 2>&1 | tr -d '\r'); else VERSION_OUTPUT=$("$TOOL_PATH" --version 2>&1 | tr -d '\r'); fi`,
		`  VERSION_LINE=$(printf '%s\n' "$VERSION_OUTPUT" | awk 'BEGIN{IGNORECASE=1} /xtrabackup version/{print; exit}')`,
		`  if [ -z "$VERSION_LINE" ]; then VERSION_LINE=$(printf '%s\n' "$VERSION_OUTPUT" | awk 'BEGIN{IGNORECASE=1} /xtrabackup/ && /based on mysql server/{print; exit}'); fi`,
		`  if [ -z "$VERSION_LINE" ]; then VERSION_LINE=$(printf '%s\n' "$VERSION_OUTPUT" | head -n1); fi`,
		`fi`,
		`TOOL_USER=$(id -un 2>/dev/null || true)`,
		`echo TOOL_USER=${TOOL_USER}`,
		`echo TOOL_PATH=${TOOL_PATH}`,
		`echo VERSION_LINE=${VERSION_LINE}`,
		`echo TOOL_ERROR=$(printf '%s' "$TOOL_ERROR" | tr '\n' ';')`,
		`exit "$STATUS"`,
	)
	return strings.Join(lines, "\n")
}

func isXtraBackupVersionLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	return strings.Contains(lower, "xtrabackup version")
}
