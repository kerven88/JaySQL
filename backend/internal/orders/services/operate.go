package services

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	commonServices "github.com/simajiong/jaysql/internal/common/services"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/orders/forms"
	"github.com/simajiong/jaysql/internal/orders/models"
	usersSvc "github.com/simajiong/jaysql/internal/users/services"
	"gorm.io/gorm"
)

func containsOrderProgress(values []string, target string) bool {
	for _, item := range values {
		if item == target {
			return true
		}
	}
	return false
}

// checkOrderPreInspectBeforeSubmit 在提交审批前重新执行规范预审。
// 只要存在 SQL 语法错误，或预审结果包含必须修改项，就阻断提交，并给出明确原因。
// 同时把这次预审的结果落库存档：通过闸门之后，审批/执行/终态等阶段一律读这份快照，
// 不再重复执行任何规范检查（预审闸门唯一）。
func checkOrderPreInspectBeforeSubmit(c *gin.Context, username string, record models.InsightOrderRecords) error {
	if !supportsDeepInspect(string(record.DBType)) {
		return nil
	}

	svc := &PreInspectOrderService{
		InspectOrderSyntaxForm: &forms.InspectOrderSyntaxForm{
			DBType:     record.DBType,
			SQLType:    record.SQLType,
			InstanceID: record.InstanceID,
			Database:   record.Database,
			Schema:     record.Schema,
			Content:    record.Content,
		},
		C:        c,
		Username: username,
	}
	raw, err := svc.Run()
	if err != nil {
		return fmt.Errorf("SQL 预审失败，请先修正语法问题后再提交审批：%v", err)
	}

	// 归档这次预审结果（best-effort，失败只记日志，不阻断提交审批）
	PersistOrderPreInspectSnapshot(&record, raw)

	result, ok := raw.(map[string]any)
	if !ok {
		return nil
	}

	status, _ := result["status"].(string)
	if status == "must_fix" {
		mustCount, _ := result["must_count"].(int)
		var msgs []string
		// 实时预审返回的 items 是 []preInspectItem（结构体切片），
		// 此前按 []any 断言必然失败，导致「必须修改」的拦截原因里连一条明细都没有。
		for _, item := range preInspectItemsOf(result["items"]) {
			if item.Level != "must" || item.Message == "" {
				continue
			}
			msg := item.Message
			// 带上命中的规则条目，便于直接定位是哪条规则拦截；
			// 混合工单的语句冲突没有关联规则，直接展示结论本身。
			if item.RuleName != "" {
				msg = fmt.Sprintf("[%s] %s", item.RuleName, msg)
			} else if item.RuleKey != "" {
				msg = fmt.Sprintf("[%s] %s", item.RuleKey, msg)
			}
			msgs = append(msgs, msg)
			if len(msgs) >= 3 {
				break
			}
		}
		reason := "预审结果包含必须修改项，请先整改后再提交审批"
		if len(msgs) > 0 {
			reason += "：" + strings.Join(msgs, "；")
		}
		// 完整列出命中的规则条目（去重后最多 5 条），避免只看到前几条消息
		if ruleSummary := formatHitRules(result["hit_rules"], 5); ruleSummary != "" {
			reason += "（命中规则：" + ruleSummary + "）"
		}
		return fmt.Errorf("%s（共 %d 项）", reason, mustCount)
	}
	return nil
}

// formatHitRules 把预审命中的规则条目格式化为简要文本（最多 limit 条）
func formatHitRules(raw any, limit int) string {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		// 结果来自本次实时预审时是结构体切片；统一按 JSON 再解析一次，保持兼容
		encoded, err := json.Marshal(raw)
		if err != nil {
			return ""
		}
		if err := json.Unmarshal(encoded, &items); err != nil {
			return ""
		}
	}
	var names []string
	for _, it := range items {
		item, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if level, _ := item["level"].(string); level != "must" {
			continue
		}
		name, _ := item["name"].(string)
		ruleKey, _ := item["rule_key"].(string)
		if name == "" {
			name = ruleKey
		}
		if name == "" {
			continue
		}
		names = append(names, name)
		if len(names) >= limit {
			break
		}
	}
	return strings.Join(names, "、")
}

type SubmitApprovalService struct {
	*forms.SubmitApprovalForm
	C        *gin.Context
	Username string
}

func (s *SubmitApprovalService) Run() (err error) {
	// 生产环境此链路出现过 panic（前端表现为 HTTP 500「服务端错误」），
	// 这里捕获后把 panic 值与堆栈写入审计日志，便于直接在审计页面定位根因
	defer func() {
		if r := recover(); r != nil {
			stack := string(debug.Stack())
			if len(stack) > 1900 {
				stack = stack[:1900] + "...(truncated)"
			}
			global.App.Log.Errorf("submit-approval panic order=%s: %v\n%s", s.OrderID, r, stack)
			commonServices.TryWriteAuditLog(s.C, commonServices.AuditLogEvent{
				Username:       s.Username,
				AuditLevel:     "high",
				ModuleCode:     "db_order",
				ModuleName:     "数据库工单",
				ActionCode:     "submit_approval",
				ActionName:     "提交审批",
				ActionResult:   "fail",
				ResourceType:   "order",
				ResourceID:     s.OrderID,
				ResourceName:   s.OrderID,
				RelatedOrderID: s.OrderID,
				Summary:        "提交工单审批内部异常(panic)",
				ErrorMessage:   fmt.Sprintf("panic: %v\n%s", r, stack),
				RequestPayload: map[string]any{"order_id": s.OrderID, "approver": s.Approver},
			})
			err = fmt.Errorf("提交审批内部异常：%v（堆栈已记录到审计日志）", r)
		}
	}()
	var record models.InsightOrderRecords
	if err := global.App.DB.Where("order_id=?", s.OrderID).Take(&record).Error; err != nil {
		return fmt.Errorf("工单`%s`不存在", s.OrderID)
	}
	if record.Applicant != s.Username {
		return fmt.Errorf("只有提交人才能提交审批")
	}
	if !containsOrderProgress([]string{"WAIT_PRECHECK", "REJECTED"}, string(record.Progress)) {
		return fmt.Errorf("当前工单状态不允许提交审批")
	}
	var count int64
	if err := global.App.DB.Table("insight_users").
		Where("username = ? AND is_active = 1", s.Approver).
		Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("审批人不存在或未启用")
	}
	// 提交前重新执行规范预审：存在语法错误，或存在必须修改项时阻断提交
	if err := checkOrderPreInspectBeforeSubmit(s.C, s.Username, record); err != nil {
		return err
	}
	if err := global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("order_id=?", s.OrderID).Delete(&models.InsightApprovalRecords{}).Error; err != nil {
			return err
		}
		if err := tx.Create(&models.InsightApprovalRecords{
			OrderID:        record.OrderID,
			Stage:          1,
			StageName:      "人工审批",
			Approver:       s.Approver,
			ApprovalType:   "SINGLE",
			ApprovalStatus: "PENDING",
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.InsightOrderRecords{}).
			Where("order_id=?", s.OrderID).
			Updates(map[string]any{
				"progress": "PENDING",
				"stage":    1,
			}).Error; err != nil {
			return err
		}
		return WriteOrderLog(tx, s.OrderID, s.Username, fmt.Sprintf("用户%s提交工单审批，审批人为%s", s.Username, s.Approver))
	}); err != nil {
		return err
	}
	// 衔接点 1/3：提交人显式选择了钉钉通知时，把审批卡片定向发给审批人本人。
	// 失败不阻断提交 —— 通知失败只是少一条提醒，不该让"提交审批"这个动作失败
	// （工单状态已经落库，失败会让人以为没提交成功）。
	if s.wantsDingTalkNotify() {
		ctx, cancel := context.WithTimeout(context.Background(), dingTalkNotifyTimeout)
		if notifyErr := NotifyApproverByDingTalk(ctx, s.OrderID, s.Approver); notifyErr != nil {
			global.App.Log.Warnf("[order-dingtalk] 提交审批后钉钉通知失败 order=%s approver=%s: %v",
				s.OrderID, s.Approver, notifyErr)
		} else {
			global.App.Log.Infof("[order-dingtalk] 已向审批人 %s 投递钉钉审批通知 order=%s", s.Approver, s.OrderID)
		}
		cancel()
	}
	return nil
}

// wantsDingTalkNotify 提交人是否勾选了钉钉通知渠道。
func (s *SubmitApprovalService) wantsDingTalkNotify() bool {
	for _, channel := range s.NotifyChannels {
		if strings.EqualFold(strings.TrimSpace(channel), "dingtalk") {
			return true
		}
	}
	return false
}

// 审批
type ApprovalOrderService struct {
	*forms.ApprovalOrderForm
	C        *gin.Context
	Username string
}

func (s *ApprovalOrderService) Run() error {
	if s.Status == "REJECTED" && strings.TrimSpace(s.Msg) == "" {
		return fmt.Errorf("驳回必须填写原因")
	}
	var record models.InsightOrderRecords
	tx := global.App.DB.Table("`insight_order_records`").Where("order_id=?", s.OrderID).Take(&record)
	if tx.RowsAffected == 0 {
		return fmt.Errorf("工单`%s`不存在", s.OrderID)
	}
	if record.Progress != "PENDING" {
		return fmt.Errorf("当前工单不是审批中状态")
	}
	var approvalRecord models.InsightApprovalRecords
	if err := global.App.DB.Where("order_id=? AND stage=1", s.OrderID).Take(&approvalRecord).Error; err != nil {
		return fmt.Errorf("审批记录`%s`不存在", s.OrderID)
	}
	if approvalRecord.Approver != s.Username {
		return fmt.Errorf("您不是当前工单审批人")
	}
	if approvalRecord.ApprovalStatus != "PENDING" {
		return fmt.Errorf("当前工单已审批，请勿重复操作")
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	nextProgress := "APPROVED"
	action := "审批通过"
	if s.Status == "REJECTED" {
		nextProgress = "REJECTED"
		action = "审批驳回"
	}
	txErr := global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.InsightApprovalRecords{}).
			Where("id=?", approvalRecord.ID).
			Updates(map[string]any{
				"approval_status": s.Status,
				"msg":             s.Msg,
				"approval_at":     now,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.InsightOrderRecords{}).
			Where("order_id=?", s.OrderID).
			Update("progress", nextProgress).Error; err != nil {
			return err
		}
		return WriteOrderLog(tx, s.OrderID, s.Username, fmt.Sprintf("用户%s%s，附加消息：%s", s.Username, action, s.Msg))
	})

	if txErr != nil {
		return txErr
	}

	// 衔接点 3/3：从平台页面完成审批时，把钉钉里那张卡片同步刷成终态。
	// 不做这一步，审批人手上会留一张"还能点通过"的卡片，点下去只会得到一句
	// 「该工单已审批」—— 看起来像系统坏了。异步执行，不影响审批响应时间。
	finalizeCardText := "已通过"
	if s.Status == "REJECTED" {
		finalizeCardText = "已驳回"
	}
	go FinalizeApprovalCards(s.OrderID, finalizeCardText)
	return nil
}

// 转交
type TransferOrderService struct {
	*forms.TransferOrderForm
	C        *gin.Context
	Username string
}

func (s *TransferOrderService) Run() (err error) {
	var record models.InsightOrderRecords
	tx := global.App.DB.Table("`insight_order_records`").Where("order_id=?", s.OrderID).Take(&record)
	if tx.RowsAffected == 0 {
		return fmt.Errorf("工单`%s`不存在", s.OrderID)
	}
	if record.Progress != "PENDING" {
		return fmt.Errorf("当前仅审批中工单支持转交审批人")
	}
	var approvalRecord models.InsightApprovalRecords
	if err := global.App.DB.Where("order_id=? AND stage=1", s.OrderID).Take(&approvalRecord).Error; err != nil {
		return fmt.Errorf("审批记录不存在")
	}
	if approvalRecord.Approver != s.Username {
		return fmt.Errorf("只有当前审批人才能转交")
	}
	var count int64
	if err := global.App.DB.Table("insight_users").
		Where("username = ? AND is_active = 1", s.NewClaimer).
		Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("转交目标审批人不存在或未启用")
	}
	txErr := global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.InsightApprovalRecords{}).
			Where("id=?", approvalRecord.ID).
			Update("approver", s.NewClaimer).Error; err != nil {
			return err
		}
		if err := WriteOrderLog(tx, s.OrderID, s.Username, fmt.Sprintf("用户%s将审批人转交给%s，附加消息：%s", s.Username, s.NewClaimer, s.Msg)); err != nil {
			global.App.Log.Error("TransferOrderService.Run error:", err.Error())
			return err
		}
		return nil
	})

	if txErr != nil {
		return txErr
	}

	// 衔接点 2/3：平台页面上的转交，同样要对新审批人重投钉钉卡片。
	// 判定依据是"这个工单此前是否走过钉钉链路"——没走过的工单不该突然冒出一条钉钉通知。
	if orderUsesDingTalkApproval(s.OrderID) {
		ctx, cancel := context.WithTimeout(context.Background(), dingTalkNotifyTimeout)
		if notifyErr := NotifyApproverByDingTalk(ctx, s.OrderID, s.NewClaimer); notifyErr != nil {
			global.App.Log.Warnf("[order-dingtalk] 转交后向新审批人 %s 投递失败 order=%s: %v",
				s.NewClaimer, s.OrderID, notifyErr)
		}
		cancel()
	}

	return nil
}

// orderUsesDingTalkApproval 该工单是否走过钉钉审批链路（存在令牌记录即算）。
func orderUsesDingTalkApproval(orderID string) bool {
	if global.App.DB == nil || strings.TrimSpace(orderID) == "" {
		return false
	}
	var count int64
	if err := global.App.DB.Model(&models.InsightOrderApprovalTokens{}).
		Where("order_id = ?", orderID).
		Count(&count).Error; err != nil {
		return false
	}
	return count > 0
}

// 撤销工单
type RevokeOrderService struct {
	*forms.RevokeOrderForm
	C        *gin.Context
	Username string
}

func (s *RevokeOrderService) Run() (err error) {
	var record models.InsightOrderRecords
	tx := global.App.DB.Table("`insight_order_records`").Where("order_id=?", s.OrderID).Take(&record)
	if tx.RowsAffected == 0 {
		return fmt.Errorf("记录`%s`不存在", s.OrderID)
	}
	if record.Applicant != s.Username {
		return fmt.Errorf("只有工单申请人才能撤销工单")
	}
	if !containsOrderProgress([]string{"WAIT_PRECHECK", "PENDING", "APPROVED", "FAILED", "REJECTED"}, string(record.Progress)) {
		return fmt.Errorf("非可操作状态，禁止操作")
	}
	txErr := global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.InsightOrderRecords{}).
			Where("order_id=?", s.OrderID).
			Updates(map[string]any{
				"progress": "TERMINATED",
			}).Error; err != nil {
			return err
		}
		if err := WriteOrderLog(tx, s.OrderID, s.Username, fmt.Sprintf("用户%s撤销了工单，附加消息：%s", s.Username, s.Msg)); err != nil {
			global.App.Log.Error("RevokeOrderService.Run error:", err.Error())
			return err
		}
		return nil
	})

	if txErr != nil {
		return txErr
	}

	return nil
}

// WithdrawOrderService 撤回工单：与「撤销/终止」不同，撤回不终结工单，
// 而是进入「已撤回」状态——任务不再执行，申请人可编辑内容后重新提交（工单 ID 保持不变）。
type WithdrawOrderService struct {
	*forms.WithdrawOrderForm
	C        *gin.Context
	Username string
}

func (s *WithdrawOrderService) Run() (err error) {
	var record models.InsightOrderRecords
	tx := global.App.DB.Table("`insight_order_records`").Where("order_id=?", s.OrderID).Take(&record)
	if tx.RowsAffected == 0 {
		return fmt.Errorf("记录`%s`不存在", s.OrderID)
	}
	if record.Applicant != s.Username {
		return fmt.Errorf("只有工单申请人才能撤回工单")
	}
	// 可撤回阶段 = 任务尚未开始执行：待预审 / 审批中 / 审批通过
	if !containsOrderProgress([]string{"WAIT_PRECHECK", "PENDING", "APPROVED"}, string(record.Progress)) {
		return fmt.Errorf("当前工单已进入执行或终态，无法撤回")
	}
	txErr := global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.InsightOrderRecords{}).
			Where("order_id=? AND progress IN (?)", s.OrderID, []string{"WAIT_PRECHECK", "PENDING", "APPROVED"}).
			Updates(map[string]any{
				"progress": "WITHDRAWN",
			}).Error; err != nil {
			return err
		}
		msg := strings.TrimSpace(s.Msg)
		if msg == "" {
			msg = "任务不再执行，支持编辑再次提交"
		}
		// 撤回即作废旧预审结论：重新提交是一次全新校验，
		// 必须按当前生效的规则全量重跑，不得沿用撤回前的快照。
		InvalidateOrderPreInspectSnapshot(s.OrderID, tx)
		// 撤回后不再执行：撤掉可能已登记的定时触发点，避免到点白白认领一次
		CancelScheduledPlan(s.OrderID)
		if err := WriteOrderLog(tx, s.OrderID, s.Username, fmt.Sprintf("用户%s撤回了工单，%s", s.Username, msg)); err != nil {
			global.App.Log.Error("WithdrawOrderService.Run error:", err.Error())
			return err
		}
		return nil
	})
	if txErr != nil {
		return txErr
	}

	return nil
}

// UpdateOrderContentService 编辑已撤回工单：保存后回到「待预审」，可重新提交审批；工单 ID 不变。
type UpdateOrderContentService struct {
	*forms.UpdateOrderContentForm
	C        *gin.Context
	Username string
}

// validateEditExecutor 编辑工单时校验执行人：必须是存在且启用的用户（与创建同口径）。
func validateEditExecutor(executor string) error {
	var count int64
	if err := global.App.DB.Table("insight_users").
		Where("username = ? AND is_active = 1", executor).
		Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("执行人不存在或未启用")
	}
	return nil
}

func (s *UpdateOrderContentService) Run() (err error) {
	var record models.InsightOrderRecords
	tx := global.App.DB.Table("`insight_order_records`").Where("order_id=?", s.OrderID).Take(&record)
	if tx.RowsAffected == 0 {
		return fmt.Errorf("记录`%s`不存在", s.OrderID)
	}
	if record.Applicant != s.Username {
		return fmt.Errorf("只有工单申请人才能编辑工单")
	}
	if string(record.Progress) != "WITHDRAWN" {
		return fmt.Errorf("仅「已撤回」状态的工单支持编辑")
	}

	// 内容字段始终允许编辑；目标数据源/库/执行人只有在前端显式传了 instance_id
	// 时才按创建同口径重新校验并落库，否则维持原值（老客户端不传这些字段，行为不变）。
	updates := map[string]any{
		"title":                   strings.TrimSpace(s.Title),
		"remark":                  s.Remark,
		"content":                 s.Content,
		"rollback_sql":            s.RollbackSQL,
		"estimated_affected_rows": s.EstimatedAffectedRows,
	}

	// 改写后的内容可能同时换了目标资源，SQL 类型必须按新内容重新推断，
	// 否则执行链路按旧类型派发（DDL/DML 的审批与执行通道不同）。
	sqlType, err := inferOrderSQLType(s.Content, string(record.DBType))
	if err != nil {
		return err
	}
	if string(record.SQLType) != string(sqlType) {
		updates["sql_type"] = sqlType
		record.SQLType = sqlType
	}

	if strings.TrimSpace(s.InstanceID) != "" {
		// 数据源校验与创建同口径：实例必须存在，db_type 以实例配置为准。
		var config commonModels.InsightInstances
		global.App.DB.Table("`insight_instances`").
			Where("instance_id=?", strings.TrimSpace(s.InstanceID)).
			Take(&config)
		if config.InstanceID == "" {
			return fmt.Errorf("未找到对应数据源")
		}
		targetMode, err := detectOrderTargetMode(s.Content, string(config.DbType))
		if err != nil {
			return err
		}
		database := strings.TrimSpace(s.Database)
		schema := strings.TrimSpace(s.Schema)
		// 换了数据源只需按新目标重算口径，不再复核数据授权：
		// 变更 / 导出由工单审批覆盖（见 das/services/access_action_scope.go）。
		if targetMode == orderTargetNone {
			database = ""
			schema = ""
		} else if !isPgLikeDB(string(config.DbType)) {
			// MySQL 类实例只维护库字段，schema 与 database 对齐（同创建口径）
			schema = database
		}

		updates["db_type"] = config.DbType
		updates["instance_id"] = strings.TrimSpace(s.InstanceID)
		updates["database"] = database
		updates["schema"] = schema
		record.DBType = config.DbType
		record.InstanceID = strings.TrimSpace(s.InstanceID)
		record.Database = database
		record.Schema = schema
	}

	if executor := strings.TrimSpace(s.Executor); executor != "" && executor != record.Executor {
		if err := validateEditExecutor(executor); err != nil {
			return err
		}
		updates["executor"] = executor
		record.Executor = executor
	}

	updates["progress"] = "WAIT_PRECHECK"
	txErr := global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.InsightOrderRecords{}).
			Where("order_id=? AND progress='WITHDRAWN'", s.OrderID).
			Updates(updates).Error; err != nil {
			return err
		}
		// 重新进入待预审 = 全新校验：作废旧快照，避免复用撤回前的历史结论
		InvalidateOrderPreInspectSnapshot(s.OrderID, tx)
		logParts := []string{fmt.Sprintf("用户%s编辑了工单内容，工单重新进入待预审", s.Username)}
		if _, changed := updates["instance_id"]; changed {
			logParts = append(logParts, fmt.Sprintf("目标数据源变更为 %s（数据库：%s）", record.InstanceID, record.Database))
		}
		if _, changed := updates["executor"]; changed {
			logParts = append(logParts, fmt.Sprintf("执行人变更为 %s", record.Executor))
		}
		if err := WriteOrderLog(tx, s.OrderID, s.Username, strings.Join(logParts, "；")); err != nil {
			global.App.Log.Error("UpdateOrderContentService.Run error:", err.Error())
			return err
		}
		return nil
	})
	return txErr
}

type CompleteOrderService struct {
	*forms.CompleteOrderForm
	C        *gin.Context
	Username string
}

func (s *CompleteOrderService) Run() (err error) {
	var record models.InsightOrderRecords
	tx := global.App.DB.Table("`insight_order_records`").Where("order_id=?", s.OrderID).Take(&record)
	if tx.RowsAffected == 0 {
		return fmt.Errorf("记录`%s`不存在", s.OrderID)
	}
	if !containsOrderProgress([]string{"APPROVED", "EXECUTING", "FAILED"}, string(record.Progress)) {
		return fmt.Errorf("非可操作状态，禁止操作")
	}
	if record.Executor != s.Username {
		return fmt.Errorf("只有执行人才能更改工单状态")
	}
	txErr := global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.InsightOrderRecords{}).
			Where("order_id=?", s.OrderID).
			Updates(map[string]any{
				"progress":    "COMPLETED",
				"executed_at": time.Now().Format("2006-01-02 15:04:05"),
			}).Error; err != nil {
			global.App.Log.Error(err)
			return err
		}
		if err := WriteOrderLog(tx, s.OrderID, s.Username, fmt.Sprintf("用户%s更改工单状态为已完成，附加消息：%s", s.Username, s.Msg)); err != nil {
			global.App.Log.Error("CompleteOrderService.Run error:", err.Error())
			return err
		}
		return nil
	})

	if txErr != nil {
		return txErr
	}

	return nil
}

// 手动更新工单为失败
type FailOrderService struct {
	*forms.FailOrderForm
	C        *gin.Context
	Username string
}

func (s *FailOrderService) Run() (err error) {
	var record models.InsightOrderRecords
	tx := global.App.DB.Table("`insight_order_records`").Where("order_id=?", s.OrderID).Take(&record)
	if tx.RowsAffected == 0 {
		return fmt.Errorf("记录`%s`不存在", s.OrderID)
	}
	if !containsOrderProgress([]string{"APPROVED", "EXECUTING"}, string(record.Progress)) {
		return fmt.Errorf("非可操作状态，禁止操作")
	}
	if record.Executor != s.Username {
		return fmt.Errorf("只有执行人才能更改工单状态")
	}
	txErr := global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.InsightOrderRecords{}).
			Where("order_id=?", s.OrderID).
			Updates(map[string]any{"progress": "FAILED"}).Error; err != nil {
			global.App.Log.Error(err)
			return err
		}
		// 记录操作日志
		if err := WriteOrderLog(tx, s.OrderID, s.Username, fmt.Sprintf("用户%s更改工单状态为已失败，附加消息：%s", s.Username, s.Msg)); err != nil {
			global.App.Log.Error("FailOrderService.Run error:", err.Error())
			return err
		}
		return nil
	})

	if txErr != nil {
		return txErr
	}

	return nil
}

// 复核
type ReviewOrderService struct {
	*forms.ReviewOrderForm
	C        *gin.Context
	Username string
}

func (s *ReviewOrderService) Run() (err error) {
	return fmt.Errorf("当前版本已取消复核节点")
}

type TerminateOrderService struct {
	*forms.TerminateOrderForm
	C        *gin.Context
	Username string
}

func (s *TerminateOrderService) Run() error {
	var record models.InsightOrderRecords
	if err := global.App.DB.Where("order_id=?", s.OrderID).Take(&record).Error; err != nil {
		return fmt.Errorf("记录`%s`不存在", s.OrderID)
	}
	allowed := record.Applicant == s.Username || record.Executor == s.Username
	if !allowed {
		var approvalRecord models.InsightApprovalRecords
		if err := global.App.DB.Where("order_id=? AND stage=1", s.OrderID).Take(&approvalRecord).Error; err == nil {
			allowed = approvalRecord.Approver == s.Username
		}
	}
	if !allowed {
		return fmt.Errorf("只有提交人、审批人或执行人才能终止工单")
	}
	if !containsOrderProgress([]string{"WAIT_PRECHECK", "PENDING", "APPROVED", "EXECUTING", "FAILED", "REJECTED", "WITHDRAWN"}, string(record.Progress)) {
		return fmt.Errorf("当前工单状态不允许终止")
	}
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.InsightOrderRecords{}).
			Where("order_id=?", s.OrderID).
			Update("progress", "TERMINATED").Error; err != nil {
			return err
		}
		return WriteOrderLog(tx, s.OrderID, s.Username, fmt.Sprintf("用户%s终止了工单，附加消息：%s", s.Username, s.Msg))
	})
}

type UpdateOrderTitleService struct {
	*forms.UpdateOrderTitleForm
	C        *gin.Context
	Username string
}

// 修改工单名称：仅申请人本人或超级管理员可改，任意状态允许（改名不影响流程）
func (s *UpdateOrderTitleService) Run() error {
	var record models.InsightOrderRecords
	tx := global.App.DB.Table("`insight_order_records`").Where("order_id=?", s.OrderID).Take(&record)
	if tx.RowsAffected == 0 {
		return fmt.Errorf("记录`%s`不存在", s.OrderID)
	}
	if record.Applicant != s.Username {
		if !usersSvc.IsSuperuserByUsername(s.Username) {
			return fmt.Errorf("只有工单申请人才能修改工单名称")
		}
	}
	newTitle := strings.TrimSpace(s.Title)
	if newTitle == "" {
		return fmt.Errorf("工单名称不能为空")
	}
	if err := global.App.DB.Model(&models.InsightOrderRecords{}).
		Where("order_id=?", s.OrderID).
		Update("title", newTitle).Error; err != nil {
		global.App.Log.Error("UpdateOrderTitleService.Run error:", err.Error())
		return err
	}
	if err := WriteOrderLog(global.App.DB, s.OrderID, s.Username, fmt.Sprintf("用户%s修改工单名称：%s → %s", s.Username, record.Title, newTitle)); err != nil {
		global.App.Log.Error("UpdateOrderTitleService write log error:", err.Error())
	}
	return nil
}
