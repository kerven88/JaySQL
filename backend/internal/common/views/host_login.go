package views

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/common/forms"
	"github.com/simajiong/jaysql/internal/common/services"
	"github.com/simajiong/jaysql/pkg/response"
)

// ---------------------------------------------------------------------------
// 主机登录
//
// 业务失败（凭据不对、连接超时、并发超限……）一律用 response.Success 返回，
// data 里带 ok/code/message —— 前端按 code 分支展示，不需要靠 HTTP 状态码猜；
// 只有「参数没传」这类契约问题才走 response.Fail。
// ---------------------------------------------------------------------------

// HostLoginPrecheckView 终端页加载后自检：凭据是否齐备、主机是否可连。不建立交互式会话。
func HostLoginPrecheckView(c *gin.Context) {
	var form forms.HostLoginPrecheckForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, "参数校验失败：instance_id 不能为空")
		return
	}
	service := services.HostLoginPrecheckService{HostLoginPrecheckForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// CreateHostLoginSessionView 签发一次性令牌。真正的 SSH 连接在 WS 握手时才建立。
func CreateHostLoginSessionView(c *gin.Context) {
	var form forms.CreateHostLoginSessionForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.ValidateFail(c, "参数校验失败：instance_id 不能为空")
		return
	}
	service := services.CreateHostLoginSessionService{CreateHostLoginSessionForm: &form, C: c}
	data, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, data, "success")
}

// CloseHostLoginSessionView 主动断开。幂等：会话不存在也返回成功。
func CloseHostLoginSessionView(c *gin.Context) {
	sessionID := strings.TrimSpace(c.Param("id"))
	if sessionID == "" {
		response.ValidateFail(c, "会话 ID 不能为空")
		return
	}
	services.CloseHostLoginSession(sessionID, services.CurrentHostLoginUsername(c))
	response.Success(c, gin.H{"ok": true}, "success")
}

// GetHostLoginSessionsView 当前用户的活跃会话（并发上限提示里的「查看我的终端」）。
func GetHostLoginSessionsView(c *gin.Context) {
	response.Success(c, services.ListHostLoginSessions(services.CurrentHostLoginUsername(c)), "success")
}

// HostLoginTerminalView WebSocket 终端。
func HostLoginTerminalView(c *gin.Context) {
	sessionID := strings.TrimSpace(c.Param("session_id"))
	// 令牌走 query 而不是 header：浏览器 WebSocket API 不允许自定义握手头
	token := strings.TrimSpace(c.Query("token"))
	if sessionID == "" || token == "" {
		response.ValidateFail(c, "会话 ID 与令牌不能为空")
		return
	}
	services.ServeHostLoginTerminal(c, sessionID, token)
}
