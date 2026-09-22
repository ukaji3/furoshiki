// Package page transforms one HTML document of a site into the form stored in
// a bundle.
//
// For each document the processor decodes the bytes to UTF-8, parses them
// with golang.org/x/net/html, and walks the DOM:
//
//   - links to other pages of the site become hash routes ("#/docs/a.html")
//     that the bundle's runtime resolves, and every other link is made to
//     target the top window;
//   - references to style sheets, scripts, images, fonts, media, favicons
//     and downloadable files become placeholders (see package store) that the
//     runtime replaces with data: URLs;
//   - url() and @import references inside style sheets, <style> elements and
//     style attributes are rewritten the same way, recursively;
//   - <iframe> and <frame> elements that embed pages of the site receive the
//     embedded page as srcdoc;
//   - <use> references to external SVG sprites are satisfied by inlining the
//     sprite into the document;
//   - encoding declarations, <base> and Content-Security-Policy metadata that
//     would be wrong or harmful inside the bundle are normalised or removed.
//
// The resulting HTML is a complete, self-describing document (apart from the
// placeholders) that the runtime loads into an <iframe srcdoc>.
package page

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/net/html"

	"github.com/ukaji3/furoshiki/internal/css"
	"github.com/ukaji3/furoshiki/internal/diag"
	"github.com/ukaji3/furoshiki/internal/resolve"
	"github.com/ukaji3/furoshiki/internal/store"
	"github.com/ukaji3/furoshiki/internal/textenc"
)

// maxFrameDepth bounds the nesting of <iframe> documents embedded as srcdoc.
const maxFrameDepth = 8

// Options configures a Processor. Resolver, Store and Files are required.
type Options struct {
	Resolver *resolve.Resolver
	Store    *store.Store
	Files    *FileCache
	// Diag receives warnings and notes; nil discards them.
	Diag *diag.Collector
	// MaxResourceSize, when positive, is the largest file (in bytes) that is
	// embedded. Larger files are left as external references with a warning.
	MaxResourceSize int64
	// DefaultCharset is the encoding label assumed for documents that do not
	// declare one. Empty means UTF-8.
	DefaultCharset string
	// SinglePage leaves links to other pages of the site unchanged instead
	// of turning them into routes, and does not report them as Links.
	SinglePage bool
	// Shim is JavaScript injected at the start of every document's <head>.
	// It must not contain the sequence "</script". Empty injects nothing.
	Shim string
}

// Result is the outcome of processing one document.
type Result struct {
	// Rel is the site-relative path of the document.
	Rel string
	// Title is the text of the first <title> element, whitespace-collapsed;
	// empty when there is none.
	Title string
	// Lang is the lang attribute of the <html> element.
	Lang string
	// HTML is the transformed document, serialised.
	HTML string
	// Links lists the site-relative paths of the HTML pages the document
	// links to (through <a>, <area>, <link rel=next/prev> and
	// <meta http-equiv=refresh>), including those reached through embedded
	// frames, without duplicates and in document order.
	Links []string
}

// Processor transforms documents. Style sheets and scripts are processed at
// most once per Processor regardless of how many documents reference them.
type Processor struct {
	opts Options

	sheets      map[string]string // css rel → placeholder
	sheetActive map[string]bool   // css rel currently being processed (cycle guard)
	scripts     map[string]scriptEntry
}

type scriptEntry struct {
	placeholder string
	text        string
}

// NewProcessor returns a Processor for opts.
func NewProcessor(opts Options) *Processor {
	if opts.DefaultCharset == "" {
		opts.DefaultCharset = "utf-8"
	}
	if opts.Files == nil {
		opts.Files = NewFileCache()
	}
	return &Processor{
		opts:        opts,
		sheets:      make(map[string]string),
		sheetActive: make(map[string]bool),
		scripts:     make(map[string]scriptEntry),
	}
}

// Process transforms the document at the site-relative path rel.
func (p *Processor) Process(rel string) (*Result, error) {
	return p.process(rel, nil)
}

func (p *Processor) process(rel string, ancestors []string) (*Result, error) {
	data, err := p.opts.Files.Read(p.opts.Resolver.Abs(rel))
	if err != nil {
		return nil, err
	}
	text, name := p.decodeHTML(rel, data)
	root, err := html.ParseWithOptions(strings.NewReader(text), html.ParseOptionEnableScripting(false))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, err)
	}
	d := newDoc(p, rel, name, root, ancestors)
	d.transform()
	var buf bytes.Buffer
	if err := html.Render(&buf, root); err != nil {
		return nil, fmt.Errorf("render %s: %w", rel, err)
	}
	return &Result{
		Rel:   rel,
		Title: d.title(),
		Lang:  attrValue(d.htmlEl, "lang"),
		HTML:  buf.String(),
		Links: d.links,
	}, nil
}

// decodeHTML decodes a document, honouring a byte order mark, a <meta>
// declaration, and finally the configured default.
func (p *Processor) decodeHTML(rel string, data []byte) (text, name string) {
	var labels []string
	if l := textenc.SniffHTML(data); l != "" {
		if _, _, ok := textenc.Lookup(l); ok {
			labels = append(labels, l)
		} else {
			p.opts.Diag.Warnf(rel, "", "unknown charset %q declared; assuming %s", l, p.opts.DefaultCharset)
		}
	}
	labels = append(labels, p.opts.DefaultCharset)
	text, name = textenc.Decode(data, labels...)
	if name != "utf-8" {
		p.opts.Diag.Infof(rel, "", "decoded from %s", name)
	}
	return text, name
}

// ctx carries what is needed to resolve and embed references from one
// location: a page, or a style sheet with its own base.
type ctx struct {
	p *Processor
	// page attributes diagnostics.
	page string
	// base is passed to Resolver.Resolve.
	base string
	// baseExternal is true when the document declared an external <base>;
	// every relative reference is then external too.
	baseExternal bool
	// charset is the encoding label of the referring document, used as the
	// fallback for text resources it references.
	charset string
}

func (c *ctx) warn(ref, format string, args ...any) {
	c.p.opts.Diag.Warnf(c.page, ref, format, args...)
}

func (c *ctx) info(ref, format string, args ...any) {
	c.p.opts.Diag.Infof(c.page, ref, format, args...)
}

func (c *ctx) resolve(raw string) resolve.Ref {
	if c.baseExternal {
		ref := c.p.opts.Resolver.Resolve(raw, c.base)
		if ref.Kind != resolve.Empty && ref.Kind != resolve.Fragment {
			ref.Kind = resolve.External
		}
		return ref
	}
	return c.p.opts.Resolver.Resolve(raw, c.base)
}

// bundleable reports whether ref denotes a file that can be embedded, and
// records a diagnostic for the kinds that cannot be embedded but were meant
// to be files.
func (c *ctx) bundleable(ref resolve.Ref) bool {
	switch ref.Kind {
	case resolve.File:
		return true
	case resolve.Missing:
		if ref.Err != nil && os.IsNotExist(ref.Err) {
			c.warn(ref.Raw, "file not found (%s)", ref.Rel)
		} else {
			c.warn(ref.Raw, "cannot use file %s: %v", ref.Rel, ref.Err)
		}
	case resolve.Outside:
		c.warn(ref.Raw, "reference points outside the site root; left unchanged")
	case resolve.Invalid:
		c.warn(ref.Raw, "invalid URL (%v); left unchanged", ref.Err)
	case resolve.Excluded:
		c.info(ref.Raw, "excluded by pattern; left unchanged")
	}
	return false
}

// readResource reads the file behind ref, enforcing the size limit.
func (c *ctx) readResource(ref resolve.Ref) ([]byte, bool) {
	data, err := c.p.opts.Files.Read(ref.Abs)
	if err != nil {
		c.warn(ref.Raw, "cannot read %s: %v", ref.Rel, err)
		return nil, false
	}
	if limit := c.p.opts.MaxResourceSize; limit > 0 && int64(len(data)) > limit {
		c.warn(ref.Raw, "not embedded: %d bytes exceeds the resource size limit of %d bytes", len(data), limit)
		return nil, false
	}
	return data, true
}

// binaryResource embeds the file behind ref as a binary resource and returns
// its placeholder, followed by the reference's fragment when it has one
// (fragments select SVG views, filters and media ranges and stay meaningful
// on a data: URL).
func (c *ctx) binaryResource(ref resolve.Ref) (string, bool) {
	data, ok := c.readResource(ref)
	if !ok {
		return "", false
	}
	r := c.p.opts.Store.Add(ref.Rel, resolve.MIMEType(ref.Rel, data), data, false)
	if ref.Fragment != "" {
		return r.Placeholder() + "#" + ref.Fragment, true
	}
	return r.Placeholder(), true
}

// resourceRef resolves raw and embeds it as a binary resource.
func (c *ctx) resourceRef(raw string) (string, bool) {
	ref := c.resolve(raw)
	if !c.bundleable(ref) {
		return "", false
	}
	return c.binaryResource(ref)
}

// styleSheet embeds the style sheet behind ref as a text resource, rewriting
// the references inside it relative to the sheet's own location, and returns
// its placeholder. charsetAttr is the charset attribute of the referring
// element, if any.
func (c *ctx) styleSheet(ref resolve.Ref, charsetAttr string) (string, bool) {
	p := c.p
	if ph, ok := p.sheets[ref.Rel]; ok {
		return ph, true
	}
	if p.sheetActive[ref.Rel] {
		c.warn(ref.Raw, "circular @import of %s; left unchanged", ref.Rel)
		return "", false
	}
	data, ok := c.readResource(ref)
	if !ok {
		return "", false
	}
	text, name := css.Decode(data, charsetAttr, c.charset)
	if name != "utf-8" {
		p.opts.Diag.Infof(ref.Rel, "", "decoded from %s", name)
	}
	p.sheetActive[ref.Rel] = true
	sub := &ctx{p: p, page: ref.Rel, base: ref.Rel, charset: name}
	out := sub.rewriteCSS(text, true)
	delete(p.sheetActive, ref.Rel)

	r := p.opts.Store.Add(ref.Rel, "text/css", []byte(out), true)
	p.sheets[ref.Rel] = r.Placeholder()
	return r.Placeholder(), true
}

// script embeds the script behind ref as a text resource (decoded to UTF-8)
// and returns its placeholder together with the decoded text.
func (c *ctx) script(ref resolve.Ref, charsetAttr string) (placeholder, text string, ok bool) {
	p := c.p
	if e, ok := p.scripts[ref.Rel]; ok {
		return e.placeholder, e.text, true
	}
	data, ok := c.readResource(ref)
	if !ok {
		return "", "", false
	}
	text, name := textenc.Decode(data, charsetAttr, c.charset)
	if name != "utf-8" {
		p.opts.Diag.Infof(ref.Rel, "", "decoded from %s", name)
	}
	r := p.opts.Store.Add(ref.Rel, "text/javascript", []byte(text), true)
	p.scripts[ref.Rel] = scriptEntry{placeholder: r.Placeholder(), text: text}
	return r.Placeholder(), text, true
}

// rewriteCSS rewrites url(), image-set() and (when allowImport is set)
// @import references in CSS text relative to c.
func (c *ctx) rewriteCSS(text string, allowImport bool) string {
	lower := strings.ToLower(text)
	hasRef := strings.Contains(lower, "url(") || strings.Contains(lower, "image-set(") ||
		(allowImport && strings.Contains(lower, "@import"))
	if !hasRef {
		return text
	}
	return string(css.Rewrite([]byte(text), &cssHandler{ctx: c, allowImport: allowImport}))
}

type cssHandler struct {
	*ctx
	allowImport bool
}

func (h *cssHandler) URL(raw string) (string, bool) {
	return h.resourceRef(raw)
}

func (h *cssHandler) Import(raw string) (string, bool) {
	if !h.allowImport {
		return "", false
	}
	ref := h.resolve(raw)
	if !h.bundleable(ref) {
		return "", false
	}
	return h.styleSheet(ref, "")
}

// FileCache reads files at most once and remembers which files were read.
type FileCache struct {
	mu    sync.Mutex
	files map[string][]byte
	errs  map[string]error
}

// NewFileCache returns an empty FileCache.
func NewFileCache() *FileCache {
	return &FileCache{files: make(map[string][]byte), errs: make(map[string]error)}
}

// Read returns the contents of the file at the absolute path abs.
func (c *FileCache) Read(abs string) ([]byte, error) {
	abs = filepath.Clean(abs)
	c.mu.Lock()
	defer c.mu.Unlock()
	if data, ok := c.files[abs]; ok {
		return data, nil
	}
	if err, ok := c.errs[abs]; ok {
		return nil, err
	}
	data, err := os.ReadFile(abs) // #nosec G304 -- reading the site's own files is the purpose of the tool; paths are confined to the site root by package resolve
	if err != nil {
		c.errs[abs] = err
		return nil, err
	}
	c.files[abs] = data
	return data, nil
}

// Paths returns the absolute paths of all files read so far, sorted.
func (c *FileCache) Paths() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.files))
	for p := range c.files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
