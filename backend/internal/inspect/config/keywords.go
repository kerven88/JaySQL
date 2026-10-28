package config

import (
	"sort"
	"strings"
	"sync"
)

// identifierKeywords 标识符关键字规则的默认全集：MySQL 与 PostgreSQL 关键字的并集。
var (
	identifierKeywordsOnce sync.Once
	identifierKeywordsAll  []string
)

// DefaultIdentifierKeywords 返回「字段名/表名不能是关键字」规则的默认关键字全集：
// MySQL 与 PostgreSQL 关键字并集，升序去重。规范中该规则未配置 keywords 参数时使用。
func DefaultIdentifierKeywords() []string {
	identifierKeywordsOnce.Do(func() {
		seen := make(map[string]struct{}, len(DefaultMySQLKeywords)+len(DefaultPostgresKeywords))
		merged := make([]string, 0, len(seen))
		for _, kw := range DefaultMySQLKeywords {
			w := strings.ToUpper(strings.TrimSpace(kw))
			if w == "" {
				continue
			}
			if _, ok := seen[w]; ok {
				continue
			}
			seen[w] = struct{}{}
			merged = append(merged, w)
		}
		for _, kw := range DefaultPostgresKeywords {
			w := strings.ToUpper(strings.TrimSpace(kw))
			if w == "" {
				continue
			}
			if _, ok := seen[w]; ok {
				continue
			}
			seen[w] = struct{}{}
			merged = append(merged, w)
		}
		sort.Strings(merged)
		identifierKeywordsAll = merged
	})
	return identifierKeywordsAll
}

// IdentifierKeywordSet 返回标识符关键字检查使用的集合（大写）：
// 规范配置了 keywords 参数（非空）时使用配置值，否则回退默认全集。
func (p *InspectParams) IdentifierKeywordSet() map[string]struct{} {
	keywords := p.IDENTIFIER_KEYWORDS
	if len(keywords) == 0 {
		keywords = DefaultIdentifierKeywords()
	}
	set := make(map[string]struct{}, len(keywords))
	for _, kw := range keywords {
		w := strings.ToUpper(strings.TrimSpace(kw))
		if w == "" {
			continue
		}
		set[w] = struct{}{}
	}
	return set
}
