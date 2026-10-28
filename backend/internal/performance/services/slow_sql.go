package services

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/performance/forms"
	"gorm.io/gorm"
)

type slowSQLRow struct {
	ID              uint64 `json:"id"`
	InstanceID      string `json:"instance_id"`
	Remark          string `json:"remark"`
	Target          string `json:"target"`
	DBType          string `json:"db_type"`
	EnvironmentName string `json:"environment_name"`
	Username        string `json:"username"`
	Schema          string `json:"schema" gorm:"column:schema_name"`
	ClientHost      string `json:"client_host"`
	// SQLDigest SQL 指纹标识：解析失败时仍保留，用户能据此定位到具体语句批次
	SQLDigest    string `json:"sql_digest"`
	SQLText      string `json:"sql_text"`
	Duration     int    `json:"duration"`
	LockTimeMS   int    `json:"lock_time_ms"`
	RowsExamined int    `json:"rows_examined"`
	ReturnRows   int    `json:"return_rows"`
	IsFinish     bool   `json:"is_finish"`
	ErrorMsg     string `json:"error_msg"`
	CreatedAt    string `json:"created_at"`

	// 结构化失败信息：解析失败的条目照样出现在列表里，
	// 只是带上统一错误码与可读原因，绝不被过滤掉。
	CollectStatus string `json:"collect_status"`
	ErrorCode     string `json:"error_code"`
	ErrorMessage  string `json:"error_message"`
	ErrorCategory string `json:"error_category"`
	ErrorTone     string `json:"error_tone"`
	// ParseStatus 解析状态（PARSED / 其它）：仅服务端归一错误码用，不下发前端
	ParseStatus string `json:"-"`
}

type GetSlowSQLService struct {
	Form     *forms.GetSlowSQLForm
	C        *gin.Context
	Username string
}

func (s *GetSlowSQLService) Run() ([]slowSQLRow, int64, error) {
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

	var rows []slowSQLRow
	tx := global.App.DB.Table("insight_performance_slowlog_entries r").
		Select(`
			r.id,
			i.instance_id,
			i.remark,
			concat(i.hostname, ':', i.port) as target,
			i.db_type,
			ifnull(env.name, '') as environment_name,
			r.username,
                        r.client_host,
                        r.schema_name as schema_name,
                        r.sql_digest as sql_digest,
                        r.sql_text as sql_text,
                        r.query_time_ms as duration,
                        r.lock_time_ms,
                        r.rows_examined,
                        r.rows_sent as return_rows,
                        case when r.parse_status = 'PARSED' then true else false end as is_finish,
                        r.parse_error as error_msg,
                        r.parse_status as parse_status,
                        DATE_FORMAT(r.event_time, '%Y-%m-%d %H:%i:%s') as created_at
		`).
		Joins("join insight_instances i on i.instance_id = r.instance_id").
		Joins("left join insight_instance_environments env on env.id = i.environment").
		// 三类引擎一视同仁。此前这里硬编码 r.db_type = 'MySQL'，
		// 导致 PostgreSQL / Oracle 采上来的慢SQL 在明细里一条都查不到——
		// 采集端跑得好好的，页面却"没有数据"，是最难排查的那类问题。
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
	if value := strings.TrimSpace(form.SchemaName); value != "" {
		tx = tx.Where("r.schema_name = ?", value)
	}
	if value := strings.TrimSpace(form.SQLDigest); value != "" {
		tx = tx.Where("r.sql_digest = ?", value)
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

	if strings.TrimSpace(form.SQLDigest) != "" {
		// SQL 指纹抽屉场景：固定按事件时间倒序
		tx = tx.Order("r.event_time desc, r.id desc")
	} else {
		tx = tx.Order(slowSQLDetailOrderBy(form))
	}

	total, err := countSlowSQLRows(tx)
	if err != nil {
		return nil, 0, err
	}

	offset := form.PageSize * (form.Page - 1)
	var query *gorm.DB
	if form.IsPage {
		query = cloneSlowSQLQuery(tx).Limit(form.PageSize).Offset(offset)
	} else {
		query = cloneSlowSQLQuery(tx)
	}
	if err := query.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	// 逐条补上结构化失败信息。解析失败的条目**照常返回**：
	// 平台确实采到了这条慢SQL，只是没能完整解析出指标，
	// 让它从列表消失等于把"采到了但解析不了"伪装成"压根没有"。
	for i := range rows {
		errView := NormalizeSlowLogParseError(rows[i].ParseStatus, rows[i].ErrorMsg)
		rows[i].CollectStatus = errView.Status
		rows[i].ErrorCode = errView.Code
		rows[i].ErrorMessage = errView.Message
		rows[i].ErrorCategory = errView.Category
		rows[i].ErrorTone = errView.Tone
	}
	return rows, total, nil
}

func countSlowSQLRows(tx *gorm.DB) (int64, error) {
	var total int64
	countTx := cloneSlowSQLQuery(tx)
	if err := countTx.Count(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func cloneSlowSQLQuery(tx *gorm.DB) *gorm.DB {
	return tx.Session(&gorm.Session{})
}

// slowSQLDetailSortMap 明细排序白名单：前端列 key -> SQL 列，防注入。
var slowSQLDetailSortMap = map[string]string{
	"created_at":    "r.event_time",
	"duration":      "r.query_time_ms",
	"lock_time_ms":  "r.lock_time_ms",
	"rows_examined": "r.rows_examined",
	"return_rows":   "r.rows_sent",
}

// slowSQLDetailOrderBy 明细排序：仅接受白名单字段；默认按采集时间倒序。
func slowSQLDetailOrderBy(form *forms.GetSlowSQLForm) string {
	field, ok := slowSQLDetailSortMap[strings.TrimSpace(form.SortField)]
	if !ok {
		return "r.event_time desc, r.id desc"
	}
	if strings.EqualFold(strings.TrimSpace(form.SortOrder), "asc") {
		return field + " asc, r.id desc"
	}
	return field + " desc, r.id desc"
}
