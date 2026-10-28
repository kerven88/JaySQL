package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/pkg/parser"
	"github.com/simajiong/jaysql/pkg/utils"

	"github.com/simajiong/jaysql/internal/orders/api/base"
)

func stripLeadingComments(s string) string {
	s = strings.TrimSpace(s)
	for {
		changed := false
		s = strings.TrimLeftFunc(s, func(r rune) bool { return unicode.IsSpace(r) })
		if strings.HasPrefix(s, "--") {
			if idx := strings.IndexByte(s, '\n'); idx >= 0 {
				s = s[idx+1:]
			} else {
				s = ""
			}
			changed = true
		} else if strings.HasPrefix(s, "/*") {
			if idx := strings.Index(s, "*/"); idx >= 0 {
				s = s[idx+2:]
			} else {
				s = ""
			}
			changed = true
		} else if strings.HasPrefix(s, "#") {
			if idx := strings.IndexByte(s, '\n'); idx >= 0 {
				s = s[idx+1:]
			} else {
				s = ""
			}
			changed = true
		}
		if !changed {
			break
		}
	}
	return strings.TrimSpace(s)
}

// MySQL DDL
type ExecuteMySQLDDL struct {
	*base.DBConfig
}

func (e *ExecuteMySQLDDL) ExecuteCommand(binary string, args []string) (data []string, err error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan string, 100)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for v := range ch {
			data = append(data, v)

			if perr := utils.Publish(context.Background(), e.OrderID, e.TaskID, utils.RenderGhostStream, v); perr != nil {
				global.App.Log.Error(perr)
			}
		}
	}()

	err = base.Command(ctx, ch, binary, args)
	close(ch)
	<-done

	return
}

// 同一连接内按拆分顺序执行多条SQL（支持「整批SQL单个task」场景）
func executeSQLsInConnection(db *sql.DB, fullSQL string,
	log func(string),
	executeOne func(db *sql.DB, single string, ch chan<- int64) (int64, error),
	cfg *base.DBConfig,
	connectionID int64,
) (totalAffected int64, err error) {
	parts, splitErr := parser.SplitSQLText(fullSQL)
	if splitErr != nil || len(parts) == 0 {
		parts = []string{fullSQL}
	}
	validParts := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s != "" {
			validParts = append(validParts, s)
		}
	}
	if len(validParts) == 0 {
		return 0, errors.New("没有可执行的SQL语句")
	}
	for idx, one := range validParts {
		log(fmt.Sprintf("开始执行第 %d/%d 条SQL", idx+1, len(validParts)))
		ch1 := make(chan int64)
		go DaoMySQLGetProcesslist(cfg, cfg.OrderID, connectionID, ch1)
		startT := time.Now()
		n, e := executeOne(db, one, ch1)
		cost := utils.HumanfriendlyTimeUnit(time.Since(startT))
		if e != nil {
			log(fmt.Sprintf("第 %d/%d 条SQL执行失败，错误：%s，耗时：%s", idx+1, len(validParts), e.Error(), cost))
			return totalAffected, e
		}
		totalAffected += n
		log(fmt.Sprintf("第 %d/%d 条SQL执行成功，影响行数%d，耗时：%s", idx+1, len(validParts), n, cost))
	}
	return totalAffected, nil
}

// 执行Online DDL语句
func (e *ExecuteMySQLDDL) ExecuteOnlineDDL() (data base.ReturnData, err error) {
	logger := base.NewExecuteLogger()
	publisher := base.NewPublisher()

	log := func(msg string) {
		cleaned := strings.TrimSpace(msg)
		formatted := logger.Add(cleaned) + "\n"
		publisher.Publish(e.OrderID, e.TaskID, utils.RenderLogStream, formatted)
	}

	db, err := NewMySQLCnx(e.DBConfig)
	if err != nil {
		log(fmt.Sprintf("访问数据库(%s:%d)失败，错误：%s", e.Hostname, e.Port, err.Error()))
		data.ExecuteLog = logger.String()
		return data, base.SQLExecuteError{Err: err}
	}
	defer db.Close()
	log(fmt.Sprintf("访问数据库(%s:%d)成功", e.Hostname, e.Port))

	connectionID, err := DaoMySQLGetConnectionID(db)
	if err != nil {
		log(fmt.Sprintf("获取数据库Connection ID失败，错误：%s", err.Error()))
		data.ExecuteLog = logger.String()
		return data, base.SQLExecuteError{Err: err}
	}
	log(fmt.Sprintf("数据库Connection ID：%d", connectionID))

	startTime := time.Now()
	affectedRows, err := executeSQLsInConnection(db, e.SQL, log,
		func(d *sql.DB, s string, c chan<- int64) (int64, error) { return DaoMySQLExecute(d, s, c) },
		e.DBConfig,
		connectionID,
	)
	if err != nil {
		data.ExecuteLog = logger.String()
		return data, base.SQLExecuteError{Err: err}
	}
	endTime := time.Now()
	executeCostTime := utils.HumanfriendlyTimeUnit(endTime.Sub(startTime))
	log(fmt.Sprintf("SQL全部执行成功，累计影响行数%d，总耗时：%s", affectedRows, executeCostTime))

	data.ExecuteLog = logger.String()
	data.AffectedRows = affectedRows
	data.ExecuteCostTime = executeCostTime
	return
}

// 判断ALTER TABLE是否可以直连执行而不需要走gh-ost：
// 1）纯元数据 instantaneous DDL（INSTANT，秒级完成）；
// 2）纯索引操作（ADD/DROP INDEX，ALGORITHM=INPLACE，不重建表、允许并发DML——
//
//	与 CREATE INDEX 走直连的 default 分支口径一致，为加个索引跑 gh-ost 全表拷贝是杀鸡用牛刀）
func isAlterTableOnlineDDL(sql string) bool {
	s := stripLeadingComments(sql)
	s = strings.TrimSuffix(s, ";")
	s = strings.TrimSpace(s)
	re := regexp.MustCompile(`(?i)^ALTER\s+TABLE\s+`)
	if !re.MatchString(s) {
		return false
	}
	reHead := regexp.MustCompile("(?is)^ALTER\\s+TABLE\\s+(?:IF\\s+NOT\\s+EXISTS\\s+)?(?:`[^`]+`|[^\\s]+)\\s+(.+)$")
	m := reHead.FindStringSubmatch(s)
	if len(m) < 2 {
		return false
	}
	body := strings.TrimSpace(m[1])
	firstWord := func(b string) string {
		fields := strings.Fields(b)
		if len(fields) == 0 {
			return ""
		}
		return strings.ToLower(fields[0])
	}
	act := firstWord(body)
	switch act {
	case "comment":
		return true
	case "character", "charset":
		return true
	case "collate":
		return true
	case "row_format":
		return true
	case "auto_increment":
		return true
	case "algorithm":
		return true
	case "lock":
		return true
	case "partition":
		return true
	case "remove":
		return true
	case "add":
		// add partition / add index 还是 add column?
		rest := strings.ToLower(body)
		if regexp.MustCompile(`(?i)^ADD\s+(PARTITION|COLUMN)?\s*\(?PARTITION`).MatchString(body) ||
			strings.HasPrefix(rest, "add partition") {
			return true
		}
		// 纯索引 ADD（含 UNIQUE INDEX）：INPLACE 不重建表，直连执行
		if isIndexOnlyAlterBody(body) {
			return true
		}
		return false
	case "drop":
		rest := strings.ToLower(body)
		if strings.HasPrefix(rest, "drop partition") {
			return true
		}
		// 纯索引 DROP：元数据操作，直连执行（DROP PRIMARY KEY 不在此列——需 COPY，仍走 gh-ost）
		if isIndexOnlyAlterBody(body) {
			return true
		}
		return false
	case "truncate":
		rest := strings.ToLower(body)
		if strings.HasPrefix(rest, "truncate partition") {
			return true
		}
		return false
	case "coalesce":
		rest := strings.ToLower(body)
		if strings.HasPrefix(rest, "coalesce partition") {
			return true
		}
		return false
	case "reorganize":
		rest := strings.ToLower(body)
		if strings.HasPrefix(rest, "reorganize partition") {
			return true
		}
		return false
	case "exchange":
		rest := strings.ToLower(body)
		if strings.HasPrefix(rest, "exchange partition") {
			return true
		}
		return false
	case "analyze":
		rest := strings.ToLower(body)
		if strings.HasPrefix(rest, "analyze partition") {
			return true
		}
		return false
	case "check":
		rest := strings.ToLower(body)
		if strings.HasPrefix(rest, "check partition") {
			return true
		}
		return false
	case "optimize":
		rest := strings.ToLower(body)
		if strings.HasPrefix(rest, "optimize partition") {
			return true
		}
		return false
	case "rebuild":
		rest := strings.ToLower(body)
		if strings.HasPrefix(rest, "rebuild partition") {
			return true
		}
		return false
	case "repair":
		rest := strings.ToLower(body)
		if strings.HasPrefix(rest, "repair partition") {
			return true
		}
		return false
	}
	// 兜底：body 只包含瞬时DDL关键字组合（不包含重写表级动作）
	heavyRe := regexp.MustCompile(`(?i)(^|\s|,)(ADD\s+(COLUMN|UNIQUE|INDEX|KEY|PRIMARY|FULLTEXT|SPATIAL|CONSTRAINT|FOREIGN)|CHANGE\s+COLUMN?|MODIFY\s+COLUMN?|DROP\s+(COLUMN|PRIMARY\s+KEY|INDEX|KEY|FOREIGN\s+KEY|CONSTRAINT)|RENAME\s+(TO|AS|INDEX|KEY)|CONVERT\s+TO\s+CHARACTER|ENGINE\s*=)`)
	if heavyRe.MatchString(body) {
		return false
	}
	// 以瞬时 DDL 开头的 body
	lightRe := regexp.MustCompile(`(?i)^(COMMENT\s*=?|CHARACTER\s+SET\s*=?|CHARSET\s*=?|COLLATE\s*=?|ROW_FORMAT\s*=?|AUTO_INCREMENT\s*=?|ALGORITHM\s*=?|LOCK\s*=?)`)
	if lightRe.MatchString(body) {
		return true
	}
	// PARTITION BY / REMOVE PARTITIONING 开头
	if regexp.MustCompile(`(?i)^(PARTITION\s+BY|REMOVE\s+PARTITIONING)`).MatchString(body) {
		return true
	}
	// 逗号分隔的多子句（MySQL 8.0 支持的多 ALTER 组合）
	commaParts := regexp.MustCompile(`\s*,\s*`).Split(body, -1)
	if len(commaParts) <= 1 {
		return false
	}
	for _, p := range commaParts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		a := firstWord(p)
		switch a {
		case "comment", "character", "charset", "collate", "row_format", "auto_increment", "algorithm", "lock":
			continue
		}
		return false
	}
	return true
}

// isIndexOnlyAlterBody 判断 ALTER TABLE 的 body 是否为纯索引操作
// （ADD [UNIQUE] INDEX/KEY 或 DROP INDEX/KEY，多子句需全部为索引操作）。
// 逗号按括号深度感知拆分：索引定义里的 (col1, col2) 不会误判成多子句。
// 注意：ADD/DROP PRIMARY KEY、FULLTEXT、SPATIAL 不算（PK 组合变更需 COPY，FT/SP 有锁限制），仍走 gh-ost。
func isIndexOnlyAlterBody(body string) bool {
	addIdxRe := regexp.MustCompile(`(?i)^ADD\s+(UNIQUE\s+)?(INDEX|KEY)\b`)
	dropIdxRe := regexp.MustCompile(`(?i)^DROP\s+(INDEX|KEY)\b`)
	for _, p := range splitTopLevelCommas(body) {
		p = strings.TrimSpace(p)
		if p == "" {
			return false
		}
		if !addIdxRe.MatchString(p) && !dropIdxRe.MatchString(p) {
			return false
		}
	}
	return true
}

// splitTopLevelCommas 按顶层逗号拆分（括号深度为 0 处的逗号）。
func splitTopLevelCommas(body string) []string {
	var parts []string
	depth := 0
	start := 0
	for i, r := range body {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, body[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, body[start:])
	return parts
}

// buildGhostAlterClause 还原要传给 gh-ost --alter 参数的 ALTER 片段。
//
// 历史实现会把片段里的反引号全部删掉，并把双引号统一改写成单引号，这会引发两类真实故障：
//  1. 列名是 MySQL 保留字时（sql / order / key / status / desc / range / data …），
//     `MODIFY COLUMN `sql` mediumtext` 被改写成 `MODIFY COLUMN sql mediumtext`，
//     gh-ost 执行时直接 Error 1064（本次 tk-tx7e5vcqtnv0 第 2 条即属此类）。
//  2. COMMENT 字面量含引号时，把双引号改写成单引号会截断字面量，同样 1064。
//
// gh-ost 是通过 exec.Command 直接传 argv 启动的（不经过 shell），反引号不存在命令注入风险；
// 且 gh-ost 只是把 --alter 原样拼进 ALTER TABLE，因此保留 SQL 原貌才是正确语义。
//
// 这里只做末端分号的清理：gh-ost 的 --alter 约定不带语句结束符，带分号时部分版本会报语法错误。
func buildGhostAlterClause(raw string) string {
	return strings.TrimSuffix(strings.TrimSpace(raw), ";")
}

// 执行ghost封装后的DDL
func (e *ExecuteMySQLDDL) ExecuteDDLWithGhost(sql string) (data base.ReturnData, err error) {
	logger := base.NewExecuteLogger()
	publisher := base.NewPublisher()

	log := func(msg string) {
		cleaned := strings.TrimSpace(msg)
		formatted := logger.Add(cleaned) + "\n"
		publisher.Publish(e.OrderID, e.TaskID, utils.RenderLogStream, formatted)
	}

	newSQL := stripLeadingComments(sql)
	log("移除SQL语句前后的所有空白字符，包括空格、制表符、换行符等")

	tableName, err := parser.GetTableNameFromAlterStatement(newSQL)
	if err != nil {
		log(fmt.Sprintf("解析SQL提取表名失败，错误：%s", err.Error()))
		data.ExecuteLog = logger.String()
		return data, base.SQLExecuteError{Err: err}
	}
	log("从SQL语句中提取表名成功")

	syntax := `(?i)^ALTER(\s+)TABLE(\s+)([\S]*)(\s+)(ADD|CHANGE|RENAME|MODIFY|DROP|ENGINE|CONVERT|COMMENT|CHARACTER|CHARSET|COLLATE|ROW_FORMAT|AUTO_INCREMENT|ALGORITHM|LOCK|PARTITION|TRUNCATE|COALESCE|REORGANIZE|EXCHANGE|ANALYZE|CHECK|OPTIMIZE|REBUILD|REPAIR)(\s*)([\S\s]*)`
	re, err := regexp.Compile(syntax)
	if err != nil {
		log(fmt.Sprintf("正则匹配SQL语句失败，错误：%s", err.Error()))
		data.ExecuteLog = logger.String()
		return data, base.SQLExecuteError{Err: err}
	}
	match := re.FindStringSubmatch(newSQL)
	if len(match) < 5 {
		log("正则匹配SQL语句失败，未匹配到预期结果")
		data.ExecuteLog = logger.String()
		return data, base.SQLExecuteError{Err: errors.New("正则匹配SQL语句失败，未匹配到预期结果")}
	}
	log("正则匹配SQL语句成功")

	vv := buildGhostAlterClause(strings.Join(match[5:], ""))
	log("提取 gh-ost 的 --alter 片段（保留反引号与原始引号）")

	log("生成gh-ost执行命令")

	// gh-ost 二进制解析：优先用 config.yaml 的 ghost.path；未配置或路径不存在时，
	// 回退到内置 gh-ost（随服务分发，释放到本地缓存目录），服务器无需单独安装。
	ghostBinary := global.App.Config.Ghost.Path
	if ghostBinary == "" {
		embeddedPath, eerr := EnsureEmbeddedGhostBinary()
		if eerr != nil {
			log(fmt.Sprintf("gh-ost 初始化失败：%s", eerr.Error()))
			data.ExecuteLog = logger.String()
			return data, base.SQLExecuteError{Err: eerr}
		}
		ghostBinary = embeddedPath
		log(fmt.Sprintf("ghost.path 未配置，使用内置 gh-ost：%s", ghostBinary))
	} else if _, serr := os.Stat(ghostBinary); serr != nil {
		embeddedPath, eerr := EnsureEmbeddedGhostBinary()
		if eerr != nil {
			log(fmt.Sprintf("gh-ost 初始化失败：%s 不存在或不可访问（%s），且内置 gh-ost 不可用（%s）", ghostBinary, serr.Error(), eerr.Error()))
			data.ExecuteLog = logger.String()
			return data, base.SQLExecuteError{Err: fmt.Errorf("gh-ost 路径配置错误（%s 不存在）且内置 gh-ost 不可用：%s", ghostBinary, eerr.Error())}
		}
		ghostBinary = embeddedPath
		log(fmt.Sprintf("ghost.path 指向的 %s 不存在，回退使用内置 gh-ost：%s", global.App.Config.Ghost.Path, ghostBinary))
	}
	// 前置检查：二进制缺失时给出可行动的提示，而不是把
	// fork/exec xxx: no such file or directory 原样抛给工单执行日志
	if info, serr := os.Stat(ghostBinary); serr != nil {
		log(fmt.Sprintf("gh-ost 初始化失败：%s 不存在或不可访问（%s）。请在服务端安装 gh-ost 并放到该路径，或修正 config.yaml 的 ghost.path 配置", ghostBinary, serr.Error()))
		data.ExecuteLog = logger.String()
		return data, base.SQLExecuteError{Err: fmt.Errorf("gh-ost 未安装或路径配置错误（%s 不存在）：请在服务端安装 gh-ost，或修正 config.yaml 的 ghost.path", ghostBinary)}
	} else if info.IsDir() {
		log(fmt.Sprintf("gh-ost 初始化失败：%s 是目录而非可执行文件，请检查 config.yaml 的 ghost.path 配置", ghostBinary))
		data.ExecuteLog = logger.String()
		return data, base.SQLExecuteError{Err: fmt.Errorf("gh-ost 路径配置错误（%s 是目录）：请修正 config.yaml 的 ghost.path", ghostBinary)}
	}
	ghostArgs := make([]string, 0, len(global.App.Config.Ghost.Args)+8)
	ghostArgs = append(ghostArgs, global.App.Config.Ghost.Args...)
	ghostArgs = append(ghostArgs,
		fmt.Sprintf("--user=%s", e.UserName),
		fmt.Sprintf("--password=%s", e.Password),
		fmt.Sprintf("--host=%s", e.Hostname),
		fmt.Sprintf("--port=%d", e.Port),
		fmt.Sprintf("--database=%s", e.Schema),
		fmt.Sprintf("--table=%s", tableName),
		fmt.Sprintf("--alter=%s", vv),
		"--execute",
	)

	if strings.Contains(e.Hostname, "rds.aliyuncs.com") {
		ghostArgs = append(ghostArgs, "-aliyun-rds=true")
		ghostArgs = append(ghostArgs, fmt.Sprintf("-assume-master-host=%s", e.Hostname))
	}

	startTime := time.Now()
	printGhostCMD := base.RenderCommandForLog(ghostBinary, ghostArgs)
	log(fmt.Sprintf("执行gh-ost命令：%s", printGhostCMD))
	elog, eerr := e.ExecuteCommand(ghostBinary, ghostArgs)
	for _, l := range elog {
		log(l)
	}
	if eerr != nil {
		log(fmt.Sprintf("gh-ost命令执行失败，错误：%s", eerr.Error()))
		data.ExecuteLog = logger.String()
		return data, base.SQLExecuteError{Err: eerr}
	}
	log("gh-ost命令执行成功")
	endTime := time.Now()
	executeCostTime := utils.HumanfriendlyTimeUnit(endTime.Sub(startTime))

	data.ExecuteLog = logger.String()
	data.ExecuteCostTime = executeCostTime
	return
}

// serverVersionInfo 目标实例版本解析结果。
type serverVersionInfo struct {
	Raw       string // 原始版本串
	Major     int
	Minor     int
	Patch     int
	IsMariaDB bool
}

// parseMySQLVersion 解析 SELECT VERSION() 返回串。
// 兼容："8.0.36"、"5.7.44-log"、"8.0.36-tidb-v7.5.0"（按 MySQL 语义取前三段数字）；
// MariaDB：版本串含 "mariadb"（含兼容协议下的 "5.5.5-10.11.6-MariaDB" 双段格式，取 '-' 后的发行版本）。
func parseMySQLVersion(raw string) (serverVersionInfo, error) {
	info := serverVersionInfo{Raw: strings.TrimSpace(raw)}
	if info.Raw == "" {
		return info, errors.New("版本串为空")
	}
	lower := strings.ToLower(info.Raw)
	s := info.Raw
	if strings.Contains(lower, "mariadb") {
		info.IsMariaDB = true
		// 兼容协议伪装串 "5.5.5-10.x.y-MariaDB"：去掉前缀取真实发行版本；普通 "10.11.6-MariaDB" 不受影响
		s = strings.TrimPrefix(s, "5.5.5-")
	}
	nums := regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`).FindStringSubmatch(s)
	if nums == nil {
		return info, fmt.Errorf("版本串 %q 中未找到形如 x.y.z 的数字版本", info.Raw)
	}
	var err error
	if info.Major, err = strconv.Atoi(nums[1]); err != nil {
		return info, fmt.Errorf("版本串 %q 主版本解析失败: %v", info.Raw, err)
	}
	if info.Minor, err = strconv.Atoi(nums[2]); err != nil {
		return info, fmt.Errorf("版本串 %q 次版本解析失败: %v", info.Raw, err)
	}
	if nums[3] != "" {
		info.Patch, _ = strconv.Atoi(nums[3])
	}
	return info, nil
}

// minSupportedMySQLVersion 平台支持的最低版本：MySQL 5.7（不支持 MariaDB 与 5.7 以下版本）。
func (e *ExecuteMySQLDDL) gateMySQLVersion(log func(string)) error {
	db, cerr := NewMySQLCnx(e.DBConfig)
	if cerr != nil {
		return fmt.Errorf("无法连接数据库以校验版本: %v", cerr)
	}
	defer db.Close()

	raw, qerr := DaoMySQLSelectVersion(db)
	if qerr != nil {
		return fmt.Errorf("版本校验失败（fail-closed，已中止执行）: %v", qerr)
	}
	info, perr := parseMySQLVersion(raw)
	if perr != nil {
		return fmt.Errorf("版本校验失败（fail-closed，已中止执行）: %v", perr)
	}
	log(fmt.Sprintf("目标实例版本：%s（%s:%d）", info.Raw, e.Hostname, e.Port))

	if info.IsMariaDB {
		return fmt.Errorf("检测到 MariaDB %s：平台仅支持 MySQL 5.7 及以上版本，不支持 MariaDB，已中止执行", info.Raw)
	}
	if info.Major < 5 || (info.Major == 5 && info.Minor < 7) {
		return fmt.Errorf("目标实例 MySQL %s 低于平台支持的最低版本 5.7，已中止执行", info.Raw)
	}
	return nil
}

func (e *ExecuteMySQLDDL) Run() (data base.ReturnData, err error) {
	logger := base.NewExecuteLogger()
	publisher := base.NewPublisher()
	log := func(msg string) {
		cleaned := strings.TrimSpace(msg)
		formatted := logger.Add(cleaned) + "\n"
		publisher.Publish(e.OrderID, e.TaskID, utils.RenderLogStream, formatted)
	}

	// 先拆所有语句（对齐多语句支持：避免 gh-ost 分支只取 firstSQL 导致 N-1 条静默丢弃）
	parts, perr := parser.SplitSQLText(e.SQL)
	if perr != nil || len(parts) == 0 {
		cleaned := stripLeadingComments(e.SQL)
		if cleaned != "" {
			parts = []string{cleaned}
		}
	}
	validParts := make([]string, 0, len(parts))
	for _, p := range parts {
		c := stripLeadingComments(p)
		if c == "" {
			continue
		}
		validParts = append(validParts, c)
	}
	if len(validParts) == 0 {
		return data, errors.New("没有可执行的SQL语句")
	}

	// 版本闸门：平台仅支持 MySQL 5.7 及以上（不支持 MariaDB）。
	// 查询失败按拒绝处理（fail-closed）——宁可不动库，也不能在未知版本上盲执行 DDL。
	if verr := e.gateMySQLVersion(log); verr != nil {
		log(fmt.Sprintf("版本校验未通过：%s", verr.Error()))
		data.ExecuteLog = logger.String()
		return data, base.SQLExecuteError{Err: verr}
	}

	// 策略一：所有语句都可直连执行（瞬时 DDL / 纯索引操作）→ 统一走 ExecuteOnlineDDL（内部已拆句循环）
	allInstantaneous := func() bool {
		for _, c := range validParts {
			st, sterr := parser.GetSqlStatement(c)
			if sterr != nil {
				return false
			}
			if st == "DropDatabase" {
				return false
			}
			if st == "AlterTable" {
				if !isAlterTableOnlineDDL(c) {
					return false
				}
			}
		}
		return true
	}()
	if allInstantaneous {
		if len(validParts) == 1 {
			sqlType, _ := parser.GetSqlStatement(validParts[0])
			if sqlType == "DropDatabase" {
				return data, errors.New("【风险】禁止执行drop database操作")
			}
		}
		return e.ExecuteOnlineDDL()
	}

	// 策略二：混合语句 / 存在非瞬时 ALTER → 单条逐条走（gh-ost 一次只支持 1 张表 1 条 ALTER）

	// 共用一个 DB 连接（gh-ost 自己会新开 gh-ost 进程连 DB；OnlineDDL 里也会自己开连接，这里只用于连接日志一致性）
	db, cerr := NewMySQLCnx(e.DBConfig)
	if cerr == nil {
		defer db.Close()
		log(fmt.Sprintf("访问数据库(%s:%d)成功（准备执行 %d 条语句）", e.Hostname, e.Port, len(validParts)))
	} else {
		log(fmt.Sprintf("访问数据库(%s:%d)失败（非致命，继续尝试执行子任务），错误：%s", e.Hostname, e.Port, cerr.Error()))
	}

	startTime := time.Now()
	var totalAffected int64
	var lastCost string
	for idx, one := range validParts {
		log(fmt.Sprintf("开始执行第 %d/%d 条SQL", idx+1, len(validParts)))
		sqlType, err := parser.GetSqlStatement(one)
		if err != nil {
			log(fmt.Sprintf("第 %d/%d 条SQL解析失败，错误：%s", idx+1, len(validParts), err.Error()))
			data.ExecuteLog = logger.String()
			data.AffectedRows = totalAffected
			return data, base.SQLExecuteError{Err: err}
		}
		startT := time.Now()
		switch sqlType {
		case "DropDatabase":
			log(fmt.Sprintf("第 %d/%d 条SQL禁止执行：drop database", idx+1, len(validParts)))
			data.ExecuteLog = logger.String()
			data.AffectedRows = totalAffected
			return data, errors.New("【风险】禁止执行drop database操作")
		case "AlterTable":
			if isAlterTableOnlineDDL(one) {
				// 可直连的 ALTER（瞬时 / 纯索引操作）：构造一个只含这 1 条的 ExecuteMySQLDDL 调 ExecuteOnlineDDL（复用 OnlineDDL 的 processlist 日志）
				sub := &ExecuteMySQLDDL{DBConfig: e.DBConfig}
				sub.SQL = one
				rd, rerr := sub.ExecuteOnlineDDL()
				if rerr != nil {
					log(fmt.Sprintf("第 %d/%d 条ALTER直连执行失败：%s（日志：%s）", idx+1, len(validParts), rerr.Error(), rd.ExecuteLog))
					data.ExecuteLog = logger.String() + "\n" + rd.ExecuteLog
					data.AffectedRows = totalAffected + rd.AffectedRows
					return data, rerr
				}
				totalAffected += rd.AffectedRows
				lastCost = rd.ExecuteCostTime
				log(fmt.Sprintf("第 %d/%d 条ALTER直连执行成功，影响行数%d，耗时：%s", idx+1, len(validParts), rd.AffectedRows, rd.ExecuteCostTime))
			} else {
				// 非瞬时 ALTER：走 gh-ost（gh-ost 内部处理日志发布到同一个 order_id + task_id）
				rd, rerr := e.ExecuteDDLWithGhost(one)
				if rerr != nil {
					log(fmt.Sprintf("第 %d/%d 条gh-ost ALTER执行失败：%s（日志：%s）", idx+1, len(validParts), rerr.Error(), rd.ExecuteLog))
					data.ExecuteLog = logger.String() + "\n" + rd.ExecuteLog
					data.AffectedRows = totalAffected
					return data, rerr
				}
				lastCost = rd.ExecuteCostTime
				log(fmt.Sprintf("第 %d/%d 条gh-ost ALTER执行成功，耗时：%s", idx+1, len(validParts), rd.ExecuteCostTime))
			}
		default:
			// 其他 DDL（Create/Drop/Truncate/Rename/CreateIndex 等）：单条调用 ExecuteOnlineDDL
			sub := &ExecuteMySQLDDL{DBConfig: e.DBConfig}
			sub.SQL = one
			rd, rerr := sub.ExecuteOnlineDDL()
			if rerr != nil {
				log(fmt.Sprintf("第 %d/%d 条%s执行失败：%s（日志：%s）", idx+1, len(validParts), sqlType, rerr.Error(), rd.ExecuteLog))
				data.ExecuteLog = logger.String() + "\n" + rd.ExecuteLog
				data.AffectedRows = totalAffected + rd.AffectedRows
				return data, rerr
			}
			totalAffected += rd.AffectedRows
			lastCost = rd.ExecuteCostTime
			log(fmt.Sprintf("第 %d/%d 条%s执行成功，影响行数%d，耗时：%s", idx+1, len(validParts), sqlType, rd.AffectedRows, rd.ExecuteCostTime))
		}
		cost := utils.HumanfriendlyTimeUnit(time.Since(startT))
		_ = cost
	}
	executeCostTime := utils.HumanfriendlyTimeUnit(time.Since(startTime))
	log(fmt.Sprintf("SQL全部执行成功，累计影响行数%d，总耗时：%s（最后子任务耗时：%s）", totalAffected, executeCostTime, lastCost))

	data.ExecuteLog = logger.String()
	data.AffectedRows = totalAffected
	data.ExecuteCostTime = executeCostTime
	return data, nil
}
