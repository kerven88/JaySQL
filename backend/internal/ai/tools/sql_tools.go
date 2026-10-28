package tools

// ============================================================
// SQL 类工具：NL2SQL / SQL 解释 / 执行计划 / 索引推荐 / 数据采样
//
// 与 instance_tables.go 的定位差别：那边的工具回答「平台上有什么」，
// 这边回答「这条 SQL 怎么写、为什么慢、该怎么建索引、表里装的是什么」。
//
// 三条硬约束（缺一条这个模块就不能上线）：
//  1. 鉴权前置：调用任何查询型服务之前，先按「库 / 表」问一次数据授权；
//     无权限直接返回统一措辞的友好提示，不调用任何查询工具。
//  2. 只读：五个工具全部 Write=false。索引建议只给 DDL 文本，想落库必须
//     走 create_order 生成工单草稿由用户确认。
//  3. 结果可控：候选表数量、采样行数、单元格长度、执行计划行数全部有上限，
//     避免一次调用把模型上下文撑爆。
// ============================================================

import (
	"fmt"
	"sort"
	"strings"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	dasForms "github.com/simajiong/jaysql/internal/das/forms"
	dasServices "github.com/simajiong/jaysql/internal/das/services"
)

func init() {
	register(generateSQLTool())
	register(interpretSQLTool())
	register(explainSQLTool())
	register(recommendIndexesTool())
	register(sampleDataTool())
}

const (
	// NL2SQL 候选表数量：DDL 上下文很占 token，宁少勿多
	defaultNL2SQLTables = 3
	maxNL2SQLTables     = 5
	nl2SQLColumnLimit   = 40

	// 采样：行数与单元格长度双重上限
	defaultSampleLimit = 10
	maxSampleLimit     = 50
	sampleCellMaxChars = 200
	sampleColumnLimit  = 30

	// 执行计划回显行数上限
	maxExplainRows = 60

	// 索引建议条数上限
	maxIndexSuggestions = 10
)

// ------------------------------------------------------------------
// 公共步骤：定位实例 → 鉴权 → 取结构
// ------------------------------------------------------------------

// resolveToolInstance 定位实例并做可见性收敛。
// 未授权的实例一律按「没找到」处理：不能让 AI 借 SQL 类工具摸清未授权实例。
func resolveToolInstance(ctx *Context, args map[string]any) (*commonModels.InsightInstances, error) {
	instance, err := lookupInstance(
		argString(args, "instance_id", "instanceId"),
		argString(args, "host"),
		argInt(args, "port", 0),
	)
	if err != nil {
		return nil, err
	}
	visible, err := instanceVisible(ctx, instance.InstanceID)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, fmt.Errorf("未找到实例 %s", instance.InstanceID)
	}
	return instance, nil
}

// sqlToolResources 把 SQL 涉及的库表转成待鉴权资源。
// 解析失败时退化为库级校验 —— 宁可校验得粗一点，也不能因为解析失败就跳过鉴权。
func sqlToolResources(sqlText, schema string) []sqlResource {
	tables, ok := extractSQLTables(sqlText, schema)
	if !ok || len(tables) == 0 {
		return []sqlResource{{Schema: schema}}
	}
	out := make([]sqlResource, 0, len(tables))
	for _, item := range tables {
		out = append(out, sqlResource{Schema: item.Schema, Table: item.Table})
	}
	return out
}

// guardSQLAccess 统一鉴权前置。
// 返回 (友好提示结果, 是否放行, 系统错误)：未放行时调用方必须直接 return 提示结果。
func guardSQLAccess(ctx *Context, instanceID string, resources []sqlResource) (map[string]any, bool, error) {
	missing, err := checkSQLResources(ctx, instanceID, SQLActionSelect, resources)
	if err != nil {
		return nil, false, err
	}
	if len(missing) > 0 {
		return noDataGrantResult(SQLActionSelect, missing), false, nil
	}
	return nil, true, nil
}

// fetchSchemaTables 列出某库下的表（复用 das 直连，与 list_tables 同一实现）
func fetchSchemaTables(ctx *Context, instanceID, schema string) ([]map[string]any, error) {
	ginCtx, cancel := newToolGinContext()
	defer cancel()
	service := dasServices.GetTablesService{
		GetTablesForm: &dasForms.GetTablesForm{InstanceID: instanceID, Schema: schema},
		C:             ginCtx,
		Username:      usernameOf(ctx),
	}
	data, err := service.Run()
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0)
	if data == nil {
		return out, nil
	}
	for _, row := range *data {
		name := cellString(row["table_name"])
		if name == "" {
			continue
		}
		item := map[string]any{"table": name}
		setIfNotEmpty(item, "comment", cellString(row["table_comment"]))
		setIfNotEmpty(item, "rows", cellString(row["table_rows"]))
		out = append(out, item)
	}
	return out, nil
}

// fetchTableDDL 取单表建表语句（复用 das 直连，与 describe_table 同一实现）
func fetchTableDDL(ctx *Context, instanceID, schema, table string) (string, error) {
	ginCtx, cancel := newToolGinContext()
	defer cancel()
	service := dasServices.GetTableInfoService{
		GetTableInfoForm: &dasForms.GetTableInfoForm{
			InstanceID: instanceID,
			Schema:     schema,
			Table:      table,
			Type:       "structure",
		},
		C:        ginCtx,
		Username: usernameOf(ctx),
	}
	data, err := service.Run()
	if err != nil {
		return "", err
	}
	// 各引擎返回的字段名不同（MySQL 是 Create Table，PG 是拼好的建表语句），
	// 取最长的一段文本即 DDL 本体。
	ddl := ""
	if data != nil {
		for _, row := range *data {
			for _, value := range row {
				if text := cellString(value); len(text) > len(ddl) {
					ddl = text
				}
			}
		}
	}
	return ddl, nil
}

// tableShape 表结构摘要：列 + 索引（NL2SQL 与索引推荐共用）
func tableShape(ddl string) map[string]any {
	columns, indexes := parseCreateTable(ddl)
	shape := map[string]any{}
	columnList := make([]map[string]any, 0, len(columns))
	for index, column := range columns {
		if index >= nl2SQLColumnLimit {
			break
		}
		item := map[string]any{"name": column.Name}
		if text := strings.TrimSpace(column.Type); text != "" {
			item["type"] = text
		}
		columnList = append(columnList, item)
	}
	if len(columnList) > 0 {
		shape["columns"] = columnList
		shape["column_count"] = len(columns)
	}
	indexList := make([]string, 0, len(indexes))
	for _, index := range indexes {
		if index.Primary {
			indexList = append(indexList, fmt.Sprintf("PRIMARY(%s)", strings.Join(index.Columns, ",")))
			continue
		}
		prefix := "INDEX"
		if index.Unique {
			prefix = "UNIQUE"
		}
		indexList = append(indexList, fmt.Sprintf("%s %s(%s)", prefix, index.Name, strings.Join(index.Columns, ",")))
	}
	if len(indexList) > 0 {
		shape["indexes"] = indexList
	}
	return shape
}

// ------------------------------------------------------------------
// generate_sql（NL2SQL）
// ------------------------------------------------------------------

func generateSQLTool() *Tool {
	return &Tool{
		Name: "generate_sql",
		Description: "把自然语言需求转成 SQL 的前置准备：在指定库里按需求关键词挑出候选表，" +
			"返回这些表的字段、类型与索引，以及生成 SQL 时必须遵守的约束，再由你据此写出最终语句。" +
			"需要 instance（instance_id 或 host+port）与 schema；不知道库表时先用 get_instance / list_tables。" +
			"本工具只准备上下文、不执行任何查询。",
		Permission: "sql.console.access",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question":    map[string]any{"type": "string", "description": "用户的自然语言需求，如「统计最近 7 天每个用户的下单金额」"},
				"instance_id": map[string]any{"type": "string", "description": "实例业务 ID（inst- 开头）"},
				"host":        map[string]any{"type": "string", "description": "实例主机地址或 IP（与 instance_id 二选一）"},
				"port":        map[string]any{"type": "integer", "description": "实例端口，如 3306"},
				"schema":      map[string]any{"type": "string", "description": "库名 / 资源路径（PostgreSQL 形如 database.schema），必填"},
				"tables":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "候选表名；留空则由工具按需求关键词在库内挑选"},
				"limit":       map[string]any{"type": "integer", "description": fmt.Sprintf("候选表数量，默认 %d，最多 %d", defaultNL2SQLTables, maxNL2SQLTables)},
			},
			"required": []string{"question", "schema"},
		},
		Handler: func(ctx *Context, args map[string]any) (any, error) {
			question := argString(args, "question", "requirement", "text")
			if question == "" {
				return nil, fmt.Errorf("缺少参数 question：请给出要用 SQL 实现的自然语言需求")
			}
			instance, err := resolveToolInstance(ctx, args)
			if err != nil {
				return nil, err
			}
			schema := argString(args, "schema", "database", "db")
			if schema == "" {
				return nil, fmt.Errorf("缺少参数 schema：可先用 get_instance 查看该实例已采集的库清单")
			}

			limit := argInt(args, "limit", defaultNL2SQLTables)
			if limit <= 0 {
				limit = defaultNL2SQLTables
			}
			if limit > maxNL2SQLTables {
				limit = maxNL2SQLTables
			}

			// 鉴权前置（库级）：连表清单都不该在没有库权限时列出来
			if denied, allowed, guardErr := guardSQLAccess(ctx, instance.InstanceID,
				[]sqlResource{{Schema: schema}}); guardErr != nil || !allowed {
				return denied, guardErr
			}

			wanted := argStringList(args, "tables", "table")
			candidates := make([]map[string]any, 0, len(wanted))
			if len(wanted) > 0 {
				for _, name := range wanted {
					if len(candidates) >= limit {
						break
					}
					candidates = append(candidates, map[string]any{"table": name})
				}
			} else {
				tables, listErr := fetchSchemaTables(ctx, instance.InstanceID, schema)
				if listErr != nil {
					return nil, fmt.Errorf("读取库 %s 的表清单失败：%s", schema, strings.TrimSpace(listErr.Error()))
				}
				candidates = pickCandidateTables(tables, question, limit)
				if len(candidates) == 0 {
					return map[string]any{
						"instance_id": instance.InstanceID,
						"schema":      schema,
						"db_type":     plainString(instance.DbType),
						"question":    question,
						"table_count": 0,
						"tables":      []map[string]any{},
						"hint": "该库下没有匹配到相关表，请补充具体的表名（tables 参数），" +
							"或先用 list_tables 看看库里有哪些表再重试",
					}, nil
				}
			}

			// 逐表鉴权 + 取结构：库级放行不等于每张表都能看
			items := make([]map[string]any, 0, len(candidates))
			skipped := make([]string, 0)
			for _, candidate := range candidates {
				table := plainString(candidate["table"])
				if table == "" {
					continue
				}
				missing, checkErr := checkSQLResources(ctx, instance.InstanceID, SQLActionSelect,
					[]sqlResource{{Schema: schema, Table: table}})
				if checkErr != nil {
					return nil, checkErr
				}
				if len(missing) > 0 {
					skipped = append(skipped, missing...)
					continue
				}
				ddl, ddlErr := fetchTableDDL(ctx, instance.InstanceID, schema, table)
				if ddlErr != nil {
					items = append(items, map[string]any{
						"table": table,
						"error": "读取表结构失败：" + strings.TrimSpace(ddlErr.Error()),
					})
					continue
				}
				item := map[string]any{"table": table}
				setIfNotEmpty(item, "comment", plainString(candidate["comment"]))
				setIfNotEmpty(item, "rows", plainString(candidate["rows"]))
				if text := strings.TrimSpace(ddl); text == "" {
					item["error"] = "未取到建表语句，请确认表名是否正确"
					items = append(items, item)
					continue
				}
				for key, value := range tableShape(ddl) {
					item[key] = value
				}
				items = append(items, item)
			}

			result := map[string]any{
				"instance_id": instance.InstanceID,
				"schema":      schema,
				"db_type":     plainString(instance.DbType),
				"question":    question,
				"table_count": len(items),
				"tables":      items,
				"constraints": []string{
					"只输出一条完整的 SELECT 语句，不要输出多条，也不要附带执行动作",
					"表名与字段名必须与上面给出的字段清单完全一致，不允许臆造字段",
					"必须带 LIMIT（默认不超过 100），避免一次性拉取全表",
					"避免 SELECT *，只取需要的字段",
					"时间范围条件优先使用已有的索引列，不要在列上套函数（如 DATE(created_at)）",
					"只生成只读查询；任何写操作都要改用 create_order 生成工单草稿",
				},
				"instruction": "请基于上面的字段清单、索引与约束，用 SQL 代码块给出最终语句，并简要说明关键条件；不要声称已执行过。",
			}
			if len(skipped) > 0 {
				result["skipped_tables"] = skipped
				result["skipped_message"] = noDataGrantMessage(SQLActionSelect, skipped)
			}
			if len(items) == 0 {
				result["hint"] = "没有可用的表结构（全部因权限或读取失败被跳过），请把上面的权限提示转述给用户"
			}
			return result, nil
		},
	}
}

// pickCandidateTables 按需求关键词给表打分，返回得分最高的若干张。
// 一个都没命中时退化为「按数据量从大到小给出参考」——总比让模型空手猜强。
func pickCandidateTables(tables []map[string]any, question string, limit int) []map[string]any {
	keywords := questionKeywords(question)
	type scored struct {
		item  map[string]any
		score int
		rows  string
	}
	ranked := make([]scored, 0, len(tables))
	for _, item := range tables {
		score := 0
		haystack := strings.ToLower(plainString(item["table"]) + " " + plainString(item["comment"]))
		for _, keyword := range keywords {
			if strings.Contains(haystack, keyword) {
				score++
			}
		}
		ranked = append(ranked, scored{item: item, score: score, rows: plainString(item["rows"])})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return len(ranked[i].rows) > len(ranked[j].rows) || ranked[i].rows > ranked[j].rows
	})
	if len(ranked) > 0 && ranked[0].score == 0 {
		out := make([]map[string]any, 0, limit)
		for index, entry := range ranked {
			if index >= limit {
				break
			}
			out = append(out, entry.item)
		}
		return out
	}
	out := make([]map[string]any, 0, limit)
	for _, entry := range ranked {
		if entry.score <= 0 {
			continue
		}
		if len(out) >= limit {
			break
		}
		out = append(out, entry.item)
	}
	return out
}

// questionKeywords 从自然语言需求里提取用于匹配表名的关键词：
// 英文取长度 ≥3 的词，中文取 2 字滑窗（中文分词不在依赖范围内，2-gram 足够做粗筛）
func questionKeywords(question string) []string {
	out := make([]string, 0)
	seen := map[string]struct{}{}
	appendWord := func(word string) {
		word = strings.ToLower(strings.TrimSpace(word))
		if word == "" {
			return
		}
		if _, ok := seen[word]; ok {
			return
		}
		seen[word] = struct{}{}
		out = append(out, word)
	}
	for _, token := range strings.FieldsFunc(question, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_'
	}) {
		if len(token) >= 3 {
			appendWord(token)
		}
	}
	runes := []rune(question)
	for index := 0; index+2 <= len(runes); index++ {
		gram := string(runes[index : index+2])
		if isHanPair(gram) {
			appendWord(gram)
		}
	}
	return out
}

func isHanPair(text string) bool {
	runes := []rune(text)
	if len(runes) != 2 {
		return false
	}
	for _, r := range runes {
		if r < 0x4e00 || r > 0x9fff {
			return false
		}
	}
	return true
}

// ------------------------------------------------------------------
// interpret_sql
// ------------------------------------------------------------------

func interpretSQLTool() *Tool {
	return &Tool{
		Name: "interpret_sql",
		Description: "把一条 SQL 解释成结构化说明：语句类型、涉及的库表、查询字段、" +
			"JOIN 与关联条件、WHERE / GROUP BY / ORDER BY / LIMIT，以及风险提示（如缺少 WHERE、SELECT *）。" +
			"本工具只做语法层解析，不执行 SQL、不查询平台数据；要看实际执行代价请用 explain_sql。",
		Permission: "sql.console.access",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"sql":         map[string]any{"type": "string", "description": "待解释的 SQL 语句，一次一条"},
				"instance_id": map[string]any{"type": "string", "description": "实例业务 ID；给出后会按数据授权校验涉及的库表"},
				"host":        map[string]any{"type": "string", "description": "实例主机地址或 IP（与 instance_id 二选一）"},
				"port":        map[string]any{"type": "integer", "description": "实例端口"},
				"schema":      map[string]any{"type": "string", "description": "默认库名；SQL 里未限定库名时用它补全"},
			},
			"required": []string{"sql"},
		},
		Handler: func(ctx *Context, args map[string]any) (any, error) {
			sqlText := strings.TrimSpace(argString(args, "sql", "sql_text", "content"))
			if sqlText == "" {
				return nil, fmt.Errorf("缺少参数 sql：请给出要解释的 SQL 语句")
			}
			schema := argString(args, "schema", "database", "db")

			// 给了实例才做鉴权：没给说明用户只是想解释一段自己手里的 SQL，
			// 此时不触碰任何平台数据，也就无从校验（结果里会明确标注）。
			instanceID := argString(args, "instance_id", "instanceId")
			host := argString(args, "host")
			if instanceID != "" || host != "" {
				instance, err := resolveToolInstance(ctx, args)
				if err != nil {
					return nil, err
				}
				denied, allowed, guardErr := guardSQLAccess(ctx, instance.InstanceID, sqlToolResources(sqlText, schema))
				if guardErr != nil {
					return nil, guardErr
				}
				if !allowed {
					return denied, nil
				}
			}

			explained, err := interpretSQL(sqlText, schema)
			if err != nil {
				return nil, err
			}
			if instanceID == "" && host == "" {
				explained["note"] = "未指定实例，本次只做了语法层解释：未校验数据授权，也没有获取任何平台数据"
			}
			return explained, nil
		},
	}
}

// ------------------------------------------------------------------
// explain_sql
// ------------------------------------------------------------------

func explainSQLTool() *Tool {
	return &Tool{
		Name: "explain_sql",
		Description: "获取一条 SELECT 语句的执行计划（EXPLAIN）并给出可读的风险提示：" +
			"全表扫描、有索引未使用、filesort、临时表、预估扫描行数等。" +
			"当前支持 MySQL 系与 PostgreSQL 系；只执行 EXPLAIN，不执行原查询，也不会改动数据。",
		Permission: "sql.console.access",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"sql":         map[string]any{"type": "string", "description": "待分析的 SELECT 语句（不要自带 EXPLAIN 前缀）"},
				"instance_id": map[string]any{"type": "string", "description": "实例业务 ID（inst- 开头）"},
				"host":        map[string]any{"type": "string", "description": "实例主机地址或 IP（与 instance_id 二选一）"},
				"port":        map[string]any{"type": "integer", "description": "实例端口，如 3306"},
				"schema":      map[string]any{"type": "string", "description": "库名 / 资源路径（PostgreSQL 形如 database.schema）"},
			},
			"required": []string{"sql"},
		},
		Handler: func(ctx *Context, args map[string]any) (any, error) {
			sqlText := strings.TrimSpace(argString(args, "sql", "sql_text", "content"))
			if sqlText == "" {
				return nil, fmt.Errorf("缺少参数 sql：请给出要分析的 SELECT 语句")
			}
			instance, err := resolveToolInstance(ctx, args)
			if err != nil {
				return nil, err
			}
			schema := argString(args, "schema", "database", "db")

			// 鉴权前置：执行计划会暴露表名、索引名与预估行数，属于数据范畴
			denied, allowed, guardErr := guardSQLAccess(ctx, instance.InstanceID, sqlToolResources(sqlText, schema))
			if guardErr != nil {
				return nil, guardErr
			}
			if !allowed {
				return denied, nil
			}

			ginCtx, cancel := newToolGinContext()
			defer cancel()
			username := usernameOf(ctx)
			dbType := plainString(instance.DbType)
			isPostgres := dasServices.IsPostgresFamily(dbType)

			var response dasServices.ResponseData
			switch {
			case dasServices.IsMySQLFamily(dbType):
				service := dasServices.ExplainMySQLQueryService{
					ExecuteMySQLQueryForm: &dasForms.ExecuteMySQLQueryForm{
						InstanceID: instance.InstanceID,
						Schema:     schema,
						SQLText:    sqlText,
					},
					C:        ginCtx,
					Username: username,
				}
				response, err = service.Run()
			case isPostgres:
				service := dasServices.ExplainPostgresQueryService{
					ExecutePostgresQueryForm: &dasForms.ExecutePostgresQueryForm{
						InstanceID: instance.InstanceID,
						Schema:     schema,
						SQLText:    sqlText,
					},
					C:        ginCtx,
					Username: username,
				}
				response, err = service.Run()
			default:
				return nil, fmt.Errorf("执行计划分析当前仅支持 MySQL 系与 PostgreSQL 系，当前实例类型为 %s", dbType)
			}
			if err != nil {
				return nil, fmt.Errorf("获取执行计划失败：%s", strings.TrimSpace(err.Error()))
			}

			columns := make([]string, 0)
			if response.Columns != nil {
				columns = append(columns, (*response.Columns)...)
			}
			rows := make([]map[string]any, 0)
			if response.Data != nil {
				for index, row := range *response.Data {
					if index >= maxExplainRows {
						break
					}
					rows = append(rows, row)
				}
			}
			analysis := analyzeExplainPlan(columns, rows, isPostgres)
			if response.Data != nil && len(*response.Data) > maxExplainRows {
				analysis["truncated"] = true
			}
			analysis["instance_id"] = instance.InstanceID
			analysis["db_type"] = dbType
			if schema != "" {
				analysis["schema"] = schema
			}
			analysis["sql"] = sqlText
			if text := strings.TrimSpace(response.SQLText); text != "" {
				analysis["explain_sql"] = text
			}
			if text := strings.TrimSpace(response.Duration); text != "" {
				analysis["duration"] = text
			}
			if len(rows) == 0 {
				analysis["hint"] = "数据库没有返回执行计划行，请确认语句是单条 SELECT 且实例可达"
			}
			return analysis, nil
		},
	}
}

// ------------------------------------------------------------------
// recommend_indexes
// ------------------------------------------------------------------

func recommendIndexesTool() *Tool {
	return &Tool{
		Name: "recommend_indexes",
		Description: "基于表结构与（可选的）SQL 给出索引建议：缺失索引候选、冗余索引、无主键提示、" +
			"低区分度列与长文本列的建索引注意点。只做分析并给出 DDL 文本，不执行任何变更；" +
			"确认要落地时请用 create_order 生成工单草稿。",
		Permission: "sql.console.access",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"instance_id": map[string]any{"type": "string", "description": "实例业务 ID（inst- 开头）"},
				"host":        map[string]any{"type": "string", "description": "实例主机地址或 IP（与 instance_id 二选一）"},
				"port":        map[string]any{"type": "integer", "description": "实例端口，如 3306"},
				"schema":      map[string]any{"type": "string", "description": "库名 / 资源路径（PostgreSQL 形如 database.schema）"},
				"table":       map[string]any{"type": "string", "description": "表名，必填"},
				"sql":         map[string]any{"type": "string", "description": "可选：基于这条 SQL 的过滤、关联与排序列给建议"},
			},
			"required": []string{"schema", "table"},
		},
		Handler: func(ctx *Context, args map[string]any) (any, error) {
			instance, err := resolveToolInstance(ctx, args)
			if err != nil {
				return nil, err
			}
			schema := argString(args, "schema", "database", "db")
			table := argString(args, "table", "table_name")
			if schema == "" || table == "" {
				return nil, fmt.Errorf("需要同时提供 schema 与 table；可先用 list_tables 获取表清单")
			}
			sqlText := strings.TrimSpace(argString(args, "sql", "sql_text"))

			// 鉴权前置：索引分析要读建表语句，属于表结构范畴
			resources := []sqlResource{{Schema: schema, Table: table}}
			if sqlText != "" {
				resources = append(resources, sqlToolResources(sqlText, schema)...)
			}
			denied, allowed, guardErr := guardSQLAccess(ctx, instance.InstanceID, resources)
			if guardErr != nil {
				return nil, guardErr
			}
			if !allowed {
				return denied, nil
			}

			dbType := plainString(instance.DbType)
			isPostgres := dasServices.IsPostgresFamily(dbType)
			ddl, err := fetchTableDDL(ctx, instance.InstanceID, schema, table)
			if err != nil {
				return nil, fmt.Errorf("读取表 %s 的结构失败：%s", table, strings.TrimSpace(err.Error()))
			}
			if strings.TrimSpace(ddl) == "" {
				return map[string]any{
					"instance_id":  instance.InstanceID,
					"schema":       schema,
					"table":        table,
					"db_type":      dbType,
					"table_count":  0,
					"column_count": 0,
					"hint":         "未取到建表语句，请确认库名与表名是否正确；表清单可用 list_tables 查看",
				}, nil
			}

			columns, indexes := parseCreateTable(ddl)
			indexKnown := len(indexes) > 0
			// PG 的建表语句里没有索引定义，额外查一次 pg_indexes 补齐；
			// 查不到就按「索引未知」处理，只给待确认的候选，不断言缺失。
			if isPostgres && !indexKnown {
				if fetched := fetchPostgresIndexes(ctx, instance.InstanceID, schema, table); len(fetched) > 0 {
					indexes = fetched
					indexKnown = true
				}
			}

			usedColumns := make([]string, 0)
			if sqlText != "" {
				if explained, parseErr := interpretSQL(sqlText, schema); parseErr == nil {
					for _, key := range []string{"where", "having"} {
						usedColumns = append(usedColumns, columnsInExpr(plainString(explained[key]), columns)...)
					}
					if joins, ok := explained["joins"].([]map[string]any); ok {
						for _, join := range joins {
							usedColumns = append(usedColumns, columnsInExpr(plainString(join["condition"]), columns)...)
						}
					}
					for _, key := range []string{"group_by", "order_by"} {
						if items, ok := explained[key].([]string); ok {
							for _, item := range items {
								usedColumns = append(usedColumns, columnsInExpr(item, columns)...)
							}
						}
					}
				}
				usedColumns = dedupeStrings(usedColumns)
			}

			suggestions := recommendTableIndexes(table, columns, indexes, usedColumns, indexKnown)
			if len(suggestions) > maxIndexSuggestions {
				suggestions = suggestions[:maxIndexSuggestions]
			}

			result := map[string]any{
				"instance_id":      instance.InstanceID,
				"schema":           schema,
				"table":            table,
				"db_type":          dbType,
				"column_count":     len(columns),
				"index_count":      len(indexes),
				"existing_indexes": indexTexts(indexes),
				"analyzed_sql":     sqlText,
				"sql_columns":      usedColumns,
				"suggestion_count": len(suggestions),
				"suggestions":      suggestions,
			}
			if !indexKnown {
				result["index_info"] = "未能确认该表已有的索引（当前数据库类型的表结构输出不含索引定义），" +
					"下面关于「缺失索引」的候选需要人工复核后再决定是否创建"
			}
			if len(suggestions) == 0 {
				result["hint"] = "基于现有信息没有发现明显的索引优化点；若某条 SQL 确实慢，可改用 explain_sql 看实际执行计划"
			} else {
				result["note"] = "以上仅为建议：索引会影响写入性能与优化器选择，落地前请在测试环境验证，并走工单审批"
			}
			return result, nil
		},
	}
}

func indexTexts(indexes []tableIndex) []string {
	out := make([]string, 0, len(indexes))
	for _, index := range indexes {
		prefix := "INDEX"
		switch {
		case index.Primary:
			prefix = "PRIMARY"
		case index.Unique:
			prefix = "UNIQUE"
		}
		out = append(out, fmt.Sprintf("%s %s(%s)", prefix, index.Name, strings.Join(index.Columns, ",")))
	}
	return out
}

func dedupeStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[strings.ToLower(value)]; ok {
			continue
		}
		seen[strings.ToLower(value)] = struct{}{}
		out = append(out, value)
	}
	return out
}

// fetchPostgresIndexes 查 pg_indexes 补齐 PG 侧的索引信息。
// 只读系统视图、且入参已过标识符白名单；失败时返回空，调用方按「索引未知」处理。
func fetchPostgresIndexes(ctx *Context, instanceID, schema, table string) []tableIndex {
	schemaName := schema
	if parts := strings.SplitN(schema, ".", 2); len(parts) == 2 {
		schemaName = parts[1]
	}
	if _, err := validateSampleIdentifier(schemaName, "schema"); err != nil {
		return nil
	}
	if _, err := validateSampleIdentifier(table, "table"); err != nil {
		return nil
	}
	query := fmt.Sprintf(
		"SELECT indexname, indexdef FROM pg_indexes WHERE schemaname = '%s' AND tablename = '%s'",
		schemaName, table,
	)
	ginCtx, cancel := newToolGinContext()
	defer cancel()
	service := dasServices.ExecutePostgresQueryService{
		ExecutePostgresQueryForm: &dasForms.ExecutePostgresQueryForm{
			InstanceID: instanceID,
			Schema:     schema,
			SQLText:    query,
		},
		C:        ginCtx,
		Username: usernameOf(ctx),
	}
	response, err := service.Run()
	if err != nil || response.Data == nil {
		return nil
	}
	out := make([]tableIndex, 0, len(*response.Data))
	for _, row := range *response.Data {
		name := cellString(pickRowValue(row, "indexname"))
		definition := cellString(pickRowValue(row, "indexdef"))
		if name == "" && definition == "" {
			continue
		}
		out = append(out, tableIndex{
			Name:    name,
			Columns: parseIndexDefinitionColumns(definition),
			Unique:  strings.Contains(strings.ToLower(definition), "create unique index"),
		})
	}
	return out
}

// parseIndexDefinitionColumns 从 CREATE INDEX ... USING btree (a, b) 里取列清单
func parseIndexDefinitionColumns(definition string) []string {
	start := strings.LastIndex(definition, "(")
	end := strings.LastIndex(definition, ")")
	if start < 0 || end <= start {
		return nil
	}
	return parseIndexColumns(definition[start+1 : end])
}

// ------------------------------------------------------------------
// sample_data
// ------------------------------------------------------------------

func sampleDataTool() *Tool {
	return &Tool{
		Name: "sample_data",
		Description: "从指定表抽样少量数据行，用于了解字段的实际取值形态与数据分布。" +
			"只生成并执行 SELECT ... LIMIT n（默认 10 行，最多 50 行），不执行任何写操作；" +
			"WHERE 只接受简单的列比较条件，不支持子查询与多语句。",
		Permission: "sql.console.access",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"instance_id": map[string]any{"type": "string", "description": "实例业务 ID（inst- 开头）"},
				"host":        map[string]any{"type": "string", "description": "实例主机地址或 IP（与 instance_id 二选一）"},
				"port":        map[string]any{"type": "integer", "description": "实例端口，如 3306"},
				"schema":      map[string]any{"type": "string", "description": "库名 / 资源路径（PostgreSQL 形如 database.schema）"},
				"table":       map[string]any{"type": "string", "description": "表名，必填"},
				"columns":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "只看这些列；留空表示取全部列"},
				"where":       map[string]any{"type": "string", "description": "简单的过滤条件，如 status = 1 AND created_at >= '2026-01-01'"},
				"limit":       map[string]any{"type": "integer", "description": fmt.Sprintf("采样行数，默认 %d，最多 %d", defaultSampleLimit, maxSampleLimit)},
			},
			"required": []string{"schema", "table"},
		},
		Handler: func(ctx *Context, args map[string]any) (any, error) {
			instance, err := resolveToolInstance(ctx, args)
			if err != nil {
				return nil, err
			}
			schema := argString(args, "schema", "database", "db")
			table := argString(args, "table", "table_name")
			if schema == "" || table == "" {
				return nil, fmt.Errorf("需要同时提供 schema 与 table；可先用 list_tables 获取表清单")
			}
			limit := argInt(args, "limit", defaultSampleLimit)
			if limit <= 0 {
				limit = defaultSampleLimit
			}
			if limit > maxSampleLimit {
				limit = maxSampleLimit
			}
			columns := argStringList(args, "columns")
			where, err := validateSampleWhere(argString(args, "where", "condition"))
			if err != nil {
				return nil, err
			}

			// 鉴权前置：采样会真正把业务数据读出来，这是五个工具里最敏感的一个
			denied, allowed, guardErr := guardSQLAccess(ctx, instance.InstanceID,
				[]sqlResource{{Schema: schema, Table: table}})
			if guardErr != nil {
				return nil, guardErr
			}
			if !allowed {
				return denied, nil
			}

			dbType := plainString(instance.DbType)
			isPostgres := dasServices.IsPostgresFamily(dbType)
			if !isPostgres && !dasServices.IsMySQLFamily(dbType) {
				return nil, fmt.Errorf("数据采样当前仅支持 MySQL 系与 PostgreSQL 系，当前实例类型为 %s", dbType)
			}
			query, err := buildSampleQuery(schema, table, columns, where, limit, isPostgres)
			if err != nil {
				return nil, err
			}

			ginCtx, cancel := newToolGinContext()
			defer cancel()
			username := usernameOf(ctx)
			var response dasServices.ResponseData
			if isPostgres {
				service := dasServices.ExecutePostgresQueryService{
					ExecutePostgresQueryForm: &dasForms.ExecutePostgresQueryForm{
						InstanceID: instance.InstanceID,
						Schema:     schema,
						SQLText:    query,
					},
					C:        ginCtx,
					Username: username,
				}
				response, err = service.Run()
			} else {
				service := dasServices.ExecuteMySQLQueryService{
					ExecuteMySQLQueryForm: &dasForms.ExecuteMySQLQueryForm{
						InstanceID: instance.InstanceID,
						Schema:     schema,
						SQLText:    query,
					},
					C:        ginCtx,
					Username: username,
				}
				response, err = service.Run()
			}
			if err != nil {
				return nil, fmt.Errorf("采样失败：%s", strings.TrimSpace(err.Error()))
			}

			columnNames := make([]string, 0)
			if response.Columns != nil {
				for index, column := range *response.Columns {
					if index >= sampleColumnLimit {
						break
					}
					columnNames = append(columnNames, column)
				}
			}
			sampleRows := make([]map[string]any, 0, limit)
			if response.Data != nil {
				for index, row := range *response.Data {
					if index >= limit {
						break
					}
					item := make(map[string]any, len(row))
					position := 0
					for _, column := range columnNames {
						position++
						if position > sampleColumnLimit {
							break
						}
						value := cellString(pickRowValue(row, column))
						if len(value) > sampleCellMaxChars {
							value = value[:sampleCellMaxChars] + "…（已截断）"
						}
						item[column] = value
					}
					sampleRows = append(sampleRows, item)
				}
			}

			result := map[string]any{
				"instance_id":  instance.InstanceID,
				"schema":       schema,
				"table":        table,
				"db_type":      dbType,
				"sql":          query,
				"row_count":    len(sampleRows),
				"limit":        limit,
				"columns":      columnNames,
				"rows":         sampleRows,
				"sampled_only": true,
			}
			if text := strings.TrimSpace(response.Duration); text != "" {
				result["duration"] = text
			}
			if len(sampleRows) == 0 {
				result["hint"] = "没有采样到数据：可能是表为空，或 WHERE 条件过窄；可去掉 where 再试一次"
			} else {
				result["note"] = "以上只是抽样结果（非全量）：可用于判断字段取值形态，不能用于统计口径；" +
					"长文本字段已截断，单值最多 " + fmt.Sprintf("%d", sampleCellMaxChars) + " 字符"
			}
			return result, nil
		},
	}
}
