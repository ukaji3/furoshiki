package resolve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newSite creates a site directory with a fixed layout and returns a Resolver
// for it.
func newSite(t *testing.T, excludes ...string) (*Resolver, string) {
	t.Helper()
	root := t.TempDir()
	files := []string{
		"index.html",
		"about.html",
		"css/style.css",
		"img/logo.png",
		"img/a b.png",
		"docs/index.html",
		"docs/guide.html",
		"drafts/secret.html",
		"notes.txt",
		"noext",
		"Upper.HTM",
	}
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := New(root, excludes)
	if err != nil {
		t.Fatal(err)
	}
	return r, root
}

func TestNewErrors(t *testing.T) {
	if _, err := New(filepath.Join(t.TempDir(), "nope"), nil); err == nil {
		t.Error("New on a missing directory should fail")
	}
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(f, nil); err == nil {
		t.Error("New on a file should fail")
	}
	if _, err := New(t.TempDir(), []string{"["}); err == nil {
		t.Error("New with a malformed pattern should fail")
	}
}

func TestResolve(t *testing.T) {
	r, root := newSite(t, "drafts", "*.txt")

	tests := []struct {
		name     string
		raw      string
		base     string
		kind     Kind
		rel      string
		query    string
		fragment string
		isHTML   bool
	}{
		{name: "empty", raw: "", base: "index.html", kind: Empty},
		{name: "whitespace only", raw: "  \t", base: "index.html", kind: Empty},
		{name: "fragment", raw: "#top", base: "index.html", kind: Fragment, fragment: "top"},
		{name: "empty fragment", raw: "#", base: "index.html", kind: Fragment, fragment: ""},
		{name: "http", raw: "https://example.com/a.html", base: "index.html", kind: External},
		{name: "protocol-relative", raw: "//cdn.example.com/x.js", base: "index.html", kind: External},
		{name: "mailto", raw: "mailto:a@example.com", base: "index.html", kind: External},
		{name: "data", raw: "data:image/png;base64,AAAA", base: "index.html", kind: External},
		{name: "javascript", raw: "javascript:void(0)", base: "index.html", kind: External},
		{name: "unknown scheme", raw: "foo:bar.html", base: "index.html", kind: External},
		{name: "invalid percent", raw: "100%.html", base: "index.html", kind: Invalid},
		{name: "sibling", raw: "about.html", base: "index.html", kind: File, rel: "about.html", isHTML: true},
		{name: "subdir", raw: "docs/guide.html", base: "index.html", kind: File, rel: "docs/guide.html", isHTML: true},
		{name: "dot slash", raw: "./about.html", base: "index.html", kind: File, rel: "about.html", isHTML: true},
		{name: "parent from subdir", raw: "../about.html", base: "docs/guide.html", kind: File, rel: "about.html", isHTML: true},
		{name: "relative resource from subdir", raw: "../css/style.css", base: "docs/guide.html", kind: File, rel: "css/style.css"},
		{name: "root-relative", raw: "/css/style.css", base: "docs/guide.html", kind: File, rel: "css/style.css"},
		{name: "root-relative page", raw: "/about.html", base: "docs/guide.html", kind: File, rel: "about.html", isHTML: true},
		{name: "escape above root", raw: "../../etc/passwd", base: "docs/guide.html", kind: Outside, rel: "../etc/passwd"},
		{name: "escape root-relative", raw: "/../x.html", base: "index.html", kind: Outside, rel: "../x.html"},
		{name: "directory with slash", raw: "docs/", base: "index.html", kind: File, rel: "docs/index.html", isHTML: true},
		{name: "directory without slash", raw: "docs", base: "index.html", kind: File, rel: "docs/index.html", isHTML: true},
		{name: "parent directory", raw: "../", base: "docs/guide.html", kind: File, rel: "index.html", isHTML: true},
		{name: "dot dot", raw: "..", base: "docs/guide.html", kind: File, rel: "index.html", isHTML: true},
		{name: "current directory", raw: "./", base: "docs/guide.html", kind: File, rel: "docs/index.html", isHTML: true},
		{name: "root", raw: "/", base: "docs/guide.html", kind: File, rel: "index.html", isHTML: true},
		{name: "directory without index", raw: "empty/", base: "index.html", kind: Missing, rel: "empty/index.html", isHTML: true},
		{name: "missing", raw: "nope.png", base: "index.html", kind: Missing, rel: "nope.png"},
		{name: "query and fragment", raw: "about.html?x=1&y=2#sec", base: "index.html", kind: File, rel: "about.html", query: "x=1&y=2", fragment: "sec", isHTML: true},
		{name: "query only", raw: "?page=2", base: "about.html", kind: File, rel: "about.html", query: "page=2", isHTML: true},
		{name: "cache buster on resource", raw: "css/style.css?v=3", base: "index.html", kind: File, rel: "css/style.css", query: "v=3"},
		{name: "percent-encoded space", raw: "img/a%20b.png", base: "index.html", kind: File, rel: "img/a b.png"},
		{name: "literal space", raw: "img/a b.png", base: "index.html", kind: File, rel: "img/a b.png"},
		{name: "backslash", raw: "img\\logo.png", base: "index.html", kind: File, rel: "img/logo.png"},
		{name: "surrounding whitespace", raw: "  about.html\n", base: "index.html", kind: File, rel: "about.html", isHTML: true},
		{name: "embedded tab", raw: "abo\tut.html", base: "index.html", kind: File, rel: "about.html", isHTML: true},
		{name: "excluded directory", raw: "drafts/secret.html", base: "index.html", kind: Excluded, rel: "drafts/secret.html", isHTML: true},
		{name: "excluded glob", raw: "notes.txt", base: "index.html", kind: Excluded, rel: "notes.txt"},
		{name: "upper-case extension", raw: "Upper.HTM", base: "index.html", kind: File, rel: "Upper.HTM", isHTML: true},
		{name: "no extension", raw: "noext", base: "index.html", kind: File, rel: "noext"},
		{name: "base is directory", raw: "guide.html", base: "docs/", kind: File, rel: "docs/guide.html", isHTML: true},
		{name: "base is root", raw: "about.html", base: "", kind: File, rel: "about.html", isHTML: true},
		{name: "fragment with escaped chars", raw: "about.html#a%20b", base: "index.html", kind: File, rel: "about.html", fragment: "a%20b", isHTML: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := r.Resolve(tc.raw, tc.base)
			if got.Kind != tc.kind {
				t.Fatalf("Kind = %v, want %v (ref=%+v)", got.Kind, tc.kind, got)
			}
			if got.Raw != tc.raw {
				t.Errorf("Raw = %q, want %q", got.Raw, tc.raw)
			}
			if got.Rel != tc.rel {
				t.Errorf("Rel = %q, want %q", got.Rel, tc.rel)
			}
			if got.Query != tc.query {
				t.Errorf("Query = %q, want %q", got.Query, tc.query)
			}
			if got.Fragment != tc.fragment {
				t.Errorf("Fragment = %q, want %q", got.Fragment, tc.fragment)
			}
			if got.IsHTML != tc.isHTML {
				t.Errorf("IsHTML = %v, want %v", got.IsHTML, tc.isHTML)
			}
			if tc.kind == File || tc.kind == Missing || tc.kind == Excluded {
				want := filepath.Join(root, filepath.FromSlash(tc.rel))
				if got.Abs != want {
					t.Errorf("Abs = %q, want %q", got.Abs, want)
				}
			}
			if tc.kind == Missing && got.Err == nil {
				t.Error("Missing should carry Err")
			}
			if tc.kind == Invalid && got.Err == nil {
				t.Error("Invalid should carry Err")
			}
		})
	}
}

func TestResolveDirectoryIsNotRegular(t *testing.T) {
	r, root := newSite(t)
	// A directory named like a file, without index.html inside, is Missing.
	if err := os.MkdirAll(filepath.Join(root, "weird.html"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := r.Resolve("weird.html", "index.html")
	if got.Kind != Missing || got.Rel != "weird.html/index.html" {
		t.Errorf("got %+v", got)
	}
}

func TestBaseOf(t *testing.T) {
	tests := []struct {
		page string
		href string
		base string
		kind Kind
	}{
		{"docs/guide.html", "", "docs/guide.html", File},
		{"docs/guide.html", "#x", "docs/guide.html", File},
		{"docs/guide.html", "https://example.com/", "docs/guide.html", External},
		{"docs/guide.html", "//example.com/", "docs/guide.html", External},
		{"docs/guide.html", "/", "", File},
		{"docs/guide.html", "/assets/", "assets/", File},
		{"docs/guide.html", "/assets", "assets", File},
		{"docs/guide.html", "../", "", File},
		{"docs/guide.html", "..", "", File},
		{"docs/guide.html", "./", "docs/", File},
		{"docs/guide.html", ".", "docs/", File},
		{"docs/guide.html", "sub/", "docs/sub/", File},
		{"docs/guide.html", "sub/page.html", "docs/sub/page.html", File},
		{"docs/guide.html", "../../up/", "docs/guide.html", Outside},
		{"docs/guide.html", "100%", "docs/guide.html", Invalid},
		{"index.html", "?q=1", "index.html", File},
	}
	for _, tc := range tests {
		t.Run(tc.page+"|"+tc.href, func(t *testing.T) {
			base, kind := BaseOf(tc.page, tc.href)
			if base != tc.base || kind != tc.kind {
				t.Errorf("BaseOf(%q, %q) = (%q, %v), want (%q, %v)", tc.page, tc.href, base, kind, tc.base, tc.kind)
			}
		})
	}
}

func TestBaseOfComposesWithResolve(t *testing.T) {
	r, _ := newSite(t)
	base, kind := BaseOf("docs/guide.html", "/")
	if kind != File {
		t.Fatalf("kind = %v", kind)
	}
	got := r.Resolve("css/style.css", base)
	if got.Kind != File || got.Rel != "css/style.css" {
		t.Errorf("got %+v", got)
	}
	base, _ = BaseOf("index.html", "docs/")
	got = r.Resolve("guide.html", base)
	if got.Kind != File || got.Rel != "docs/guide.html" {
		t.Errorf("got %+v", got)
	}
}

func TestExcluded(t *testing.T) {
	tests := []struct {
		pattern string
		rel     string
		want    bool
	}{
		{"drafts", "drafts/secret.html", true},
		{"drafts", "drafts/deep/x.html", true},
		{"drafts", "docs/drafts.html", false},
		{"drafts", "drafts.html", false},
		{"*.bak", "a.bak", true},
		{"*.bak", "docs/a.bak", true},
		{"*.bak", "docs/a.bak.html", false},
		{"docs/internal/*", "docs/internal/a.html", true},
		{"docs/internal/*", "docs/internal/deep/a.html", true},
		{"docs/internal/*", "docs/a.html", false},
		{"docs/internal", "docs/internal/a.html", true},
		{"docs/*.html", "docs/a.html", true},
		{"docs/*.html", "docs/sub/a.html", false},
		{"index.html", "index.html", true},
		{"index.html", "docs/index.html", true},
		{"./drafts/", "drafts/x.html", false}, // trailing slash never matches a segment
		{"/drafts", "drafts/x.html", true},
	}
	for _, tc := range tests {
		t.Run(tc.pattern+"|"+tc.rel, func(t *testing.T) {
			r, err := New(t.TempDir(), []string{tc.pattern})
			if err != nil {
				t.Fatal(err)
			}
			if got := r.Excluded(tc.rel); got != tc.want {
				t.Errorf("Excluded(%q) with %q = %v, want %v", tc.rel, tc.pattern, got, tc.want)
			}
		})
	}
}

func TestIsHTMLPath(t *testing.T) {
	for p, want := range map[string]bool{
		"a.html": true, "a.htm": true, "A.HTML": true, "dir/a.html": true,
		"a.xhtml": false, "a.css": false, "html": false, "a.html.bak": false,
	} {
		if got := IsHTMLPath(p); got != want {
			t.Errorf("IsHTMLPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestMIMEType(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	tests := []struct {
		path string
		data []byte
		want string
	}{
		{"a.css", nil, "text/css"},
		{"a.JS", nil, "text/javascript"},
		{"a.mjs", nil, "text/javascript"},
		{"a.svg", nil, "image/svg+xml"},
		{"a.woff2", nil, "font/woff2"},
		{"a.png", nil, "image/png"},
		{"a.pdf", nil, "application/pdf"},
		{"a.html", nil, "text/html"},
		{"noext", png, "image/png"},
		{"noext", []byte("hello world"), "text/plain"},
		{"noext", []byte{0, 1, 2, 3, 0xff, 0xfe}, "application/octet-stream"},
	}
	for _, tc := range tests {
		if got := MIMEType(tc.path, tc.data); got != tc.want {
			t.Errorf("MIMEType(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestKindString(t *testing.T) {
	names := []string{"empty", "fragment", "external", "invalid", "outside", "missing", "excluded", "file"}
	for i, want := range names {
		if got := Kind(i).String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", i, got, want)
		}
	}
	if !strings.HasPrefix(Kind(99).String(), "Kind(") {
		t.Error("unknown kind should format as Kind(n)")
	}
}
