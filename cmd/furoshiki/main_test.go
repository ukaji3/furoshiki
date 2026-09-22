package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const site1 = "../../testdata/site1"

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestRunWritesDefaultOutput(t *testing.T) {
	// The default output goes to the current directory, so run inside a
	// temporary one.
	tmp := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(wd) }()

	site := filepath.Join(wd, site1)
	code, stdout, stderr := runCLI(t, site)
	if code != exitOK {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout should be empty, got %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(tmp, "site1.html")); err != nil {
		t.Errorf("default output not written: %v", err)
	}
	if !strings.Contains(stderr, "wrote site1.html (7 pages, 8 resources,") {
		t.Errorf("summary missing in stderr:\n%s", stderr)
	}
	if !strings.Contains(stderr, `warning: index.html: "missing.html": file not found`) {
		t.Errorf("warnings missing in stderr:\n%s", stderr)
	}
	if !strings.Contains(stderr, "furoshiki: 2 warning(s)") {
		t.Errorf("warning count missing in stderr:\n%s", stderr)
	}
	if strings.Contains(stderr, "info:") {
		t.Errorf("info notes should need -v:\n%s", stderr)
	}
}

func TestRunOutputFlagAndVerbose(t *testing.T) {
	out := filepath.Join(t.TempDir(), "b.html")
	code, _, stderr := runCLI(t, "-v", "-o", out, "--exclude", "drafts", site1)
	if code != exitOK {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("output not written: %v", err)
	}
	for _, want := range []string{
		"info: sjis.html: decoded from shift_jis",
		`info: index.html: "drafts/secret.html": excluded by pattern`,
		"  page index.html\n",
		"  page frame.html\n",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in stderr:\n%s", want, stderr)
		}
	}
}

func TestRunStdout(t *testing.T) {
	code, stdout, stderr := runCLI(t, "-q", "-o", "-", site1)
	if code != exitOK {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "<!DOCTYPE html>") || !strings.Contains(stdout, `id="furoshiki-payload"`) {
		t.Errorf("stdout does not look like a bundle: %.80q", stdout)
	}
	if stderr != "" {
		t.Errorf("-q should silence stderr, got:\n%s", stderr)
	}
}

func TestRunStrict(t *testing.T) {
	out := filepath.Join(t.TempDir(), "b.html")
	code, _, stderr := runCLI(t, "--strict", "-o", out, site1)
	if code != exitFailure {
		t.Errorf("exit %d, want %d; stderr:\n%s", code, exitFailure, stderr)
	}
	if !strings.Contains(stderr, "2 warning(s) treated as errors") {
		t.Errorf("strict message missing:\n%s", stderr)
	}
	// The bundle is still written: strict only changes the exit status.
	if _, err := os.Stat(out); err != nil {
		t.Errorf("output should still be written under --strict: %v", err)
	}

	// A site without warnings passes --strict.
	code, _, stderr = runCLI(t, "--strict", "-o", filepath.Join(t.TempDir(), "c.html"), "-e", "docs/guide.html", "--exclude", "index.html", "--exclude", "about.html", "--exclude", "sjis.html", "--exclude", "frame.html", site1)
	if code != exitOK {
		t.Errorf("exit %d, stderr:\n%s", code, stderr)
	}
}

func TestRunEntryAndSinglePage(t *testing.T) {
	code, stdout, stderr := runCLI(t, "-q", "-o", "-", "--single-page", "--entry", "about.html", site1)
	if code != exitOK {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, `"entry":"about.html"`) {
		t.Error("entry not honoured")
	}
	if strings.Contains(stdout, `"index.html":{`) {
		t.Error("single-page bundle should not contain other pages")
	}
}

func TestRunMaxResourceSizeAndCharset(t *testing.T) {
	code, _, stderr := runCLI(t, "-o", filepath.Join(t.TempDir(), "b.html"), "--max-resource-size", "100", "--charset", "shift_jis", site1)
	if code != exitOK {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "exceeds the resource size limit of 100 bytes") {
		t.Errorf("size limit not applied:\n%s", stderr)
	}
}

func TestRunErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"no args", nil, exitUsage, "missing <site-dir>"},
		{"two dirs", []string{"a", "b"}, exitUsage, "unexpected arguments: b"},
		{"unknown flag", []string{"--bogus", site1}, exitUsage, "flag provided but not defined"},
		{"bad size", []string{"--max-resource-size", "lots", site1}, exitUsage, "invalid size"},
		{"q and v", []string{"-q", "-v", site1}, exitUsage, "mutually exclusive"},
		{"missing dir", []string{"-o", "-", filepath.Join(t.TempDir(), "nope")}, exitFailure, "nope"},
		{"missing entry", []string{"-o", "-", "-e", "nope.html", site1}, exitFailure, "entry page nope.html"},
		{"bad exclude", []string{"-o", "-", "--exclude", "[", site1}, exitFailure, "invalid exclude pattern"},
		{"output is input", []string{"-o", filepath.Join(site1, "index.html"), site1}, exitFailure, "would overwrite an input file"},
		{"output dir missing", []string{"-o", filepath.Join(t.TempDir(), "x", "y.html"), site1}, exitFailure, "create temporary output"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(t, tc.args...)
			if code != tc.code {
				t.Errorf("exit %d, want %d", code, tc.code)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr lacks %q:\n%s", tc.want, stderr)
			}
			if stdout != "" && tc.code == exitUsage {
				t.Errorf("stdout should be empty on usage errors, got %q", stdout)
			}
		})
	}
	// Make sure the failed "output is input" run did not touch the fixture.
	data, err := os.ReadFile(filepath.Join(site1, "index.html"))
	if err != nil || !bytes.HasPrefix(data, []byte("<!DOCTYPE html>\n<html lang=\"ja\">")) {
		t.Errorf("fixture index.html was modified or unreadable: %v", err)
	}
}

func TestRunHelpAndVersion(t *testing.T) {
	code, stdout, stderr := runCLI(t, "-h")
	if code != exitOK || !strings.Contains(stdout, "Usage: furoshiki [options] <site-dir>") || stderr != "" {
		t.Errorf("-h: exit %d, stdout %.60q, stderr %q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "-max-resource-size") || !strings.Contains(stdout, "Exit status:") {
		t.Errorf("usage text incomplete:\n%s", stdout)
	}
	code, stdout, _ = runCLI(t, "--version")
	if code != exitOK || !strings.HasPrefix(stdout, "furoshiki ") || strings.TrimSpace(stdout) == "furoshiki" {
		t.Errorf("--version: exit %d, stdout %q", code, stdout)
	}
	version = "v9.9.9"
	defer func() { version = "" }()
	if got := Version(); got != "v9.9.9" {
		t.Errorf("Version() = %q, want linker value", got)
	}
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"0", 0, true},
		{"1024", 1024, true},
		{"2k", 2048, true},
		{"2K", 2048, true},
		{"2KB", 2048, true},
		{"2KiB", 2048, true},
		{"3M", 3 << 20, true},
		{"1G", 1 << 30, true},
		{" 5 M ", 5 << 20, true},
		{"", 0, false},
		{"-1", 0, false},
		{"abc", 0, false},
		{"1T", 0, false},
	}
	for _, tc := range tests {
		got, err := parseSize(tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("parseSize(%q) = %d, %v; want %d, ok=%v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}

func TestDefaultOutput(t *testing.T) {
	tests := map[string]string{
		"site":          "site.html",
		"./site/":       "site.html",
		"/tmp/x/mysite": "mysite.html",
		"a/b/c":         "c.html",
	}
	for in, want := range tests {
		if got := defaultOutput(in); got != want {
			t.Errorf("defaultOutput(%q) = %q, want %q", in, got, want)
		}
	}
	// "." resolves to the current directory's name.
	wd, _ := os.Getwd()
	if got := defaultOutput("."); got != filepath.Base(wd)+".html" {
		t.Errorf("defaultOutput(\".\") = %q", got)
	}
}

func TestHumanSize(t *testing.T) {
	for n, want := range map[int64]string{
		0:           "0 B",
		1023:        "1023 B",
		1024:        "1.0 KiB",
		1536:        "1.5 KiB",
		5 << 20:     "5.0 MiB",
		3 << 30:     "3.0 GiB",
		1<<40 + 512: "1.0 TiB",
	} {
		if got := humanSize(n); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", n, got, want)
		}
	}
}
