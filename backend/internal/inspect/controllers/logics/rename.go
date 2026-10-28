package logics

import (
	"github.com/simajiong/jaysql/pkg/utils"

	"github.com/simajiong/jaysql/internal/inspect/controllers"
	"github.com/simajiong/jaysql/internal/inspect/controllers/dao"
	"github.com/simajiong/jaysql/internal/inspect/controllers/traverses"
)

// LogicRenameTable
func LogicRenameTable(v *traverses.TraverseRenameTable, r *controllers.RuleHint) {
	if v.IsMatch == 0 {
		return
	}
	if !r.InspectParams.ENABLE_RENAME_TABLE_NAME {
		r.Warn("禁止执行 `RENAME TABLE`")
		return
	}
	var oldTables []string
	// 旧表必须存在：避免把不存在的表重命名。
	for _, t := range v.Tables {
		if msg, err := dao.CheckIfTableExists(t.OldTable, r.DB); err != nil {
			r.WarnFatal(
				"obj_tbl_must_exist",
				controllers.CategoryObjectMissing,
				t.OldTable,
				"① 核对原表名拼写与大小写；② 确认是否选错了目标库；③ 若确认存在仍被报缺失，请先同步实例元数据再重新检查",
				msg,
			)
		} else {
			oldTables = append(oldTables, t.OldTable)
		}
	}
	// 新表名不能已存在：避免覆盖现有对象导致数据风险。
	for _, t := range v.Tables {
		if len(oldTables) > 0 && utils.IsContain(oldTables, t.NewTable) {
			continue
		}
		if msg, err := dao.CheckIfTableExists(t.NewTable, r.DB); err == nil {
			r.WarnFatal(
				"obj_tbl_must_not_exist",
				controllers.CategoryObjectConflict,
				t.NewTable,
				"① 更换新表名；② 若目标就是覆盖，请先单独提交 DROP TABLE 工单（数据将丢失，需另行评估）",
				msg,
			)
		}
	}
}
