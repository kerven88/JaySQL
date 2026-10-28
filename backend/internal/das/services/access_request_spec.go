package services

// ---------------------------------------------------------------------------
// 数据访问申请：多资源圈选（多库 / 多模式 / 表维度四种方式）
//
// 2026-10-23 前一张申请单只能圈一个资源；实际使用里「把业务库的十张订单表都给我」
// 只能拆成十张单子，审批人被同样的事由轰炸十遍。这里把申请单升级为一次圈选多个资源：
//
//	库 / 模式：多选（模式项为「库.模式」完整路径，跨库）；
//	表维度：list 指定表（多选，选一个即单表）/ wildcard 通配符 / except 排除表（反选）。
//
// 存储口径：
//   - 申请单 resource_spec 列存归一化 JSON（本文件的 accessResourceSpec），
//     旧三列（database / schema_name / table_name）只存第一个资源做兼容展示；
//   - 授权落地时展开成多条 insight_das_data_grants，每条带 request_no 指回申请单；
//   - spec 为空 = 旧数据，按旧三列解析成单资源，展示 / 落权 / 撤销联动全兼容。
//
// 通配符语法（wildcard）：* 任意字符序列、? 单个字符，其余字面匹配。
// 不用 SQL 的 % / _ —— 表名里下划线是常见字面字符，做通配符会让「order_%」这种
// 模式出现「到底是字面下划线还是单字符通配」的歧义。
// ---------------------------------------------------------------------------

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/global"
)

// 表维度圈选方式
const (
	AccessTableModeList     = "list"     // 指定表（单表 / 部分表）
	AccessTableModeWildcard = "wildcard" // 通配符
	AccessTableModeExcept   = "except"   // 排除表（反选）
)

// 多资源上限：防止一次申请把整库几千张表逐条展开成授权行
const (
	accessSpecMaxDatabases = 50
	accessSpecMaxSchemas   = 100
	accessSpecMaxTables    = 200
)

// accessResourceSpec 申请单的多资源快照（resource_spec 列的 JSON 结构）
type accessResourceSpec struct {
	// Databases 库名清单（database 级为圈选值；schema/table 级由路径派生，仅展示用）
	Databases []string `json:"databases,omitempty"`
	// Schemas 表维度的「容器」路径清单：三层结构为「库.模式」，单层结构为库名
	Schemas []string `json:"schemas,omitempty"`
	// TableMode 表维度圈选方式：list / wildcard / except（非表级为空）
	TableMode string `json:"table_mode,omitempty"`
	// Tables 指定表清单，每项带完整路径前缀：三层「库.模式.表」、单层「库.表」
	Tables []string `json:"tables,omitempty"`
	// Pattern 通配符模式（wildcard）
	Pattern string `json:"pattern,omitempty"`
	// Except 排除表清单（except），格式同 Tables
	Except []string `json:"except,omitempty"`
}

// parseAccessResourceSpec 解析申请单的 resource_spec。空串 = 旧数据，返回 nil。
func parseAccessResourceSpec(raw string) (*accessResourceSpec, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil, nil
	}
	var spec accessResourceSpec
	if err := json.Unmarshal([]byte(text), &spec); err != nil {
		return nil, fmt.Errorf("资源圈选快照解析失败: %w", err)
	}
	return &spec, nil
}

// specFromLegacyRow 旧单据兼容：按旧三列构造单资源 spec。
func specFromLegacyRow(level, schemaName, tableName string) *accessResourceSpec {
	switch level {
	case DataResourceLevelDatabase:
		if schemaName == "" {
			return nil
		}
		return &accessResourceSpec{Databases: []string{schemaName}}
	case DataResourceLevelSchema:
		if schemaName == "" {
			return nil
		}
		return &accessResourceSpec{Databases: []string{accessRequestDatabaseOf(schemaName)}, Schemas: []string{schemaName}}
	case DataResourceLevelTable:
		if schemaName == "" || tableName == "" {
			return nil
		}
		return &accessResourceSpec{
			Databases: []string{accessRequestDatabaseOf(schemaName)},
			Schemas:   []string{schemaName},
			TableMode: AccessTableModeList,
			Tables:    []string{schemaName + "." + tableName},
		}
	}
	return nil
}

// normalizeStringList 去空白、去空项、去重、排序，产出可比较的规范列表。
func normalizeStringList(items []string, limit int, label string) ([]string, error) {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		v := strings.TrimSpace(item)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, nil
	}
	if limit > 0 && len(out) > limit {
		return nil, fmt.Errorf("%s数量超过上限 %d", label, limit)
	}
	sort.Strings(out)
	return out, nil
}

// splitResourcePath 按「.」拆资源路径：期望 want 段，段数不符报错。
func splitResourcePath(path string, want int, label string) ([]string, error) {
	parts := strings.Split(path, ".")
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			return nil, fmt.Errorf("%s 路径不合法：%s", label, path)
		}
	}
	if len(parts) != want {
		return nil, fmt.Errorf("%s 路径不合法：%s（应为 %d 段）", label, path, want)
	}
	return parts, nil
}

// buildAccessResourceSpec 从表单构造并校验多资源快照。
// hasSchemaLevel 决定路径段数：三层结构模式为「库.模式」两段、表为三段；
// 单层结构库就是库名、表为「库.表」两段。
// 旧单资源字段（DatabaseName / SchemaName / TableName）在新字段全空时兜底，
// 保证脚本 / AI 工具等旧调用方不受影响。
func buildAccessResourceSpec(level string, form *forms.AccessRequestCreateForm, hasSchemaLevel bool) (*accessResourceSpec, error) {
	spec := &accessResourceSpec{}

	databases, err := normalizeStringList(form.DatabaseNames, accessSpecMaxDatabases, "库")
	if err != nil {
		return nil, err
	}
	schemas, err := normalizeStringList(form.SchemaNames, accessSpecMaxSchemas, "模式")
	if err != nil {
		return nil, err
	}
	tables, err := normalizeStringList(form.TableNames, accessSpecMaxTables, "指定表")
	if err != nil {
		return nil, err
	}
	excepts, err := normalizeStringList(form.ExceptTables, accessSpecMaxTables, "排除表")
	if err != nil {
		return nil, err
	}

	// 旧单资源字段兜底：新字段全空时把旧值转成对应的新结构
	if databases == nil && schemas == nil && tables == nil && excepts == nil &&
		strings.TrimSpace(form.TablePattern) == "" {
		if v := strings.TrimSpace(form.DatabaseName); v != "" {
			databases = []string{v}
		}
		if v := strings.TrimSpace(form.SchemaName); v != "" {
			schemas = []string{v}
		}
		if v := strings.TrimSpace(form.TableName); v != "" {
			// 旧调用方的表名是单段裸名，补上容器前缀（旧字段必带 SchemaName / DatabaseName）
			if !strings.Contains(v, ".") {
				container := strings.TrimSpace(form.SchemaName)
				if container == "" {
					container = strings.TrimSpace(form.DatabaseName)
				}
				if container != "" {
					v = container + "." + v
				}
			}
			tables = []string{v}
		}
	}

	switch level {
	case DataResourceLevelInstance:
		return &accessResourceSpec{}, nil

	case DataResourceLevelDatabase:
		if len(databases) == 0 {
			return nil, errors.New("请至少选择一个库")
		}
		if len(schemas) > 0 || len(tables) > 0 || len(excepts) > 0 || form.TablePattern != "" ||
			strings.TrimSpace(form.TableMode) != "" {
			return nil, errors.New("库级申请只能选择库，不能携带模式 / 表")
		}
		spec.Databases = databases
		return spec, nil

	case DataResourceLevelSchema:
		if len(schemas) == 0 {
			return nil, errors.New("请至少选择一个模式")
		}
		if len(tables) > 0 || len(excepts) > 0 || form.TablePattern != "" ||
			strings.TrimSpace(form.TableMode) != "" {
			return nil, errors.New("模式级申请只能选择模式，不能携带表")
		}
		if !hasSchemaLevel {
			return nil, errors.New("该数据源没有独立的「模式」层级，请选择库或表")
		}
		dbs := make([]string, 0, len(schemas))
		for _, path := range schemas {
			parts, err := splitResourcePath(path, 2, "模式")
			if err != nil {
				return nil, err
			}
			dbs = append(dbs, parts[0])
		}
		spec.Databases, err = normalizeStringList(dbs, accessSpecMaxDatabases, "库")
		if err != nil {
			return nil, err
		}
		spec.Schemas = schemas
		return spec, nil

	case DataResourceLevelTable:
		mode := strings.ToLower(strings.TrimSpace(form.TableMode))
		if mode == "" {
			mode = AccessTableModeList
		}
		// 表维度的「容器」路径：三层结构 = SchemaNames（库.模式），
		// 单层结构 = DatabaseNames（库名就是容器，没有独立的模式层）。
		containers, err := normalizeStringList(
			map[bool][]string{true: schemas, false: databases}[hasSchemaLevel],
			accessSpecMaxSchemas, "模式")
		if err != nil {
			return nil, err
		}
		if !hasSchemaLevel {
			for _, path := range containers {
				if strings.Contains(path, ".") {
					return nil, errors.New("该数据源没有独立的「模式」层级，容器路径应为库名")
				}
			}
		}
		switch mode {
		case AccessTableModeList:
			if len(tables) == 0 {
				return nil, errors.New("请至少选择一张表")
			}
			// 路径段数：三层结构「库.模式.表」，单层结构「库.表」
			want := 2
			if hasSchemaLevel {
				want = 3
			}
			containerSet := make(map[string]struct{}, len(containers))
			for _, c := range containers {
				containerSet[c] = struct{}{}
			}
			for _, item := range tables {
				parts, err := splitResourcePath(item, want, "表")
				if err != nil {
					return nil, err
				}
				// 表所属容器必须已在所选清单内，防止「模式没选、表却带了前缀」的脏数据
				tableContainer := strings.Join(parts[:want-1], ".")
				if _, ok := containerSet[tableContainer]; !ok {
					return nil, fmt.Errorf("表 %s 不在所选库/模式下，请先选择 %s", item, tableContainer)
				}
			}
			spec.Schemas = containers
			spec.TableMode = AccessTableModeList
			spec.Tables = tables
		case AccessTableModeWildcard:
			pattern := strings.TrimSpace(form.TablePattern)
			if pattern == "" {
				return nil, errors.New("请填写表名通配符模式")
			}
			if err := validateTablePattern(pattern); err != nil {
				return nil, err
			}
			if len(containers) == 0 {
				if hasSchemaLevel {
					return nil, errors.New("请至少选择一个模式")
				}
				return nil, errors.New("请至少选择一个库")
			}
			spec.TableMode = AccessTableModeWildcard
			spec.Pattern = pattern
			spec.Schemas = containers
		case AccessTableModeExcept:
			if len(excepts) == 0 {
				return nil, errors.New("请至少选择一张要排除的表")
			}
			if len(containers) == 0 {
				if hasSchemaLevel {
					return nil, errors.New("请至少选择一个模式")
				}
				return nil, errors.New("请至少选择一个库")
			}
			want := 2
			if hasSchemaLevel {
				want = 3
			}
			containerSet := make(map[string]struct{}, len(containers))
			for _, c := range containers {
				containerSet[c] = struct{}{}
			}
			for _, item := range excepts {
				parts, err := splitResourcePath(item, want, "排除表")
				if err != nil {
					return nil, err
				}
				if _, ok := containerSet[strings.Join(parts[:want-1], ".")]; !ok {
					return nil, fmt.Errorf("排除表 %s 不在所选库/模式下，请先选择对应库/模式", item)
				}
			}
			spec.TableMode = AccessTableModeExcept
			spec.Schemas = containers
			spec.Except = excepts
		default:
			return nil, fmt.Errorf("不支持的表圈选方式：%s", mode)
		}
		// 库名从容器路径派生（模式级 / 表级的 Databases 仅展示用）
		dbs := make([]string, 0, len(spec.Schemas))
		for _, path := range spec.Schemas {
			dbs = append(dbs, accessRequestDatabaseOf(path))
		}
		spec.Databases, err = normalizeStringList(dbs, accessSpecMaxDatabases, "库")
		if err != nil {
			return nil, err
		}
		return spec, nil
	}
	return nil, fmt.Errorf("不支持的资源层级：%s", level)
}

// validateTablePattern 通配符模式的基础校验：非空、不超长、必须包含通配符或字面字符均可，
// 但拒绝控制字符与首尾空白（已在 TrimSpace 处理）。
func validateTablePattern(pattern string) error {
	if len(pattern) > 128 {
		return errors.New("通配符模式过长（最多 128 字符）")
	}
	for _, r := range pattern {
		if r == '\n' || r == '\r' || r == '\t' {
			return errors.New("通配符模式不能包含换行 / 制表符")
		}
	}
	if strings.Contains(pattern, "..") {
		return errors.New("通配符模式不能包含连续点号")
	}
	return nil
}

// wildcardMatchTable 通配符匹配表名：* 任意字符序列、? 单个字符，其余字面；
// 与 MySQL 的列排序规则一致按不区分大小写处理。
func wildcardMatchTable(pattern, table string) bool {
	if pattern == "" {
		return false
	}
	var b strings.Builder
	b.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return false
	}
	return re.MatchString(strings.ToLower(strings.TrimSpace(table)))
}

// matchTableByGrantRow 判定一条表级授权行是否覆盖指定表。
// mode 为空按 exact 处理（旧数据列默认值缺失时兜底）。
func matchTableByGrantRow(mode, stored, table string) bool {
	table = strings.TrimSpace(table)
	if table == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "exact":
		return strings.EqualFold(strings.TrimSpace(stored), table)
	case AccessTableModeWildcard:
		return wildcardMatchTable(stored, table)
	case AccessTableModeExcept:
		// stored = 逗号分隔的排除表清单；命中排除项 = 不覆盖
		for _, item := range strings.Split(stored, ",") {
			if strings.EqualFold(strings.TrimSpace(item), table) {
				return false
			}
		}
		return true
	}
	return false
}

// validateAccessSpecResources 校验圈选的资源都真实挂在实例下：
// 库名 / 容器路径必须在 insight_instance_schemas 里有对应行，防止拼出批了也授不了的路径。
// 表名不在此校验 —— 表清单来自实时连库（table-options），通配符也无法逐表验证，
// 审批人是这道防线的最后一环。
func validateAccessSpecResources(instanceID string, hasSchemaLevel bool, spec *accessResourceSpec) error {
	if spec == nil || spec.isEmpty() {
		return nil
	}
	// 需要校验的「库」：库级是 Databases 本身；三层结构的模式/表级校验 Schemas（库.模式），
	// 单层结构的表级 Schemas 存的就是库名。三层结构下元数据里库与模式是两类行，
	// 必须按 resource_type 区分，否则「库名」能冒充「模式」通过校验。
	paths := make([]string, 0, 2)
	checkSchemaType := false
	wantSchemaRow := false
	switch {
	case len(spec.Schemas) == 0:
		paths = append(paths, spec.Databases...)
		wantSchemaRow = hasSchemaLevel // 三层结构：库级必须命中 database 行
	case hasSchemaLevel:
		paths = append(paths, spec.Schemas...)
		checkSchemaType = true // 三层结构下这些路径必须是 schema 行，不能拿库名冒充
	default:
		paths = append(paths, spec.Schemas...)
	}
	if len(paths) == 0 {
		return nil
	}
	tx := global.App.DB.Table("insight_instance_schemas").
		Where("instance_id = ? AND is_deleted = 0 AND `schema` IN ?", instanceID, paths)
	if checkSchemaType {
		tx = tx.Where("resource_type = ?", "schema")
	} else if wantSchemaRow {
		tx = tx.Where("resource_type = ?", "database")
	}
	var count int64
	if err := tx.Count(&count).Error; err != nil {
		return err
	}
	if int(count) < len(paths) {
		return errors.New("部分库 / 模式在该数据源下不存在，请重新选择")
	}
	return nil
}

// specLegacyColumns 从 spec 派生旧三列的兼容值（只存第一个资源）。
// 授权落地不再依赖这三列，但列表搜索 / 历史展示口径还挂在这几列上。
func specLegacyColumns(level string, spec *accessResourceSpec) (database, schemaName, tableName string) {
	if spec == nil || spec.isEmpty() {
		return "", "", ""
	}
	switch level {
	case DataResourceLevelDatabase:
		if len(spec.Databases) > 0 {
			return spec.Databases[0], spec.Databases[0], ""
		}
	case DataResourceLevelSchema:
		if len(spec.Schemas) > 0 {
			return accessRequestDatabaseOf(spec.Schemas[0]), spec.Schemas[0], ""
		}
	case DataResourceLevelTable:
		if len(spec.Schemas) > 0 {
			database = accessRequestDatabaseOf(spec.Schemas[0])
			schemaName = spec.Schemas[0]
			switch spec.TableMode {
			case AccessTableModeWildcard:
				tableName = spec.Pattern
			case AccessTableModeExcept:
				for _, item := range spec.Except {
					if strings.HasPrefix(item, schemaName+".") {
						tableName = strings.TrimPrefix(item, schemaName+".")
						break
					}
				}
			default:
				for _, item := range spec.Tables {
					if strings.HasPrefix(item, schemaName+".") {
						tableName = item[len(schemaName)+1:]
						break
					}
				}
			}
		}
	}
	return database, schemaName, tableName
}

// accessGrantResource 一条授权行要落的资源（由 spec 展开而来）
type accessGrantResource struct {
	Level     string // instance / database / schema / table
	Schema    string // 库名或「库.模式」完整路径
	Table     string // 表名 / 通配符模式 / 逗号分隔的排除表清单
	TableMode string // exact / wildcard / except
}

// expandGrantResources 把 spec 展开成授权行清单（去重后）。
// 展开规则：
//
//	database 级 → 每库一条；schema 级 → 每条路径一条；
//	table 级 list → 每（路径, 表）一条 exact；wildcard → 每路径一条（table=模式）；
//	except → 每路径一条（table=该路径下的排除表清单）。
func (s *accessResourceSpec) expandGrantResources() []accessGrantResource {
	if s == nil {
		return nil
	}
	out := make([]accessGrantResource, 0, 8)
	seen := make(map[string]struct{}, 8)
	add := func(level, schema, table, mode string) {
		key := level + "|" + schema + "|" + table + "|" + mode
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, accessGrantResource{Level: level, Schema: schema, Table: table, TableMode: mode})
	}

	switch {
	case len(s.Schemas) == 0 && len(s.Databases) == 0:
		add(DataResourceLevelInstance, "", "", "exact")
	case len(s.Schemas) == 0:
		// 库级
		for _, db := range s.Databases {
			add(DataResourceLevelDatabase, db, "", "exact")
		}
	case s.TableMode == "":
		// 模式级：每条「库.模式」路径一条 schema 级授权
		for _, path := range s.Schemas {
			add(DataResourceLevelSchema, path, "", "exact")
		}
	default:
		// 表级：按圈选方式展开
		switch s.TableMode {
		case AccessTableModeWildcard:
			for _, path := range s.Schemas {
				add(DataResourceLevelTable, path, s.Pattern, AccessTableModeWildcard)
			}
		case AccessTableModeExcept:
			for _, path := range s.Schemas {
				excludes := make([]string, 0, 4)
				prefix := path + "."
				for _, item := range s.Except {
					if strings.HasPrefix(item, prefix) {
						excludes = append(excludes, strings.TrimPrefix(item, prefix))
					}
				}
				if len(excludes) == 0 {
					continue
				}
				sort.Strings(excludes)
				add(DataResourceLevelTable, path, strings.Join(excludes, ","), AccessTableModeExcept)
			}
		default:
			// list：按（路径, 表叶子）展开
			for _, item := range s.Tables {
				rest := item
				// 三层：库.模式.表；两层：库.表 —— 叶子是最后一段，路径是其余段
				idx := strings.LastIndex(item, ".")
				if idx <= 0 {
					continue
				}
				table := rest[idx+1:]
				path := item[:idx]
				// 路径必须命中已选容器，防止前端拼出「模式没选、表却带了前缀」的脏数据
				found := false
				for _, c := range s.Schemas {
					if c == path {
						found = true
						break
					}
				}
				if !found {
					continue
				}
				add(DataResourceLevelTable, path, table, "exact")
			}
		}
		// 模式清单为空（list 展开全部被过滤）时兜底：不产出任何行，由调用方校验报错
	}
	return out
}

// canonicalSpecJSON 归一化 JSON（列表已排序去重后序列化），用于去重比较与落库。
func (s *accessResourceSpec) canonicalSpecJSON() (string, error) {
	if s == nil {
		return "", nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// isEmpty 判断 spec 是否没有携带任何资源（instance 级也是空结构）。
func (s *accessResourceSpec) isEmpty() bool {
	if s == nil {
		return true
	}
	return len(s.Databases) == 0 && len(s.Schemas) == 0 && len(s.Tables) == 0 &&
		s.Pattern == "" && len(s.Except) == 0
}

// resourceSummary 生成人类可读的资源范围摘要（事件文案 / 审计用）。
func (s *accessResourceSpec) resourceSummary() string {
	if s == nil || s.isEmpty() {
		return "整个实例"
	}
	switch {
	case len(s.Schemas) == 0:
		return "库：" + strings.Join(s.Databases, "、")
	case s.TableMode == "":
		return "模式：" + strings.Join(s.Schemas, "、")
	case s.TableMode == AccessTableModeWildcard:
		return fmt.Sprintf("模式 %s 下表名匹配 %s", strings.Join(s.Schemas, "、"), s.Pattern)
	case s.TableMode == AccessTableModeExcept:
		return fmt.Sprintf("模式 %s 下除 %s 外的全部表",
			strings.Join(s.Schemas, "、"), strings.Join(s.Except, "、"))
	default:
		return "表：" + strings.Join(s.Tables, "、")
	}
}

// ---------------------------------------------------------------------------
// 授权清单（v2）：每条条目 = 一个库 / 模式对象 + 该对象自己的表圈选方式。
//
// v1 的多资源圈选共享一份 TableMode（db1 与 db2 只能同一种圈法），实际使用里
// 「db1 整库只读 + db2 指定表 + db3 通配」还是要拆单。参考数据授权弹窗的
// 授权清单模型，把申请单升级为条目列表：每个对象独立配置圈选方式。
//
// 存储口径：resource_spec 列存 {"entries":[...]}（本节结构）；
// v1 格式（databases/schemas/...）与旧三列继续兼容解析。
// ---------------------------------------------------------------------------

// accessSpecMaxEntries 授权清单条目上限
const accessSpecMaxEntries = 100

// accessResourceEntry 授权清单条目（resource_spec entries 数组的元素）
type accessResourceEntry struct {
	// Kind 对象类型：database 库 / schema 模式（库.模式）
	Kind string `json:"kind"`
	// Database 库名
	Database string `json:"database"`
	// Schema 模式完整路径「库.模式」，kind=schema 时非空
	Schema string `json:"schema,omitempty"`
	// TableMode 该对象下的表圈选方式：空 = 整个对象；list / wildcard / except
	TableMode string `json:"table_mode,omitempty"`
	// Tables 指定表叶子名清单（容器由条目自身决定）
	Tables []string `json:"tables,omitempty"`
	// Pattern 通配符模式（wildcard）
	Pattern string `json:"pattern,omitempty"`
	// Except 排除表叶子名清单（except）
	Except []string `json:"except,omitempty"`
}

// entryContainer 条目的资源容器路径：模式条目 =「库.模式」，库条目 = 库名。
func (e *accessResourceEntry) entryContainer() string {
	if e.Kind == "schema" {
		return e.Schema
	}
	return e.Database
}

// entryKey 清单去重键（同对象只允许出现一次）
func (e *accessResourceEntry) entryKey() string {
	return e.Kind + "|" + e.entryContainer()
}

// entryLabel 条目的展示名：库条目 = 库名，模式条目 = 库.模式
func (e *accessResourceEntry) entryLabel() string {
	return e.entryContainer()
}

// entryTableLevel 判断条目是否带表圈选（TableMode 非空）
func (e *accessResourceEntry) entryTableLevel() bool {
	return e.TableMode != ""
}

// normalizeEntryLeaves 表叶子名清单：去空白、去重、排序，拒绝带容器前缀 / 点号的脏值。
func normalizeEntryLeaves(items []string, limit int, label string) ([]string, error) {
	for _, item := range items {
		if strings.Contains(item, ".") {
			return nil, fmt.Errorf("%s 不应携带库/模式前缀：%s", label, item)
		}
	}
	return normalizeStringList(items, limit, label)
}

// buildAccessEntries 从表单构造并校验授权清单。
// kind=schema 仅三层结构受理；三层结构的整库条目只受理「整个库」，
// 单层结构（MySQL 系）的库条目本身就是表维度的容器，四种圈选方式都可用。
func buildAccessEntries(items []forms.AccessRequestEntryForm, hasSchemaLevel bool) ([]accessResourceEntry, error) {
	out := make([]accessResourceEntry, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for i := range items {
		f := &items[i]
		e := accessResourceEntry{
			Kind:      strings.ToLower(strings.TrimSpace(f.Kind)),
			Database:  strings.TrimSpace(f.Database),
			Schema:    strings.TrimSpace(f.Schema),
			TableMode: strings.ToLower(strings.TrimSpace(f.TableMode)),
			Pattern:   strings.TrimSpace(f.Pattern),
		}
		switch e.Kind {
		case "database":
			if e.Database == "" {
				return nil, errors.New("清单条目缺少库名")
			}
			if strings.Contains(e.Database, ".") {
				return nil, fmt.Errorf("库名不应包含点号：%s", e.Database)
			}
			if hasSchemaLevel && e.TableMode != "" {
				// 三层结构下表挂在模式下面，整库条目不能再带表圈选
				return nil, fmt.Errorf("库 %s 是整库授权，不能携带表圈选方式", e.Database)
			}
		case "schema":
			if !hasSchemaLevel {
				return nil, errors.New("该数据源没有独立的「模式」层级，清单里不能添加模式条目")
			}
			if e.Schema == "" {
				return nil, errors.New("模式条目缺少模式路径")
			}
			parts, err := splitResourcePath(e.Schema, 2, "模式")
			if err != nil {
				return nil, err
			}
			if parts[0] != e.Database {
				return nil, fmt.Errorf("模式 %s 与所属库 %s 不一致", e.Schema, e.Database)
			}
		default:
			return nil, fmt.Errorf("不支持的清单对象类型：%s", e.Kind)
		}

		switch e.TableMode {
		case "":
			// 整个对象：不允许残留任何表圈选字段
			if len(f.Tables) > 0 || len(f.Except) > 0 || e.Pattern != "" {
				return nil, fmt.Errorf("%s 是整个对象授权，不能携带表圈选内容", e.entryLabel())
			}
		case AccessTableModeList:
			tables, err := normalizeEntryLeaves(f.Tables, accessSpecMaxTables, "指定表")
			if err != nil {
				return nil, err
			}
			if len(tables) == 0 {
				return nil, fmt.Errorf("请为 %s 至少选择一张表", e.entryLabel())
			}
			e.Tables = tables
		case AccessTableModeWildcard:
			if e.Pattern == "" {
				return nil, fmt.Errorf("请为 %s 填写表名通配符", e.entryLabel())
			}
			if err := validateTablePattern(e.Pattern); err != nil {
				return nil, err
			}
		case AccessTableModeExcept:
			excepts, err := normalizeEntryLeaves(f.Except, accessSpecMaxTables, "排除表")
			if err != nil {
				return nil, err
			}
			if len(excepts) == 0 {
				return nil, fmt.Errorf("请为 %s 至少选择一张要排除的表", e.entryLabel())
			}
			e.Except = excepts
		default:
			return nil, fmt.Errorf("不支持的表圈选方式：%s", e.TableMode)
		}

		key := e.entryKey()
		if _, ok := seen[key]; ok {
			continue // 同对象重复加入：保留第一条
		}
		seen[key] = struct{}{}
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil, errors.New("授权清单不能为空")
	}
	if len(out) > accessSpecMaxEntries {
		return nil, fmt.Errorf("授权清单条目数超过上限 %d", accessSpecMaxEntries)
	}
	// 排序保证 resource_spec JSON 归一化（去重比较按全等，顺序敏感）
	sort.Slice(out, func(i, j int) bool { return out[i].entryKey() < out[j].entryKey() })
	return out, nil
}

// entriesCanonicalJSON 清单归一化 JSON（构建时已排序去重）
func entriesCanonicalJSON(entries []accessResourceEntry) (string, error) {
	if len(entries) == 0 {
		return "", nil
	}
	b, err := json.Marshal(map[string]any{"entries": entries})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// parseAccessEntries 解析 resource_spec 里的 entries 数组。
// 返回 ok=false 表示不是清单格式（v1 格式或空），调用方回退旧解析。
func parseAccessEntries(raw string) ([]accessResourceEntry, bool, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil, false, nil
	}
	var probe struct {
		Entries []json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal([]byte(text), &probe); err != nil {
		return nil, false, fmt.Errorf("资源圈选快照解析失败: %w", err)
	}
	if len(probe.Entries) == 0 {
		return nil, false, nil
	}
	var doc struct {
		Entries []accessResourceEntry `json:"entries"`
	}
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		return nil, false, fmt.Errorf("资源圈选快照解析失败: %w", err)
	}
	return doc.Entries, true, nil
}

// validateAccessEntriesResources 校验清单里的每个对象都真实挂在实例下：
// 库名必须命中 database 行、模式路径必须命中 schema 行（三层结构），
// 防止拼出批了也授不了的路径。表名不在此校验（清单来自实时连库，审批人把关）。
func validateAccessEntriesResources(instanceID string, hasSchemaLevel bool, entries []accessResourceEntry) error {
	dbPaths := make([]string, 0, len(entries))
	schemaPaths := make([]string, 0, len(entries))
	for i := range entries {
		e := &entries[i]
		if e.Kind == "schema" {
			schemaPaths = append(schemaPaths, e.Schema)
		} else {
			dbPaths = append(dbPaths, e.Database)
		}
	}
	count := func(paths []string, resourceType string) (int64, error) {
		if len(paths) == 0 {
			return 0, nil
		}
		var n int64
		tx := global.App.DB.Table("insight_instance_schemas").
			Where("instance_id = ? AND is_deleted = 0 AND `schema` IN ?", instanceID, paths)
		if resourceType != "" {
			tx = tx.Where("resource_type = ?", resourceType)
		}
		if err := tx.Count(&n).Error; err != nil {
			return 0, err
		}
		return n, nil
	}
	// 单层结构的元数据只有 database 行；三层结构的库名必须命中 database 行，
	// 不能拿「库.模式」里的同名段冒充。
	dbWanted := "database"
	if !hasSchemaLevel {
		dbWanted = ""
	}
	dbCount, err := count(dbPaths, dbWanted)
	if err != nil {
		return err
	}
	schemaCount, err := count(schemaPaths, "schema")
	if err != nil {
		return err
	}
	if int(dbCount) < len(dbPaths) || int(schemaCount) < len(schemaPaths) {
		return errors.New("部分库 / 模式在该数据源下不存在，请重新选择")
	}
	return nil
}

// entriesExpandGrantResources 把清单展开成授权行（去重后）。
// 整个对象 → 一条 database / schema 级授权；list → 每（容器, 表）一条 exact；
// wildcard → 每容器一条（table=模式）；except → 每容器一条（table=排除清单）。
func entriesExpandGrantResources(entries []accessResourceEntry) []accessGrantResource {
	out := make([]accessGrantResource, 0, len(entries)*2)
	seen := make(map[string]struct{}, len(entries)*2)
	add := func(level, schema, table, mode string) {
		key := level + "|" + schema + "|" + table + "|" + mode
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, accessGrantResource{Level: level, Schema: schema, Table: table, TableMode: mode})
	}
	for i := range entries {
		e := &entries[i]
		container := e.entryContainer()
		switch {
		case !e.entryTableLevel():
			level := DataResourceLevelDatabase
			if e.Kind == "schema" {
				level = DataResourceLevelSchema
			}
			add(level, container, "", "exact")
		case e.TableMode == AccessTableModeWildcard:
			add(DataResourceLevelTable, container, e.Pattern, AccessTableModeWildcard)
		case e.TableMode == AccessTableModeExcept:
			if len(e.Except) == 0 {
				continue
			}
			excludes := append([]string(nil), e.Except...)
			sort.Strings(excludes)
			add(DataResourceLevelTable, container, strings.Join(excludes, ","), AccessTableModeExcept)
		default:
			for _, leaf := range e.Tables {
				add(DataResourceLevelTable, container, leaf, "exact")
			}
		}
	}
	return out
}

// entriesLegacyColumns 从清单派生旧三列的兼容值（取排序后的第一条）。
func entriesLegacyColumns(entries []accessResourceEntry) (database, schemaName, tableName string) {
	if len(entries) == 0 {
		return "", "", ""
	}
	e := &entries[0]
	database = e.Database
	if e.Kind == "schema" {
		schemaName = e.Schema
	} else {
		schemaName = e.Database
	}
	switch e.TableMode {
	case AccessTableModeWildcard:
		tableName = e.Pattern
	case AccessTableModeExcept:
		if len(e.Except) > 0 {
			tableName = e.Except[0]
		}
	case AccessTableModeList:
		if len(e.Tables) > 0 {
			tableName = e.Tables[0]
		}
	}
	return database, schemaName, tableName
}

// entriesResourceSummary 清单的人类可读摘要（事件文案 / 审计用）。
func entriesResourceSummary(entries []accessResourceEntry) string {
	if len(entries) == 0 {
		return "整个实例"
	}
	parts := make([]string, 0, len(entries))
	for i := range entries {
		e := &entries[i]
		switch {
		case !e.entryTableLevel() && e.Kind == "database":
			parts = append(parts, "库 "+e.Database)
		case !e.entryTableLevel():
			parts = append(parts, "模式 "+e.Schema)
		case e.TableMode == AccessTableModeWildcard:
			parts = append(parts, fmt.Sprintf("%s 通配表 %s", e.entryLabel(), e.Pattern))
		case e.TableMode == AccessTableModeExcept:
			parts = append(parts, fmt.Sprintf("%s 除 %s 外全部表", e.entryLabel(), strings.Join(e.Except, "、")))
		default:
			parts = append(parts, fmt.Sprintf("%s 指定表 %s", e.entryLabel(), strings.Join(e.Tables, "、")))
		}
	}
	return strings.Join(parts, "；")
}

// derivedAccessResourceLevel 按清单内容派生申请单层级（表 > 模式 > 库）。
// 申请单 resource_level 只剩展示与检索用途，真实粒度以每条授权行为准。
func derivedAccessResourceLevel(entries []accessResourceEntry) string {
	hasTable, hasSchema := false, false
	for i := range entries {
		if entries[i].entryTableLevel() {
			hasTable = true
		}
		if entries[i].Kind == "schema" {
			hasSchema = true
		}
	}
	switch {
	case hasTable:
		return DataResourceLevelTable
	case hasSchema:
		return DataResourceLevelSchema
	default:
		return DataResourceLevelDatabase
	}
}
