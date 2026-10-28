package services

import (
	"fmt"
	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/pkg/pagination"

	"github.com/simajiong/jaysql/internal/das/forms"

	"github.com/gin-gonic/gin"
	"strings"
	"time"
)

type GetHistoryService struct {
	*forms.GetHistoryForm
	C        *gin.Context
	Username string
}

func parseHistoryTime(value string) (*time.Time, bool, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return nil, false, nil
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
				return nil, false, fmt.Errorf("时间范围仅支持 2000-2099")
			}
			return &t, layout != "2006-01-02", nil
		}
	}
	return nil, false, fmt.Errorf("时间格式不合法: %s", raw)
}

func (s *GetHistoryService) Run() (responseData *[]map[string]any, total int64, err error) {
	var list []map[string]any
	tx := global.App.DB.Table("insight_das_records r").
		Select(`
			r.id,
			r.created_at,
			r.updated_at,
			r.username,
			r.instance_id,
			ifnull(i.remark, '') as instance_name,
			ifnull(i.hostname, '') as hostname,
			ifnull(i.port, 0) as port,
			r.schema,
			r.tables,
			r.sqltext,
			r.query_id,
			r.duration,
			r.rewrite_sqltext,
			r.params,
			r.is_finish,
			r.is_kill,
			r.error_msg,
			r.return_rows,
			r.request_id
		`).
		Joins("left join insight_instances i on r.instance_id = i.instance_id").
		Where("r.username=?", s.Username).
		Order("r.created_at desc")

	startAt, _, err := parseHistoryTime(s.StartDate)
	if err != nil {
		return nil, 0, err
	}
	endAt, endHasTime, err := parseHistoryTime(s.EndDate)
	if err != nil {
		return nil, 0, err
	}
	if startAt != nil {
		tx = tx.Where("r.created_at >= ?", startAt)
	}
	if endAt != nil {
		if endHasTime {
			tx = tx.Where("r.created_at <= ?", endAt)
		} else {
			tx = tx.Where("r.created_at < ?", endAt.AddDate(0, 0, 1))
		}
	}

	// 搜索schema
	if s.Search != "" {
		tx = tx.Where("r.`schema` like ? or r.`tables` like ? or r.`sqltext` like ?", "%"+s.Search+"%", "%"+s.Search+"%", "%"+s.Search+"%")
	}
	total = pagination.Pager(&s.PaginationQ, tx, &list)

	return &list, total, nil
}
