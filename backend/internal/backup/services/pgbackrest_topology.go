package services

import (
	"fmt"
	"strings"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
)

func ensurePGBackRestRepositoryAccess(instance *commonModels.InsightInstances, executorNode *backupModels.InsightBackupNodes, repositoryNode *backupModels.InsightBackupNodes, repoPath string, repoRemote bool) error {
	if executorNode == nil || repositoryNode == nil {
		return fmt.Errorf("pgBackRest 远端仓库拓扑不完整")
	}
	sameHost := sameBackupNodeHost(executorNode, repositoryNode)
	repoSSHUser := defaultBackupNodeSSHUser(repositoryNode.SSHUser)
	chmodChownFrag := []string{
		"TARGET_USER=\"" + repoSSHUser + "\"",
		"if [ -z \"$TARGET_USER\" ] || [ \"$TARGET_USER\" = \"root\" ]; then TARGET_USER=$(id -un 2>/dev/null || echo root); fi",
		"TARGET_GROUP=$(id -gn \"$TARGET_USER\" 2>/dev/null || echo \"$TARGET_USER\")",
		"mkdir -p " + shellQuote(repoPath),
		"REPO_PATH_P1=$(dirname " + shellQuote(repoPath) + ") || true",
		"REPO_PATH_P2=$(dirname \"$REPO_PATH_P1\") || true",
		"REPO_PATH_P3=$(dirname \"$REPO_PATH_P2\") || true",
		"for _d in " + shellQuote(repoPath) + " \"$REPO_PATH_P1\" \"$REPO_PATH_P2\" \"$REPO_PATH_P3\"; do",
		"  if [ -n \"$_d\" ] && [ -d \"$_d\" ]; then",
		"    if command -v chmod >/dev/null 2>&1; then chmod 2775 \"$_d\" || exit 21; fi",
		"    if command -v chown >/dev/null 2>&1; then chown \"${TARGET_USER}:${TARGET_GROUP}\" \"$_d\" || exit 22; fi",
		"  fi",
		"done",
		"test -w " + shellQuote(repoPath),
	}
	if !repoRemote || sameHost {
		tryCfg, tryCfgErr := buildExecutorNodeRemoteConfigWithInstance(executorNode, instance)
		if tryCfgErr == nil {
			if runner, dialErr := newSSHRunner(tryCfg); dialErr == nil {
				defer runner.Close()
				script := strings.Join(append([]string{"set -e"}, chmodChownFrag...), "\n")
				output, runErr := runner.Run(wrapRemoteRootScript(tryCfg, script))
				if runErr == nil {
					return nil
				}
				_ = output
			}
		}
		repositoryCfg, err := buildStoredBackupNodeRemoteConfig(repositoryNode)
		if err != nil {
			return err
		}
		repositoryRunner, err := newSSHRunner(repositoryCfg)
		if err != nil {
			return fmt.Errorf("连接仓库节点失败: %w", err)
		}
		defer repositoryRunner.Close()
		script := strings.Join(append([]string{"set -e"}, chmodChownFrag...), "\n")
		output, err := repositoryRunner.Run(wrapRemoteRootScript(repositoryCfg, script))
		if err != nil {
			return fmt.Errorf("准备本地仓库目录失败: %v\n%s", err, output)
		}
		return nil
	}
	executorCfg, err := buildExecutorNodeRemoteConfigWithInstance(executorNode, instance)
	if err != nil {
		return err
	}
	repositoryCfg, err := buildStoredBackupNodeRemoteConfig(repositoryNode)
	if err != nil {
		return err
	}
	executorRunner, err := newSSHRunner(executorCfg)
	if err != nil {
		return fmt.Errorf("连接执行节点失败: %w", err)
	}
	defer executorRunner.Close()
	repositoryRunner, err := newSSHRunner(repositoryCfg)
	if err != nil {
		return fmt.Errorf("连接仓库节点失败: %w", err)
	}
	defer repositoryRunner.Close()

	publicKey, err := ensureExecutorSSHKey(executorRunner)
	if err != nil {
		return err
	}
	if err := authorizeExecutorKeyOnRepository(repositoryRunner, repositoryCfg, publicKey, repoPath); err != nil {
		return err
	}
	if err := verifyExecutorCanReachRepository(executorRunner, repositoryCfg, repositoryNode, repoPath); err != nil {
		return err
	}
	return nil
}

func buildExecutorNodeRemoteConfigWithInstance(node *backupModels.InsightBackupNodes, instance *commonModels.InsightInstances) (*backupNodeRemoteConfig, error) {
	if node == nil {
		return nil, fmt.Errorf("备份节点不存在")
	}
	cfg := &backupNodeRemoteConfig{
		NodeID:        node.ID,
		Host:          strings.TrimSpace(node.Host),
		Port:          node.Port,
		BaseDir:       strings.TrimSpace(node.BaseDir),
		Token:         node.Token,
		SSHUser:       defaultBackupNodeSSHUser(node.SSHUser),
		SSHPassword:   strings.TrimSpace(node.SSHPassword),
		SSHPrivateKey: strings.TrimSpace(node.SSHPrivateKey),
		ServerURL:     strings.TrimSpace(node.ServerURL),
		InstallDir:    defaultBackupNodeInstallDir(node.InstallDir),
		ToolsDir:      defaultBackupNodeToolsDir(node.ToolsDir),
		ServiceName:   defaultBackupNodeServiceName(node.ServiceName, node.ID),
		LogPath:       defaultBackupNodeLogPath(node.LogPath, node.InstallDir),
	}
	if cfg.Host == "" {
		return nil, fmt.Errorf("备份节点地址不能为空")
	}
	if cfg.Port <= 0 {
		cfg.Port = 22
	}
	if instance != nil && strings.EqualFold(strings.TrimSpace(instance.Hostname), cfg.Host) {
		sshUser, sshPassword, sshPrivateKey, sshPort, ok, _ := resolveSSHCredentialsFromInstance(instance)
		if ok {
			cfg.SSHUser = defaultBackupNodeSSHUser(sshUser)
			cfg.SSHPassword = strings.TrimSpace(sshPassword)
			cfg.SSHPrivateKey = strings.TrimSpace(sshPrivateKey)
			if sshPort > 0 {
				cfg.Port = sshPort
			}
		}
	}
	if cfg.SSHPassword == "" && cfg.SSHPrivateKey == "" {
		return nil, fmt.Errorf("节点 %s 缺少 SSH 密码或私钥，无法自动完成 pgBackRest 仓库接入", strings.TrimSpace(node.Name))
	}
	return cfg, nil
}

func buildStoredBackupNodeRemoteConfig(node *backupModels.InsightBackupNodes) (*backupNodeRemoteConfig, error) {
	if node == nil {
		return nil, fmt.Errorf("备份节点不存在")
	}
	host := strings.TrimSpace(node.Host)
	if host == "" {
		return nil, fmt.Errorf("备份节点地址不能为空")
	}
	cfg := &backupNodeRemoteConfig{
		NodeID:        node.ID,
		Host:          host,
		Port:          node.Port,
		BaseDir:       strings.TrimSpace(node.BaseDir),
		Token:         node.Token,
		SSHUser:       defaultBackupNodeSSHUser(node.SSHUser),
		SSHPassword:   strings.TrimSpace(node.SSHPassword),
		SSHPrivateKey: strings.TrimSpace(node.SSHPrivateKey),
		ServerURL:     strings.TrimSpace(node.ServerURL),
		InstallDir:    defaultBackupNodeInstallDir(node.InstallDir),
		ToolsDir:      defaultBackupNodeToolsDir(node.ToolsDir),
		ServiceName:   defaultBackupNodeServiceName(node.ServiceName, node.ID),
		LogPath:       defaultBackupNodeLogPath(node.LogPath, node.InstallDir),
	}
	if cfg.Port <= 0 {
		cfg.Port = 22
	}
	if cfg.SSHPassword == "" && cfg.SSHPrivateKey == "" {
		return nil, fmt.Errorf("节点 %s 缺少 SSH 密码或私钥，无法自动完成 pgBackRest 仓库接入", strings.TrimSpace(node.Name))
	}
	return cfg, nil
}

func ensureExecutorSSHKey(runner *sshRunner) (string, error) {
	output, err := runner.Run(strings.Join([]string{
		"set -e",
		"WHO_AM_I=$(id -un 2>/dev/null || true)",
		"[ -n \"$WHO_AM_I\" ] && [ \"$WHO_AM_I\" != \"root\" ] && HOME_DIR=$(eval echo \"~$WHO_AM_I\") || HOME_DIR=$HOME",
		"if [ -n \"$WHO_AM_I\" ] && [ \"$WHO_AM_I\" != \"root\" ] && [ -d \"$HOME_DIR\" ]; then",
		"  true",
		"else",
		"  WHO_AM_I=$USER || true",
		"  HOME_DIR=$HOME",
		"fi",
		"SSH_DIR=\"${HOME_DIR}/.ssh\"",
		"mkdir -p \"$SSH_DIR\"",
		"chmod 700 \"$SSH_DIR\"",
		"OWNER=\"$WHO_AM_I\"",
		"if [ \"$OWNER\" = \"\" ] || [ \"$OWNER\" = \"root\" ]; then OWNER=$(stat -c %U \"$HOME_DIR\" 2>/dev/null || echo root); fi",
		"OWNER_GROUP=$(id -gn \"$OWNER\" 2>/dev/null || echo \"$OWNER\")",
		"if command -v chown >/dev/null 2>&1; then chown -R \"${OWNER}:${OWNER_GROUP}\" \"$SSH_DIR\" || true; fi",
		"command -v ssh >/dev/null 2>&1",
		"command -v ssh-keygen >/dev/null 2>&1",
		"EXISTING=''",
		"if [ -f \"$SSH_DIR/id_ed25519.pub\" ]; then EXISTING=ed25519; fi",
		"if [ -z \"$EXISTING\" ] && [ -f \"$SSH_DIR/id_rsa.pub\" ]; then EXISTING=rsa; fi",
		"KEY_PATH=''",
		"if [ -n \"$EXISTING\" ]; then KEY_PATH=\"$SSH_DIR/id_$EXISTING\"; echo \"REUSED:$EXISTING\"; fi",
		"if [ -z \"$KEY_PATH\" ]; then ssh-keygen -t ed25519 -N '' -f \"$SSH_DIR/id_ed25519\" >/dev/null 2>&1; KEY_PATH=\"$SSH_DIR/id_ed25519\"; echo \"GENERATED:ed25519\"; fi",
		"if command -v chown >/dev/null 2>&1; then chown \"${OWNER}:${OWNER_GROUP}\" \"$KEY_PATH\" \"${KEY_PATH}.pub\" || true; fi",
		"chmod 600 \"$KEY_PATH\" 2>/dev/null || true",
		"chmod 644 \"${KEY_PATH}.pub\" 2>/dev/null || true",
		"cat \"${KEY_PATH}.pub\"",
	}, "\n"))
	if err != nil {
		return "", fmt.Errorf("准备执行节点 SSH 密钥失败: %v\n%s", err, output)
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	publicKey := ""
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "REUSED:") || strings.HasPrefix(line, "GENERATED:") {
			continue
		}
		publicKey = line
		break
	}
	if publicKey == "" {
		return "", fmt.Errorf("未能读取执行节点 SSH 公钥")
	}
	return publicKey, nil
}

func authorizeExecutorKeyOnRepository(runner *sshRunner, cfg *backupNodeRemoteConfig, publicKey string, repoPath string) error {
	repoSSHUser := defaultBackupNodeSSHUser(cfg.SSHUser)
	repoToolsDir := defaultBackupNodeToolsDir(cfg.ToolsDir)
	script := strings.Join([]string{
		"set -e",
		"TARGET_USER=\"" + repoSSHUser + "\"",
		"if [ -z \"$TARGET_USER\" ] || [ \"$TARGET_USER\" = \"root\" ]; then",
		"  WHO_AM_I=$(id -un 2>/dev/null || true)",
		"  [ -n \"$WHO_AM_I\" ] && [ \"$WHO_AM_I\" != \"root\" ] && HOME_DIR=$(eval echo \"~$WHO_AM_I\") || HOME_DIR=$HOME",
		"  if [ -n \"$WHO_AM_I\" ] && [ \"$WHO_AM_I\" != \"root\" ] && [ -d \"$HOME_DIR\" ]; then true; else WHO_AM_I=$USER || true; HOME_DIR=$HOME; fi",
		"  TARGET_USER=\"$WHO_AM_I\"",
		"  TARGET_GROUP=$(id -gn \"$TARGET_USER\" 2>/dev/null || echo \"$TARGET_USER\")",
		"else",
		"  HOME_DIR=$(eval echo \"~$TARGET_USER\")",
		"  TARGET_GROUP=$(id -gn \"$TARGET_USER\" 2>/dev/null || echo \"$TARGET_USER\")",
		"fi",
		"[ -n \"$HOME_DIR\" ] && [ -d \"$HOME_DIR\" ] || (echo \"ERR_HOME_DIR target_user=$TARGET_USER home=$HOME_DIR\"; exit 11)",
		"SSH_DIR=\"${HOME_DIR}/.ssh\"",
		"mkdir -p \"$SSH_DIR\"",
		"chmod 700 \"$SSH_DIR\"",
		"touch \"$SSH_DIR/authorized_keys\"",
		"chmod 600 \"$SSH_DIR/authorized_keys\"",
		"grep -qxF " + shellQuote(publicKey) + " \"$SSH_DIR/authorized_keys\" || printf '%s\\n' " + shellQuote(publicKey) + " >> \"$SSH_DIR/authorized_keys\"",
		"if command -v sort >/dev/null 2>&1; then sort -u \"$SSH_DIR/authorized_keys\" > \"$SSH_DIR/authorized_keys.tmp\" && mv \"$SSH_DIR/authorized_keys.tmp\" \"$SSH_DIR/authorized_keys\"; fi",
		"chmod 600 \"$SSH_DIR/authorized_keys\"",
		"if command -v chown >/dev/null 2>&1; then",
		"  chown -R \"${TARGET_USER}:${TARGET_GROUP}\" \"$SSH_DIR\" || exit 12",
		"  chown \"${TARGET_USER}:${TARGET_GROUP}\" \"$SSH_DIR/authorized_keys\" || exit 13",
		"fi",
		"mkdir -p " + shellQuote(repoPath),
		"REPO_PATH_P1=$(dirname " + shellQuote(repoPath) + ") || true",
		"REPO_PATH_P2=$(dirname \"$REPO_PATH_P1\") || true",
		"REPO_PATH_P3=$(dirname \"$REPO_PATH_P2\") || true",
		"for _d in " + shellQuote(repoPath) + " \"$REPO_PATH_P1\" \"$REPO_PATH_P2\" \"$REPO_PATH_P3\"; do",
		"  if [ -n \"$_d\" ] && [ -d \"$_d\" ]; then",
		"    if command -v chmod >/dev/null 2>&1; then chmod 2775 \"$_d\" || exit 31; fi",
		"    if command -v chown >/dev/null 2>&1; then chown \"${TARGET_USER}:${TARGET_GROUP}\" \"$_d\" || exit 32; fi",
		"  fi",
		"done",
		"test -w " + shellQuote(repoPath),
		"if [ -n \"" + repoToolsDir + "\" ] && [ -d " + shellQuote(repoToolsDir) + " ]; then true; fi",
	}, "\n")
	output, err := runner.Run(wrapRemoteRootScript(cfg, script))
	if err != nil {
		return fmt.Errorf("准备仓库节点失败: %v\n%s", err, output)
	}
	return nil
}

func verifyExecutorCanReachRepository(runner *sshRunner, repositoryCfg *backupNodeRemoteConfig, repositoryNode *backupModels.InsightBackupNodes, repoPath string) error {
	repoHost := strings.TrimSpace(repositoryNode.Host)
	repoUser := defaultBackupNodeSSHUser(repositoryNode.SSHUser)
	repoPort := repositoryNode.Port
	repoSSHPassword := ""
	if repositoryCfg != nil {
		repoSSHPassword = strings.TrimSpace(repositoryCfg.SSHPassword)
	}
	if repoPort <= 0 {
		repoPort = 22
	}
	sshBaseOptsPubkey := "-o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=15"
	sshBaseOptsSSHPass := "-o BatchMode=no -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=15 -o NumberOfPasswordPrompts=1 -o PreferredAuthentications=password"
	remoteCmd := strings.Join([]string{
		"set -e",
		"TARGET_USER=" + shellQuote(repoUser),
		"TARGET_GROUP=$(id -gn \"$TARGET_USER\" 2>/dev/null || echo \"$TARGET_USER\")",
		"mkdir -p " + shellQuote(repoPath),
		"REPO_PATH_P1=$(dirname " + shellQuote(repoPath) + ") || true",
		"REPO_PATH_P2=$(dirname \"$REPO_PATH_P1\") || true",
		"REPO_PATH_P3=$(dirname \"$REPO_PATH_P2\") || true",
		"for _d in " + shellQuote(repoPath) + " \"$REPO_PATH_P1\" \"$REPO_PATH_P2\" \"$REPO_PATH_P3\"; do",
		"  if [ -n \"$_d\" ] && [ -d \"$_d\" ]; then",
		"    if command -v chmod >/dev/null 2>&1; then chmod 2775 \"$_d\" 2>/dev/null || true; fi",
		"    if command -v chown >/dev/null 2>&1; then chown \"${TARGET_USER}:${TARGET_GROUP}\" \"$_d\" 2>/dev/null || true; fi",
		"  fi",
		"done",
		"test -w " + shellQuote(repoPath),
	}, "\n")
	scriptLines := []string{
		"set +e",
		"REPO_USER=" + shellQuote(repoUser),
		"REPO_HOST=" + shellQuote(repoHost),
		"REPO_PORT=" + fmt.Sprintf("%d", repoPort),
		"REPO_SSHPASS=" + shellQuote(repoSSHPassword),
		"WHO_AM_I=$(id -un 2>/dev/null || true)",
		"[ -n \"$WHO_AM_I\" ] && [ \"$WHO_AM_I\" != \"root\" ] && HOME_DIR=$(eval echo \"~$WHO_AM_I\") || HOME_DIR=$HOME",
		"if [ -n \"$WHO_AM_I\" ] && [ \"$WHO_AM_I\" != \"root\" ] && [ -d \"$HOME_DIR\" ]; then true; else WHO_AM_I=$USER || true; HOME_DIR=$HOME; fi",
		"SSH_DIR=\"${HOME_DIR}/.ssh\"",
		"mkdir -p \"$SSH_DIR\"",
		"chmod 700 \"$SSH_DIR\"",
		"OWNER=\"$WHO_AM_I\"",
		"if [ \"$OWNER\" = \"\" ] || [ \"$OWNER\" = \"root\" ]; then OWNER=$(stat -c %U \"$HOME_DIR\" 2>/dev/null || echo root); fi",
		"OWNER_GROUP=$(id -gn \"$OWNER\" 2>/dev/null || echo \"$OWNER\")",
		"if command -v chown >/dev/null 2>&1; then chown -R \"${OWNER}:${OWNER_GROUP}\" \"$SSH_DIR\" || true; fi",
		"KNOWN_HOSTS=\"${SSH_DIR}/known_hosts\"",
		"touch \"$KNOWN_HOSTS\"",
		"chmod 644 \"$KNOWN_HOSTS\"",
		"if command -v chown >/dev/null 2>&1; then chown \"${OWNER}:${OWNER_GROUP}\" \"$KNOWN_HOSTS\" || true; fi",
		"if command -v ssh-keyscan >/dev/null 2>&1; then",
		"  ssh-keyscan -p \"$REPO_PORT\" \"$REPO_HOST\" >> \"$KNOWN_HOSTS\" 2>/dev/null || true",
		"fi",
		"command -v ssh >/dev/null 2>&1",
		"REMOTE_SH_CMD=" + shellQuote("/bin/sh -lc "+shellQuote(remoteCmd)),
		"PUBKEY_OUTPUT=$(ssh " + sshBaseOptsPubkey + " -p \"$REPO_PORT\" \"$REPO_USER@$REPO_HOST\" \"$REMOTE_SH_CMD\" 2>&1)",
		"RC_PUBKEY=$?",
		"if [ $RC_PUBKEY -eq 0 ]; then",
		"  echo \"VERIFY_OK:pubkey\"",
		"  exit 0",
		"fi",
		"RC_USE_SSHPASS=99",
		"SSHPASS_BIN=\"\"",
		"if command -v sshpass >/dev/null 2>&1; then SSHPASS_BIN=$(command -v sshpass); else",
		"  if command -v apt-get >/dev/null 2>&1; then DEBIAN_FRONTEND=noninteractive apt-get install -y sshpass >/dev/null 2>&1 && command -v sshpass >/dev/null 2>&1 && SSHPASS_BIN=$(command -v sshpass); fi",
		"  if [ -z \"$SSHPASS_BIN\" ] && command -v yum >/dev/null 2>&1; then yum install -y sshpass >/dev/null 2>&1 && command -v sshpass >/dev/null 2>&1 && SSHPASS_BIN=$(command -v sshpass); fi",
		"fi",
		"SSHPASS_OUTPUT=\"\"",
		"if [ -n \"$SSHPASS_BIN\" ] && [ -n \"$REPO_SSHPASS\" ]; then",
		"  export SSHPASS=\"$REPO_SSHPASS\"",
		"  SSHPASS_OUTPUT=$(\"$SSHPASS_BIN\" -e ssh " + sshBaseOptsSSHPass + " -p \"$REPO_PORT\" \"$REPO_USER@$REPO_HOST\" \"$REMOTE_SH_CMD\" 2>&1)",
		"  RC_USE_SSHPASS=$?",
		"  unset SSHPASS",
		"  if [ $RC_USE_SSHPASS -eq 0 ]; then",
		"    echo \"VERIFY_OK:sshpass\"",
		"    exit 0",
		"  fi",
		"fi",
		"echo \"VERIFY_FAIL pubkey_rc=$RC_PUBKEY sshpass_rc=$RC_USE_SSHPASS repo_user=$REPO_USER repo_host=$REPO_HOST repo_port=$REPO_PORT sshpass_bin=$SSHPASS_BIN\"",
		"echo \"--pubkey-output--\"",
		"echo \"$PUBKEY_OUTPUT\"",
		"if [ -n \"$SSHPASS_OUTPUT\" ]; then echo \"--sshpass-output--\"; echo \"$SSHPASS_OUTPUT\"; fi",
		"exit 1",
	}
	output, err := runner.Run(strings.Join(scriptLines, "\n"))
	if err != nil {
		return fmt.Errorf("执行节点无法连通仓库节点，请检查网络与 SSH 互信: %v\n%s", err, output)
	}
	return nil
}
