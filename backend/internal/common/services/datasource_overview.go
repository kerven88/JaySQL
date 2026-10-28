package services

import (
	"fmt"

	"github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// 数据源总览（数据源管理一级菜单）
//
// 一次取回「实例清单 + 探活状态 + 监控/备份/SQL 规范覆盖标记」，供 /datasource/overview
// 页面单表展示。之所以聚合在后端：四个覆盖标记分属监控 / 备份 / 规范三个域，
// 前端各自拼接口既有权限边界问题（普通成员不可见备份任务列表），也会放大请求数。
//
// 口径：
//   * 探活状态：insight_instances.last_check_status（后台常驻探活回写，success/error/unknown）
//   * 监控覆盖：insight_monitor_target_configs 存在记录且 enabled=true（平台不自动下发 target，
//     有行即代表管理员显式挂载过）
//   * 备份覆盖：insight_backup_tasks 按 source_instance_id 计数，**必须带 deleted_at IS NULL**
//     （软删口径，见长期备忘：该表有 gorm.DeletedAt）
//   * 规范覆盖：insight_instance_sql_standard_bindings 按 instance_id 绑定，附带规范名
// ---------------------------------------------------------------------------

type DatasourceOverviewServices struct {
	C *gin.Context
}

type DatasourceOverviewRow struct {
	ID                uint64            `json:"id"`
	InstanceID        string            `json:"instance_id"`
	Name              string            `json:"name"`
	Hostname          string            `json:"hostname"`
	Port              int               `json:"port"`
	DbType            string            `json:"db_type"`
	DbVersion         string            `json:"db_version"`
	EnvironmentID     int               `json:"environment_id"`
	EnvironmentName   string            `json:"environment_name"`
	EnvironmentColor  string            `json:"environment_color"`
	LastCheckStatus   string            `json:"last_check_status"`
	LastCheckedAt     *models.LocalTime `json:"last_checked_at"`
	MonitorRegistered bool              `json:"monitor_registered"`
	BackupTaskCount   int64             `json:"backup_task_count"`
	StdBound          bool              `json:"std_bound"`
	StdID             uint64            `json:"std_id"`
	StdName           string            `json:"std_name"`

	// 主机登录：三态在后端一次算好（引擎白名单 + 部署形态 + 凭据齐备），
	// 前端不重复拼规则 —— 支持范围以后要扩展时，只改服务端一处。
	HostLoginSupported bool   `json:"host_login_supported"`
	HostLoginReady     bool   `json:"host_login_ready"`
	HostLoginUser      string `json:"host_login_user"`
	HostLoginBlockCode string `json:"host_login_block_code"`
	HostLoginBlockNote string `json:"host_login_block_note"`

	// 判定输入，不参与序列化：extra_config 里含数据库与 SSH 凭据密文，任何接口都不得下发
	DeployProvider string `json:"-"`
	ExtraConfig    []byte `json:"-"`
}

func (s *DatasourceOverviewServices) Run() (interface{}, error) {
	db := global.App.DB

	var rows []DatasourceOverviewRow
	err := db.Table("insight_instances i").
		Select("i.id, i.instance_id, i.hostname, i.port, i.db_type, IFNULL(i.db_version, '') AS db_version, " +
			"IFNULL(i.environment, 0) AS environment_id, " +
			"IFNULL(i.remark, '') AS name, IFNULL(i.last_check_status, 'unknown') AS last_check_status, i.last_checked_at, " +
			"IFNULL(e.name, '') AS environment_name, IFNULL(e.color, '') AS environment_color, " +
			// 主机登录判定用：部署形态区分云数据库，extra_config 里是 ssh_* 凭据
			"IFNULL(i.deploy_provider, '') AS deploy_provider, i.extra_config").
		Joins("LEFT JOIN insight_instance_environments e ON e.id = i.environment").
		Order("environment_name ASC, i.hostname ASC, i.port ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("查询数据源清单失败: %w", err)
	}

	// 监控挂载：instance_id(业务号) → enabled
	type monitorRow struct {
		InstanceID string
		Enabled    bool
	}
	var monitors []monitorRow
	if err := db.Table("insight_monitor_target_configs").
		Select("instance_id, enabled").
		Scan(&monitors).Error; err != nil {
		return nil, fmt.Errorf("查询监控挂载失败: %w", err)
	}
	monitorSet := make(map[string]bool, len(monitors))
	for _, m := range monitors {
		if m.Enabled {
			monitorSet[m.InstanceID] = true
		}
	}

	// 备份任务计数：软删不计入
	type backupRow struct {
		SourceInstanceID uint64
		Cnt              int64
	}
	var backups []backupRow
	if err := db.Table("insight_backup_tasks").
		Select("source_instance_id, count(*) AS cnt").
		Where("deleted_at IS NULL").
		Group("source_instance_id").
		Scan(&backups).Error; err != nil {
		return nil, fmt.Errorf("查询备份任务覆盖失败: %w", err)
	}
	backupCount := make(map[uint64]int64, len(backups))
	for _, b := range backups {
		backupCount[b.SourceInstanceID] = b.Cnt
	}

	// SQL 规范绑定（每实例至多一条，uniqueIndex:uniq_instance_id）
	// standard_id 供前端跳转规范详情页（/datasource/sql-standards/:id），
	// 只带名字无法定位到具体规范（同名规范跨环境可能重复）。
	type bindingRow struct {
		InstanceID string
		StandardID uint64
		StdName    string
	}
	var bindings []bindingRow
	if err := db.Table("insight_instance_sql_standard_bindings b").
		Select("b.instance_id, b.standard_id, IFNULL(s.name, '') AS std_name").
		Joins("LEFT JOIN insight_sql_standards s ON s.id = b.standard_id").
		Scan(&bindings).Error; err != nil {
		return nil, fmt.Errorf("查询规范绑定失败: %w", err)
	}
	type stdBinding struct {
		ID   uint64
		Name string
	}
	stdByInstance := make(map[string]stdBinding, len(bindings))
	for _, b := range bindings {
		stdByInstance[b.InstanceID] = stdBinding{ID: b.StandardID, Name: b.StdName}
	}

	for i := range rows {
		row := &rows[i]
		// 展示名：备注优先，为空回落 hostname:port
		if StringValue(row.Name) == "" {
			row.Name = fmt.Sprintf("%s:%d", row.Hostname, row.Port)
		}
		row.MonitorRegistered = monitorSet[row.InstanceID]
		row.BackupTaskCount = backupCount[row.ID]
		binding := stdByInstance[row.InstanceID]
		row.StdID = binding.ID
		row.StdName = binding.Name
		row.StdBound = row.StdName != ""
		// 主机登录：解析失败视为「没配凭据」，不因为一行坏数据把整页打挂
		cfg, _ := parseExtraConfig(row.ExtraConfig)
		blockCode, blockNote, sshUser, ready := ClassifyHostLoginBlock(row.DbType, row.DeployProvider, cfg)
		row.HostLoginBlockCode = blockCode
		row.HostLoginBlockNote = blockNote
		row.HostLoginUser = sshUser
		row.HostLoginReady = ready
		row.HostLoginSupported = blockCode == HostLoginBlockNone || blockCode == HostLoginBlockNoCredential ||
			blockCode == HostLoginBlockCredentialIncomplete
	}

	return rows, nil
}

// StringValue 空串判断辅助（name 列扫描自 IFNULL 后仍可能是空白字符）
func StringValue(v string) string {
	for _, r := range v {
		if r != ' ' && r != '\t' {
			return v
		}
	}
	return ""
}
