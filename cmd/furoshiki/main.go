// Command furoshiki bundles a static web site into a single HTML file.
//
// Usage:
//
//	furoshiki [options] <site-dir>
//
// The entry page (index.html by default) and every page reachable from it
// through links are wrapped, together with their style sheets, scripts,
// images, fonts and other files, into one self-contained HTML document.
// Navigation between the pages keeps working through the fragment of the
// bundle's URL (bundle.html#/docs/guide.html).
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/ukaji3/furoshiki/internal/bundle"
	"github.com/ukaji3/furoshiki/internal/diag"
)

// version is set by the linker (-ldflags "-X main.version=v1.2.3") in
// release builds; otherwise it is derived from the module's build info.
var version = ""

// Exit codes.
const (
	exitOK      = 0
	exitFailure = 1 // the bundle could not be built, or --strict found warnings
	exitUsage   = 2 // invalid command line
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// stringList collects repeated flag values.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// sizeFlag parses byte sizes with optional K/M/G suffixes.
type sizeFlag int64

func (s *sizeFlag) String() string { return strconv.FormatInt(int64(*s), 10) }

func (s *sizeFlag) Set(v string) error {
	n, err := parseSize(v)
	if err != nil {
		return err
	}
	*s = sizeFlag(n)
	return nil
}

func parseSize(v string) (int64, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, errors.New("empty size")
	}
	mult := int64(1)
	upper := strings.ToUpper(v)
	switch {
	case strings.HasSuffix(upper, "K"), strings.HasSuffix(upper, "KB"), strings.HasSuffix(upper, "KIB"):
		mult = 1 << 10
	case strings.HasSuffix(upper, "M"), strings.HasSuffix(upper, "MB"), strings.HasSuffix(upper, "MIB"):
		mult = 1 << 20
	case strings.HasSuffix(upper, "G"), strings.HasSuffix(upper, "GB"), strings.HasSuffix(upper, "GIB"):
		mult = 1 << 30
	}
	num := strings.TrimRight(upper, "KMGIB")
	n, err := strconv.ParseInt(strings.TrimSpace(num), 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid size %q", v)
	}
	return n * mult, nil
}

func usage(fs *flag.FlagSet, w io.Writer) {
	fmt.Fprintf(w, `Usage: furoshiki [options] <site-dir>

Bundle a static web site into a single HTML file. The entry page and every
page reachable from it through links are included, together with the style
sheets, scripts, images, fonts and other files they reference.

Options:
`)
	fs.SetOutput(w)
	fs.PrintDefaults()
	fmt.Fprintf(w, `
Exit status:
  0  success
  1  the bundle could not be built, or --strict was given and warnings occurred
  2  invalid command line

Examples:
  furoshiki ./site                    # writes ./site.html
  furoshiki -o docs.html -e start.html ./site
  furoshiki --exclude drafts --exclude '*.bak' ./site
  furoshiki -o - ./site > site.html   # write to standard output
`)
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("furoshiki", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		out         string
		entry       string
		excludes    stringList
		singlePage  bool
		maxSize     sizeFlag
		charset     string
		strict      bool
		quiet       bool
		verbose     bool
		showVersion bool
	)
	fs.StringVar(&out, "o", "", "output file; default is <site-dir name>.html in the current directory; \"-\" writes to standard output")
	fs.StringVar(&out, "output", "", "same as -o")
	fs.StringVar(&entry, "e", bundle.DefaultEntry, "entry page, relative to <site-dir>")
	fs.StringVar(&entry, "entry", bundle.DefaultEntry, "same as -e")
	fs.Var(&excludes, "exclude", "glob pattern of paths (relative to <site-dir>) to leave out; may be repeated. A pattern without \"/\" matches any path segment, so \"drafts\" excludes the whole drafts/ tree")
	fs.BoolVar(&singlePage, "single-page", false, "bundle only the entry page; links to other pages are left unchanged")
	fs.Var(&maxSize, "max-resource-size", "largest file to embed, in bytes (suffixes K, M, G allowed); larger files stay external references and produce a warning. 0 means no limit")
	fs.StringVar(&charset, "charset", "", "encoding assumed for documents without an encoding declaration (default utf-8), e.g. shift_jis")
	fs.BoolVar(&strict, "strict", false, "treat warnings as errors: exit with status 1 if any warning was reported")
	fs.BoolVar(&quiet, "q", false, "suppress warnings and the summary; errors are still reported")
	fs.BoolVar(&quiet, "quiet", false, "same as -q")
	fs.BoolVar(&verbose, "v", false, "also print informational notes and the list of bundled pages")
	fs.BoolVar(&verbose, "verbose", false, "same as -v")
	fs.BoolVar(&showVersion, "version", false, "print the version and exit")
	fs.Usage = func() {}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			usage(fs, stdout)
			return exitOK
		}
		fmt.Fprintf(stderr, "furoshiki: %v\n", err)
		fmt.Fprintf(stderr, "Run 'furoshiki -h' for usage.\n")
		return exitUsage
	}
	if showVersion {
		fmt.Fprintf(stdout, "furoshiki %s\n", Version())
		return exitOK
	}
	if fs.NArg() != 1 {
		if fs.NArg() == 0 {
			fmt.Fprintln(stderr, "furoshiki: missing <site-dir> argument")
		} else {
			fmt.Fprintf(stderr, "furoshiki: unexpected arguments: %s\n", strings.Join(fs.Args()[1:], " "))
		}
		fmt.Fprintf(stderr, "Run 'furoshiki -h' for usage.\n")
		return exitUsage
	}
	if quiet && verbose {
		fmt.Fprintln(stderr, "furoshiki: -q and -v are mutually exclusive")
		return exitUsage
	}
	dir := fs.Arg(0)
	if out == "" {
		out = defaultOutput(dir)
	}

	diags := &diag.Collector{}
	opts := bundle.Options{
		Dir:             dir,
		Entry:           entry,
		Excludes:        excludes,
		SinglePage:      singlePage,
		MaxResourceSize: int64(maxSize),
		DefaultCharset:  charset,
		Generator:       "furoshiki " + Version(),
		Diag:            diags,
	}

	var (
		res *bundle.Result
		err error
	)
	if out == "-" {
		res, err = bundle.Build(opts, stdout)
	} else {
		res, err = bundle.BuildFile(opts, out)
	}
	report(stderr, diags, quiet, verbose)
	if err != nil {
		fmt.Fprintf(stderr, "furoshiki: %v\n", err)
		return exitFailure
	}

	if !quiet {
		target := out
		if out == "-" {
			target = "standard output"
		}
		fmt.Fprintf(stderr, "furoshiki: wrote %s (%d pages, %d resources, %s)\n", target, len(res.Pages), res.Resources, humanSize(res.Bytes))
		if verbose {
			for _, p := range res.Pages {
				fmt.Fprintf(stderr, "  page %s\n", p)
			}
		}
	}
	if warnings := diags.Count(diag.Warning); warnings > 0 {
		if strict {
			fmt.Fprintf(stderr, "furoshiki: %d warning(s) treated as errors (--strict)\n", warnings)
			return exitFailure
		}
		if !quiet {
			fmt.Fprintf(stderr, "furoshiki: %d warning(s)\n", warnings)
		}
	}
	return exitOK
}

// report prints the collected diagnostics.
func report(w io.Writer, diags *diag.Collector, quiet, verbose bool) {
	if quiet {
		return
	}
	for _, d := range diags.Items() {
		if d.Level == diag.Info && !verbose {
			continue
		}
		fmt.Fprintln(w, d.String())
	}
}

// defaultOutput derives the output file name from the site directory:
// "./site" and "site/" give "site.html" in the current directory; "." gives
// the name of the current directory.
func defaultOutput(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	base := filepath.Base(filepath.Clean(abs))
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "site"
	}
	return base + ".html"
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// Version returns the version string: the linker-provided value, else the
// module version recorded by "go install ...@version", else "devel" with the
// VCS revision when available.
func Version() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "devel"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	var rev, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				modified = "-dirty"
			}
		}
	}
	if rev != "" {
		if len(rev) > 12 {
			rev = rev[:12]
		}
		return "devel-" + rev + modified
	}
	return "devel"
}
