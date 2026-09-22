package page

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/japanese"

	"github.com/ukaji3/furoshiki/internal/diag"
	"github.com/ukaji3/furoshiki/internal/resolve"
	"github.com/ukaji3/furoshiki/internal/store"
)

// env bundles a Processor with the collaborators tests inspect.
type env struct {
	p     *Processor
	store *store.Store
	diags *diag.Collector
	root  string
}

// newEnv writes files into a fresh site directory and returns a Processor for
// it. Values are file contents; keys are site-relative paths.
func newEnv(t *testing.T, files map[string]string, mod func(*Options)) *env {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rs, err := resolve.New(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{store: store.New(), diags: &diag.Collector{}, root: root}
	opts := Options{Resolver: rs, Store: e.store, Diag: e.diags, Files: NewFileCache(), Shim: "/*shim*/"}
	if mod != nil {
		mod(&opts)
	}
	e.p = NewProcessor(opts)
	return e
}

func (e *env) process(t *testing.T, rel string) *Result {
	t.Helper()
	res, err := e.p.Process(rel)
	if err != nil {
		t.Fatalf("Process(%s): %v", rel, err)
	}
	return res
}

// placeholderFor returns the placeholder of the resource first seen under rel.
func (e *env) placeholderFor(t *testing.T, rel string) string {
	t.Helper()
	for _, r := range e.store.Resources() {
		if r.Rel == rel {
			return r.Placeholder()
		}
	}
	t.Fatalf("no resource for %s; have %v", rel, e.resourceRels())
	return ""
}

func (e *env) resourceRels() []string {
	var out []string
	for _, r := range e.store.Resources() {
		out = append(out, r.Rel)
	}
	return out
}

func (e *env) resource(t *testing.T, rel string) *store.Resource {
	t.Helper()
	for _, r := range e.store.Resources() {
		if r.Rel == rel {
			return r
		}
	}
	t.Fatalf("no resource for %s; have %v", rel, e.resourceRels())
	return nil
}

func (e *env) warnings() []string {
	var out []string
	for _, d := range e.diags.Items() {
		if d.Level == diag.Warning {
			out = append(out, d.String())
		}
	}
	return out
}

func (e *env) infos() []string {
	var out []string
	for _, d := range e.diags.Items() {
		if d.Level == diag.Info {
			out = append(out, d.String())
		}
	}
	return out
}

func mustContain(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("missing %q in:\n%s", sub, s)
		}
	}
}

func mustNotContain(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			t.Errorf("unexpected %q in:\n%s", sub, s)
		}
	}
}

func hasDiag(list []string, sub string) bool {
	for _, d := range list {
		if strings.Contains(d, sub) {
			return true
		}
	}
	return false
}

func TestLinks(t *testing.T) {
	e := newEnv(t, map[string]string{
		"index.html": `<!DOCTYPE html><html lang="ja"><head><title> Home  Page </title></head><body>
<a id="about" href="about.html">about</a>
<a id="sub" href="docs/guide.html?x=1#sec">guide</a>
<a id="dir" href="docs/">docs</a>
<a id="frag" href="#top">top</a>
<a id="empty-frag" href="#">empty</a>
<a id="ext" href="https://example.com/" target="_self">ext</a>
<a id="blank" href="about.html" target="_blank">blank</a>
<a id="self" href="about.html" target="_self">self</a>
<a id="mail" href="mailto:x@example.com">mail</a>
<a id="js" href="javascript:void(0)">js</a>
<a id="missing" href="nope.html">missing</a>
<a id="outside" href="../secret.html">outside</a>
<a id="self-page" href="index.html">self page</a>
<a id="query-self" href="?p=2">query self</a>
<map><area href="about.html#a" alt="a"></map>
<svg><a href="docs/guide.html"><text>svg</text></a></svg>
</body></html>`,
		"about.html":      `<title>About</title>`,
		"docs/guide.html": `<title>Guide</title>`,
		"docs/index.html": `<title>Docs</title>`,
	}, nil)
	res := e.process(t, "index.html")

	if res.Title != "Home Page" {
		t.Errorf("Title = %q", res.Title)
	}
	if res.Lang != "ja" {
		t.Errorf("Lang = %q", res.Lang)
	}
	if got := strings.Join(res.Links, ","); got != "about.html,docs/guide.html,docs/index.html" {
		t.Errorf("Links = %q", got)
	}
	h := res.HTML
	mustContain(t, h,
		`<a href="#/about.html" id="about" data-furoshiki="route">`,
		`<a href="#/docs/guide.html?x=1#sec" id="sub" data-furoshiki="route">`,
		`<a href="#/docs/index.html" id="dir" data-furoshiki="route">`,
		`<a href="#/index.html#top" id="frag" data-furoshiki="route">`,
		`<a href="#/index.html" id="empty-frag" data-furoshiki="route">`,
		`<a href="https://example.com/" id="ext">`,
		`<a href="#/about.html" id="blank" target="_blank" data-furoshiki="route">`,
		`<a href="#/about.html" id="self" data-furoshiki="route">`,
		`<a href="mailto:x@example.com" id="mail">`,
		`<a href="javascript:void(0)" id="js">`,
		`<a href="nope.html" id="missing">`,
		`<a href="../secret.html" id="outside">`,
		`<a href="#/index.html" id="self-page" data-furoshiki="route">`,
		`<a href="#/index.html?p=2" id="query-self" data-furoshiki="route">`,
		`<area href="#/about.html#a" alt="a" data-furoshiki="route"/>`,
		`<a href="#/docs/guide.html" data-furoshiki="route"><text>svg</text></a>`,
		`<base target="_top"/>`,
		`<meta charset="utf-8"/>`,
		`<script data-furoshiki="shim">/*shim*/</script>`,
		`<html lang="ja" data-furoshiki-page="index.html">`,
	)
	w := e.warnings()
	if !hasDiag(w, `"nope.html": file not found`) {
		t.Errorf("missing warning for nope.html: %v", w)
	}
	if !hasDiag(w, `"../secret.html": reference points outside`) {
		t.Errorf("missing warning for outside link: %v", w)
	}
	if len(w) != 2 {
		t.Errorf("unexpected warnings: %v", w)
	}
}

func TestSinglePage(t *testing.T) {
	e := newEnv(t, map[string]string{
		"index.html": `<a href="about.html">a</a><a href="#x">f</a><img src="a.png">`,
		"about.html": `x`,
		"a.png":      "PNG",
	}, func(o *Options) { o.SinglePage = true })
	res := e.process(t, "index.html")
	if len(res.Links) != 0 {
		t.Errorf("Links = %v, want none", res.Links)
	}
	mustContain(t, res.HTML, `<a href="about.html">`, `<a href="#/index.html#x" data-furoshiki="route">`, `<img src="`+e.placeholderFor(t, "a.png")+`"/>`)
	if !hasDiag(e.infos(), "single-page mode") {
		t.Errorf("expected single-page info, got %v", e.infos())
	}
}

func TestStylesheets(t *testing.T) {
	e := newEnv(t, map[string]string{
		"index.html": `<html><head>
<link rel="stylesheet" href="css/site.css" media="screen" integrity="sha256-x" crossorigin="anonymous">
<link rel="stylesheet" href="css/site.css">
<link rel="alternate stylesheet" href="css/alt.css" title="Alt">
<link rel="stylesheet" type="text/less" href="css/x.less">
<link rel="preload" as="style" href="css/site.css" onload="this.rel='stylesheet'">
<style>body{background:url(img/bg.png)} @import "css/alt.css";</style>
</head><body style="background-image: url('img/bg.png')"><p style="color:red">x</p></body></html>`,
		"css/site.css":  `@import url(base.css) screen;@import "missing.css";body{background:url(../img/bg.png) , url("../fonts/f.woff2#iefix")}`,
		"css/base.css":  `@import "site.css";h1{color:red}`,
		"css/alt.css":   `p{}`,
		"css/x.less":    `@x: 1;`,
		"img/bg.png":    "PNG",
		"fonts/f.woff2": "WOFF2",
	}, nil)
	res := e.process(t, "index.html")
	h := res.HTML

	site := e.placeholderFor(t, "css/site.css")
	alt := e.placeholderFor(t, "css/alt.css")
	bg := e.placeholderFor(t, "img/bg.png")
	mustContain(t, h,
		`<link rel="stylesheet" href="`+site+`" media="screen"/>`,
		`<link rel="stylesheet" href="`+site+`"/>`,
		`<link rel="alternate stylesheet" href="`+alt+`" title="Alt"/>`,
		`<link rel="stylesheet" type="text/less" href="css/x.less"/>`,
		`<link rel="preload" as="style" href="`+site+`" onload="this.rel=&#39;stylesheet&#39;"/>`,
		`<style>body{background:url("`+bg+`")} @import url("`+alt+`");</style>`,
		`<body style="background-image: url(&#34;`+bg+`&#34;)">`,
		`<p style="color:red">`,
	)
	mustNotContain(t, h, "integrity", "crossorigin")

	siteRes := e.resource(t, "css/site.css")
	if !siteRes.Text || siteRes.MIME != "text/css" {
		t.Errorf("site.css resource = %+v", siteRes)
	}
	base := e.placeholderFor(t, "css/base.css")
	font := e.placeholderFor(t, "fonts/f.woff2")
	if got, want := string(siteRes.Data), `@import url("`+base+`") screen;@import "missing.css";body{background:url("`+bg+`") , url("`+font+`#iefix")}`; got != want {
		t.Errorf("site.css =\n%s\nwant\n%s", got, want)
	}
	// The circular import back to site.css is left unchanged.
	if got := string(e.resource(t, "css/base.css").Data); got != `@import "site.css";h1{color:red}` {
		t.Errorf("base.css = %s", got)
	}
	w := e.warnings()
	if !hasDiag(w, `css/site.css: "missing.css": file not found`) {
		t.Errorf("missing warning attributed to the style sheet: %v", w)
	}
	if !hasDiag(w, `circular @import of css/site.css`) {
		t.Errorf("missing circular import warning: %v", w)
	}
	if e.store.Len() != 5 {
		t.Errorf("resources = %v, want site, base, alt, bg, font", e.resourceRels())
	}
	// Fully expanded, the style sheet becomes a data: URL containing data: URLs.
	expanded := e.store.Expand(`<link href="` + site + `">`)
	mustContain(t, expanded, "data:text/css;charset=utf-8,", store.PercentEncode([]byte(`url("data:image/png;base64,UE5H")`)))
}

func TestScripts(t *testing.T) {
	e := newEnv(t, map[string]string{
		"index.html": `<head>
<script src="js/app.js" defer integrity="x"></script>
<script src="js/app.js" async></script>
<script type="module" src="js/mod.js"></script>
<script type="text/template" src="tpl.html"></script>
<script type="application/json" id="d">{"a":1}</script>
<script type="module">import x from './js/mod.js';</script>
<script>var inline = "</scr" + "ipt>";</script>
<script src="https://cdn.example.com/x.js"></script>
<script src="nope.js"></script>
<link rel="modulepreload" href="js/mod.js">
<link rel="preload" as="script" href="js/app.js">
</head>`,
		"js/app.js": "console.log('app');\n",
		"js/mod.js": `import {a} from "./lib.js";export default a;`,
		"tpl.html":  `<b>tpl</b>`,
	}, nil)
	res := e.process(t, "index.html")
	h := res.HTML
	app := e.placeholderFor(t, "js/app.js")
	mod := e.placeholderFor(t, "js/mod.js")
	mustContain(t, h,
		`<script src="`+app+`" defer=""></script>`,
		`<script src="`+app+`" async=""></script>`,
		`<script type="module" src="`+mod+`"></script>`,
		`<script type="text/template" src="tpl.html"></script>`,
		`<script type="application/json" id="d">{"a":1}</script>`,
		`<script type="module">import x from './js/mod.js';</script>`,
		`<script>var inline = "</scr" + "ipt>";</script>`,
		`<script src="https://cdn.example.com/x.js"></script>`,
		`<script src="nope.js"></script>`,
		`<link rel="modulepreload" href="`+mod+`"/>`,
		`<link rel="preload" as="script" href="`+app+`"/>`,
	)
	mustNotContain(t, h, "integrity")
	r := e.resource(t, "js/app.js")
	if !r.Text || r.MIME != "text/javascript" || string(r.Data) != "console.log('app');\n" {
		t.Errorf("app.js resource = %+v", r)
	}
	if e.store.Len() != 2 {
		t.Errorf("resources = %v", e.resourceRels())
	}
	w := e.warnings()
	if !hasDiag(w, `"js/mod.js": module script uses import`) {
		t.Errorf("missing module warning: %v", w)
	}
	if !hasDiag(w, "inline module script uses import") {
		t.Errorf("missing inline module warning: %v", w)
	}
	if !hasDiag(w, `"nope.js": file not found`) {
		t.Errorf("missing not-found warning: %v", w)
	}
	if !hasDiag(e.infos(), `"tpl.html": script with type "text/template" is not JavaScript`) {
		t.Errorf("missing template info: %v", e.infos())
	}
}

func TestMediaAndImages(t *testing.T) {
	e := newEnv(t, map[string]string{
		"index.html": `<body background="img/bg.gif">
<img src="img/a.png" srcset="img/a.png 1x, img/b.png 2x">
<img srcset="img/a.png 480w,img/b.png 800w" sizes="100vw">
<picture><source srcset="img/b.png" type="image/png"><img src="img/a.png"></picture>
<video src="m/v.mp4" poster="img/a.png"></video>
<audio src="m/a.mp3"></audio>
<video><source src="m/v.mp4"><track src="m/t.vtt"></video>
<embed src="m/e.swf"><object data="m/d.pdf"></object>
<input type="image" src="img/a.png"><input type="text" src="img/a.png">
<table background="img/bg.gif"><tr><td background="img/bg.gif">x</td></tr></table>
<img src="data:image/gif;base64,R0lGOD"><img src="">
<a href="m/d.pdf">pdf</a><a href="m/d.pdf" download="">dl</a><a href="m/d.pdf" download="x.pdf">named</a>
<svg><image href="img/a.png"/><image xlink:href="img/b.png"/></svg>
<template><img src="img/a.png"></template>
<noscript><img src="img/b.png"></noscript>
</body>`,
		"img/a.png": "A", "img/b.png": "B", "img/bg.gif": "G",
		"m/v.mp4": "V", "m/a.mp3": "M", "m/t.vtt": "T", "m/e.swf": "S", "m/d.pdf": "%PDF",
	}, nil)
	res := e.process(t, "index.html")
	h := res.HTML
	a, b, bg := e.placeholderFor(t, "img/a.png"), e.placeholderFor(t, "img/b.png"), e.placeholderFor(t, "img/bg.gif")
	v, m, tr, sw, pdf := e.placeholderFor(t, "m/v.mp4"), e.placeholderFor(t, "m/a.mp3"), e.placeholderFor(t, "m/t.vtt"), e.placeholderFor(t, "m/e.swf"), e.placeholderFor(t, "m/d.pdf")
	mustContain(t, h,
		`<body background="`+bg+`">`,
		`<img src="`+a+`" srcset="`+a+` 1x, `+b+` 2x"/>`,
		`<img srcset="`+a+` 480w, `+b+` 800w" sizes="100vw"/>`,
		`<source srcset="`+b+`" type="image/png"/>`,
		`<video src="`+v+`" poster="`+a+`">`,
		`<audio src="`+m+`"></audio>`,
		`<source src="`+v+`"/><track src="`+tr+`"/>`,
		`<embed src="`+sw+`"/><object data="`+pdf+`">`,
		`<input type="image" src="`+a+`"/><input type="text" src="img/a.png"/>`,
		`<table background="`+bg+`">`,
		`<td background="`+bg+`">`,
		`<img src="data:image/gif;base64,R0lGOD"/><img src=""/>`,
		`<a href="`+pdf+`" download="d.pdf">pdf</a><a download="d.pdf" href="`+pdf+`">dl</a><a download="x.pdf" href="`+pdf+`">named</a>`,
		`<image href="`+a+`"></image><image xlink:href="`+b+`"></image>`,
		`<template><img src="`+a+`"/></template>`,
		`<noscript><img src="`+b+`"/></noscript>`,
	)
	if e.store.Len() != 8 {
		t.Errorf("resources = %v", e.resourceRels())
	}
	if r := e.resource(t, "m/d.pdf"); r.MIME != "application/pdf" || r.Text {
		t.Errorf("pdf resource = %+v", r)
	}
	if len(e.warnings()) != 0 {
		t.Errorf("unexpected warnings: %v", e.warnings())
	}
}

func TestBaseHref(t *testing.T) {
	e := newEnv(t, map[string]string{
		"docs/page.html": `<head><base href="/" target="_blank"><base href="/ignored/"></head>
<body><a href="about.html">a</a><img src="img/a.png"><a href="#f">f</a></body>`,
		"about.html": "x",
		"img/a.png":  "A",
	}, nil)
	res := e.process(t, "docs/page.html")
	mustContain(t, res.HTML,
		`<base target="_blank"/>`,
		`<a href="#/about.html" data-furoshiki="route">`,
		`<img src="`+e.placeholderFor(t, "img/a.png")+`"/>`,
		`<a href="#/docs/page.html#f" data-furoshiki="route">`,
	)
	if strings.Count(res.HTML, "<base") != 1 {
		t.Errorf("original <base> elements should be removed:\n%s", res.HTML)
	}
	if got := strings.Join(res.Links, ","); got != "about.html" {
		t.Errorf("Links = %q", got)
	}
}

func TestExternalBaseHref(t *testing.T) {
	e := newEnv(t, map[string]string{
		"index.html": `<head><base href="https://example.com/site/"></head><body><a href="about.html">a</a><img src="img/a.png"><a href="#f">f</a><a href="/root.html">r</a></body>`,
		"about.html": "x",
		"img/a.png":  "A",
	}, nil)
	res := e.process(t, "index.html")
	mustContain(t, res.HTML, `<a href="about.html">`, `<img src="img/a.png"/>`, `<a href="#/index.html#f" data-furoshiki="route">`, `<a href="/root.html">`)
	if e.store.Len() != 0 || len(res.Links) != 0 {
		t.Errorf("nothing should be embedded with an external base; resources=%v links=%v", e.resourceRels(), res.Links)
	}
	if !hasDiag(e.warnings(), "<base href> is an external URL") {
		t.Errorf("missing warning: %v", e.warnings())
	}
}

func TestMeta(t *testing.T) {
	e := newEnv(t, map[string]string{
		"index.html": `<!DOCTYPE html><html><head>
<meta charset="utf-8">
<meta http-equiv="Content-Type" content="text/html; charset=utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'self'">
<meta name="viewport" content="width=device-width">
<meta http-equiv="refresh" content="3; url=about.html#x">
<meta http-equiv="refresh" content="0;URL='https://example.com/'">
<meta http-equiv="refresh" content="5">
</head><body></body></html>`,
		"about.html": "x",
	}, nil)
	res := e.process(t, "index.html")
	h := res.HTML
	if strings.Count(h, "<meta charset") != 1 || !strings.HasPrefix(h, `<!DOCTYPE html><html data-furoshiki-page="index.html"><head><meta charset="utf-8"/>`) {
		t.Errorf("charset normalisation wrong:\n%s", h)
	}
	mustNotContain(t, h, "Content-Type", "Content-Security-Policy")
	mustContain(t, h,
		`<meta name="viewport" content="width=device-width"/>`,
		`<script>setTimeout(function(){top.location.hash="#/about.html#x"},3000);</script>`,
		`<script>setTimeout(function(){top.location.href="https://example.com/"},0);</script>`,
		`<meta http-equiv="refresh" content="5"/>`,
	)
	if got := strings.Join(res.Links, ","); got != "about.html" {
		t.Errorf("Links = %q", got)
	}
	if !hasDiag(e.warnings(), "Content-Security-Policy") {
		t.Errorf("missing CSP warning: %v", e.warnings())
	}
}

func TestCharsetDecoding(t *testing.T) {
	const jp = "日本語ページ"
	sjis, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte(`<html><head><meta charset="Shift_JIS"><title>` + jp + `</title></head><body><p>` + jp + `</p></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	sjisCSS, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte(`p::after{content:"` + jp + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	eucJS, err := japanese.EUCJP.NewEncoder().Bytes([]byte(`var s = "` + jp + `";`))
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, map[string]string{
		"sjis.html":   string(sjis),
		"nodecl.html": string(sjis[len(`<html><head><meta charset="Shift_JIS">`):]),
		"bad.html":    `<meta charset="klingon"><p>x</p>`,
		"s.css":       string(sjisCSS),
		"e.js":        string(eucJS),
		"link.html":   string(sjis[:len(`<html><head><meta charset="Shift_JIS">`)]) + `<link rel="stylesheet" href="s.css"><script src="e.js" charset="euc-jp"></script>`,
	}, func(o *Options) { o.DefaultCharset = "shift_jis" })

	res := e.process(t, "sjis.html")
	if res.Title != jp || !strings.Contains(res.HTML, "<p>"+jp+"</p>") {
		t.Errorf("Shift_JIS page not decoded: title=%q", res.Title)
	}
	mustNotContain(t, res.HTML, "Shift_JIS")

	res = e.process(t, "nodecl.html")
	if !strings.Contains(res.HTML, jp) {
		t.Errorf("default charset not applied:\n%s", res.HTML)
	}

	e.process(t, "bad.html")
	if !hasDiag(e.warnings(), `unknown charset "klingon"`) {
		t.Errorf("missing unknown charset warning: %v", e.warnings())
	}

	e.process(t, "link.html")
	if got := string(e.resource(t, "s.css").Data); got != `p::after{content:"`+jp+`"}` {
		t.Errorf("css not decoded with the document charset: %q", got)
	}
	if got := string(e.resource(t, "e.js").Data); got != `var s = "`+jp+`";` {
		t.Errorf("js not decoded with its charset attribute: %q", got)
	}
	if !hasDiag(e.infos(), "sjis.html: decoded from shift_jis") {
		t.Errorf("missing decode info: %v", e.infos())
	}
}

func TestFrames(t *testing.T) {
	e := newEnv(t, map[string]string{
		"index.html": `<body>
<iframe src="embed.html"></iframe>
<iframe src="loop.html"></iframe>
<iframe src="doc.pdf"></iframe>
<iframe src="https://example.com/"></iframe>
<iframe srcdoc="<p>x</p>" src="embed.html"></iframe>
</body>`,
		"frames.html": `<html><frameset><frame src="embed.html"><frame src="doc.pdf"></frameset></html>`,
		"embed.html":  `<title>E</title><a href="deep.html">d</a><img src="a.png">`,
		"loop.html":   `<iframe src="loop.html"></iframe><iframe src="index.html"></iframe>`,
		"deep.html":   "x",
		"a.png":       "A",
		"doc.pdf":     "%PDF",
	}, nil)
	res := e.process(t, "index.html")
	h := res.HTML
	a := e.placeholderFor(t, "a.png")
	// The embedded page is a complete document with routes and placeholders,
	// attribute-escaped inside srcdoc.
	mustContain(t, h,
		`<iframe srcdoc="&lt;html data-furoshiki-page=&#34;embed.html&#34;&gt;&lt;head&gt;&lt;meta charset=&#34;utf-8&#34;/&gt;&lt;base target=&#34;_top&#34;/&gt;`,
		`&lt;a href=&#34;#/deep.html&#34; data-furoshiki=&#34;route&#34;&gt;d&lt;/a&gt;&lt;img src=&#34;`+a+`&#34;/&gt;`,
		`<iframe src="`+e.placeholderFor(t, "doc.pdf")+`"></iframe>`,
		`<iframe src="https://example.com/"></iframe>`,
		`<iframe srcdoc="&lt;p&gt;x&lt;/p&gt;" src="embed.html"></iframe>`,
	)
	if strings.Count(h, `data-furoshiki-page=&#34;embed.html&#34;`) != 1 {
		t.Errorf("embed.html should be embedded once:\n%s", h)
	}
	if !strings.Contains(h, `<iframe srcdoc="&lt;html data-furoshiki-page=&#34;loop.html&#34;&gt;`) {
		t.Errorf("loop.html should be embedded once:\n%s", h)
	}
	// Inside loop.html both frames stay unchanged: one is itself, one is an ancestor.
	if strings.Count(h, `src=&#34;loop.html&#34;`) != 1 || strings.Count(h, `src=&#34;index.html&#34;`) != 1 {
		t.Errorf("cyclic frames should be left unchanged:\n%s", h)
	}
	if got := strings.Join(res.Links, ","); got != "deep.html" {
		t.Errorf("Links = %q, want links discovered inside frames", got)
	}
	fs := e.process(t, "frames.html")
	mustContain(t, fs.HTML,
		`<frame srcdoc="&lt;html data-furoshiki-page=&#34;embed.html&#34;&gt;`,
		`<frame src="`+e.placeholderFor(t, "doc.pdf")+`">`,
	)
	w := e.warnings()
	if n := 0; true {
		for _, d := range w {
			if strings.Contains(d, "frame embeds an ancestor page") {
				n++
			}
		}
		if n != 2 {
			t.Errorf("want 2 ancestor warnings, got %v", w)
		}
	}
}

func TestSVGSprite(t *testing.T) {
	e := newEnv(t, map[string]string{
		"index.html": `<body>
<svg><use href="img/sprite.svg#star"/></svg>
<svg><use xlink:href="img/sprite.svg#moon"/></svg>
<svg><use href="#local"/></svg>
<svg><use href="img/sprite.svg"/></svg>
<svg><use href="img/nope.svg#x"/></svg>
<svg><use href="img/notsvg.svg#x"/></svg>
</body>`,
		"img/sprite.svg": `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd">
<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" style="display:none">
<symbol id="star" viewBox="0 0 10 10"><path d="M0 0h10v10z"/></symbol>
<symbol id="moon"><image href="moon.png"/></symbol>
</svg>`,
		"img/moon.png":   "MOON",
		"img/notsvg.svg": `not an svg document`,
	}, nil)
	res := e.process(t, "index.html")
	h := res.HTML
	moon := e.placeholderFor(t, "img/moon.png")
	mustContain(t, h,
		`<use href="#star"></use>`,
		`<use xlink:href="#moon"></use>`,
		`<use href="#local"></use>`,
		`<use href="img/sprite.svg"></use>`,
		`<use href="img/nope.svg#x"></use>`,
		`<use href="img/notsvg.svg#x"></use>`,
		`<body><svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" style="display:none;position:absolute;width:0;height:0;overflow:hidden" aria-hidden="true" data-furoshiki-sprite="img/sprite.svg">`,
		`<symbol id="star" viewBox="0 0 10 10"><path d="M0 0h10v10z"></path></symbol>`,
		`<image href="`+moon+`"></image>`,
	)
	if strings.Count(h, `data-furoshiki-sprite`) != 1 {
		t.Errorf("sprite should be inlined once:\n%s", h)
	}
	mustNotContain(t, h, "<?xml", "DOCTYPE svg")
	w := e.warnings()
	for _, want := range []string{
		`"img/sprite.svg": <use> without a fragment identifier`,
		`"img/nope.svg#x": file not found`,
		`"img/notsvg.svg#x": img/notsvg.svg is not an SVG document`,
	} {
		if !hasDiag(w, want) {
			t.Errorf("missing warning %q in %v", want, w)
		}
	}
}

func TestLinkRelHandling(t *testing.T) {
	e := newEnv(t, map[string]string{
		"index.html": `<head>
<link rel="icon" href="favicon.ico">
<link rel="shortcut icon" href="favicon.ico">
<link rel="apple-touch-icon" href="touch.png">
<link rel="preload" as="font" href="f.woff2" type="font/woff2" crossorigin>
<link rel="preload" as="image" imagesrcset="a.png 1x, b.png 2x" imagesizes="100vw">
<link rel="prefetch" href="about.html">
<link rel="manifest" href="site.webmanifest">
<link rel="next" href="about.html">
<link rel="canonical" href="https://example.com/">
<link rel="dns-prefetch" href="//cdn.example.com">
<link rel="stylesheet" href="https://cdn.example.com/x.css">
</head>`,
		"favicon.ico": "ICO", "touch.png": "T", "f.woff2": "F", "a.png": "A", "b.png": "B",
		"about.html": "x", "site.webmanifest": "{}",
	}, nil)
	res := e.process(t, "index.html")
	h := res.HTML
	ico, touch, f, a, b := e.placeholderFor(t, "favicon.ico"), e.placeholderFor(t, "touch.png"), e.placeholderFor(t, "f.woff2"), e.placeholderFor(t, "a.png"), e.placeholderFor(t, "b.png")
	mustContain(t, h,
		`<link rel="icon" href="`+ico+`"/>`,
		`<link rel="shortcut icon" href="`+ico+`"/>`,
		`<link rel="apple-touch-icon" href="`+touch+`"/>`,
		`<link rel="preload" as="font" href="`+f+`" type="font/woff2"/>`,
		`<link rel="preload" as="image" imagesrcset="`+a+` 1x, `+b+` 2x" imagesizes="100vw"/>`,
		`<link rel="next" href="#/about.html"/>`,
		`<link rel="canonical" href="https://example.com/"/>`,
		`<link rel="dns-prefetch" href="//cdn.example.com"/>`,
		`<link rel="stylesheet" href="https://cdn.example.com/x.css"/>`,
	)
	mustNotContain(t, h, `rel="prefetch"`, "manifest", "crossorigin")
	if got := strings.Join(res.Links, ","); got != "about.html" {
		t.Errorf("Links = %q", got)
	}
	if e.store.Len() != 5 {
		t.Errorf("resources = %v", e.resourceRels())
	}
}

func TestExcludedAndSizeLimit(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"index.html":    `<a href="drafts/x.html">d</a><img src="big.png"><img src="small.png"><a href="drafts/x.html">again</a>`,
		"drafts/x.html": "x",
		"big.png":       strings.Repeat("B", 100),
		"small.png":     "s",
		"forms.html":    `<form action="submit.php"><button formaction="other.html">b</button><input formaction="https://example.com/"></form><form action="https://example.com/"></form>`,
		"submit.php":    "",
		"other.html":    "",
		"manifest.html": `<html manifest="app.cache"></html>`,
	} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rs, err := resolve.New(root, []string{"drafts"})
	if err != nil {
		t.Fatal(err)
	}
	st, diags := store.New(), &diag.Collector{}
	p := NewProcessor(Options{Resolver: rs, Store: st, Diag: diags, MaxResourceSize: 50})
	res, err := p.Process("index.html")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, res.HTML, `<a href="drafts/x.html">d</a><img src="big.png"/><img src="`+st.Resources()[0].Placeholder()+`"/>`)
	if len(res.Links) != 0 {
		t.Errorf("excluded page must not be crawled: %v", res.Links)
	}
	var infos, warns []string
	for _, d := range diags.Items() {
		if d.Level == diag.Info {
			infos = append(infos, d.String())
		} else {
			warns = append(warns, d.String())
		}
	}
	if !hasDiag(infos, `"drafts/x.html": excluded by pattern`) {
		t.Errorf("missing excluded info: %v", infos)
	}
	if !hasDiag(warns, `"big.png": not embedded: 100 bytes exceeds the resource size limit of 50 bytes`) {
		t.Errorf("missing size warning: %v", warns)
	}

	res, err = p.Process("forms.html")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, res.HTML, `<form action="submit.php">`, `<button formaction="other.html">`)
	n := 0
	for _, d := range diags.Items() {
		if strings.Contains(d.Message, "form submission") {
			n++
		}
	}
	if n != 2 {
		t.Errorf("want 2 form warnings, got %d: %v", n, diags.Items())
	}

	res, err = p.Process("manifest.html")
	if err != nil {
		t.Fatal(err)
	}
	mustNotContain(t, res.HTML, `manifest="`)
	if p.opts.Shim != "" {
		t.Error("shim should be empty")
	}
	mustNotContain(t, res.HTML, `data-furoshiki="shim"`)
}

func TestDeduplicationAcrossPages(t *testing.T) {
	e := newEnv(t, map[string]string{
		"a.html":       `<link rel="stylesheet" href="s.css"><script src="j.js"></script><img src="i.png">`,
		"sub/b.html":   `<link rel="stylesheet" href="../s.css"><script src="/j.js"></script><img src="../i.png"><img src="copy.png">`,
		"s.css":        `body{background:url(i.png)}`,
		"j.js":         `1`,
		"i.png":        "SAME",
		"sub/copy.png": "SAME",
	}, nil)
	ra := e.process(t, "a.html")
	rb := e.process(t, "sub/b.html")
	if e.store.Len() != 3 {
		t.Errorf("want 3 resources (css, js, png), got %v", e.resourceRels())
	}
	png := e.placeholderFor(t, "i.png")
	mustContain(t, ra.HTML, `<img src="`+png+`"/>`)
	mustContain(t, rb.HTML, `<img src="`+png+`"/><img src="`+png+`"/>`)
	if !strings.Contains(string(e.resource(t, "s.css").Data), png) {
		t.Error("style sheet should reference the shared image placeholder")
	}
}

func TestProcessErrors(t *testing.T) {
	e := newEnv(t, map[string]string{"index.html": "x"}, nil)
	if _, err := e.p.Process("missing.html"); err == nil {
		t.Error("Process of a missing file should fail")
	}
}

func TestRoute(t *testing.T) {
	tests := []struct {
		rel, query, frag, want string
	}{
		{"index.html", "", "", "#/index.html"},
		{"docs/guide.html", "", "", "#/docs/guide.html"},
		{"a b/c d.html", "", "", "#/a%20b/c%20d.html"},
		{"日本語.html", "", "", "#/%E6%97%A5%E6%9C%AC%E8%AA%9E.html"},
		{"a.html", "x=1&y=2", "sec", "#/a.html?x=1&y=2#sec"},
		{"a.html", "", "sec", "#/a.html#sec"},
		{"a#b.html", "", "", "#/a%23b.html"},
		{"a?b.html", "", "", "#/a%3Fb.html"},
	}
	for _, tc := range tests {
		if got := Route(tc.rel, tc.query, tc.frag); got != tc.want {
			t.Errorf("Route(%q,%q,%q) = %q, want %q", tc.rel, tc.query, tc.frag, got, tc.want)
		}
	}
}

func TestParseSrcset(t *testing.T) {
	tests := []struct {
		in   string
		want string // formatted
		n    int
	}{
		{"a.png", "a.png", 1},
		{"a.png 1x, b.png 2x", "a.png 1x, b.png 2x", 2},
		{"a.png 480w,b.png 800w", "a.png 480w, b.png 800w", 2},
		{"  a.png  ,  b.png 2x ,", "a.png, b.png 2x", 2},
		{"data:image/png;base64,AAAA 1x, b.png 2x", "data:image/png;base64,AAAA 1x, b.png 2x", 2},
		{"a.png,b.png", "a.png,b.png", 1}, // a comma without whitespace is part of the URL
		{"a.png 1x calc(1,2), b.png", "a.png 1x calc(1,2), b.png", 2},
		{"", "", 0},
		{" , , ", "", 0},
	}
	for _, tc := range tests {
		c := parseSrcset(tc.in)
		if len(c) != tc.n {
			t.Errorf("parseSrcset(%q) = %d candidates %+v, want %d", tc.in, len(c), c, tc.n)
			continue
		}
		if got := formatSrcset(c); got != tc.want {
			t.Errorf("formatSrcset(parseSrcset(%q)) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsJavaScriptType(t *testing.T) {
	for typ, want := range map[string]bool{
		"": true, "module": true, "MODULE": true, "text/javascript": true, "Text/JavaScript": true,
		"application/javascript": true, "text/javascript; charset=utf-8": true, "text/jscript": true,
		"application/json": false, "text/template": false, "importmap": false, "speculationrules": false, "text/x-handlebars": false,
	} {
		if got := isJavaScriptType(typ); got != want {
			t.Errorf("isJavaScriptType(%q) = %v, want %v", typ, got, want)
		}
	}
}

func TestHasModuleImports(t *testing.T) {
	for src, want := range map[string]bool{
		`import a from "./a.js";`:                   true,
		`import {a, b} from './a.js'`:               true,
		`import * as ns from "x"`:                   true,
		`import "./side-effect.js"`:                 true,
		"const x = 1;\nimport y from './y.js';":     true,
		`const x=1;import{a}from"./a.js";`:          true,
		`export {a} from "./a.js";`:                 true,
		`export * from './a.js'`:                    true,
		`const m = await import("./m.js");`:         true,
		`import.meta.url`:                           false,
		`export const a = 1;`:                       false,
		`const important = 1; // importing nothing`: false,
		`var s = "import x from 'y'";`:              false,
		`console.log("hi")`:                         false,
	} {
		if got := hasModuleImports(src); got != want {
			t.Errorf("hasModuleImports(%q) = %v, want %v", src, got, want)
		}
	}
}

func TestFileCache(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewFileCache()
	got, err := c.Read(p)
	if err != nil || string(got) != "one" {
		t.Fatalf("Read = %q, %v", got, err)
	}
	// Later modifications are not observed: the first read is cached.
	if err := os.WriteFile(p, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ = c.Read(filepath.Join(dir, ".", "a.txt"))
	if string(got) != "one" {
		t.Errorf("cached Read = %q, want one", got)
	}
	if _, err := c.Read(filepath.Join(dir, "missing")); err == nil {
		t.Error("Read of a missing file should fail")
	}
	if _, err := c.Read(filepath.Join(dir, "missing")); err == nil {
		t.Error("cached error should be returned again")
	}
	if paths := c.Paths(); len(paths) != 1 || paths[0] != p {
		t.Errorf("Paths = %v", paths)
	}
}
