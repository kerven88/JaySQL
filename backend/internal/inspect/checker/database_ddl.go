package checker

import (
	"strings"

	"github.com/simajiong/jaysql/internal/inspect/controllers"
	"github.com/simajiong/jaysql/internal/inspect/controllers/parser"
	"github.com/simajiong/jaysql/internal/inspect/controllers/rules"
	"github.com/simajiong/jaysql/internal/inspect/services"
	"github.com/simajiong/jaysql/pkg/query"

	"github.com/pingcap/tidb/pkg/parser/ast"
	_ "github.com/pingcap/tidb/pkg/types/parser_driver"
)

// InspectDatabaseLevelDDL 对库层级 DDL（CREATE DATABASE / ALTER DATABASE）做静态审核。
//
// 为什么需要单独一个入口：
// 工单目标是「库层级」时（detectOrderTargetMode 返回 orderTargetNone，典型场景就是 CREATE DATABASE），
// 常规审核链路 SyntaxInspectService.Run() 需要连到目标实例取 DB 变量，而库层级 SQL 在提交阶段
// 根本没有目标库可选，因此 PreInspectOrderService / InspectOrderSyntaxService 都会整体短路，
// 审核引擎一次都不会执行 —— 库级规则（如「限制库的字符集」）随之变成死代码，永远不触发。
//
// 而库级字符集规则是纯语法判定：只依赖 TiDB parser 从 DatabaseOption 里抽出的字符集，
// 完全不需要连接目标实例。因此这里单独跑一遍「不依赖数据库连接」的库级规则，
// 保证 CREATE DATABASE 与 ALTER DATABASE 都能被引擎拦截。
//
// 注意：这里刻意不跑 RuleCreateDatabaseIsExist —— 该规则要查 information_schema，
// 传 nil 的 dao.DB 会直接 panic。存在性判定留给常规链路（联机场景）处理。
func InspectDatabaseLevelDDL(sqlText string, instanceID string) ([]ReturnData, error) {
	params, err := services.BuildInspectParamsForInstance(instanceID)
	if err != nil {
		return nil, err
	}
	audit, _, err := parser.NewParse(sqlText, "", "")
	if err != nil {
		return nil, err
	}

	svc := &SyntaxInspectService{SqlText: sqlText}
	var out []ReturnData
	for _, stmt := range audit.TiStmt {
		// 只处理库层级语句，其余语句维持原有行为（不在本入口审核）
		switch stmt.(type) {
		case *ast.CreateDatabaseStmt, *ast.AlterDatabaseStmt:
		default:
			continue
		}

		sqlTrim := strings.TrimSuffix(stmt.Text(), ";")
		data := ReturnData{
			FingerId: query.Id(query.Fingerprint(sqlTrim)),
			Query:    stmt.Text(),
			Type:     databaseDDLTypeName(stmt),
			Line:     svc.computeLine(stmt.Text()),
		}

		ruleHint := &controllers.RuleHint{
			Query:         stmt.Text(),
			InspectParams: &params,
		}
		rule := rules.Rule{
			RuleHint:  ruleHint,
			Hint:      "DatabaseDDL#检查库字符集",
			CheckFunc: (*rules.Rule).RuleCheckDatabaseCharset,
		}
		var node ast.StmtNode = stmt
		rule.CheckFunc(&rule, &node)
		data.Summary = append(data.Summary, ruleHint.Summary...)

		// 无任何规则命中/提示时注入默认通过信息，与常规链路口径保持一致
		if len(data.Summary) == 0 {
			data.Summary = append(data.Summary, controllers.SummaryItem{Level: LevelInfo, Message: "语句检查通过"})
		}
		out = append(out, data)
	}
	return out, nil
}

// databaseDDLTypeName 返回库层级语句的类型名，与常规链路的 Type 字段保持一致
func databaseDDLTypeName(stmt ast.StmtNode) string {
	switch stmt.(type) {
	case *ast.CreateDatabaseStmt:
		return "CreateDatabase"
	case *ast.AlterDatabaseStmt:
		return "AlterDatabase"
	default:
		return ""
	}
}
