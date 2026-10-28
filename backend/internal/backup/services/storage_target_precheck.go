package services

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	backupForms "github.com/simajiong/jaysql/internal/backup/forms"
	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/ssh"
)

const (
	precheckOK                = "OK"
	precheckErrConnect        = "SSH_CONNECT_FAILED"
	precheckErrAuth           = "SSH_AUTH_FAILED"
	precheckErrPathNotExist   = "PATH_NOT_EXIST"
	precheckErrPathNotWritable = "PATH_NOT_WRITABLE"
	precheckWarnFAT32         = "FS_FAT32_LIMIT"
	precheckPhase2            = "STORAGE_TYPE_PHASE_2"
	precheckS3ConnectFailed   = "S3_CONNECT_FAILED"
	precheckS3AuthFailed      = "S3_AUTH_FAILED"
	precheckS3BucketNotFound  = "S3_BUCKET_NOT_FOUND"
	precheckS3Forbidden       = "S3_PERMISSION_DENIED"
)

type storageTargetPrecheckCapability struct {
	FsType       string `json:"fs_type,omitempty"`
	TotalBytes   int64  `json:"total_bytes,omitempty"`
	FreeBytes    int64  `json:"free_bytes,omitempty"`
	InodesTotal  int64  `json:"inodes_total,omitempty"`
	InodesFree   int64  `json:"inodes_free,omitempty"`
	MountPoint   string `json:"mount_point,omitempty"`
	UsedPercent  int    `json:"used_percent,omitempty"`
	InodePercent int    `json:"inode_percent,omitempty"`
}

type storageTargetS3Capability struct {
	Endpoint         string   `json:"endpoint,omitempty"`
	Bucket           string   `json:"bucket,omitempty"`
	Region           string   `json:"region,omitempty"`
	ForcePathStyle   bool     `json:"force_path_style,omitempty"`
	HeadBucketOK     bool     `json:"head_bucket_ok,omitempty"`
	ListBucketOK     bool     `json:"list_bucket_ok,omitempty"`
	PutObjectOK      bool     `json:"put_object_ok,omitempty"`
	DeleteObjectOK   bool     `json:"delete_object_ok,omitempty"`
	ObjectCount      int      `json:"object_count,omitempty"`
	ObjectSampleKeys []string `json:"object_sample_keys,omitempty"`
	AllowedActions   []string `json:"allowed_actions,omitempty"`
	DeniedActions    []string `json:"denied_actions,omitempty"`
	StorageClass     string   `json:"storage_class,omitempty"`
	ServerOutput     string   `json:"server_output,omitempty"`
}

type storageTargetPrecheckFingerprint struct {
	Algorithm string `json:"algo"`
	Hash      string `json:"hash"`
}

type storageTargetPrecheckResult struct {
	OK              bool                                 `json:"ok"`
	ErrCode         string                               `json:"err_code,omitempty"`
	ErrMessage      string                               `json:"err_message,omitempty"`
	Warnings        []string                             `json:"warnings,omitempty"`
	Capability      *storageTargetPrecheckCapability     `json:"capability,omitempty"`
	S3Capability    *storageTargetS3Capability           `json:"s3_capability,omitempty"`
	Fingerprints    []storageTargetPrecheckFingerprint   `json:"fingerprints,omitempty"`
	Host            string                               `json:"host,omitempty"`
	Port            int                                  `json:"port,omitempty"`
	User            string                               `json:"user,omitempty"`
	BasePath        string                               `json:"base_path,omitempty"`
	Whoami          string                               `json:"whoami,omitempty"`
	ServerOutput    string                               `json:"server_output,omitempty"`
}

type PrecheckStorageTargetService struct {
	*backupForms.PrecheckStorageTargetForm
	C  *gin.Context
	ID uint64
}

func (s *PrecheckStorageTargetService) Run() (*storageTargetPrecheckResult, error) {
	var (
		tp            string
		host          string
		port          int
		basePath      string
		sshUser       string
		sshPassword   string
		sshPrivateKey string
		s3Endpoint    string
		s3Region      string
		s3Bucket      string
		s3AK          string
		s3SK          string
		forcePath     bool
		err           error
	)
	if s.ID > 0 {
		target, loadErr := loadStorageTargetByID(s.ID)
		if loadErr != nil {
			return nil, loadErr
		}
		tp = string(target.Type)
		host = target.Host
		port = target.Port
		basePath = target.BasePath
		sshUser = target.SSHUser
		sshPassword, sshPrivateKey, s3AK, s3SK, err = decryptStorageTargetCredentials(target)
		if err != nil {
			return nil, err
		}
		s3Endpoint = target.S3Endpoint
		s3Region = target.S3Region
		s3Bucket = target.S3Bucket
		forcePath = target.S3ForcePathStyle
		if override := strings.TrimSpace(s.SSHPassword); override != "" {
			sshPassword = override
		}
		if override := strings.TrimSpace(s.SSHPrivateKey); override != "" {
			sshPrivateKey = override
		}
		if override := strings.TrimSpace(s.SSHUser); override != "" {
			sshUser = override
		}
		if override := strings.TrimSpace(s.Host); override != "" {
			host = override
		}
		if s.Port > 0 {
			port = s.Port
		}
		if override := strings.TrimSpace(s.BasePath); override != "" {
			basePath = override
		}
		if override := strings.TrimSpace(s.S3Endpoint); override != "" {
			s3Endpoint = override
		}
		if override := strings.TrimSpace(s.S3Region); override != "" {
			s3Region = override
		}
		if override := strings.TrimSpace(s.S3Bucket); override != "" {
			s3Bucket = override
		}
		if override := strings.TrimSpace(s.S3AccessKey); override != "" {
			s3AK = override
		}
		if override := strings.TrimSpace(s.S3SecretKey); override != "" {
			s3SK = override
		}
	} else {
		tp, err = normalizeStorageTargetType(s.Type)
		if err != nil {
			return nil, err
		}
		host = strings.TrimSpace(s.Host)
		port = s.Port
		basePath = normalizeBasePath(s.BasePath)
		sshUser = strings.TrimSpace(s.SSHUser)
		sshPassword = strings.TrimSpace(s.SSHPassword)
		sshPrivateKey = strings.TrimSpace(s.SSHPrivateKey)
		s3Endpoint = strings.TrimSpace(s.S3Endpoint)
		s3Region = strings.TrimSpace(s.S3Region)
		s3Bucket = strings.TrimSpace(s.S3Bucket)
		s3AK = strings.TrimSpace(s.S3AccessKey)
		s3SK = strings.TrimSpace(s.S3SecretKey)
	}

	if isPhase2StorageType(tp) {
		r := &storageTargetPrecheckResult{
			OK:         false,
			ErrCode:    precheckPhase2,
			ErrMessage: fmt.Sprintf("存储目标类型 %s 为二期规划，暂不支持连通测试。", tp),
		}
		s.savePrecheckResult(s.ID, r)
		return r, nil
	}

	switch tp {
	case backupModels.StorageTargetTypeLocalServer:
		result := s.precheckLocalServer(host, port, basePath, sshUser, sshPassword, sshPrivateKey)
		result.Host = host
		result.Port = port
		result.User = sshUser
		result.BasePath = basePath
		s.savePrecheckResult(s.ID, result)
		return result, nil
	case backupModels.StorageTargetTypeOSSAliyun,
		backupModels.StorageTargetTypeCOSTencent,
		backupModels.StorageTargetTypeS3Minio,
		backupModels.StorageTargetTypeOBSHuawei:
		result := s.precheckS3(tp, s3Endpoint, s3Region, s3Bucket, s3AK, s3SK, forcePath)
		s.savePrecheckResult(s.ID, result)
		return result, nil
	default:
		return nil, fmt.Errorf("不支持的存储目标类型: %s", tp)
	}
}

func (s *PrecheckStorageTargetService) savePrecheckResult(id uint64, r *storageTargetPrecheckResult) {
	if id == 0 || r == nil {
		return
	}
	status := backupModels.StorageTargetConnStatusFailed
	if r.OK {
		status = backupModels.StorageTargetConnStatusOK
	}
	b, _ := json.Marshal(r)
	updates := map[string]any{
		"connection_status":   status,
		"last_precheck_at":    commonModels.LocalTime(nowLocalTime()),
		"last_precheck_result": string(b),
	}
	_ = global.App.DB.Model(&backupModels.InsightBackupStorageTargets{}).
		Where("id = ?", id).
		Updates(updates).Error
}

type localServerSSHCfg struct {
	Host          string
	Port          int
	User          string
	Password      string
	PrivateKey    string
	Timeout       time.Duration
	FingerprintOut *[]storageTargetPrecheckFingerprint
}

func buildSSHClient(cfg *localServerSSHCfg) (*ssh.Client, error) {
	authMethods := []ssh.AuthMethod{}
	hasKey := false
	hasPwd := false
	if strings.TrimSpace(cfg.PrivateKey) != "" {
		signer, err := ssh.ParsePrivateKey([]byte(cfg.PrivateKey))
		if err != nil {
			return nil, fmt.Errorf("解析 SSH 私钥失败: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
		hasKey = true
	}
	pwd := strings.TrimSpace(cfg.Password)
	if pwd != "" {
		authMethods = append(authMethods, ssh.Password(pwd))
		authMethods = append(authMethods, ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
			answers := make([]string, 0, len(questions))
			for range questions {
				answers = append(answers, pwd)
			}
			return answers, nil
		}))
		hasPwd = true
	}
	if len(authMethods) == 0 {
		return nil, fmt.Errorf("SSH 鉴权信息不能为空")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 6 * time.Second
	}
	host := strings.TrimSpace(cfg.Host)
	port := cfg.Port
	if port <= 0 {
		port = 22
	}
	clientCfg := &ssh.ClientConfig{
		User:    strings.TrimSpace(cfg.User),
		Auth:    authMethods,
		Timeout: timeout,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			if cfg.FingerprintOut != nil {
				fp := storageTargetPrecheckFingerprint{
					Algorithm: key.Type(),
					Hash:      ssh.FingerprintSHA256(key),
				}
				*cfg.FingerprintOut = append(*cfg.FingerprintOut, fp)
			}
			return nil
		},
	}
	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", host, port), clientCfg)
	if err != nil {
		msg := err.Error()
		code := precheckErrConnect
		if strings.Contains(msg, "ssh: unable to authenticate") ||
			strings.Contains(msg, "no supported methods remain") ||
			strings.Contains(msg, "Permission denied") {
			code = precheckErrAuth
		}
		return nil, &wrappedPrecheckError{
			Code:    code,
			Message: fmt.Sprintf("SSH 登录 %s@%s:%d 失败: %v（已尝试认证：私钥=%v, 密码/交互=%v）", clientCfg.User, host, port, err, hasKey, hasPwd),
		}
	}
	return client, nil
}

type wrappedPrecheckError struct {
	Code    string
	Message string
}

func (e *wrappedPrecheckError) Error() string { return e.Message }

func (s *PrecheckStorageTargetService) precheckLocalServer(host string, port int, basePath, sshUser, sshPassword, sshPrivateKey string) *storageTargetPrecheckResult {
	fingerprints := []storageTargetPrecheckFingerprint{}
	cfg := &localServerSSHCfg{
		Host:          host,
		Port:          port,
		User:          sshUser,
		Password:      sshPassword,
		PrivateKey:    sshPrivateKey,
		Timeout:       6 * time.Second,
		FingerprintOut: &fingerprints,
	}
	client, err := buildSSHClient(cfg)
	if err != nil {
		code := precheckErrConnect
		var we *wrappedPrecheckError
		if asW, ok := err.(*wrappedPrecheckError); ok {
			we = asW
			code = we.Code
		}
		return &storageTargetPrecheckResult{
			OK:           false,
			ErrCode:      code,
			ErrMessage:   err.Error(),
			Fingerprints: fingerprints,
		}
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return &storageTargetPrecheckResult{
			OK:           false,
			ErrCode:      precheckErrConnect,
			ErrMessage:   fmt.Sprintf("创建 SSH session 失败: %v", err),
			Fingerprints: fingerprints,
		}
	}
	defer session.Close()

	whoamiScript := `whoami`
	pathScript := fmt.Sprintf(`
set -e
WHOAMI_OUT=$(whoami 2>/dev/null || echo '')
mkdir -p %s 2>&1
if [ ! -d %s ]; then
  echo 'PATH_NOT_EXIST'
  exit 2
fi
test -w %s
TMP_BASE=%s/.goinsight_precheck_$$
TMP_DEEP="$TMP_BASE"/inst123/task_abc/run_456
mkdir -p "$TMP_DEEP" 2>&1
TMP_FILE="$TMP_DEEP"/write_test.txt
printf 'goinsight storage target precheck deep write probe\n' > "$TMP_FILE"
if [ ! -s "$TMP_FILE" ]; then
  echo 'PATH_DEEP_WRITE_FAILED'
  exit 4
fi
rm -rf "$TMP_BASE"
echo 'PATH_WRITABLE'
echo "WHOAMI=$WHOAMI_OUT"
`, shellQuote(basePath), shellQuote(basePath), shellQuote(basePath), shellQuote(basePath))
	statScript := fmt.Sprintf(`
set -e
df -Pk %s 2>/dev/null || /bin/df -Pk %s
echo '---DF_INODE---'
df -Pki %s 2>/dev/null || /bin/df -Pki %s
echo '---STAT_F---'
(stat -f -c '%%T,%%S,%%b,%%f,%%c,%%d' %s 2>/dev/null || stat -f '%%HT,%%S,%%b,%%f,%%c,%%d' %s 2>/dev/null || echo 'STAT_UNSUPPORTED') 2>&1
`, shellQuote(basePath), shellQuote(basePath), shellQuote(basePath), shellQuote(basePath), shellQuote(basePath), shellQuote(basePath))

	runOutput := func(script string) (string, error) {
		sess, e := client.NewSession()
		if e != nil {
			return "", e
		}
		defer sess.Close()
		var stdout, stderr bytes.Buffer
		sess.Stdout = &stdout
		sess.Stderr = &stderr
		e = sess.Run("sh -lc " + shellQuote(script))
		output := strings.TrimSpace(stdout.String())
		if stderrText := strings.TrimSpace(stderr.String()); stderrText != "" {
			if output == "" {
				output = stderrText
			} else {
				output = output + "\n" + stderrText
			}
		}
		return output, e
	}

	whoami, _ := runOutput(whoamiScript)

	pathOut, pathErr := runOutput(pathScript)
	if pathErr != nil {
		msg := strings.TrimSpace(pathOut)
		code := precheckErrPathNotWritable
		if strings.Contains(msg, "PATH_NOT_EXIST") || strings.Contains(pathErr.Error(), "No such file") {
			code = precheckErrPathNotExist
		}
		effectiveUser := whoami
		if strings.Contains(msg, "WHOAMI=") {
			for _, raw := range strings.Split(msg, "\n") {
				line := strings.TrimSpace(raw)
				if strings.HasPrefix(line, "WHOAMI=") {
					if v := strings.TrimSpace(strings.TrimPrefix(line, "WHOAMI=")); v != "" {
						effectiveUser = v
					}
					break
				}
			}
		}
		errMessage := fmt.Sprintf("路径检查失败: %v\n%s", pathErr, msg)
		if strings.Contains(strings.ToLower(msg+pathErr.Error()), "permission denied") ||
			strings.Contains(msg, "PATH_DEEP_WRITE_FAILED") ||
			code == precheckErrPathNotWritable {
			errMessage = errMessage + "\n" + buildRemotePermissionDeniedHint(host, effectiveUser, basePath)
		}
		return &storageTargetPrecheckResult{
			OK:           false,
			ErrCode:      code,
			ErrMessage:   errMessage,
			Whoami:       whoami,
			Fingerprints: fingerprints,
		}
	}

	warnings := []string{}
	capability := &storageTargetPrecheckCapability{}
	statOut, _ := runOutput(statScript)
	parseDFStatOutput(statOut, capability, &warnings)

	// 组合最终结果
	result := &storageTargetPrecheckResult{
		OK:           true,
		Warnings:     warnings,
		Capability:   capability,
		Fingerprints: fingerprints,
		Whoami:       whoami,
		ServerOutput: strings.TrimSpace(fmt.Sprintf("%s\n---\n%s", pathOut, statOut)),
	}
	return result
}

func parseDFStatOutput(output string, cap *storageTargetPrecheckCapability, warnings *[]string) {
	if output == "" || cap == nil {
		return
	}
	lines := strings.Split(output, "\n")
	section := ""
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "---DF_INODE---") {
			section = "inode"
			continue
		}
		if strings.HasPrefix(line, "---STAT_F---") {
			section = "stat"
			continue
		}
		switch section {
		case "":
			if strings.HasPrefix(line, "Filesystem") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) >= 6 {
				// Filesystem 1024-blocks Used Available Capacity Mounted
				// 0            1           2    3         4         5
				if total, ok := parseIntKToBytes(fields[1]); ok {
					cap.TotalBytes = total
				}
				if free, ok := parseIntKToBytes(fields[3]); ok {
					cap.FreeBytes = free
				}
				if pct := parsePercent(fields[4]); pct >= 0 {
					cap.UsedPercent = pct
				}
				cap.MountPoint = fields[len(fields)-1]
			}
			// 尝试取倒数第二个字段（IUsed IFree IUse% Mounted on 有一定差异），先忽略 fs_type
		case "inode":
			if strings.HasPrefix(line, "Filesystem") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) >= 6 {
				// Inodes IUsed IFree IUse% Mounted on
				if inodes, ok := parseIntAny(fields[0]); ok {
					cap.InodesTotal = inodes
				}
				if ifree, ok := parseIntAny(fields[2]); ok {
					cap.InodesFree = ifree
				}
				if pct := parsePercent(fields[3]); pct >= 0 {
					cap.InodePercent = pct
				}
			}
		case "stat":
			// FS_T,FSIZE,BLKS,FREE,FILES,FFREE
			parts := strings.Split(line, ",")
			if len(parts) >= 6 {
				cap.FsType = strings.TrimSpace(parts[0])
				// 简单判定 FS 是否 FAT32/VFAT
				low := strings.ToLower(cap.FsType)
				if low == "vfat" || low == "fat32" || low == "msdos" || strings.Contains(low, "fat") {
					if warnings != nil {
						*warnings = append(*warnings, "检测到文件系统为 "+cap.FsType+"（类 FAT32），单文件上限约 4GB，备份大文件可能失败，建议使用 xfs/ext4/zfs。")
					}
				}
			}
		}
	}
}

func parseIntKToBytes(s string) (int64, bool) {
	// 1024-blocks
	n, ok := parseIntAny(s)
	if !ok {
		return 0, false
	}
	return n * 1024, true
}

func parseIntAny(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0, false
	}
	var n int64
	_, err := fmt.Sscanf(s, "%d", &n)
	if err != nil {
		return 0, false
	}
	return n, true
}

func parsePercent(s string) int {
	s = strings.TrimSpace(strings.TrimSuffix(s, "%"))
	if s == "" {
		return -1
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return -1
	}
	return n
}

type ConfirmStorageTargetFingerprintService struct {
	*backupForms.ConfirmFingerprintForm
	C  *gin.Context
	ID uint64
}

func (s *ConfirmStorageTargetFingerprintService) Run() error {
	if s.FingerprintConfirmed == nil || !*s.FingerprintConfirmed {
		return fmt.Errorf("必须确认指纹后才能持久化")
	}
	if strings.TrimSpace(s.Fingerprint) == "" {
		return fmt.Errorf("指纹不能为空")
	}
	target, err := loadStorageTargetByID(s.ID)
	if err != nil {
		return err
	}
	updates := map[string]any{
		"ssh_host_key_fingerprint": strings.TrimSpace(s.Fingerprint),
		"fingerprint_confirmed":    true,
		"updated_by":               currentUsername(s.C),
		"updated_at":               commonModels.LocalTime(nowLocalTime()),
	}
	return global.App.DB.Model(target).Updates(updates).Error
}

// S3 v4 签名与对象操作统一由 s3_client.go 的 S3Client 提供（单一实现），
// 预检与真实上传共用同一套签名逻辑，避免出现"连通测试绿灯、真实上传失败"。

type s3Action struct {
	Name    string
	Allowed bool
	Detail  string
}

func (s *PrecheckStorageTargetService) precheckS3(tp, endpoint, region, bucket, ak, sk string, forcePath bool) *storageTargetPrecheckResult {
	res := &storageTargetPrecheckResult{
		S3Capability: &storageTargetS3Capability{
			Endpoint:       endpoint,
			Bucket:         bucket,
			Region:         region,
			ForcePathStyle: forcePath,
		},
		Warnings: []string{},
	}
	client, clientErr := NewS3Client(S3ClientOptions{
		Endpoint:       endpoint,
		Region:         region,
		Bucket:         bucket,
		AccessKey:      ak,
		SecretKey:      sk,
		ForcePathStyle: forcePath,
		HTTPTimeout:    10 * time.Second,
		MaxRetries:     0,
	})
	if clientErr != nil {
		res.ErrCode = precheckS3ConnectFailed
		res.ErrMessage = clientErr.Error()
		return res
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	actions := make([]s3Action, 0)

	probeOK := true
	logs := []string{}
	sigErrOnWrite := false
	sigErrOnWriteDetail := ""
	testKey := ".goinsight-precheck/" + fmt.Sprintf("%d.txt", time.Now().UnixNano())
	testKeyBody := []byte("goinsight storage target precheck write probe\n")

	// 复用 S3Client 的底层请求：预检与真实上传走同一套签名实现。
	doAction := func(name, method, objKey string, qs url.Values, body []byte) (int, []byte, string) {
		status, b, doErr := client.do(ctx, method, objKey, qs, body, nil)
		if doErr != nil {
			return 0, nil, doErr.Error()
		}
		return status, b, ""
	}

	{
		status, b, errStr := doAction("HeadBucket", http.MethodHead, "", url.Values{}, nil)
		act := s3Action{Name: "s3:HeadBucket"}
		switch status {
		case 200:
			act.Allowed = true
			res.S3Capability.HeadBucketOK = true
			probeOK = true
		case 301, 302, 307, 400:
			act.Allowed = false
			act.Detail = fmt.Sprintf("状态码 %d，可能 Region/Endpoint 不匹配：%s", status, errStr)
			probeOK = false
		case 403:
			act.Allowed = false
			act.Detail = fmt.Sprintf("状态码 403，AK/SK 或 Bucket 策略拒绝访问 HeadBucket。响应: %s", strings.TrimSpace(string(b[:minInt(len(b), 200)])))
			probeOK = false
		case 404:
			act.Allowed = false
			act.Detail = fmt.Sprintf("Bucket %s 不存在或 Endpoint 错误", bucket)
			probeOK = false
		default:
			act.Allowed = false
			act.Detail = fmt.Sprintf("状态码 %d: %s %s", status, errStr, strings.TrimSpace(string(b[:minInt(len(b), 200)])))
		}
		actions = append(actions, act)
		if status != 200 {
			logs = append(logs, fmt.Sprintf("[HEAD /%s] => %d (%s)", bucket, status, act.Detail))
		}
		if status == 403 {
			if strings.Contains(string(b), "SignatureDoesNotMatch") || strings.Contains(string(b), "InvalidAccessKeyId") {
				res.ErrCode = precheckS3AuthFailed
				res.ErrMessage = fmt.Sprintf("AK/SK 认证失败，请核对 AccessKeyId / AccessKeySecret 是否正确。详情: %s", act.Detail)
				goto finalize
			}
		}
		if status == 404 {
			res.ErrCode = precheckS3BucketNotFound
			res.ErrMessage = act.Detail
			goto finalize
		}
		if status != 200 && !probeOK {
			res.ErrCode = precheckS3ConnectFailed
			res.ErrMessage = act.Detail
			goto finalize
		}
	}

	{
		qs := url.Values{}
		qs.Set("list-type", "2")
		qs.Set("max-keys", "10")
		status, b, errStr := doAction("ListBucket", http.MethodGet, "", qs, nil)
		act := s3Action{Name: "s3:ListBucket"}
		if status == 200 {
			act.Allowed = true
			res.S3Capability.ListBucketOK = true
			parsed := s3ListBucketResult{}
			if xmlErr := xml.Unmarshal(b, &parsed); xmlErr == nil {
				res.S3Capability.ObjectCount = len(parsed.Contents)
				samples := []string{}
				for _, c := range parsed.Contents {
					samples = append(samples, c.Key)
					if c.StorageClass != "" {
						res.S3Capability.StorageClass = c.StorageClass
					}
				}
				res.S3Capability.ObjectSampleKeys = samples
			}
		} else {
			act.Allowed = false
			act.Detail = fmt.Sprintf("状态码 %d: %s %s", status, errStr, strings.TrimSpace(string(b[:minInt(len(b), 200)])))
			logs = append(logs, fmt.Sprintf("[GET /%s?list-type=2&max-keys=10] => %d (%s)", bucket, status, act.Detail))
		}
		actions = append(actions, act)
	}

	{
		status, b, errStr := doAction("PutObject", http.MethodPut, testKey, url.Values{}, testKeyBody)
		act := s3Action{Name: "s3:PutObject"}
		if status == 200 || status == 201 {
			act.Allowed = true
			res.S3Capability.PutObjectOK = true
		} else {
			act.Allowed = false
			act.Detail = fmt.Sprintf("状态码 %d: %s %s", status, errStr, strings.TrimSpace(string(b[:minInt(len(b), 200)])))
			logs = append(logs, fmt.Sprintf("[PUT /%s/%s] => %d (%s)", bucket, testKey, status, act.Detail))
			bodyStr := strings.TrimSpace(string(b[:minInt(len(b), 400)]))
			if strings.Contains(bodyStr, "SignatureDoesNotMatch") ||
				strings.Contains(bodyStr, "InvalidAccessKeyId") ||
				strings.Contains(bodyStr, "IncompleteSignature") ||
				strings.Contains(bodyStr, "RequestTimeTooSkewed") {
				sigErrOnWrite = true
				sigErrOnWriteDetail = bodyStr
			}
		}
		actions = append(actions, act)
	}

	if res.S3Capability.PutObjectOK {
		status, b, errStr := doAction("DeleteObject", http.MethodDelete, testKey, url.Values{}, nil)
		act := s3Action{Name: "s3:DeleteObject"}
		if status == 200 || status == 204 {
			act.Allowed = true
			res.S3Capability.DeleteObjectOK = true
		} else {
			act.Allowed = false
			act.Detail = fmt.Sprintf("状态码 %d: %s %s", status, errStr, strings.TrimSpace(string(b[:minInt(len(b), 200)])))
			logs = append(logs, fmt.Sprintf("[DELETE /%s/%s] => %d (%s)", bucket, testKey, status, act.Detail))
		}
		actions = append(actions, act)
	}

	for _, a := range actions {
		if a.Allowed {
			res.S3Capability.AllowedActions = append(res.S3Capability.AllowedActions, a.Name)
		} else {
			res.S3Capability.DeniedActions = append(res.S3Capability.DeniedActions, a.Name+" ("+a.Detail+")")
		}
	}

	if !res.S3Capability.ListBucketOK {
		res.Warnings = append(res.Warnings, "AK 未授予 s3:ListBucket，备份后无法列出已存在对象用于校验")
	}
	if !res.S3Capability.PutObjectOK {
		res.Warnings = append(res.Warnings, "AK 未授予 s3:PutObject，无法写入备份文件")
	}

finalize:
	headOK := res.S3Capability.HeadBucketOK
	writeOK := res.S3Capability.PutObjectOK
	if res.ErrCode == "" {
		if headOK {
			res.OK = true
			res.S3Capability.ServerOutput = strings.Join(logs, "\n")
			if !writeOK {
				res.OK = false
				if sigErrOnWrite {
					// HeadBucket/ListBucket 都成功、只有写对象被签名拒绝 —— 这是签名/凭据/Endpoint 配置问题，
					// 不是 RAM / Bucket Policy / Bucket ACL 的权限问题。若沿用旧文案会把人引到错误方向。
					res.ErrCode = precheckS3AuthFailed
					res.ErrMessage = fmt.Sprintf("Bucket 可读（HeadBucket/ListBucket 均成功）但对象写入被签名校验拒绝，属签名/凭据/Endpoint 配置不一致，而非 RAM / Bucket Policy / Bucket ACL 权限不足。请核对 AccessKeySecret、Endpoint、Region 以及 path-style 设置。原始响应: %s", sigErrOnWriteDetail)
				} else {
					res.ErrCode = precheckS3Forbidden
					res.ErrMessage = fmt.Sprintf("Bucket 可达但 s3:PutObject 被拒绝（Denied=%v），备份无法写入存储。请检查 RAM / Bucket Policy / Bucket ACL 是否授予 %s:* 或至少 PutObject+ListBucket。", res.S3Capability.DeniedActions, strings.ToUpper(tp))
				}
			}
		} else {
			res.OK = false
			res.ErrCode = precheckS3ConnectFailed
			res.ErrMessage = "无法连通 Endpoint 或 HeadBucket 失败，请检查 Endpoint / Region / Bucket / AK / SK 是否正确。"
		}
	}
	res.ServerOutput = strings.Join(logs, "\n")
	res.S3Capability.ServerOutput = strings.Join(logs, "\n")
	_ = tp
	return res
}

