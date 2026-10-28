package process

import (
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"

	"github.com/pingcap/tidb/pkg/parser/mysql"
)

// 主键列类型白名单：未配置（空白名单）=> 不限制类型，任意类型都放行
func TestPrimaryKeyTypeEmptyWhitelistAllowsAll(t *testing.T) {
	types := []byte{mysql.TypeLonglong, mysql.TypeVarchar, mysql.TypeJSON, mysql.TypeGeometry, mysql.TypeDatetime}
	for _, tp := range types {
		p := &PrimaryKey{
			Table: "t_order", Column: "id", Tp: tp,
			InspectParams: &config.InspectParams{
				CHECK_PRIMARYKEY_USE_BIGINT: true,
				PRIMARYKEY_SUPPORT_TYPES:    nil, // 未配置
			},
		}
		if err := p.CheckBigint(); err != nil {
			t.Fatalf("空白名单应放行任意类型，类型字节码 %d 却报错: %v", tp, err)
		}
	}
	// 空切片同样视为未配置
	p := &PrimaryKey{
		Table: "t_order", Column: "id", Tp: mysql.TypeVarchar,
		InspectParams: &config.InspectParams{
			CHECK_PRIMARYKEY_USE_BIGINT: true,
			PRIMARYKEY_SUPPORT_TYPES:    []string{},
		},
	}
	if err := p.CheckBigint(); err != nil {
		t.Fatalf("空切片白名单应放行，实际报错: %v", err)
	}
}

// 主键列类型白名单：白名单内放行、白名单外报错，且报错文案回显白名单
func TestPrimaryKeyTypeWhitelistEnforced(t *testing.T) {
	params := &config.InspectParams{
		CHECK_PRIMARYKEY_USE_BIGINT: true,
		PRIMARYKEY_SUPPORT_TYPES:    []string{"bigint", "int"},
	}

	inList := &PrimaryKey{Table: "t_order", Column: "id", Tp: mysql.TypeLonglong, InspectParams: params}
	if err := inList.CheckBigint(); err != nil {
		t.Fatalf("bigint 在白名单 [bigint,int] 内不应报错: %v", err)
	}

	notInList := &PrimaryKey{Table: "t_order", Column: "id", Tp: mysql.TypeVarchar, InspectParams: params}
	err := notInList.CheckBigint()
	if err == nil {
		t.Fatal("varchar 不在白名单 [bigint,int] 内应报错")
	}
	if !strings.Contains(err.Error(), "仅允许使用bigint, int") {
		t.Errorf("报错文案应回显白名单，实际: %s", err.Error())
	}
	if strings.Contains(err.Error(), "unsigned") {
		t.Errorf("不应再出现 unsigned 相关文案，实际: %s", err.Error())
	}
}

// 规则关闭 => 不检查主键列类型
func TestPrimaryKeyTypeRuleOffSkips(t *testing.T) {
	p := &PrimaryKey{
		Table: "t_order", Column: "id", Tp: mysql.TypeVarchar,
		InspectParams: &config.InspectParams{CHECK_PRIMARYKEY_USE_BIGINT: false},
	}
	if err := p.CheckBigint(); err != nil {
		t.Fatalf("规则关闭不应报错: %v", err)
	}
}

// MySQL 可选值字典中的每一项都必须能被引擎识别。
// 否则用户在前端选中的"合法"类型会导致所有主键被误判为不合规（配置看似生效实则失效）。
// 注：PostgreSQL / Oracle 的类型白名单不走 TiDB parser 字节码映射（它们由各自的
// checker 用类型名字符串直接比对），故这里只校验 MySQL 家族。
func TestPrimaryKeySupportedTypesAllRecognizedByEngine(t *testing.T) {
	for _, name := range config.MySQLPrimaryKeyTypes {
		if _, ok := pkTypeNames[name]; !ok {
			t.Errorf("可选值 %q 在 pkTypeNames 中无对应类型映射：前端可选但引擎无法识别", name)
		}
	}
}

// 类型字典必须是小写、去空格、无重复（前端与后端逐项比对，大小写不一致会漏配）
func TestPrimaryKeyTypeDictNormalized(t *testing.T) {
	groups := map[string][]string{
		"mysql":      config.MySQLPrimaryKeyTypes,
		"postgresql": config.PostgreSQLPrimaryKeyTypes,
		"oracle":     config.OraclePrimaryKeyTypes,
	}
	for db, list := range groups {
		seen := map[string]struct{}{}
		for _, name := range list {
			if name != strings.ToLower(strings.TrimSpace(name)) {
				t.Errorf("%s 类型字典存在未归一化的项: %q", db, name)
			}
			if _, dup := seen[name]; dup {
				t.Errorf("%s 类型字典存在重复项: %q", db, name)
			}
			seen[name] = struct{}{}
		}
	}
}
