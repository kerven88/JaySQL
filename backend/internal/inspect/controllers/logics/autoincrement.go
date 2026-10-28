package logics

import (
	"github.com/simajiong/jaysql/internal/inspect/controllers"
	"github.com/simajiong/jaysql/internal/inspect/controllers/process"
	"github.com/simajiong/jaysql/internal/inspect/controllers/traverses"
)

// LogicAutoIncrementCols 自增列检查（CREATE TABLE 与 ALTER TABLE ADD/MODIFY/CHANGE 共用）
// 覆盖规则：limit_auto_inc_col_name_字is_id / limit_auto_inc_col_is_unsigned
func LogicAutoIncrementCols(v *traverses.TraverseAutoIncrementCols, r *controllers.RuleHint) {
	for _, item := range v.Cols {
		var p process.AutoIncrementColumn = item
		p.InspectParams = r.InspectParams
		fns := []struct {
			name string
			fn   func() error
		}{
			{"limit_auto_inc_col_name_is_id", p.CheckNameIsID},
			{"limit_auto_inc_col_is_unsigned", p.CheckUnsigned},
		}
		for _, f := range fns {
			if err := f.fn(); err != nil {
				r.WarnWithRule(f.name, err.Error())
			}
		}
	}
}
