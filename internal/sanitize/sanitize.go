package sanitize

import (
	"bytes"
	stdhtml "html"
	"strings"

	"golang.org/x/net/html"
)

var allowedTags = map[string]bool{
	"article": true, "section": true, "p": true, "br": true, "strong": true, "em": true,
	"ul": true, "ol": true, "li": true, "blockquote": true, "pre": true, "code": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "a": true,
}

func HTML(input string) string {
	root, err := html.Parse(strings.NewReader(input))
	if err != nil {
		return html.EscapeString(input)
	}
	var out bytes.Buffer
	renderChildren(&out, root)
	return out.String()
}

func renderChildren(out *bytes.Buffer, n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		renderNode(out, c)
	}
}

func renderNode(out *bytes.Buffer, n *html.Node) {
	switch n.Type {
	case html.TextNode:
		out.WriteString(stdhtml.EscapeString(n.Data))
	case html.ElementNode:
		tag := strings.ToLower(n.Data)
		if !allowedTags[tag] {
			renderChildren(out, n)
			return
		}
		out.WriteByte('<')
		out.WriteString(tag)
		if tag == "a" {
			for _, attr := range n.Attr {
				if strings.ToLower(attr.Key) == "href" && safeHref(attr.Val) {
					out.WriteString(` href="`)
					out.WriteString(stdhtml.EscapeString(attr.Val))
					out.WriteString(`" rel="nofollow noopener noreferrer" target="_blank"`)
					break
				}
			}
		}
		out.WriteByte('>')
		renderChildren(out, n)
		out.WriteString("</")
		out.WriteString(tag)
		out.WriteByte('>')
	default:
		renderChildren(out, n)
	}
}

func safeHref(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	return strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "mailto:")
}
