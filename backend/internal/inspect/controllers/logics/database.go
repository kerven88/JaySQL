package logics

import (
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/internal/inspect/controllers"
	"github.com/simajiong/jaysql/internal/inspect/controllers/dao"
	"github.com/simajiong/jaysql/internal/inspect/controllers/traverses"
	"github.com/simajiong/jaysql/pkg/utils"
)

// LogicCreateDatabaseIsExist
//
// 库已存在是独立结论：只提示，不中断后续规则（字符集等检查照常执行），
// 否则用户改完库名才看到下一批问题。
func LogicCreateDatabaseIsExist(v *traverses.TraverseCreateDatabaseIsExist, r *controllers.RuleHint) {
	if msg, err := dao.CheckIfDatabaseExists(v.Name, r.DB); err == nil {
		r.Warn("数据库已存在：" + msg)
	}
}

// databaseCharsetRuleKey 「限制库的字符集」规则在规范目录中的 key 后缀。
// 摘要携带该后缀后，resolveInspectSeverity 可精确命中规范里配置的级别（must/suggest），
// 不再退化到「文案模糊匹配 + 规范默认级别兜底」，规则被设为「必须改进」时才能真正拦截提交。
const databaseCharsetRuleKey = "limit_database_charset"

// LogicCheckDatabaseCharset 检查 CREATE/ALTER DATABASE 指定的字符集是否在规范允许列表内
func LogicCheckDatabaseCharset(v *traverses.TraverseDatabaseCharset, r *controllers.RuleHint) {
	if !r.InspectParams.CHECK_DATABASE_CHARSET {
		return
	}

	var supportCharsets []string
	for _, item := range r.InspectParams.DATABASE_SUPPORT_CHARSET {
		supportCharsets = append(supportCharsets, item["charset"])
	}
	if len(supportCharsets) == 0 {
		return
	}

	charset := strings.ToLower(strings.TrimSpace(v.Charset))
	charsetList := fmt.Sprintf("%s", supportCharsets)
	if charset == "" {
		r.WarnWithRule(databaseCharsetRuleKey, "数据库`"+v.Name+"`必须指定字符集，可选字符集为"+charsetList+"【例如:DEFAULT CHARACTER SET=utf8mb4】")
		return
	}
	if !utils.IsContain(supportCharsets, charset) {
		r.WarnWithRule(databaseCharsetRuleKey, "数据库`"+v.Name+"`指定的字符集`"+charset+"`不符合要求，可选字符集为"+charsetList)
	}
}
