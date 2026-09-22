// Package bundle crawls a static site from its entry page and writes the
// whole site as one HTML file.
//
// Starting at the entry document, every page reachable through links
// (<a>, <area>, <link rel=next|prev>, <meta http-equiv=refresh>) and frames
// is processed by package page; the resources they reference are collected
// in a package store Store; and package shell renders the final document.
// Pages that no link reaches are not included.
package bundle

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ukaji3/furoshiki/internal/diag"
	"github.com/ukaji3/furoshiki/internal/page"
	"github.com/ukaji3/furoshiki/internal/resolve"
	"github.com/ukaji3/furoshiki/internal/shell"
	"github.com/ukaji3/furoshiki/internal/store"
)

// DefaultEntry is the entry page used when Options.Entry is empty.
const DefaultEntry = "index.html"

// Options controls a build.
type Options struct {
	// Dir is the site root directory.
	Dir string
	// Entry is the site-relative path of the first page; empty means
	// DefaultEntry.
	Entry string
	// Excludes are patterns of site-relative paths that are never bundled;
	// see resolve.Resolver.Excluded for the syntax.
	Excludes []string
	// SinglePage bundles only the entry page and leaves links to other
	// pages unchanged.
	SinglePage bool
	// MaxResourceSize, when positive, is the largest file (in bytes) that
	// is embedded.
	MaxResourceSize int64
	// DefaultCharset is assumed for documents without an encoding
	// declaration; empty means UTF-8.
	DefaultCharset string
	// Generator identifies the producing program, for example
	// "furoshiki v1.0.0".
	Generator string
	// Diag receives diagnostics; nil discards them.
	Diag *diag.Collector
}

// Result summarises a build.
type Result struct {
	// Entry is the site-relative path of the entry page.
	Entry string
	// Title is the entry page's title.
	Title string
	// Pages lists the bundled pages in crawl order (breadth first from the
	// entry, links in document order).
	Pages []string
	// Resources is the number of distinct embedded resources.
	Resources int
	// ResourceBytes is the total size of the embedded resources before
	// encoding.
	ResourceBytes int64
	// Files lists the absolute paths of every input file that was read.
	Files []string
	// Bytes is the size of the written bundle.
	Bytes int64
}

// Build bundles the site described by opts and writes the bundle to w.
func Build(opts Options, w io.Writer) (*Result, error) {
	b, err := newBuilder(opts)
	if err != nil {
		return nil, err
	}
	if err := b.crawl(); err != nil {
		return nil, err
	}
	cw := &countingWriter{w: w}
	if err := b.render(cw); err != nil {
		return nil, err
	}
	b.result.Bytes = cw.n
	b.result.Files = b.files.Paths()
	return b.result, nil
}

// BuildFile bundles the site and writes the bundle to outPath atomically:
// the file is written next to its final location and renamed into place, so
// an interrupted build never leaves a truncated bundle. Writing over one of
// the site's own input files is refused.
func BuildFile(opts Options, outPath string) (*Result, error) {
	b, err := newBuilder(opts)
	if err != nil {
		return nil, err
	}
	if err := b.crawl(); err != nil {
		return nil, err
	}
	absOut, err := filepath.Abs(outPath)
	if err != nil {
		return nil, err
	}
	for _, in := range b.files.Paths() {
		if sameFile(in, absOut) {
			return nil, fmt.Errorf("output %s would overwrite an input file of the site", outPath)
		}
	}
	dir := filepath.Dir(absOut)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(absOut)+".*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create temporary output: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	cw := &countingWriter{w: tmp}
	if err := b.render(cw); err != nil {
		cleanup()
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return nil, fmt.Errorf("write %s: %w", outPath, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return nil, fmt.Errorf("write %s: %w", outPath, err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil { // #nosec G302 -- a bundle is a document meant to be shared, not a secret
		_ = os.Remove(tmpName)
		return nil, fmt.Errorf("write %s: %w", outPath, err)
	}
	if err := os.Rename(tmpName, absOut); err != nil {
		_ = os.Remove(tmpName)
		return nil, fmt.Errorf("write %s: %w", outPath, err)
	}
	b.result.Bytes = cw.n
	b.result.Files = b.files.Paths()
	return b.result, nil
}

type builder struct {
	opts     Options
	resolver *resolve.Resolver
	store    *store.Store
	files    *page.FileCache
	proc     *page.Processor
	pages    map[string]shell.Page
	lang     string
	result   *Result
}

func newBuilder(opts Options) (*builder, error) {
	if opts.Dir == "" {
		return nil, errors.New("no site directory given")
	}
	rs, err := resolve.New(opts.Dir, opts.Excludes)
	if err != nil {
		return nil, err
	}
	entry := strings.TrimSpace(opts.Entry)
	if entry == "" {
		entry = DefaultEntry
	}
	ref := rs.Resolve(entry, "")
	switch ref.Kind {
	case resolve.File:
	case resolve.Excluded:
		return nil, fmt.Errorf("entry page %s is excluded by a pattern", ref.Rel)
	case resolve.Missing:
		return nil, fmt.Errorf("entry page %s: %w", entry, ref.Err)
	default:
		return nil, fmt.Errorf("entry page %q is not a file inside %s", entry, opts.Dir)
	}
	b := &builder{
		opts:     opts,
		resolver: rs,
		store:    store.New(),
		files:    page.NewFileCache(),
		pages:    make(map[string]shell.Page),
		result:   &Result{Entry: ref.Rel},
	}
	b.proc = page.NewProcessor(page.Options{
		Resolver:        rs,
		Store:           b.store,
		Files:           b.files,
		Diag:            opts.Diag,
		MaxResourceSize: opts.MaxResourceSize,
		DefaultCharset:  opts.DefaultCharset,
		SinglePage:      opts.SinglePage,
		Shim:            shell.Shim(),
	})
	return b, nil
}

// crawl processes the entry page and, breadth first, every page it links to.
func (b *builder) crawl() error {
	queue := []string{b.result.Entry}
	seen := map[string]bool{b.result.Entry: true}
	for len(queue) > 0 {
		rel := queue[0]
		queue = queue[1:]
		res, err := b.proc.Process(rel)
		if err != nil {
			if rel == b.result.Entry {
				return fmt.Errorf("entry page: %w", err)
			}
			b.opts.Diag.Warnf(rel, "", "page skipped: %v", err)
			continue
		}
		b.pages[rel] = shell.Page{Title: res.Title, HTML: res.HTML}
		b.result.Pages = append(b.result.Pages, rel)
		if rel == b.result.Entry {
			b.result.Title = res.Title
			b.lang = res.Lang
		}
		for _, l := range res.Links {
			if !seen[l] {
				seen[l] = true
				queue = append(queue, l)
			}
		}
	}
	b.result.Resources = b.store.Len()
	b.result.ResourceBytes = b.store.Size()
	return nil
}

// render writes the shell document.
func (b *builder) render(w io.Writer) error {
	resources := make(map[string]shell.Resource, b.store.Len())
	for _, r := range b.store.Resources() {
		if r.Text {
			resources[r.Key] = shell.Resource{IsText: true, MIME: r.MIME, Text: string(r.Data)}
		} else {
			resources[r.Key] = shell.Resource{DataURL: r.DataURL()}
		}
	}
	title := b.result.Title
	if title == "" {
		// Fall back to the site directory's name, then to the entry file.
		title = filepath.Base(b.resolver.Root())
		if title == "" || title == "." || title == string(filepath.Separator) {
			title = path.Base(b.result.Entry)
		}
	}
	return shell.Render(w, shell.Input{
		Title:     title,
		Lang:      b.lang,
		Generator: b.opts.Generator,
		Payload: &shell.Payload{
			Generator: b.opts.Generator,
			Entry:     b.result.Entry,
			Pages:     b.pages,
			Resources: resources,
		},
	})
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// sameFile reports whether a and b name the same existing file, or the same
// path when b does not exist yet.
func sameFile(a, b string) bool {
	if a == b {
		return true
	}
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}
