package notifier

import (
	"html"
	"net/url"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// RenderMessageMarkdown converts CommonMark to Telegram's restricted HTML and plain text.
// Raw HTML is treated as text; only tags generated here and safe links are emitted.
func RenderMessageMarkdown(markdown string) (string, string) {
	rich, plain, _ := renderMessageMarkdown(markdown)
	return rich, plain
}
func renderMessageMarkdown(markdown string) (string, string, string) {
	source := []byte(markdown)
	root := goldmark.New().Parser().Parse(text.NewReader(source))
	var rich, plain, visible strings.Builder
	emit := func(s string) {
		rich.WriteString(html.EscapeString(s))
		plain.WriteString(s)
		visible.WriteString(s)
	}
	tag := func(s string) { rich.WriteString(s) }
	decode := func(s []byte) string {
		return string(util.ResolveEntityNames(util.ResolveNumericReferences(util.UnescapePunctuations(s))))
	}
	safeURL := func(s []byte) string {
		value := decode(s)
		u, err := url.Parse(value)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "mailto") {
			return ""
		}
		return value
	}
	var walk func(ast.Node)
	var children func(ast.Node)
	children = func(n ast.Node) {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			walk(c)
		}
	}
	block := func() { emit("\n\n") }
	walk = func(n ast.Node) {
		switch v := n.(type) {
		case *ast.Text:
			value := string(v.Segment.Value(source))
			if !v.IsRaw() {
				value = decode(v.Segment.Value(source))
			}
			emit(value)
			if v.SoftLineBreak() || v.HardLineBreak() {
				emit("\n")
			}
		case *ast.String:
			emit(string(v.Value))
		case *ast.Heading:
			tag("<b>")
			children(n)
			tag("</b>")
			block()
		case *ast.Paragraph:
			children(n)
			block()
		case *ast.Emphasis:
			name := "i"
			if v.Level == 2 {
				name = "b"
			}
			tag("<" + name + ">")
			children(n)
			tag("</" + name + ">")
		case *ast.CodeSpan:
			// Telegram forbids nesting code entities inside emphasis/headings/links.
			// Retain the outer format and literal code text in these combinations.
			nested := false
			for parent := n.Parent(); parent != nil; parent = parent.Parent() {
				switch parent.(type) {
				case *ast.Emphasis, *ast.Heading, *ast.Link:
					nested = true
				}
			}
			if !nested {
				tag("<code>")
			}
			children(n)
			if !nested {
				tag("</code>")
			}
		case *ast.FencedCodeBlock:
			tag("<pre>")
			for i := 0; i < v.Lines().Len(); i++ {
				line := v.Lines().At(i)
				emit(string(line.Value(source)))
			}
			tag("</pre>")
			block()
		case *ast.CodeBlock:
			tag("<pre>")
			for i := 0; i < v.Lines().Len(); i++ {
				line := v.Lines().At(i)
				emit(string(line.Value(source)))
			}
			tag("</pre>")
			block()
		case *ast.Link:
			href := safeURL(v.Destination)
			if href != "" {
				tag(`<a href="` + html.EscapeString(href) + `">`)
			}
			children(n)
			if href != "" {
				tag("</a>")
				plain.WriteString(" (" + href + ")")
			}
		case *ast.AutoLink:
			href := safeURL(v.URL(source))
			if href != "" {
				tag(`<a href="` + html.EscapeString(href) + `">`)
			}
			emit(string(v.Label(source)))
			if href != "" {
				tag("</a>")
			}
		case *ast.Image:
			children(n)
		case *ast.ListItem:
			emit("- ")
			children(n)
			emit("\n")
		case *ast.List:
			children(n)
			emit("\n")
		case *ast.Blockquote:
			children(n)
			emit("\n")
		case *ast.ThematicBreak:
			emit("────────\n")
		case *ast.RawHTML:
			for i := 0; i < v.Segments.Len(); i++ {
				segment := v.Segments.At(i)
				emit(string(segment.Value(source)))
			}
		case *ast.HTMLBlock:
			for i := 0; i < v.Lines().Len(); i++ {
				line := v.Lines().At(i)
				emit(string(line.Value(source)))
			}
			if v.HasClosure() {
				emit(string(v.ClosureLine.Value(source)))
			}
			block()
		default:
			children(n)
		}
	}
	walk(root)
	return strings.TrimSpace(rich.String()), strings.TrimSpace(plain.String()), strings.TrimSpace(visible.String())
}
