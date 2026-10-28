package secret

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"gorm.io/gorm"
)

// FieldSpec 描述一个需要纳管的敏感字段。
//
// JSONPath 非空表示该列存的是 JSON，需按路径定位内部敏感值，例如：
//
//	secret.FieldSpec{Table: "insight_instances", Column: "extra_config",
//	    JSONPath: []string{"$.ssh_password", "$.ssh_private_key"}}
//
// 支持数组通配：$.channels[*].secret
type FieldSpec struct {
	Table    string
	Column   string
	JSONPath []string
}

// Key 返回 "table.column"，用作 map 键。
func (f FieldSpec) Key() string {
	return strings.ToLower(f.Table) + "." + strings.ToLower(f.Column)
}

// 默认清单。业务包可以在 init() 里就近追加（见 Register），
// 这里放的是「跨模块共用、没有单一归属」的字段。
//
// 注意：insight_users.password 是 bcrypt（不可逆），**不要**纳管。
var defaultSpecs = []FieldSpec{
	// ---- 数据源 ----
	{Table: "insight_instances", Column: "password"},
	{Table: "insight_instances", Column: "extra_config", JSONPath: []string{"$.ssh_password", "$.ssh_private_key"}},

	// ---- 通知 ----
	{Table: "insight_notify_settings", Column: "mail_password"},

	// ---- AI ----
	{Table: "insight_llm_providers", Column: "api_key_enc"},

	// ---- 备份 ----
	{Table: "insight_backup_storage_targets", Column: "ssh_password"},
	{Table: "insight_backup_storage_targets", Column: "ssh_private_key"},
	{Table: "insight_backup_storage_targets", Column: "s3_access_key"},
	{Table: "insight_backup_storage_targets", Column: "s3_secret_key"},
	{Table: "insight_backup_nodes", Column: "ssh_password"},
	{Table: "insight_backup_nodes", Column: "ssh_private_key"},

	// ---- 云账号 ----
	{Table: "insight_cloud_accounts", Column: "access_key_id"},
	{Table: "insight_cloud_accounts", Column: "access_key_secret"},
}

var registry = struct {
	mu    sync.RWMutex
	specs map[string]FieldSpec
}{specs: map[string]FieldSpec{}}

func init() {
	Register(defaultSpecs...)
}

// Register 注册敏感字段。建议在各 models 包的 init() 中就近声明，
// 字段与其所属表放在一起，改表时不容易忘记同步。
//
// 重复注册同一 table.column 会被后者覆盖（等价幂等）。
func Register(specs ...FieldSpec) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for _, s := range specs {
		s.Table = strings.ToLower(strings.TrimSpace(s.Table))
		s.Column = strings.ToLower(strings.TrimSpace(s.Column))
		if s.Table == "" || s.Column == "" {
			continue
		}
		registry.specs[s.Key()] = s
	}
}

// FieldsFor 返回某表的敏感字段清单（供 GORM 插件与脱敏器使用）。
func FieldsFor(table string) []FieldSpec {
	table = strings.ToLower(strings.TrimSpace(table))
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	out := make([]FieldSpec, 0, 4)
	for _, s := range registry.specs {
		if s.Table == table {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Column < out[j].Column })
	return out
}

// All 返回全部已注册字段（按 table.column 排序，便于日志/审计输出稳定）。
func All() []FieldSpec {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	out := make([]FieldSpec, 0, len(registry.specs))
	for _, s := range registry.specs {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}

// Validate 启动期校验：清单里的表/列是否真实存在。
//
// 表被删、列被改名时能立刻发现，而不是等线上出现明文才察觉。
// 表不存在的场合（例如 insight_cloud_accounts 在测试环境未建表）只告警不报错。
func Validate(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("secret: Validate 需要传入 *gorm.DB")
	}
	var problems []string
	for _, spec := range All() {
		count := int64(0)
		err := db.Raw(
			"SELECT COUNT(1) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?",
			spec.Table, spec.Column).Scan(&count).Error
		if err != nil {
			// 连不上元库或没权限：无法校验，交给启动流程决定是否致命
			return fmt.Errorf("secret: 校验字段 %s 失败: %w", spec.Key(), err)
		}
		if count == 0 {
			problems = append(problems, spec.Key())
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("secret: 敏感字段清单里有不存在的列（表未创建或列名已变更）: %s", strings.Join(problems, ", "))
	}
	return nil
}
