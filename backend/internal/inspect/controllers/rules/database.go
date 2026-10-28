package rules

import (
	"github.com/simajiong/jaysql/internal/inspect/controllers/logics"
	"github.com/simajiong/jaysql/internal/inspect/controllers/traverses"

	"github.com/pingcap/tidb/pkg/parser/ast"
)

func CreateDatabaseRules() []Rule {
	return []Rule{
		{
			Hint:      "CreateDatabase#检查DB是否存在",
			CheckFunc: (*Rule).RuleCreateDatabaseIsExist,
		},
		{
			Hint:      "CreateDatabase#检查库字符集",
			CheckFunc: (*Rule).RuleCheckDatabaseCharset,
		},
	}
}

// RuleCreateDatabaseIsExist
func (r *Rule) RuleCreateDatabaseIsExist(tistmt *ast.StmtNode) {
	v := &traverses.TraverseCreateDatabaseIsExist{}
	(*tistmt).Accept(v)
	logics.LogicCreateDatabaseIsExist(v, r.RuleHint)
}

// RuleCheckDatabaseCharset 检查 CREATE/ALTER DATABASE 的字符集是否符合规范
func (r *Rule) RuleCheckDatabaseCharset(tistmt *ast.StmtNode) {
	v := &traverses.TraverseDatabaseCharset{}
	(*tistmt).Accept(v)
	logics.LogicCheckDatabaseCharset(v, r.RuleHint)
}
