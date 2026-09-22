package page

import (
	"encoding/json"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/ukaji3/furoshiki/internal/resolve"
	"github.com/ukaji3/furoshiki/internal/textenc"
)

// doc holds the state of one document while it is transformed.
type doc struct {
	*ctx
	rel       string
	root      *html.Node
	htmlEl    *html.Node
	head      *html.Node
	body      *html.Node
	ancestors []string // pages that embed this one through frames

	baseTarget string
	links      []string
	linkSeen   map[string]bool

	remove       []*html.Node
	replacements []replacement
	sprites      map[string]bool
	spriteNodes  []*html.Node
}

type replacement struct {
	old, new *html.Node
}

func newDoc(p *Processor, rel, charset string, root *html.Node, ancestors []string) *doc {
	d := &doc{
		ctx:       &ctx{p: p, page: rel, base: rel, charset: charset},
		rel:       rel,
		root:      root,
		ancestors: ancestors,
		linkSeen:  make(map[string]bool),
		sprites:   make(map[string]bool),
	}
	for n := root.FirstChild; n != nil; n = n.NextSibling {
		if n.Type == html.ElementNode && n.DataAtom == atom.Html {
			d.htmlEl = n
			break
		}
	}
	if d.htmlEl != nil {
		for n := d.htmlEl.FirstChild; n != nil; n = n.NextSibling {
			if n.Type != html.ElementNode {
				continue
			}
			switch n.DataAtom {
			case atom.Head:
				d.head = n
			case atom.Body:
				d.body = n
			}
		}
	}
	return d
}

// transform performs the whole rewrite of the document.
func (d *doc) transform() {
	d.applyBase()
	d.walk(d.root)
	d.finish()
}

// applyBase honours the first <base href> of the document and schedules all
// <base> elements for removal; finish inserts a replacement that only carries
// the target.
func (d *doc) applyBase() {
	var hrefSeen, targetSeen bool
	d.forEach(d.root, func(n *html.Node) {
		if n.Type != html.ElementNode || n.DataAtom != atom.Base || n.Namespace != "" {
			return
		}
		if href, ok := attr(n, "href"); ok && !hrefSeen {
			hrefSeen = true
			base, kind := resolve.BaseOf(d.rel, href)
			switch kind {
			case resolve.External:
				d.baseExternal = true
				d.warn(href, "<base href> is an external URL; relative references were not embedded")
			case resolve.Outside:
				d.warn(href, "<base href> points outside the site root; ignored")
			case resolve.Invalid:
				d.warn(href, "<base href> is not a valid URL; ignored")
			default:
				d.base = base
			}
		}
		if t, ok := attr(n, "target"); ok && !targetSeen {
			targetSeen = true
			d.baseTarget = t
		}
		d.remove = append(d.remove, n)
	})
}

func (d *doc) forEach(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		d.forEach(c, fn)
	}
}

func (d *doc) walk(n *html.Node) {
	if n.Type == html.ElementNode {
		d.element(n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		d.walk(c)
	}
}

func (d *doc) element(n *html.Node) {
	if a := attrPtr(n, "", "style"); a != nil && a.Val != "" {
		a.Val = d.rewriteCSS(a.Val, false)
	}
	switch n.Namespace {
	case "":
	case "svg":
		d.svgElement(n)
		return
	default:
		return
	}
	switch n.DataAtom {
	case atom.A, atom.Area:
		d.anchor(n, attrPtr(n, "", "href"))
	case atom.Link:
		d.link(n)
	case atom.Script:
		d.script(n)
	case atom.Img, atom.Source:
		d.resourceAttr(n, "src")
		d.srcsetAttr(n, "srcset")
	case atom.Video:
		d.resourceAttr(n, "src")
		d.resourceAttr(n, "poster")
	case atom.Audio, atom.Track, atom.Embed:
		d.resourceAttr(n, "src")
	case atom.Object:
		d.resourceAttr(n, "data")
	case atom.Input:
		if strings.EqualFold(attrValue(n, "type"), "image") {
			d.resourceAttr(n, "src")
		}
		d.formAction(n, "formaction")
	case atom.Button:
		d.formAction(n, "formaction")
	case atom.Form:
		d.formAction(n, "action")
	case atom.Iframe, atom.Frame:
		d.frame(n)
	case atom.Meta:
		d.meta(n)
	case atom.Style:
		d.styleElement(n)
	case atom.Body, atom.Table, atom.Thead, atom.Tbody, atom.Tfoot, atom.Tr, atom.Td, atom.Th:
		d.resourceAttr(n, "background")
	case atom.Html:
		if _, ok := attr(n, "manifest"); ok {
			removeAttr(n, "manifest")
			d.info("", "application cache manifest attribute removed")
		}
	}
}

func (d *doc) svgElement(n *html.Node) {
	switch n.Data {
	case "image", "feImage":
		if a := svgHref(n); a != nil {
			if ph, ok := d.resourceRef(a.Val); ok {
				a.Val = ph
			}
		}
	case "use":
		d.svgUse(n)
	case "a":
		d.anchor(n, svgHref(n))
	case "style":
		d.styleElement(n)
	}
}

// resourceAttr embeds the file referenced by the attribute key of n.
func (d *doc) resourceAttr(n *html.Node, key string) {
	a := attrPtr(n, "", key)
	if a == nil || a.Val == "" {
		return
	}
	if ph, ok := d.resourceRef(a.Val); ok {
		a.Val = ph
	}
}

// srcsetAttr embeds every candidate of a srcset (or imagesrcset) attribute.
func (d *doc) srcsetAttr(n *html.Node, key string) {
	a := attrPtr(n, "", key)
	if a == nil || strings.TrimSpace(a.Val) == "" {
		return
	}
	cands := parseSrcset(a.Val)
	changed := false
	for i := range cands {
		if ph, ok := d.resourceRef(cands[i].url); ok {
			cands[i].url = ph
			changed = true
		}
	}
	if changed {
		a.Val = formatSrcset(cands)
	}
}

// anchor rewrites the href of an <a> or <area> element (HTML or SVG).
func (d *doc) anchor(n *html.Node, a *html.Attribute) {
	if a == nil || a.Val == "" {
		return
	}
	ref := d.resolve(a.Val)
	switch ref.Kind {
	case resolve.Fragment:
		a.Val = Route(d.rel, "", ref.Fragment)
		d.markRouted(n)
	case resolve.External:
		d.fixTarget(n)
	case resolve.File:
		if ref.IsHTML {
			if d.p.opts.SinglePage {
				d.info(a.Val, "link to another page left unchanged (single-page mode)")
				return
			}
			d.addLink(ref.Rel)
			a.Val = Route(ref.Rel, ref.Query, ref.Fragment)
			d.markRouted(n)
			return
		}
		ph, ok := d.binaryResource(ref)
		if !ok {
			return
		}
		a.Val = ph
		if dl := attrValue(n, "download"); dl == "" {
			setAttr(n, "download", path.Base(ref.Rel))
		}
	default:
		d.bundleable(ref)
	}
}

// markRouted flags a link whose href is a route so that the runtime shim
// leaves it alone, and makes sure it navigates the top window.
func (d *doc) markRouted(n *html.Node) {
	setAttr(n, "data-furoshiki", "route")
	d.fixTarget(n)
}

// fixTarget removes target="_self": inside the bundle the document lives in
// a frame, and navigating that frame would break the bundle. The default
// target injected by finish then applies.
func (d *doc) fixTarget(n *html.Node) {
	if t, ok := attr(n, "target"); ok && strings.EqualFold(strings.TrimSpace(t), "_self") {
		removeAttr(n, "target")
	}
}

func (d *doc) addLink(rel string) {
	if !d.linkSeen[rel] && rel != d.rel {
		d.linkSeen[rel] = true
		d.links = append(d.links, rel)
	}
}

// link handles <link> elements according to their rel.
func (d *doc) link(n *html.Node) {
	rels := strings.Fields(strings.ToLower(attrValue(n, "rel")))
	has := func(want string) bool { return slices.Contains(rels, want) }
	as := strings.ToLower(strings.TrimSpace(attrValue(n, "as")))

	if has("preload") && as == "image" {
		d.srcsetAttr(n, "imagesrcset")
	}
	href := attrPtr(n, "", "href")
	if href == nil || href.Val == "" {
		return
	}

	switch {
	case has("stylesheet") || (has("preload") && as == "style"):
		if t := strings.TrimSpace(attrValue(n, "type")); t != "" && !strings.EqualFold(t, "text/css") {
			d.info(href.Val, "stylesheet with type %q left unchanged", t)
			return
		}
		ref := d.resolve(href.Val)
		if !d.bundleable(ref) {
			return
		}
		if ph, ok := d.styleSheet(ref, attrValue(n, "charset")); ok {
			href.Val = ph
			removeAttr(n, "integrity")
			removeAttr(n, "crossorigin")
		}
	case has("icon") || has("shortcut") || has("apple-touch-icon") || has("apple-touch-icon-precomposed") || has("mask-icon"):
		if ph, ok := d.resourceRef(href.Val); ok {
			href.Val = ph
		}
	case has("modulepreload") || (has("preload") && as == "script"):
		ref := d.resolve(href.Val)
		if !d.bundleable(ref) {
			return
		}
		if ph, _, ok := d.ctx.script(ref, ""); ok {
			href.Val = ph
			removeAttr(n, "integrity")
			removeAttr(n, "crossorigin")
		}
	case has("preload"):
		if ph, ok := d.resourceRef(href.Val); ok {
			href.Val = ph
			removeAttr(n, "integrity")
			removeAttr(n, "crossorigin")
		}
	case has("prefetch") || has("prerender") || has("manifest"):
		d.remove = append(d.remove, n)
		d.info(href.Val, "<link rel=%q> removed", strings.Join(rels, " "))
	case has("next") || has("prev") || has("alternate") || has("canonical") || has("help") || has("license") || has("author") || has("search"):
		ref := d.resolve(href.Val)
		if ref.Kind == resolve.File && ref.IsHTML && !d.p.opts.SinglePage {
			d.addLink(ref.Rel)
			href.Val = Route(ref.Rel, ref.Query, ref.Fragment)
		}
	}
}

// script embeds external scripts and warns about module imports.
func (d *doc) script(n *html.Node) {
	typ := strings.TrimSpace(attrValue(n, "type"))
	isModule := strings.EqualFold(typ, "module")
	src := attrPtr(n, "", "src")
	if src == nil || src.Val == "" {
		if isModule && hasModuleImports(textContent(n)) {
			d.warn("", "inline module script uses import; module specifiers cannot be resolved inside the bundle")
		}
		return
	}
	if !isJavaScriptType(typ) {
		d.info(src.Val, "script with type %q is not JavaScript; left unchanged", typ)
		return
	}
	ref := d.resolve(src.Val)
	if !d.bundleable(ref) {
		return
	}
	ph, text, ok := d.ctx.script(ref, attrValue(n, "charset"))
	if !ok {
		return
	}
	if isModule && hasModuleImports(text) {
		d.warn(src.Val, "module script uses import; relative module specifiers will not resolve inside the bundle")
	}
	src.Val = ph
	removeAttr(n, "integrity")
	removeAttr(n, "crossorigin")
}

// formAction warns about forms that submit to pages of the site; the bundle
// cannot serve them.
func (d *doc) formAction(n *html.Node, key string) {
	v, ok := attr(n, key)
	if !ok || v == "" {
		return
	}
	ref := d.resolve(v)
	if ref.Kind == resolve.File || ref.Kind == resolve.Missing {
		d.warn(v, "form submission to a site path is not supported inside the bundle; left unchanged")
	}
}

// frame embeds the page referenced by an <iframe> or <frame> as srcdoc.
func (d *doc) frame(n *html.Node) {
	if _, ok := attr(n, "srcdoc"); ok {
		return
	}
	src := attrPtr(n, "", "src")
	if src == nil || src.Val == "" {
		return
	}
	ref := d.resolve(src.Val)
	if !d.bundleable(ref) {
		return
	}
	if !ref.IsHTML {
		if ph, ok := d.binaryResource(ref); ok {
			src.Val = ph
		}
		return
	}
	if ref.Rel == d.rel || slices.Contains(d.ancestors, ref.Rel) {
		d.warn(src.Val, "frame embeds an ancestor page; left unchanged")
		return
	}
	if len(d.ancestors) >= maxFrameDepth {
		d.warn(src.Val, "frames nested more than %d levels deep; left unchanged", maxFrameDepth)
		return
	}
	child, err := d.p.process(ref.Rel, append(slices.Clone(d.ancestors), d.rel))
	if err != nil {
		d.warn(src.Val, "cannot embed frame: %v", err)
		return
	}
	removeAttr(n, "src")
	setAttr(n, "srcdoc", child.HTML)
	for _, l := range child.Links {
		d.addLink(l)
	}
}

var refreshContent = regexp.MustCompile(`(?i)^\s*(\d+(?:\.\d+)?)\s*(?:[;,]|\s)\s*(?:url\s*=\s*)?['"]?\s*([^'"\s]*)`)

// meta normalises encoding declarations, removes Content-Security-Policy
// metadata, and converts redirects to pages of the site into routes.
func (d *doc) meta(n *html.Node) {
	if _, ok := attr(n, "charset"); ok {
		d.remove = append(d.remove, n)
		return
	}
	switch strings.ToLower(strings.TrimSpace(attrValue(n, "http-equiv"))) {
	case "content-type":
		d.remove = append(d.remove, n)
	case "content-security-policy":
		d.remove = append(d.remove, n)
		d.warn("", "<meta http-equiv=Content-Security-Policy> removed; a policy could block the embedded resources")
	case "refresh":
		d.metaRefresh(n)
	}
}

func (d *doc) metaRefresh(n *html.Node) {
	m := refreshContent.FindStringSubmatch(attrValue(n, "content"))
	if m == nil || m[2] == "" {
		return
	}
	seconds, _ := strconv.ParseFloat(m[1], 64)
	ref := d.resolve(m[2])
	var target string
	switch {
	case ref.Kind == resolve.File && ref.IsHTML && !d.p.opts.SinglePage:
		d.addLink(ref.Rel)
		target = "top.location.hash=" + jsString(Route(ref.Rel, ref.Query, ref.Fragment))
	case ref.Kind == resolve.External:
		target = "top.location.href=" + jsString(strings.TrimSpace(m[2]))
	default:
		d.bundleable(ref)
		return
	}
	script := &html.Node{Type: html.ElementNode, Data: "script", DataAtom: atom.Script}
	script.AppendChild(&html.Node{Type: html.TextNode, Data: "setTimeout(function(){" + target + "}," + strconv.FormatInt(int64(seconds*1000), 10) + ");"})
	d.replacements = append(d.replacements, replacement{old: n, new: script})
	d.info(m[2], "<meta http-equiv=refresh> converted to a script")
}

// styleElement rewrites the CSS text of a <style> element.
func (d *doc) styleElement(n *html.Node) {
	text := textContent(n)
	if text == "" {
		return
	}
	out := d.rewriteCSS(text, true)
	if out == text {
		return
	}
	for n.FirstChild != nil {
		n.RemoveChild(n.FirstChild)
	}
	n.AppendChild(&html.Node{Type: html.TextNode, Data: out})
}

var xmlProlog = regexp.MustCompile(`(?is)^\s*(?:<\?xml[^>]*\?>\s*)?(?:<!DOCTYPE[^>]*>\s*)?`)

// svgUse satisfies <use href="sprite.svg#id"> by inlining the sprite once
// and pointing the reference at the local copy.
func (d *doc) svgUse(n *html.Node) {
	a := svgHref(n)
	if a == nil || a.Val == "" {
		return
	}
	ref := d.resolve(a.Val)
	if ref.Kind != resolve.File {
		d.bundleable(ref)
		return
	}
	if ref.Fragment == "" {
		d.warn(a.Val, "<use> without a fragment identifier cannot be embedded; left unchanged")
		return
	}
	if !d.sprites[ref.Rel] {
		data, ok := d.readResource(ref)
		if !ok {
			return
		}
		text, _ := textenc.Decode(data, "utf-8")
		text = xmlProlog.ReplaceAllString(text, "")
		nodes, err := html.ParseFragment(strings.NewReader(text), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
		var svg *html.Node
		if err == nil {
			for _, c := range nodes {
				if c.Type == html.ElementNode && c.Namespace == "svg" && c.Data == "svg" {
					svg = c
					break
				}
			}
		}
		if svg == nil {
			d.warn(a.Val, "%s is not an SVG document; left unchanged", ref.Rel)
			return
		}
		const hidden = "position:absolute;width:0;height:0;overflow:hidden"
		if s := attrValue(svg, "style"); s != "" {
			setAttr(svg, "style", strings.TrimSuffix(strings.TrimSpace(s), ";")+";"+hidden)
		} else {
			setAttr(svg, "style", hidden)
		}
		setAttr(svg, "aria-hidden", "true")
		setAttr(svg, "data-furoshiki-sprite", ref.Rel)
		// References inside the sprite are relative to the sprite file.
		saved := d.base
		d.base = ref.Rel
		d.walk(svg)
		d.base = saved
		d.sprites[ref.Rel] = true
		d.spriteNodes = append(d.spriteNodes, svg)
	}
	a.Val = "#" + ref.Fragment
}

// finish applies the deferred structural changes and injects the bundle
// metadata: the UTF-8 declaration, the default link target, the runtime shim
// and inlined sprites.
func (d *doc) finish() {
	for _, r := range d.replacements {
		if r.old.Parent != nil {
			r.old.Parent.InsertBefore(r.new, r.old)
			r.old.Parent.RemoveChild(r.old)
		}
	}
	for _, n := range d.remove {
		if n.Parent != nil {
			n.Parent.RemoveChild(n)
		}
	}
	if d.head != nil {
		var inject []*html.Node
		meta := &html.Node{Type: html.ElementNode, Data: "meta", DataAtom: atom.Meta}
		setAttr(meta, "charset", "utf-8")
		inject = append(inject, meta)

		target := d.baseTarget
		if target == "" || strings.EqualFold(strings.TrimSpace(target), "_self") {
			target = "_top"
		}
		base := &html.Node{Type: html.ElementNode, Data: "base", DataAtom: atom.Base}
		setAttr(base, "target", target)
		inject = append(inject, base)

		if d.p.opts.Shim != "" {
			script := &html.Node{Type: html.ElementNode, Data: "script", DataAtom: atom.Script}
			setAttr(script, "data-furoshiki", "shim")
			script.AppendChild(&html.Node{Type: html.TextNode, Data: d.p.opts.Shim})
			inject = append(inject, script)
		}
		for i := len(inject) - 1; i >= 0; i-- {
			d.head.InsertBefore(inject[i], d.head.FirstChild)
		}
	}
	if d.htmlEl != nil {
		setAttr(d.htmlEl, "data-furoshiki-page", d.rel)
	}
	if d.body != nil {
		for i := len(d.spriteNodes) - 1; i >= 0; i-- {
			d.body.InsertBefore(d.spriteNodes[i], d.body.FirstChild)
		}
	}
}

// title returns the whitespace-collapsed text of the first <title>.
func (d *doc) title() string {
	var found *html.Node
	d.forEach(d.root, func(n *html.Node) {
		if found == nil && n.Type == html.ElementNode && n.DataAtom == atom.Title && n.Namespace == "" {
			found = n
		}
	})
	if found == nil {
		return ""
	}
	return strings.Join(strings.Fields(textContent(found)), " ")
}

// jsString encodes s as a JavaScript string literal that is also safe inside
// a <script> element.
func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
