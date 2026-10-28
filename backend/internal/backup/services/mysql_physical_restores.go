package services

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	backupForms "github.com/simajiong/jaysql/internal/backup/forms"
	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
)

// ============================================================================
// MySQL XtraBackup 物理恢复（EXISTING_INSTANCE 覆盖模式）
//
// 执行通道：server 通过 SSH 编排目标节点（与 PostgreSQL pgBackRest 恢复一致）。
// 流程：预检查 → 传输备份文件(流式) → 远端 detached 脚本执行
//       (xbstream 解流 → prepare → 停库 → 清空 datadir → copy-back → 起库 → 验证)
//       → 轮询进度/状态 → SUCCESS/FAILED。
// ============================================================================

type mysqlPhysicalRestoreResolved struct {
	TargetHost        string `json:"target_host"`
	TargetDataDir     string `json:"target_data_dir"`
	TargetPort        int    `json:"target_port"`
	TargetVersion     string `json:"target_version"`
	SourceVersion     string `json:"source_version"`
	LogBinBasename    string `json:"log_bin_basename"`
	XtraBackupPath    string `json:"xtrabackup_path"`
	XtraBackupVersion string `json:"xtrabackup_version"`
	MysqlOSUser       string `json:"mysql_os_user"`
	ServiceUnit       string `json:"service_unit"`
	WorkDir           string `json:"work_dir"`
	RemoteBackupFile  string `json:"remote_backup_file"`
	BackupSizeBytes   int64  `json:"backup_size_bytes"`
	BackupSha256      string `json:"backup_sha256"`
	ToolsDir          string `json:"tools_dir"`

	// Chain 为恢复链路的完整层级（[FULL, INCR1..INCRn]）。全量备份集只有一层；
	// 增量备份集必须携带其依赖的全量基线与各级增量，恢复时按序合并。
	Chain []mysqlPhysicalRestoreChainNode `json:"chain,omitempty"`
}

type mysqlPhysicalRestoreContext struct {
	Form           *backupForms.PrecheckPhysicalRestoreForm
	BackupSet      *backupModels.InsightBackupSets
	BackupFilePath string
	TargetInstance *commonModels.InsightInstances
	TargetNode     *backupModels.InsightBackupNodes
	RemoteConfig   *backupNodeRemoteConfig
	DriverCfg      mysqlPhysicalDriverConfig
	Resolved       mysqlPhysicalRestoreResolved
	PrecheckItems  []physicalRestorePrecheckItem
	Passed         bool
}

// --- 预检查入口 ---

func buildMySQLPhysicalRestoreContext(form *backupForms.PrecheckPhysicalRestoreForm, c *gin.Context) (*mysqlPhysicalRestoreContext, error) {
	ctx := &mysqlPhysicalRestoreContext{Form: form, Passed: false}

	set, err := LoadBackupSet(form.BackupSetID)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(strings.TrimSpace(string(set.DBType)), "MySQL") || !isMySQLPhysicalDriverType(string(set.DriverType)) {
		return nil, fmt.Errorf("备份集不是 MySQL XtraBackup 物理备份，无法按此流程恢复")
	}
	if !strings.EqualFold(strings.TrimSpace(form.RestoreMode), physicalRestoreModeExistingInstance) {
		return nil, fmt.Errorf("MySQL XtraBackup 恢复当前仅支持覆盖模式（EXISTING_INSTANCE）")
	}
	ctx.BackupSet = set

	addItem := func(key string, label string, passed bool, severity string, message string, detail map[string]any) {
		ctx.PrecheckItems = append(ctx.PrecheckItems, physicalRestorePrecheckItem{
			ItemKey: key, Label: label, Passed: passed, Severity: severity, Message: message, Detail: detail,
		})
	}

	// 1. 备份文件可用性（server 本地）
	backupPath, fileErr := ValidateBackupSetFile(set)
	if fileErr != nil {
		addItem("backup_file_exists", "备份文件校验", false, "ERROR",
			fmt.Sprintf("备份文件不可用: %v（当前仅支持本地存储目标的备份集）", fileErr), nil)
		return finalizeMySQLPhysicalRestoreContext(ctx), nil
	}
	ctx.BackupFilePath = backupPath
	sizeBytes := set.SizeBytes
	if sizeBytes <= 0 {
		if st, statErr := os.Stat(backupPath); statErr == nil {
			sizeBytes = st.Size()
		}
	}
	ctx.Resolved.BackupSizeBytes = sizeBytes
	ctx.Resolved.BackupSha256 = strings.TrimSpace(set.Sha256)
	addItem("backup_file_exists", "备份文件校验", true, "INFO",
		fmt.Sprintf("备份文件可用: %s（大小 %s）", backupPath, formatPhysicalRestoreBytes(sizeBytes)),
		map[string]any{"sha256": ctx.Resolved.BackupSha256})

	// 1.1 恢复链路解析：增量备份集必须能完整回溯到全量基线，否则拒绝恢复。
	// 少了基线直接 prepare 会得到一份缺数据页的目录，必须在这里拦死。
	chain, chainErr := resolveMySQLPhysicalRestoreChain(set)
	if chainErr != nil {
		addItem("backup_chain", "恢复链路校验", false, "ERROR",
			fmt.Sprintf("无法构建恢复链路: %v", chainErr), nil)
		return finalizeMySQLPhysicalRestoreContext(ctx), nil
	}
	ctx.Resolved.Chain = chain
	chainBytes := mysqlPhysicalRestoreChainTotalBytes(chain)
	addItem("backup_chain", "恢复链路校验", true, "INFO",
		summarizeMySQLPhysicalRestoreChain(chain), mysqlPhysicalRestoreChainDetail(chain))

	// 2. 目标实例
	if form.TargetInstanceID == 0 {
		return nil, fmt.Errorf("覆盖恢复必须选择目标实例")
	}
	instance, loadErr := loadInstanceByID(form.TargetInstanceID)
	if loadErr != nil {
		return nil, loadErr
	}
	if !strings.EqualFold(strings.TrimSpace(string(instance.DbType)), "MySQL") {
		return nil, fmt.Errorf("目标实例必须是 MySQL，当前为 %s", instance.DbType)
	}
	ctx.TargetInstance = instance

	// 3. SQL 探测目标实例（datadir / 版本）
	vars, probeErr := probeMySQLPhysicalVariables(strings.TrimSpace(instance.Hostname), instance.Port,
		strings.TrimSpace(string(instance.User)), strings.TrimSpace(string(instance.Password)))
	if probeErr != nil {
		addItem("target_instance_probe", "目标实例探测", false, "ERROR",
			fmt.Sprintf("无法连接目标实例 %s:%d 读取物理参数: %v", instance.Hostname, instance.Port, probeErr), nil)
		return finalizeMySQLPhysicalRestoreContext(ctx), nil
	}
	datadir := strings.TrimSpace(vars["datadir"])
	targetVersion := extractMySQLVersionFromComment(vars["version"])
	if datadir == "" || targetVersion == "" {
		addItem("target_instance_probe", "目标实例探测", false, "ERROR",
			"目标实例参数读取不完整（datadir/version 为空）", map[string]any{"vars": vars})
		return finalizeMySQLPhysicalRestoreContext(ctx), nil
	}
	ctx.Resolved.TargetDataDir = datadir
	ctx.Resolved.TargetVersion = targetVersion
	ctx.Resolved.TargetHost = strings.TrimSpace(instance.Hostname)
	ctx.Resolved.TargetPort = instance.Port
	ctx.Resolved.LogBinBasename = strings.TrimSpace(vars["log_bin_basename"])

	// 4. 版本兼容性：目标库与备份源必须同 major
	sourceVersion := strings.TrimSpace(set.ServerVersion)
	ctx.Resolved.SourceVersion = sourceVersion
	sourceMajor := mysqlVersionMajor(sourceVersion)
	targetMajor := mysqlVersionMajor(targetVersion)
	if sourceMajor == "" || targetMajor == "" || sourceMajor != targetMajor {
		addItem("version_compatible", "版本兼容性校验", false, "ERROR",
			fmt.Sprintf("目标实例版本(%s)与备份源版本(%s)大版本不一致，物理恢复将导致数据文件不可用", targetVersion, sourceVersion),
			map[string]any{"source_major": sourceMajor, "target_major": targetMajor})
		return finalizeMySQLPhysicalRestoreContext(ctx), nil
	}
	addItem("version_compatible", "版本兼容性校验", true, "INFO",
		fmt.Sprintf("目标实例版本 %s 与备份源版本 %s 大版本一致（%s）。", targetVersion, sourceVersion, sourceMajor), nil)

	// 5. 目标节点（SSH 编排通道）
	targetNode, nodeErr := loadBackupNodeByHost(instance.Hostname)
	if nodeErr != nil {
		addItem("target_node", "目标节点校验", false, "ERROR",
			fmt.Sprintf("未找到实例主机 %s 对应的备份节点，无法执行 SSH 恢复编排: %v", instance.Hostname, nodeErr), nil)
		return finalizeMySQLPhysicalRestoreContext(ctx), nil
	}
	ctx.TargetNode = targetNode
	remoteCfg, cfgErr := buildBackupNodeRemoteConfigForRestore(targetNode, c)
	if cfgErr != nil {
		addItem("target_node", "目标节点校验", false, "ERROR", cfgErr.Error(), nil)
		return finalizeMySQLPhysicalRestoreContext(ctx), nil
	}
	ctx.RemoteConfig = remoteCfg
	ctx.Resolved.ToolsDir = remoteCfg.ToolsDir

	// 6. 远端环境预检查（xtrabackup / datadir / 磁盘 / systemd 单元）
	runner, sshErr := newSSHRunner(remoteCfg)
	if sshErr != nil {
		addItem("target_connectivity", "目标节点连接", false, "ERROR",
			fmt.Sprintf("无法连接目标节点: %v", sshErr), nil)
		return finalizeMySQLPhysicalRestoreContext(ctx), nil
	}
	defer runner.Close()
	addItem("target_connectivity", "目标节点连接", true, "INFO",
		fmt.Sprintf("已连接目标节点 %s。", remoteCfg.Host), nil)

	ctx.DriverCfg, _ = loadMySQLPhysicalDriverConfigSnapshot(set.DriverConfig)
	script := buildMySQLPhysicalRestorePrecheckScript(datadir, targetMajor, remoteCfg.ToolsDir, ctx.DriverCfg.ToolPath)
	output, runErr := runner.Run(script)
	if runErr != nil {
		addItem("target_runtime_check", "目标节点环境检查", false, "ERROR",
			fmt.Sprintf("目标节点环境检查失败: %v", runErr), map[string]any{"output": output})
		return finalizeMySQLPhysicalRestoreContext(ctx), nil
	}
	parsed := parsePhysicalRestoreKeyValues(output)

	xbPath := strings.TrimSpace(parsed["XB_PATH"])
	xbVersion := strings.TrimSpace(parsed["XB_VERSION_LINE"])
	xbMajor := mysqlVersionMajor(xbVersion)
	ctx.Resolved.XtraBackupPath = xbPath
	ctx.Resolved.XtraBackupVersion = xbVersion
	xbOK := xbPath != "" && xbMajor != "" && xbMajor == targetMajor
	xbMessage := ternaryString(xbOK,
		fmt.Sprintf("已检测到 xtrabackup %s（版本与目标实例匹配）。", xbPath),
		fmt.Sprintf("目标节点未检测到与实例大版本(%s)匹配的 xtrabackup 工具（检测到: %s %s）。", targetMajor, displayOrDefault(xbPath, "无"), displayOrDefault(xbVersion, "")))
	addItem("xtrabackup_ready", "xtrabackup 工具校验", xbOK, "ERROR", xbMessage,
		map[string]any{"path": xbPath, "version": xbVersion, "required_major": targetMajor})

	osUser := strings.TrimSpace(parsed["MYSQL_OS_USER"])
	if osUser == "" {
		osUser = "mysql"
	}
	ctx.Resolved.MysqlOSUser = osUser

	datadirExists := strings.TrimSpace(parsed["DATADIR_EXISTS"]) == "1"
	addItem("datadir_exists", "目标数据目录校验", datadirExists, "ERROR",
		ternaryString(datadirExists, fmt.Sprintf("目标数据目录 %s 存在。", datadir), fmt.Sprintf("目标数据目录 %s 不存在。", datadir)), nil)

	serviceUnit := strings.TrimSpace(parsed["SERVICE_UNIT"])
	ctx.Resolved.ServiceUnit = serviceUnit
	unitOK := serviceUnit != ""
	addItem("service_unit_detected", "MySQL 服务单元校验", unitOK, "ERROR",
		ternaryString(unitOK,
			fmt.Sprintf("已识别运行中的 systemd 服务单元 %s，恢复完成后将自动拉起。", serviceUnit),
			"未识别到运行中的 MySQL systemd 服务单元，恢复后需要人工启动实例。"), nil)

	availableBytes, _ := strconv.ParseInt(strings.TrimSpace(parsed["AVAILABLE_BYTES"]), 10, 64)
	// 链路恢复的峰值占用：各层 xbstream 落地一份 + 各层解流目录一份 + copy-back 回填一份，
	// 因此按链路总大小的 3 倍预留（单层全量时同样覆盖解流与回填两份）。
	requiredBytes := chainBytes * 3
	spaceOK := chainBytes <= 0 || availableBytes >= requiredBytes
	addItem("disk_space", "磁盘空间校验", spaceOK, "ERROR",
		ternaryString(spaceOK,
			fmt.Sprintf("数据目录所在分区可用空间充足（可用 %s，需要约 %s）。", formatPhysicalRestoreBytes(availableBytes), formatPhysicalRestoreBytes(requiredBytes)),
			fmt.Sprintf("数据目录所在分区可用空间不足：可用 %s，恢复至少需要约 %s。", formatPhysicalRestoreBytes(availableBytes), formatPhysicalRestoreBytes(requiredBytes))),
		map[string]any{"available_bytes": availableBytes, "required_bytes": requiredBytes, "chain_bytes": chainBytes})

	if !strings.EqualFold(parsed["DATADIR_WRITABLE"], "1") {
		addItem("datadir_writable", "数据目录写权限校验", false, "ERROR",
			fmt.Sprintf("当前 SSH 用户无法写入数据目录 %s（恢复需清空并回填该目录）。", datadir), nil)
	} else {
		addItem("datadir_writable", "数据目录写权限校验", true, "INFO",
			fmt.Sprintf("当前 SSH 用户可写入数据目录 %s。", datadir), nil)
	}

	workDir := mysqlRestoreWorkDir(datadir, 0)
	ctx.Resolved.WorkDir = workDir
	return finalizeMySQLPhysicalRestoreContext(ctx), nil
}

func finalizeMySQLPhysicalRestoreContext(ctx *mysqlPhysicalRestoreContext) *mysqlPhysicalRestoreContext {
	passed := true
	for _, item := range ctx.PrecheckItems {
		if !item.Passed && strings.EqualFold(strings.TrimSpace(item.Severity), "ERROR") {
			passed = false
			break
		}
	}
	ctx.Passed = passed
	return ctx
}

func loadMySQLPhysicalDriverConfigSnapshot(raw datatypes.JSON) (mysqlPhysicalDriverConfig, error) {
	cfg := mysqlPhysicalDriverConfig{}
	if len(raw) == 0 {
		return cfg, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return cfg, err
	}
	if v, ok := decoded["tool_path"].(string); ok {
		cfg.ToolPath = strings.TrimSpace(v)
	}
	if v, ok := decoded["os_user"].(string); ok {
		cfg.OSUser = strings.TrimSpace(v)
	}
	return cfg, nil
}

func buildMySQLPhysicalRestorePrecheckScript(datadir string, targetMajor string, toolsDir string, configuredToolPath string) string {
	lines := []string{
		"set +e",
		fmt.Sprintf(`DATADIR=%s`, shellQuote(datadir)),
		fmt.Sprintf(`TOOLS_DIR=%s`, shellQuote(toolsDir)),
		fmt.Sprintf(`CONFIGURED_TOOL=%s`, shellQuote(configuredToolPath)),
		`XB_PATH=""`,
		`for c in "$CONFIGURED_TOOL" "$TOOLS_DIR/mysql/8.4/bin/xtrabackup" "$TOOLS_DIR/mysql/8.0/bin/xtrabackup" "$TOOLS_DIR/mysql/5.7/bin/xtrabackup" /usr/local/goinsight/tools/mysql/8.4/bin/xtrabackup /usr/local/goinsight/tools/mysql/8.0/bin/xtrabackup /usr/local/goinsight/tools/mysql/5.7/bin/xtrabackup; do`,
		`  if [ -n "$c" ] && [ -x "$c" ]; then XB_PATH="$c"; break; fi;`,
		`done`,
		`if [ -z "$XB_PATH" ]; then XB_PATH=$(command -v xtrabackup 2>/dev/null); fi`,
		`XB_VERSION_LINE=""`,
		`if [ -n "$XB_PATH" ]; then XB_VERSION_LINE=$("$XB_PATH" --version 2>&1 | grep -oE '[0-9]+\.[0-9]+\.[0-9]+(-[0-9]+)?' | head -n1); fi`,
		`DATADIR_EXISTS=0; MYSQL_OS_USER=""`,
		`if [ -d "$DATADIR" ]; then`,
		`  DATADIR_EXISTS=1`,
		`  MYSQL_OS_USER=$(stat -c %U "$DATADIR" 2>/dev/null || true)`,
		`fi`,
		`DATADIR_WRITABLE=0`,
		`if [ -d "$DATADIR" ] && [ -w "$DATADIR" ]; then DATADIR_WRITABLE=1; fi`,
		`AVAILABLE_BYTES=$(df -Pk "$(dirname "$DATADIR")" 2>/dev/null | awk 'NR==2{print $4*1024}')`,
		`SERVICE_UNIT=""`,
		`if command -v systemctl >/dev/null 2>&1; then`,
		`  SERVICE_UNIT=$(systemctl list-units --type=service --state=running --no-legend 2>/dev/null | awk '{print $1}' | grep -iE 'mysqld|mysql' | grep -vE 'backup|agent' | head -n1)`,
		`fi`,
		`echo "XB_PATH=$XB_PATH"`,
		`echo "XB_VERSION_LINE=$XB_VERSION_LINE"`,
		`echo "DATADIR_EXISTS=$DATADIR_EXISTS"`,
		`echo "MYSQL_OS_USER=$MYSQL_OS_USER"`,
		`echo "DATADIR_WRITABLE=$DATADIR_WRITABLE"`,
		`echo "AVAILABLE_BYTES=$AVAILABLE_BYTES"`,
		`echo "SERVICE_UNIT=$SERVICE_UNIT"`,
	}
	return strings.Join(lines, "\n")
}

func extractMySQLVersionFromComment(comment string) string {
	text := strings.TrimSpace(comment)
	if text == "" {
		return ""
	}
	fields := strings.Fields(text)
	for _, field := range fields {
		if len(field) > 0 && field[0] >= '0' && field[0] <= '9' && strings.Contains(field, ".") {
			return field
		}
	}
	return text
}

func mysqlVersionMajor(version string) string {
	text := strings.TrimSpace(version)
	if text == "" {
		return ""
	}
	parts := strings.Split(text, ".")
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "." + parts[1]
}

func mysqlRestoreWorkDir(datadir string, runID uint64) string {
	cleaned := strings.TrimRight(strings.TrimSpace(datadir), "/")
	base := filepath.Base(cleaned)
	suffix := ""
	if runID > 0 {
		suffix = fmt.Sprintf("_restore_%d", runID)
	} else {
		suffix = "_restore_precheck"
	}
	return filepath.Join(filepath.Dir(cleaned), base+suffix)
}

// --- 创建恢复任务 ---

func createMySQLPhysicalRestoreRun(s *CreatePhysicalRestoreRunService, ctx *mysqlPhysicalRestoreContext) (*backupModels.InsightRestoreRuns, error) {
	if !ctx.Passed {
		return nil, fmt.Errorf("预检查未通过：%s", summarizePhysicalRestoreFailures(ctx.PrecheckItems))
	}
	precheckJSON, err := marshalJSONValue(gin.H{
		"summary":  summarizePhysicalRestorePrecheck(ctx.PrecheckItems),
		"items":    ctx.PrecheckItems,
		"resolved": ctx.Resolved,
	})
	if err != nil {
		return nil, err
	}
	autoStart := true
	if s.AutoStart != nil {
		autoStart = *s.AutoStart
	}
	resolvedJSON, err := buildMySQLPhysicalRestoreResolvedContextJSON(ctx.Resolved)
	if err != nil {
		return nil, err
	}
	optionsJSON, err := buildPhysicalRestoreOptionsJSON(autoStart)
	if err != nil {
		return nil, err
	}
	run := &backupModels.InsightRestoreRuns{
		Model:                &commonModels.Model{},
		TaskID:               generateRestoreTaskID(),
		Name:                 normalizeRestoreRunName(s.Name, ctx.BackupSet.Name, ctx.Resolved.TargetDataDir),
		BackupSetID:          ctx.BackupSet.ID,
		SourceInstanceID:     ctx.BackupSet.SourceInstanceID,
		DBType:               ctx.BackupSet.DBType,
		RestoreKind:          restoreKindPhysical,
		DriverType:           ctx.BackupSet.DriverType,
		RestoreMode:          commonModels.EnumType(physicalRestoreModeExistingInstance),
		TargetLevel:          restoreTargetLevelInstance,
		TargetInstanceID:     ctx.TargetInstance.ID,
		TargetNodeID:         ctx.TargetNode.ID,
		TargetName:           ctx.Resolved.TargetDataDir,
		TargetDBName:         "",
		TargetHost:           ctx.Resolved.TargetHost,
		TargetPort:           ctx.Resolved.TargetPort,
		DriverConfigSnapshot: ctx.BackupSet.DriverConfig,
		ResolvedContext:      resolvedJSON,
		Options:              optionsJSON,
		Status:               "PENDING",
		CurrentStage:         physicalRestoreStagePending,
		ProgressPercent:      0,
		CurrentObject:        "",
		CurrentAction:        "等待执行",
		RiskLevel:            "HIGH",
		PrecheckSummary:      precheckJSON,
		CreatedBy:            currentUsername(s.C),
	}
	if err := global.App.DB.Create(run).Error; err != nil {
		return nil, err
	}
	go executeMySQLPhysicalRestoreRun(run.ID)
	return run, nil
}

func buildMySQLPhysicalRestoreResolvedContextJSON(resolved mysqlPhysicalRestoreResolved) (datatypes.JSON, error) {
	raw, err := json.Marshal(resolved)
	if err != nil {
		return nil, err
	}
	return datatypes.JSON(raw), nil
}

func decodeMySQLPhysicalRestoreResolvedContextJSON(raw datatypes.JSON) mysqlPhysicalRestoreResolved {
	resolved := mysqlPhysicalRestoreResolved{}
	if len(raw) == 0 {
		return resolved
	}
	_ = json.Unmarshal(raw, &resolved)
	return resolved
}

// --- 执行引擎 ---

func executeMySQLPhysicalRestoreRun(runID uint64) {
	var run backupModels.InsightRestoreRuns
	if err := global.App.DB.Where("id = ? AND restore_kind = ? AND driver_type = ?", runID, restoreKindPhysical, "XTRABACKUP").First(&run).Error; err != nil {
		return
	}
	switch strings.ToUpper(strings.TrimSpace(string(run.Status))) {
	case "STOPPED":
		return
	case "STOP_REQUESTED":
		writeMySQLPhysicalRestoreStopped(run.ID, "", resolveRestoreStopReason(run.CreatedBy))
		return
	}
	resolved := decodeMySQLPhysicalRestoreResolvedContextJSON(run.ResolvedContext)
	autoStart := restoreRunAutoStart(&run)

	set, err := LoadBackupSet(run.BackupSetID)
	if err != nil {
		writeMySQLPhysicalRestoreFailed(run.ID, "", err)
		return
	}
	backupPath, err := ValidateBackupSetFile(set)
	if err != nil {
		writeMySQLPhysicalRestoreFailed(run.ID, "", err)
		return
	}
	targetNode, err := loadBackupNodeByID(run.TargetNodeID)
	if err != nil {
		writeMySQLPhysicalRestoreFailed(run.ID, "", err)
		return
	}
	remoteCfg, err := buildBackupNodeRemoteConfigForRestore(targetNode, nil)
	if err != nil {
		writeMySQLPhysicalRestoreFailed(run.ID, "", err)
		return
	}
	if strings.TrimSpace(resolved.TargetDataDir) == "" {
		writeMySQLPhysicalRestoreFailed(run.ID, "", fmt.Errorf("恢复任务缺少目标数据目录信息"))
		return
	}
	runIDSuffix := fmt.Sprintf("%d", run.ID)
	resolved.WorkDir = mysqlRestoreWorkDir(resolved.TargetDataDir, run.ID)
	resolved.RemoteBackupFile = filepath.Join(resolved.WorkDir, "backup.xbstream")
	// 恢复链路：增量备份集按 [全量, 增量…] 顺序合并，全量备份集只有一层。
	// 历史 run 的 ResolvedContext 中没有链路信息，退化为单文件全量恢复。
	restoreChain := resolveMySQLPhysicalRestoreChainForExecution(resolved, backupPath)

	baseDir, err := absMediaBackupDir()
	if err != nil {
		writeMySQLPhysicalRestoreFailed(run.ID, "", err)
		return
	}
	logDir := filepath.Join(baseDir, "restore", "physical", "mysql", runIDSuffix)
	if err := ensureDir(logDir); err != nil {
		writeMySQLPhysicalRestoreFailed(run.ID, "", err)
		return
	}
	logPath := filepath.Join(logDir, "run.log")
	logger, err := openBackupRunLogger(logPath)
	if err != nil {
		writeMySQLPhysicalRestoreFailed(run.ID, "", err)
		return
	}
	defer logger.Close()

	updateMySQLPhysicalRestoreRunProgress(run.ID, mysqlPhysicalRestorePatch{
		Status: "RUNNING", CurrentStage: physicalRestoreStagePrecheck, ProgressPercent: 3,
		CurrentAction: "连接目标节点", LogPath: logPath,
	})
	logger.Infof("开始执行 MySQL XtraBackup 物理恢复 run=%d backup_set=%d target=%s:%d datadir=%s workdir=%s",
		run.ID, run.BackupSetID, resolved.TargetHost, resolved.TargetPort, resolved.TargetDataDir, resolved.WorkDir)

	runner, err := newSSHRunner(remoteCfg)
	if err != nil {
		writeMySQLPhysicalRestoreFailed(run.ID, logPath, err)
		return
	}
	defer runner.Close()

	// ---- 阶段一：按链路顺序传输备份文件（流式，边传边更新进度） ----
	updateMySQLPhysicalRestoreRunProgress(run.ID, mysqlPhysicalRestorePatch{
		Status: "RUNNING", CurrentStage: physicalRestoreStagePrepareTarget, ProgressPercent: 8,
		CurrentAction: "准备目标目录并传输备份文件", LogPath: logPath,
	})
	transferErr := transferMySQLRestoreBackupChain(run.ID, runner, restoreChain, logger)
	if transferErr != nil {
		if isMySQLPhysicalRestoreStopRequested(run.ID) {
			writeMySQLPhysicalRestoreStopped(run.ID, logPath, resolveRestoreStopReason(""))
			return
		}
		writeMySQLPhysicalRestoreFailed(run.ID, logPath, transferErr)
		return
	}

	// ---- 阶段二：远端 detached 脚本执行恢复 ----
	updateMySQLPhysicalRestoreRunProgress(run.ID, mysqlPhysicalRestorePatch{
		Status: "RUNNING", CurrentStage: physicalRestoreStageRestore, ProgressPercent: 50,
		CurrentAction: "执行 xtrabackup 解流/prepare/回填", LogPath: logPath,
	})
	execErr := runDetachedMySQLRestoreScript(run.ID, runner, resolved, restoreChain, autoStart, logger)
	if execErr != nil {
		if isMySQLPhysicalRestoreStopRequested(run.ID) {
			writeMySQLPhysicalRestoreStopped(run.ID, logPath, resolveRestoreStopReason(""))
			return
		}
		writeMySQLPhysicalRestoreFailed(run.ID, logPath, execErr)
		return
	}

	resultJSON, _ := marshalJSONValue(gin.H{
		"target_data_dir":   resolved.TargetDataDir,
		"target_port":       resolved.TargetPort,
		"target_version":    resolved.TargetVersion,
		"source_version":    resolved.SourceVersion,
		"xtrabackup_path":   resolved.XtraBackupPath,
		"service_unit":      resolved.ServiceUnit,
		"work_dir":          resolved.WorkDir,
		"backup_size_bytes": resolved.BackupSizeBytes,
		"auto_start":        autoStart,
	})
	updateMySQLPhysicalRestoreRunProgress(run.ID, mysqlPhysicalRestorePatch{
		Status: "SUCCESS", CurrentStage: physicalRestoreStageFinished, ProgressPercent: 100,
		CurrentAction: "恢复完成", LogPath: logPath, ResultSummary: resultJSON,
	})
	logger.Infof("MySQL XtraBackup 物理恢复完成。")
}

// transferMySQLRestoreBackupChain：按恢复链路顺序，把每一层 xbstream 流式推送到目标节点。
// 进度按整条链路的总字节换算，保证跨层单调递增。
func transferMySQLRestoreBackupChain(runID uint64, runner *sshRunner, chain []mysqlPhysicalRestoreChainNode, logger *backupRunLogger) error {
	if len(chain) == 0 {
		return fmt.Errorf("恢复链路为空，无可传输的备份文件")
	}
	grandTotal := mysqlPhysicalRestoreChainTotalBytes(chain)
	if grandTotal <= 0 {
		grandTotal = 1
	}
	var doneBytes int64
	for _, node := range chain {
		if isMySQLPhysicalRestoreStopRequested(runID) {
			return fmt.Errorf("收到终止请求，传输中止")
		}
		if err := transferMySQLRestoreSingleFile(runID, runner, node, len(chain), doneBytes, grandTotal, logger); err != nil {
			return err
		}
		doneBytes += node.SizeBytes
	}
	logger.Infof("恢复链路 %d 层全部传输完成并校验通过，合计 %s", len(chain), formatPhysicalRestoreBytes(grandTotal))
	return nil
}

// transferMySQLRestoreSingleFile：传输链路中的某一层，完成后校验远端大小与 sha256。
func transferMySQLRestoreSingleFile(runID uint64, runner *sshRunner, node mysqlPhysicalRestoreChainNode, chainLen int, doneBytes int64, grandTotal int64, logger *backupRunLogger) error {
	remotePath := strings.TrimSpace(node.RemotePath)
	if remotePath == "" {
		return fmt.Errorf("第 %d 层备份文件缺少目标节点落地路径", node.Order)
	}
	localFile, err := os.Open(node.LocalPath)
	if err != nil {
		return fmt.Errorf("打开第 %d 层本地备份文件失败: %w", node.Order, err)
	}
	defer localFile.Close()
	st, err := localFile.Stat()
	if err != nil {
		return fmt.Errorf("读取第 %d 层本地备份文件信息失败: %w", node.Order, err)
	}
	totalBytes := st.Size()

	if _, err := runner.Run(fmt.Sprintf("umask 022 && mkdir -p %s", shellQuote(filepath.Dir(remotePath)))); err != nil {
		return fmt.Errorf("创建远端工作目录失败: %w", err)
	}
	logger.Infof("开始传输第 %d/%d 层备份文件(%s): %s -> %s:%s (%s)",
		node.Order, chainLen, node.BackupType, node.LocalPath, runnerHost(runner), remotePath, formatPhysicalRestoreBytes(totalBytes))

	session, err := runner.client.NewSession()
	if err != nil {
		return fmt.Errorf("建立 SSH 传输会话失败: %w", err)
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return fmt.Errorf("建立 SSH 标准输入失败: %w", err)
	}
	var stderrBuf strings.Builder
	session.Stderr = &stderrBuf
	if err := session.Start("umask 022 && cat > " + shellQuote(remotePath)); err != nil {
		return fmt.Errorf("启动远端接收进程失败: %w", err)
	}

	hasher := sha256.New()
	progressWriter := &progressTrackingWriter{
		total:      totalBytes,
		runID:      runID,
		baseBytes:  doneBytes,
		grandTotal: grandTotal,
		lastAt:     time.Now(),
	}
	multiWriter := io.MultiWriter(stdin, hasher, progressWriter)
	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(multiWriter, localFile)
		copyDone <- copyErr
	}()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	transferDone := false
	for !transferDone {
		select {
		case copyErr := <-copyDone:
			transferDone = true
			if copyErr != nil {
				_ = stdin.Close()
				_ = session.Wait()
				return fmt.Errorf("传输备份文件中断: %w", copyErr)
			}
		case <-ticker.C:
			if isMySQLPhysicalRestoreStopRequested(runID) {
				_ = stdin.Close()
				_ = session.Close()
				return fmt.Errorf("收到终止请求，传输中止")
			}
			percent := progressWriter.percent()
			if percent > 0 {
				updateMySQLPhysicalRestoreRunProgress(runID, mysqlPhysicalRestorePatch{
					Status: "RUNNING", CurrentStage: physicalRestoreStagePrepareTarget,
					ProgressPercent: clampPercent(8 + percent*40/100),
					CurrentAction:   fmt.Sprintf("正在传输第 %d/%d 层备份文件 %d%%", node.Order, chainLen, percent),
				})
			}
		}
	}
	_ = stdin.Close()
	if err := session.Wait(); err != nil {
		return fmt.Errorf("远端接收文件失败: %w %s", err, strings.TrimSpace(stderrBuf.String()))
	}

	// 校验：远端文件大小 + sha256
	localSha := hex.EncodeToString(hasher.Sum(nil))
	checkScript := strings.Join([]string{
		`ACTUAL_SIZE=$(stat -c %s ` + shellQuote(remotePath) + ` 2>/dev/null || echo 0)`,
		`ACTUAL_SHA=$(sha256sum ` + shellQuote(remotePath) + ` 2>/dev/null | awk '{print $1}')`,
		`echo "SIZE=$ACTUAL_SIZE"`,
		`echo "SHA=$ACTUAL_SHA"`,
	}, "\n")
	checkOut, checkErr := runner.Run(checkScript)
	if checkErr != nil {
		return fmt.Errorf("校验远端备份文件失败: %w", checkErr)
	}
	parsed := parsePhysicalRestoreKeyValues(checkOut)
	if parsed["SHA"] != "" && localSha != "" && parsed["SHA"] != localSha {
		return fmt.Errorf("第 %d 层备份文件 SHA256 校验不一致（本地 %s / 远端 %s），传输可能已损坏", node.Order, localSha, parsed["SHA"])
	}
	if expected := strings.TrimSpace(node.Sha256); expected != "" && parsed["SHA"] != "" && parsed["SHA"] != expected {
		return fmt.Errorf("第 %d 层备份文件 SHA256 与备份登记值不一致（登记 %s / 实际 %s）", node.Order, expected, parsed["SHA"])
	}
	if expectedSize := strconv.FormatInt(totalBytes, 10); parsed["SIZE"] != expectedSize {
		return fmt.Errorf("第 %d 层备份文件大小校验不一致（期望 %s / 实际 %s）", node.Order, expectedSize, parsed["SIZE"])
	}
	logger.Infof("第 %d/%d 层备份文件传输完成并校验通过 sha256=%s size=%d", node.Order, chainLen, localSha, totalBytes)
	return nil
}

type progressTrackingWriter struct {
	total      int64
	written    int64
	runID      uint64
	baseBytes  int64 // 本层之前各层已传输完成的字节数
	grandTotal int64 // 整条恢复链路的总字节数
	lastAt     time.Time
}

func (w *progressTrackingWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.written += int64(n)
	return n, nil
}

// percent 返回整条链路（而非当前层）的完成百分比，保证跨层进度单调递增。
func (w *progressTrackingWriter) percent() int {
	if w.grandTotal <= 0 {
		return 0
	}
	done := w.baseBytes + w.written
	if done > w.grandTotal {
		done = w.grandTotal
	}
	if done <= 0 {
		return 0
	}
	return int(done * 100 / w.grandTotal)
}

func runnerHost(runner *sshRunner) string {
	if runner == nil || runner.client == nil {
		return ""
	}
	return runner.client.RemoteAddr().String()
}

// runDetachedMySQLRestoreScript：上传恢复脚本 → nohup 后台启动 → 轮询状态。
func runDetachedMySQLRestoreScript(runID uint64, runner *sshRunner, resolved mysqlPhysicalRestoreResolved, chain []mysqlPhysicalRestoreChainNode, autoStart bool, logger *backupRunLogger) error {
	scriptPath := fmt.Sprintf("/tmp/goinsight-mysql-restore-%d.sh", runID)
	runLog := filepath.Join(resolved.WorkDir, "restore-run.log")
	pidFile := filepath.Join(resolved.WorkDir, "restore-run.pid")

	script := buildMySQLPhysicalRestoreExecuteScript(resolved, chain, autoStart)
	if err := runner.Upload(scriptPath, []byte(script)); err != nil {
		return fmt.Errorf("上传恢复脚本失败: %w", err)
	}
	launchOut, launchErr := runner.Run(strings.Join([]string{
		`rm -f ` + shellQuote(pidFile),
		fmt.Sprintf(`nohup /bin/sh %s > %s 2>&1 < /dev/null & echo $! > %s; cat %s`,
			shellQuote(scriptPath), shellQuote(runLog), shellQuote(pidFile), shellQuote(pidFile)),
	}, "\n"))
	if launchErr != nil {
		return fmt.Errorf("启动远端恢复脚本失败: %w %s", launchErr, launchOut)
	}
	pid := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(launchOut), "PID="))
	if pid == "" {
		return fmt.Errorf("未获取到远端恢复进程 PID，启动可能失败: %s", launchOut)
	}
	logger.Infof("远端恢复脚本已启动 pid=%s log=%s", pid, runLog)

	stageProgress := map[string]int{
		"UNPACK":      55,
		"PREPARE":     63,
		"STOP_MYSQLD": 72,
		"WIPE":        76,
		"COPY_BACK":   82,
		"START":       90,
		"VERIFY":      95,
	}
	pollScript := strings.Join([]string{
		`if [ -f ` + shellQuote(pidFile) + ` ]; then PID=$(cat ` + shellQuote(pidFile) + `); if kill -0 "$PID" 2>/dev/null; then echo "ALIVE=1"; else echo "ALIVE=0"; fi; else echo "ALIVE=0"; fi`,
		`grep -aE '^(STAGE|STATUS|ERROR|VERIFY|WARNING)=' ` + shellQuote(runLog) + ` 2>/dev/null | tail -n 8`,
		`echo '---- tail ----'`,
		`tail -n 12 ` + shellQuote(runLog) + ` 2>/dev/null || true`,
	}, "\n")

	deadline := time.Now().Add(6 * time.Hour)
	lastStage := ""
	for {
		if isMySQLPhysicalRestoreStopRequested(runID) {
			_, _ = runner.Run(fmt.Sprintf("kill %s 2>/dev/null; sleep 1; kill -9 %s 2>/dev/null; true", shellQuote(pid), shellQuote(pid)))
			logger.Warnf("收到终止请求，已中止远端恢复进程 pid=%s", pid)
			return fmt.Errorf("任务已终止")
		}
		out, pollErr := runner.Run(pollScript)
		if pollErr == nil {
			parsed := parsePhysicalRestoreKeyValues(out)
			status := strings.TrimSpace(parsed["STATUS"])
			stage := strings.TrimSpace(parsed["STAGE"])
			if stage != "" && stage != lastStage {
				lastStage = stage
				if percent, ok := stageProgress[stage]; ok {
					updateMySQLPhysicalRestoreRunProgress(runID, mysqlPhysicalRestorePatch{
						Status: "RUNNING", CurrentStage: physicalRestoreStageRestore,
						ProgressPercent: percent, CurrentAction: mysqlRestoreStageAction(stage), LogPath: "",
					})
				}
				logger.Infof("恢复阶段推进: %s", stage)
			}
			if status != "" {
				if strings.EqualFold(status, "SUCCESS") {
					logger.Infof("远端恢复脚本执行成功。")
					return nil
				}
				errText := strings.TrimSpace(parsed["ERROR"])
				tail := extractRestoreLogTail(out)
				return fmt.Errorf("远端恢复失败: %s\n%s", displayOrDefault(errText, "未知错误"), tail)
			}
			if strings.TrimSpace(parsed["ALIVE"]) == "0" {
				tail := extractRestoreLogTail(out)
				return fmt.Errorf("远端恢复进程异常退出（未写入状态标记）\n%s", tail)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("恢复执行超过 6 小时上限，已放弃等待（远端进程可能仍在运行，请人工确认）")
		}
		time.Sleep(3 * time.Second)
	}
}

func extractRestoreLogTail(pollOutput string) string {
	idx := strings.Index(pollOutput, "---- tail ----")
	if idx < 0 {
		return ""
	}
	tail := strings.TrimSpace(pollOutput[idx+len("---- tail ----"):])
	if len(tail) > 2000 {
		tail = tail[len(tail)-2000:]
	}
	return tail
}

func mysqlRestoreStageAction(stage string) string {
	switch strings.ToUpper(strings.TrimSpace(stage)) {
	case "UNPACK":
		return "xbstream 解流备份文件"
	case "PREPARE":
		return "xtrabackup --prepare 崩溃恢复"
	case "STOP_MYSQLD":
		return "停止目标实例"
	case "WIPE":
		return "清空目标数据目录"
	case "COPY_BACK":
		return "回填数据文件到数据目录"
	case "START":
		return "启动目标实例"
	case "VERIFY":
		return "恢复后验证"
	default:
		return "执行恢复中"
	}
}

// buildMySQLPhysicalRestoreExecuteScript：远端执行的恢复脚本（需 root 或 sudo）。
//
// 增量链路的合并遵循 xtrabackup 官方流程：
//  1. 全量基线先 --prepare --apply-log-only（应用 redo，但不做 undo 回滚）
//  2. 中间的每一级增量依次 --prepare --apply-log-only --incremental-dir=<该层>
//  3. 最后一级增量去掉 --apply-log-only，做最终 redo 应用与 undo 回滚
//  4. 从基线目录 --copy-back（此时增量数据已合并进基线目录）
//
// 链路只有一层（纯全量）时保持原流程：直接 --prepare 后 copy-back。
func buildMySQLPhysicalRestoreExecuteScript(resolved mysqlPhysicalRestoreResolved, chain []mysqlPhysicalRestoreChainNode, autoStart bool) string {
	autoStartFlag := "0"
	if autoStart {
		autoStartFlag = "1"
	}
	if len(chain) == 0 {
		chain = []mysqlPhysicalRestoreChainNode{{
			Order:      1,
			BackupType: "FULL",
			RemotePath: strings.TrimSpace(resolved.RemoteBackupFile),
		}}
	}
	baseDir := mysqlPhysicalRestoreRemoteUnpackDir(resolved.WorkDir, chain[0].Order)
	lines := []string{
		`fail() { echo "STATUS=FAILED"; echo "ERROR=$1"; exit 1; }`,
		`echo "STAGE=UNPACK"`,
	}
	for _, node := range chain {
		unpackDir := mysqlPhysicalRestoreRemoteUnpackDir(resolved.WorkDir, node.Order)
		lines = append(lines,
			fmt.Sprintf(`mkdir -p %s || fail "创建解流目录失败"`, shellQuote(unpackDir)),
			fmt.Sprintf(`rm -rf %s/* >/dev/null 2>&1 || true`, shellQuote(unpackDir)),
			fmt.Sprintf(`xbstream -x -C %s < %s || fail "xbstream 解流失败（第 %d 层 %s，请确认 xbstream 在 PATH 中）"`,
				shellQuote(unpackDir), shellQuote(node.RemotePath), node.Order, node.BackupType),
		)
	}
	lines = append(lines, `echo "STAGE=PREPARE"`)
	if len(chain) == 1 {
		lines = append(lines, fmt.Sprintf(`%s --prepare --target-dir=%s || fail "xtrabackup prepare 失败"`,
			shellQuote(resolved.XtraBackupPath), shellQuote(baseDir)))
	} else {
		lines = append(lines, fmt.Sprintf(`%s --prepare --apply-log-only --target-dir=%s || fail "xtrabackup 全量基线 prepare 失败"`,
			shellQuote(resolved.XtraBackupPath), shellQuote(baseDir)))
		for _, node := range chain[1 : len(chain)-1] {
			incrementalDir := mysqlPhysicalRestoreRemoteUnpackDir(resolved.WorkDir, node.Order)
			lines = append(lines, fmt.Sprintf(`%s --prepare --apply-log-only --target-dir=%s --incremental-dir=%s || fail "xtrabackup 合并第 %d 层增量失败"`,
				shellQuote(resolved.XtraBackupPath), shellQuote(baseDir), shellQuote(incrementalDir), node.Order))
		}
		lastNode := chain[len(chain)-1]
		lastIncrementalDir := mysqlPhysicalRestoreRemoteUnpackDir(resolved.WorkDir, lastNode.Order)
		lines = append(lines, fmt.Sprintf(`%s --prepare --target-dir=%s --incremental-dir=%s || fail "xtrabackup 合并末层增量（第 %d 层）失败"`,
			shellQuote(resolved.XtraBackupPath), shellQuote(baseDir), shellQuote(lastIncrementalDir), lastNode.Order))
	}
	lines = append(lines, `echo "STAGE=STOP_MYSQLD"`,
		`STOPPED=0`,
		`SERVICE_UNIT=""`,
		`if command -v systemctl >/dev/null 2>&1; then`,
		`  SERVICE_UNIT=$(systemctl list-units --type=service --state=running --no-legend 2>/dev/null | awk '{print $1}' | grep -iE 'mysqld|mysql' | grep -vE 'backup|agent' | head -n1)`,
		`  if [ -n "$SERVICE_UNIT" ]; then systemctl stop "$SERVICE_UNIT" && STOPPED=1; fi`,
		`fi`,
		fmt.Sprintf(`PORT_FREE=0; for i in $(seq 1 60); do if ! (command -v ss >/dev/null 2>&1 && ss -lnt 2>/dev/null | grep -q ':%d[[:space:]]') && ! (command -v netstat >/dev/null 2>&1 && netstat -lnt 2>/dev/null | grep -q ':%d[[:space:]]'); then PORT_FREE=1; break; fi; sleep 1; done`,
			resolved.TargetPort, resolved.TargetPort),
		`[ "$PORT_FREE" = "1" ] || fail "停止目标实例后端口仍被占用（可能未成功停库），中止恢复以保护数据"`,
		`echo "STAGE=WIPE"`,
		fmt.Sprintf(`[ -d %s ] || fail "目标数据目录不存在: %s"`, shellQuote(resolved.TargetDataDir), shellQuote(resolved.TargetDataDir)),
		fmt.Sprintf(`echo "WIPE_FILE_COUNT=$(find %s -mindepth 1 | wc -l)"`, shellQuote(resolved.TargetDataDir)),
		fmt.Sprintf(`find %s -mindepth 1 -maxdepth 1 -exec rm -rf {} + || fail "清空目标数据目录失败"`,
			shellQuote(resolved.TargetDataDir)),
		fmt.Sprintf(`BINLOG_BASE=%s; if [ -n "$BINLOG_BASE" ]; then rm -f "$BINLOG_BASE.index" "$BINLOG_BASE".[0-9]* 2>/dev/null || true; fi`,
			shellQuote(resolved.LogBinBasename)),
		`echo "STAGE=COPY_BACK"`,
		fmt.Sprintf(`%s --copy-back --target-dir=%s --datadir=%s || fail "xtrabackup copy-back 失败"`,
			shellQuote(resolved.XtraBackupPath), shellQuote(baseDir), shellQuote(resolved.TargetDataDir)),
		fmt.Sprintf(`chown -R %s:%s %s || fail "修正数据目录属主失败"`,
			shellQuote(resolved.MysqlOSUser), shellQuote(resolved.MysqlOSUser), shellQuote(resolved.TargetDataDir)),
		fmt.Sprintf(`BINLOG_BASE=%s; if [ -n "$BINLOG_BASE" ]; then BINLOG_DIR=$(dirname "$BINLOG_BASE"); DATADIR_DIR=$(dirname %s); if [ "$BINLOG_DIR" != "$DATADIR_DIR" ] && [ -d "$BINLOG_DIR" ]; then chown -R %s:%s "$BINLOG_DIR" 2>/dev/null || true; fi; fi`,
			shellQuote(resolved.LogBinBasename), shellQuote(resolved.TargetDataDir),
			shellQuote(resolved.MysqlOSUser), shellQuote(resolved.MysqlOSUser)),
		`echo "STAGE=START"`,
		fmt.Sprintf(`AUTO_START=%s`, shellQuote(autoStartFlag)),
		`if [ "$AUTO_START" = "1" ]; then`,
		`  if [ -n "$SERVICE_UNIT" ]; then`,
		`    systemctl start "$SERVICE_UNIT" || fail "启动目标实例失败（systemctl start $SERVICE_UNIT）"`,
		`  else`,
		`    echo "WARNING=NEED_MANUAL_START：未识别到 systemd 服务单元，请人工启动实例"`,
		`  fi`,
		`else`,
		`  echo "WARNING=SKIP_AUTO_START：按任务设置不自动启动实例"`,
		`fi`,
		`echo "STAGE=VERIFY"`,
		fmt.Sprintf(`VERIFY_OK=0; for i in $(seq 1 60); do if (command -v ss >/dev/null 2>&1 && ss -lnt 2>/dev/null | grep -q ':%d[[:space:]]') || (command -v netstat >/dev/null 2>&1 && netstat -lnt 2>/dev/null | grep -q ':%d[[:space:]]'); then VERIFY_OK=1; break; fi; sleep 2; done`,
			resolved.TargetPort, resolved.TargetPort),
		`if [ "$VERIFY_OK" = "1" ]; then echo "VERIFY=PORT_LISTENING"; else echo "VERIFY=PORT_NOT_LISTENING"; fi`,
		`echo "STATUS=SUCCESS"`,
	)
	return strings.Join(lines, "\n")
}

// --- DB 更新（driver=XTRABACKUP 专用，避免触碰 PG 记录） ---

type mysqlPhysicalRestorePatch struct {
	Status          string
	CurrentStage    string
	ProgressPercent int
	CurrentAction   string
	LogPath         string
	ErrorMessage    string
	ResultSummary   datatypes.JSON
}

func updateMySQLPhysicalRestoreRunProgress(id uint64, patch mysqlPhysicalRestorePatch) {
	updates := map[string]any{
		"last_report_at": commonModels.LocalTime(nowLocalTime()),
	}
	if text := strings.TrimSpace(patch.Status); text != "" {
		updates["status"] = text
		if strings.EqualFold(text, "RUNNING") {
			updates["start_at"] = commonModels.LocalTime(nowLocalTime())
		}
		if strings.EqualFold(text, "SUCCESS") || strings.EqualFold(text, "FAILED") || strings.EqualFold(text, "STOPPED") {
			updates["end_at"] = commonModels.LocalTime(nowLocalTime())
		}
	}
	if text := strings.TrimSpace(patch.CurrentStage); text != "" {
		updates["current_stage"] = text
	}
	if patch.ProgressPercent >= 0 {
		updates["progress_percent"] = clampPercent(patch.ProgressPercent)
	}
	if text := strings.TrimSpace(patch.CurrentAction); text != "" {
		updates["current_action"] = text
	}
	if text := strings.TrimSpace(patch.LogPath); text != "" {
		updates["log_path"] = text
		updates["log_excerpt"] = tailLogExcerpt(text)
	}
	if text := strings.TrimSpace(patch.ErrorMessage); text != "" {
		updates["error_message"] = text
	}
	if len(patch.ResultSummary) > 0 {
		updates["result_summary"] = patch.ResultSummary
	}
	global.App.DB.Model(&backupModels.InsightRestoreRuns{}).
		Where("id = ? AND restore_kind = ? AND driver_type = ?", id, restoreKindPhysical, "XTRABACKUP").Updates(updates)
}

func writeMySQLPhysicalRestoreFailed(id uint64, logPath string, err error) {
	if strings.TrimSpace(logPath) != "" {
		appendBackupRunLog(logPath, "ERROR", "%v", err)
	}
	updateMySQLPhysicalRestoreRunProgress(id, mysqlPhysicalRestorePatch{
		Status: "FAILED", CurrentStage: physicalRestoreStageRestore, ProgressPercent: 100,
		CurrentAction: "执行失败", LogPath: logPath, ErrorMessage: strings.TrimSpace(err.Error()),
	})
}

func writeMySQLPhysicalRestoreStopped(id uint64, logPath string, reason string) {
	if strings.TrimSpace(logPath) != "" {
		appendBackupRunLog(logPath, "WARN", "任务已终止: %s", reason)
	}
	updateMySQLPhysicalRestoreRunProgress(id, mysqlPhysicalRestorePatch{
		Status: "STOPPED", ProgressPercent: 100, CurrentAction: "已终止",
		LogPath: logPath, ErrorMessage: reason,
	})
}

func isMySQLPhysicalRestoreStopRequested(runID uint64) bool {
	if runID == 0 || global.App.DB == nil {
		return false
	}
	var row struct {
		Status string
	}
	if err := global.App.DB.Model(&backupModels.InsightRestoreRuns{}).
		Select("status").
		Where("id = ? AND restore_kind = ? AND driver_type = ?", runID, restoreKindPhysical, "XTRABACKUP").
		Take(&row).Error; err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(row.Status), "STOP_REQUESTED")
}
