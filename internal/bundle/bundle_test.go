package bundle

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/ukaji3/furoshiki/internal/diag"
	"github.com/ukaji3/furoshiki/internal/shell"
	"github.com/ukaji3/furoshiki/internal/store"
)

const site1 = "../../testdata/site1"

var payloadRE = regexp.MustCompile(`(?s)<script type="application/json" id="furoshiki-payload">(.*?)</script>`)

// extractPayload parses the payload out of a rendered bundle.
func extractPayload(t *testing.T, bundle []byte) *shell.Payload {
	t.Helper()
	m := payloadRE.FindSubmatch(bundle)
	if m == nil {
		t.Fatal("payload not found in bundle")
	}
	var p shell.Payload
	if err := json.Unmarshal(m[1], &p); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	return &p
}

func build(t *testing.T, opts Options) ([]byte, *Result, *diag.Collector) {
	t.Helper()
	if opts.Dir == "" {
		opts.Dir = site1
	}
	if opts.Generator == "" {
		opts.Generator = "furoshiki vTEST"
	}
	diags := &diag.Collector{}
	opts.Diag = diags
	var buf bytes.Buffer
	res, err := Build(opts, &buf)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return buf.Bytes(), res, diags
}

func diagStrings(c *diag.Collector, level diag.Level) []string {
	var out []string
	for _, d := range c.Items() {
		if d.Level == level {
			out = append(out, d.String())
		}
	}
	return out
}

func hasDiag(list []string, sub string) bool {
	for _, d := range list {
		if strings.Contains(d, sub) {
			return true
		}
	}
	return false
}

func TestBuildSite1(t *testing.T) {
	out, res, diags := build(t, Options{Excludes: []string{"drafts"}})
	p := extractPayload(t, out)

	// Crawl order: breadth first from index.html, links in document order.
	wantPages := []string{"index.html", "about.html", "docs/index.html", "docs/guide.html", "sjis.html", "frame.html"}
	if got := strings.Join(res.Pages, ","); got != strings.Join(wantPages, ",") {
		t.Errorf("Pages = %q\n   want %q", got, strings.Join(wantPages, ","))
	}
	if len(p.Pages) != len(wantPages) {
		t.Errorf("payload has %d pages, want %d", len(p.Pages), len(wantPages))
	}
	if _, ok := p.Pages["unreachable.html"]; ok {
		t.Error("unreachable.html must not be bundled")
	}
	if _, ok := p.Pages["drafts/secret.html"]; ok {
		t.Error("excluded page must not be bundled")
	}
	if p.Entry != "index.html" || res.Entry != "index.html" {
		t.Errorf("entry = %q / %q", p.Entry, res.Entry)
	}
	if res.Title != "Site1 ホーム" || p.Pages["index.html"].Title != "Site1 ホーム" {
		t.Errorf("title = %q / %q", res.Title, p.Pages["index.html"].Title)
	}
	if p.Pages["sjis.html"].Title != "日本語のページ" || !strings.Contains(p.Pages["sjis.html"].HTML, "シフトJISで書かれています。") {
		t.Errorf("Shift_JIS page not decoded: %q", p.Pages["sjis.html"].Title)
	}
	if p.Generator != "furoshiki vTEST" || !bytes.Contains(out, []byte(`<meta name="generator" content="furoshiki vTEST">`)) {
		t.Errorf("generator = %q", p.Generator)
	}
	if !bytes.Contains(out, []byte(`<html lang="ja">`)) || !bytes.Contains(out, []byte(`<title>Site1 ホーム</title>`)) {
		t.Error("shell lang/title not taken from the entry page")
	}

	// Resources: site.css, base.css, app.js, home.js, icon.svg, logo.png
	// (deduplicated with docs/logo-copy.png), bg.png, notes.txt, sprite
	// image is inlined (not a resource).
	wantResources := 8
	if res.Resources != wantResources || len(p.Resources) != wantResources {
		var rels []string
		for k, r := range p.Resources {
			if r.IsText {
				rels = append(rels, k+"(text "+r.MIME+")")
			} else {
				rels = append(rels, k+"("+r.DataURL[:min(40, len(r.DataURL))]+")")
			}
		}
		t.Errorf("resources = %d / %d, want %d: %v", res.Resources, len(p.Resources), wantResources, rels)
	}
	texts, bins := 0, 0
	for _, r := range p.Resources {
		if r.IsText {
			texts++
		} else {
			bins++
		}
	}
	if texts != 4 || bins != 4 {
		t.Errorf("text/binary resources = %d/%d, want 4/4", texts, bins)
	}

	// Every placeholder in every page and text resource must name a
	// resource of the payload.
	check := func(where, text string) {
		for _, m := range store.Placeholder.FindAllStringSubmatch(text, -1) {
			if _, ok := p.Resources[m[1]]; !ok {
				t.Errorf("%s references unknown resource %s", where, m[1])
			}
		}
	}
	for rel, pg := range p.Pages {
		check(rel, pg.HTML)
		if !strings.Contains(pg.HTML, `data-furoshiki-page="`+rel+`"`) {
			t.Errorf("%s lacks its page marker", rel)
		}
		if !strings.Contains(pg.HTML, `<script data-furoshiki="shim">`) {
			t.Errorf("%s lacks the shim", rel)
		}
	}
	for k, r := range p.Resources {
		if r.IsText {
			check("resource "+k, r.Text)
		}
	}

	// Files read: all inputs except the unreachable and excluded ones and
	// the missing references.
	var files []string
	for _, f := range res.Files {
		rel, _ := filepath.Rel(mustAbs(t, site1), f)
		files = append(files, filepath.ToSlash(rel))
	}
	wantFiles := "about.html,css/base.css,css/site.css,docs/guide.html,docs/index.html,docs/logo-copy.png,files/notes.txt,frame.html,img/bg.png,img/icon.svg,img/logo.png,img/sprite.svg,index.html,js/app.js,js/home.js,sjis.html"
	if got := strings.Join(files, ","); got != wantFiles {
		t.Errorf("Files = %s\n   want %s", got, wantFiles)
	}
	if res.Bytes != int64(len(out)) || res.Bytes == 0 {
		t.Errorf("Bytes = %d, len(out) = %d", res.Bytes, len(out))
	}
	if res.ResourceBytes <= 0 {
		t.Errorf("ResourceBytes = %d", res.ResourceBytes)
	}

	warns := diagStrings(diags, diag.Warning)
	for _, want := range []string{
		`index.html: "missing.html": file not found`,
		`index.html: "img/nope.png": file not found`,
	} {
		if !hasDiag(warns, want) {
			t.Errorf("missing warning %q in %v", want, warns)
		}
	}
	if len(warns) != 2 {
		t.Errorf("unexpected warnings: %v", warns)
	}
	infos := diagStrings(diags, diag.Info)
	if !hasDiag(infos, `index.html: "drafts/secret.html": excluded by pattern`) {
		t.Errorf("missing excluded info in %v", infos)
	}
	if !hasDiag(infos, "sjis.html: decoded from shift_jis") {
		t.Errorf("missing decode info in %v", infos)
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	a, _, _ := build(t, Options{})
	b, _, _ := build(t, Options{})
	if !bytes.Equal(a, b) {
		t.Error("two builds of the same site differ")
	}
}

func TestBuildSinglePage(t *testing.T) {
	out, res, _ := build(t, Options{SinglePage: true})
	p := extractPayload(t, out)
	if len(res.Pages) != 1 || len(p.Pages) != 1 {
		t.Errorf("single page build bundled %v", res.Pages)
	}
	if !strings.Contains(p.Pages["index.html"].HTML, `<a href="about.html" id="nav-about">`) {
		t.Error("links must stay unchanged in single-page mode")
	}
}

func TestBuildEntryAndErrors(t *testing.T) {
	out, res, _ := build(t, Options{Entry: "docs/guide.html", Excludes: []string{"drafts"}})
	p := extractPayload(t, out)
	if res.Entry != "docs/guide.html" || p.Entry != "docs/guide.html" {
		t.Errorf("entry = %q", res.Entry)
	}
	if got := strings.Join(res.Pages, ","); got != "docs/guide.html,docs/index.html,index.html,about.html,sjis.html,frame.html" {
		t.Errorf("Pages = %q", got)
	}

	for _, tc := range []struct {
		name string
		opts Options
		want string
	}{
		{"no dir", Options{}, "no site directory"},
		{"missing dir", Options{Dir: filepath.Join(t.TempDir(), "nope")}, "nope"},
		{"missing entry", Options{Dir: site1, Entry: "nope.html"}, "entry page nope.html"},
		{"entry outside", Options{Dir: site1, Entry: "../site1/../../go.mod"}, "not a file inside"},
		{"entry excluded", Options{Dir: site1, Entry: "drafts/secret.html", Excludes: []string{"drafts"}}, "excluded by a pattern"},
		{"entry external", Options{Dir: site1, Entry: "https://example.com/"}, "not a file inside"},
		{"bad exclude", Options{Dir: site1, Excludes: []string{"["}}, "invalid exclude pattern"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Build(tc.opts, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Build error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestBuildTitleFallback(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mysite")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<p>no title</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, res, _ := build(t, Options{Dir: dir})
	if res.Title != "" {
		t.Errorf("Title = %q, want empty", res.Title)
	}
	if !bytes.Contains(out, []byte("<title>mysite</title>")) {
		t.Error("shell title should fall back to the directory name")
	}
}

func TestBuildMaxResourceSize(t *testing.T) {
	_, res, diags := build(t, Options{MaxResourceSize: 100})
	// Files of at most 100 bytes stay: notes.txt (31), bg.png (73),
	// logo.png (73, shared with its copy) and home.js (68). site.css,
	// app.js, icon.svg and sprite.svg are larger and are left as external
	// references; base.css is only reachable through site.css.
	if res.Resources != 4 {
		t.Errorf("Resources = %d, want 4", res.Resources)
	}
	warns := diagStrings(diags, diag.Warning)
	for _, want := range []string{
		`"css/site.css": not embedded: 216 bytes exceeds the resource size limit of 100 bytes`,
		`"js/app.js": not embedded: 263 bytes exceeds the resource size limit of 100 bytes`,
		`"img/sprite.svg#dot": not embedded: 185 bytes exceeds the resource size limit of 100 bytes`,
	} {
		if !hasDiag(warns, want) {
			t.Errorf("missing size warning %q in %v", want, warns)
		}
	}
}

func TestBuildFile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "site.html")
	res, err := BuildFile(Options{Dir: site1, Generator: "g"}, out)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(data)) != res.Bytes || len(data) == 0 {
		t.Errorf("wrote %d bytes, result says %d", len(data), res.Bytes)
	}
	fi, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %o, want 644", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
	var buf bytes.Buffer
	if _, err := Build(Options{Dir: site1, Generator: "g"}, &buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), data) {
		t.Error("BuildFile output differs from Build output")
	}

	// Overwriting the previous bundle is fine.
	if _, err := BuildFile(Options{Dir: site1, Generator: "g"}, out); err != nil {
		t.Errorf("second BuildFile: %v", err)
	}
}

func TestBuildFileRefusesToOverwriteInput(t *testing.T) {
	// Copy the fixture so the guard can be exercised safely.
	dir := t.TempDir()
	for _, f := range []string{"index.html", "css/site.css", "css/base.css"} {
		data, err := os.ReadFile(filepath.Join(site1, f))
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"index.html", "css/site.css"} {
		_, err := BuildFile(Options{Dir: dir}, filepath.Join(dir, target))
		if err == nil || !strings.Contains(err.Error(), "would overwrite an input file") {
			t.Errorf("BuildFile onto %s: err = %v", target, err)
		}
	}
	// A new file inside the site directory is allowed.
	if _, err := BuildFile(Options{Dir: dir}, filepath.Join(dir, "bundle.html")); err != nil {
		t.Errorf("BuildFile into site dir: %v", err)
	}
	// A missing output directory is an error, and no temp file is left.
	if _, err := BuildFile(Options{Dir: dir}, filepath.Join(dir, "nodir", "x.html")); err == nil {
		t.Error("BuildFile into a missing directory should fail")
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
