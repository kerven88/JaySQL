package services

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	inspectServices "github.com/simajiong/jaysql/internal/inspect/services"
	"github.com/simajiong/jaysql/internal/orders/forms"
	"github.com/simajiong/jaysql/internal/orders/models"

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// 规范预审快照（历史态）说明
//
// 规范预审是「实时检查」：部分规则（如 ALTER ADD COLUMN 的列重复判定）会即时读取
// 目标库的表结构。工单一旦执行成功，DDL 已落库，再重跑预审就会把「刚刚加上的列」
// 判成重复列，从而在成功工单上出现「必须改进」的事后误报。
//
// 因此预审结果以「快照」形式落库。预审只在两个时点执行，通过闸门之后全流程不再走规范：
//   - 工单创建成功后生成一次（待预审页面展示用）；
//   - 「提交审批」闸门处实时预审一次，结果直接落库（此后审批/执行/终态一律读这份快照）；
//   - 「待预审 / 被驳回」阶段允许按需重建（SQL 内容变化或上次生成失败时）；
//   - 存量工单（快照机制上线前创建）无快照时回填一次：
//       · 执行前阶段（审批中/审批通过/执行中）：DDL 未执行，当前表结构仍等于执行前结构，结果有效；
//       · 终态（执行成功/执行失败/已终止）：表结构已被本工单 DDL 改变，回填时排除
//         「由本工单执行造成」的事后误报项（如执行成功工单的 ADD COLUMN 被判成重复列）。

// 允许重新生成预审快照的工单进度：与详情页「可编辑态」（可提交审批/重新检查）保持一致
var orderPreInspectEditableProgress = []string{"WAIT_PRECHECK", "REJECTED"}

// 已进入执行终态的工单进度：执行成功 / 执行失败 / 已终止。
// 这些工单的表结构已被本工单 DDL 改变（或执行已中止），任何重跑/回填都只会产生事后误报：
// 因此终态工单绝不重跑、绝不回填，且回填产生的快照（backfilled=true）不作为「执行前状况」展示——
// 终态工单的【规范预审】只反映提交时刻（执行前）生成的快照。
var orderPreInspectFinalProgress = []string{"COMPLETED", "FAILED", "TERMINATED"}

// IsOrderPreInspectEditable 判断工单当前是否处于可重新预审的阶段
func IsOrderPreInspectEditable(progress string) bool {
	current := strings.ToUpper(strings.TrimSpace(progress))
	for _, item := range orderPreInspectEditableProgress {
		if current == item {
			return true
		}
	}
	return false
}

// IsOrderPreInspectFinal 判断工单是否已进入执行终态（执行成功/执行失败/已终止）
func IsOrderPreInspectFinal(progress string) bool {
	current := strings.ToUpper(strings.TrimSpace(progress))
	for _, item := range orderPreInspectFinalProgress {
		if current == item {
			return true
		}
	}
	return false
}

// orderPreInspectContentHash 计算工单 SQL 内容哈希，用于判断已有快照是否仍然对应当前 SQL
func orderPreInspectContentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// loadOrderPreInspectSnapshot 读取工单快照，不存在返回 (nil, nil)
func loadOrderPreInspectSnapshot(orderID string) (*models.InsightOrderPreInspections, error) {
	var record models.InsightOrderPreInspections
	err := global.App.DB.Where("order_id = ?", orderID).Take(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// upsertOrderPreInspectSnapshot 一单一快照：已存在则覆盖
func upsertOrderPreInspectSnapshot(record *models.InsightOrderPreInspections) error {
	var existing models.InsightOrderPreInspections
	err := global.App.DB.Where("order_id = ?", record.OrderID).Take(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return global.App.DB.Create(record).Error
	}
	if err != nil {
		return err
	}
	updates := map[string]any{
		"content_hash": record.ContentHash,
		"order_stage":  record.OrderStage,
		"status":       record.Status,
		"message":      record.Message,
		"result":       record.Result,
		"backfilled":   record.Backfilled,
		"rule_version": record.RuleVersion,
	}
	if err := global.App.DB.Model(&models.InsightOrderPreInspections{}).
		Where("id = ?", existing.ID).
		Updates(updates).Error; err != nil {
		return err
	}
	// record 可能由调用方手构造（未初始化嵌入 *Model），直接写 record.ID 会 nil 解引用
	if record.Model == nil {
		record.Model = &commonModels.Model{}
	}
	record.ID = existing.ID
	return nil
}

// generateOrderPreInspectSnapshot 以工单当前内容实时执行一次预审并覆盖落库。
// 预审执行失败时同样落一条 failed 快照（保留失败原因），便于页面区分
// 「从未生成过快照」与「预审执行失败、可重试」。
//   - backfilled=true：这不是提交时刻的快照，而是快照机制上线后对存量工单的回填；
//   - reconcile=true：回填发生在工单已执行之后，需要排除「由本工单执行造成」的事后误报项。
func generateOrderPreInspectSnapshot(order *models.InsightOrderRecords, c *gin.Context, backfilled bool, reconcile bool) (*models.InsightOrderPreInspections, error) {
	record := &models.InsightOrderPreInspections{
		Model:       &commonModels.Model{},
		OrderID:     order.OrderID,
		ContentHash: orderPreInspectContentHash(order.Content),
		OrderStage:  strings.ToUpper(strings.TrimSpace(string(order.Progress))),
		Backfilled:  backfilled,
	}

	// 以工单申请人身份执行：预审结果属于该工单，不应因查看者（如审批人/执行人）的
	// 数据源权限差异而失败。
	service := PreInspectOrderService{
		InspectOrderSyntaxForm: &forms.InspectOrderSyntaxForm{
			DBType:     order.DBType,
			SQLType:    order.SQLType,
			InstanceID: order.InstanceID,
			Database:   order.Database,
			Schema:     order.Schema,
			Content:    order.Content,
		},
		C:        c,
		Username: order.Applicant,
	}

	result, inspectErr := service.Run()
	switch {
	case inspectErr != nil:
		record.Status = "failed"
		record.Message = inspectErr.Error()
	default:
		payload, err := marshalPreInspectResult(result)
		if err != nil {
			record.Status = "failed"
			record.Message = err.Error()
			break
		}
		if reconcile {
			removed := reconcileSelfCausedItems(order, payload)
			payload["reconciled"] = true
			payload["reconciled_count"] = removed
			payload["backfill_phase"] = preInspectBackfillPhasePostExec
		} else if backfilled {
			payload["backfill_phase"] = preInspectBackfillPhasePreExec
		}
		// 记录本次预审所用的规则版本：与审核执行同源（由 PreInspectOrderService 产出），
		// 回写时同时落到专用列，供下次判断快照是否仍对应当前生效规则
		record.RuleVersion = preInspectRuleVersionOf(payload)
		body, err := json.Marshal(payload)
		if err != nil {
			record.Status = "failed"
			record.Message = err.Error()
			break
		}
		record.Status = "success"
		record.Result = datatypes.JSON(body)
	}

	if err := upsertOrderPreInspectSnapshot(record); err != nil {
		global.App.Log.Warnf("save order pre-inspect snapshot failed: %v, order_id: %s", err, order.OrderID)
	}
	return record, inspectErr
}

// 快照来源标记（写入快照 result JSON，读取逻辑据此判断是否需要重新回填）
const (
	// 执行前阶段回填：DDL 未执行，当前表结构等同于执行前结构
	preInspectBackfillPhasePreExec = "pre_exec"
	// 终态回填：DDL 已执行，已排除由本工单执行造成的事后误报
	preInspectBackfillPhasePostExec = "post_exec"
)

// marshalPreInspectResult 把预审结果体转成可增删字段的 map（结果体是内部结构体切片）
func marshalPreInspectResult(result any) (map[string]any, error) {
	body, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// PersistOrderPreInspectSnapshot 将一次已算出的预审结果直接落库（不再重复执行预审）。
// 用于「提交审批」闸门：闸门本身就会实时预审一次，这里复用同一份结果存档，
// 保证通过闸门之后的审批/执行/终态阶段都能读到执行前的预审结果。任何失败只记日志。
func PersistOrderPreInspectSnapshot(order *models.InsightOrderRecords, result any) {
	if order == nil || strings.TrimSpace(order.OrderID) == "" || strings.TrimSpace(order.Content) == "" {
		return
	}
	payload, err := marshalPreInspectResult(result)
	if err != nil {
		global.App.Log.Warnf("规范预审结果落库失败（不影响提交审批）: %v, order_id: %s", err, order.OrderID)
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		global.App.Log.Warnf("规范预审结果落库失败（不影响提交审批）: %v, order_id: %s", err, order.OrderID)
		return
	}
	record := &models.InsightOrderPreInspections{
		Model:       &commonModels.Model{},
		OrderID:     order.OrderID,
		ContentHash: orderPreInspectContentHash(order.Content),
		OrderStage:  strings.ToUpper(strings.TrimSpace(string(order.Progress))),
		Status:      "success",
		Result:      datatypes.JSON(body),
		RuleVersion: preInspectRuleVersionOf(payload),
	}
	if err := upsertOrderPreInspectSnapshot(record); err != nil {
		global.App.Log.Warnf("规范预审结果落库失败（不影响提交审批）: %v, order_id: %s", err, order.OrderID)
	}
}

// preInspectRuleVersionOf 从预审结果中取出本次所用的规则版本
func preInspectRuleVersionOf(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	version, _ := payload["rule_version"].(string)
	return strings.TrimSpace(version)
}

// InvalidateOrderPreInspectSnapshot 作废旧预审结论（直接删除快照行）。
//
// 用于「撤回工单」「编辑已撤回工单」：撤回后的重新提交视为一次全新校验，
// 旧结论全部作废，下次查看或提交时必须按当前生效规则全量重跑。
// tx 允许传入事务句柄，使作废与工单状态变更在同一事务里完成。
func InvalidateOrderPreInspectSnapshot(orderID string, tx *gorm.DB) {
	db := tx
	if db == nil {
		db = global.App.DB
	}
	if db == nil || strings.TrimSpace(orderID) == "" {
		return
	}
	if err := db.Where("order_id = ?", strings.TrimSpace(orderID)).
		Delete(&models.InsightOrderPreInspections{}).Error; err != nil {
		global.App.Log.Warnf("invalidate order pre-inspect snapshot failed: %v, order_id: %s", err, orderID)
	}
}

// SaveOrderPreInspectSnapshotQuietly 创建工单后生成快照的入口：任何失败只记录日志，不阻断主流程
func SaveOrderPreInspectSnapshotQuietly(order *models.InsightOrderRecords, c *gin.Context) {
	if order == nil || strings.TrimSpace(order.OrderID) == "" || strings.TrimSpace(order.Content) == "" {
		return
	}
	if _, err := generateOrderPreInspectSnapshot(order, c, false, false); err != nil {
		global.App.Log.Warnf("生成工单规范预审快照失败（不影响工单创建）: %v, order_id: %s", err, order.OrderID)
	}
}

// reconcileSelfCausedItems 排除「由本工单执行本身造成」的事后误报项，返回被排除的条数。
//
// 判定依据（仅对执行成功的工单成立）：MySQL 上 ADD COLUMN / ADD INDEX / CREATE 的对象若已存在
// 必然报错，工单既然执行成功，就说明这些对象是本工单新增的；回填预审时它们已经落在表结构里，
// 因此任何「重复 / 已存在」类判定，只要指向本工单引入的对象，都是本工单自身造成的误报。
func reconcileSelfCausedItems(order *models.InsightOrderRecords, payload map[string]any) int {
	if !strings.EqualFold(strings.TrimSpace(string(order.Progress)), "COMPLETED") {
		return 0
	}
	selfObjects := selfIntroducedObjects(order.Content)
	if len(selfObjects) == 0 {
		return 0
	}
	items, ok := payload["items"].([]any)
	if !ok || len(items) == 0 {
		return 0
	}

	kept := make([]any, 0, len(items))
	removed := 0
	for _, raw := range items {
		if item, ok := raw.(map[string]any); ok {
			message, _ := item["message"].(string)
			if isSelfCausedConflict(message, selfObjects) {
				removed++
				continue
			}
		}
		kept = append(kept, raw)
	}
	if removed == 0 {
		return 0
	}

	payload["items"] = kept
	mustCount, suggestCount := 0, 0
	for _, raw := range kept {
		if item, ok := raw.(map[string]any); ok {
			if item["level"] == "must" {
				mustCount++
			} else {
				suggestCount++
			}
		}
	}
	payload["must_count"] = mustCount
	payload["suggest_count"] = suggestCount
	switch {
	case mustCount > 0:
		payload["status"] = "must_fix"
	case suggestCount > 0:
		payload["status"] = "suggest"
	default:
		payload["status"] = "passed"
	}
	return removed
}

var (
	// ADD [COLUMN|UNIQUE|FULLTEXT|SPATIAL] name
	selfAddObjectRe = regexp.MustCompile("(?i)add\\s+(?:(?:column|unique|fulltext|spatial)\\s+)?`?([a-z0-9_$]+)`?")
	// ADD [UNIQUE|FULLTEXT|SPATIAL] INDEX|KEY name
	selfAddIndexRe = regexp.MustCompile("(?i)add\\s+(?:(?:unique|fulltext|spatial)\\s+)?(?:index|key)\\s+`?([a-z0-9_$]+)`?")
	// CREATE TABLE|INDEX|DATABASE|SCHEMA|VIEW [IF NOT EXISTS] name
	selfCreateObjectRe = regexp.MustCompile("(?i)create\\s+(?:table|index|database|schema|view)\\s+(?:if\\s+not\\s+exists\\s+)?`?([a-z0-9_$]+)`?")
	// 这些是 SQL 关键字，被正则误捕时忽略
	selfObjectKeywords = map[string]bool{
		"column": true, "index": true, "key": true, "constraint": true, "primary": true,
		"foreign": true, "unique": true, "check": true, "partition": true,
		"fulltext": true, "spatial": true, "if": true, "not": true, "exists": true,
	}
)

// selfIntroducedObjects 从工单 SQL 中提取「本工单新引入的对象名」（新增列/索引/表等）
func selfIntroducedObjects(content string) map[string]bool {
	objects := map[string]bool{}
	for _, re := range []*regexp.Regexp{selfAddIndexRe, selfAddObjectRe, selfCreateObjectRe} {
		for _, match := range re.FindAllStringSubmatch(content, -1) {
			if len(match) < 2 {
				continue
			}
			name := strings.ToLower(strings.TrimSpace(match[1]))
			if name == "" || selfObjectKeywords[name] {
				continue
			}
			objects[name] = true
		}
	}
	return objects
}

// backtickTokens 提取文本中被反引号包裹的标识符（小写）
func backtickTokens(text string) []string {
	var tokens []string
	for i := 0; i < len(text); i++ {
		if text[i] != '`' {
			continue
		}
		offset := strings.IndexByte(text[i+1:], '`')
		if offset < 0 {
			break
		}
		token := strings.ToLower(strings.TrimSpace(text[i+1 : i+1+offset]))
		if token != "" {
			tokens = append(tokens, token)
		}
		i = i + 1 + offset
	}
	return tokens
}

// isSelfCausedConflict 判断一条「重复 / 已存在」类预审项是否由本工单引入的对象引起
func isSelfCausedConflict(message string, selfObjects map[string]bool) bool {
	message = strings.TrimSpace(message)
	if message == "" {
		return false
	}
	if !strings.Contains(message, "重复") && !strings.Contains(message, "已存在") {
		return false
	}
	// 去掉「（表`xxx`）」这类表名后缀，只保留冲突对象本身，避免表名干扰判定
	head := message
	if idx := strings.Index(head, "（表"); idx > 0 {
		head = head[:idx]
	}
	tokens := backtickTokens(head)
	if len(tokens) == 0 {
		return false
	}
	for _, token := range tokens {
		if selfObjects[token] {
			return true
		}
	}
	return false
}

// buildOrderPreInspectSnapshotResponse 组装响应：
// 快照的结果体（status/must_count/suggest_count/items）平铺在顶层，保持与实时预审接口一致的结构，
// 额外补充快照元信息（snapshot/snapshot_status/generated_at/outdated/editable/message）。
func buildOrderPreInspectSnapshotResponse(
	order *models.InsightOrderRecords,
	snapshot *models.InsightOrderPreInspections,
	contentHash string,
	currentRuleVersion string,
) map[string]any {
	progress := strings.ToUpper(strings.TrimSpace(string(order.Progress)))
	response := map[string]any{
		"order_id":        order.OrderID,
		"progress":        progress,
		"editable":        IsOrderPreInspectEditable(progress),
		"snapshot":        snapshot != nil,
		"snapshot_status": "",
		"snapshot_stage":  "",
		"generated_at":    nil,
		"backfilled":      false,
		"outdated":        false,
		"status":          "passed",
		"must_count":      0,
		"suggest_count":   0,
		"items":           []any{},
		"message":         "",
		// 规则版本信息：快照所用版本 / 当前生效版本 / 快照是否已过期
		"snapshot_rule_version": "",
		"current_rule_version":  currentRuleVersion,
		"rule_version_stale":    false,
	}

	if snapshot == nil {
		if IsOrderPreInspectFinal(progress) {
			response["message"] = "该工单未生成规范预审快照，且无法回填（工单 SQL 内容为空）"
		} else {
			response["message"] = "该工单未生成规范预审快照"
		}
		return response
	}

	response["snapshot_status"] = snapshot.Status
	response["snapshot_stage"] = snapshot.OrderStage
	response["generated_at"] = snapshot.UpdatedAt
	response["backfilled"] = snapshot.Backfilled
	response["outdated"] = snapshot.ContentHash != contentHash
	response["snapshot_rule_version"] = snapshot.RuleVersion
	response["rule_version_stale"] = !snapshotRuleVersionIsCurrent(snapshot, currentRuleVersion)

	if snapshot.Status != "success" {
		response["message"] = snapshot.Message
		return response
	}
	if len(snapshot.Result) > 0 {
		var payload map[string]any
		if err := json.Unmarshal(snapshot.Result, &payload); err == nil {
			for key, value := range payload {
				response[key] = value
			}
		}
	}
	return response
}

// 读取工单规范预审快照（详情页专用）
// 规则：
//  1. 「待预审 / 被驳回」阶段且满足下列任一条件时，重新生成并覆盖快照：
//     快照不存在、SQL 内容已变化、上一次生成失败；
//  2. 「审批中 / 审批通过 / 执行中」阶段只读快照；存量工单（快照机制上线前创建）
//     没有快照时回填一次——DDL 尚未执行，当前表结构仍等于执行前结构，结果有效；
//  3. 终态（执行成功/执行失败/已终止）同样只读快照；存量工单没有快照时，
//     以「自因过滤」方式回填一次（排除由本工单执行造成的事后误报，如执行成功工单的
//     ADD COLUMN 被判成重复列），使已执行工单也能展示执行前的规范预审信息。
type GetOrderPreInspectSnapshotService struct {
	OrderID  string
	Username string
	C        *gin.Context
}

func (s *GetOrderPreInspectSnapshotService) Run() (any, error) {
	orderID := strings.TrimSpace(s.OrderID)
	if orderID == "" {
		return nil, fmt.Errorf("工单ID不能为空")
	}

	var order models.InsightOrderRecords
	if err := global.App.DB.Where("order_id = ?", orderID).Take(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("未找到工单 %s", orderID)
		}
		return nil, err
	}

	snapshot, err := loadOrderPreInspectSnapshot(order.OrderID)
	if err != nil {
		return nil, err
	}

	progress := string(order.Progress)
	contentHash := orderPreInspectContentHash(order.Content)
	// 当前生效的规则版本：重新预审必须以它为基准，绝不复用旧版本算出的结论
	currentVersion := inspectServices.GetInstanceStandardRuleVersion(order.InstanceID).Version
	switch {
	case IsOrderPreInspectEditable(progress) && strings.TrimSpace(order.Content) != "":
		// 重建条件（任一满足即按当前规则全量重跑）：
		//   · 快照不存在（如撤回已作废旧结论）；
		//   · SQL 内容已变化；
		//   · 上一次生成失败；
		//   · 快照所用规则版本已不是当前生效版本（规则被启用/停用/改级别/改参数）；
		//   · 快照生成于版本机制上线前（无版本记录）——只重建一次，之后写入版本。
		needRebuild := snapshot == nil ||
			snapshot.ContentHash != contentHash ||
			snapshot.Status != "success" ||
			!snapshotRuleVersionIsCurrent(snapshot, currentVersion)
		if needRebuild {
			var inspectErr error
			snapshot, inspectErr = generateOrderPreInspectSnapshot(&order, s.C, false, false)
			if inspectErr != nil {
				return nil, inspectErr
			}
		}
	case IsOrderPreInspectFinal(progress):
		if strings.TrimSpace(order.Content) != "" && needsTerminalBackfill(snapshot) {
			// 存量工单回填：表结构已被本工单 DDL 改变，必须排除自因误报；
			// 失败不报错——failed 快照已落库，响应中会带出失败原因。
			if record, inspectErr := generateOrderPreInspectSnapshot(&order, s.C, true, true); record != nil {
				snapshot = record
				if inspectErr != nil {
					global.App.Log.Warnf("回填终态工单规范预审快照失败: %v, order_id: %s", inspectErr, order.OrderID)
				}
			}
		}
	case snapshot == nil && strings.TrimSpace(order.Content) != "":
		// 执行前阶段的存量工单（审批中/审批通过/执行中）：DDL 尚未执行，当前表结构
		// 仍等于执行前结构，回填一次结果有效；标记 backfilled 供前端说明来源。
		if record, inspectErr := generateOrderPreInspectSnapshot(&order, s.C, true, false); record != nil {
			snapshot = record
			if inspectErr != nil {
				global.App.Log.Warnf("回填工单规范预审快照失败: %v, order_id: %s", inspectErr, order.OrderID)
			}
		}
	}

	return buildOrderPreInspectSnapshotResponse(&order, snapshot, contentHash, currentVersion), nil
}

// snapshotRuleVersionIsCurrent 判断快照所用的规则版本是否仍是当前生效版本。
//
// 未绑定规范（currentVersion 为空）时没有版本可言，只要快照同样没有版本记录就认为有效，
// 避免存量工单被无意义地反复重跑。
func snapshotRuleVersionIsCurrent(snapshot *models.InsightOrderPreInspections, currentVersion string) bool {
	if snapshot == nil {
		return false
	}
	if currentVersion == "" {
		return snapshot.RuleVersion == ""
	}
	return snapshot.RuleVersion == currentVersion
}

// needsTerminalBackfill 判断终态工单是否需要回填快照：
//   - 没有快照 → 需要；
//   - 提交时刻生成的快照（backfilled=false）→ 权威数据，不需要；
//   - 执行前阶段回填（pre_exec）→ 当时 DDL 未执行，等同执行前状况，不需要；
//   - 执行后回填且已排除自因误报（post_exec + reconciled）→ 不需要；
//   - 其余（历史遗留回填、失败快照、旧版本产物）→ 按新口径重新回填一次。
func needsTerminalBackfill(snapshot *models.InsightOrderPreInspections) bool {
	if snapshot == nil {
		return true
	}
	if !snapshot.Backfilled {
		return false
	}
	if snapshot.Status != "success" {
		return true
	}
	phase, reconciled := snapshotBackfillMeta(snapshot)
	if phase == preInspectBackfillPhasePreExec {
		return false
	}
	return !(phase == preInspectBackfillPhasePostExec && reconciled)
}

// snapshotBackfillMeta 读取快照内记录的来源标记（生成阶段 / 是否已做自因过滤）
func snapshotBackfillMeta(snapshot *models.InsightOrderPreInspections) (string, bool) {
	if snapshot == nil || len(snapshot.Result) == 0 {
		return "", false
	}
	var payload struct {
		Phase      string `json:"backfill_phase"`
		Reconciled bool   `json:"reconciled"`
	}
	if err := json.Unmarshal(snapshot.Result, &payload); err != nil {
		return "", false
	}
	return payload.Phase, payload.Reconciled
}
