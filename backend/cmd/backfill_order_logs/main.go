// 一次性回填工具：为「新日志逻辑上线前」已执行完成的工单补录操作历史。
//
// 背景：旧版本只在任务失败时写「执行任务失败」日志，成功链路（提交执行/开始执行/执行成功）
// 没有留痕，导致已完成的工单操作历史缺少执行节点。本工具从 insight_order_tasks.result
// 的 execute_log 时间戳行提取起止时间，按原样式补写 insight_order_logs。
//
// 幂等规则：工单已存在任何执行相关日志（提交执行/开始执行任务/任务执行成功/执行任务失败）
// 则整单跳过，避免重复。失败工单因旧版本已写「执行任务失败」日志，同样会被跳过。
//
// 用法：
//	go run ./cmd/backfill_order_logs -dsn "user:pass@tcp(host:3306)/db?charset=utf8mb4" [-dry-run]
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	_ "github.com/go-sql-driver/mysql"
)

type taskRow struct {
	OrderID  string
	TaskID   string
	Executor sql.NullString
	SQL      sql.NullString
	Progress string
	Result   sql.NullString
	Updated  sql.NullString
}

type taskResult struct {
	Error      string `json:"error"`
	ExecuteLog string `json:"execute_log"`
}

var timeRE = regexp.MustCompile(`\[\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\]`)

// 与 execute_tasks.go 的 truncateSQLHint 保持一致
func truncateSQLHint(s string) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	runes := []rune(s)
	if len(runes) > 60 {
		return string(runes[:60]) + "..."
	}
	return s
}

func extractTimes(log string) []string {
	matches := timeRE.FindAllString(log, -1)
	times := make([]string, 0, len(matches))
	for _, m := range matches {
		times = append(times, strings.Trim(m, "[]"))
	}
	return times
}

func main() {
	dsn := flag.String("dsn", "", "MySQL DSN，例如 user:pass@tcp(127.0.0.1:3306)/goinsight?charset=utf8mb4")
	dryRun := flag.Bool("dry-run", false, "仅打印将插入的日志，不写库")
	flag.Parse()
	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "必须通过 -dsn 指定数据库连接")
		flag.Usage()
		os.Exit(1)
	}
	db, err := sql.Open("mysql", *dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "连接数据库失败:", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		fmt.Fprintln(os.Stderr, "数据库不可达:", err)
		os.Exit(1)
	}

	// 只处理终态工单下已结束的任务，按工单+任务顺序排列
	rows, err := db.Query(`
		SELECT t.order_id, t.task_id, t.executor, t.sql, t.progress, t.result, t.updated_at, r.applicant
		FROM insight_order_tasks t
		JOIN insight_order_records r ON r.order_id = t.order_id
		WHERE t.progress IN ('COMPLETED','FAILED')
		  AND r.progress IN ('COMPLETED','FAILED')
		ORDER BY t.order_id, t.id`)
	if err != nil {
		fmt.Fprintln(os.Stderr, "查询任务失败:", err)
		os.Exit(1)
	}
	defer rows.Close()

	type orderPlan struct {
		orderID   string
		applicant string
		tasks     []taskRow
	}
	orders := map[string]*orderPlan{}
	var orderSeq []string
	for rows.Next() {
		var t taskRow
		var applicant sql.NullString
		if err := rows.Scan(&t.OrderID, &t.TaskID, &t.Executor, &t.SQL, &t.Progress, &t.Result, &t.Updated, &applicant); err != nil {
			fmt.Fprintln(os.Stderr, "扫描任务行失败:", err)
			os.Exit(1)
		}
		op, ok := orders[t.OrderID]
		if !ok {
			op = &orderPlan{orderID: t.OrderID, applicant: applicant.String}
			orders[t.OrderID] = op
			orderSeq = append(orderSeq, t.OrderID)
		}
		op.tasks = append(op.tasks, t)
	}
	if err := rows.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "遍历任务失败:", err)
		os.Exit(1)
	}

	inserted, skipped := 0, 0
	for _, oid := range orderSeq {
		op := orders[oid]
		// 幂等：已存在执行相关日志的工单整单跳过
		var cnt int
		if err := db.QueryRow(`
			SELECT COUNT(*) FROM insight_order_logs
			WHERE order_id = ? AND (
				msg LIKE '用户%提交执行%' OR msg LIKE '开始执行任务%'
				OR msg LIKE '任务执行成功%' OR msg LIKE '%执行任务失败%')`,
			oid).Scan(&cnt); err != nil {
			fmt.Fprintf(os.Stderr, "工单 %s 查重失败: %v\n", oid, err)
			os.Exit(1)
		}
		if cnt > 0 {
			skipped++
			continue
		}

		type logRow struct {
			username, msg, createdAt string
		}
		var logs []logRow
		for i, t := range op.tasks {
			username := t.Executor.String
			if username == "" {
				username = op.applicant
			}
			hint := truncateSQLHint(t.SQL.String)
			var res taskResult
			if t.Result.Valid && t.Result.String != "" {
				_ = json.Unmarshal([]byte(t.Result.String), &res)
			}
			times := extractTimes(res.ExecuteLog)
			fallback := t.Updated.String
			if len(fallback) > 19 {
				fallback = fallback[:19]
			}
			start := fallback
			end := fallback
			if len(times) > 0 {
				start = times[0]
				if len(times) > 1 {
					end = times[len(times)-1]
				}
			}
			if i == 0 {
				logs = append(logs, logRow{username, fmt.Sprintf("用户%s提交执行", username), start})
			}
			logs = append(logs, logRow{username, fmt.Sprintf("开始执行任务，SQL：%s", hint), start})
			if t.Progress == "FAILED" {
				msg := strings.TrimSpace(res.Error)
				if msg == "" {
					msg = "执行失败"
				}
				logs = append(logs, logRow{username, fmt.Sprintf("执行任务失败：%s", msg), end})
			} else {
				logs = append(logs, logRow{username, fmt.Sprintf("任务执行成功，SQL：%s", hint), end})
			}
		}

		fmt.Printf("工单 %s：补录 %d 条日志\n", oid, len(logs))
		if *dryRun {
			for _, l := range logs {
				fmt.Printf("  [%s] %s | %s\n", l.createdAt, l.username, l.msg)
			}
			inserted += len(logs)
			continue
		}
		for _, l := range logs {
			msg := l.msg
			if len([]rune(msg)) > 1024 {
				msg = string([]rune(msg)[:1024])
			}
			if _, err := db.Exec(
				"INSERT INTO insight_order_logs (username, order_id, msg, created_at, updated_at) VALUES (?,?,?,?,?)",
				l.username, oid, msg, l.createdAt, l.createdAt); err != nil {
				fmt.Fprintln(os.Stderr, "插入日志失败:", err)
				os.Exit(1)
			}
			inserted++
		}
	}
	fmt.Printf("完成：处理工单 %d 个（跳过已含执行日志 %d 个），补录日志 %d 条（dry-run=%v）\n",
		len(orderSeq), skipped, inserted, *dryRun)
}
