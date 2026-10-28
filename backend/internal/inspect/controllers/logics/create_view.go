package logics

import (
	"strings"

	"github.com/simajiong/jaysql/internal/inspect/controllers"
	"github.com/simajiong/jaysql/internal/inspect/controllers/dao"
	"github.com/simajiong/jaysql/internal/inspect/controllers/traverses"
)

// LogicCreateViewIsExist
//
// 视图存在性、依赖表存在性同样要查并输出，因此这里既不中断链路也不提前 return
// （历史实现在禁止创建视图时直接 return，把同一条语句上的存在性与依赖表问题一起吞掉了）。
//
// 「禁止创建视图」已上移到语句准入闸门（checker.statementAdmissionGate）：
// 那属于「这类语句能不能执行」，命中后整条规则链都不会跑，不必在这里再报一次 ——
// 原先这里的 `if !ENABLE_CREATE_VIEW { r.Warn("禁止创建视图") }` 在闸门接管后永远不可达。
func LogicCreateViewIsExist(v *traverses.TraverseCreateViewIsExist, r *controllers.RuleHint) {
	if !v.OrReplace {
		// `CREATE VIEW`（非 OR REPLACE）要求视图不存在，否则执行会失败。
		if msg, err := dao.CheckIfTableExists(v.View, r.DB); err == nil {
			r.WarnFatal(
				"obj_view_must_not_exist",
				controllers.CategoryObjectConflict,
				v.View,
				"① 更换视图名；② 需要重复执行请改用 `CREATE OR REPLACE VIEW`",
				strings.Join([]string{msg, "；建议在 TiDB 使用 `CREATE OR REPLACE VIEW` 更新视图"}, ""),
			)
		}
	}
	for _, table := range v.Tables {
		// 依赖表校验：引用的表/视图必须存在（排除当前要创建的视图名本身）。
		// 这里校验的是「表/视图」而不是「数据库」，跨库引用按跨库探测。
		if v.View != table {
			if msg, err := dao.CheckIfTableExistsCrossDB(table, r.DB); err != nil {
				r.WarnFatal(
					"obj_tbl_must_exist",
					controllers.CategoryObjectMissing,
					table,
					"① 核对引用的表/视图名拼写与大小写；② 确认引用的库是否正确；③ 若该对象应由本工单创建，请调整语句顺序；④ 若确认存在仍被报缺失，请先同步实例元数据再重新检查",
					msg,
				)
			}
		}
	}
}
