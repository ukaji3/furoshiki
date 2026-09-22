package page

import (
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// attr returns the value of the attribute key (no namespace) of n.
func attr(n *html.Node, key string) (string, bool) {
	if a := attrPtr(n, "", key); a != nil {
		return a.Val, true
	}
	return "", false
}

// attrValue returns the value of the attribute key of n, or "".
func attrValue(n *html.Node, key string) string {
	v, _ := attr(n, key)
	return v
}

// attrPtr returns a pointer to the attribute of n with the given namespace and
// key, valid until n.Attr is modified.
func attrPtr(n *html.Node, namespace, key string) *html.Attribute {
	if n == nil {
		return nil
	}
	for i := range n.Attr {
		if n.Attr[i].Namespace == namespace && n.Attr[i].Key == key {
			return &n.Attr[i]
		}
	}
	return nil
}

// setAttr sets the attribute key (no namespace) of n, appending it if absent.
func setAttr(n *html.Node, key, val string) {
	if a := attrPtr(n, "", key); a != nil {
		a.Val = val
		return
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

// removeAttr deletes every attribute named key (no namespace) from n.
func removeAttr(n *html.Node, key string) {
	out := n.Attr[:0]
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			continue
		}
		out = append(out, a)
	}
	n.Attr = out
}

// svgHref returns the href attribute of an SVG element, preferring plain href
// over the deprecated xlink:href.
func svgHref(n *html.Node) *html.Attribute {
	if a := attrPtr(n, "", "href"); a != nil {
		return a
	}
	return attrPtr(n, "xlink", "href")
}

// textContent concatenates the text nodes directly below n.
func textContent(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	}
	return b.String()
}

// Route returns the hash route under which the bundle's runtime shows the page
// rel, followed by the query and fragment when they are non-empty. The path
// segments are percent-escaped; the runtime decodes them with
// decodeURIComponent.
func Route(rel, query, fragment string) string {
	segs := strings.Split(rel, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	var b strings.Builder
	b.WriteString("#/")
	b.WriteString(strings.Join(segs, "/"))
	if query != "" {
		b.WriteByte('?')
		b.WriteString(query)
	}
	if fragment != "" {
		b.WriteByte('#')
		b.WriteString(fragment)
	}
	return b.String()
}

// srcsetCandidate is one entry of a srcset attribute.
type srcsetCandidate struct {
	url  string
	desc string // width or density descriptor, possibly empty
}

func isASCIISpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

// parseSrcset splits a srcset attribute into candidates following the HTML
// specification's algorithm: a URL runs until whitespace, a URL ending in
// commas has no descriptors, and descriptors run until a comma outside
// parentheses.
func parseSrcset(s string) []srcsetCandidate {
	var out []srcsetCandidate
	i, n := 0, len(s)
	for i < n {
		for i < n && (isASCIISpace(s[i]) || s[i] == ',') {
			i++
		}
		if i >= n {
			break
		}
		start := i
		for i < n && !isASCIISpace(s[i]) {
			i++
		}
		u := s[start:i]
		if strings.HasSuffix(u, ",") {
			out = append(out, srcsetCandidate{url: strings.TrimRight(u, ",")})
			continue
		}
		dstart, depth := i, 0
	descriptors:
		for i < n {
			switch s[i] {
			case '(':
				depth++
			case ')':
				depth--
			case ',':
				if depth <= 0 {
					break descriptors
				}
			}
			i++
		}
		desc := strings.TrimSpace(s[dstart:i])
		if i < n {
			i++ // the separating comma
		}
		out = append(out, srcsetCandidate{url: u, desc: desc})
	}
	return out
}

// formatSrcset serialises candidates back to attribute syntax.
func formatSrcset(cands []srcsetCandidate) string {
	parts := make([]string, 0, len(cands))
	for _, c := range cands {
		if c.desc == "" {
			parts = append(parts, c.url)
		} else {
			parts = append(parts, c.url+" "+c.desc)
		}
	}
	return strings.Join(parts, ", ")
}

// javaScriptMIMETypes lists the JavaScript MIME type essences recognised by
// the HTML specification.
var javaScriptMIMETypes = map[string]bool{
	"application/ecmascript":   true,
	"application/javascript":   true,
	"application/x-ecmascript": true,
	"application/x-javascript": true,
	"text/ecmascript":          true,
	"text/javascript":          true,
	"text/javascript1.0":       true,
	"text/javascript1.1":       true,
	"text/javascript1.2":       true,
	"text/javascript1.3":       true,
	"text/javascript1.4":       true,
	"text/javascript1.5":       true,
	"text/jscript":             true,
	"text/livescript":          true,
	"text/x-ecmascript":        true,
	"text/x-javascript":        true,
}

// isJavaScriptType reports whether a <script type> value denotes a classic or
// module script that the browser would fetch and run.
func isJavaScriptType(t string) bool {
	t = strings.ToLower(strings.TrimSpace(t))
	if t == "" || t == "module" {
		return true
	}
	if i := strings.IndexByte(t, ';'); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	return javaScriptMIMETypes[t]
}

var moduleImportRE = regexp.MustCompile(`(?m)(?:^|[;{}])\s*import\s*(?:[\w$]|\{|\*|"|')|(?:^|[;{}])\s*export\s+[^;]*?\sfrom\s*["']|\bimport\s*\(`)

// hasModuleImports heuristically reports whether JavaScript source contains
// import declarations, re-exports or dynamic import() calls. It is used only
// to warn: such specifiers cannot be resolved inside the bundle.
func hasModuleImports(src string) bool {
	return moduleImportRE.MatchString(src)
}
