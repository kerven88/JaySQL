package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"github.com/simajiong/jaysql/internal/orders/models"
	usersModels "github.com/simajiong/jaysql/internal/users/models"
	"github.com/simajiong/jaysql/pkg/dingtalk"
	"github.com/simajiong/jaysql/pkg/notifier"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ============================================================================
// 工单审批的钉钉集成 —— 投递侧
//
// 与现有流程的衔接点只有三处，都是「在原有动作之后追加一次通知」，
// 不改动任何既有状态流转：
//   1. SubmitApprovalService.Run()  提交审批成功后  → NotifyApproverByDingTalk
//   2. TransferOrderService.Run()   转交成功后      → NotifyApproverByDingTalk（对新审批人）
//   3. ApprovalOrderService.Run()   审批完成后      → FinalizeApprovalCard（更新卡片为终态）
// 回 调 侧（钉钉内点「通过/转交」）见 dingtalk_callback.go。
//
// 设计红线：**转交后必须对新审批人重新签发令牌并重新投递卡片**。
// 否则新审批人收不到任何通知，而旧审批人手上那张卡片还留着可点的按钮——
// 这正是「转交后工单从『与我相关』消失」那个缺陷在 IM 通道上的镜像版本。
// ============================================================================

const (
	// dingTalkChannelKind 渠道类型：钉钉企业内部应用（可定向到个人）
	dingTalkChannelKind = "dingtalk_app"

	// approvalTokenTTL 令牌有效期。审批周期通常以天计，给 7 天足够，
	// 过期后卡片按钮点击会被拒绝并提示联系提交人重新发起。
	approvalTokenTTL = 7 * 24 * time.Hour

	// cardActionApprove / cardActionTransfer 卡片按钮的 actionId，
	// 需与钉钉卡片搭建器里两个按钮配置的 actionId 保持一致（见渠道元数据说明）。
	cardActionApprove  = "approve"
	cardActionTransfer = "transfer"

	// dingTalkNotifyTimeout 单次钉钉投递的等待上限。审批提交是交互式操作，
	// 不能让提交人干等 HTTP 超时；10 秒足够一次卡片投放或工作通知。
	dingTalkNotifyTimeout = 10 * time.Second
)

// ---------- 渠道解析 ----------

// loadDingTalkChannel 读取第一个启用的「钉钉企业应用」渠道实例（配置已解密）。
func loadDingTalkChannel() (monitorModels.InsightMonitorNotifyChannels, map[string]any, error) {
	var row monitorModels.InsightMonitorNotifyChannels
	if global.App.DB == nil {
		return row, nil, fmt.Errorf("数据库未初始化")
	}
	err := global.App.DB.
		Where("kind = ? AND enabled = ?", dingTalkChannelKind, true).
		Order("id asc").
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return row, nil, fmt.Errorf("尚未配置「钉钉企业应用」通知渠道（告警配置 → 通知渠道 → 新建渠道）")
		}
		return row, nil, err
	}
	cfg, err := notifier.DecodeChannelConfig(row.Config)
	if err != nil {
		return row, nil, fmt.Errorf("钉钉渠道配置解析失败: %w", err)
	}
	return row, cfg, nil
}

// ---------- 审批人钉钉身份 ----------

// resolveApproverDingTalkID 解析审批人的钉钉 userid。
//
// 优先级：用户表里已登记的 dingtalk_userid（管理员手工维护或上次自动匹配的结果），
// 取不到再用手机号调钉钉接口自动匹配并回填。
// 这样既能让绝大多数用户零配置直接可用，又给「通讯录手机号不一致」留了人工兜底。
func resolveApproverDingTalkID(ctx context.Context, client *dingtalk.Client, username string) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return "", fmt.Errorf("审批人为空")
	}
	var user usersModels.InsightUsers
	if err := global.App.DB.Where("username = ? AND is_deleted = 0", username).Take(&user).Error; err != nil {
		return "", fmt.Errorf("审批人 `%s` 不存在", username)
	}
	if !user.IsActive {
		return "", fmt.Errorf("审批人 `%s` 已停用，无法接收钉钉通知", username)
	}
	if id := strings.TrimSpace(user.DingTalkUserID); id != "" {
		return id, nil
	}
	mobile := strings.TrimSpace(user.Mobile)
	if mobile == "" {
		return "", fmt.Errorf("审批人 `%s` 未登记手机号，无法自动匹配钉钉账号；可在「用户管理」中手工填写其钉钉账号", username)
	}
	userID, err := client.UserIDByMobile(ctx, mobile)
	if err != nil {
		return "", fmt.Errorf("审批人 `%s` 钉钉账号匹配失败：%v", username, err)
	}
	if userID == "" {
		return "", fmt.Errorf("审批人 `%s` 的手机号未匹配到钉钉账号，请确认其已在企业通讯录中", username)
	}
	// 回填，后续免调用；失败不影响本次投递。
	//
	// 列名必须写 GORM 的默认转换结果 `ding_talk_user_id`：Updates 传 map 时 GORM
	// **不套用命名策略**，key 会被原样当列名拼进 SQL。写成 dingtalk_user_id 会直接
	// 报 Unknown column，又因为这里只吞 Warn，表现为"功能正常但每次都重新调钉钉接口"。
	if err := global.App.DB.Model(&usersModels.InsightUsers{}).
		Where("uid = ?", user.Uid).
		Updates(dingTalkBindingUpdates(userID)).Error; err != nil {
		global.App.Log.Warnf("[order-dingtalk] 回填用户钉钉账号失败 user=%s: %v", username, err)
	}
	return userID, nil
}

// dingTalkBindingUpdates 回填钉钉绑定要写的字段。
//
// 单独抽成函数是为了让测试能把它对着 AutoMigrate 出来的**真实表结构**跑一遍：
// GORM 对 Updates(map[string]any{...}) 的 key 不套用命名策略，列名写错会在运行时
// 报 Unknown column，而调用点只吞 Warn —— 只能靠测试提前拦住。
func dingTalkBindingUpdates(userID string) map[string]any {
	return map[string]any{
		"ding_talk_user_id":  userID,
		"ding_talk_bound_at": time.Now(),
	}
}

// ---------- 可用性预览 ----------

// ApprovalNotifyPreview 提交审批前的钉钉通知可用性预览。
type ApprovalNotifyPreview struct {
	// Available 现在能否给这位审批人发钉钉通知
	Available bool `json:"available"`
	// Reason 不可用时的可读原因（直接展示给提交人）
	Reason string `json:"reason"`
	// ChannelName 命中的渠道实例名
	ChannelName string `json:"channel_name"`
	// Mode card=互动卡片（可在钉钉内一键审批） / work_notice=工作通知（点链接回平台）
	Mode string `json:"mode"`
	// ApproverBound 审批人的钉钉账号是否已解析成功
	ApproverBound bool `json:"approver_bound"`
	// StreamConnected 卡片回调用的 Stream 长连接是否已建立。
	// 只在 mode=card 时有意义：卡片发出去了但长连接没连上，审批人点了按钮
	// 平台上收不到任何回调，表现为「点了没反应」——这类故障必须在提交前就提示出来，
	// 否则提交人会以为已通知、审批人会以为系统坏了。
	StreamConnected bool `json:"stream_connected"`
}

// PreviewApprovalNotify 供提交审批弹窗实时查询：渠道配好了吗、这位审批人能被找到吗。
//
// 刻意区分「渠道没配」与「审批人没绑」两种原因：前者要管理员处理，
// 后者可以让提交人换人或提醒对方补手机号，提示文案完全不同。
func PreviewApprovalNotify(ctx context.Context, approver string) ApprovalNotifyPreview {
	row, cfg, err := loadDingTalkChannel()
	if err != nil {
		return ApprovalNotifyPreview{Available: false, Reason: err.Error()}
	}
	preview := ApprovalNotifyPreview{ChannelName: row.Name, Mode: notifyModeOf(cfg)}
	client := notifier.DingTalkAppClientFromConfig(cfg)
	if _, err := resolveApproverDingTalkID(ctx, client, approver); err != nil {
		preview.Reason = err.Error()
		return preview
	}
	preview.Available = true
	preview.ApproverBound = true
	// 只有卡片模式依赖长连接回传点击；工作通知是"点链接回平台"，不依赖它。
	if preview.Mode == "card" {
		preview.StreamConnected = DingTalkStreamConnected()
	}
	if preview.Mode == "work_notice" {
		preview.Reason = "未配置互动卡片模板，将以工作通知方式推送（需点链接回平台审批）"
	}
	return preview
}

// notifyModeOf 判断该渠道当前会走哪条投递路径。
func notifyModeOf(cfg map[string]any) string {
	value, ok := cfg["card_template_id"]
	if !ok || value == nil {
		return "work_notice"
	}
	if strings.TrimSpace(fmt.Sprintf("%v", value)) == "" {
		return "work_notice"
	}
	return "card"
}

// ---------- 投递 ----------

// NotifyApproverByDingTalk 给指定审批人投递「待审批」通知（卡片优先，降级工作通知）。
//
// 幂等性处理：每次投递前先把该工单未消费的旧令牌置为 REVOKED，
// 保证同一时刻最多只有一张有效卡片，避免「重新提交/转交」后新旧卡片都能点。
func NotifyApproverByDingTalk(ctx context.Context, orderID, approver string) error {
	orderID = strings.TrimSpace(orderID)
	approver = strings.TrimSpace(approver)
	if orderID == "" || approver == "" {
		return fmt.Errorf("工单号与审批人均不能为空")
	}

	_, cfg, err := loadDingTalkChannel()
	if err != nil {
		return err
	}
	client := notifier.DingTalkAppClientFromConfig(cfg)
	dingTalkID, err := resolveApproverDingTalkID(ctx, client, approver)
	if err != nil {
		return err
	}

	var record models.InsightOrderRecords
	if err := global.App.DB.Where("order_id = ?", orderID).Take(&record).Error; err != nil {
		return fmt.Errorf("工单 `%s` 不存在", orderID)
	}

	// 先作废旧令牌：即使后面的投递失败，也不会留下一张能点的过期卡片
	if err := RevokeApprovalTokens(orderID, "重新投递前作废旧令牌"); err != nil {
		global.App.Log.Warnf("[order-dingtalk] 作废旧审批令牌失败 order=%s: %v", orderID, err)
	}

	token := newApprovalToken()
	tokenRow := models.InsightOrderApprovalTokens{
		Model:              newOrderBaseModel(),
		Token:              token,
		OrderID:            orderID,
		Stage:              1,
		Approver:           approver,
		ApproverDingTalkID: dingTalkID,
		Channel:            dingTalkChannelKind,
		OutTrackID:         "",
		Status:             "ISSUED",
		ExpiresAt:          commonModels.LocalTime(time.Now().Add(approvalTokenTTL)),
	}

	linkURL := approvalLinkFor(record)
	params := cardParamsForOrder(record, "待您审批")

	if notifyModeOf(cfg) == "card" {
		outTrackID := "gi-apv-" + newApprovalToken()[:24]
		tokenRow.OutTrackID = outTrackID
		if err := global.App.DB.Create(&tokenRow).Error; err != nil {
			return fmt.Errorf("签发审批令牌失败: %w", err)
		}
		_, deliverErr := client.DeliverCardToUser(ctx, dingtalk.CardDeliverRequest{
			OutTrackID: outTrackID,
			UserID:     dingTalkID,
			Params:     params,
			// 私有数据只回给这一位用户，回调时作为第二重凭据
			PrivateParams: map[string]any{"approval_token": token},
		})
		if deliverErr != nil {
			// 卡片投递失败时退化为工作通知，保证审批人不至于完全收不到。
			// 这里必须显式留痕：降级成功时本函数返回 nil，调用方只会打一条"已投递"，
			// 运维在日志里看不出卡片其实没发出去（真机踩过：应用缺 Card.Instance.Write
			// 权限，卡片 403，审批人只收到工作通知，排查时毫无线索）。
			dingTalkLogw("[order-dingtalk] 卡片投递失败，降级为工作通知 order=%s approver=%s: %v",
				orderID, approver, deliverErr)
			recordOrderMessage(orderID, dingTalkChannelKind, "ORDER_PENDING_APPROVAL",
				[]string{approver}, "FAILED", deliverErr.Error(), token)
			if fallbackErr := client.SendWorkNotice(ctx, []string{dingTalkID},
				"工单待审批（卡片投递失败）", buildWorkNoticeText(record), linkURL); fallbackErr != nil {
				return fmt.Errorf("卡片投递失败：%v；工作通知降级同样失败：%v", deliverErr, fallbackErr)
			}
			dingTalkLogw("[order-dingtalk] 已按工作通知降级投递 order=%s approver=%s（卡片模式未生效）",
				orderID, approver)
			return nil
		}
		recordOrderMessage(orderID, dingTalkChannelKind, "ORDER_PENDING_APPROVAL",
			[]string{approver}, "SENT", fmt.Sprintf("card outTrackId=%s", outTrackID), token)
		return nil
	}

	// 未配置卡片模板：工作通知 + 平台审批页链接
	if err := global.App.DB.Create(&tokenRow).Error; err != nil {
		return fmt.Errorf("签发审批令牌失败: %w", err)
	}
	if err := client.SendWorkNotice(ctx, []string{dingTalkID},
		"有工单待您审批", buildWorkNoticeText(record), linkURL); err != nil {
		recordOrderMessage(orderID, dingTalkChannelKind, "ORDER_PENDING_APPROVAL",
			[]string{approver}, "FAILED", err.Error(), token)
		return fmt.Errorf("钉钉工作通知发送失败: %w", err)
	}
	recordOrderMessage(orderID, dingTalkChannelKind, "ORDER_PENDING_APPROVAL",
		[]string{approver}, "SENT", "work notice", token)
	return nil
}

// RevokeApprovalTokens 作废某工单下所有未消费的令牌（重新投递、转交、审批完成时调用）。
func RevokeApprovalTokens(orderID, reason string) error {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return nil
	}
	return global.App.DB.Model(&models.InsightOrderApprovalTokens{}).
		Where("order_id = ? AND status = ?", orderID, "ISSUED").
		Updates(map[string]any{
			"status":     "REVOKED",
			"remark":     truncateRemark(reason),
			"updated_at": time.Now(),
		}).Error
}

// ---------- 卡片内容 ----------

// cardParamsForOrder 组装互动卡片的模板变量。
//
// 变量名与渠道元数据里的说明一一对应，用户在钉钉卡片搭建器上按同名变量引用即可。
// 非字符串会被钉钉忽略，所以这里全部转成 string。
func cardParamsForOrder(record models.InsightOrderRecords, status string) map[string]string {
	return map[string]string{
		"title":   "SQL 工单审批 · " + record.Title,
		"content": buildWorkNoticeText(record),
		"status":  status,
		"link":    approvalLinkFor(record),
	}
}

func buildWorkNoticeText(record models.InsightOrderRecords) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "**工单标题**：%s\n", record.Title)
	fmt.Fprintf(&builder, "**申请人**：%s\n", record.Applicant)
	fmt.Fprintf(&builder, "**环境**：%s\n", record.Environment)
	fmt.Fprintf(&builder, "**工单类型**：%s\n", orderSQLTypeLabel(string(record.SQLType)))
	fmt.Fprintf(&builder, "**数据库类型**：%s\n", string(record.DBType))
	fmt.Fprintf(&builder, "**库名**：%s", record.Schema)
	return builder.String()
}

// approvalLinkFor 生成卡片/通知里的「查看详情」链接。
// 优先用渠道自己配的 notice_url，其次用平台通知配置里的前缀。
func approvalLinkFor(record models.InsightOrderRecords) string {
	base := ""
	if _, cfg, err := loadDingTalkChannel(); err == nil {
		if value, ok := cfg["notice_url"]; ok && value != nil {
			base = strings.TrimRight(strings.TrimSpace(fmt.Sprintf("%v", value)), "/")
		}
	}
	fromChannel := base != ""
	if !fromChannel {
		if noticeURL, err := notifier.LoadNoticeURL(); err == nil {
			base = strings.TrimRight(strings.TrimSpace(noticeURL), "/")
		}
	}
	path := fmt.Sprintf("/sql-workbench/orders?order_id=%s", record.OrderID)
	if base == "" {
		return path
	}
	// 兜底告警：平台默认的 notice_url 是从 app.listen_address 派生的，而监听地址常写成
	// 0.0.0.0:8083 —— 派生出来就是 http://0.0.0.0:8083，这个地址对人不可达，钉钉里点开
	// 只会报「无法访问」。这不是钉钉集成的问题，但会在卡片上直接暴露出来，所以在这里
	// 明确指路：要么在「通知渠道」里给钉钉企业应用填 notice_url，要么改平台通知设置。
	if isUnroutableHost(base) && !fromChannel {
		global.App.Log.Warnf("[order-dingtalk] 审批链接基址不可达（%s），"+
			"请在「钉钉企业应用」渠道中配置 notice_url，或修正平台通知设置里的回链地址", base)
	}
	return base + path
}

// isUnroutableHost 判断基址的主机部分是否是不可达的通配地址（0.0.0.0 / :: / 空）。
func isUnroutableHost(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return true
	}
	host := parsed.Hostname()
	if host == "" {
		// 只有路径（相对地址）时无法在钉钉里点开，同样视为不可达
		return true
	}
	return host == "0.0.0.0" || host == "::" || host == "[::]" || host == "*"
}

// ---------- 投递留痕 ----------

// recordOrderMessage 写一条投递记录（best-effort）。
//
// 这张表是回答「审批人说没收到通知」的唯一依据，所以失败也要落库，
// 但写记录本身失败不能影响主流程。
func recordOrderMessage(orderID, channel, msgType string, receivers []string, status, response, token string) {
	if global.App.DB == nil {
		return
	}
	encoded, err := json.Marshal(receivers)
	if err != nil {
		encoded = []byte("[]")
	}
	row := models.InsightOrderMessages{
		Model:    newOrderBaseModel(),
		OrderID:  orderID,
		Channel:  channel,
		MsgType:  msgType,
		Receiver: datatypes.JSON(encoded),
		Response: truncateRemark(response),
		Status:   status,
		DedupKey: fmt.Sprintf("%s|%s|%s", orderID, msgType, strings.Join(receivers, ",")),
	}
	if token != "" {
		row.DedupKey = fmt.Sprintf("%s|%s", row.DedupKey, token[:8])
	}
	if err := global.App.DB.Create(&row).Error; err != nil {
		global.App.Log.Warnf("[order-dingtalk] 写投递记录失败 order=%s: %v", orderID, err)
	}
}

// ---------- 卡片终态同步 ----------

// FinalizeApprovalCards 把该工单在钉钉里的卡片刷新为终态（异步、best-effort）。
//
// 只有「平台页面完成审批」这条路径需要它：钉钉内点按钮的那条路径，
// 回调本身就会带上卡片更新数据，钉钉会就地刷新。
// 异步执行的代价是可能的失败被吞掉，所以失败必须落日志——卡片状态不一致时，
// 这张日志是唯一的排查线索。
func FinalizeApprovalCards(orderID, statusText string) {
	defer func() {
		if r := recover(); r != nil {
			global.App.Log.Errorf("[order-dingtalk] 刷新卡片终态 panic order=%s: %v", orderID, r)
		}
	}()
	if global.App.DB == nil || strings.TrimSpace(orderID) == "" {
		return
	}
	var rows []models.InsightOrderApprovalTokens
	if err := global.App.DB.
		Where("order_id = ? AND out_track_id <> ''", orderID).
		Order("id desc").
		Limit(10).
		Find(&rows).Error; err != nil {
		return
	}
	if len(rows) == 0 {
		return
	}
	_, cfg, err := loadDingTalkChannel()
	if err != nil {
		return
	}
	client := notifier.DingTalkAppClientFromConfig(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), dingTalkNotifyTimeout)
	defer cancel()

	// 同一张卡片可能因重投留下多条记录，按 outTrackId 去重，避免重复请求
	seen := map[string]struct{}{}
	for _, row := range rows {
		if _, ok := seen[row.OutTrackID]; ok {
			continue
		}
		seen[row.OutTrackID] = struct{}{}
		if err := client.UpdateCard(ctx, dingtalk.UpdateCardRequest{
			OutTrackID: row.OutTrackID,
			Params:     map[string]string{"status": statusText},
		}); err != nil {
			global.App.Log.Warnf("[order-dingtalk] 刷新卡片状态失败 outTrackId=%s: %v", row.OutTrackID, err)
		}
	}
}

// ---------- 小工具 ----------

// newOrderBaseModel 显式初始化内嵌的 Model。
// ID/时间戳是嵌入指针的提升字段，Model 为 nil 时给它赋值会直接空指针 panic。
func newOrderBaseModel() *commonModels.Model { return &commonModels.Model{} }

// newApprovalToken 生成 32 字节随机令牌的十六进制串（64 字符）。
func newApprovalToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// 理论上不会发生；真发生时也不能退化成可预测的值，
		// 用当前纳秒时间与额外随机量混合兜底。
		return hex.EncodeToString([]byte(fmt.Sprintf("%d-%d-%d", time.Now().UnixNano(), time.Now().Unix(), randFallback())))
	}
	return hex.EncodeToString(buf)
}

func randFallback() int64 {
	var seed [8]byte
	_, _ = rand.Read(seed[:])
	return time.Now().UnixNano() ^ int64(seed[0])<<24
}

func truncateRemark(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= 255 {
		return string(runes)
	}
	return string(runes[:255])
}

// dingTalkLogw 包内 nil 安全的告警日志。日志组件在单测与极早期启动阶段可能尚未初始化，
// 直接取 global.App.Log 会空指针 panic（pkg/dingtalk 里同样有过这个坑）。
func dingTalkLogw(format string, args ...any) {
	if global.App.Log == nil {
		return
	}
	global.App.Log.Warnf(format, args...)
}
