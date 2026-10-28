package process

import (
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/inspect/config"
	"github.com/pingcap/tidb/pkg/parser/mysql"
)

// 主键列类型白名单可配置：命中放行、未配置回退 bigint/int、不匹配拦截且回显白名单
func TestPrimaryKeyCheckBigintConfigurable(t *testing.T) {
	base := func() *PrimaryKey {
		params := config.DefaultInspectParams()
		return &PrimaryKey{
			Table:         "t_demo",
			Column:        "id",
			Tp:            mysql.TypeLong, // int
			InspectParams: &params,
		}
	}

	// 1) 规则关闭：不检查
	pk := base()
	if err := pk.CheckBigint(); err != nil {
		t.Fatalf("规则关闭不应报错，实际: %v", err)
	}

	// 2) 规则开启未配置白名单：默认 bigint/int，int 放行
	pk = base()
	pk.InspectParams.CHECK_PRIMARYKEY_USE_BIGINT = true
	if err := pk.CheckBigint(); err != nil {
		t.Fatalf("int 在默认白名单内不应报错，实际: %v", err)
	}

	// 3) 白名单 [bigint]：int 命中且回显白名单
	pk = base()
	pk.InspectParams.CHECK_PRIMARYKEY_USE_BIGINT = true
	pk.InspectParams.PRIMARYKEY_SUPPORT_TYPES = []string{"bigint"}
	err := pk.CheckBigint()
	if err == nil {
		t.Fatal("int 不在白名单 [bigint] 内应报错")
	}
	if !strings.Contains(err.Error(), "bigint") {
		t.Errorf("报错文案应回显白名单，实际: %s", err.Error())
	}

	// 4) 白名单 [varchar]：varchar 主键放行
	pk = base()
	pk.Tp = mysql.TypeVarchar
	pk.Column = "code"
	pk.InspectParams.CHECK_PRIMARYKEY_USE_BIGINT = true
	pk.InspectParams.PRIMARYKEY_SUPPORT_TYPES = []string{"varchar"}
	if err := pk.CheckBigint(); err != nil {
		t.Fatalf("varchar 在白名单 [varchar] 内不应报错，实际: %v", err)
	}
}
