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
	"figure": true, "figcaption": true, "img": true,
}

var dropTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "template": true,
	"svg": true, "canvas": true,
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
		if dropTags[tag] {
			return
		}
		if !allowedTags[tag] {
			renderChildren(out, n)
			return
		}
		if tag == "img" {
			renderImage(out, n.Attr)
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

func renderImage(out *bytes.Buffer, attrs []html.Attribute) {
	src, alt, width, height := imageAttrs(attrs)
	if !safeMediaSrc(src) {
		return
	}
	out.WriteString(`<img src="`)
	out.WriteString(stdhtml.EscapeString(src))
	out.WriteByte('"')
	if alt != "" {
		out.WriteString(` alt="`)
		out.WriteString(stdhtml.EscapeString(alt))
		out.WriteByte('"')
	}
	if width != "" {
		out.WriteString(` width="` + width + `"`)
	}
	if height != "" {
		out.WriteString(` height="` + height + `"`)
	}
	out.WriteString(` loading="lazy" decoding="async">`)
}

func imageAttrs(attrs []html.Attribute) (src, alt, width, height string) {
	for _, attr := range attrs {
		switch strings.ToLower(attr.Key) {
		case "src":
			src = strings.TrimSpace(attr.Val)
		case "alt":
			alt = strings.TrimSpace(attr.Val)
		case "width":
			if safeDimension(attr.Val) {
				width = strings.TrimSpace(attr.Val)
			}
		case "height":
			if safeDimension(attr.Val) {
				height = strings.TrimSpace(attr.Val)
			}
		}
	}
	return
}

func safeMediaSrc(value string) bool {
	const prefix = "/api/media/"
	id, ok := strings.CutPrefix(value, prefix)
	if !ok || id == "" {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func safeDimension(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 5 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != "0"
}

func safeHref(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	return strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "mailto:")
}
