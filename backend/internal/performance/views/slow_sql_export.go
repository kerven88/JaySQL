package views

import (
	"bytes"
	"encoding/csv"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/performance/forms"
	"github.com/simajiong/jaysql/internal/performance/services"
	"github.com/simajiong/jaysql/pkg/response"
)

// formatSlowSQLSeconds 与前端 formatSeconds 同口径：毫秒转秒并去掉尾零。
func formatSlowSQLSeconds(ms int64) string {
	value := float64(ms) / 1000
	if value <= 0 {
		return "0"
	}
	var text string
	if value >= 100 {
		text = strconv.FormatFloat(value, 'f', 1, 64)
	} else if value >= 10 {
		text = strconv.FormatFloat(value, 'f', 2, 64)
	} else {
		text = strconv.FormatFloat(value, 'f', 3, 64)
	}
	text = strings.TrimRight(text, "0")
	text = strings.TrimSuffix(text, ".")
	if text == "" {
		return "0"
	}
	return text
}

// sanitizeCSVCell 防 Excel 公式注入：以 = + - @ 等开头的文本前置单引号。
func sanitizeCSVCell(value string) string {
	if value == "" {
		return ""
	}
	switch value[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + value
	}
	return value
}

// writeSlowSQLCSV 统一输出：UTF-8 BOM（兼容 Excel 中文）+ attachment 头。
func writeSlowSQLCSV(c *gin.Context, filename string, headers []string, rows [][]string) {
	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF")
	w := csv.NewWriter(&buf)
	if err := w.Write(headers); err != nil {
		response.Fail(c, err.Error())
		return
	}
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			response.Fail(c, err.Error())
			return
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		response.Fail(c, err.Error())
		return
	}

	encodedName := url.PathEscape(filename)
	timestamp := time.Now().Format("20060102150405")
	c.Header("Content-Disposition", `attachment; filename="`+timestamp+`_export.csv"; filename*=UTF-8''`+encodedName)
	c.Data(200, "text/csv; charset=utf-8", buf.Bytes())
}

// ExportSlowSQLStatsView 导出「慢SQL统计」：字段与页面列一致。
func ExportSlowSQLStatsView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.GetSlowSQLStatsForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	form.IsPage = false
	service := services.GetSlowSQLStatsService{Form: &form, C: c, Username: username}
	rows, _, _, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}

	headers := []string{"时间", "数据库名", "SQL语句", "执行次数", "总耗时(S)", "平均(MS)", "扫描行数"}
	records := make([][]string, 0, len(rows))
	for _, row := range rows {
		avg := int64(0)
		if row.ExecCount > 0 {
			avg = row.TotalDurationMS / row.ExecCount
		}
		records = append(records, []string{
			row.LatestTime,
			sanitizeCSVCell(row.SchemaName),
			sanitizeCSVCell(row.SQLFingerprint),
			strconv.FormatInt(row.ExecCount, 10),
			formatSlowSQLSeconds(row.TotalDurationMS),
			strconv.FormatInt(avg, 10),
			strconv.FormatInt(row.MaxRowsExamined, 10),
		})
	}
	writeSlowSQLCSV(c, "慢SQL统计.csv", headers, records)
}

// ExportSlowSQLView 导出「慢SQL明细」：字段与页面列一致。
func ExportSlowSQLView(c *gin.Context) {
	username, ok := getUsername(c)
	if !ok {
		return
	}
	var form forms.GetSlowSQLForm
	if err := c.ShouldBindQuery(&form); err != nil {
		response.ValidateFail(c, err.Error())
		return
	}
	form.IsPage = false
	service := services.GetSlowSQLService{Form: &form, C: c, Username: username}
	rows, _, err := service.Run()
	if err != nil {
		response.Fail(c, err.Error())
		return
	}

	headers := []string{"慢日志采集时间", "SQL语句", "客户端IP", "用户", "数据库名", "执行时长(秒)", "锁定时长(秒)", "解析行数", "返回行数"}
	records := make([][]string, 0, len(rows))
	for _, row := range rows {
		records = append(records, []string{
			row.CreatedAt,
			sanitizeCSVCell(row.SQLText),
			row.ClientHost,
			row.Username,
			row.Schema,
			formatSlowSQLSeconds(int64(row.Duration)),
			formatSlowSQLSeconds(int64(row.LockTimeMS)),
			strconv.Itoa(row.RowsExamined),
			strconv.Itoa(row.ReturnRows),
		})
	}
	writeSlowSQLCSV(c, "慢SQL明细.csv", headers, records)
}
