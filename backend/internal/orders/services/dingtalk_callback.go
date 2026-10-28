package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/orders/forms"
	"github.com/simajiong/jaysql/internal/orders/models"
)

// ============================================================================
// 工单审批的钉钉集成 —— 回调侧
//
// 审批人在钉钉卡片上点「通过 / 转交」后，事件经 Stream 长连接推到这里。
// 安全上有四道闸，缺一不可：
//   1. Stream 通道本身已用 AppKey/AppSecret 鉴权，来源可信；
//   2. 卡片实例 ID（outTrackId，服务端生成的随机串）定位到唯一一条令牌记录；
//   3. 点击人 userId 必须与该令牌绑定的审批人一致 —— 卡片被转发也点不动；
//   4. 令牌用行锁原子消费，重复点击/重放直接返回幂等结果。
//
// 通过/转交之后**不新写流程**，一律复用 ApprovalOrderService / TransferOrderService，
// 保证「钉钉里点的」和「平台页面点的」走完全相同的状态流转与操作日志。
// ============================================================================

// cardCallbackData 钉钉推来的卡片互动回调 data 字段（JSON 字符串解码后）。
type cardCallbackData struct {
	OutTrackID string `json:"outTrackId"`
	UserID     string `json:"userId"`
	CorpID     string `json:"corpId"`
	SpaceType  string `json:"spaceType"`
	// Content 是嵌套的 JSON 字符串，形如 {"cardPrivateData":{"actionIds":[...],"params":{...}}}
	Content string `json:"content"`
}

// cardActionContent 解析 Content 后的动作数据。
type cardActionContent struct {
	CardPrivateData struct {
		ActionIDs []string       `json:"actionIds"`
		Params    map[string]any `json:"params"`
	} `json:"cardPrivateData"`
}

// HandleDingTalkCardCallback 处理卡片按钮回调，返回给钉钉的 ACK data。
//
// 返回值会作为 Stream 帧的 data 回给钉钉（用于就地更新卡片）。
// 业务失败不返回 error（那会让钉钉按 500 重推），而是把原因写进卡片提示。
func HandleDingTalkCardCallback(ctx context.Context, data string) (string, error) {
	callback, action, paramString, err := decodeCardCallback(data)
	if err != nil {
		global.App.Log.Warnf("[order-dingtalk] 卡片回调解析失败: %v", err)
		// 解析不了也要 ACK，否则钉钉会持续重推同一条脏数据
		return "", nil
	}
	global.App.Log.Infof("[order-dingtalk] 收到卡片回调 outTrackId=%s action=%s user=%s",
		callback.OutTrackID, action, maskDingTalkID(callback.UserID))

	// 原子消费令牌；拿到令牌记录才允许继续
	tokenRow, err := consumeApprovalToken(callback.OutTrackID, callback.UserID)
	if err != nil {
		global.App.Log.Warnf("[order-dingtalk] 令牌校验失败 outTrackId=%s: %v", callback.OutTrackID, err)
		return buildCardNotice(fmt.Sprintf("操作未生效：%v", err))
	}

	var actionErr error
	var statusText string
	switch action {
	case cardActionApprove:
		actionErr = handleApproveAction(ctx, tokenRow)
		if actionErr == nil {
			statusText = "已通过"
		}
	case cardActionTransfer:
		newClaimer := strings.TrimSpace(paramString(newClaimerParamKey))
		if newClaimer == "" {
			// 没有指定转交对象：不消费令牌，让审批人重新操作
			if releaseErr := releaseApprovalToken(tokenRow); releaseErr != nil {
				global.App.Log.Warnf("[order-dingtalk] 释放令牌失败: %v", releaseErr)
			}
			return buildCardNotice("请先在卡片中选择转交对象，再点击「转交」")
		}
		actionErr = handleTransferAction(ctx, tokenRow, newClaimer)
		if actionErr == nil {
			statusText = "已转交给 " + newClaimer
		}
	default:
		actionErr = fmt.Errorf("该按钮未配置处理逻辑（actionId=%s）", action)
	}

	if actionErr != nil {
		// 审批失败：把令牌放回去，让审批人有机会重试或改用平台页面
		if releaseErr := releaseApprovalToken(tokenRow); releaseErr != nil {
			global.App.Log.Warnf("[order-dingtalk] 释放令牌失败: %v", releaseErr)
		}
		global.App.Log.Warnf("[order-dingtalk] 卡片操作失败 order=%s action=%s: %v",
			tokenRow.OrderID, action, actionErr)
		return buildCardNotice(fmt.Sprintf("操作失败：%v", actionErr))
	}

	finalizeApprovalToken(tokenRow, fmt.Sprintf("%s by %s", action, tokenRow.Approver))
	return buildCardNotice(statusText)
}

// decodeCardCallback 解开两层嵌套 JSON，取出动作与参数。
func decodeCardCallback(data string) (cardCallbackData, string, func(string) string, error) {
	var callback cardCallbackData
	if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &callback); err != nil {
		return callback, "", nil, fmt.Errorf("解析回调内容失败: %w", err)
	}
	if strings.TrimSpace(callback.OutTrackID) == "" {
		return callback, "", nil, fmt.Errorf("回调缺少 outTrackId")
	}
	action := ""
	params := map[string]any{}
	if strings.TrimSpace(callback.Content) != "" {
		var content cardActionContent
		if err := json.Unmarshal([]byte(callback.Content), &content); err == nil {
			if len(content.CardPrivateData.ActionIDs) > 0 {
				action = strings.TrimSpace(content.CardPrivateData.ActionIDs[0])
			}
			params = content.CardPrivateData.Params
		}
	}
	// 兜底：部分卡片模板把动作名放在 params.action 里
	if action == "" {
		if value, ok := params["action"]; ok && value != nil {
			action = strings.TrimSpace(fmt.Sprintf("%v", value))
		}
	}
	lookup := func(key string) string {
		value, ok := params[key]
		if !ok || value == nil {
			return ""
		}
		return fmt.Sprintf("%v", value)
	}
	return callback, action, lookup, nil
}

// newClaimerParamKey 卡片「转交」按钮回传参数里承载新审批人的键名。
const newClaimerParamKey = "new_claimer"

// consumeApprovalToken 行锁消费令牌：只有「卡片实例ID 匹配 + 状态为 ISSUED + 点击人匹配」时才置为 CONSUMED。
func consumeApprovalToken(outTrackID, clickerUserID string) (models.InsightOrderApprovalTokens, error) {
	var tokenRow models.InsightOrderApprovalTokens
	if global.App.DB == nil {
		return tokenRow, fmt.Errorf("数据库未初始化")
	}
	err := global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("out_track_id = ?", outTrackID).
			Order("id desc").
			Take(&tokenRow).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return fmt.Errorf("找不到该卡片对应的审批记录")
			}
			return err
		}
		switch tokenRow.Status {
		case "CONSUMED":
			return fmt.Errorf("该审批已处理，无需重复操作")
		case "REVOKED":
			return fmt.Errorf("该审批已失效（工单可能已转交或重新提交）")
		}
		expiresAt := time.Time(tokenRow.ExpiresAt)
		if !expiresAt.IsZero() && expiresAt.Before(time.Now()) {
			return fmt.Errorf("审批链接已过期，请联系提交人重新发起")
		}
		if bound := strings.TrimSpace(tokenRow.ApproverDingTalkID); bound != "" &&
			bound != strings.TrimSpace(clickerUserID) {
			return fmt.Errorf("只有该工单的审批人本人可以操作")
		}
		return tx.Model(&models.InsightOrderApprovalTokens{}).
			Where("id = ? AND status = ?", tokenRow.ID, "ISSUED").
			Updates(map[string]any{
				"status":      "CONSUMED",
				"consumed_at": time.Now(),
			}).Error
	})
	return tokenRow, err
}

// releaseApprovalToken 把令牌放回 ISSUED，让审批人可以重试。
func releaseApprovalToken(tokenRow models.InsightOrderApprovalTokens) error {
	return global.App.DB.Model(&models.InsightOrderApprovalTokens{}).
		Where("id = ? AND status = ?", tokenRow.ID, "CONSUMED").
		Updates(map[string]any{
			"status":      "ISSUED",
			"consumed_at": nil,
			"remark":      "",
		}).Error
}

// finalizeApprovalToken 记录令牌的最终消费结果（便于事后核对卡片与库内是否一致）。
func finalizeApprovalToken(tokenRow models.InsightOrderApprovalTokens, remark string) {
	if err := global.App.DB.Model(&models.InsightOrderApprovalTokens{}).
		Where("id = ?", tokenRow.ID).
		Update("remark", truncateRemark(remark)).Error; err != nil {
		global.App.Log.Warnf("[order-dingtalk] 更新令牌结果失败: %v", err)
	}
}

// handleApproveAction 审批通过：复用平台同一套审批服务。
func handleApproveAction(_ context.Context, tokenRow models.InsightOrderApprovalTokens) error {
	if err := ensureTokenStillCurrent(tokenRow); err != nil {
		return err
	}
	service := &ApprovalOrderService{
		ApprovalOrderForm: &forms.ApprovalOrderForm{
			OrderID: tokenRow.OrderID,
			Status:  "APPROVED",
			Msg:     "通过钉钉审批卡片操作",
		},
		C:        systemContext(),
		Username: tokenRow.Approver,
	}
	if err := service.Run(); err != nil {
		return err
	}
	// 审批完成后旧卡片一律失效，避免继续可点
	return RevokeApprovalTokens(tokenRow.OrderID, "审批已完成")
}

// handleTransferAction 转交：复用平台的转交服务，并对新审批人重新投递卡片。
func handleTransferAction(ctx context.Context, tokenRow models.InsightOrderApprovalTokens, newClaimer string) error {
	if newClaimer == tokenRow.Approver {
		return fmt.Errorf("转交对象不能是自己")
	}
	if err := ensureTokenStillCurrent(tokenRow); err != nil {
		return err
	}
	service := &TransferOrderService{
		TransferOrderForm: &forms.TransferOrderForm{
			OrderID:    tokenRow.OrderID,
			NewClaimer: newClaimer,
			Msg:        "通过钉钉审批卡片转交",
		},
		C:        systemContext(),
		Username: tokenRow.Approver,
	}
	if err := service.Run(); err != nil {
		return err
	}
	// 旧卡片作废
	if err := RevokeApprovalTokens(tokenRow.OrderID, "已转交，该卡片失效"); err != nil {
		global.App.Log.Warnf("[order-dingtalk] 作废旧令牌失败 order=%s: %v", tokenRow.OrderID, err)
	}
	// 关键：给新审批人重新签发令牌并重新投递，否则他收不到任何通知
	if err := NotifyApproverByDingTalk(ctx, tokenRow.OrderID, newClaimer); err != nil {
		global.App.Log.Warnf("[order-dingtalk] 转交后向新审批人 %s 投递失败: %v", newClaimer, err)
		// 不向回调方报错：转交本身已成功，通知失败只影响体验，界面提示由平台侧兜底
	}
	return nil
}

// ensureTokenStillCurrent 确认令牌绑定的人仍是工单当前审批人。
//
// 防的是这种时序：提交人先发给 A（卡片1），A 转交给 B（卡片2 已发），
// 随后 A 又点开了卡片1 —— 此时 A 已不是审批人，必须拒绝。
func ensureTokenStillCurrent(tokenRow models.InsightOrderApprovalTokens) error {
	var approval models.InsightApprovalRecords
	if err := global.App.DB.Where("order_id = ? AND stage = ?", tokenRow.OrderID, 1).
		Take(&approval).Error; err != nil {
		return fmt.Errorf("审批记录不存在或已变更")
	}
	if approval.Approver != tokenRow.Approver {
		return fmt.Errorf("您已不是该工单的审批人（当前审批人：%s）", approval.Approver)
	}
	if approval.ApprovalStatus != "PENDING" {
		return fmt.Errorf("该工单已审批，无需重复操作")
	}
	return nil
}

// systemContext 为「无 HTTP 请求」的调用路径构造一个最小 gin.Context。
//
// 为什么需要：审批/转交服务在写审计日志时会解析 JWT claims，nil context 会 panic。
// Stream 回调没有真实请求，这里给一个空壳，让审计日志至少记下操作人、动作与结果。
func systemContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	return c
}

// buildCardNotice 构造「就地更新卡片」的回调响应。
//
// 钉钉约定：callback 的 data 里放 {"response":{cardUpdateOptions, cardData}}，
// 卡片会立刻刷新为新的 status/content，用户能直接看到结果，不必再进平台确认。
func buildCardNotice(status string) (string, error) {
	payload := map[string]any{
		"response": map[string]any{
			"cardUpdateOptions": map[string]any{
				"updateCardDataByKey":    true,
				"updatePrivateDataByKey": true,
			},
			"cardData": map[string]any{
				"cardParamMap": map[string]string{
					"status": status,
				},
			},
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// maskDingTalkID 日志里对钉钉 userid 打码，避免把员工标识明文写进日志。
func maskDingTalkID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 6 {
		return "***"
	}
	return value[:3] + "***" + value[len(value)-3:]
}
