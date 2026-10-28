package services

import (
	"fmt"
	"strings"
	"time"

	jwt "github.com/appleboy/gin-jwt/v2"
	"github.com/gin-gonic/gin"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
)

// UpdateAlertActionService 大屏「告警事件中心」的人工处置。
//
//   - ack    （认领工单）：status → acknowledged，记录认领人/时间。评估器对
//     acknowledged 事件只更新数值不回退状态，指标恢复正常后照常走恢复防抖落定 recovered。
//   - ignore （忽略）：ignored = 1。与 Suppressed 刻意分列 —— Suppressed 是采集失败期
//     的自动抑制，每轮被评估器覆写；人工忽略必须独立存列，否则下一轮就被冲掉。
//   - unignore（取消忽略）：ignored = 0，告警回到待处置列表。
//
// 只允许处置 active / pending / acknowledged 的事件；已恢复（recovered）的告警
// 是历史记录，不该再被翻出来操作。
type UpdateAlertActionService struct {
	Fingerprint string
	Action      string // ack | ignore | unignore
	C           *gin.Context
}

func (s *UpdateAlertActionService) Run() error {
	if global.App.DB == nil {
		return fmt.Errorf("数据库未初始化")
	}
	fingerprint := strings.TrimSpace(s.Fingerprint)
	if fingerprint == "" {
		return fmt.Errorf("告警标识不能为空")
	}

	username := ""
	if s.C != nil {
		if raw, ok := jwt.ExtractClaims(s.C)["id"].(string); ok {
			username = strings.TrimSpace(raw)
		}
	}

	now := commonModels.LocalTime(time.Now())
	var updates map[string]any
	switch s.Action {
	case "ack":
		updates = map[string]any{"status": "acknowledged", "acked_by": username, "acked_at": now}
	case "ignore":
		updates = map[string]any{"ignored": true, "ignored_by": username, "ignored_at": now}
	case "unignore":
		updates = map[string]any{"ignored": false, "ignored_by": "", "ignored_at": nil}
	default:
		return fmt.Errorf("不支持的处置动作：%s", s.Action)
	}

	result := global.App.DB.Model(&monitorModels.InsightMonitorAlertEvents{}).
		Where("fingerprint = ? AND status IN ?", fingerprint, []string{"active", "pending", "acknowledged"}).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("告警不存在或已恢复，无法处置：%s", fingerprint)
	}
	global.App.Log.Infof("monitor alert %s by %s: %s", s.Action, username, fingerprint)

	// 认领回执：向命中策略的渠道发一条"已被认领，恢复前不再重复发送"。
	// 恢复时 recoverSilentAlerts 照常发恢复通知（认领算"被告知过"）；
	// 周期重发对 acknowledged 事件不生效（MaybeDispatchRepeatNotify 只处理 active）。
	if s.Action == "ack" {
		var event monitorModels.InsightMonitorAlertEvents
		if err := global.App.DB.
			Where("fingerprint = ? AND status = ?", fingerprint, "acknowledged").
			Order("id desc").
			Take(&event).Error; err == nil {
			DispatchAckNotify(event.ID, username)
			// 「人工认领后停止升级」：终止该告警当前及后续的全部升级。
			// 后续级此时还没生成（只有上一级升级时才创建），因此终止当前级即终止整条链。
			// 是否真的终止取决于策略的 stop_on_ack 开关 —— 运维显式关掉的策略会继续升级。
			cancelEscalationsOnAck(fingerprint)
		}
	}
	if s.Action == "ignore" {
		// 人工忽略 = 不再需要任何人介入，升级链一并终止（不看 stop_on_ack：忽略比认领更彻底）
		cancelAlertEscalations(fingerprint, "告警已被人工忽略")
	}
	return nil
}
