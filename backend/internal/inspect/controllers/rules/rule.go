package rules

import (
	"github.com/simajiong/jaysql/internal/inspect/controllers"

	"github.com/pingcap/tidb/pkg/parser/ast"
)

type Rule struct {
	*controllers.RuleHint
	Hint      string                     `json:"hint"` // 规则说明
	CheckFunc func(*Rule, *ast.StmtNode) // 函数名
}
