package views

import (
	"context"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/orders/services"
	"github.com/simajiong/jaysql/pkg/response"
)

// ============================================================================
// 钉钉审批相关视图
//
// 三个端点各有用处：
//   - preview：提交审批弹窗实时问「这位审批人能收到钉钉吗」，把配置问题前置暴露
//   - status ：长连接健康度，出问题时第一眼就能看出是"没连上"还是"连上但没回调"
//   - callback：HTTP 形态的回调入口。生产走 Stream 长连接，这里主要用于联调与兜底
//     （以及对未来「部署在公网、想改用 HTTP 回调」的场景预留）
// ============================================================================

// PreviewDingTalkNotifyView 查询能否给指定审批人投递钉钉审批通知。
//
// 注意：available=false 不是错误，是"当前发不了"的正常结论，
// 所以走 Success 返回、由前端按 available/reason 展示提示。
func PreviewDingTalkNotifyView(c *gin.Context) {
	approver := strings.TrimSpace(c.Query("approver"))
	if approver == "" {
		response.Fail(c, "审批人不能为空")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	response.Success(c, services.PreviewApprovalNotify(ctx, approver), "success")
}

// DingTalkStreamStatusView 返回 Stream 长连接状态（诊断用）。
func DingTalkStreamStatusView(c *gin.Context) {
	response.Success(c, gin.H{
		"connected": services.DingTalkStreamConnected(),
	}, "success")
}

// dingTalkCallbackForm HTTP 回调/联调入参。
type dingTalkCallbackForm struct {
	// Data 与钉钉 Stream 帧的 data 字段同构（JSON 字符串）：
	// {"outTrackId":"...","userId":"...","content":"{\"cardPrivateData\":{...}}"}
	Data string `json:"data" binding:"required"`
}

// DingTalkCardCallbackView HTTP 形态的卡片回调入口。
//
// 与 Stream 路径共用同一个 services.HandleDingTalkCardCallback，
// 因此两条通道的校验、幂等与业务流转完全一致，不会出现"流式能审、HTTP 不能审"的分裂。
func DingTalkCardCallbackView(c *gin.Context) {
	var form dingTalkCallbackForm
	if err := c.ShouldBind(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	ack, err := services.HandleDingTalkCardCallback(c.Request.Context(), form.Data)
	if err != nil {
		response.Fail(c, err.Error())
		return
	}
	response.Success(c, gin.H{"ack": ack}, "success")
}
