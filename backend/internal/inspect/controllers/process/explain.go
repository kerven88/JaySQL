package process

import (
	"errors"
	"strconv"
	"strings"

	"github.com/simajiong/jaysql/pkg/kv"
	"github.com/simajiong/jaysql/pkg/utils"

	"github.com/simajiong/jaysql/internal/inspect/controllers/dao"

	"github.com/mitchellh/mapstructure"
)

// 对应explain的输出
type ExplainOutput struct {
	Table string `json:"Column:table"`
	// MySQL 的 Explain 预估行数。
	// 多表关联（JOIN）的 EXPLAIN 中可能出现 rows=NULL 的行（未匹配到行），
	// 因此用 interface{} 承接原始值后统一转换，避免整条语句被判成 0 行。
	Rows interface{} `json:"Column:rows"`

	// TiDB (v4.0及之后)的Explain预估行数存储在Count中
	EstRows interface{} `json:"Column:estRows"`

	// MySQL EXPLAIN 的访问类型（type 列）：ALL=全表扫描，ref/range/index/eq_ref...
	// 用于「UPDATE/DELETE 必须使用索引」规则（update_delete_stmt_must_use_idx）。
	Type string `json:"Column:type" mapstructure:"type"`
	// MySQL EXPLAIN 实际使用的索引（key 列）；未使用索引时为 NULL（DAO 层统一转成字符串 "NULL"）
	Key string `json:"Column:key" mapstructure:"key"`
	// MySQL EXPLAIN 可能用到的索引（possible_keys 列），仅用于告警文案给出建议
	PossibleKeys string `json:"Column:possible_keys" mapstructure:"possible_keys"`
	// MySQL EXPLAIN 的附加信息（Extra 列），如 "Using where; Using index"。
	// 「必须使用索引」规则用它识别覆盖索引扫描（Using index）：type=index 但
	// 全程只读索引不回表，属于真用上了索引，不应告警。
	// mapstructure 匹配大小写不敏感，MySQL 8 传统 EXPLAIN 的列名是 "Extra"。
	Extra string `json:"Column:extra" mapstructure:"extra"`
}

// explainIntValue 把 EXPLAIN 输出中的数值字段归一为 int。
// 取值来源是 DAO 层：NULL 被转成字符串 "NULL"，其余为字符串形式的数字。
// 无法解析（NULL / N/A / 空）时返回 false，由调用方决定跳过还是置 0。
func explainIntValue(value interface{}) (int, bool) {
	switch v := value.(type) {
	case nil:
		return 0, false
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		text := strings.TrimSpace(v)
		if text == "" || strings.EqualFold(text, "NULL") || strings.EqualFold(text, "N/A") {
			return 0, false
		}
		if i, err := strconv.Atoi(text); err == nil {
			return i, true
		}
		if f, err := strconv.ParseFloat(text, 64); err == nil {
			return int(f), true
		}
	}
	return 0, false
}

type Explain struct {
	DB  *dao.DB
	SQL string
	KV  *kv.KVCache
}

func (e Explain) ConvertToExplain() string {
	var explain []string
	explain = append(explain, "EXPLAIN ")
	explain = append(explain, e.SQL)
	return strings.Join(explain, "")
}

// fetchRows 执行 EXPLAIN 并返回原始结果行。
//
// 同一次审核里「必须使用索引」与「影响行数」两条规则都会 EXPLAIN 同一条 SQL，
// 这里按 EXPLAIN 语句文本做请求内缓存（KV 是 per-request 的），避免对目标库重复执行 EXPLAIN。
func (e *Explain) fetchRows() (*[]map[string]interface{}, error) {
	explainSQL := e.ConvertToExplain()
	if !strings.HasPrefix(explainSQL, "EXPLAIN") {
		return nil, errors.New("Explain语句未检测到以`EXPLAIN`开头，请联系管理员")
	}
	cacheKey := "explain_rows:" + explainSQL
	if e.KV != nil {
		if cached, ok := e.KV.Get(cacheKey).(*[]map[string]interface{}); ok && cached != nil {
			return cached, nil
		}
	}
	rows, err := e.DB.Query(explainSQL)
	if err != nil {
		return nil, err
	}
	if e.KV != nil {
		e.KV.Put(cacheKey, rows)
	}
	return rows, nil
}

// ExplainIndexUsage 「UPDATE/DELETE 是否使用了索引」的判定结果
//
// Checked=false 表示当前数据源无法判定（TiDB 的 EXPLAIN 没有 type/key 列、
// 或 EXPLAIN 输出里没有可判定的真实表），调用方必须跳过，不能据此告警。
type ExplainIndexUsage struct {
	Checked      bool   // 是否完成判定
	Used         bool   // 是否使用了索引（所有被访问的表都用上索引才为 true）
	Table        string // 首个未使用索引的表名
	AccessType   string // 该表的访问类型（EXPLAIN type 列，如 ALL/range/ref）
	PossibleKeys string // 该表可选索引（EXPLAIN possible_keys 列），用于告警文案给建议
}

// GetIndexUsage 判定 UPDATE/DELETE 是否使用了索引（规则 update_delete_stmt_must_use_idx）。
//
// 判定口径（任一命中即判为「未使用索引」）：
//  1. EXPLAIN type 为 ALL（全表扫描）或 index（全索引扫描）；
//  2. EXPLAIN key 为空 / NULL（未选中任何索引）。
//
// 两条都要看：MySQL 8 的 `EXPLAIN UPDATE` 恒带 key=PRIMARY（更新需按主键定位行），
// 只看 key 会让 UPDATE 侧永不告警；type 则能区分「扫全表」与「经索引定位」。
// 派生表 / 临时表（table 形如 <derived2>、<union1,2>、NULL）不参与判定，避免误报。
func (e *Explain) GetIndexUsage() (ExplainIndexUsage, error) {
	var usage ExplainIndexUsage
	// TiDB 的 EXPLAIN 是树状计划（id/estRows/task/operator），没有 type/key 列，
	// 无法按同一口径判定，直接跳过（不误报）。
	dbVersion := ""
	if e.KV != nil {
		if v, ok := e.KV.Get("dbVersion").(string); ok {
			dbVersion = v
		}
	}
	dbVersionIns := DbVersion{dbVersion}
	if dbVersionIns.IsTiDB() {
		return usage, nil
	}
	rows, err := e.fetchRows()
	if err != nil {
		return usage, err
	}
	var data []ExplainOutput
	if err := mapstructure.WeakDecode(rows, &data); err != nil {
		return usage, err
	}
	return judgeIndexUsage(data), nil
}

// judgeIndexUsage 依据 EXPLAIN 输出判定索引使用情况（纯函数，便于单测）。
func judgeIndexUsage(data []ExplainOutput) ExplainIndexUsage {
	var usage ExplainIndexUsage
	// EXPLAIN 输出里没有 type/key 列（非 MySQL 形态，如 TiDB 走到这里）时不判定
	hasIndexColumns := false
	for _, item := range data {
		if strings.TrimSpace(item.Type) != "" || strings.TrimSpace(item.Key) != "" {
			hasIndexColumns = true
			break
		}
	}
	if !hasIndexColumns {
		return usage
	}
	for _, item := range data {
		table := strings.TrimSpace(item.Table)
		// 派生表 / 联合结果 / 无表名行不做索引判定
		if table == "" || strings.EqualFold(table, "NULL") || strings.HasPrefix(table, "<") {
			continue
		}
		if !explainUsesIndex(item.Type, item.Key, item.Extra) {
			usage.Checked = true
			usage.Used = false
			usage.Table = table
			usage.AccessType = strings.TrimSpace(item.Type)
			usage.PossibleKeys = strings.TrimSpace(item.PossibleKeys)
			return usage
		}
		usage.Checked = true
	}
	usage.Used = usage.Checked
	return usage
}

// dmlAccessTypesWithoutIndex 「没有用索引做过滤定位」的访问类型（EXPLAIN type 列，小写）。
//
//   - ALL：全表扫描；
//   - index：全索引扫描——对 UPDATE/DELETE 意味着整张表逐行扫一遍。
//
// 为什么不能只看 key：MySQL 8 的 `EXPLAIN UPDATE` 因为更新要按主键定位行，
// 即使 WHERE 条件没有任何可用索引，输出也是 `type=index, key=PRIMARY`（实测 8.0.43/8.0.45）。
// 只看「key 是否为空」会让 UPDATE 侧永不告警、规则形同虚设，所以以 type 为主、key 为辅。
var dmlAccessTypesWithoutIndex = map[string]bool{
	"all":   true,
	"index": true,
}

// explainUsesIndex 判断 EXPLAIN 的这一行是否「用上了索引做过滤定位」
//
// type=index（全索引扫描）默认判未使用——这是为 MySQL 8 EXPLAIN UPDATE
// 恒带 type=index + key=PRIMARY 的行为兜底（WHERE 无可用索引仍按主键全扫）。
// 但覆盖索引扫描例外：key 有效且 Extra 含 "Using index"（全程只读二级索引、
// 不回表取行），属于真用上了索引——典型如多表删除里被驱动表按覆盖索引扫小表
// （实测：`DELETE rp FROM a rp JOIN b p ...` 的 p 行 type=index + Using index）。
func explainUsesIndex(accessType string, key string, extra string) bool {
	accessTypeLower := strings.ToLower(strings.TrimSpace(accessType))
	if dmlAccessTypesWithoutIndex[accessTypeLower] {
		// 覆盖索引扫描：索引已包含查询/过滤所需的全部列，不回表。
		// Extra 是分号分隔的短语列表，按短语精确匹配；"Using index condition"
		//（ICP，索引条件下推后仍需回表）包含 "using index" 子串，必须排除。
		if accessTypeLower == "index" && explainIndexIsUsed(key) && hasExtraFlag(extra, "using index") {
			return true
		}
		return false
	}
	return explainIndexIsUsed(key)
}

// explainIndexIsUsed 判断 EXPLAIN 的 key 列是否表示「用上了索引」。
// DAO 层把 SQL NULL 统一转成字符串 "NULL"，其余情形按空串处理。
func explainIndexIsUsed(key string) bool {
	k := strings.TrimSpace(key)
	return k != "" && !strings.EqualFold(k, "NULL")
}

// hasExtraFlag 判断 EXPLAIN Extra 列（分号分隔的短语列表，如
// "Using where; Using index"）中是否包含某个短语（大小写不敏感、整短语匹配）。
func hasExtraFlag(extra, flag string) bool {
	flag = strings.ToLower(strings.TrimSpace(flag))
	for _, part := range strings.Split(extra, ";") {
		if strings.EqualFold(strings.TrimSpace(part), flag) {
			return true
		}
	}
	return false
}

func (e *Explain) Get(EXPLAIN_RULE string) (int, error) {
	rows, err := e.fetchRows()
	if err != nil {
		return 0, err
	}
	// 赋值给结构体
	var data []ExplainOutput
	err = mapstructure.WeakDecode(rows, &data)
	if err != nil {
		return 0, err
	}
	// 获取db版本
	dbVersionIns := DbVersion{e.KV.Get("dbVersion").(string)}
	isTiDB := dbVersionIns.IsTiDB()

	var AffectedRows []int
	for _, item := range data {
		// 多表关联的 EXPLAIN 会出现 rows=NULL 的行（如未匹配到行）：
		// 这类行直接跳过，不参与统计，也不把整条语句判成 0 行——否则多表 UPDATE/DELETE
		// 的影响行数规则永远不触发。
		if isTiDB {
			// tidb的执行计划第一行可能是 estRows=N/A
			if intEstRows, ok := explainIntValue(item.EstRows); ok {
				AffectedRows = append(AffectedRows, intEstRows)
			}
			continue
		}
		if rowsCount, ok := explainIntValue(item.Rows); ok {
			AffectedRows = append(AffectedRows, rowsCount)
		}
	}
	if len(AffectedRows) == 0 {
		return 0, nil
	}
	if EXPLAIN_RULE == "first" {
		return AffectedRows[0], nil
	}
	if EXPLAIN_RULE == "max" {
		return utils.MaxInt(AffectedRows), nil
	}
	return 0, nil
}
