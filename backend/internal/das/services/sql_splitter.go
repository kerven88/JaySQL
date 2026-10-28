package services

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	sqlStDefault = iota
	sqlStSingleQuote
	sqlStSingleQuoteEscape
	sqlStDoubleQuote
	sqlStBacktick
	sqlStDollarTagStart
	sqlStDollarTagBody
	sqlStDollarQuote
	sqlStLineDash
	sqlStLineDash2
	sqlStLinePound
	sqlStBlockEnter
	sqlStBlockComment
	sqlStBlockExit
)

func SplitSQLStatements(sql string) []string {
	if sql == "" {
		return nil
	}

	var (
		statements []string
		start      int
		state      = sqlStDefault
		dollarTag  string
		blockDepth int
		escapeNext bool
	)

	emit := func(end int, keepTrailingSemicolon bool) {
		chunk := sql[start:end]
		if !keepTrailingSemicolon {
			chunk = strings.TrimRightFunc(chunk, func(r rune) bool {
				return r == ';' || unicode.IsSpace(r)
			})
		}
		chunk = strings.TrimSpace(chunk)
		if chunk != "" {
			statements = append(statements, chunk)
		}
		start = end
	}

	i := 0
	for i < len(sql) {
		r, size := utf8.DecodeRuneInString(sql[i:])
		next := i + size

		switch state {
		case sqlStDefault:
			switch {
			case r == '\'':
				state = sqlStSingleQuote
				escapeNext = false
			case r == '"':
				state = sqlStDoubleQuote
			case r == '`':
				state = sqlStBacktick
			case r == '-':
				state = sqlStLineDash
			case r == '#':
				state = sqlStLinePound
			case r == '/':
				state = sqlStBlockEnter
			case r == '$':
				tag, afterTagIdx, ok := tryReadDollarQuote(sql, i)
				if ok {
					state = sqlStDollarQuote
					dollarTag = tag
					next = afterTagIdx
				}
			case r == 'E' || r == 'e':
				prevOk := i == 0
				if !prevOk {
					pr, _ := utf8.DecodeLastRuneInString(sql[:i])
					prevOk = !isSQLIdentifierPart(pr)
				}
				nr, nrSize := utf8.DecodeRuneInString(sql[next:])
				if prevOk && nr == '\'' && nrSize > 0 {
					state = sqlStSingleQuoteEscape
					escapeNext = false
					next += nrSize
				}
			case r == ';':
				emit(next, false)
				start = next
			}
		case sqlStSingleQuote:
			switch {
			case escapeNext:
				escapeNext = false
			case r == '\\':
				escapeNext = true
			case r == '\'':
				nr, nrSize := utf8.DecodeRuneInString(sql[next:])
				if nr == '\'' && nrSize > 0 {
					next += nrSize
				} else {
					state = sqlStDefault
				}
			}
		case sqlStSingleQuoteEscape:
			switch {
			case escapeNext:
				escapeNext = false
			case r == '\\':
				escapeNext = true
			case r == '\'':
				nr, nrSize := utf8.DecodeRuneInString(sql[next:])
				if nr == '\'' && nrSize > 0 {
					next += nrSize
				} else {
					state = sqlStDefault
				}
			}
		case sqlStDoubleQuote:
			if r == '"' {
				nr, nrSize := utf8.DecodeRuneInString(sql[next:])
				if nr == '"' && nrSize > 0 {
					next += nrSize
				} else {
					state = sqlStDefault
				}
			}
		case sqlStBacktick:
			if r == '`' {
				nr, nrSize := utf8.DecodeRuneInString(sql[next:])
				if nr == '`' && nrSize > 0 {
					next += nrSize
				} else {
					state = sqlStDefault
				}
			}
		case sqlStLineDash:
			if r == '-' {
				state = sqlStLineDash2
			} else {
				state = sqlStDefault
				continue
			}
		case sqlStLineDash2:
			if r == '\n' {
				state = sqlStDefault
			}
		case sqlStLinePound:
			if r == '\n' {
				state = sqlStDefault
			}
		case sqlStBlockEnter:
			if r == '*' {
				state = sqlStBlockComment
				blockDepth = 1
			} else {
				state = sqlStDefault
				continue
			}
		case sqlStBlockComment:
			if r == '/' {
				nr, nrSize := utf8.DecodeRuneInString(sql[next:])
				if nr == '*' && nrSize > 0 {
					blockDepth++
					next += nrSize
				}
			} else if r == '*' {
				nr, nrSize := utf8.DecodeRuneInString(sql[next:])
				if nr == '/' && nrSize > 0 {
					blockDepth--
					next += nrSize
					if blockDepth == 0 {
						state = sqlStDefault
					}
				}
			}
		case sqlStDollarQuote:
			if r == '$' {
				tag, afterTagIdx, ok := tryReadDollarQuote(sql, i)
				if ok && tag == dollarTag {
					state = sqlStDefault
					next = afterTagIdx
				}
			}
		}

		i = next
	}

	if start < len(sql) {
		emit(len(sql), false)
	}

	return statements
}

func isSQLIdentifierPart(r rune) bool {
	if r == utf8.RuneError {
		return false
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func tryReadDollarQuote(s string, at int) (tag string, after int, ok bool) {
	if at < 0 || at >= len(s) || s[at] != '$' {
		return "", 0, false
	}
	i := at + 1
	for i < len(s) {
		c := s[i]
		if c == '$' {
			tag = s[at+1 : i]
			if tag == "" {
				return "$$", i + 1, true
			}
			for j := 0; j < len(tag); j++ {
				b := tag[j]
				isLower := b >= 'a' && b <= 'z'
				isUpper := b >= 'A' && b <= 'Z'
				isDigit := b >= '0' && b <= '9'
				if !isLower && !isUpper && !isDigit && b != '_' {
					return "", 0, false
				}
			}
			return tag, i + 1, true
		}
		isLower := c >= 'a' && c <= 'z'
		isUpper := c >= 'A' && c <= 'Z'
		isDigit := c >= '0' && c <= '9'
		if !isLower && !isUpper && !isDigit && c != '_' {
			return "", 0, false
		}
		i++
	}
	return "", 0, false
}
