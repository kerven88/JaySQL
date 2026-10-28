package process

import (
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/internal/inspect/config"

	"github.com/pingcap/tidb/pkg/parser/mysql"
)

// AutoIncrementColumn 自增列（规则 limit_auto_inc_col_name_字is_id / limit_auto_inc_col_is_unsigned）
type AutoIncrementColumn struct {
	Table         string // 表名
	Column        string // 列名
	Tp            byte   // 类型
	Flag          uint   // flag
	InspectParams *config.InspectParams
}

// CheckNameIsID 限制自增列名字为 ID（大小写不敏感）
func (c *AutoIncrementColumn) CheckNameIsID() error {
	if !c.InspectParams.CHECK_AUTOINCREMENT_COL_NAME_IS_ID {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(c.Column), "id") {
		return fmt.Errorf("自增列`%s`命名必须为 id（表`%s`）", c.Column, c.Table)
	}
	return nil
}

// CheckUnsigned 限制自增列为无符号
func (c *AutoIncrementColumn) CheckUnsigned() error {
	if !c.InspectParams.CHECK_AUTOINCREMENT_COL_UNSIGNED {
		return nil
	}
	if !mysql.HasUnsignedFlag(c.Flag) {
		return fmt.Errorf("自增列`%s`必须定义为unsigned（表`%s`）", c.Column, c.Table)
	}
	return nil
}
