package services

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	dasModels "github.com/simajiong/jaysql/internal/das/models"
	dasServices "github.com/simajiong/jaysql/internal/das/services"
	"github.com/simajiong/jaysql/internal/global"
	orderModels "github.com/simajiong/jaysql/internal/orders/models"
	orderScopes "github.com/simajiong/jaysql/internal/orders/scopes"
	userServices "github.com/simajiong/jaysql/internal/users/services"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type HomeDashboardService struct {
	C             *gin.Context
	Username      string
	Environment   int
	permissionSet map[string]struct{}
	isSuperuser   bool
	dismissedMap  map[string]map[string]struct{}
}

func (s *HomeDashboardService) loadPermissions() error {
	codes, err := userServices.GetPermissionCodesByUsername(s.Username)
	if err != nil {
		return err
	}
	set := make(map[string]struct{}, len(codes))
	s.isSuperuser = false
	for _, c := range codes {
		if c == "*" {
			s.isSuperuser = true
			break
		}
		set[c] = struct{}{}
	}
	s.permissionSet = set
	return nil
}

func (s *HomeDashboardService) hasPerm(code string) bool {
	if s.isSuperuser {
		return true
	}
	if code == "" {
		return true
	}
	_, ok := s.permissionSet[code]
	return ok
}

func (s *HomeDashboardService) hasAnyPerm(codes ...string) bool {
	if s.isSuperuser {
		return true
	}
	for _, c := range codes {
		if _, ok := s.permissionSet[c]; ok {
			return true
		}
	}
	return false
}

type HomeEnvironmentOption struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
}

type HomeHero struct {
	Greeting string `json:"greeting"`
	Headline string `json:"headline"`
	Subtitle string `json:"subtitle"`
}

type HomeSummary struct {
	MyPendingOrders int64 `json:"my_pending_orders"`
	ExecutingOrders int64 `json:"executing_orders"`
	SQLQueriesToday int64 `json:"sql_queries_today"`
	BackupRunsToday int64 `json:"backup_runs_today"`
	FailedJobs      int64 `json:"failed_jobs"`
	RiskAlerts      int64 `json:"risk_alerts"`
}

type HomeTodoItem struct {
	Kind        string                 `json:"kind"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Status      string                 `json:"status"`
	Path        string                 `json:"path"`
	Query       map[string]interface{} `json:"query,omitempty"`
	CreatedAt   commonModels.LocalTime `json:"created_at"`
	sortAt      time.Time
}

type HomeRiskOverview struct {
	FailedBackups24h  int64 `json:"failed_backups_24h"`
	FailedRestores24h int64 `json:"failed_restores_24h"`
	OfflineNodes      int64 `json:"offline_nodes"`
	EnabledTasks      int64 `json:"enabled_tasks"`
}

type HomeWorkbenchRiskCounts struct {
	InstanceConn     int64 `json:"instance_conn"`
	ManualBackupFail int64 `json:"manual_backup_fail"`
	RestoreFail      int64 `json:"restore_fail"`
	NodeOffline      int64 `json:"node_offline"`
	InstanceNoBackup int64 `json:"instance_no_backup"`
	DailyBackupMiss  int64 `json:"daily_backup_miss"`
	OrderFail        int64 `json:"order_fail"`
	Total            int64 `json:"total"`
}

type HomeWorkbenchTodoCounts struct {
	OrderSubmit  int64 `json:"order_submit"`
	OrderApprove int64 `json:"order_approve"`
	OrderExecute int64 `json:"order_execute"`
	BackupEnable int64 `json:"backup_enable"`
	Total        int64 `json:"total"`
}

// ---- 首页「我的资源」卡片 ----
// 三张卡片的计数口径（全部为真实统计值，无数据即 0）：
//
//	instances：normal = last_check_status='success'，abnormal = 其余（两者相加恒等于 total）
//	backup   ：success = 该任务最近一次运行 SUCCESS；abnormal = 最近一次运行 FAILED；total = 未删除任务数
//	            （只取「最近一次运行」，所以 success + abnormal ≤ total：从未跑过或正在跑的任务不计入两者）
//	orders   ：pending = 尚未开始执行的进行中工单；success = 执行成功(COMPLETED)；
//	            failed = 执行失败(FAILED)；total = 全部工单
type HomeInstanceResource struct {
	Normal   int64 `json:"normal"`
	Abnormal int64 `json:"abnormal"`
	Total    int64 `json:"total"`
}

type HomeBackupResource struct {
	Success  int64 `json:"success"`
	Abnormal int64 `json:"abnormal"`
	Total    int64 `json:"total"`
}

type HomeOrderResource struct {
	Pending int64 `json:"pending"`
	// Rejected 对应工单进度 REJECTED（审批驳回）。首页「我的资源 / SQL工单」卡片当前展示的是它，
	// 替代了原来的「执行成功」列。
	Rejected int64 `json:"rejected"`
	Success  int64 `json:"success"`
	Failed   int64 `json:"failed"`
	Total    int64 `json:"total"`
}

type HomeResources struct {
	Instances HomeInstanceResource `json:"instances"`
	Backup    HomeBackupResource   `json:"backup"`
	Orders    HomeOrderResource    `json:"orders"`
}

type HomeDashboardResponse struct {
	Environments []HomeEnvironmentOption `json:"environments"`
	Hero         HomeHero                `json:"hero"`
	Summary      HomeSummary             `json:"summary"`
	TodoItems    []HomeTodoItem          `json:"todo_items"`
	Risks        HomeRiskOverview        `json:"risks"`
	RiskCounts   HomeWorkbenchRiskCounts `json:"risk_counts"`
	TodoCounts   HomeWorkbenchTodoCounts `json:"todo_counts"`
	Resources    HomeResources           `json:"resources"`
}

func (s *HomeDashboardService) Run() (HomeDashboardResponse, error) {
	resp := HomeDashboardResponse{}

	if err := s.loadPermissions(); err != nil {
		return resp, err
	}
	if err := s.loadDismissed(); err != nil {
		s.dismissedMap = make(map[string]map[string]struct{})
	}
	if err := s.loadEnvironments(&resp); err != nil {
		return resp, err
	}
	if err := s.loadSummary(&resp); err != nil {
		return resp, err
	}
	if err := s.loadTodoItems(&resp); err != nil {
		return resp, err
	}
	if err := s.loadRisks(&resp); err != nil {
		return resp, err
	}
	if err := s.loadWorkbenchCounts(&resp); err != nil {
		return resp, err
	}
	if err := s.loadResources(&resp); err != nil {
		return resp, err
	}

	resp.Hero = s.buildHero(resp.Summary)
	return resp, nil
}

// loadResources 汇总首页「我的资源」三张卡片的数量。
//
// 口径统一（2026-10-13）：三张卡全部改为「与我相关 / 我可见」，即首页作为个人工作台，
// 卡片上的数字必须等于点进去后看到的清单：
//   - 数据源   = 我有数据访问权限的实例（与 SQL 工作台左栏数据源树同源）
//   - 备份任务 = 我创建的任务（与备份中心「只看我创建的」筛选同源）
//   - SQL 工单 = 与我相关的工单（与工单列表默认筛选同源）
//
// 历史教训：此前三张卡都统计平台全量，而工单列表默认「与我相关」+ 过滤实例已删除的记录，
// 于是出现「首页 8 条、列表 3 条」的口径分裂。数字一致靠「复用同一个查询构造器」保证，
// 而不是靠两处分别写 SQL 后人工同步。
func (s *HomeDashboardService) loadResources(resp *HomeDashboardResponse) error {
	// 数据源卡：口径与「我能看到那份数据源清单」的页面保持一致，两类角色看到的清单本身不同：
	//   - 能打开实例配置页/监控页（view 级）→ 该页对所有人都展示全量，卡片 = 全量（按环境过滤）
	//   - 只能通过 SQL 工作台用库的开发者 → 卡片 = 我有数据访问权限的实例（与左栏数据源树同源）
	// 若统一按 DAS 口径，「有实例页权限但没有 DAS 授权」的角色（如只读/审计）会出现
	// 首页显示 0、实例页显示 N 的新口径分裂 —— 正是本次要消灭的问题类型。
	if s.hasAnyPerm("system.instance.view", "system.instance.manage", "monitor.view", "monitor.manage") {
		instTx := global.App.DB.Table(commonModels.InsightInstances{}.TableName())
		if s.Environment > 0 {
			instTx = instTx.Where("environment = ?", s.Environment)
		}
		if err := instTx.Count(&resp.Resources.Instances.Total).Error; err != nil {
			return err
		}
		normalTx := global.App.DB.Table(commonModels.InsightInstances{}.TableName()).
			Where("last_check_status = ?", "success")
		if s.Environment > 0 {
			normalTx = normalTx.Where("environment = ?", s.Environment)
		}
		if err := normalTx.Count(&resp.Resources.Instances.Normal).Error; err != nil {
			return err
		}
		resp.Resources.Instances.Abnormal = resp.Resources.Instances.Total - resp.Resources.Instances.Normal
	} else if s.hasPerm("sql.console.access") {
		stats, err := dasServices.CountAccessibleInstances(s.Username, s.Environment)
		if err != nil {
			// 数据访问权限侧异常不应让整个首页 500：降级为 0 并记日志，便于排查
			global.App.Log.Errorf("home dashboard: count accessible instances failed: %v", err)
		} else {
			resp.Resources.Instances.Total = stats.Total
			resp.Resources.Instances.Normal = stats.Normal
			resp.Resources.Instances.Abnormal = stats.Abnormal
		}
	}

	// 备份任务卡：只统计「我创建的」任务，与备份中心「只看我创建的」筛选口径一致。
	if s.hasPerm("backup.task.view") || s.hasPerm("backup.task.manage") {
		backupBase := func() *gorm.DB {
			tx := global.App.DB.Table(backupModels.InsightBackupTasks{}.TableName()).
				Where("deleted_at IS NULL")
			if strings.TrimSpace(s.Username) != "" {
				tx = tx.Where("created_by = ?", s.Username)
			}
			return tx
		}
		if err := backupBase().Count(&resp.Resources.Backup.Total).Error; err != nil {
			return err
		}
		// 每个任务只取其最近一次运行的状态：最新一次成功算"成功"，最新一次失败才算"异常"
		type backupLatestRow struct {
			Status string
		}
		var latestRows []backupLatestRow
		latestTx := global.App.DB.
			Table(backupModels.InsightBackupTasks{}.TableName() + " t").
			Select("IFNULL(r.status, '') AS status").
			Joins("LEFT JOIN " + backupModels.InsightBackupRuns{}.TableName() + " r ON r.id = (" +
				"SELECT MAX(r2.id) FROM " + backupModels.InsightBackupRuns{}.TableName() + " r2 WHERE r2.task_id = t.task_id)").
			Where("t.deleted_at IS NULL")
		if strings.TrimSpace(s.Username) != "" {
			latestTx = latestTx.Where("t.created_by = ?", s.Username)
		}
		if err := latestTx.Scan(&latestRows).Error; err != nil {
			return err
		}
		for _, row := range latestRows {
			switch strings.ToUpper(strings.TrimSpace(row.Status)) {
			case "SUCCESS":
				resp.Resources.Backup.Success++
			case "FAILED":
				resp.Resources.Backup.Abnormal++
			}
		}
	}

	// SQL 工单卡：与工单列表默认筛选完全同源（scope=MY_RELATED，且同样过滤实例已删除的记录），
	// 直接用列表侧的对齐构造器，从代码层面消灭双份 where 造成的口径漂移。
	if s.hasPerm("order.view") {
		if err := s.orderListAlignedBaseQuery(orderScopes.ScopeMyRelated, true).Count(&resp.Resources.Orders.Total).Error; err != nil {
			return err
		}
		if err := s.orderListAlignedBaseQuery(orderScopes.ScopeMyRelated, true).
			Where("a.progress IN ?", []string{"WAIT_PRECHECK", "PENDING", "APPROVED"}).
			Count(&resp.Resources.Orders.Pending).Error; err != nil {
			return err
		}
		// 审批驳回：卡片第二列，口径与工单列表页的「审批驳回」筛选一致
		if err := s.orderListAlignedBaseQuery(orderScopes.ScopeMyRelated, true).
			Where("a.progress = ?", "REJECTED").
			Count(&resp.Resources.Orders.Rejected).Error; err != nil {
			return err
		}
		// 执行成功：卡片已换成「审批驳回」，这里保留字段供接口兼容，前端不再展示
		if err := s.orderListAlignedBaseQuery(orderScopes.ScopeMyRelated, true).
			Where("a.progress = ?", "COMPLETED").
			Count(&resp.Resources.Orders.Success).Error; err != nil {
			return err
		}
		if err := s.orderListAlignedBaseQuery(orderScopes.ScopeMyRelated, true).
			Where("a.progress = ?", "FAILED").
			Count(&resp.Resources.Orders.Failed).Error; err != nil {
			return err
		}
	}

	return nil
}

func (s *HomeDashboardService) loadDismissed() error {
	svc := RiskDismissedService{
		C:        s.C,
		Username: s.Username,
	}
	riskTypes := []string{
		"instance_conn", "manual_backup_fail", "restore_fail", "node_offline",
		"instance_no_backup", "daily_backup_miss", "order_fail",
		"todo_order_submit", "todo_order_approve", "todo_order_execute", "todo_backup_enable",
	}
	data, err := svc.GetDismissedKeys(riskTypes)
	if err != nil {
		return err
	}
	s.dismissedMap = data
	return nil
}

func (s *HomeDashboardService) isDismissed(riskType, riskKey string) bool {
	if s.dismissedMap == nil {
		return false
	}
	m, ok := s.dismissedMap[strings.TrimSpace(riskType)]
	if !ok {
		return false
	}
	_, ok = m[strings.TrimSpace(riskKey)]
	return ok
}

func (s *HomeDashboardService) dismissedCount(riskType string) int64 {
	if s.dismissedMap == nil {
		return 0
	}
	m, ok := s.dismissedMap[strings.TrimSpace(riskType)]
	if !ok {
		return 0
	}
	return int64(len(m))
}

func dayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func (s *HomeDashboardService) loadEnvironments(resp *HomeDashboardResponse) error {
	return global.App.DB.Table(commonModels.InsightInstanceEnvironments{}.TableName()).
		Select("id, name").
		Order("id asc").
		Scan(&resp.Environments).Error
}

func (s *HomeDashboardService) loadSummary(resp *HomeDashboardResponse) error {
	startOfDay := dayStart(time.Now())
	window24h := time.Now().Add(-24 * time.Hour)
	canManageBackup := s.hasPerm("backup.task.manage")
	canExecuteRestore := s.hasPerm("backup.restore.execute")

	if err := s.orderBaseQuery().
		Where("a.applicant = ? AND a.progress IN ?", s.Username, []string{"PENDING", "APPROVED", "CLAIMED", "EXECUTING", "FAILED"}).
		Count(&resp.Summary.MyPendingOrders).Error; err != nil {
		return err
	}
	if err := s.orderBaseQuery().
		Where("a.applicant = ? AND a.progress = ?", s.Username, "EXECUTING").
		Count(&resp.Summary.ExecutingOrders).Error; err != nil {
		return err
	}
	if err := s.queryBase().
		Where("a.created_at >= ?", startOfDay).
		Count(&resp.Summary.SQLQueriesToday).Error; err != nil {
		return err
	}
	if canManageBackup {
		if err := global.App.DB.Table(backupModels.InsightBackupRuns{}.TableName()).
			Where("created_at >= ?", startOfDay).
			Count(&resp.Summary.BackupRunsToday).Error; err != nil {
			return err
		}
	} else {
		resp.Summary.BackupRunsToday = 0
	}

	var failedOrders int64
	var failedBackups int64
	var failedRestores int64
	var offlineNodes int64

	if err := s.orderBaseQuery().
		Where("a.applicant = ? AND a.progress = ? AND a.created_at >= ?", s.Username, "FAILED", startOfDay).
		Count(&failedOrders).Error; err != nil {
		return err
	}
	if canManageBackup {
		if err := global.App.DB.Table(backupModels.InsightBackupRuns{}.TableName()).
			Where("status = ? AND created_at >= ?", "FAILED", startOfDay).
			Count(&failedBackups).Error; err != nil {
			return err
		}
	}
	if canExecuteRestore {
		if err := global.App.DB.Table(backupModels.InsightRestoreRuns{}.TableName()).
			Where("status = ? AND created_at >= ?", "FAILED", startOfDay).
			Count(&failedRestores).Error; err != nil {
			return err
		}
	}
	if canManageBackup {
		if err := global.App.DB.Table(backupModels.InsightBackupNodes{}.TableName()).
			Where("status = ?", "OFFLINE").
			Count(&offlineNodes).Error; err != nil {
			return err
		}
	}

	resp.Summary.FailedJobs = failedOrders + failedBackups + failedRestores

	var failedBackups24h int64
	var failedRestores24h int64
	if canManageBackup {
		if err := global.App.DB.Table(backupModels.InsightBackupRuns{}.TableName()).
			Where("status = ? AND created_at >= ?", "FAILED", window24h).
			Count(&failedBackups24h).Error; err != nil {
			return err
		}
	}
	if canExecuteRestore {
		if err := global.App.DB.Table(backupModels.InsightRestoreRuns{}.TableName()).
			Where("status = ? AND created_at >= ?", "FAILED", window24h).
			Count(&failedRestores24h).Error; err != nil {
			return err
		}
	}
	resp.Summary.RiskAlerts = failedBackups24h + failedRestores24h + offlineNodes

	return nil
}

func (s *HomeDashboardService) loadTodoItems(resp *HomeDashboardResponse) error {
	type orderRow struct {
		Title           string                 `json:"title"`
		OrderID         string                 `json:"order_id"`
		Progress        string                 `json:"progress"`
		EnvironmentName string                 `json:"environment_name"`
		InstanceName    string                 `json:"instance_name"`
		CreatedAt       commonModels.LocalTime `json:"created_at"`
	}

	type backupRow struct {
		TaskName     string                 `json:"task_name"`
		Status       string                 `json:"status"`
		ErrorMessage string                 `json:"error_message"`
		CreatedAt    commonModels.LocalTime `json:"created_at"`
	}

	type restoreRow struct {
		TargetDBName string                 `json:"target_db_name"`
		Status       string                 `json:"status"`
		ErrorMessage string                 `json:"error_message"`
		CreatedAt    commonModels.LocalTime `json:"created_at"`
	}
	canManageBackup := s.hasPerm("backup.task.manage")
	canExecuteRestore := s.hasPerm("backup.restore.execute")

	var orderRows []orderRow
	if err := s.orderBaseQuery().
		Select(`
			a.title,
			a.order_id,
			a.progress,
			COALESCE(env.name, a.environment, '-') as environment_name,
			COALESCE(CONCAT(inst.hostname, ':', inst.port), '-') as instance_name,
			a.created_at
		`).
		Joins("left join insight_instance_environments env on a.environment = env.id").
		Joins("left join insight_instances inst on a.instance_id = inst.instance_id").
		Where("a.applicant = ? AND a.progress IN ?", s.Username, []string{"PENDING", "APPROVED", "CLAIMED", "EXECUTING", "FAILED"}).
		Order("a.created_at desc").
		Limit(5).
		Scan(&orderRows).Error; err != nil {
		return err
	}

	var backupRows []backupRow
	if canManageBackup {
		if err := global.App.DB.Table(backupModels.InsightBackupRuns{}.TableName()+" r").
			Select(`
				COALESCE(t.name, CONCAT('备份任务 ', r.task_id)) as task_name,
				r.status,
				r.error_message,
				r.created_at
			`).
			Joins("left join insight_backup_tasks t on r.task_id = t.id").
			Where("r.status = ?", "FAILED").
			Order("r.created_at desc").
			Limit(2).
			Scan(&backupRows).Error; err != nil {
			return err
		}
	}

	var restoreRows []restoreRow
	if canExecuteRestore {
		if err := global.App.DB.Table(backupModels.InsightRestoreRuns{}.TableName()).
			Select("target_db_name, status, error_message, created_at").
			Where("status = ?", "FAILED").
			Order("created_at desc").
			Limit(2).
			Scan(&restoreRows).Error; err != nil {
			return err
		}
	}

	items := make([]HomeTodoItem, 0, len(orderRows)+len(backupRows)+len(restoreRows))
	for _, row := range orderRows {
		createdAt := time.Time(row.CreatedAt)
		items = append(items, HomeTodoItem{
			Kind:        "order",
			Title:       row.Title,
			Description: fmt.Sprintf("%s · %s", emptyFallback(row.EnvironmentName, "-"), emptyFallback(row.InstanceName, "-")),
			Status:      row.Progress,
			Path:        "/orders/" + row.OrderID,
			CreatedAt:   row.CreatedAt,
			sortAt:      createdAt,
		})
	}
	for _, row := range backupRows {
		createdAt := time.Time(row.CreatedAt)
		items = append(items, HomeTodoItem{
			Kind:        "backup",
			Title:       row.TaskName,
			Description: trimText(emptyFallback(row.ErrorMessage, "备份任务执行失败，请尽快处理"), 80),
			Status:      row.Status,
			Path:        "/backup/task",
			CreatedAt:   row.CreatedAt,
			sortAt:      createdAt,
		})
	}
	for _, row := range restoreRows {
		createdAt := time.Time(row.CreatedAt)
		items = append(items, HomeTodoItem{
			Kind:        "restore",
			Title:       "恢复任务 · " + emptyFallback(row.TargetDBName, "未命名目标库"),
			Description: trimText(emptyFallback(row.ErrorMessage, "恢复任务执行失败，请检查日志"), 80),
			Status:      row.Status,
			Path:        "/backup/restore",
			CreatedAt:   row.CreatedAt,
			sortAt:      createdAt,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].sortAt.After(items[j].sortAt)
	})
	if len(items) > 6 {
		items = items[:6]
	}
	resp.TodoItems = items
	return nil
}

func (s *HomeDashboardService) loadRisks(resp *HomeDashboardResponse) error {
	window24h := time.Now().Add(-24 * time.Hour)
	canManageBackup := s.hasPerm("backup.task.manage")
	canExecuteRestore := s.hasPerm("backup.restore.execute")

	if canManageBackup {
		if err := global.App.DB.Table(backupModels.InsightBackupRuns{}.TableName()).
			Where("status = ? AND created_at >= ?", "FAILED", window24h).
			Count(&resp.Risks.FailedBackups24h).Error; err != nil {
			return err
		}
	} else {
		resp.Risks.FailedBackups24h = 0
	}
	if canExecuteRestore {
		if err := global.App.DB.Table(backupModels.InsightRestoreRuns{}.TableName()).
			Where("status = ? AND created_at >= ?", "FAILED", window24h).
			Count(&resp.Risks.FailedRestores24h).Error; err != nil {
			return err
		}
	} else {
		resp.Risks.FailedRestores24h = 0
	}
	if canManageBackup {
		if err := global.App.DB.Table(backupModels.InsightBackupNodes{}.TableName()).
			Where("status = ?", "OFFLINE").
			Count(&resp.Risks.OfflineNodes).Error; err != nil {
			return err
		}
	} else {
		resp.Risks.OfflineNodes = 0
	}
	if canManageBackup {
		if err := global.App.DB.Table(backupModels.InsightBackupTasks{}.TableName()).
			Where("enabled = ?", true).
			Count(&resp.Risks.EnabledTasks).Error; err != nil {
			return err
		}
	} else {
		resp.Risks.EnabledTasks = 0
	}
	return nil
}

func (s *HomeDashboardService) buildHero(summary HomeSummary) HomeHero {
	now := time.Now()
	greeting := "你好"
	switch hour := now.Hour(); {
	case hour < 12:
		greeting = "早上好"
	case hour < 18:
		greeting = "下午好"
	default:
		greeting = "晚上好"
	}

	headline := fmt.Sprintf("%s，%s", greeting, emptyFallback(s.Username, "同学"))
	subtitle := fmt.Sprintf(
		"当前有 %d 条待处理事项，%d 个风险项需要关注。优先处理待办，再进入 SQL、工单和备份恢复操作。",
		summary.MyPendingOrders,
		summary.RiskAlerts,
	)

	return HomeHero{
		Greeting: greeting,
		Headline: headline,
		Subtitle: subtitle,
	}
}

func (s *HomeDashboardService) orderBaseQuery() *gorm.DB {
	tx := global.App.DB.Table(orderModels.InsightOrderRecords{}.TableName() + " a")
	if s.Environment > 0 {
		tx = tx.Where("a.environment = ?", s.Environment)
	}
	return tx
}

func (s *HomeDashboardService) orderListAlignedBaseQuery(scope string, filterDbType bool) *gorm.DB {
	tx := global.App.DB.Table(orderModels.InsightOrderRecords{}.TableName() + " a").
		Joins("left join insight_users u on a.applicant = u.username").
		Joins("left join insight_approval_records ar on a.order_id = ar.order_id and ar.stage = 1")
	if filterDbType {
		// 工单列表页等场景只展示实例仍存在且 db_type 有效的记录；
		// 风险统计时需要包含实例已失效的工单，避免已关闭记录口径不一致导致数量被错误减成 0。
		// 白名单与工单列表页同源（scopes.SupportedOrderDbTypes），两处口径必须一致。
		tx = tx.
			Joins("left join insight_instances b on a.instance_id = b.instance_id").
			Where("a.db_type in ? AND b.db_type in ?", orderScopes.SupportedOrderDbTypes, orderScopes.SupportedOrderDbTypes)
	}
	if s.Environment > 0 {
		tx = tx.Where("a.environment = ?", s.Environment)
	}
	scope = strings.ToUpper(strings.TrimSpace(scope))
	switch scope {
	case orderScopes.ScopeAll:
		// 非管理者没有全量视图，退化为「与我相关」（与工单列表页同源判定）。
		if !(s.isSuperuser || s.hasPerm("order.manage")) {
			condition, args := orderScopes.RelatedCondition(s.Username)
			tx = tx.Where(condition, args...)
		}
	case orderScopes.ScopeMyRelated, "":
		condition, args := orderScopes.RelatedCondition(s.Username)
		tx = tx.Where(condition, args...)
	case orderScopes.ScopeMySubmitted, "MY_SUBMIT":
		tx = tx.Where("a.applicant = ?", s.Username)
	case orderScopes.ScopePendingMyApproval:
		tx = tx.Where("a.progress = ? AND ar.approver = ? AND ar.approval_status = ?", "PENDING", s.Username, "PENDING")
	case orderScopes.ScopePendingMyExecution:
		tx = tx.Where("a.executor = ? AND a.progress = ?", s.Username, "APPROVED")
	}
	return tx
}

func (s *HomeDashboardService) queryBase() *gorm.DB {
	tx := global.App.DB.Table(dasModels.InsightDASRecords{}.TableName()+" a").
		Where("a.username = ?", s.Username)
	if s.Environment > 0 {
		tx = tx.Joins("left join insight_instances inst_filter on a.instance_id = inst_filter.instance_id").
			Where("inst_filter.environment = ?", s.Environment)
	}
	return tx
}

func (s *HomeDashboardService) riskKeyInstanceConn(id uint64, lastCheckedAt *commonModels.LocalTime, lastCheckStatus string) string {
	return fmt.Sprintf("instance_conn:%d", id)
}

func (s *HomeDashboardService) riskKeyManualBackupFail(taskID string, lastRunID *uint64) string {
	if lastRunID != nil {
		return fmt.Sprintf("manual_backup_fail:%s:%d", taskID, *lastRunID)
	}
	return fmt.Sprintf("manual_backup_fail:%s", taskID)
}

func (s *HomeDashboardService) riskKeyRestoreFail(id uint64, updatedAt commonModels.LocalTime) string {
	return fmt.Sprintf("restore_fail:%d", id)
}

func (s *HomeDashboardService) riskKeyNodeOffline(id uint64, status string, lastHeartbeat *commonModels.LocalTime) string {
	return fmt.Sprintf("node_offline:%d", id)
}

func (s *HomeDashboardService) riskKeyInstanceNoBackup(id uint64, updatedAt commonModels.LocalTime) string {
	return fmt.Sprintf("instance_no_backup:%d", id)
}

func (s *HomeDashboardService) riskKeyDailyBackupMiss(taskID string, day string) string {
	return fmt.Sprintf("daily_backup_miss:%s:%s", taskID, day)
}

func (s *HomeDashboardService) riskKeyOrderFail(orderID string, createdAt commonModels.LocalTime, updatedAt commonModels.LocalTime) string {
	return fmt.Sprintf("order_fail:%s", orderID)
}

func (s *HomeDashboardService) loadWorkbenchCounts(resp *HomeDashboardResponse) error {
	canManageInstance := s.hasAnyPerm("system.instance.manage", "monitor.manage")
	canManageBackup := s.hasPerm("backup.task.manage")
	canExecuteRestore := s.hasPerm("backup.restore.execute")
	canViewOrder := s.hasPerm("order.view")

	var (
		instanceConnTotal     int64
		manualBackupFailTotal int64
		restoreFailTotal      int64
		nodeOfflineTotal      int64
		instanceNoBackupTotal int64
		dailyBackupMissTotal  int64
		orderFailTotal        int64
		orderSubmitTotal      int64
		orderApproveTotal     int64
		orderExecuteTotal     int64
		backupEnableTotal     int64
	)

	type idRow struct {
		ID              uint64
		LastCheckedAt   *commonModels.LocalTime
		LastCheckStatus string
	}
	type nodeRow struct {
		ID            uint64
		Status        commonModels.EnumType
		LastHeartbeat *commonModels.LocalTime
	}
	type restoreRow struct {
		ID        uint64
		UpdatedAt commonModels.LocalTime
	}
	type taskMetaRow struct {
		ID               uint64
		TaskID           string
		Enabled          bool
		SourceInstanceID uint64
		ScheduleDays     string
		ScheduleTime     string
		UpdatedAt        commonModels.LocalTime
	}
	type instanceRow struct {
		ID        uint64
		UpdatedAt commonModels.LocalTime
	}
	type orderRow struct {
		OrderID   string
		CreatedAt commonModels.LocalTime
		UpdatedAt commonModels.LocalTime
	}
	type simpleID struct {
		ID uint64
	}
	_ = simpleID{}

	window24h := time.Now().Add(-24 * time.Hour)
	day := dayStart(time.Now())
	sanitizeDismissed := func(riskType string, totalRows int) map[string]struct{} {
		if s.dismissedMap == nil {
			return nil
		}
		dm, ok := s.dismissedMap[riskType]
		if !ok || len(dm) == 0 {
			return nil
		}
		if len(dm) > 0 && totalRows >= 0 && len(dm) >= totalRows+8 {
			return nil
		}
		return dm
	}
	_ = day

	deriveTriggerMode := func(createdBy string) string {
		if strings.EqualFold(strings.TrimSpace(createdBy), "system") {
			return "SCHEDULED"
		}
		return "MANUAL"
	}

	if canManageInstance {
		var icRows []idRow
		if err := global.App.DB.Table(commonModels.InsightInstances{}.TableName()).
			Select("id, last_checked_at, last_check_status").
			Where("last_check_status = ?", "error").
			Scan(&icRows).Error; err != nil {
			return err
		}
		icDismissed := sanitizeDismissed("instance_conn", len(icRows))
		for _, r := range icRows {
			key := s.riskKeyInstanceConn(r.ID, r.LastCheckedAt, r.LastCheckStatus)
			if icDismissed != nil {
				if _, ok := icDismissed[key]; ok {
					continue
				}
			}
			instanceConnTotal++
		}
	}
	if canManageBackup {
		var taskMeta []taskMetaRow
		if err := global.App.DB.Table(backupModels.InsightBackupTasks{}.TableName()).
			Select("id, task_id, enabled, source_instance_id, IFNULL(schedule_days,'') as schedule_days, IFNULL(schedule_time,'') as schedule_time, updated_at").
			Where("deleted_at IS NULL").
			Scan(&taskMeta).Error; err != nil {
			return err
		}

		latestRunSub := global.App.DB.Table(backupModels.InsightBackupRuns{}.TableName() + " br").
			Select("br.status AS lr_status, br.start_at AS lr_start_at, br.end_at AS lr_end_at, br.id AS lr_run_id, br.created_by AS lr_created_by, br.task_id AS lr_task_id").
			Joins("JOIN (SELECT task_id, MAX(id) AS max_id FROM " + backupModels.InsightBackupRuns{}.TableName() + " GROUP BY task_id) t ON br.id = t.max_id")
		type rawRunRow struct {
			TaskID    string
			Status    string
			StartAt   *commonModels.LocalTime
			EndAt     *commonModels.LocalTime
			RunID     uint64
			CreatedBy string
		}
		var rawRuns []rawRunRow
		if err := global.App.DB.Table("(?) AS r", latestRunSub).
			Select("r.lr_task_id AS task_id, IFNULL(r.lr_status,'') AS status, r.lr_start_at AS start_at, r.lr_end_at AS end_at, r.lr_run_id AS run_id, IFNULL(r.lr_created_by,'') AS created_by").
			Scan(&rawRuns).Error; err != nil {
			return err
		}
		runByTaskID := make(map[string]rawRunRow, len(rawRuns))
		for _, r := range rawRuns {
			runByTaskID[r.TaskID] = r
		}
		type taskRunsJoinRow struct {
			TaskMetaID      uint64
			TaskMetaTaskID  string
			LatestRunID     uint64
			LatestStatus    string
			LatestStartAt   *commonModels.LocalTime
			LatestEndAt     *commonModels.LocalTime
			LatestCreatedBy string
		}
		latestRunMap := make(map[uint64]taskRunsJoinRow, len(taskMeta))
		for _, t := range taskMeta {
			lr := taskRunsJoinRow{TaskMetaID: t.ID, TaskMetaTaskID: t.TaskID}
			if t.TaskID != "" {
				if rr, ok := runByTaskID[t.TaskID]; ok {
					lr.LatestRunID = rr.RunID
					lr.LatestStatus = rr.Status
					lr.LatestStartAt = rr.StartAt
					lr.LatestEndAt = rr.EndAt
					lr.LatestCreatedBy = rr.CreatedBy
				}
			}
			latestRunMap[t.ID] = lr
		}

		manualDismissed := sanitizeDismissed("manual_backup_fail", len(taskMeta))
		taskRows := taskMeta
		for _, t := range taskMeta {
			lr := latestRunMap[t.ID]
			ltm := strings.ToUpper(strings.TrimSpace(deriveTriggerMode(lr.LatestCreatedBy)))
			ls := strings.ToUpper(strings.TrimSpace(lr.LatestStatus))
			if ltm != "MANUAL" {
				continue
			}
			if ls != "FAILED" {
				continue
			}
			if lr.LatestStartAt != nil {
				tt := time.Time(*lr.LatestStartAt)
				if tt.Before(window24h) {
					continue
				}
			}
			var runIDPtr *uint64
			if lr.LatestRunID != 0 {
				runIDPtr = &lr.LatestRunID
			}
			key := s.riskKeyManualBackupFail(t.TaskID, runIDPtr)
			if manualDismissed != nil {
				if _, ok := manualDismissed[key]; ok {
					continue
				}
			}
			manualBackupFailTotal++
		}

		var nodeRows2 []nodeRow
		if err := global.App.DB.Table(backupModels.InsightBackupNodes{}.TableName()).
			Select("id, status, last_heartbeat").
			Where("status = ?", "OFFLINE").
			Scan(&nodeRows2).Error; err != nil {
			return err
		}
		nodeDismissed := sanitizeDismissed("node_offline", len(nodeRows2))
		for _, r := range nodeRows2 {
			key := s.riskKeyNodeOffline(r.ID, string(r.Status), r.LastHeartbeat)
			if nodeDismissed != nil {
				if _, ok := nodeDismissed[key]; ok {
					continue
				}
			}
			nodeOfflineTotal++
		}

		var instRows []instanceRow
		if err := global.App.DB.Table(commonModels.InsightInstances{}.TableName()).
			Select("id, updated_at").Scan(&instRows).Error; err != nil {
			return err
		}

		enabledTaskInstIDs := make(map[uint64]struct{})
		for _, t := range taskRows {
			if t.Enabled {
				enabledTaskInstIDs[t.SourceInstanceID] = struct{}{}
			}
			if !t.Enabled {
				key := fmt.Sprintf("todo_backup_enable:%s", t.TaskID)
				dismissedEnable := sanitizeDismissed("todo_backup_enable", len(taskRows))
				if dismissedEnable == nil {
					backupEnableTotal++
					continue
				}
				if _, ok := dismissedEnable[key]; !ok {
					backupEnableTotal++
				}
			}
		}

		instDismissed := sanitizeDismissed("instance_no_backup", len(instRows))
		for _, r := range instRows {
			if _, ok := enabledTaskInstIDs[r.ID]; ok {
				continue
			}
			key := s.riskKeyInstanceNoBackup(r.ID, r.UpdatedAt)
			if instDismissed != nil {
				if _, ok := instDismissed[key]; ok {
					continue
				}
			}
			instanceNoBackupTotal++
		}

		dailyDismissed := sanitizeDismissed("daily_backup_miss", len(taskRows))
		dayStr := day.Format("2006-01-02")
		for _, t := range taskRows {
			if !t.Enabled {
				continue
			}
			sdRaw := strings.TrimSpace(t.ScheduleDays)
			st := strings.TrimSpace(t.ScheduleTime)
			if sdRaw == "" || sdRaw == "<nil>" || sdRaw == "null" || sdRaw == "[]" {
				continue
			}
			if st == "" && sdRaw != "null" && sdRaw != "[]" {
				continue
			}
			lr := latestRunMap[t.ID]
			lastStatus := strings.ToUpper(strings.TrimSpace(lr.LatestStatus))
			if lastStatus == "SUCCESS" && lr.LatestEndAt != nil {
				te := time.Time(*lr.LatestEndAt)
				if te.After(day) {
					continue
				}
			}
			key := s.riskKeyDailyBackupMiss(t.TaskID, dayStr)
			if dailyDismissed != nil {
				if _, ok := dailyDismissed[key]; ok {
					continue
				}
			}
			dailyBackupMissTotal++
		}
	}
	if canExecuteRestore {
		var rfRows []restoreRow
		if err := global.App.DB.Table(backupModels.InsightRestoreRuns{}.TableName()).
			Select("id, updated_at").
			Where("status = ?", "FAILED").
			Scan(&rfRows).Error; err != nil {
			return err
		}
		rfDismissed := sanitizeDismissed("restore_fail", len(rfRows))
		for _, r := range rfRows {
			key := s.riskKeyRestoreFail(r.ID, r.UpdatedAt)
			if rfDismissed != nil {
				if _, ok := rfDismissed[key]; ok {
					continue
				}
			}
			restoreFailTotal++
		}
	}
	if canViewOrder {
		var ofRows []orderRow
		// 统计工单执行失败风险时不按实例 db_type 过滤，确保已关闭记录口径一致。
		if err := s.orderListAlignedBaseQuery(orderScopes.ScopeMyRelated, false).
			Select("DISTINCT a.order_id, a.created_at, a.updated_at").
			Where("a.progress = ?", "FAILED").
			Scan(&ofRows).Error; err != nil {
			return err
		}
		ofDismissed := sanitizeDismissed("order_fail", len(ofRows))
		for _, r := range ofRows {
			key := s.riskKeyOrderFail(r.OrderID, r.CreatedAt, r.UpdatedAt)
			if ofDismissed != nil {
				if _, ok := ofDismissed[key]; ok {
					continue
				}
			}
			orderFailTotal++
		}

		type todoOrderRow struct {
			OrderID string
		}
		var submitRows []todoOrderRow
		if err := s.orderListAlignedBaseQuery(orderScopes.ScopeMySubmitted, true).
			Select("DISTINCT a.order_id").
			Where("a.progress = ?", "WAIT_PRECHECK").
			Scan(&submitRows).Error; err != nil {
			return err
		}
		dmSubmit := sanitizeDismissed("todo_order_submit", len(submitRows))
		for _, r := range submitRows {
			oid := r.OrderID
			key := fmt.Sprintf("todo_order_submit:%s", oid)
			if dmSubmit != nil {
				if _, ok := dmSubmit[key]; ok {
					continue
				}
			}
			orderSubmitTotal++
		}

		type approveJoinRow struct {
			OrderID string
		}
		var approveRows []approveJoinRow
		approveTx := s.orderListAlignedBaseQuery(orderScopes.ScopePendingMyApproval, true)
		if err := approveTx.Select("DISTINCT a.order_id").Scan(&approveRows).Error; err != nil {
			return err
		}
		dmApprove := sanitizeDismissed("todo_order_approve", len(approveRows))
		for _, r := range approveRows {
			oid := r.OrderID
			key := fmt.Sprintf("todo_order_approve:%s", oid)
			if dmApprove != nil {
				if _, ok := dmApprove[key]; ok {
					continue
				}
			}
			orderApproveTotal++
		}

		type executeScopeRow struct {
			OrderID string
		}
		var executeRows []executeScopeRow
		executeTx := s.orderListAlignedBaseQuery(orderScopes.ScopePendingMyExecution, true)
		if err := executeTx.Select("DISTINCT a.order_id").Scan(&executeRows).Error; err != nil {
			return err
		}
		dmExecute := sanitizeDismissed("todo_order_execute", len(executeRows))
		for _, r := range executeRows {
			oid := r.OrderID
			key := fmt.Sprintf("todo_order_execute:%s", oid)
			if dmExecute != nil {
				if _, ok := dmExecute[key]; ok {
					continue
				}
			}
			orderExecuteTotal++
		}
	}

	if !canManageBackup {
		backupEnableTotal = 0
	}
	if !canViewOrder {
		orderSubmitTotal = 0
		orderApproveTotal = 0
		orderExecuteTotal = 0
	}

	resp.RiskCounts = HomeWorkbenchRiskCounts{
		InstanceConn:     instanceConnTotal,
		ManualBackupFail: manualBackupFailTotal,
		RestoreFail:      restoreFailTotal,
		NodeOffline:      nodeOfflineTotal,
		InstanceNoBackup: instanceNoBackupTotal,
		DailyBackupMiss:  dailyBackupMissTotal,
		OrderFail:        orderFailTotal,
		Total:            instanceConnTotal + manualBackupFailTotal + restoreFailTotal + nodeOfflineTotal + instanceNoBackupTotal + dailyBackupMissTotal + orderFailTotal,
	}
	resp.TodoCounts = HomeWorkbenchTodoCounts{
		OrderSubmit:  orderSubmitTotal,
		OrderApprove: orderApproveTotal,
		OrderExecute: orderExecuteTotal,
		BackupEnable: backupEnableTotal,
		Total:        orderSubmitTotal + orderApproveTotal + orderExecuteTotal + backupEnableTotal,
	}

	return nil
}

func trimText(text string, max int) string {
	text = strings.TrimSpace(text)
	if text == "" || max <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max]) + "..."
}

func emptyFallback(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

var _ = strconv.Itoa
