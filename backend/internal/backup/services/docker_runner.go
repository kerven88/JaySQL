package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func dockerNetworkMode() string {
	v := strings.TrimSpace(os.Getenv("GOINSIGHT_BACKUP_DOCKER_NETWORK"))
	if v == "" {
		return "host"
	}
	return v
}

func runDocker(ctx context.Context, workDir string, args []string, env map[string]string, logWriter io.Writer) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), mapToEnv(env)...)
	}
	cmd.Stdout = logWriter
	cmd.Stderr = logWriter
	return cmd.Run()
}

func mapToEnv(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, fmt.Sprintf("%s=%s", k, v))
	}
	return out
}

func ensureDir(path string) error {
	cleaned := filepath.Clean(path)
	if cleaned == "" || cleaned == "." {
		cleaned, _ = os.Getwd()
	}
	if err := os.MkdirAll(cleaned, 0o777); err != nil {
		var permHint string
		if isPermissionDeniedErr(err) {
			permHint = buildPermissionDeniedHint(cleaned)
		}
		if permHint != "" {
			return fmt.Errorf("%w\n%s", err, permHint)
		}
		return err
	}
	current := cleaned
	for i := 0; i < 10; i++ {
		_ = os.Chmod(current, 0o777)
		parent := filepath.Dir(current)
		if parent == current || parent == "/" || parent == "" {
			break
		}
		current = parent
	}
	return nil
}

func isPermissionDeniedErr(err error) bool {
	if err == nil {
		return false
	}
	if os.IsPermission(err) {
		return true
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		if pathErr.Err == syscall.EACCES || pathErr.Err == syscall.EPERM {
			return true
		}
	}
	return strings.Contains(strings.ToLower(err.Error()), "permission denied")
}

func buildPermissionDeniedHint(path string) string {
	userName := ""
	if u, uErr := user.Current(); uErr == nil && u != nil {
		userName = u.Username
		if userName == "" && u.Uid != "" {
			userName = fmt.Sprintf("uid=%s", u.Uid)
		}
	}
	if userName == "" {
		userName = "当前进程用户"
	}
	targetDir := findClosestExistingParent(path)
	shellQuote := func(s string) string {
		if strings.ContainsAny(s, " \t\n'\"\\$`!;&|(){}[]<>*?#~^") {
			return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
		}
		return s
	}
	userField := shellQuote(userName)
	lines := []string{
		"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━",
		fmt.Sprintf("【目录写入权限不足】当前备份进程以用户「%s」身份运行，无法在路径下创建目录：%s", userName, path),
		"请在目标主机（执行备份的 DB 主机 / Agent 所在主机）上，以 root 权限执行以下任一方案即可：",
		"",
	}
	if targetDir != "" {
		td := shellQuote(targetDir)
		lines = append(lines,
			fmt.Sprintf("  方案①（推荐：把目录授权给该用户；父目录=%s）：", targetDir),
			fmt.Sprintf("    chown -R %s:%s %s", userField, userField, td),
			fmt.Sprintf("    chmod 750 %s", td),
			"",
			fmt.Sprintf("  方案②（不改变目录归属，用 ACL 精准加权限；父目录=%s）：", targetDir),
			fmt.Sprintf("    setfacl -R -m u:%s:rwx %s", userField, td),
			fmt.Sprintf("    setfacl -d -m u:%s:rwx %s  # 使新建的子目录/文件自动继承该权限", userField, td),
			"",
		)
	}
	lines = append(lines,
		fmt.Sprintf("  方案③（临时验证：直接把父目录赋权 777，不推荐生产使用）："),
		fmt.Sprintf("    chmod 777 %s  && mkdir -p %s", shellQuote(filepath.Dir(path)), shellQuote(path)),
		"",
		"【验证命令】执行完后，用以下命令确认权限是否真正生效（必须以备份运行用户验证，不能用 root 测）：",
		fmt.Sprintf("    su -s /bin/sh %s -c %s", userField, shellQuote(fmt.Sprintf("mkdir -p %s && echo OK_DIR_WRITE", shellQuote(path)))),
		"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━",
	)
	return strings.Join(lines, "\n")
}

func findClosestExistingParent(p string) string {
	cur := filepath.Clean(p)
	for {
		if cur == "" || cur == "." || cur == string(filepath.Separator) {
			if cur == string(filepath.Separator) {
				return cur
			}
			return ""
		}
		info, err := os.Lstat(cur)
		if err == nil && info.IsDir() {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return cur
		}
		cur = parent
	}
}

func shellQuoteString(s string) string {
	if strings.ContainsAny(s, " \t\n'\"\\$`!;&|(){}[]<>*?#~^") {
		return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
	}
	return s
}

func buildRemotePermissionDeniedHint(host, sshUser, basePath string) string {
	if strings.TrimSpace(sshUser) == "" {
		sshUser = "SSH登录用户"
	}
	path := strings.TrimSpace(basePath)
	if path == "" {
		path = "备份根路径"
	}
	userField := shellQuoteString(sshUser)
	td := shellQuoteString(path)
	hostField := shellQuoteString(host)
	lines := []string{
		"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━",
		fmt.Sprintf("【存储目标服务器权限不足】SSH 登录到存储目标服务器 %s 的用户「%s」", host, sshUser),
		fmt.Sprintf("对备份根路径 %s（或其父目录）缺少 w+x 写入权限，无法创建多层子目录（实例ID/任务ID/执行ID 三级嵌套）。", path),
		"",
		fmt.Sprintf("请在【存储目标服务器 %s】上（注意：不是 DB 主机，是您填的 SSH 远程主机 %s）以 root 权限执行以下任一方案：", host, host),
		"",
		fmt.Sprintf("  方案①（推荐：把备份根路径 chown 给 SSH 用户）："),
		fmt.Sprintf("    ssh root@%s \"", hostField),
		fmt.Sprintf("      mkdir -p %s && chown -R %s:%s %s && chmod 750 %s", td, userField, userField, td, td),
		"    \"",
		"",
		fmt.Sprintf("  方案②（保留原目录归属，仅用 ACL 加精准权限）："),
		fmt.Sprintf("    ssh root@%s \"", hostField),
		fmt.Sprintf("      mkdir -p %s && setfacl -R -m u:%s:rwx %s && setfacl -d -m u:%s:rwx %s", td, userField, td, userField, td),
		"    \"",
		"",
		fmt.Sprintf("  方案③（临时验证 777，不推荐生产）："),
		fmt.Sprintf("    ssh root@%s \"mkdir -p %s && chmod 777 %s\"", hostField, td, td),
		"",
		"【验证命令】授权后必须用 SSH 用户登录远程主机验证（不要用 root 测）：",
		fmt.Sprintf("    ssh %s@%s %s", userField, hostField, shellQuoteString(fmt.Sprintf(
			"mkdir -p %s/.verify_write/inst1/task2/run3 && touch %s/.verify_write/inst1/task2/run3/file.txt && rm -rf %s/.verify_write && echo OK_REMOTE_WRITE",
			path, path, path))),
		"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━",
	}
	return strings.Join(lines, "\n")
}

func fileSha256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func nowLocalTime() time.Time {
	return time.Now()
}

func absMediaBackupDir() (string, error) {
	base, err := filepath.Abs("./media/backup")
	if err != nil {
		return "", err
	}
	if err := ensureDir(base); err != nil {
		return "", err
	}
	return base, nil
}

// resolveBackupOutputBaseDir resolves a path for the execution node, not the API server.
// Never inspect or create this path on the JaySQL server: it belongs to the Agent.
func resolveBackupOutputBaseDir(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return filepath.Abs("./media/backup")
	}
	if !filepath.IsAbs(input) {
		return "", fmt.Errorf("执行节点工作目录必须填写绝对路径，例如 /data/backup；此目录属于数据库主机 / Agent，不是 JaySQL 服务器或 S3 对象路径")
	}
	base := filepath.Clean(input)
	if base == "." || base == string(os.PathSeparator) {
		return "", fmt.Errorf("执行节点工作目录不能使用根目录")
	}
	return base, nil
}

func validateNodeBaseDir(input string) (string, error) {
	if !filepath.IsAbs(strings.TrimSpace(input)) {
		return "", fmt.Errorf("节点基准目录必须填写绝对路径")
	}
	base := filepath.Clean(strings.TrimSpace(input))
	if base == "." || base == string(os.PathSeparator) {
		return "", fmt.Errorf("节点基准目录不合法")
	}
	return base, nil
}

func validateNodeOutputDir(input string) (string, error) {
	v := strings.TrimSpace(input)
	if v == "" {
		return "", nil
	}
	if filepath.IsAbs(v) {
		return "", fmt.Errorf("节点目录必须填写相对路径")
	}
	cleaned := filepath.Clean(v)
	if cleaned == "." {
		return "", nil
	}
	if strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("节点目录不能越过基准目录")
	}
	return cleaned, nil
}

func resolveNodeExecutionBaseDir(baseDir string, taskOutputDir string) (string, error) {
	base, err := validateNodeBaseDir(baseDir)
	if err != nil {
		return "", err
	}
	var target string
	trimmed := strings.TrimSpace(taskOutputDir)
	if filepath.IsAbs(trimmed) {
		target = filepath.Clean(trimmed)
	} else {
		rel, err := validateNodeOutputDir(taskOutputDir)
		if err != nil {
			return "", err
		}
		target = base
		if rel != "" {
			target = filepath.Join(base, rel)
		}
	}
	if err := ensureDir(target); err != nil {
		return "", err
	}
	return target, nil
}
