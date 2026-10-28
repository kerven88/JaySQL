package services

import (
	"errors"
	"fmt"
	"net"
	urlpkg "net/url"
	"strings"
	"time"

	backupForms "github.com/simajiong/jaysql/internal/backup/forms"
	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/notifier"
	"github.com/simajiong/jaysql/pkg/pagination"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const nodeOfflineTTL = 30 * time.Second

func normalizeBackupNodeHost(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = strings.Trim(s, "[]()（）【】{}<>《》\"'` \t\r\n")
	if idx := strings.LastIndex(s, ":"); idx > 0 {
		suffix := s[idx+1:]
		if suffix != "" {
			allDigits := true
			for i := 0; i < len(suffix); i++ {
				if suffix[i] < '0' || suffix[i] > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				s = s[:idx]
			}
		}
	}
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "[]()（）【】{}<>《》\"'` \t\r\n")
	return strings.ToLower(s)
}

type GetBackupNodesService struct {
	*backupForms.GetBackupNodesForm
	C *gin.Context
}

func (s *GetBackupNodesService) Run() (data any, total int64, err error) {
	refreshBackupNodeStatus()
	type row struct {
		ID                 uint64                 `json:"id"`
		Name               string                 `json:"name"`
		Host               string                 `json:"host"`
		Port               int                    `json:"port"`
		BaseDir            string                 `json:"base_dir"`
		SSHUser            string                 `json:"ssh_user"`
		Status             string                 `json:"status"`
		AgentVersion       string                 `json:"agent_version"`
		LastHeartbeat      commonModels.LocalTime `json:"last_heartbeat"`
		Remark             string                 `json:"remark"`
		CreatedAt          commonModels.LocalTime `json:"created_at"`
		TokenMasked        string                 `json:"token_masked"`
		ServerURL          string                 `json:"server_url"`
		InstallDir         string                 `json:"install_dir"`
		ToolsDir           string                 `json:"tools_dir"`
		ServiceName        string                 `json:"service_name"`
		LogPath            string                 `json:"log_path"`
		StatusLabel        string                 `json:"status_label"`
		IsOnline           bool                   `json:"is_online"`
		OfflineReason      string                 `json:"offline_reason"`
		StatusHint         string                 `json:"status_hint"`
		PasswordConfigured bool                   `json:"ssh_password_configured"`
		PrivateKeyConfigured bool                 `json:"ssh_private_key_configured"`
	}
	var list []row
	tx := global.App.DB.Table("insight_backup_nodes").Select(`
		id, name, host, port, base_dir, ssh_user, status, agent_version, last_heartbeat, remark, created_at, server_url, install_dir, tools_dir, service_name, log_path,
		case when ssh_password <> '' then true else false end as ssh_password_configured,
		case when ssh_private_key <> '' then true else false end as ssh_private_key_configured,
		case
			when char_length(token) > 8 then concat(left(token, 4), '****', right(token, 4))
			else token
		end as token_masked`).Order("id desc")
	if s.Search != "" {
		like := "%" + s.Search + "%"
		tx = tx.Where("name like ? or host like ? or base_dir like ? or remark like ?", like, like, like, like)
	}
	if s.Status != "" {
		tx = tx.Where("status = ?", s.Status)
	}
	total = pagination.Pager(&s.PaginationQ, tx, &list)
	for i := range list {
		statusLabel, isOnline, offlineReason, statusHint := describeBackupNodeHealth(list[i].Status, list[i].LastHeartbeat)
		list[i].StatusLabel = statusLabel
		list[i].IsOnline = isOnline
		list[i].OfflineReason = offlineReason
		list[i].StatusHint = statusHint
		list[i].SSHUser = defaultBackupNodeSSHUser(list[i].SSHUser)
		list[i].ServerURL = defaultBackupNodeServerURL(list[i].ServerURL, s.C)
		list[i].InstallDir = defaultBackupNodeInstallDir(list[i].InstallDir)
		list[i].ToolsDir = defaultBackupNodeToolsDir(list[i].ToolsDir)
		list[i].ServiceName = defaultBackupNodeServiceName(list[i].ServiceName, list[i].ID)
		list[i].LogPath = defaultBackupNodeLogPath(list[i].LogPath, list[i].InstallDir)
	}
	return &list, total, nil
}

type GetBackupNodeDetailService struct {
	ID uint64
	C  *gin.Context
}

func (s *GetBackupNodeDetailService) Run() (any, error) {
	refreshBackupNodeStatus()
	type row struct {
		ID                     uint64                 `json:"id"`
		Name                   string                 `json:"name"`
		Host                   string                 `json:"host"`
		Port                   int                    `json:"port"`
		BaseDir                string                 `json:"base_dir"`
		SSHUser                string                 `json:"ssh_user"`
		Status                 string                 `json:"status"`
		AgentVersion           string                 `json:"agent_version"`
		LastHeartbeat          commonModels.LocalTime `json:"last_heartbeat"`
		Remark                 string                 `json:"remark"`
		CreatedAt              commonModels.LocalTime `json:"created_at"`
		TokenMasked            string                 `json:"token_masked"`
		ServerURL              string                 `json:"server_url"`
		InstallDir             string                 `json:"install_dir"`
		ToolsDir               string                 `json:"tools_dir"`
		ServiceName            string                 `json:"service_name"`
		LogPath                string                 `json:"log_path"`
		SSHPasswordConfigured  bool                   `json:"ssh_password_configured"`
		StatusLabel            string                 `json:"status_label"`
		IsOnline               bool                   `json:"is_online"`
		OfflineReason          string                 `json:"offline_reason"`
		StatusHint             string                 `json:"status_hint"`
		LastHeartbeatAgoSec    int64                  `json:"last_heartbeat_ago_seconds"`
		HeartbeatTimeoutSec    int64                  `json:"heartbeat_timeout_seconds"`
		RecommendedActionTitle string                 `json:"recommended_action_title"`
		PrivateKeyConfigured   bool                   `json:"ssh_private_key_configured"`
	}
	var r row
	err := global.App.DB.Table("insight_backup_nodes").Select(`
		id, name, host, port, base_dir, ssh_user, status, agent_version, last_heartbeat, remark, created_at, server_url, install_dir, tools_dir, service_name, log_path,
		case when ssh_password <> '' then true else false end as ssh_password_configured,
		case when ssh_private_key <> '' then true else false end as ssh_private_key_configured,
		case
			when char_length(token) > 8 then concat(left(token, 4), '****', right(token, 4))
			else token
		end as token_masked`).
		Where("id = ?", s.ID).
		Scan(&r).Error
	if err != nil {
		return nil, err
	}
	if r.ID == 0 {
		return nil, fmt.Errorf("备份节点不存在")
	}
	r.StatusLabel, r.IsOnline, r.OfflineReason, r.StatusHint = describeBackupNodeHealth(r.Status, r.LastHeartbeat)
	r.SSHUser = defaultBackupNodeSSHUser(r.SSHUser)
	r.ServerURL = defaultBackupNodeServerURL(r.ServerURL, s.C)
	r.InstallDir = defaultBackupNodeInstallDir(r.InstallDir)
	r.ToolsDir = defaultBackupNodeToolsDir(r.ToolsDir)
	r.ServiceName = defaultBackupNodeServiceName(r.ServiceName, r.ID)
	r.LogPath = defaultBackupNodeLogPath(r.LogPath, r.InstallDir)
	r.LastHeartbeatAgoSec = backupNodeHeartbeatAgeSeconds(r.LastHeartbeat)
	r.HeartbeatTimeoutSec = int64(nodeOfflineTTL / time.Second)
	if r.IsOnline {
		r.RecommendedActionTitle = "Agent 正常运行"
	} else if strings.TrimSpace(r.OfflineReason) == "" {
		r.RecommendedActionTitle = "需要重新安装或启动 Agent"
	} else {
		r.RecommendedActionTitle = "建议先执行修复命令，再重新检测"
	}
	return r, nil
}

type GetBackupNodeTokenService struct {
	ID uint64
	C  *gin.Context
}

func (s *GetBackupNodeTokenService) Run() (any, error) {
	node, err := loadBackupNodeByID(s.ID)
	if err != nil {
		return nil, err
	}
	return gin.H{
		"id":    node.ID,
		"token": node.Token,
	}, nil
}

type CreateBackupNodeService struct {
	*backupForms.CreateBackupNodeForm
	C *gin.Context
}

func (s *CreateBackupNodeService) Run() (*backupModels.InsightBackupNodes, error) {
	baseDir, err := validateNodeBaseDir(s.BaseDir)
	if err != nil {
		return nil, err
	}
	// SSH 凭据加密入库（幂等：已是密文则原样保留）
	encryptedPassword, err := encryptCredential(s.SSHPassword)
	if err != nil {
		return nil, fmt.Errorf("备份节点 SSH 密码加密失败: %w", err)
	}
	encryptedKey, err := encryptCredential(s.SSHPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("备份节点 SSH 私钥加密失败: %w", err)
	}
	node := backupModels.InsightBackupNodes{
		Model:         &commonModels.Model{},
		Name:          strings.TrimSpace(s.Name),
		Host:          strings.TrimSpace(s.Host),
		Port:          s.Port,
		BaseDir:       baseDir,
		SSHUser:       strings.TrimSpace(s.SSHUser),
		SSHPassword:   encryptedPassword,
		SSHPrivateKey: encryptedKey,
		ServerURL:     strings.TrimSpace(s.ServerURL),
		InstallDir:    strings.TrimSpace(s.InstallDir),
		ToolsDir:      strings.TrimSpace(s.ToolsDir),
		ServiceName:   strings.TrimSpace(s.ServiceName),
		LogPath:       strings.TrimSpace(s.LogPath),
		Token:         strings.ReplaceAll(uuid.NewString(), "-", ""),
		Status:        commonModels.EnumType("OFFLINE"),
		Remark:        strings.TrimSpace(s.Remark),
		CreatedBy:     currentUsername(s.C),
		UpdatedBy:     currentUsername(s.C),
		LastHeartbeat: commonModels.LocalTime{},
	}
	if err := global.App.DB.Create(&node).Error; err != nil {
		return nil, err
	}
	return &node, nil
}

type UpdateBackupNodeService struct {
	*backupForms.UpdateBackupNodeForm
	C  *gin.Context
	ID uint64
}

func (s *UpdateBackupNodeService) Run() error {
	baseDir, err := validateNodeBaseDir(s.BaseDir)
	if err != nil {
		return err
	}
	updates := map[string]any{
		"name":         strings.TrimSpace(s.Name),
		"host":         strings.TrimSpace(s.Host),
		"port":         s.Port,
		"base_dir":     baseDir,
		"ssh_user":     strings.TrimSpace(s.SSHUser),
		"server_url":   strings.TrimSpace(s.ServerURL),
		"install_dir":  strings.TrimSpace(s.InstallDir),
		"tools_dir":    strings.TrimSpace(s.ToolsDir),
		"service_name": strings.TrimSpace(s.ServiceName),
		"log_path":     strings.TrimSpace(s.LogPath),
		"remark":       strings.TrimSpace(s.Remark),
		"updated_by":   currentUsername(s.C),
	}
	// SSH 凭据加密入库（幂等：已是密文则原样写回，避免双重加密）
	if strings.TrimSpace(s.SSHPassword) != "" {
		encrypted, encErr := encryptCredential(s.SSHPassword)
		if encErr != nil {
			return fmt.Errorf("备份节点 SSH 密码加密失败: %w", encErr)
		}
		updates["ssh_password"] = encrypted
	}
	if strings.TrimSpace(s.SSHPrivateKey) != "" {
		encrypted, encErr := encryptCredential(s.SSHPrivateKey)
		if encErr != nil {
			return fmt.Errorf("备份节点 SSH 私钥加密失败: %w", encErr)
		}
		updates["ssh_private_key"] = encrypted
	}
	return global.App.DB.Model(&backupModels.InsightBackupNodes{}).Where("id = ?", s.ID).Updates(updates).Error
}

func loadBackupNodeByID(id uint64) (*backupModels.InsightBackupNodes, error) {
	if id == 0 {
		return nil, fmt.Errorf("备份节点不存在")
	}
	var node backupModels.InsightBackupNodes
	silentDB := global.App.DB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	if err := silentDB.Where("id = ?", id).First(&node).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("备份节点不存在")
		}
		return nil, err
	}
	return &node, nil
}

func loadBackupNodeByHost(host string) (*backupModels.InsightBackupNodes, error) {
	norm := normalizeBackupNodeHost(host)
	if norm == "" {
		return nil, fmt.Errorf("备份节点地址不能为空")
	}
	var nodes []backupModels.InsightBackupNodes
	if err := global.App.DB.
		Where("1 = 1").
		Order("CASE WHEN upper(status) = 'ONLINE' THEN 0 ELSE 1 END, id DESC").
		Find(&nodes).Error; err != nil {
		return nil, err
	}
	var exact []backupModels.InsightBackupNodes
	var fuzzy []backupModels.InsightBackupNodes
	for _, n := range nodes {
		cur := normalizeBackupNodeHost(n.Host)
		if cur == "" {
			continue
		}
		if cur == norm {
			exact = append(exact, n)
			continue
		}
		if strings.Contains(cur, norm) || strings.Contains(norm, cur) {
			fuzzy = append(fuzzy, n)
		}
	}
	if len(exact) > 0 {
		return &exact[0], nil
	}
	if len(fuzzy) > 0 {
		return &fuzzy[0], nil
	}
	return nil, fmt.Errorf("未找到与实例主机匹配的备份节点: %s (normalized=%s)", strings.TrimSpace(host), norm)
}

func authenticateBackupNode(nodeID uint64, token string) (*backupModels.InsightBackupNodes, error) {
	node, err := loadBackupNodeByID(nodeID)
	if err != nil {
		return nil, err
	}
	if node.Token == "" || strings.TrimSpace(token) == "" || token != node.Token {
		return nil, fmt.Errorf("节点认证失败")
	}
	return node, nil
}

func markNodeHeartbeat(node *backupModels.InsightBackupNodes, agentVersion string) error {
	updates := map[string]any{
		"status":         "ONLINE",
		"last_heartbeat": commonModels.LocalTime(nowLocalTime()),
	}
	if strings.TrimSpace(agentVersion) != "" {
		updates["agent_version"] = strings.TrimSpace(agentVersion)
	}
	return global.App.DB.Model(&backupModels.InsightBackupNodes{}).Where("id = ?", node.ID).Updates(updates).Error
}

func refreshBackupNodeStatus() {
	threshold := commonModels.LocalTime(nowLocalTime().Add(-nodeOfflineTTL))
	global.App.DB.Model(&backupModels.InsightBackupNodes{}).
		Where("status = ? and last_heartbeat < ?", "ONLINE", threshold).
		Update("status", "OFFLINE")
}

func describeBackupNodeHealth(status string, lastHeartbeat commonModels.LocalTime) (statusLabel string, isOnline bool, offlineReason string, statusHint string) {
	if strings.EqualFold(strings.TrimSpace(status), "ONLINE") {
		return "在线", true, "", "Agent 心跳正常，当前节点可领取并执行备份任务。"
	}
	ageSeconds := backupNodeHeartbeatAgeSeconds(lastHeartbeat)
	if ageSeconds <= 0 {
		return "离线", false, "平台从未收到该节点的 Agent 心跳。通常是 Agent 尚未安装、未启动，或 server/token 配置错误。", "请先填写备份机信息，生成安装命令或 systemd 配置，在节点机器执行后再回到页面重新检测。"
	}
	return "离线", false,
		fmt.Sprintf("最近一次心跳距今约 %d 秒，已超过 %d 秒离线阈值。通常是 Agent 进程退出、机器重启后未自启动，或节点无法访问平台地址。", ageSeconds, int64(nodeOfflineTTL/time.Second)),
		"建议优先使用 systemd 托管 Agent，执行页面生成的修复或安装命令后，再点击“重新检测节点状态”。"
}

func backupNodeHeartbeatAgeSeconds(lastHeartbeat commonModels.LocalTime) int64 {
	last := time.Time(lastHeartbeat)
	if last.IsZero() {
		return 0
	}
	age := nowLocalTime().Sub(last)
	if age < 0 {
		return 0
	}
	return int64(age / time.Second)
}

func defaultBackupNodeSSHUser(value string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return "root"
}

func defaultBackupNodeInstallDir(value string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return "/usr/local/goinsight/backup-agent"
}

func defaultBackupNodeToolsDir(value string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return "/usr/local/goinsight/tools"
}

func defaultBackupNodeServiceName(value string, nodeID uint64) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fmt.Sprintf("goinsight-backup-agent-%d", nodeID)
}

func defaultBackupNodeLogPath(value string, installDir string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	dir := strings.TrimRight(defaultBackupNodeInstallDir(installDir), "/")
	return dir + "/backup-agent.log"
}

func defaultBackupNodeServerURL(value string, c *gin.Context) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	if configured := configuredBackupNodeServerURL(); configured != "" {
		return configured
	}
	return requestBackupNodeServerURL(c)
}

func resolveBackupNodeServerURL(value string, c *gin.Context, targetHost string) (string, error) {
	candidates := []string{}
	seen := map[string]struct{}{}
	appendCandidate := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		candidates = append(candidates, v)
	}
	appendCandidate(value)
	appendCandidate(configuredBackupNodeServerURL())
	appendCandidate(requestBackupNodeServerURL(c))
	if len(candidates) == 0 {
		return "", fmt.Errorf("平台服务地址不能为空；请先在系统通知配置中设置可被备份主机访问的系统地址，或使用可被目标主机访问的地址打开当前系统")
	}
	var lastErr error
	for _, candidate := range candidates {
		if err := validateBackupNodeServerURL(candidate, targetHost); err == nil {
			return candidate, nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("未找到可被目标主机访问的平台服务地址")
	}
	return "", fmt.Errorf("当前平台服务地址对目标主机 %s 不可用：%w。请改用目标主机可访问的系统地址（例如局域网 IP/域名），不要使用 127.0.0.1 或 localhost", strings.TrimSpace(targetHost), lastErr)
}

func configuredBackupNodeServerURL() string {
	if noticeURL, err := notifier.LoadNoticeURL(); err == nil && strings.TrimSpace(noticeURL) != "" {
		return strings.TrimSpace(noticeURL)
	}
	return strings.TrimRight(strings.TrimSpace(global.App.Config.Notify.NoticeURL), "/")
}

func requestBackupNodeServerURL(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	scheme := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto"))
	if scheme == "" {
		if c.Request.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := strings.TrimSpace(c.Request.Host)
	if host == "" {
		host = strings.TrimSpace(c.GetHeader("Host"))
	}
	if host == "" {
		return ""
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}

func validateBackupNodeServerURL(raw string, targetHost string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("平台服务地址不能为空")
	}
	u, err := urlpkg.Parse(raw)
	if err != nil {
		return fmt.Errorf("平台服务地址格式不正确：%w", err)
	}
	if strings.TrimSpace(u.Scheme) == "" || strings.TrimSpace(u.Host) == "" {
		return fmt.Errorf("平台服务地址必须包含协议和主机名")
	}
	if !requiresNonLoopbackServerURL(targetHost) {
		return nil
	}
	host := strings.TrimSpace(u.Hostname())
	if isLoopbackOrUnspecifiedHost(host) {
		return fmt.Errorf("平台服务地址 %s 使用了本地回环地址 %s", raw, host)
	}
	return nil
}

func requiresNonLoopbackServerURL(targetHost string) bool {
	host := normalizeBackupNodeHost(targetHost)
	if host == "" {
		return false
	}
	return !isLoopbackOrUnspecifiedHost(host)
}

func isLoopbackOrUnspecifiedHost(host string) bool {
	h := strings.TrimSpace(strings.Trim(host, "[]"))
	if h == "" {
		return false
	}
	switch strings.ToLower(h) {
	case "localhost", "ip6-localhost", "ip6-loopback":
		return true
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsUnspecified()
}

func refreshBackupNodeDetail(id uint64, c *gin.Context) (any, error) {
	service := GetBackupNodeDetailService{ID: id, C: c}
	return service.Run()
}
