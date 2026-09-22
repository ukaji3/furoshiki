//go:build e2e

// Package e2e drives a real browser against a bundle of testdata/site1.
//
// The tests need Google Chrome or Chromium on PATH (or FUROSHIKI_CHROME set
// to the executable) and are only compiled with -tags e2e:
//
//	go test -tags e2e ./e2e/
package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/ukaji3/furoshiki/internal/bundle"
)

var bundleURL string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "furoshiki-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out := filepath.Join(dir, "site1.html")
	if _, err := bundle.BuildFile(bundle.Options{
		Dir:       "../testdata/site1",
		Excludes:  []string{"drafts"},
		Generator: "furoshiki e2e",
	}, out); err != nil {
		fmt.Fprintln(os.Stderr, "build bundle:", err)
		os.Exit(1)
	}
	bundleURL = "file://" + filepath.ToSlash(out)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// findChrome locates the browser executable. FUROSHIKI_CHROME wins; otherwise
// well-known names are tried in order. Snap-packaged browsers are used only
// as a last resort: their confinement hides the temporary directory the
// bundle is written to (file:// loads fail with ERR_FILE_NOT_FOUND).
func findChrome(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("FUROSHIKI_CHROME"); p != "" {
		return p
	}
	var snap string
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium-browser", "chromium", "chrome", "headless_shell", "headless-shell"} {
		p, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		if strings.HasPrefix(p, "/snap/") {
			if snap == "" {
				snap = p
			}
			continue
		}
		return p
	}
	if snap != "" {
		t.Logf("only a snap-packaged browser was found (%s); set FUROSHIKI_CHROME if file:// loads fail", snap)
		return snap
	}
	t.Fatal("no Chrome or Chromium executable found; set FUROSHIKI_CHROME")
	return ""
}

// browser returns a chromedp context with a fresh headless browser.
func browser(t *testing.T) context.Context {
	t.Helper()
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts,
		chromedp.ExecPath(findChrome(t)),
		chromedp.Flag("allow-file-access-from-files", true),
		chromedp.WindowSize(1024, 768),
	)
	if os.Getenv("CI") != "" || os.Getuid() == 0 {
		opts = append(opts, chromedp.NoSandbox)
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancel := chromedp.NewContext(allocCtx)
	ctx, cancelTimeout := context.WithTimeout(ctx, 60*time.Second)
	t.Cleanup(func() {
		cancelTimeout()
		cancel()
		cancelAlloc()
	})
	// Start the browser eagerly so a missing executable fails clearly.
	if err := chromedp.Run(ctx); err != nil {
		t.Fatalf("cannot start browser: %v", err)
	}
	return ctx
}

// frameJS is JavaScript evaluated in the top document that returns the
// embedded page's document, once it is loaded.
const frameJS = `document.querySelector('#furoshiki-view > iframe').contentDocument`

// waitPage waits until the frame shows the page rel and its scripts ran.
func waitPage(rel string) chromedp.Action {
	return chromedp.Poll(fmt.Sprintf(`(() => {
		const f = document.querySelector('#furoshiki-view > iframe');
		if (!f || !f.contentDocument) return false;
		const d = f.contentDocument;
		return d.readyState === 'complete' && d.documentElement.getAttribute('data-furoshiki-page') === %q;
	})()`, rel), nil, chromedp.WithPollingInterval(20*time.Millisecond), chromedp.WithPollingTimeout(15*time.Second))
}

func evalString(ctx context.Context, expr string) (string, error) {
	var s string
	err := chromedp.Run(ctx, chromedp.Evaluate(expr, &s))
	return s, err
}

func mustEval(ctx context.Context, t *testing.T, expr string) string {
	t.Helper()
	s, err := evalString(ctx, expr)
	if err != nil {
		t.Fatalf("evaluate %s: %v", expr, err)
	}
	return s
}

// clickInFrame clicks the element with the given id inside the frame.
func clickInFrame(ctx context.Context, id string) error {
	return chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(`%s.getElementById(%q).click()`, frameJS, id), nil))
}

func TestEntryPageRendersWithResources(t *testing.T) {
	ctx := browser(t)
	if err := chromedp.Run(ctx, chromedp.Navigate(bundleURL), waitPage("index.html")); err != nil {
		t.Fatal(err)
	}
	var hash, title string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`location.hash`, &hash), chromedp.Title(&title)); err != nil {
		t.Fatal(err)
	}
	if hash != "#/index.html" {
		t.Errorf("hash = %q, want #/index.html (entry route written into the URL)", hash)
	}
	if title != "Site1 ホーム" {
		t.Errorf("title = %q", title)
	}

	// External style sheet (with @import) applied: h1 colour comes from
	// base.css through site.css.
	color := mustEval(ctx, t, frameJS+`.defaultView.getComputedStyle(`+frameJS+`.getElementById('heading')).color`)
	if color != "rgb(200, 30, 30)" {
		t.Errorf("h1 color = %q, want rgb(200, 30, 30) from base.css via @import", color)
	}
	// Inline <style> with url() → background image loads.
	bg := mustEval(ctx, t, frameJS+`.defaultView.getComputedStyle(`+frameJS+`.getElementById('hero')).backgroundImage`)
	if !strings.HasPrefix(bg, `url("data:image/png;base64,`) {
		t.Errorf("hero background = %.60q, want data: URL", bg)
	}
	// Shared and page-specific scripts ran.
	if got := mustEval(ctx, t, frameJS+`.getElementById('js-status').textContent`); got != "js ran on index.html" {
		t.Errorf("js-status = %q", got)
	}
	if got := mustEval(ctx, t, frameJS+`.getElementById('home-status').textContent`); got != "home js ran" {
		t.Errorf("home-status = %q", got)
	}
	// Image decoded with its real dimensions.
	var w, h int
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(frameJS+`.getElementById('logo').naturalWidth`, &w),
		chromedp.Evaluate(frameJS+`.getElementById('logo').naturalHeight`, &h),
	); err != nil {
		t.Fatal(err)
	}
	if w != 4 || h != 4 {
		t.Errorf("logo natural size = %dx%d, want 4x4", w, h)
	}
	// Inlined sprite: the <use> now points at a local symbol that exists.
	if got := mustEval(ctx, t, frameJS+`.querySelector('use').getAttribute('href')`); got != "#dot" {
		t.Errorf("use href = %q", got)
	}
	if got := mustEval(ctx, t, `String(!!`+frameJS+`.getElementById('dot'))`); got != "true" {
		t.Error("sprite symbol #dot not present in the page")
	}
	// Placeholders never reach the browser.
	if got := mustEval(ctx, t, `String(`+frameJS+`.documentElement.outerHTML.includes('furoshiki-res:'))`); got != "false" {
		t.Error("unexpanded placeholder found in the displayed page")
	}
	// Download link: data URL with a download attribute.
	if got := mustEval(ctx, t, frameJS+`.getElementById('nav-download').getAttribute('download')`); got != "notes.txt" {
		t.Errorf("download attribute = %q", got)
	}
	if got := mustEval(ctx, t, frameJS+`.getElementById('nav-download').href`); !strings.HasPrefix(got, "data:text/plain;base64,") {
		t.Errorf("download href = %.50q", got)
	}
	// External link opens in the top window.
	if got := mustEval(ctx, t, frameJS+`.getElementById('nav-external').target || `+frameJS+`.querySelector('base').target`); got != "_top" {
		t.Errorf("effective target for external link = %q", got)
	}
}

func TestNavigationHistoryAndFragments(t *testing.T) {
	ctx := browser(t)
	if err := chromedp.Run(ctx, chromedp.Navigate(bundleURL), waitPage("index.html")); err != nil {
		t.Fatal(err)
	}

	// Click a rewritten link.
	if err := clickInFrame(ctx, "nav-about"); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(ctx, waitPage("about.html")); err != nil {
		t.Fatal("about.html not shown:", err)
	}
	if got := mustEval(ctx, t, `location.hash`); got != "#/about.html" {
		t.Errorf("hash = %q", got)
	}
	if got := mustEval(ctx, t, `document.title`); got != "About - Site1" {
		t.Errorf("title = %q", got)
	}
	if got := mustEval(ctx, t, frameJS+`.getElementById('js-status').textContent`); got != "js ran on about.html" {
		t.Errorf("shared script did not run on about.html: %q", got)
	}

	// Scroll down, then follow a link with a fragment to another page.
	if err := chromedp.Run(ctx, chromedp.Evaluate(frameJS+`.defaultView.scrollTo(0, 1200)`, nil)); err != nil {
		t.Fatal(err)
	}
	if err := clickInFrame(ctx, "to-guide"); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(ctx, waitPage("docs/guide.html")); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(ctx, t, `location.hash`); got != "#/docs/guide.html" {
		t.Errorf("hash = %q", got)
	}
	var y float64
	if err := chromedp.Run(ctx, chromedp.Evaluate(frameJS+`.defaultView.scrollY`, &y)); err != nil {
		t.Fatal(err)
	}
	if y != 0 {
		t.Errorf("new page should start at the top, scrollY = %v", y)
	}

	// Back restores about.html and its scroll position.
	if err := chromedp.Run(ctx, chromedp.NavigateBack(), waitPage("about.html")); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(ctx, chromedp.Poll(frameJS+`.defaultView.scrollY > 1000`, nil, chromedp.WithPollingTimeout(5*time.Second))); err != nil {
		y2, _ := evalString(ctx, `String(`+frameJS+`.defaultView.scrollY)`)
		t.Errorf("scroll position not restored after Back: scrollY = %s", y2)
	}
	// Back again reaches the entry page; Forward returns to about.html.
	if err := chromedp.Run(ctx, chromedp.NavigateBack(), waitPage("index.html")); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(ctx, chromedp.NavigateForward(), waitPage("about.html")); err != nil {
		t.Fatal(err)
	}

	// Fragment link to another page scrolls to the anchor.
	if err := chromedp.Run(ctx, chromedp.Navigate(bundleURL+"#/index.html"), waitPage("index.html")); err != nil {
		t.Fatal(err)
	}
	if err := clickInFrame(ctx, "nav-guide"); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(ctx, waitPage("docs/guide.html")); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(ctx, t, `location.hash`); got != "#/docs/guide.html#install" {
		t.Errorf("hash = %q", got)
	}
	if err := chromedp.Run(ctx, chromedp.Poll(frameJS+`.defaultView.scrollY > 2000`, nil, chromedp.WithPollingTimeout(5*time.Second))); err != nil {
		y2, _ := evalString(ctx, `String(`+frameJS+`.defaultView.scrollY)`)
		t.Errorf("not scrolled to #install: scrollY = %s", y2)
	}

	// Same-page fragment: no reload, just a scroll.
	if err := chromedp.Run(ctx, chromedp.Navigate(bundleURL+"#/index.html"), waitPage("index.html")); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(frameJS+`.defaultView.__marker = 1`, nil)); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(`location.hash = '#/index.html#bottom'`, nil), chromedp.Sleep(200*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(ctx, t, `String(`+frameJS+`.defaultView.__marker)`); got != "1" {
		t.Error("same-document fragment navigation reloaded the frame")
	}
	if err := chromedp.Run(ctx, chromedp.Poll(frameJS+`.defaultView.scrollY > 2500`, nil, chromedp.WithPollingTimeout(5*time.Second))); err != nil {
		t.Error("not scrolled to #bottom")
	}

	// Directory link resolves to index.html of the directory.
	if err := clickInFrame(ctx, "nav-docs"); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(ctx, waitPage("docs/index.html")); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(ctx, t, `location.hash`); got != "#/docs/index.html" {
		t.Errorf("hash = %q", got)
	}
	// Root-relative and parent links from a sub-directory.
	if err := clickInFrame(ctx, "to-about"); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(ctx, waitPage("about.html")); err != nil {
		t.Fatal(err)
	}
}

func TestDynamicLinksAreRouted(t *testing.T) {
	ctx := browser(t)
	if err := chromedp.Run(ctx, chromedp.Navigate(bundleURL+"#/about.html"), waitPage("about.html")); err != nil {
		t.Fatal(err)
	}
	// The link was created by the page's own script and points at a
	// relative URL the bundler never saw; the shim must route it.
	if got := mustEval(ctx, t, frameJS+`.getElementById('dynamic-link').getAttribute('href')`); got != "docs/guide.html" {
		t.Fatalf("dynamic link href = %q", got)
	}
	if err := clickInFrame(ctx, "dynamic-link"); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(ctx, waitPage("docs/guide.html")); err != nil {
		t.Fatal("dynamic link was not routed:", err)
	}
	if got := mustEval(ctx, t, `location.hash`); got != "#/docs/guide.html" {
		t.Errorf("hash = %q", got)
	}
}

func TestMissingPageAndBadRoutes(t *testing.T) {
	ctx := browser(t)
	if err := chromedp.Run(ctx, chromedp.Navigate(bundleURL+"#/nope.html")); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(ctx, chromedp.Poll(frameJS+` && `+frameJS+`.title === 'Page not found'`, nil, chromedp.WithPollingTimeout(10*time.Second))); err != nil {
		t.Fatal("not-found page not shown:", err)
	}
	if got := mustEval(ctx, t, frameJS+`.body.textContent`); !strings.Contains(got, "nope.html") {
		t.Errorf("not-found page text = %q", got)
	}
	// The link back to the start page works.
	if err := chromedp.Run(ctx, chromedp.Evaluate(frameJS+`.querySelector('a').click()`, nil), waitPage("index.html")); err != nil {
		t.Fatal(err)
	}

	// A fragment that is not a route falls back to the entry page.
	if err := chromedp.Run(ctx, chromedp.Navigate(bundleURL+"#random"), waitPage("index.html")); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(ctx, t, `location.hash`); got != "#/index.html" {
		t.Errorf("hash = %q", got)
	}
	// The unreachable page was not bundled.
	if err := chromedp.Run(ctx, chromedp.Navigate(bundleURL+"#/unreachable.html"), chromedp.Poll(frameJS+` && `+frameJS+`.title === 'Page not found'`, nil, chromedp.WithPollingTimeout(10*time.Second))); err != nil {
		t.Error("unreachable.html should not be in the bundle")
	}
}

func TestShiftJISPage(t *testing.T) {
	ctx := browser(t)
	if err := chromedp.Run(ctx, chromedp.Navigate(bundleURL+"#/sjis.html"), waitPage("sjis.html")); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(ctx, t, frameJS+`.getElementById('text').textContent`); got != "シフトJISで書かれています。" {
		t.Errorf("text = %q", got)
	}
	if got := mustEval(ctx, t, `document.title`); got != "日本語のページ" {
		t.Errorf("title = %q", got)
	}
	if err := clickInFrame(ctx, "home"); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(ctx, waitPage("index.html")); err != nil {
		t.Fatal(err)
	}
}

func TestNestedFrame(t *testing.T) {
	ctx := browser(t)
	if err := chromedp.Run(ctx, chromedp.Navigate(bundleURL+"#/frame.html"), waitPage("frame.html")); err != nil {
		t.Fatal(err)
	}
	inner := frameJS + `.getElementById('inner').contentDocument`
	if err := chromedp.Run(ctx, chromedp.Poll(inner+` && `+inner+`.readyState === 'complete' && `+inner+`.getElementById('js-status') && `+inner+`.getElementById('js-status').textContent.startsWith('js ran')`, nil, chromedp.WithPollingTimeout(10*time.Second))); err != nil {
		t.Fatal("nested frame did not load or run its script:", err)
	}
	if got := mustEval(ctx, t, inner+`.documentElement.getAttribute('data-furoshiki-page')`); got != "docs/guide.html" {
		t.Errorf("nested page = %q", got)
	}
	// A link inside the nested frame navigates the whole bundle.
	if err := chromedp.Run(ctx, chromedp.Evaluate(inner+`.getElementById('home').click()`, nil), waitPage("index.html")); err != nil {
		t.Fatal("link inside nested frame did not route the bundle:", err)
	}
}

func TestPrintPreparation(t *testing.T) {
	ctx := browser(t)
	if err := chromedp.Run(ctx, chromedp.Navigate(bundleURL), waitPage("index.html")); err != nil {
		t.Fatal(err)
	}
	// Simulate the browser's print lifecycle on the top document: the frame
	// must grow to the page's full height and shrink back afterwards.
	var before, during, after string
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.querySelector('#furoshiki-view > iframe').style.height`, &before),
		chromedp.Evaluate(`window.dispatchEvent(new Event('beforeprint')); document.querySelector('#furoshiki-view > iframe').style.height`, &during),
		chromedp.Evaluate(`window.dispatchEvent(new Event('afterprint')); document.querySelector('#furoshiki-view > iframe').style.height`, &after),
	); err != nil {
		t.Fatal(err)
	}
	if before != "" || after != "" {
		t.Errorf("frame height before/after = %q/%q, want unset", before, after)
	}
	if !strings.HasSuffix(during, "px") || during == "0px" {
		t.Errorf("frame height during print = %q, want full page height", during)
	}
	var n int
	if err := chromedp.Run(ctx, chromedp.Evaluate(`parseInt(`+strings.TrimSuffix(fmt.Sprintf("%q", during), `"`)+`", 10)`, &n)); err == nil && n < 3000 {
		t.Errorf("frame height during print = %d, want at least the 3000px spacer", n)
	}
	// Ctrl+P inside the page calls window.print of the page, not the shell.
	if err := chromedp.Run(ctx, chromedp.Evaluate(frameJS+`.defaultView.print = function(){ `+frameJS+`.defaultView.__printed = true; }; `+frameJS+`.dispatchEvent(new KeyboardEvent('keydown', {key: 'p', ctrlKey: true, bubbles: true, cancelable: true}))`, nil)); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(ctx, t, `String(`+frameJS+`.defaultView.__printed === true)`); got != "true" {
		t.Error("Ctrl+P inside the page did not print the page")
	}
	// Ctrl+P on the shell prints the frame too.
	if err := chromedp.Run(ctx, chromedp.Evaluate(frameJS+`.defaultView.__printed = false; document.dispatchEvent(new KeyboardEvent('keydown', {key: 'p', ctrlKey: true, bubbles: true, cancelable: true}))`, nil)); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(ctx, t, `String(`+frameJS+`.defaultView.__printed === true)`); got != "true" {
		t.Error("Ctrl+P on the shell did not print the page")
	}
}
