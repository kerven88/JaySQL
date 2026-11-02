package services

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	backupForms "github.com/simajiong/jaysql/internal/backup/forms"
	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	"github.com/simajiong/jaysql/internal/global"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/ssh"
)

type OperateBackupNodeService struct {
	*backupForms.OperateBackupNodeForm
	C  *gin.Context
	ID uint64
}

type backupNodeOperateResult struct {
	Action     string   `json:"action"`
	Title      string   `json:"title"`
	Message    string   `json:"message"`
	Output     string   `json:"output"`
	Warnings   []string `json:"warnings,omitempty"`
	Node       any      `json:"node"`
	NodeOnline bool     `json:"node_online"`
}

type backupNodeRemoteConfig struct {
	NodeID        uint64
	Host          string
	Port          int
	BaseDir       string
	Token         string
	SSHUser       string
	SSHPassword   string
	SSHPrivateKey string
	ServerURL     string
	InstallDir    string
	ToolsDir      string
	ServiceName   string
	LogPath       string
}

type sshRunner struct {
	client *ssh.Client
}

func (s *OperateBackupNodeService) Run() (any, error) {
	node, err := loadBackupNodeByID(s.ID)
	if err != nil {
		return nil, err
	}
	cfg, updates, err := s.buildRemoteConfig(node)
	if err != nil {
		return nil, err
	}
	if len(updates) > 0 {
		updates["updated_by"] = currentUsername(s.C)
		if err := global.App.DB.Model(&backupModels.InsightBackupNodes{}).Where("id = ?", node.ID).Updates(updates).Error; err != nil {
			return nil, err
		}
	}
	runner, err := newSSHRunner(cfg)
	if err != nil {
		return nil, fmt.Errorf("连接备份机失败: %w", err)
	}
	defer runner.Close()

	var result *backupNodeOperateResult
	switch strings.TrimSpace(s.Action) {
	case "connect_test":
		result, err = operateNodeConnectTest(runner, cfg, s.C, s.ID)
	case "install_agent":
		result, err = operateNodeInstallOrRepair(runner, cfg, s.C, s.ID, true)
	case "repair_agent":
		result, err = operateNodeInstallOrRepair(runner, cfg, s.C, s.ID, false)
	case "restart_agent":
		result, err = operateNodeRestartAgent(runner, cfg, s.C, s.ID, s.waitOnlineSeconds())
	case "stop_agent":
		result, err = operateNodeStopAgent(runner, cfg, s.C, s.ID)
	default:
		return nil, fmt.Errorf("不支持的节点操作")
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *OperateBackupNodeService) waitOnlineSeconds() int {
	if s.WaitOnlineSec > 0 {
		return s.WaitOnlineSec
	}
	return 15
}

func (s *OperateBackupNodeService) buildRemoteConfig(node *backupModels.InsightBackupNodes) (*backupNodeRemoteConfig, map[string]any, error) {
	host := strings.TrimSpace(node.Host)
	if host == "" {
		return nil, nil, fmt.Errorf("备份节点地址不能为空")
	}
	port := node.Port
	if port <= 0 {
		port = 22
	}
	sshUser := defaultBackupNodeSSHUser(firstNonEmpty(s.SSHUser, node.SSHUser))
	// 库里的节点凭据是密文（历史明文由 Decrypt 幂等原样返回）
	sshPassword := decryptCredential(firstNonEmpty(s.SSHPassword, node.SSHPassword))
	sshPrivateKey := decryptCredential(firstNonEmpty(s.SSHPrivateKey, node.SSHPrivateKey))
	if sshPassword == "" && sshPrivateKey == "" {
		return nil, nil, fmt.Errorf("请先填写备份机密码或 SSH 私钥")
	}
	serverURL, resolveErr := resolveBackupNodeServerURL(firstNonEmpty(s.ServerURL, node.ServerURL), s.C, host)
	if resolveErr != nil {
		return nil, nil, resolveErr
	}
	installDir := defaultBackupNodeInstallDir(firstNonEmpty(s.InstallDir, node.InstallDir))
	toolsDir := defaultBackupNodeToolsDir(firstNonEmpty(s.ToolsDir, node.ToolsDir))
	serviceName := defaultBackupNodeServiceName(firstNonEmpty(s.ServiceName, node.ServiceName), node.ID)
	logPath := defaultBackupNodeLogPath(firstNonEmpty(s.LogPath, node.LogPath), installDir)
	updates := map[string]any{
		"ssh_user":     sshUser,
		"server_url":   serverURL,
		"install_dir":  installDir,
		"tools_dir":    toolsDir,
		"service_name": serviceName,
		"log_path":     logPath,
	}
	if strings.TrimSpace(s.SSHPassword) != "" {
		encrypted, encErr := encryptCredential(s.SSHPassword)
		if encErr != nil {
			return nil, nil, fmt.Errorf("备份节点 SSH 密码加密失败: %w", encErr)
		}
		updates["ssh_password"] = encrypted
	}
	if strings.TrimSpace(s.SSHPrivateKey) != "" {
		encrypted, encErr := encryptCredential(s.SSHPrivateKey)
		if encErr != nil {
			return nil, nil, fmt.Errorf("备份节点 SSH 私钥加密失败: %w", encErr)
		}
		updates["ssh_private_key"] = encrypted
	}
	return &backupNodeRemoteConfig{
		NodeID:        node.ID,
		Host:          host,
		Port:          port,
		BaseDir:       strings.TrimSpace(node.BaseDir),
		Token:         node.Token,
		SSHUser:       sshUser,
		SSHPassword:   sshPassword,
		SSHPrivateKey: sshPrivateKey,
		ServerURL:     serverURL,
		InstallDir:    installDir,
		ToolsDir:      toolsDir,
		ServiceName:   serviceName,
		LogPath:       logPath,
	}, updates, nil
}

func newSSHRunner(cfg *backupNodeRemoteConfig) (*sshRunner, error) {
	authMethods := []ssh.AuthMethod{}
	hasKey := false
	hasPwd := false
	if strings.TrimSpace(cfg.SSHPrivateKey) != "" {
		signer, err := ssh.ParsePrivateKey([]byte(cfg.SSHPrivateKey))
		if err != nil {
			return nil, fmt.Errorf("解析 SSH 私钥失败: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
		hasKey = true
	}
	password := strings.TrimSpace(cfg.SSHPassword)
	if password != "" {
		authMethods = append(authMethods, ssh.Password(password))
		authMethods = append(authMethods, ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
			answers := make([]string, 0, len(questions))
			for range questions {
				answers = append(answers, password)
			}
			return answers, nil
		}))
		hasPwd = true
	}
	if len(authMethods) == 0 {
		return nil, fmt.Errorf("SSH 鉴权信息不能为空")
	}
	user := strings.TrimSpace(cfg.SSHUser)
	host := strings.TrimSpace(cfg.Host)
	port := cfg.Port
	if port <= 0 {
		port = 22
	}
	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", host, port), &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         8 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("SSH 登录 %s@%s:%d 失败: %w（已尝试认证：私钥=%v, 密码/交互=%v）", user, host, port, err, hasKey, hasPwd)
	}
	return &sshRunner{client: client}, nil
}

func (r *sshRunner) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	return r.client.Close()
}

func (r *sshRunner) Run(script string) (string, error) {
	tempPath := fmt.Sprintf("/tmp/goinsight-remote-%d-%d.sh", os.Getpid(), time.Now().UnixNano())
	payload := []byte("umask 022\n" + script + "\n")
	if err := r.Upload(tempPath, payload); err != nil {
		return "", err
	}
	defer cleanupRemoteScript(r.client, tempPath)

	session, err := r.client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	timer := time.AfterFunc(150*time.Second, func() {
		_ = session.Close()
	})
	defer timer.Stop()
	err = session.Run("/bin/sh " + shellQuote(tempPath))
	output := strings.TrimSpace(stdout.String())
	if stderrText := strings.TrimSpace(stderr.String()); stderrText != "" {
		if output == "" {
			output = stderrText
		} else {
			output = output + "\n" + stderrText
		}
	}
	if err != nil && !timer.Stop() {
		if output != "" {
			return output, fmt.Errorf("远程脚本执行超过 150 秒上限已强制终止: %w\n输出: %s", err, output)
		}
		return output, fmt.Errorf("远程脚本执行超过 150 秒上限已强制终止: %w", err)
	}
	return output, err
}

func cleanupRemoteScript(client *ssh.Client, path string) {
	if client == nil || strings.TrimSpace(path) == "" {
		return
	}
	session, err := client.NewSession()
	if err != nil {
		return
	}
	defer session.Close()
	_ = session.Run("sh -lc " + shellQuote("rm -f "+shellQuote(path)))
}

func (r *sshRunner) Upload(targetPath string, content []byte) error {
	session, err := r.client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	session.Stderr = &buf
	if err := session.Start("sh -lc " + shellQuote("umask 022 && cat > "+shellQuote(targetPath))); err != nil {
		return err
	}
	if _, err := stdin.Write(content); err != nil {
		_ = stdin.Close()
		return err
	}
	_ = stdin.Close()
	if err := session.Wait(); err != nil {
		if strings.TrimSpace(buf.String()) != "" {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(buf.String()))
		}
		return err
	}
	return nil
}

func operateNodeConnectTest(runner *sshRunner, cfg *backupNodeRemoteConfig, c *gin.Context, nodeID uint64) (*backupNodeOperateResult, error) {
	output, err := runner.Run(strings.Join([]string{
		"set -e",
		"hostname",
		"whoami",
		"uname -sr",
		"mkdir -p " + shellQuote(cfg.BaseDir),
		"test -w " + shellQuote(cfg.BaseDir),
		"echo '__GI_CONNECT_OK__'",
	}, "\n"))
	if err != nil {
		return nil, fmt.Errorf("连接测试失败: %v\n%s", err, output)
	}
	nodeDetail, detailErr := refreshBackupNodeDetail(nodeID, c)
	if detailErr != nil {
		return nil, detailErr
	}
	return &backupNodeOperateResult{
		Action:     "connect_test",
		Title:      "连接测试成功",
		Message:    "平台已能连接到备份机，并确认基准目录可写。",
		Output:     output,
		Node:       nodeDetail,
		NodeOnline: currentNodeOnline(nodeID),
	}, nil
}

func operateNodeInstallOrRepair(runner *sshRunner, cfg *backupNodeRemoteConfig, c *gin.Context, nodeID uint64, installMode bool) (*backupNodeOperateResult, error) {
	warnings := []string{}
	sudoCheckOutput, sudoErr := runner.Run(strings.Join([]string{
		"set +e",
		"WHOAMI=$(id -un 2>/dev/null || whoami 2>/dev/null || echo root)",
		"if [ \"$WHOAMI\" = 'root' ]; then",
		"  echo __GI_SUDO_OK_AS_ROOT__",
		"  exit 0",
		"fi",
		"if command -v sudo >/dev/null 2>&1; then",
		"  sudo -n true >/dev/null 2>&1",
		"  SUDO_N_STATUS=$?",
		"  if [ $SUDO_N_STATUS -eq 0 ]; then",
		"    echo __GI_SUDO_OK__",
		"    exit 0",
		"  fi",
		"  echo \"__GI_SUDO_FAIL_CODE__=$SUDO_N_STATUS\"",
		"  echo \"SUDO_DETAIL=$(sudo -n true 2>&1 || true)\"",
		"  exit 5",
		"else",
		"  echo '__GI_SUDO_MISSING__'",
		"  exit 6",
		"fi",
	}, "\n"))
	if sudoErr != nil {
		lower := strings.ToLower(sudoCheckOutput)
		user := strings.TrimSpace(cfg.SSHUser)
		if strings.Contains(lower, "__gi_sudo_missing__") {
			return nil, fmt.Errorf("数据库主机 %s 上未安装 sudo 命令，请先以 root 身份执行 `yum install -y sudo` 或 `apt-get install -y sudo` 后再试", cfg.Host)
		}
		return nil, fmt.Errorf("SSH 用户 %s@%s 缺少 passwordless sudo 权限，无法安装/修复备份 Agent 或执行备份命令。%s\n原始执行输出：\n%s", user, cfg.Host, passwordlessSudoHint(user), sudoCheckOutput)
	}
	agentBinary, sourcePath, err := loadAgentBinaryPayload()
	if err != nil {
		return nil, err
	}
	tempBinary := fmt.Sprintf("/tmp/goinsight-backup-agent-%d", nodeID)
	tempUnit := fmt.Sprintf("/tmp/goinsight-backup-agent-%d.service", nodeID)
	if err := runner.Upload(tempBinary, agentBinary); err != nil {
		return nil, fmt.Errorf("上传 Agent 二进制失败: %w", err)
	}
	unitContent := buildAgentSystemdConfig(cfg)
	if err := runner.Upload(tempUnit, []byte(unitContent)); err != nil {
		return nil, fmt.Errorf("上传 systemd 配置失败: %w", err)
	}
	toolsCheck, _ := runner.Run("sh -lc " + shellQuote("if [ -d "+shellQuote(cfg.ToolsDir)+" ]; then echo TOOLS_OK; else echo TOOLS_MISSING; fi"))
	if strings.Contains(toolsCheck, "TOOLS_MISSING") {
		warnings = append(warnings, fmt.Sprintf("目标机器上的工具目录 %s 当前不存在，Agent 可启动，但后续备份执行前仍需准备数据库客户端工具。", cfg.ToolsDir))
	}
	script := buildInstallOrRepairScript(cfg, tempBinary, tempUnit)
	output, err := runner.Run(script)
	if err != nil {
		return nil, fmt.Errorf("执行远程%s失败: %v\n%s", actionLabel(installMode), err, output)
	}
	nodeDetail, detailErr := waitBackupNodeOnline(nodeID, c, sToDurationSeconds(15))
	if detailErr != nil {
		warnings = append(warnings, detailErr.Error())
		nodeDetail, _ = refreshBackupNodeDetail(nodeID, c)
	}
	return &backupNodeOperateResult{
		Action:     ternaryAction(installMode, "install_agent", "repair_agent"),
		Title:      ternaryText(installMode, "安装完成", "修复完成"),
		Message:    ternaryText(installMode, "平台已完成 Agent 安装并尝试拉起服务。", "平台已完成 Agent 修复并尝试拉起服务。"),
		Output:     fmt.Sprintf("本地 Agent 来源：%s\n\n%s", sourcePath, output),
		Warnings:   warnings,
		Node:       nodeDetail,
		NodeOnline: currentNodeOnline(nodeID),
	}, nil
}

func operateNodeRestartAgent(runner *sshRunner, cfg *backupNodeRemoteConfig, c *gin.Context, nodeID uint64, waitSec int) (*backupNodeOperateResult, error) {
	script := buildRestartScript(cfg)
	output, err := runner.Run(script)
	if err != nil {
		return nil, fmt.Errorf("远程重启 Agent 失败: %v\n%s", err, output)
	}
	nodeDetail, detailErr := waitBackupNodeOnline(nodeID, c, sToDurationSeconds(waitSec))
	warnings := []string{}
	if detailErr != nil {
		warnings = append(warnings, detailErr.Error())
		nodeDetail, _ = refreshBackupNodeDetail(nodeID, c)
	}
	return &backupNodeOperateResult{
		Action:     "restart_agent",
		Title:      "重启命令已执行",
		Message:    "平台已向备份机下发 Agent 重启请求。",
		Output:     output,
		Warnings:   warnings,
		Node:       nodeDetail,
		NodeOnline: currentNodeOnline(nodeID),
	}, nil
}

func operateNodeStopAgent(runner *sshRunner, cfg *backupNodeRemoteConfig, c *gin.Context, nodeID uint64) (*backupNodeOperateResult, error) {
	script := buildStopScript(cfg)
	output, err := runner.Run(script)
	if err != nil {
		return nil, fmt.Errorf("远程停止 Agent 失败: %v\n%s", err, output)
	}
	_ = global.App.DB.Model(&backupModels.InsightBackupNodes{}).
		Where("id = ?", nodeID).
		Updates(map[string]any{
			"status":     "OFFLINE",
			"updated_by": currentUsername(c),
		}).Error
	nodeDetail, _ := refreshBackupNodeDetail(nodeID, c)
	return &backupNodeOperateResult{
		Action:     "stop_agent",
		Title:      "停止命令已执行",
		Message:    "平台已向备份机下发 Agent 停止请求。",
		Output:     output,
		Node:       nodeDetail,
		NodeOnline: false,
	}, nil
}

func waitBackupNodeOnline(nodeID uint64, c *gin.Context, timeout time.Duration) (any, error) {
	deadline := time.Now().Add(timeout)
	for {
		detail, err := refreshBackupNodeDetail(nodeID, c)
		if err != nil {
			return nil, err
		}
		node, loadErr := loadBackupNodeByID(nodeID)
		if loadErr == nil && strings.EqualFold(strings.TrimSpace(string(node.Status)), "ONLINE") {
			return detail, nil
		}
		if time.Now().After(deadline) {
			return detail, fmt.Errorf("Agent 已执行远程操作，但平台在 %d 秒内仍未收到心跳，请检查网络连通性、server 地址和 token 配置", int(timeout/time.Second))
		}
		time.Sleep(1 * time.Second)
	}
}

func currentNodeOnline(nodeID uint64) bool {
	node, err := loadBackupNodeByID(nodeID)
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(string(node.Status)), "ONLINE")
}

func killAgentProcessesScript(nodeID uint64, signal string) string {
	pattern := fmt.Sprintf("(^|[[:space:]])-node-id([[:space:]]+|=)%d([[:space:]]|$)", nodeID)
	return "if command -v pgrep >/dev/null 2>&1 && command -v kill >/dev/null 2>&1; then " +
		"PIDS=$(pgrep -a -x " + shellQuote("backup-agent") + " 2>/dev/null | grep -E -- " + shellQuote(pattern) + " | awk '{print $1}' || true); " +
		"if [ -n \"$PIDS\" ]; then " +
		"  kill " + signal + " $PIDS 2>/dev/null || true; " +
		"fi; " +
		"fi"
}

func buildInstallOrRepairScript(cfg *backupNodeRemoteConfig, tempBinary string, tempUnit string) string {
	agentBin := shellQuote(filepath.Join(cfg.InstallDir, "backup-agent"))
	logDir := shellQuote(filepath.Dir(cfg.LogPath))
	logPath := shellQuote(cfg.LogPath)
	unitPath := shellQuote("/etc/systemd/system/" + cfg.ServiceName + ".service")
	serviceName := shellQuote(cfg.ServiceName)
	envTools := "GOINSIGHT_BACKUP_TOOL_HOME=" + shellQuote(cfg.ToolsDir)
	argsCommon := "-server " + shellQuote(cfg.ServerURL) + " -node-id " + fmt.Sprintf("%d", cfg.NodeID) + " -token " + shellQuote(cfg.Token)
	installScript := strings.Join([]string{
		"umask 022",
		"echo '[install] phase 1/4: stop previous agent'",
		"set +e; " + killAgentProcessesScript(cfg.NodeID, "-TERM") + "; sleep 1; " + killAgentProcessesScript(cfg.NodeID, "-9") + "; set -e",
		"echo '[install] phase 2/4: lay down binaries and dirs'",
		"mkdir -p " + shellQuote(cfg.InstallDir) + " " + shellQuote(cfg.BaseDir) + " " + shellQuote(cfg.ToolsDir) + " " + logDir,
		"mv " + shellQuote(tempBinary) + " " + agentBin,
		"chmod 0755 " + agentBin,
		"if command -v systemctl >/dev/null 2>&1; then",
		"  echo '[install] phase 3/4: install via systemd'",
		"  mv " + shellQuote(tempUnit) + " " + unitPath + " && chmod 0644 " + unitPath + " || true",
		"  ( systemctl daemon-reload ) </dev/null >/dev/null 2>&1 || true",
		"  ( systemctl enable --now " + serviceName + " ) </dev/null >/dev/null 2>&1 || true",
		"  ( systemctl restart " + serviceName + " ) </dev/null >/dev/null 2>&1 || true",
		"  ( systemctl is-active " + serviceName + " ) </dev/null || true",
		"else",
		"  echo '[install] phase 3/4: install via nohup standalone'",
		"  set +e; " + killAgentProcessesScript(cfg.NodeID, "-TERM") + "; sleep 1; " + killAgentProcessesScript(cfg.NodeID, "-9") + "; set -e",
		"  ( nohup setsid env " + envTools + " " + agentBin + " " + argsCommon + " </dev/null >> " + logPath + " 2>&1 & disown ) >/dev/null 2>&1 || true",
		"fi",
		"echo '[install] phase 4/4: collect tail log (includes historical entries; verify current heartbeat)'",
		"sleep 2",
		"tail -n 40 " + logPath + " 2>/dev/null || echo '(no log content yet)'",
	}, "\n")
	return wrapRemoteRootScript(cfg, installScript)
}

func buildRestartScript(cfg *backupNodeRemoteConfig) string {
	agentBin := shellQuote(filepath.Join(cfg.InstallDir, "backup-agent"))
	logDir := shellQuote(filepath.Dir(cfg.LogPath))
	logPath := shellQuote(cfg.LogPath)
	unitPath := shellQuote("/etc/systemd/system/" + cfg.ServiceName + ".service")
	serviceName := shellQuote(cfg.ServiceName)
	envTools := "GOINSIGHT_BACKUP_TOOL_HOME=" + shellQuote(cfg.ToolsDir)
	argsCommon := "-server " + shellQuote(cfg.ServerURL) + " -node-id " + fmt.Sprintf("%d", cfg.NodeID) + " -token " + shellQuote(cfg.Token)
	restartScript := strings.Join([]string{
		"umask 022",
		"echo '[restart] phase 1/3: stop previous agent'",
		"set +e; " + killAgentProcessesScript(cfg.NodeID, "-TERM") + "; sleep 1; " + killAgentProcessesScript(cfg.NodeID, "-9") + "; set -e",
		"if command -v systemctl >/dev/null 2>&1 && [ -f " + unitPath + " ]; then",
		"  echo '[restart] phase 2/3: restart via systemd'",
		"  ( systemctl daemon-reload ) </dev/null >/dev/null 2>&1 || true",
		"  ( systemctl restart " + serviceName + " ) </dev/null >/dev/null 2>&1 || true",
		"  ( systemctl is-active " + serviceName + " ) </dev/null || true",
		"else",
		"  echo '[restart] phase 2/3: restart via nohup standalone'",
		"  mkdir -p " + logDir,
		"  ( nohup setsid env " + envTools + " " + agentBin + " " + argsCommon + " </dev/null >> " + logPath + " 2>&1 & disown ) >/dev/null 2>&1 || true",
		"fi",
		"echo '[restart] phase 3/3: collect tail log'",
		"sleep 2",
		"tail -n 40 " + logPath + " 2>/dev/null || echo '(no log content yet)'",
	}, "\n")
	return wrapRemoteRootScript(cfg, restartScript)
}

func buildStopScript(cfg *backupNodeRemoteConfig) string {
	unitPath := shellQuote("/etc/systemd/system/" + cfg.ServiceName + ".service")
	serviceName := shellQuote(cfg.ServiceName)
	stopScript := strings.Join([]string{
		"umask 022",
		"echo '[stop] phase 1/2: stop service or process'",
		"if command -v systemctl >/dev/null 2>&1 && [ -f " + unitPath + " ]; then",
		"  ( systemctl stop " + serviceName + " ) </dev/null >/dev/null 2>&1 || true",
		"  ( systemctl is-active " + serviceName + " ) </dev/null || true",
		"fi",
		"set +e; " + killAgentProcessesScript(cfg.NodeID, "-TERM") + "; sleep 1; " + killAgentProcessesScript(cfg.NodeID, "-9") + "; set -e",
		"echo '[stop] phase 2/2: collect process status'",
		"if command -v systemctl >/dev/null 2>&1 && [ -f " + unitPath + " ]; then",
		"  ( systemctl status " + serviceName + " --no-pager -l ) </dev/null 2>&1 | tail -n 20 || true",
		"else",
		"  pgrep -af " + shellQuote("backup-agent") + " 2>/dev/null || echo 'agent process not running'",
		"fi",
	}, "\n")
	return wrapRemoteRootScript(cfg, stopScript)
}

func wrapRemoteRootScript(cfg *backupNodeRemoteConfig, script string) string {
	if strings.EqualFold(strings.TrimSpace(cfg.SSHUser), "root") {
		return script
	}
	inner := "trap '' HUP TERM INT; umask 022; " + script
	if strings.TrimSpace(cfg.SSHPassword) != "" {
		return fmt.Sprintf("printf '%%s\\n' %s | sudo -S -E -p '' /bin/sh -lc %s", shellQuote(cfg.SSHPassword), shellQuote(inner))
	}
	return "sudo -n -E -p '' /bin/sh -lc " + shellQuote(inner)
}

func buildAgentSystemdConfig(cfg *backupNodeRemoteConfig) string {
	return strings.Join([]string{
		"[Unit]",
		fmt.Sprintf("Description=JaySQL Backup Agent Node %d", cfg.NodeID),
		"After=network-online.target",
		"Wants=network-online.target",
		"",
		"[Service]",
		"Type=simple",
		fmt.Sprintf("User=%s", cfg.SSHUser),
		fmt.Sprintf("WorkingDirectory=%s", cfg.InstallDir),
		fmt.Sprintf("Environment=GOINSIGHT_BACKUP_TOOL_HOME=%s", cfg.ToolsDir),
		fmt.Sprintf("ExecStart=%s -server %s -node-id %d -token %s", filepath.Join(cfg.InstallDir, "backup-agent"), cfg.ServerURL, cfg.NodeID, cfg.Token),
		fmt.Sprintf("StandardOutput=append:%s", cfg.LogPath),
		fmt.Sprintf("StandardError=append:%s", cfg.LogPath),
		"Restart=always",
		"RestartSec=5",
		"",
		"[Install]",
		"WantedBy=multi-user.target",
		"",
	}, "\n")
}

func loadAgentBinaryPayload() ([]byte, string, error) {
	candidates := []string{
		strings.TrimSpace(os.Getenv("GOINSIGHT_AGENT_BINARY")),
		"/usr/local/bin/backup-agent",
		"/opt/app/backup-agent/backup-agent",
		filepath.Join(filepath.Dir(os.Args[0]), "backup-agent"),
		"./backup-agent",
	}
	for _, candidate := range candidates {
		path := strings.TrimSpace(candidate)
		if path == "" {
			continue
		}
		content, err := os.ReadFile(path)
		if err == nil && len(content) > 0 {
			return content, path, nil
		}
	}
	return nil, "", fmt.Errorf("未找到可用于下发的 backup-agent 二进制，请先在服务机器准备 /usr/local/bin/backup-agent 或设置 GOINSIGHT_AGENT_BINARY")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func ternaryText(flag bool, yes string, no string) string {
	if flag {
		return yes
	}
	return no
}

func ternaryAction(flag bool, yes string, no string) string {
	if flag {
		return yes
	}
	return no
}

func actionLabel(installMode bool) string {
	if installMode {
		return "安装"
	}
	return "修复"
}

func sToDurationSeconds(v int) time.Duration {
	if v <= 0 {
		v = 15
	}
	return time.Duration(v) * time.Second
}
