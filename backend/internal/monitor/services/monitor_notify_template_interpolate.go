package services

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

func longestNotifyRun(value string, char byte) int {
	longest, current := 0, 0
	for i := 0; i < len(value); i++ {
		if value[i] == char {
			current++
			if current > longest {
				longest = current
			}
		} else {
			current = 0
		}
	}
	return longest
}

// Parse before interpolation, then serialize a normalized CommonMark document.
// AST context distinguishes code from ordinary text, including code inside lists
// and quotations. Data cannot create Markdown structure or close code delimiters.
func interpolateNotifyTemplate(template string, values map[string]string) string {
	// Mask placeholders with punctuation-free tokens before parsing: CommonMark may
	// otherwise split {{instance_name}} across multiple text nodes.
	prefix := "JAYSQLTEMPLATEVAR"
	for {
		collision := strings.Contains(template, prefix)
		for _, value := range values {
			collision = collision || strings.Contains(value, prefix)
		}
		if !collision {
			break
		}
		prefix += "X"
	}
	tokens := map[string]string{}
	masked := notifyVariablePattern.ReplaceAllStringFunc(template, func(match string) string {
		token := fmt.Sprintf("%s%dEND", prefix, len(tokens))
		key := notifyVariablePattern.FindStringSubmatch(match)[1]
		tokens[token] = values[key]
		return token
	})
	tokenPattern := regexp.MustCompile(regexp.QuoteMeta(prefix) + `[0-9]+END`)
	substitute := func(value string, escape bool) string {
		return tokenPattern.ReplaceAllStringFunc(value, func(token string) string {
			value := tokens[token]
			if escape {
				return escapeNotifyMarkdown(value)
			}
			return value
		})
	}
	source := []byte(masked)
	root := goldmark.New().Parser().Parse(text.NewReader(source))
	replace := func(s string) string { return substitute(s, true) }
	codeBlock := func(body, language string) string {
		body = substitute(body, false)
		language = strings.ReplaceAll(strings.ReplaceAll(substitute(language, false), "\r", " "), "\n", " ")
		fence := strings.Repeat("~", max(3, longestNotifyRun(body, '~')+1))
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		return fence + language + "\n" + body + fence + "\n\n"
	}
	var render func(ast.Node) string
	children := func(n ast.Node) string {
		var b strings.Builder
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			b.WriteString(render(child))
		}
		return b.String()
	}
	render = func(n ast.Node) string {
		switch v := n.(type) {
		case *ast.Text:
			value := replace(string(v.Segment.Value(source)))
			if v.HardLineBreak() {
				value += "  \n"
			} else if v.SoftLineBreak() {
				value += "\n"
			}
			return value
		case *ast.String:
			return replace(string(v.Value))
		case *ast.Paragraph:
			return children(n) + "\n\n"
		case *ast.Heading:
			return strings.Repeat("#", v.Level) + " " + children(n) + "\n\n"
		case *ast.Emphasis:
			delimiter := strings.Repeat("*", v.Level)
			return delimiter + children(n) + delimiter
		case *ast.CodeSpan:
			body := string(v.Text(source))
			body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", " "), "\n", " ")
			body = substitute(body, false)
			body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", " "), "\n", " ")
			if body == "" {
				return ""
			}
			delimiter := strings.Repeat("`", longestNotifyRun(body, '`')+1)
			if strings.TrimSpace(body) != "" {
				body = " " + body + " "
			}
			return delimiter + body + delimiter
		case *ast.FencedCodeBlock:
			return codeBlock(string(v.Lines().Value(source)), string(v.Language(source)))
		case *ast.CodeBlock:
			return codeBlock(string(v.Lines().Value(source)), "")
		case *ast.Link:
			destination := replace(string(v.Destination))
			return "[" + children(n) + "](" + destination + ")"
		case *ast.Image:
			return "![" + children(n) + "](" + replace(string(v.Destination)) + ")"
		case *ast.AutoLink:
			return "<" + replace(string(v.URL(source))) + ">"
		case *ast.List:
			var b strings.Builder
			index := v.Start
			for item := n.FirstChild(); item != nil; item = item.NextSibling() {
				prefix := "- "
				if v.IsOrdered() {
					prefix = fmt.Sprintf("%d. ", index)
					index++
				}
				body := strings.TrimRight(children(item), "\n")
				b.WriteString(prefix + strings.ReplaceAll(body, "\n", "\n"+strings.Repeat(" ", len(prefix))) + "\n")
			}
			return b.String() + "\n"
		case *ast.Blockquote:
			body := strings.TrimRight(children(n), "\n")
			return "> " + strings.ReplaceAll(body, "\n", "\n> ") + "\n\n"
		case *ast.ThematicBreak:
			return "---\n\n"
		case *ast.RawHTML:
			var b strings.Builder
			for i := 0; i < v.Segments.Len(); i++ {
				segment := v.Segments.At(i)
				b.Write(segment.Value(source))
			}
			return replace(b.String())
		case *ast.HTMLBlock:
			value := string(v.Lines().Value(source))
			if v.HasClosure() {
				value += string(v.ClosureLine.Value(source))
			}
			return replace(value) + "\n\n"
		default:
			return children(n)
		}
	}
	return strings.TrimSpace(render(root))
}
