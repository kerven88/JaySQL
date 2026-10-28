package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/common/models"
	usersSvc "github.com/simajiong/jaysql/internal/users/services"

	"github.com/simajiong/jaysql/internal/orders/forms"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/pkg/pagination"
	"github.com/simajiong/jaysql/pkg/utils"

	ordersModels "github.com/simajiong/jaysql/internal/orders/models"
	orderScopes "github.com/simajiong/jaysql/internal/orders/scopes"

	"github.com/gin-gonic/gin"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func normalizeOrderProgressByTasks(orderID string) {
	var counts struct {
		Failed    int64
		Executing int64
		Pending   int64
		Completed int64
		Total     int64
	}
	if err := global.App.DB.Table("`insight_order_tasks`").
		Select(`
			COUNT(*) AS total,
			SUM(CASE WHEN progress = 'FAILED' THEN 1 ELSE 0 END) AS failed,
			SUM(CASE WHEN progress = 'EXECUTING' THEN 1 ELSE 0 END) AS executing,
			SUM(CASE WHEN progress = 'PENDING' THEN 1 ELSE 0 END) AS pending,
			SUM(CASE WHEN progress = 'COMPLETED' THEN 1 ELSE 0 END) AS completed
		`).
		Where("order_id = ?", orderID).
		Scan(&counts).Error; err != nil {
		return
	}
	if counts.Total == 0 {
		return
	}
	nextProgress := ""
	switch {
	case counts.Failed > 0:
		nextProgress = "FAILED"
	case counts.Executing > 0:
		nextProgress = "EXECUTING"
	case counts.Pending == 0 && counts.Completed == counts.Total:
		nextProgress = "COMPLETED"
	}
	if nextProgress == "" {
		return
	}
	_ = global.App.DB.Table("`insight_order_records`").
		Where("order_id = ? AND progress <> ?", orderID, nextProgress).
		Update("progress", nextProgress).Error
}

type GetOrderListServices struct {
	*forms.GetOrderListForm
	C        *gin.Context
	Username string
}

func (s *GetOrderListServices) isSuperuser() bool {
	return usersSvc.IsSuperuserByUsername(s.Username)
}

func (s *GetOrderListServices) hasPermission(code string) bool {
	codes, err := usersSvc.GetPermissionCodesByUsername(s.Username)
	if err != nil {
		return false
	}
	for _, c := range codes {
		if c == "*" || c == code {
			return true
		}
	}
	return false
}

func (s *GetOrderListServices) canManageAllOrders() bool {
	return s.isSuperuser() || s.hasPermission("order.manage")
}

func parseOrderListTime(value string) (*time.Time, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return nil, nil
	}
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02",
		time.RFC3339,
	}
	for _, layout := range layouts {
		t, err := time.ParseInLocation(layout, raw, time.Local)
		if err == nil {
			if t.Year() < 2000 || t.Year() > 2099 {
				return nil, fmt.Errorf("时间范围仅支持 2000-2099")
			}
			return &t, nil
		}
	}
	return nil, fmt.Errorf("时间格式不合法: %s", raw)
}

func (s *GetOrderListServices) Run() (responseData any, total int64, err error) {
	type record struct {
		OrderID         string           `json:"order_id"`
		Title           string           `json:"title"`
		Remark          string           `json:"remark"`
		DBType          string           `json:"db_type"`
		InstanceID      string           `json:"instance_id"`
		InstanceName    string           `json:"instance_name"`
		InstanceHost    string           `json:"instance_host"`
		InstancePort    int              `json:"instance_port"`
		Database        string           `json:"database"`
		Schema          string           `json:"schema"`
		Applicant       string           `json:"applicant"`
		ApplicantName   string           `json:"applicant_name"`
		ApplicantAvatar string           `json:"applicant_avatar"`
		Executor        string           `json:"executor"`
		EstimatedRows   int64            `json:"estimated_affected_rows"`
		Content         string           `json:"content"`
		RollbackSQL     string           `json:"rollback_sql"`
		CC              datatypes.JSON   `json:"cc"`
		Progress        string           `json:"progress"`
		ExecutionMode   string           `json:"execution_mode"`
		ScheduledAt     models.LocalTime `json:"scheduled_at"`
		CreatedAt       models.LocalTime `json:"created_at"`
	}
	var records []record

	scope := strings.ToUpper(strings.TrimSpace(s.Scope))
	if scope == "" {
		// 未显式指定作用域（含「只看我的」勾选场景）时统一落到「与我相关」，
		// 与首页「我的资源 · SQL工单」卡口径一致。
		scope = orderScopes.ScopeMyRelated
	}

	startAt, err := parseOrderListTime(s.StartAt)
	if err != nil {
		return nil, 0, err
	}
	endAt, err := parseOrderListTime(s.EndAt)
	if err != nil {
		return nil, 0, err
	}

	tx := global.App.DB.Table("insight_order_records a").
		Select(`
			a.order_id,
			a.title,
			a.remark,
			a.db_type,
			a.instance_id,
			b.remark as instance_name,
			b.hostname as instance_host,
			b.port as instance_port,
			a.database,
			a.schema,
			a.applicant,
			u.username as applicant_name,
			u.avatar_file as applicant_avatar,
			a.executor,
			a.estimated_affected_rows,
			a.content,
			a.rollback_sql,
			a.cc,
			a.progress,
			a.execution_mode,
			a.scheduled_at,
			a.created_at
		`).
		Joins("left join insight_instances b on a.instance_id = b.instance_id").
		Joins("left join insight_users u on a.applicant = u.username").
		Joins("left join insight_approval_records ar on a.order_id = ar.order_id and ar.stage = 1").
		Order("a.created_at desc")

	// 风险详情等场景需要包含实例已失效/非工单类型库的工单，避免关闭记录与列表口径不一致。
	// 白名单与执行链支持范围保持一致（scopes.SupportedOrderDbTypes），
	// 硬编码 MySQL/PostgreSQL 曾导致 Oracle/TiDB/Doris 工单在列表页「凭空消失」。
	if !s.NoDbTypeFilter {
		tx = tx.Where("a.db_type in ? AND b.db_type in ?", orderScopes.SupportedOrderDbTypes, orderScopes.SupportedOrderDbTypes)
	}

	if strings.TrimSpace(s.InstanceID) != "" {
		id := strings.TrimSpace(s.InstanceID)
		if id == "" {
			return nil, 0, fmt.Errorf("数据源ID不能为空")
		}
		tx = tx.Where("a.instance_id = ?", id)
	}

	if startAt != nil {
		tx = tx.Where("a.created_at >= ?", *startAt)
	}
	if endAt != nil {
		tx = tx.Where("a.created_at <= ?", *endAt)
	}

	if strings.TrimSpace(s.Search) != "" {
		keyword := strings.TrimSpace(s.Search)
		like := "%" + keyword + "%"
		plain := strings.TrimPrefix(strings.ToLower(keyword), "tk-")
		plain = strings.ReplaceAll(plain, "-", "")
		plainLike := "%" + plain + "%"
		tx = tx.Where("(a.title like ? OR a.order_id like ? OR replace(a.order_id,'-','') like ?)", like, like, plainLike)
	}

	if strings.TrimSpace(s.Progress) != "" {
		tx = tx.Where("a.progress = ?", strings.TrimSpace(s.Progress))
	}
	if s.Environment > 0 {
		tx = tx.Where("a.environment=?", s.Environment)
	}

	if strings.TrimSpace(s.InstanceName) != "" {
		like := "%" + strings.TrimSpace(s.InstanceName) + "%"
		tx = tx.Where("(b.remark like ? OR b.hostname like ?)", like, like)
	}
	if strings.TrimSpace(s.Applicant) != "" {
		like := "%" + strings.TrimSpace(s.Applicant) + "%"
		tx = tx.Where("(a.applicant like ? OR u.username like ?)", like, like)
	}

	isSuperuser := s.canManageAllOrders()
	switch scope {
	case orderScopes.ScopeAll:
		// 非管理者没有全量视图，退化为「与我相关」——这是刻意的越权保护，
		// 而非多算：前端对无 order.manage 的用户已不展示「全部任务」选项（见 #5 修复）。
		if !isSuperuser {
			condition, args := orderScopes.RelatedCondition(s.Username)
			tx = tx.Where(condition, args...)
		}
	case orderScopes.ScopeMyRelated:
		condition, args := orderScopes.RelatedCondition(s.Username)
		tx = tx.Where(condition, args...)
	case orderScopes.ScopeMySubmitted:
		tx = tx.Where("a.applicant = ?", s.Username)
	case orderScopes.ScopeMyCC:
		tx = tx.Where("JSON_CONTAINS(IFNULL(a.cc, JSON_ARRAY()), JSON_QUOTE(?), '$')", s.Username)
	case orderScopes.ScopePendingMyApproval:
		tx = tx.Where("a.progress = ? AND ar.approver = ? AND ar.approval_status = ?", "PENDING", s.Username, "PENDING")
	case orderScopes.ScopeMyApproved:
		tx = tx.Where("ar.approver = ? AND ar.approval_status in ?", s.Username, []string{"APPROVED", "REJECTED"})
	case orderScopes.ScopePendingMyExecution:
		tx = tx.Where("a.executor = ? AND a.progress = ?", s.Username, "APPROVED")
	case orderScopes.ScopeMyExecuted:
		tx = tx.Where("a.executor = ? AND a.progress in ?", s.Username, []string{"COMPLETED", "FAILED"})
	default:
		condition, args := orderScopes.RelatedCondition(s.Username)
		tx = tx.Where(condition, args...)
	}

	total = pagination.Pager(&s.PaginationQ, tx, &records)
	return &records, total, nil
}

type GetOrderDetailServices struct {
	C        *gin.Context
	OrderID  string
	Username string
}

func (s *GetOrderDetailServices) convertToList(data datatypes.JSON) (users []string) {
	var usersList []map[string]string
	err := json.Unmarshal([]byte(data), &usersList)
	if err != nil {
		global.App.Log.Error("GetDetailServices.convertToList", err.Error())
		return
	}
	for _, entry := range usersList {
		users = append(users, entry["user"])
	}
	return
}

func (s *GetOrderDetailServices) Run() (responseData any, err error) {
	type orderExtras struct {
		EnvironmentLabel string
		InstanceHostPort string
		InstanceName     string
		CurrentApprover  string
	}

	var base ordersModels.InsightOrderRecords
	tx := global.App.DB.
		Where("order_id=?", s.OrderID).
		Take(&base)
	if tx.Error != nil {
		if errors.Is(tx.Error, gorm.ErrRecordNotFound) {
			return base, fmt.Errorf("记录`%s`不存在", s.OrderID)
		}
		return base, tx.Error
	}
	if tx.RowsAffected == 0 {
		return base, fmt.Errorf("记录`%s`不存在", s.OrderID)
	}

	// 放行 order.submit 后，无 order.view（且非管理者）的用户仅能读取自己提交的工单
	if base.Applicant != s.Username {
		canViewAll := usersSvc.IsSuperuserByUsername(s.Username)
		if !canViewAll {
			if codes, err := usersSvc.GetPermissionCodesByUsername(s.Username); err == nil {
				for _, c := range codes {
					if c == "*" || c == "order.manage" || c == "order.view" {
						canViewAll = true
						break
					}
				}
			}
		}
		if !canViewAll {
			return base, fmt.Errorf("记录`%s`不存在", s.OrderID)
		}
	}

	if base.Progress == "EXECUTING" || base.Progress == "APPROVED" {
		normalizeOrderProgressByTasks(s.OrderID)
		base.Progress = ""
		tx2 := global.App.DB.
			Where("order_id=?", s.OrderID).
			Take(&base)
		if tx2.Error != nil {
			if errors.Is(tx2.Error, gorm.ErrRecordNotFound) {
				return base, fmt.Errorf("记录`%s`不存在", s.OrderID)
			}
			return base, tx2.Error
		}
		if tx2.RowsAffected == 0 {
			return base, fmt.Errorf("记录`%s`不存在", s.OrderID)
		}
	}

	extras := orderExtras{}
	if base.InstanceID != "" {
		var inst struct {
			InstanceID string
			Hostname   string
			Port       int
			Remark     string
		}
		_ = global.App.DB.Table("insight_instances").
			Select("instance_id, hostname, port, remark").
			Where("instance_id=?", base.InstanceID).
			Take(&inst).Error
		if inst.Hostname != "" {
			extras.InstanceHostPort = fmt.Sprintf("%s:%d", inst.Hostname, inst.Port)
		}
		// 实例展示名：remark 优先，回落 instance_id（与监控模块 monitorInstanceDisplayName 口径一致）
		name := strings.TrimSpace(inst.Remark)
		if name == "" {
			name = inst.InstanceID
		}
		extras.InstanceName = name
	}
	if base.Environment != "" {
		extras.EnvironmentLabel = base.Environment
	}
	{
		var approver string
		_ = global.App.DB.Table("insight_approval_records").
			Select("approver").
			Where("order_id=? AND stage=1", s.OrderID).
			Limit(1).
			Pluck("approver", &approver).Error
		extras.CurrentApprover = approver
	}

	type detailRecord struct {
		ordersModels.InsightOrderRecords
		Instance        string `json:"instance"`
		InstanceName    string `json:"instance_name"`
		CurrentApprover string `json:"current_approver"`
	}
	detail := detailRecord{
		InsightOrderRecords: base,
		Instance:            extras.InstanceHostPort,
		InstanceName:        extras.InstanceName,
		CurrentApprover:     extras.CurrentApprover,
	}
	if extras.EnvironmentLabel != "" {
		detail.InsightOrderRecords.Environment = extras.EnvironmentLabel
	}

	if detail.IsRestrictAccess {
		var users []string = []string{detail.Applicant}
		users = append(users, s.convertToList(detail.Approver)...)
		users = append(users, s.convertToList(detail.Reviewer)...)
		users = append(users, s.convertToList(detail.CC)...)
		isMgr := usersSvc.IsSuperuserByUsername(s.Username)
		if !isMgr {
			if codes, err := usersSvc.GetPermissionCodesByUsername(s.Username); err == nil {
				for _, c := range codes {
					if c == "*" || c == "order.manage" {
						isMgr = true
						break
					}
				}
			}
		}
		if !utils.IsContain(users, s.Username) && !isMgr {
			detail.Content = "您没有权限查看当前工单内容"
		}
	}
	return detail, nil
}

type GetOrderApprovalsServices struct {
	C        *gin.Context
	OrderID  string
	Username string
}

func (s *GetOrderApprovalsServices) Run() (responseData any, err error) {
	var result []ordersModels.InsightApprovalRecords
	// 返回记录
	tx := global.App.DB.Table("`insight_approval_records` a").
		Where("a.order_id=?", s.OrderID).
		Scan(&result)
	if tx.Error != nil {
		return result, tx.Error
	}
	return result, nil
}

type GetOrderLogsServices struct {
	C       *gin.Context
	OrderID string
}

func (s *GetOrderLogsServices) Run() (responseData any, err error) {
	var result []ordersModels.InsightOrderLogs
	// 返回记录
	global.App.DB.Table("`insight_order_logs` a").
		Where("a.order_id=?", s.OrderID).
		Order("a.created_at desc").
		Scan(&result)
	return result, nil
}
