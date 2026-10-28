package services

// ---------------------------------------------------------------------------
// 数据访问权限申请：申请 / 审批 / 驳回 / 撤回 / 查询
//
// 与授权服务（data_grant_admin.go）的分工：
//   data_grant_admin.go —— 「谁直接把权限给了谁」
//   access_request.go   —— 「用户凭什么把权限要到了手」
//
// 核心不是状态机，而是「审批通过 = 授权真实落地」这一条：
// 判定侧（data_grant.go / CheckDataAccess）只认 insight_das_data_grants，
// 申请单状态不参与任何判定。所以审批通过必须与写授权记录同事务完成，
// 否则会出现「页面显示已通过、实际还是没权限」的双真相，比不给权限更难排查。
// ---------------------------------------------------------------------------

import (
	"errors"
	"fmt"
	"strings"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/das/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/pagination"
	"github.com/simajiong/jaysql/pkg/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// 申请单状态
	//   pending    待审批（已指定审批人）
	//   forwarded  待审批·已转交（当前处理人被改写过，语义与 pending 相同）
	//   wait_grant 待执行授权（审批已通过，尚未写授权表——此时还没有任何权限）
	//   approved   已通过（授权已真实写入 insight_das_data_grants）
	AccessRequestStatusPending    = "pending"
	AccessRequestStatusForwarded  = "forwarded"
	AccessRequestStatusWaitGrant  = "wait_grant"
	AccessRequestStatusApproved   = "approved"
	AccessRequestStatusRejected   = "rejected"
	AccessRequestStatusWithdrawn  = "withdrawn"
	AccessRequestStatusTerminated = "terminated"
	AccessRequestStatusExpired    = "expired"
	AccessRequestStatusRevoked    = "revoked"

	// 流转事件
	AccessRequestEventCreate    = "create"
	AccessRequestEventApprove   = "approve"
	AccessRequestEventReject    = "reject"
	AccessRequestEventForward   = "forward"
	AccessRequestEventWithdraw  = "withdraw"
	AccessRequestEventTerminate = "terminate"
	AccessRequestEventGrant     = "grant"
	AccessRequestEventExpire    = "expire"
	AccessRequestEventRevoke    = "revoke"

	// 申请单默认与最长有效期（天）。默认 6 个月，与申请页的默认选中项一致。
	accessRequestDefaultExpireDays = 180
	accessRequestMaxExpireDays     = 3650
	// accessRequestExpireForever 长期有效的哨兵值：不设失效时间。
	// 申请单的 requested/approved_expire_at 落 NULL，授权表的 expire_at 也落 NULL，
	// 而 data_grant.go 的判定一律把 NULL 当「永久有效」，所以两端口径天然一致。
	accessRequestExpireForever = -1
)

var (
	ErrAccessRequestNotFound    = errors.New("申请单不存在")
	ErrAccessRequestNotPending  = errors.New("申请单当前状态不允许该操作")
	ErrAccessRequestSelfApprove = errors.New("不能审批自己提交的申请")
	ErrAccessRequestSelfGrant   = errors.New("不能为自己的申请执行授权，请由其他审批人落地")
	ErrAccessRequestDuplicate   = errors.New("该资源已有进行中的申请，请等待审批结果")
	ErrAccessRequestGranted     = errors.New("该资源的数据访问权限仍在有效期内，无需重复申请")
	ErrAccessRequestNotAssignee = errors.New("不是当前处理人，无法处理该申请")
)

// accessRequestInApproval 审批中（含已转交）：可审批 / 驳回 / 转交 / 撤回 / 终止
func accessRequestInApproval(status string) bool {
	return status == AccessRequestStatusPending || status == AccessRequestStatusForwarded
}

// ---------------------------------------------------------------------------
// 派生与工具
// ---------------------------------------------------------------------------

// accessRequestResourcePath 流转事件里的资源路径：
// 实例级=整个实例，库级=库名，模式级=库.模式，表级=库.模式.表
func accessRequestResourcePath(level, schema, table string) string {
	switch level {
	case DataResourceLevelInstance:
		return "整个实例"
	case DataResourceLevelTable:
		if schema != "" && table != "" {
			return schema + "." + table
		}
	}
	if schema != "" {
		return schema
	}
	return "整个实例"
}

// accessRequestDatabaseOf 从授权路径里取库名。
// 三层结构（PG / SQL Server）的路径是「库.模式」，取第一段；单层结构整段就是库名。
func accessRequestDatabaseOf(grantPath string) string {
	if idx := strings.Index(grantPath, "."); idx > 0 {
		return grantPath[:idx]
	}
	return grantPath
}

// accessRequestResourceType 授权行的 resource_type（database / schema）。
// 模式级与表级都属于「模式内」资源，但模式级本身要单独标出来，
// 否则授权管理页按 resource_type 渲染时会把模式级授权显示成库级。
func accessRequestResourceType(level string) string {
	if level == DataResourceLevelSchema {
		return "schema"
	}
	return "database"
}

// isInstanceResourceLevel 申请范围是否为「整个实例」。
func isInstanceResourceLevel(level string) bool {
	return strings.EqualFold(strings.TrimSpace(level), DataResourceLevelInstance)
}

// accessRequestLevelLabel 资源层级的中文标签（事件文案不出现裸英文枚举）
func accessRequestLevelLabel(level string) string {
	switch level {
	case DataResourceLevelInstance:
		return "实例"
	case DataResourceLevelDatabase:
		return "库"
	case DataResourceLevelSchema:
		return "模式"
	case DataResourceLevelTable:
		return "表"
	}
	return level
}

// accessRequestExpire 计算申请的有效期截止时间。
// days > 0 → now + days；days == 0 → 默认天数；days < 0 → 长期有效（返回 nil，落库为 NULL）。
func accessRequestExpire(days int) (*time.Time, error) {
	if days < 0 {
		return nil, nil
	}
	if days == 0 {
		days = accessRequestDefaultExpireDays
	}
	if days > accessRequestMaxExpireDays {
		return nil, fmt.Errorf("有效期不能超过 %d 天（如需永久授权请选择「长期」）", accessRequestMaxExpireDays)
	}
	at := time.Now().AddDate(0, 0, days)
	return &at, nil
}

// accessRequestExpireText 有效期文案。nil = 长期有效，事件与提示里不能出现 <nil> 或零值日期。
func accessRequestExpireText(at *time.Time) string {
	if at == nil {
		return "长期"
	}
	return at.Format(dataGrantTimeLayout)
}

// nullableTimeValue 把可空时间转成 map 更新值。
// 直接把 (*time.Time)(nil) 放进 map 交给 GORM，写出来是零值日期而不是 NULL，
// 长期授权就会被当成「1970 年到期」——这是必须显式归一化的地方。
func nullableTimeValue(at *time.Time) any {
	if at == nil {
		return nil
	}
	return *at
}

// mergeGrantActions 合并两个逗号分隔的操作集合（按 select → export → dml 固定顺序输出）
func mergeGrantActions(existing, incoming string) string {
	has := map[string]bool{}
	for _, raw := range strings.Split(existing+","+incoming, ",") {
		v := strings.ToLower(strings.TrimSpace(raw))
		if v != "" {
			has[v] = true
		}
	}
	out := make([]string, 0, 3)
	for _, action := range []string{DataActionSelect, DataActionExport, DataActionDML} {
		if has[action] {
			out = append(out, action)
		}
	}
	if len(out) == 0 {
		return DataActionSelect
	}
	return strings.Join(out, ",")
}

// writeAccessRequestEvent 写一条申请单流转事件。必须与状态变更同事务。
func writeAccessRequestEvent(tx *gorm.DB, req *models.InsightDasAccessRequest, eventType, fromStatus, toStatus, operator, comment, sourceIP string) {
	if tx == nil || req == nil {
		return
	}
	_ = tx.Create(&models.InsightDasAccessRequestEvent{
		Model:            &commonModels.Model{},
		RequestNo:        req.RequestNo,
		EventType:        eventType,
		FromStatus:       fromStatus,
		ToStatus:         toStatus,
		OperatorUsername: operator,
		OperatorName:     operator,
		Comment:          strings.TrimSpace(comment),
		SourceIP:         strings.TrimSpace(sourceIP),
	}).Error
}

// ---------------------------------------------------------------------------
// 发起申请
// ---------------------------------------------------------------------------

type CreateAccessRequestService struct {
	*forms.AccessRequestCreateForm
	Username string
	SourceIP string
}

func (s *CreateAccessRequestService) Run() (*models.InsightDasAccessRequest, error) {
	instanceID := strings.TrimSpace(s.InstanceID)
	if instanceID == "" {
		return nil, errors.New("请选择目标数据源")
	}

	// 目标实例必须存在（申请人未必有该实例的授权——这正是要走申请流程的原因）
	var inst struct {
		InstanceID string
		Hostname   string
		Port       int
		DBType     string
		Remark     string
	}
	if err := global.App.DB.Table("insight_instances").
		Select("instance_id, ifnull(hostname,'') as hostname, ifnull(port,0) as port, ifnull(db_type,'') as db_type, ifnull(remark,'') as remark").
		Where("instance_id = ?", instanceID).Take(&inst).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("目标数据源不存在")
		}
		return nil, err
	}
	hasSchemaLevel := HasSchemaLevel(inst.DBType)

	// 圈选快照：授权清单（entries）优先；否则走 v1 多资源 / 旧单资源解析。
	// 旧单资源字段在新字段全空时兜底，脚本 / AI 工具等旧调用方不受影响。
	level := strings.ToLower(strings.TrimSpace(s.ResourceLevel))
	var (
		specJSON        string
		specEmpty       bool
		databaseName    string
		schemaName      string
		tableName       string
		resourceSummary string
	)
	if len(s.Entries) > 0 {
		entries, err := buildAccessEntries(s.Entries, hasSchemaLevel)
		if err != nil {
			return nil, err
		}
		// 清单里的每个对象都必须真实挂在实例下：防止拼出一段不存在的路径，批了也授不了
		if err := validateAccessEntriesResources(instanceID, hasSchemaLevel, entries); err != nil {
			return nil, err
		}
		specJSON, err = entriesCanonicalJSON(entries)
		if err != nil {
			return nil, err
		}
		// 清单模式下一单可混搭多层级，resource_level 由清单内容派生（表 > 模式 > 库）
		level = derivedAccessResourceLevel(entries)
		databaseName, schemaName, tableName = entriesLegacyColumns(entries)
		resourceSummary = entriesResourceSummary(entries)
	} else {
		spec, err := buildAccessResourceSpec(level, s.AccessRequestCreateForm, hasSchemaLevel)
		if err != nil {
			return nil, err
		}
		specJSON, err = spec.canonicalSpecJSON()
		if err != nil {
			return nil, err
		}
		specEmpty = spec.isEmpty()
		// 每个资源都必须真实挂在实例下：防止拼出一段不存在的路径，批了也授不了
		if err := validateAccessSpecResources(instanceID, hasSchemaLevel, spec); err != nil {
			return nil, err
		}
		// 旧列兼容值：只存第一个资源（详情 / 列表的历史口径展示用），
		// 完整圈选以 resource_spec 为权威。
		databaseName, schemaName, tableName = specLegacyColumns(level, spec)
		resourceSummary = spec.resourceSummary()
	}

	// 操作类型白名单校验，合法值去重排序
	actions, err := normalizeGrantActions(s.Actions)
	if err != nil {
		return nil, err
	}
	// 申请入口只受理查询权限：变更(DML/DDL)与导出(EXPORT)由 SQL 工单自身的
	// 「提交 → 审批 → 执行」流程覆盖，再申请一次数据权限等于同一件事审两遍，
	// 且常驻授权在有效期内可执行任意变更，反而比工单审批更宽（见 access_action_scope.go）。
	if scope := AccessAPIScopeOfAction(actions); scope != AccessAPIScopeQuery {
		return nil, errors.New("本入口只受理查询权限；变更与导出请在 SQL 工单中提交，由工单审批流程覆盖")
	}

	// 审批人：申请人显式指定，且必须真的持有审批权（否则单子会投进没人能处理的池子）
	approver := strings.TrimSpace(s.ApproverUsername)
	if strings.EqualFold(approver, s.Username) {
		return nil, ErrAccessRequestSelfApprove
	}
	if err := ensureAccessApproverCandidate(approver); err != nil {
		return nil, err
	}

	// 每个资源都必须真实挂在实例下：防止拼出一段不存在的路径，批了也授不了
	//（清单分支已在上面各自校验）

	scope, err := resolveDASActorScope(s.Username)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	// 去重：同一申请人 + 同一数据源 + 同一份圈选内容，只要有「进行中 / 仍生效」的单子
	// 就不允许重复提交。spec 非空按 spec 全等比较（多资源单的圈选内容完全一致才算重复）；
	// spec 为空（旧数据 / 兼容路径）沿用旧列比较。
	// 不再比较 actions：申请入口只受理查询，actions 恒为 select。
	var blocking int64
	if specJSON != "" && !specEmpty {
		if err := global.App.DB.Table("insight_das_access_requests").
			Where("applicant_username = ? AND instance_id = ? AND resource_level = ? AND resource_spec = ?"+
				" AND (status IN ? OR (status = ? AND (expire_at IS NULL OR expire_at > ?)))",
				s.Username, instanceID, level, specJSON,
				[]string{AccessRequestStatusPending, AccessRequestStatusForwarded, AccessRequestStatusWaitGrant},
				AccessRequestStatusApproved, now).
			Count(&blocking).Error; err != nil {
			return nil, err
		}
	} else {
		if err := global.App.DB.Table("insight_das_access_requests").
			Where("applicant_username = ? AND instance_id = ? AND schema_name = ? AND table_name = ?"+
				" AND (status IN ? OR (status = ? AND (expire_at IS NULL OR expire_at > ?)))",
				s.Username, instanceID, schemaName, tableName,
				[]string{AccessRequestStatusPending, AccessRequestStatusForwarded, AccessRequestStatusWaitGrant},
				AccessRequestStatusApproved, now).
			Count(&blocking).Error; err != nil {
			return nil, err
		}
	}
	if blocking > 0 {
		return nil, ErrAccessRequestDuplicate
	}

	expireAt, err := accessRequestExpire(s.ExpireDays)
	if err != nil {
		return nil, err
	}

	// 数据源展示名：优先实例备注（与列表页「数据源」列口径一致），为空回落 host:port
	instanceDisplay := strings.TrimSpace(inst.Remark)
	if instanceDisplay == "" {
		instanceDisplay = fmt.Sprintf("%s:%d", inst.Hostname, inst.Port)
	}

	req := &models.InsightDasAccessRequest{
		Model:             &commonModels.Model{},
		RequestNo:         utils.GenerateAccessRequestID(),
		ApplicantID:       scope.UID,
		ApplicantUsername: s.Username,
		ApplicantName:     s.Username,
		InstanceID:        instanceID,
		InstanceName:      fmt.Sprintf("%s:%d", inst.Hostname, inst.Port),
		DBType:            inst.DBType,
		Database:          databaseName,
		SchemaName:        schemaName,
		Table:             tableName,
		ResourceLevel:     level,
		ResourceSpec:      specJSON,
		Actions:           actions,
		Reason:            strings.TrimSpace(s.Reason),
		RequestedExpireAt: toLocalTimePtr(expireAt),
		Status:            AccessRequestStatusPending,
		ApproverUsername:  approver,
		ApproverName:      approver,
	}

	// 事件里的资源摘要：多资源单必须把圈选范围说全，只写第一个资源会误导审批人
	if resourceSummary == "" {
		resourceSummary = accessRequestResourcePath(level, schemaName, tableName)
	}

	if err := global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(req).Error; err != nil {
			return err
		}
		writeAccessRequestEvent(tx, req, AccessRequestEventCreate, "", AccessRequestStatusPending, s.Username,
			fmt.Sprintf("发起申请：%s / %s / %s，有效期 %s，审批人 %s", instanceDisplay,
				resourceSummary, actions, accessRequestExpireText(expireAt), approver), s.SourceIP)
		return nil
	}); err != nil {
		return nil, err
	}
	return req, nil
}

// ---------------------------------------------------------------------------
// 审批通过：只推进到「待执行授权」
//
// 审批与落地分开：审批人批的是「这份申请可以给」，执行人做的是「把权限写下去」。
// 合在一步里时，通过即授权生效，事后分不清「批错了」还是「执行错了」，
// 也没法要求另一位执行人复核——与 SQL 工单的审批 / 执行分离口径保持一致。
// 注意：这一步不写授权表，所以 wait_grant 状态下申请人仍然没有任何权限，
// 列表与详情必须把这个中间态显式展示出来。
// ---------------------------------------------------------------------------

type ApproveAccessRequestService struct {
	*forms.AccessRequestApproveForm
	RequestNo string
	Username  string
	SourceIP  string
	// IsAdmin 管理员旁路：任何人的单子都能处理（仍不能批自己的）
	IsAdmin bool
}

func (s *ApproveAccessRequestService) Run() (*models.InsightDasAccessRequest, error) {
	var result models.InsightDasAccessRequest
	err := global.App.DB.Transaction(func(tx *gorm.DB) error {
		// 行锁：两个处理人同时点「通过」时，只有一个能把状态推进到 wait_grant
		req, err := lockAccessRequest(tx, s.RequestNo)
		if err != nil {
			return err
		}
		if !accessRequestInApproval(req.Status) {
			return ErrAccessRequestNotPending
		}
		if strings.EqualFold(req.ApplicantUsername, s.Username) {
			return ErrAccessRequestSelfApprove
		}
		if err := ensureCurrentApprover(req, s.Username, s.IsAdmin); err != nil {
			return err
		}

		// 有效期：审批人可以调整，不填沿用申请人填的值
		// 有效期：审批人可以调整，不填沿用申请人填的值；双方都选「长期」时为 nil
		expireAt, err := resolveAccessRequestExpire(req, s.ExpireDays)
		if err != nil {
			return err
		}
		if expireAt != nil && !expireAt.After(time.Now()) {
			return errors.New("授权有效期必须晚于当前时间")
		}

		now := time.Now()
		if err := tx.Table("insight_das_access_requests").Where("id = ?", req.Model.ID).
			Updates(map[string]any{
				"status":             AccessRequestStatusWaitGrant,
				"approver_username":  s.Username,
				"approver_name":      s.Username,
				"approved_at":        now,
				"approve_comment":    strings.TrimSpace(s.Comment),
				"approved_expire_at": nullableTimeValue(expireAt),
			}).Error; err != nil {
			return err
		}
		req.Status = AccessRequestStatusWaitGrant
		req.ApproverUsername = s.Username
		req.ApproverName = s.Username
		req.ApprovedAt = toLocalTimePtr(&now)
		req.ApproveComment = strings.TrimSpace(s.Comment)
		req.ApprovedExpireAt = toLocalTimePtr(expireAt)

		writeAccessRequestEvent(tx, req, AccessRequestEventApprove, AccessRequestStatusPending, AccessRequestStatusWaitGrant,
			s.Username, fmt.Sprintf("审批通过，待执行授权落地；有效期：%s。%s",
				accessRequestExpireText(expireAt), strings.TrimSpace(s.Comment)), s.SourceIP)

		result = *req
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// ---------------------------------------------------------------------------
// 转交 / 终止
// ---------------------------------------------------------------------------

// ForwardAccessRequestService 转交：把当前处理人换成另一位审批人。
// 状态仍处于审批中（forwarded），目的只是换人继续审，不是审批结果。
type ForwardAccessRequestService struct {
	*forms.AccessRequestForwardForm
	RequestNo string
	Username  string
	SourceIP  string
	IsAdmin   bool
}

func (s *ForwardAccessRequestService) Run() error {
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		req, err := lockAccessRequest(tx, s.RequestNo)
		if err != nil {
			return err
		}
		if !accessRequestInApproval(req.Status) {
			return ErrAccessRequestNotPending
		}
		if err := ensureCurrentApprover(req, s.Username, s.IsAdmin); err != nil {
			return err
		}

		target := strings.TrimSpace(s.ApproverUsername)
		if strings.EqualFold(target, req.ApproverUsername) {
			return errors.New("转交对象不能是当前处理人自己")
		}
		if strings.EqualFold(target, req.ApplicantUsername) {
			return ErrAccessRequestSelfApprove
		}
		if err := ensureAccessApproverCandidate(target); err != nil {
			return err
		}

		from := req.Status
		if err := tx.Table("insight_das_access_requests").Where("id = ?", req.Model.ID).
			Updates(map[string]any{
				"status":            AccessRequestStatusForwarded,
				"approver_username": target,
				"approver_name":     target,
			}).Error; err != nil {
			return err
		}
		req.Status = AccessRequestStatusForwarded
		req.ApproverUsername = target
		req.ApproverName = target

		writeAccessRequestEvent(tx, req, AccessRequestEventForward, from, AccessRequestStatusForwarded, s.Username,
			fmt.Sprintf("转交给 %s 继续审批：%s", target, strings.TrimSpace(s.Comment)), s.SourceIP)
		return nil
	})
}

// TerminateAccessRequestService 终止：申请人或管理员在审批中叫停本单。
//
// 与「撤回」的区别只在主体：撤回是申请人自己的动作，终止允许管理员代为叫停
// （例如申请人已离职、资源已下线）。两者都是终态，都不会写授权。
type TerminateAccessRequestService struct {
	*forms.AccessRequestTerminateForm
	RequestNo string
	Username  string
	SourceIP  string
	IsAdmin   bool
}

func (s *TerminateAccessRequestService) Run() error {
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		req, err := lockAccessRequest(tx, s.RequestNo)
		if err != nil {
			return err
		}
		if !accessRequestInApproval(req.Status) {
			return ErrAccessRequestNotPending
		}
		if !s.IsAdmin && !strings.EqualFold(req.ApplicantUsername, s.Username) {
			return errors.New("只有申请人或管理员可以终止本单")
		}

		from := req.Status
		if err := tx.Table("insight_das_access_requests").Where("id = ?", req.Model.ID).
			Updates(map[string]any{"status": AccessRequestStatusTerminated}).Error; err != nil {
			return err
		}
		req.Status = AccessRequestStatusTerminated

		comment := strings.TrimSpace(s.Comment)
		if comment == "" {
			comment = "终止本单，流程结束"
		}
		writeAccessRequestEvent(tx, req, AccessRequestEventTerminate, from, AccessRequestStatusTerminated,
			s.Username, comment, s.SourceIP)
		return nil
	})
}

// ---------------------------------------------------------------------------
// 执行授权：把审批通过的申请真正写成权限
//
// 这是「申请单 → 权限」的唯一落地点：只有这里写 insight_das_data_grants。
// 事务边界必须把「写授权」与「改状态」包在一起，否则会出现
// 「权限已生效但单子还停在待执行」（重复执行会写出两条授权）。
// ---------------------------------------------------------------------------

type GrantAccessRequestService struct {
	*forms.AccessRequestGrantForm
	RequestNo string
	Username  string
	SourceIP  string
}

func (s *GrantAccessRequestService) Run() (*models.InsightDasAccessRequest, error) {
	var result models.InsightDasAccessRequest
	err := global.App.DB.Transaction(func(tx *gorm.DB) error {
		req, err := lockAccessRequest(tx, s.RequestNo)
		if err != nil {
			return err
		}
		if req.Status != AccessRequestStatusWaitGrant {
			return errors.New("只有「待执行授权」的申请可以执行授权")
		}
		// 自批已被拦，自执行同样要拦：审批 + 执行都落在同一个人身上等于没有审批
		if strings.EqualFold(req.ApplicantUsername, s.Username) {
			return ErrAccessRequestSelfGrant
		}

		expireAt, err := resolveAccessRequestExpire(req, 0)
		if err != nil {
			return err
		}
		if expireAt != nil && !expireAt.After(time.Now()) {
			return errors.New("审批确定的有效期已过期，请驳回后由申请人重新发起")
		}

		grant, err := grantAccessForRequest(tx, req, expireAt, s.Username, strings.TrimSpace(s.Comment), s.SourceIP)
		if err != nil {
			return err
		}

		now := time.Now()
		if err := tx.Table("insight_das_access_requests").Where("id = ?", req.Model.ID).
			Updates(map[string]any{
				"status":            AccessRequestStatusApproved,
				"grant_no":          grant.GrantNo,
				"effective_at":      now,
				"expire_at":         nullableTimeValue(expireAt),
				"executor_username": s.Username,
				"executor_name":     s.Username,
				"executed_at":       now,
			}).Error; err != nil {
			return err
		}
		req.Status = AccessRequestStatusApproved
		req.GrantNo = grant.GrantNo
		req.EffectiveAt = toLocalTimePtr(&now)
		req.ExpireAt = toLocalTimePtr(expireAt)
		req.ExecutorUsername = s.Username
		req.ExecutorName = s.Username
		req.ExecutedAt = toLocalTimePtr(&now)

		writeAccessRequestEvent(tx, req, AccessRequestEventGrant, AccessRequestStatusWaitGrant, AccessRequestStatusApproved,
			s.Username, fmt.Sprintf("执行授权，授权单号 %s，有效期：%s。%s",
				grant.GrantNo, accessRequestExpireText(expireAt), strings.TrimSpace(s.Comment)), s.SourceIP)

		result = *req
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// ---------------------------------------------------------------------------
// 写授权记录
// ---------------------------------------------------------------------------

// accessRequestAuditReason 授权审计里的来源描述。
// 历史申请单有 order_id（早期「凭工单申请」），新申请单没有，不能硬拼「工单 %s」留下空串。
func accessRequestAuditReason(req *models.InsightDasAccessRequest, suffix string) string {
	base := "数据访问申请 " + req.RequestNo
	if orderID := strings.TrimSpace(req.OrderID); orderID != "" {
		base = "工单 " + orderID + " " + base
	}
	if extra := strings.TrimSpace(suffix); extra != "" {
		base += " " + extra
	}
	return base
}

// grantAccessForRequest 依据申请单快照写授权记录。
//
// 用申请单快照而不是重新读工单：审批人批的是「他看到的那份申请」，
// 若审批期间工单被改到另一个库，按新工单授权等于批了没被审过的东西。
// 同主体同资源已有生效授权时不再新建，而是「动作取并集 + 失效时间取较晚」，
// 避免同一个人同一资源下堆出一串互相重叠的授权记录。
// expireAt 为 nil 表示长期有效（授权表 expire_at 落 NULL）。
//
// 多资源申请单（resource_spec 非空）展开成多条授权行，每条带 request_no 指回申请单；
// 返回值是第一条授权（申请单的 grant_no 列仍存它，兼容旧展示与撤销联动）。
func grantAccessForRequest(tx *gorm.DB, req *models.InsightDasAccessRequest, expireAt *time.Time, operator, comment, sourceIP string) (*models.InsightDasDataGrant, error) {
	if req == nil {
		return nil, errors.New("申请单为空")
	}
	actions, err := normalizeGrantActions(req.Actions)
	if err != nil {
		return nil, err
	}

	// 圈选快照：授权清单（entries）优先；v1 格式读多资源 spec；旧单按旧三列构造单资源
	var resources []accessGrantResource
	if entries, ok, err := parseAccessEntries(req.ResourceSpec); err != nil {
		return nil, err
	} else if ok {
		resources = entriesExpandGrantResources(entries)
	} else {
		spec, err := parseAccessResourceSpec(req.ResourceSpec)
		if err != nil {
			return nil, err
		}
		if spec == nil {
			spec = specFromLegacyRow(req.ResourceLevel, req.SchemaName, req.Table)
		}
		// 整实例申请没有库 / 模式可圈选，resource_spec 落的是空结构 "{}"
		// （历史单为 NULL）。这是合法圈选而不是"缺资源"，展开成一条 instance 级授权行。
		// 早前这里把空 spec 一律判成「没有可授权的资源」，
		// 导致所有「申请范围 = 整个实例」的单子都卡在最后一步无法落地。
		if spec == nil && isInstanceResourceLevel(req.ResourceLevel) {
			spec = &accessResourceSpec{}
		}
		if spec == nil || (spec.isEmpty() && !isInstanceResourceLevel(req.ResourceLevel)) {
			return nil, errors.New("申请单没有可授权的资源")
		}
		resources = spec.expandGrantResources()
	}
	if len(resources) == 0 {
		return nil, errors.New("申请单没有可授权的资源")
	}

	var firstGrant *models.InsightDasDataGrant
	for _, res := range resources {
		grant, err := grantAccessResourceRow(tx, req, res, actions, expireAt, operator, comment, sourceIP)
		if err != nil {
			return nil, err
		}
		if firstGrant == nil {
			firstGrant = grant
		}
	}
	return firstGrant, nil
}

// grantAccessResourceRow 落一条授权行：已有同主体同资源同圈选方式的生效授权时合并，否则新建。
func grantAccessResourceRow(tx *gorm.DB, req *models.InsightDasAccessRequest, res accessGrantResource, actions string, expireAt *time.Time, operator, comment, sourceIP string) (*models.InsightDasDataGrant, error) {
	tableMode := strings.ToLower(strings.TrimSpace(res.TableMode))
	if tableMode == "" {
		tableMode = "exact"
	}

	var existing models.InsightDasDataGrant
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("principal_type = ? AND principal_id = ? AND instance_id = ? AND schema_name = ? AND table_name = ? AND table_mode = ? AND effect = ? AND status = ?",
			dasPrincipalUser, req.ApplicantID, req.InstanceID, res.Schema, res.Table, tableMode, DataEffectAllow, DataGrantStatusActive).
		Take(&existing).Error
	if err == nil {
		before := snapshotGrant(&existing)
		merged := mergeGrantActions(existing.Actions, actions)
		// 失效时间取较晚：本次长期 ⇒ 结果也是长期（NULL 覆盖任何具体日期）
		newExpire := expireAt
		if existing.ExpireAt != nil && newExpire != nil {
			if existingExpire := time.Time(*existing.ExpireAt); existingExpire.After(*newExpire) {
				newExpire = &existingExpire
			}
		}
		updates := map[string]any{"actions": merged, "expire_at": nullableTimeValue(newExpire)}
		// 旧行没有 request_no（历史授权被并入本次申请）时补上，撤销联动才有据可查
		if strings.TrimSpace(existing.RequestNo) == "" {
			updates["request_no"] = req.RequestNo
		}
		if err := tx.Table("insight_das_data_grants").Where("id = ?", existing.Model.ID).
			Updates(updates).Error; err != nil {
			return nil, err
		}
		existing.Actions = merged
		existing.ExpireAt = toLocalTimePtr(newExpire)
		existing.RequestNo = anyStringValue(updates["request_no"], existing.RequestNo)
		writeGrantAudit(tx, &models.InsightDasGrantAudit{
			Model:            &commonModels.Model{},
			GrantNo:          existing.GrantNo,
			GrantID:          existing.Model.ID,
			Action:           DataGrantAuditRenew,
			OperatorUsername: operator,
			OperatorName:     operator,
			PrincipalType:    existing.PrincipalType,
			PrincipalID:      existing.PrincipalID,
			PrincipalName:    existing.PrincipalName,
			ResourceDesc:     describeDataResource(existing.InstanceID, existing.SchemaName, existing.Table),
			Actions:          merged,
			BeforeSnapshot:   before,
			AfterSnapshot:    snapshotGrant(&existing),
			Reason:           accessRequestAuditReason(req, "执行授权（合并至已有授权）"+strings.TrimSpace(comment)),
			SourceIP:         sourceIP,
		})
		return &existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	now := time.Now()
	grant := &models.InsightDasDataGrant{
		Model:         &commonModels.Model{},
		GrantNo:       utils.GenerateDataGrantID(),
		PrincipalType: dasPrincipalUser,
		PrincipalID:   req.ApplicantID,
		PrincipalName: req.ApplicantName,
		ResourceLevel: res.Level,
		InstanceID:    req.InstanceID,
		SchemaName:    res.Schema,
		Table:         res.Table,
		TableMode:     tableMode,
		RequestNo:     req.RequestNo,
		// 资源类型跟层级走：模式级授权要标成 schema，否则授权管理页把它当库级展示
		ResourceType:    accessRequestResourceType(res.Level),
		Actions:         actions,
		Effect:          DataEffectAllow,
		EffectiveAt:     toLocalTimePtr(&now),
		ExpireAt:        toLocalTimePtr(expireAt),
		GrantorUsername: operator,
		GrantorName:     operator,
		GrantSource:     DataGrantSourceApply,
		Status:          DataGrantStatusActive,
		Reason:          accessRequestAuditReason(req, "执行授权"),
	}
	if err := tx.Create(grant).Error; err != nil {
		return nil, err
	}
	writeGrantAudit(tx, &models.InsightDasGrantAudit{
		Model:            &commonModels.Model{},
		GrantNo:          grant.GrantNo,
		GrantID:          grant.Model.ID,
		Action:           DataGrantAuditCreate,
		OperatorUsername: operator,
		OperatorName:     operator,
		PrincipalType:    grant.PrincipalType,
		PrincipalID:      grant.PrincipalID,
		PrincipalName:    grant.PrincipalName,
		ResourceDesc:     describeDataResource(grant.InstanceID, grant.SchemaName, grant.Table),
		Actions:          grant.Actions,
		AfterSnapshot:    snapshotGrant(grant),
		Reason:           grant.Reason,
		SourceIP:         sourceIP,
	})
	return grant, nil
}

// anyStringValue map 更新值里取字符串（value 不一定是 string 类型时回落 def）。
func anyStringValue(v any, def string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

// ---------------------------------------------------------------------------
// 事务内共用工具
// ---------------------------------------------------------------------------

// lockAccessRequest 取申请单并加行锁（并发处理同一单时只有一个能推进状态）
func lockAccessRequest(tx *gorm.DB, requestNo string) (*models.InsightDasAccessRequest, error) {
	var req models.InsightDasAccessRequest
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("request_no = ?", strings.TrimSpace(requestNo)).Take(&req).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAccessRequestNotFound
		}
		return nil, err
	}
	return &req, nil
}

// ensureCurrentApprover 只有当前处理人能推进审批（管理员旁路）。
// 转交之后原审批人即失权——这一条是服务层拦截，不依赖前端把按钮置灰。
func ensureCurrentApprover(req *models.InsightDasAccessRequest, username string, isAdmin bool) error {
	if isAdmin {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(req.ApproverUsername), strings.TrimSpace(username)) {
		return fmt.Errorf("%w：当前处理人是 %s", ErrAccessRequestNotAssignee, req.ApproverUsername)
	}
	return nil
}

// resolveAccessRequestExpire 计算本次落地要用的有效期（nil = 长期有效）。
// 审批时显式选择的调整值优先（expireDays），其次审批确定的值（approved_expire_at），
// 最后申请人填的期望值（requested_expire_at）。
// 两个时点都为空 ⇒ 双方都选了「长期」：wait_grant 状态只可能由审批动作写入，
// 所以「审批已过但 approved_expire_at 为空」只可能是长期，不能回落默认天数。
func resolveAccessRequestExpire(req *models.InsightDasAccessRequest, expireDays int) (*time.Time, error) {
	if expireDays != 0 {
		return accessRequestExpire(expireDays)
	}
	if req.ApprovedExpireAt != nil {
		at := time.Time(*req.ApprovedExpireAt)
		return &at, nil
	}
	if req.RequestedExpireAt != nil {
		at := time.Time(*req.RequestedExpireAt)
		return &at, nil
	}
	return nil, nil
}

// 驳回 / 撤回
// ---------------------------------------------------------------------------

type RejectAccessRequestService struct {
	*forms.AccessRequestRejectForm
	RequestNo string
	Username  string
	SourceIP  string
	IsAdmin   bool
}

func (s *RejectAccessRequestService) Run() error {
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		req, err := lockAccessRequest(tx, s.RequestNo)
		if err != nil {
			return err
		}
		if !accessRequestInApproval(req.Status) {
			return ErrAccessRequestNotPending
		}
		if strings.EqualFold(req.ApplicantUsername, s.Username) {
			return ErrAccessRequestSelfApprove
		}
		if err := ensureCurrentApprover(req, s.Username, s.IsAdmin); err != nil {
			return err
		}
		from := req.Status
		now := time.Now()
		if err := tx.Table("insight_das_access_requests").Where("id = ?", req.Model.ID).
			Updates(map[string]any{
				"status":            AccessRequestStatusRejected,
				"approver_username": s.Username,
				"approver_name":     s.Username,
				"approved_at":       now,
				"approve_comment":   strings.TrimSpace(s.Comment),
			}).Error; err != nil {
			return err
		}
		writeAccessRequestEvent(tx, req, AccessRequestEventReject, from, AccessRequestStatusRejected,
			s.Username, strings.TrimSpace(s.Comment), s.SourceIP)
		return nil
	})
}

type WithdrawAccessRequestService struct {
	*forms.AccessRequestWithdrawForm
	RequestNo string
	Username  string
	SourceIP  string
}

func (s *WithdrawAccessRequestService) Run() error {
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		req, err := lockAccessRequest(tx, s.RequestNo)
		if err != nil {
			return err
		}
		if !strings.EqualFold(req.ApplicantUsername, s.Username) {
			return errors.New("只能撤回本人提交的申请")
		}
		// 撤回只在审批中有效：进入待执行授权后再撤回，权限已经被批了，
		// 正确出口是让管理员终止或直接不执行授权。
		if !accessRequestInApproval(req.Status) {
			return ErrAccessRequestNotPending
		}
		from := req.Status
		if err := tx.Table("insight_das_access_requests").Where("id = ?", req.Model.ID).
			Updates(map[string]any{"status": AccessRequestStatusWithdrawn}).Error; err != nil {
			return err
		}
		writeAccessRequestEvent(tx, req, AccessRequestEventWithdraw, from, AccessRequestStatusWithdrawn,
			s.Username, strings.TrimSpace(s.Comment), s.SourceIP)
		return nil
	})
}

// ---------------------------------------------------------------------------
// 过期收敛 & 授权回收联动
// ---------------------------------------------------------------------------

// ExpireStaleAccessRequests 把已过有效期但仍标记为 approved 的申请单收敛为 expired。
// 判定侧本来就按时间窗口过滤，这里只是让列表口径与判定口径一致。
func ExpireStaleAccessRequests() (int64, error) {
	now := time.Now()
	var rows []models.InsightDasAccessRequest
	if err := global.App.DB.Where("status = ? AND expire_at IS NOT NULL AND expire_at <= ?", AccessRequestStatusApproved, now).
		Find(&rows).Error; err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	var count int64
	for i := range rows {
		req := rows[i]
		if err := global.App.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Table("insight_das_access_requests").
				Where("id = ? AND status = ?", req.Model.ID, AccessRequestStatusApproved).
				Updates(map[string]any{"status": AccessRequestStatusExpired}).Error; err != nil {
				return err
			}
			writeAccessRequestEvent(tx, &req, AccessRequestEventExpire, AccessRequestStatusApproved, AccessRequestStatusExpired,
				"system", "授权已到期，系统自动收敛", "")
			return nil
		}); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// MarkAccessRequestRevokedByGrantNo 授权被撤销 / 回收时，把来源申请单同步置为 revoked。
//
// 不联动会出现「申请单显示已通过、实际权限已被撤掉」的口径分裂——
// 申请人看着页面以为自己还有权限，执行时才被拦，问题定位成本极高。
// 多资源申请单一次授权会展开成多条授权行，关联口径是授权行新列 request_no；
// 旧数据（request_no 为空）回退按申请单的 grant_no 关联。
func MarkAccessRequestRevokedByGrantNo(tx *gorm.DB, grantNo, operator, reason string) {
	if tx == nil || strings.TrimSpace(grantNo) == "" {
		return
	}
	// 先取授权行自带的来源单号；取不到（行已不存在等）就只走 grant_no 兜底
	requestNo := ""
	var grantRow models.InsightDasDataGrant
	if err := tx.Where("grant_no = ?", grantNo).Take(&grantRow).Error; err == nil {
		requestNo = strings.TrimSpace(grantRow.RequestNo)
	}
	query := tx.Where("status = ?", AccessRequestStatusApproved)
	if requestNo != "" {
		query = query.Where("request_no = ? OR grant_no = ?", requestNo, grantNo)
	} else {
		query = query.Where("grant_no = ?", grantNo)
	}
	var rows []models.InsightDasAccessRequest
	if err := query.Find(&rows).Error; err != nil {
		return
	}
	for i := range rows {
		req := rows[i]
		if err := tx.Table("insight_das_access_requests").Where("id = ?", req.Model.ID).
			Updates(map[string]any{"status": AccessRequestStatusRevoked}).Error; err != nil {
			continue
		}
		writeAccessRequestEvent(tx, &req, AccessRequestEventRevoke, AccessRequestStatusApproved, AccessRequestStatusRevoked,
			operator, reason, "")
	}
}

// ---------------------------------------------------------------------------
// 查询：申请单列表
// ---------------------------------------------------------------------------

type ListAccessRequestsService struct {
	*forms.AccessRequestListForm
	Username string
	// CanApprove / CanViewAll 由视图层按权限点算出后传入：
	// 服务层不依赖 HTTP 上下文，也避免「前端传个 scope=all 就能看全公司申请」。
	CanApprove bool
	CanViewAll bool
}

type accessRequestListRow struct {
	ID                uint64                 `json:"id"`
	CreatedAt         commonModels.LocalTime `json:"created_at"`
	UpdatedAt         commonModels.LocalTime `json:"updated_at"`
	RequestNo         string                 `json:"request_no"`
	OrderID           string                 `json:"order_id"`
	OrderTitle        string                 `json:"order_title"`
	ApplicantUsername string                 `json:"applicant_username"`
	ApplicantName     string                 `json:"applicant_name"`
	InstanceID        string                 `json:"instance_id"`
	InstanceName      string                 `json:"instance_name"`
	// InstanceRemark 数据源名称（insight_instances.remark），申请单列表第一行展示
	InstanceRemark string `json:"instance_remark"`
	// InstanceAddr 数据源连接地址 host:port，申请单列表第二行灰字展示
	InstanceAddr string `json:"instance_addr"`
	DBType       string `json:"db_type"`
	Database     string `json:"database"`
	SchemaName   string `json:"schema_name"`
	// ResourceSpec 多资源圈选快照（多库 / 多模式 / 表维度），前端解析展示；
	// 为空表示旧单据，按 schema_name / table_name 单资源展示。
	ResourceSpec      string                  `json:"resource_spec"`
	ResourceLevel     string                  `json:"resource_level"`
	SQLType           string                  `json:"sql_type"`
	Actions           string                  `json:"actions"`
	Reason            string                  `json:"reason"`
	RequestedExpireAt *commonModels.LocalTime `json:"requested_expire_at"`
	Status            string                  `json:"status"`
	ApproverUsername  string                  `json:"approver_username"`
	ApproverName      string                  `json:"approver_name"`
	ApprovedAt        *commonModels.LocalTime `json:"approved_at"`
	ApproveComment    string                  `json:"approve_comment"`
	ApprovedExpireAt  *commonModels.LocalTime `json:"approved_expire_at"`
	ExecutorUsername  string                  `json:"executor_username"`
	ExecutorName      string                  `json:"executor_name"`
	ExecutedAt        *commonModels.LocalTime `json:"executed_at"`
	GrantNo           string                  `json:"grant_no"`
	EffectiveAt       *commonModels.LocalTime `json:"effective_at"`
	ExpireAt          *commonModels.LocalTime `json:"expire_at"`

	// GrantStatus 授权记录的当前状态（active/revoked/expired），来自授权表左连接。
	GrantStatus string `json:"grant_status"`
	// EffectiveStatus 页面实际展示的状态：审批状态 + 授权真实状态的合成。
	// 授权被撤销/过期时即便申请单还是 approved，也不能显示成「已通过」。
	EffectiveStatus string `json:"effective_status"`
}

func (s *ListAccessRequestsService) Run() (data any, total int64, err error) {
	tx := global.App.DB.Table("insight_das_access_requests r").
		Select(`r.id, r.created_at, r.updated_at, r.request_no, r.order_id, r.order_title,
		        r.applicant_username, r.applicant_name,
		        r.instance_id, r.instance_name,
		        coalesce(nullif(r.db_type, ''), ifnull(i.db_type, '')) as db_type,
		        r.database, r.schema_name, r.resource_spec, r.resource_level,
		        r.sql_type, r.actions, r.reason, r.requested_expire_at,
		        r.status, r.approver_username, r.approver_name, r.approved_at, r.approve_comment,
		        r.approved_expire_at, r.executor_username, r.executor_name, r.executed_at,
		        r.grant_no, r.effective_at, r.expire_at,
		        ifnull(g.status, '') as grant_status,
		        ifnull(i.remark, '') as instance_remark,
		        case when i.instance_id is null then ifnull(r.instance_name, '')
		             else concat(ifnull(i.hostname, ''), ':', ifnull(i.port, '')) end as instance_addr`).
		Joins("left join insight_das_data_grants g on g.grant_no = r.grant_no").
		Joins("left join insight_instances i on i.instance_id = r.instance_id")

	scope := strings.ToLower(strings.TrimSpace(s.Scope))
	switch scope {
	case "related":
		// 与我相关（默认视图）：我申请的 + 我处理过的 +（有审批权时）待我审批的
		cond := "r.applicant_username = ? OR r.approver_username = ?"
		args := []any{s.Username, s.Username}
		if s.CanApprove {
			// 待我审批 = 还没人接手的单（pending / forwarded），已批到我名下的也算
			cond += " OR (r.status IN ? AND r.applicant_username <> ?)"
			args = append(args, []string{AccessRequestStatusPending, AccessRequestStatusForwarded}, s.Username)
		}
		tx = tx.Where(cond, args...)
	case "pending":
		// 待我审批：排除自己提交的（自批在服务端也会被拦，这里只是不让它出现在待办里）
		if !s.CanApprove {
			return []accessRequestListRow{}, 0, nil
		}
		tx = tx.Where("r.status IN ? AND r.applicant_username <> ?",
			[]string{AccessRequestStatusPending, AccessRequestStatusForwarded}, s.Username)
	case "all":
		if !s.CanViewAll && !s.CanApprove {
			tx = tx.Where("r.applicant_username = ?", s.Username)
		}
	default: // mine
		tx = tx.Where("r.applicant_username = ?", s.Username)
	}

	if v := strings.TrimSpace(s.Status); v != "" {
		tx = tx.Where("r.status = ?", v)
	}
	if v := strings.TrimSpace(s.Search); v != "" {
		like := "%" + v + "%"
		tx = tx.Where("(r.request_no like ? or r.order_id like ? or r.order_title like ? or r.applicant_username like ? or r.instance_name like ? or i.remark like ? or r.schema_name like ?)",
			like, like, like, like, like, like, like)
	}

	var rows []accessRequestListRow
	total = pagination.Pager(&s.PaginationQ, tx.Order("r.id desc"), &rows)
	fillAccessRequestEffectiveStatus(rows)
	return rows, total, nil
}

// fillAccessRequestEffectiveStatus 合成页面展示状态
func fillAccessRequestEffectiveStatus(rows []accessRequestListRow) {
	now := time.Now()
	for i := range rows {
		rows[i].EffectiveStatus = rows[i].Status
		if rows[i].Status != AccessRequestStatusApproved {
			continue
		}
		switch rows[i].GrantStatus {
		case DataGrantStatusRevoked:
			rows[i].EffectiveStatus = AccessRequestStatusRevoked
		case DataGrantStatusExpired:
			rows[i].EffectiveStatus = AccessRequestStatusExpired
		default:
			if rows[i].ExpireAt != nil && !time.Time(*rows[i].ExpireAt).After(now) {
				rows[i].EffectiveStatus = AccessRequestStatusExpired
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 查询：申请单详情（含流转事件）
// ---------------------------------------------------------------------------

type accessRequestGrantBrief struct {
	GrantNo string `json:"grant_no"`
	// ResourceDesc 授权覆盖的资源（多资源申请单会拆成多条授权行，每条覆盖范围不同）
	ResourceDesc string                  `json:"resource_desc"`
	Actions      string                  `json:"actions"`
	Effect       string                  `json:"effect"`
	Status       string                  `json:"status"`
	GrantSource  string                  `json:"grant_source"`
	GrantorName  string                  `json:"grantor_name"`
	EffectiveAt  *commonModels.LocalTime `json:"effective_at"`
	ExpireAt     *commonModels.LocalTime `json:"expire_at"`
	RevokedAt    *commonModels.LocalTime `json:"revoked_at"`
	RevokedBy    string                  `json:"revoked_by"`
}

// AccessRequestPermissions 详情页可执行的操作。
//
// 由服务端算好再下发，而不是让前端各自拼条件：
// 前端的按钮灰置只是体验，真正的拦截在服务层（ensureCurrentApprover 等）；
// 两边各写一套条件必然漂移，最后表现为「按钮能点但点了报 403」。
type AccessRequestPermissions struct {
	// IsApplicant 当前用户是申请人
	IsApplicant bool `json:"is_applicant"`
	// IsCurrentApprover 当前用户是这份单子的当前处理人（转交后为接手人）
	IsCurrentApprover bool `json:"is_current_approver"`
	// CanApprove / CanReject / CanForward 审批三连：仅当前处理人（或管理员）
	CanApprove bool `json:"can_approve"`
	CanReject  bool `json:"can_reject"`
	CanForward bool `json:"can_forward"`
	// CanWithdraw / CanTerminate 申请人（或管理员）在审批中可用
	CanWithdraw  bool `json:"can_withdraw"`
	CanTerminate bool `json:"can_terminate"`
	// CanGrant 执行授权：待执行授权 + 有审批权 + 不是申请人自己
	CanGrant bool `json:"can_grant"`
	// CanCreateSimilar 类似创建：恒可用（以本单为模板发起新申请）
	CanCreateSimilar bool `json:"can_create_similar"`
}

type AccessRequestDetail struct {
	Request models.InsightDasAccessRequest        `json:"request"`
	Events  []models.InsightDasAccessRequestEvent `json:"events"`
	// Grants 本单落地的全部授权行（多资源申请单会有多条，按 request_no 关联）。
	// Grant 兼容字段保留第一条，旧消费方不破。
	Grant       *accessRequestGrantBrief  `json:"grant"`
	Grants      []accessRequestGrantBrief `json:"grants"`
	Permissions AccessRequestPermissions  `json:"permissions"`
}

type GetAccessRequestDetailService struct {
	RequestNo string
	Username  string
	CanView   bool // 有审批或全量查看权限时可看他人申请
	// CanApprove 是否持有 das.access.approve（决定能否执行授权）
	CanApprove bool
	// IsAdmin 管理员旁路（可处理任何人的单子）
	IsAdmin bool
}

// buildAccessRequestPermissions 计算详情页按钮可用性。
func buildAccessRequestPermissions(req *models.InsightDasAccessRequest, username string, canApprove, isAdmin bool) AccessRequestPermissions {
	perms := AccessRequestPermissions{CanCreateSimilar: true}
	if req == nil {
		return perms
	}
	perms.IsApplicant = strings.EqualFold(req.ApplicantUsername, username)
	perms.IsCurrentApprover = strings.EqualFold(req.ApproverUsername, username)
	inApproval := accessRequestInApproval(req.Status)
	// 自批自审一律不开放：申请人自己出现在审批位时，宁可不给按钮
	if (perms.IsCurrentApprover || isAdmin) && inApproval && !perms.IsApplicant {
		perms.CanApprove = true
		perms.CanReject = true
		perms.CanForward = true
	}
	if (perms.IsApplicant || isAdmin) && inApproval {
		perms.CanWithdraw = true
		perms.CanTerminate = true
	}
	perms.CanGrant = req.Status == AccessRequestStatusWaitGrant && canApprove && !perms.IsApplicant
	return perms
}

func (s *GetAccessRequestDetailService) Run() (*AccessRequestDetail, error) {
	var req models.InsightDasAccessRequest
	if err := global.App.DB.Where("request_no = ?", strings.TrimSpace(s.RequestNo)).Take(&req).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAccessRequestNotFound
		}
		return nil, err
	}
	if !s.CanView && !strings.EqualFold(req.ApplicantUsername, s.Username) {
		return nil, errors.New("无权查看该申请单")
	}
	var events []models.InsightDasAccessRequestEvent
	if err := global.App.DB.Where("request_no = ?", req.RequestNo).Order("id asc").Find(&events).Error; err != nil {
		return nil, err
	}
	detail := &AccessRequestDetail{
		Request:     req,
		Events:      events,
		Permissions: buildAccessRequestPermissions(&req, s.Username, s.CanApprove, s.IsAdmin),
	}
	// 授权行：优先按 request_no 取（多资源申请单 = 多条授权），取不到再按 grant_no 兜底（旧数据）。
	// OR 条件必须加括号：不加的话与 status 过滤组合会变成 (A AND B) OR C。
	gtx := global.App.DB.Where("status <> ?", DataGrantStatusExpired)
	if strings.TrimSpace(req.RequestNo) != "" {
		gtx = gtx.Where("(request_no = ? OR grant_no = ?)", req.RequestNo, req.GrantNo)
	} else {
		gtx = gtx.Where("grant_no = ?", req.GrantNo)
	}
	var grantRows []models.InsightDasDataGrant
	if err := gtx.Order("id asc").Find(&grantRows).Error; err == nil {
		briefs := make([]accessRequestGrantBrief, 0, len(grantRows))
		for i := range grantRows {
			g := grantRows[i]
			brief := accessRequestGrantBrief{
				GrantNo:      g.GrantNo,
				ResourceDesc: describeDataResource(g.InstanceID, g.SchemaName, g.Table),
				Actions:      g.Actions,
				Effect:       g.Effect,
				Status:       g.Status,
				GrantSource:  g.GrantSource,
				GrantorName:  g.GrantorName,
				EffectiveAt:  g.EffectiveAt,
				ExpireAt:     g.ExpireAt,
				RevokedAt:    g.RevokedAt,
				RevokedBy:    g.RevokedBy,
			}
			briefs = append(briefs, brief)
			if detail.Grant == nil {
				detail.Grant = &brief
			}
		}
		detail.Grants = briefs
	}
	return detail, nil
}
