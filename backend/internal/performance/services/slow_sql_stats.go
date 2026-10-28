package services

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/performance/forms"
	"gorm.io/gorm"
)

type slowSQLStatsRow struct {
	SQLDigest         string `json:"sql_digest"`
	SQLFingerprint    string `json:"sql_fingerprint"`
	InstanceID        string `json:"instance_id"`
	Remark            string `json:"remark"`
	Target            string `json:"target"`
	DBType            string `json:"db_type"`
	SchemaName        string `json:"schema_name"`
	SQLText           string `json:"sql_text"`
	LatestTime        string `json:"latest_time"`
	ExecCount         int64  `json:"exec_count"`
	TotalDurationMS   int64  `json:"total_duration_ms"`
	MaxDurationMS     int64  `json:"max_duration_ms"`
	TotalLockTimeMS   int64  `json:"total_lock_time_ms"`
	MaxLockTimeMS     int64  `json:"max_lock_time_ms"`
	TotalRowsExamined int64  `json:"total_rows_examined"`
	MaxRowsExamined   int64  `json:"max_rows_examined"`
	TotalReturnRows   int64  `json:"total_return_rows"`
	MaxReturnRows     int64  `json:"max_return_rows"`
}

type GetSlowSQLStatsService struct {
	Form     *forms.GetSlowSQLStatsForm
	C        *gin.Context
	Username string
}

func (s *GetSlowSQLStatsService) Run() ([]slowSQLStatsRow, int64, int64, error) {
	form := s.Form
	// is_page=false 表示不分页取全量（导出场景），仅 is_page=true 时应用分页默认值
	if form.IsPage {
		if form.PageSize <= 0 {
			form.PageSize = 10
		}
		if form.Page <= 0 {
			form.Page = 1
		}
	}

	var rows []slowSQLStatsRow
	tx, err := buildSlowSQLStatsQuery(form)
	if err != nil {
		return nil, 0, 0, err
	}

	total, err := countSlowSQLStatsRows(tx)
	if err != nil {
		return nil, 0, 0, err
	}

	rawTotal, err := countSlowSQLRawRows(form)
	if err != nil {
		return nil, 0, 0, err
	}

	offset := form.PageSize * (form.Page - 1)
	var query *gorm.DB
	if form.IsPage {
		query = cloneSlowSQLStatsQuery(tx).Limit(form.PageSize).Offset(offset)
	} else {
		query = cloneSlowSQLStatsQuery(tx)
	}
	if err := query.Scan(&rows).Error; err != nil {
		return nil, 0, 0, err
	}
	return rows, total, rawTotal, nil
}

func buildSlowSQLStatsQuery(form *forms.GetSlowSQLStatsForm) (*gorm.DB, error) {
	tx := global.App.DB.Table("insight_performance_slowlog_entries r").
		Select(`
                        max(r.sql_digest) as sql_digest,
                        substring_index(group_concat(r.sql_fingerprint order by char_length(r.sql_fingerprint) desc separator '\n/*__GI_SPLIT__*/\n'), '\n/*__GI_SPLIT__*/\n', 1) as sql_fingerprint,
                        max(i.instance_id) as instance_id,
                        max(i.remark) as remark,
                        max(concat(i.hostname, ':', i.port)) as target,
                        max(i.db_type) as db_type,
                        max(r.schema_name) as schema_name,
                        substring_index(group_concat(r.sql_text order by r.query_time_ms desc separator '\n/*__GI_SPLIT__*/\n'), '\n/*__GI_SPLIT__*/\n', 1) as sql_text,
                        DATE_FORMAT(max(r.event_time), '%Y-%m-%d %H:%i:%s') as latest_time,
                        count(*) as exec_count,
                        ifnull(sum(r.query_time_ms), 0) as total_duration_ms,
                        ifnull(max(r.query_time_ms), 0) as max_duration_ms,
                        ifnull(sum(r.lock_time_ms), 0) as total_lock_time_ms,
                        ifnull(max(r.lock_time_ms), 0) as max_lock_time_ms,
                        ifnull(sum(r.rows_examined), 0) as total_rows_examined,
                        ifnull(max(r.rows_examined), 0) as max_rows_examined,
                        ifnull(sum(r.rows_sent), 0) as total_return_rows,
                        ifnull(max(r.rows_sent), 0) as max_return_rows
                `).
		Joins("join insight_instances i on i.instance_id = r.instance_id").
		// 与明细列表同一口径：三类引擎一视同仁，不再只统计 MySQL
		Where("r.db_type in ?", supportedDBTypes)

	if keyword := strings.TrimSpace(form.Search); keyword != "" {
		like := "%" + keyword + "%"
		tx = tx.Where(
			"i.instance_id like ? or i.remark like ? or i.hostname like ? or r.schema_name like ? or r.username like ? or r.sql_text like ?",
			like, like, like, like, like, like,
		)
	}
	if value := strings.TrimSpace(form.DBType); value != "" {
		tx = tx.Where("r.db_type = ?", value)
	}
	if value := strings.TrimSpace(form.InstanceID); value != "" {
		tx = tx.Where("i.instance_id = ?", value)
	}
	if form.MinDurationMS > 0 {
		tx = tx.Where("r.query_time_ms >= ?", form.MinDurationMS)
	}
	if value := strings.TrimSpace(form.StartAt); value != "" {
		tx = tx.Where("r.event_time >= ?", value)
	}
	if value := strings.TrimSpace(form.EndAt); value != "" {
		tx = tx.Where("r.event_time <= ?", value)
	}

	return tx.
		Group("r.instance_id, r.schema_name, r.sql_digest").
		Order(slowSQLStatsOrderBy(form)), nil
}

// slowSQLStatsSortMap 统计排序白名单：前端列 key -> 聚合表达式，防注入。
var slowSQLStatsSortMap = map[string]string{
	"latest_time":    "latest_time",
	"exec_count":     "exec_count",
	"total_duration": "total_duration_ms",
	"avg_duration":   "total_duration_ms / exec_count",
	"scan_rows":      "max_rows_examined",
}

// slowSQLStatsOrderBy 统计排序：仅接受白名单字段；默认按最新时间倒序。
func slowSQLStatsOrderBy(form *forms.GetSlowSQLStatsForm) string {
	field, ok := slowSQLStatsSortMap[strings.TrimSpace(form.SortField)]
	if !ok {
		return "latest_time desc, exec_count desc"
	}
	if strings.EqualFold(strings.TrimSpace(form.SortOrder), "asc") {
		return field + " asc, exec_count desc"
	}
	return field + " desc, exec_count desc"
}

func countSlowSQLStatsRows(tx *gorm.DB) (int64, error) {
	var total int64
	countTx := global.App.DB.Table("(?) as stats_rows", cloneSlowSQLStatsQuery(tx))
	if err := countTx.Count(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func countSlowSQLRawRows(form *forms.GetSlowSQLStatsForm) (int64, error) {
	var total int64
	countTx, err := buildSlowSQLStatsBaseQuery(form)
	if err != nil {
		return 0, err
	}
	if err := countTx.Count(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func buildSlowSQLStatsBaseQuery(form *forms.GetSlowSQLStatsForm) (*gorm.DB, error) {
	tx := global.App.DB.Table("insight_performance_slowlog_entries r").
		Joins("join insight_instances i on i.instance_id = r.instance_id").
		// 原始条数（未经指纹聚合）同样要覆盖三类引擎，
		// 否则会与上面的聚合总数对不上，页面出现"聚合 N 条 / 原始 M 条"的口径矛盾
		Where("r.db_type in ?", supportedDBTypes)

	if keyword := strings.TrimSpace(form.Search); keyword != "" {
		like := "%" + keyword + "%"
		tx = tx.Where(
			"i.instance_id like ? or i.remark like ? or i.hostname like ? or r.schema_name like ? or r.username like ? or r.sql_text like ?",
			like, like, like, like, like, like,
		)
	}
	if value := strings.TrimSpace(form.DBType); value != "" {
		tx = tx.Where("r.db_type = ?", value)
	}
	if value := strings.TrimSpace(form.InstanceID); value != "" {
		tx = tx.Where("i.instance_id = ?", value)
	}
	if form.MinDurationMS > 0 {
		tx = tx.Where("r.query_time_ms >= ?", form.MinDurationMS)
	}
	if value := strings.TrimSpace(form.StartAt); value != "" {
		tx = tx.Where("r.event_time >= ?", value)
	}
	if value := strings.TrimSpace(form.EndAt); value != "" {
		tx = tx.Where("r.event_time <= ?", value)
	}

	return tx, nil
}

func cloneSlowSQLStatsQuery(tx *gorm.DB) *gorm.DB {
	return tx.Session(&gorm.Session{})
}
