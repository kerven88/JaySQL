package services

import (
	"fmt"
	"strings"
	"unicode"
)

var blockedLeadingSQLKeywords = map[string]struct{}{
	"alter":    {},
	"analyze":  {},
	"call":     {},
	"comment":  {},
	"commit":   {},
	"copy":     {},
	"create":   {},
	"delete":   {},
	"do":       {},
	"drop":     {},
	"execute":  {},
	"grant":    {},
	"insert":   {},
	"merge":    {},
	"refresh":  {},
	"reindex":  {},
	"replace":  {},
	"revoke":   {},
	"rollback": {},
	"set":      {},
	"truncate": {},
	"update":   {},
	"vacuum":   {},
}

func validateSingleSelectOnly(sqlText string) (string, error) {
	trimmed := strings.TrimSpace(sqlText)
	if trimmed == "" {
		return "", fmt.Errorf("SQL不能为空")
	}
	statements := SplitSQLStatements(sqlText)
	if len(statements) == 0 {
		return "", fmt.Errorf("SQL不能为空")
	}
	if len(statements) > 1 {
		return "", fmt.Errorf("每次仅允许执行一条语句")
	}
	normalized := strings.TrimSpace(statements[0])
	inspectTarget := strings.ToLower(stripLeadingSQLComments(normalized))
	if inspectTarget == "" {
		return "", fmt.Errorf("SQL不能为空")
	}
	if strings.HasPrefix(inspectTarget, "select") || strings.HasPrefix(inspectTarget, "with") {
		return normalized, nil
	}
	if _, blocked := blockedLeadingSQLKeywords[firstSQLKeyword(inspectTarget)]; blocked {
		return "", fmt.Errorf("当前阶段仅允许执行SELECT语句")
	}
	return normalized, nil
}

func validateSingleExplainQueryOnly(sqlText string) (string, error) {
	normalized, err := validateSingleSelectOnly(sqlText)
	if err != nil {
		if err.Error() == "当前阶段仅允许执行SELECT语句" {
			return "", fmt.Errorf("解释功能当前仅支持单条 SELECT / WITH 查询")
		}
		return "", err
	}
	return normalized, nil
}

func stripLeadingSQLComments(sqlText string) string {
	remaining := strings.TrimSpace(sqlText)
	for {
		switch {
		case strings.HasPrefix(remaining, "--"):
			lineEnd := strings.IndexByte(remaining, '\n')
			if lineEnd < 0 {
				return ""
			}
			remaining = strings.TrimSpace(remaining[lineEnd+1:])
		case strings.HasPrefix(remaining, "/*"):
			commentEnd := strings.Index(remaining, "*/")
			if commentEnd < 0 {
				return ""
			}
			remaining = strings.TrimSpace(remaining[commentEnd+2:])
		default:
			return remaining
		}
	}
}

func firstSQLKeyword(sqlText string) string {
	var builder strings.Builder
	for _, r := range sqlText {
		if builder.Len() == 0 {
			if !unicode.IsLetter(r) {
				return ""
			}
		} else if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_') {
			break
		}
		builder.WriteRune(unicode.ToLower(r))
	}
	return builder.String()
}
