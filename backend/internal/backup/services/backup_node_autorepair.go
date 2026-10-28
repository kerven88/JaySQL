package services

import (
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	backupForms "github.com/simajiong/jaysql/internal/backup/forms"
	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
)

const (
	backupNodeAutoRepairStateHealthy        = "HEALTHY"
	backupNodeAutoRepairStateRestarted      = "RESTARTED"
	backupNodeAutoRepairStateRepaired       = "REPAIRED"
	backupNodeAutoRepairStateRebuilt        = "REBUILT"
	backupNodeAutoRepairStateManualRequired = "MANUAL_REQUIRED"
	backupNodeAutoRepairStateFailed         = "FAILED"
)

type backupNodeAutoRepairResult struct {
	Attempted   bool
	Recovered   bool
	Rebuilt     bool
	State       string
	Summary     string
	LastError   string
	Actions     []string
	Detail      map[string]any
	CurrentNode *backupModels.InsightBackupNodes
}

func tryAutoRepairBackupNode(c *gin.Context, node *backupModels.InsightBackupNodes, instance *commonModels.InsightInstances, roleLabel string) (*backupModels.InsightBackupNodes, *backupNodeAutoRepairResult) {
	result := &backupNodeAutoRepairResult{
		State:   backupNodeAutoRepairStateHealthy,
		Actions: []string{},
	}
	if node == nil || node.ID == 0 {
		result.State = backupNodeAutoRepairStateManualRequired
		result.Summary = firstNonEmpty(strings.TrimSpace(roleLabel), "节点") + "不存在，无法自动修复。"
		result.Detail = map[string]any{"reason": "node_missing"}
		return node, result
	}
	fresh, err := loadBackupNodeByID(node.ID)
	if err == nil && fresh != nil {
		node = fresh
	}
	result.CurrentNode = node
	if strings.EqualFold(strings.TrimSpace(string(node.Status)), "ONLINE") {
		result.Summary = firstNonEmpty(strings.TrimSpace(roleLabel), "节点") + "当前在线，无需自动修复。"
		result.Detail = buildBackupNodeAutoRepairDetail(result, node)
		return node, result
	}

	result.Attempted = true
	result.Actions = append(result.Actions, fmt.Sprintf("检测到%s离线，开始执行自动修复流程", firstNonEmpty(strings.TrimSpace(roleLabel), "节点")))

	form, formErr := buildBackupNodeAutoRepairForm(node, instance, c)
	if formErr != nil {
		result.State = backupNodeAutoRepairStateManualRequired
		result.LastError = formErr.Error()
		result.Summary = fmt.Sprintf("%s离线，且当前缺少自动修复所需的 SSH/平台地址信息。", firstNonEmpty(strings.TrimSpace(roleLabel), "节点"))
		result.Actions = append(result.Actions, "自动修复准备失败："+formErr.Error())
		result.Detail = buildBackupNodeAutoRepairDetail(result, node)
		return node, result
	}

	if _, opErr := runBackupNodeAutoRepairAction(c, node.ID, form, "restart_agent", 12); opErr == nil {
		if refreshed, ok := reloadBackupNodeIfOnline(node.ID); ok {
			result.Recovered = true
			result.State = backupNodeAutoRepairStateRestarted
			result.Summary = fmt.Sprintf("%s离线后已自动重启 Agent，并恢复在线。", firstNonEmpty(strings.TrimSpace(roleLabel), "节点"))
			result.Actions = append(result.Actions, "已自动执行 Agent 重启并恢复在线")
			result.CurrentNode = refreshed
			result.Detail = buildBackupNodeAutoRepairDetail(result, refreshed)
			return refreshed, result
		}
		result.Actions = append(result.Actions, "已执行 Agent 重启，但未恢复在线")
	} else {
		result.LastError = opErr.Error()
		result.Actions = append(result.Actions, "Agent 重启失败："+opErr.Error())
	}

	if _, _, payloadErr := loadAgentBinaryPayload(); payloadErr != nil {
		result.State = backupNodeAutoRepairStateManualRequired
		result.LastError = payloadErr.Error()
		result.Summary = fmt.Sprintf("%s离线，系统已尝试重启，但当前服务端缺少可用于重装/重建的 backup-agent 二进制。", firstNonEmpty(strings.TrimSpace(roleLabel), "节点"))
		result.Actions = append(result.Actions, "跳过修复/重建："+payloadErr.Error())
		result.Detail = buildBackupNodeAutoRepairDetail(result, node)
		return node, result
	}

	if _, opErr := runBackupNodeAutoRepairAction(c, node.ID, form, "repair_agent", 15); opErr == nil {
		if refreshed, ok := reloadBackupNodeIfOnline(node.ID); ok {
			result.Recovered = true
			result.State = backupNodeAutoRepairStateRepaired
			result.Summary = fmt.Sprintf("%s离线后已自动修复 Agent，并恢复在线。", firstNonEmpty(strings.TrimSpace(roleLabel), "节点"))
			result.Actions = append(result.Actions, "已自动执行 Agent 修复并恢复在线")
			result.CurrentNode = refreshed
			result.Detail = buildBackupNodeAutoRepairDetail(result, refreshed)
			return refreshed, result
		}
		result.Actions = append(result.Actions, "已执行 Agent 修复，但未恢复在线")
	} else {
		result.LastError = opErr.Error()
		result.Actions = append(result.Actions, "Agent 修复失败："+opErr.Error())
	}

	if rebuildErr := rebuildBackupNodeIdentity(c, node, form); rebuildErr != nil {
		result.State = backupNodeAutoRepairStateFailed
		result.LastError = rebuildErr.Error()
		result.Summary = fmt.Sprintf("%s离线，系统已尝试自动修复，但在重建节点身份时失败。", firstNonEmpty(strings.TrimSpace(roleLabel), "节点"))
		result.Actions = append(result.Actions, "节点身份重建失败："+rebuildErr.Error())
		result.Detail = buildBackupNodeAutoRepairDetail(result, node)
		return node, result
	}
	result.Actions = append(result.Actions, "已刷新节点 token / server_url / service 配置，开始重建 Agent")
	if _, opErr := runBackupNodeAutoRepairAction(c, node.ID, form, "install_agent", 20); opErr == nil {
		if refreshed, ok := reloadBackupNodeIfOnline(node.ID); ok {
			result.Recovered = true
			result.Rebuilt = true
			result.State = backupNodeAutoRepairStateRebuilt
			result.Summary = fmt.Sprintf("%s离线后已自动重建 Agent，并恢复在线。", firstNonEmpty(strings.TrimSpace(roleLabel), "节点"))
			result.Actions = append(result.Actions, "已自动重建 Agent 并恢复在线")
			result.CurrentNode = refreshed
			result.Detail = buildBackupNodeAutoRepairDetail(result, refreshed)
			return refreshed, result
		}
		result.Actions = append(result.Actions, "已执行 Agent 重建，但未恢复在线")
	} else {
		result.LastError = opErr.Error()
		result.Actions = append(result.Actions, "Agent 重建失败："+opErr.Error())
	}

	refreshed, _ := loadBackupNodeByID(node.ID)
	if refreshed != nil {
		node = refreshed
	}
	result.State = backupNodeAutoRepairStateFailed
	result.Summary = fmt.Sprintf("%s离线，系统已依次尝试重启、修复、重建 Agent，但仍未恢复在线。", firstNonEmpty(strings.TrimSpace(roleLabel), "节点"))
	result.CurrentNode = node
	result.Detail = buildBackupNodeAutoRepairDetail(result, node)
	return node, result
}

func buildBackupNodeAutoRepairForm(node *backupModels.InsightBackupNodes, instance *commonModels.InsightInstances, c *gin.Context) (*backupForms.OperateBackupNodeForm, error) {
	if node == nil {
		return nil, fmt.Errorf("备份节点不存在")
	}
	host := strings.TrimSpace(node.Host)
	if host == "" {
		return nil, fmt.Errorf("备份节点地址不能为空")
	}
	sshUser := defaultBackupNodeSSHUser(strings.TrimSpace(node.SSHUser))
	// 库里的节点凭据是密文，这里解密后填入操作表单（历史明文由 Decrypt 幂等原样返回）
	sshPassword := decryptCredential(node.SSHPassword)
	sshPrivateKey := decryptCredential(node.SSHPrivateKey)
	port := node.Port
	if port <= 0 {
		port = 22
	}
	if instance != nil && strings.EqualFold(strings.TrimSpace(instance.Hostname), host) {
		if user, password, privateKey, sshPort, ok, _ := resolveSSHCredentialsFromInstance(instance); ok {
			sshUser = defaultBackupNodeSSHUser(strings.TrimSpace(user))
			sshPassword = strings.TrimSpace(password)
			sshPrivateKey = strings.TrimSpace(privateKey)
			if sshPort > 0 {
				port = sshPort
			}
		}
	}
	if sshPassword == "" && sshPrivateKey == "" {
		return nil, fmt.Errorf("节点 %s 缺少 SSH 密码或私钥", host)
	}
	serverURL := strings.TrimSpace(node.ServerURL)
	if resolved, err := resolveBackupNodeServerURL(serverURL, c, host); err == nil {
		serverURL = resolved
	} else if serverURL == "" {
		return nil, err
	}
	if strings.TrimSpace(serverURL) == "" {
		return nil, fmt.Errorf("节点 %s 缺少可用的 server_url", host)
	}
	return &backupForms.OperateBackupNodeForm{
		SSHUser:       sshUser,
		SSHPassword:   sshPassword,
		SSHPrivateKey: sshPrivateKey,
		ServerURL:     serverURL,
		InstallDir:    defaultBackupNodeInstallDir(strings.TrimSpace(node.InstallDir)),
		ToolsDir:      defaultBackupNodeToolsDir(strings.TrimSpace(node.ToolsDir)),
		ServiceName:   defaultBackupNodeServiceName(strings.TrimSpace(node.ServiceName), node.ID),
		LogPath:       defaultBackupNodeLogPath(strings.TrimSpace(node.LogPath), strings.TrimSpace(node.InstallDir)),
		WaitOnlineSec: 20,
	}, nil
}

func runBackupNodeAutoRepairAction(c *gin.Context, nodeID uint64, form *backupForms.OperateBackupNodeForm, action string, waitSec int) (*backupNodeOperateResult, error) {
	if form == nil {
		return nil, fmt.Errorf("节点操作表单不能为空")
	}
	cloned := *form
	cloned.Action = action
	cloned.WaitOnlineSec = waitSec
	operate := OperateBackupNodeService{
		OperateBackupNodeForm: &cloned,
		C:                     c,
		ID:                    nodeID,
	}
	raw, err := operate.Run()
	if err != nil {
		return nil, err
	}
	if result, ok := raw.(*backupNodeOperateResult); ok {
		return result, nil
	}
	return nil, nil
}

func rebuildBackupNodeIdentity(c *gin.Context, node *backupModels.InsightBackupNodes, form *backupForms.OperateBackupNodeForm) error {
	if node == nil || form == nil {
		return fmt.Errorf("节点重建参数不完整")
	}
	installDir := defaultBackupNodeInstallDir(firstNonEmpty(strings.TrimSpace(form.InstallDir), strings.TrimSpace(node.InstallDir)))
	updates := map[string]any{
		"token":          strings.ReplaceAll(uuid.NewString(), "-", ""),
		"status":         commonModels.EnumType("OFFLINE"),
		"server_url":     strings.TrimSpace(form.ServerURL),
		"ssh_user":       defaultBackupNodeSSHUser(firstNonEmpty(strings.TrimSpace(form.SSHUser), strings.TrimSpace(node.SSHUser))),
		"install_dir":    installDir,
		"tools_dir":      defaultBackupNodeToolsDir(firstNonEmpty(strings.TrimSpace(form.ToolsDir), strings.TrimSpace(node.ToolsDir))),
		"service_name":   defaultBackupNodeServiceName(firstNonEmpty(strings.TrimSpace(form.ServiceName), strings.TrimSpace(node.ServiceName)), node.ID),
		"log_path":       defaultBackupNodeLogPath(firstNonEmpty(strings.TrimSpace(form.LogPath), strings.TrimSpace(node.LogPath)), installDir),
		"updated_at":     time.Now(),
		"last_heartbeat": commonModels.LocalTime{},
	}
	if username := currentUsername(c); username != "" {
		updates["updated_by"] = username
	}
	if strings.TrimSpace(form.SSHPassword) != "" {
		encrypted, encErr := encryptCredential(form.SSHPassword)
		if encErr != nil {
			return fmt.Errorf("备份节点 SSH 密码加密失败: %w", encErr)
		}
		updates["ssh_password"] = encrypted
	}
	if strings.TrimSpace(form.SSHPrivateKey) != "" {
		encrypted, encErr := encryptCredential(form.SSHPrivateKey)
		if encErr != nil {
			return fmt.Errorf("备份节点 SSH 私钥加密失败: %w", encErr)
		}
		updates["ssh_private_key"] = encrypted
	}
	return global.App.DB.Model(&backupModels.InsightBackupNodes{}).Where("id = ?", node.ID).Updates(updates).Error
}

func reloadBackupNodeIfOnline(nodeID uint64) (*backupModels.InsightBackupNodes, bool) {
	node, err := loadBackupNodeByID(nodeID)
	if err != nil || node == nil {
		return node, false
	}
	return node, strings.EqualFold(strings.TrimSpace(string(node.Status)), "ONLINE")
}

func buildBackupNodeAutoRepairDetail(result *backupNodeAutoRepairResult, node *backupModels.InsightBackupNodes) map[string]any {
	detail := map[string]any{
		"state":     strings.TrimSpace(result.State),
		"attempted": result.Attempted,
		"recovered": result.Recovered,
		"rebuilt":   result.Rebuilt,
		"actions":   strings.Join(result.Actions, " -> "),
	}
	if result.LastError != "" {
		detail["last_error"] = result.LastError
	}
	if node != nil {
		detail["node_id"] = node.ID
		detail["host"] = strings.TrimSpace(node.Host)
		detail["final_status"] = strings.TrimSpace(string(node.Status))
		detail["server_url"] = strings.TrimSpace(node.ServerURL)
		detail["service_name"] = strings.TrimSpace(node.ServiceName)
	}
	return detail
}
