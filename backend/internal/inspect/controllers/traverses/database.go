package traverses

import (
	"github.com/pingcap/tidb/pkg/parser/ast"
)

// TraverseCreateViewIsExist
type TraverseCreateDatabaseIsExist struct {
	Name string // 库名
}

func (c *TraverseCreateDatabaseIsExist) Enter(in ast.Node) (ast.Node, bool) {
	if stmt, ok := in.(*ast.CreateDatabaseStmt); ok {
		c.Name = stmt.Name.String()
	}
	return in, false
}

func (c *TraverseCreateDatabaseIsExist) Leave(in ast.Node) (ast.Node, bool) {
	return in, true
}

// TraverseDatabaseCharset 提取 CREATE/ALTER DATABASE 语句中的库名、字符集与排序规则
type TraverseDatabaseCharset struct {
	Name    string // 库名
	Charset string // 字符集
	Collate string // 排序规则
}

func (c *TraverseDatabaseCharset) Enter(in ast.Node) (ast.Node, bool) {
	switch stmt := in.(type) {
	case *ast.CreateDatabaseStmt:
		c.Name = stmt.Name.String()
		c.parseOptions(stmt.Options)
	case *ast.AlterDatabaseStmt:
		if !stmt.AlterDefaultDatabase {
			c.Name = stmt.Name.String()
		}
		c.parseOptions(stmt.Options)
	}
	return in, false
}

func (c *TraverseDatabaseCharset) parseOptions(options []*ast.DatabaseOption) {
	for _, opt := range options {
		switch opt.Tp {
		case ast.DatabaseOptionCharset:
			c.Charset = opt.Value
		case ast.DatabaseOptionCollate:
			c.Collate = opt.Value
		}
	}
}

func (c *TraverseDatabaseCharset) Leave(in ast.Node) (ast.Node, bool) {
	return in, true
}
