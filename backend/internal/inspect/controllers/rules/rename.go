package rules

import (
	"github.com/simajiong/jaysql/internal/inspect/controllers/logics"
	"github.com/simajiong/jaysql/internal/inspect/controllers/traverses"

	"github.com/pingcap/tidb/pkg/parser/ast"
)

func RenameTableRules() []Rule {
	return []Rule{
		{
			Hint:      "RenameTable#检查",
			CheckFunc: (*Rule).RuleRenameTable,
		},
	}
}

// RuleRenameTable
func (r *Rule) RuleRenameTable(tistmt *ast.StmtNode) {
	v := &traverses.TraverseRenameTable{}
	(*tistmt).Accept(v)
	logics.LogicRenameTable(v, r.RuleHint)
}
