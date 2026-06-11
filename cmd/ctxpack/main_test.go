package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateHome points the stats history at a temp directory on every
// platform: os.UserHomeDir reads HOME on Unix and USERPROFILE on Windows.
func isolateHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()
	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func serveJSShell(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<html><head><title>App</title></head><body><noscript>You need to enable JavaScript to run this app.</noscript><div id="root"></div><script src="/main.js"></script></body></html>`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fixturePath() string { return filepath.Join("..", "..", "tests", "fixture.html") }

func TestRunVersionFlagPrintsVersion(t *testing.T) {
	var code int
	out := captureStdout(t, func() { code = run([]string{"--version"}) })
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out, "ctxpack "+version) {
		t.Fatalf("version output = %q", out)
	}
}

func TestRunHelpFlagPrintsUsage(t *testing.T) {
	var code int
	out := captureStdout(t, func() { code = run([]string{"--help"}) })
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out, "Usage: ctxpack") || !strings.Contains(out, "--query") {
		t.Fatalf("usage output = %q", out)
	}
}

func TestRunFixtureJSON(t *testing.T) {
	isolateHome(t)
	out := captureStdout(t, func() {
		if code := run([]string{fixturePath(), "--json"}); code != 0 {
			t.Errorf("code != 0")
		}
	})
	if !strings.Contains(out, `"ok": true`) || !strings.Contains(out, `"format": "markdown"`) {
		t.Fatalf("json output = %q", out)
	}
}

func TestRunSubcommandForm(t *testing.T) {
	isolateHome(t)
	out := captureStdout(t, func() {
		if code := run([]string{"run", fixturePath(), "--json"}); code != 0 {
			t.Errorf("code != 0")
		}
	})
	if !strings.Contains(out, `"ok": true`) {
		t.Fatalf("json output = %q", out)
	}
}

func TestRunOutputWritesFile(t *testing.T) {
	isolateHome(t)
	out := filepath.Join(t.TempDir(), "out.md")
	if code := run([]string{fixturePath(), "--no-record", "-o", out}); code != 0 {
		t.Fatalf("code = %d", code)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("empty output file")
	}
}

func TestRunMissingFileReturns2(t *testing.T) {
	isolateHome(t)
	if code := run([]string{filepath.Join(t.TempDir(), "does-not-exist.html"), "--no-record"}); code != 2 {
		t.Fatalf("code = %d", code)
	}
}

func TestRunHTTPErrorStatusReturns1(t *testing.T) {
	isolateHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "<html><body>gone</body></html>", http.StatusNotFound)
	}))
	defer srv.Close()
	if code := run([]string{srv.URL, "--no-record"}); code != 1 {
		t.Fatalf("code = %d", code)
	}
}

func TestRunJSRequiredPageReturns3WithHint(t *testing.T) {
	isolateHome(t)
	srv := serveJSShell(t)
	var code int
	stderr := captureStderr(t, func() { code = run([]string{srv.URL, "--no-record"}) })
	if code != 3 {
		t.Fatalf("code = %d, want 3 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "JavaScript rendering") || !strings.Contains(stderr, "--dump-dom") || !strings.Contains(stderr, "'"+srv.URL+"'") {
		t.Fatalf("stderr missing hint: %q", stderr)
	}
}

func TestRunJSRequiredJSONKeepsStdoutEmpty(t *testing.T) {
	isolateHome(t)
	srv := serveJSShell(t)
	var code int
	out := captureStdout(t, func() {
		_ = captureStderr(t, func() { code = run([]string{srv.URL, "--json", "--no-record"}) })
	})
	if code != 3 {
		t.Fatalf("code = %d, want 3", code)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want empty so JSON consumers cannot misread a failure", out)
	}
}

func TestHintURLFallsBackOnShellUnsafeURLs(t *testing.T) {
	if got := hintURL("https://example.com/docs"); got != "'https://example.com/docs'" {
		t.Fatalf("hintURL = %q", got)
	}
	for _, unsafe := range []string{"https://x/a'b", "https://x/a\nb", "https://x/a\x7fb"} {
		if got := hintURL(unsafe); got != "URL" {
			t.Fatalf("hintURL(%q) = %q, want placeholder", unsafe, got)
		}
	}
}

func TestRunUnreachableHostReturns1(t *testing.T) {
	isolateHome(t)
	// Port 1 on localhost refuses connections, exercising the net.Error
	// (retriable) classification path.
	if code := run([]string{"http://127.0.0.1:1/", "--no-record"}); code != 1 {
		t.Fatalf("code = %d", code)
	}
}

func TestRunStatsAndResetFlow(t *testing.T) {
	isolateHome(t)
	captureStdout(t, func() {
		if code := run([]string{fixturePath()}); code != 0 {
			t.Errorf("pack code != 0")
		}
	})
	statsOut := captureStdout(t, func() {
		if code := run([]string{"stats"}); code != 0 {
			t.Errorf("stats code != 0")
		}
	})
	if !strings.Contains(statsOut, "Runs:        1") {
		t.Fatalf("stats output = %q", statsOut)
	}
	resetOut := captureStdout(t, func() {
		if code := run([]string{"reset", "--yes"}); code != 0 {
			t.Errorf("reset code != 0")
		}
	})
	if !strings.Contains(resetOut, "Reset 1 recorded run(s).") {
		t.Fatalf("reset output = %q", resetOut)
	}
	afterOut := captureStdout(t, func() {
		if code := run([]string{"stats"}); code != 0 {
			t.Errorf("stats code != 0")
		}
	})
	if !strings.Contains(afterOut, "Runs:        0") {
		t.Fatalf("stats after reset = %q", afterOut)
	}
}

func TestRunNoRecordSkipsHistory(t *testing.T) {
	isolateHome(t)
	captureStdout(t, func() {
		if code := run([]string{fixturePath(), "--no-record"}); code != 0 {
			t.Errorf("pack code != 0")
		}
	})
	statsOut := captureStdout(t, func() {
		if code := run([]string{"stats"}); code != 0 {
			t.Errorf("stats code != 0")
		}
	})
	if !strings.Contains(statsOut, "Runs:        0") {
		t.Fatalf("--no-record still recorded: %q", statsOut)
	}
}

func TestRunResetRequiresYes(t *testing.T) {
	isolateHome(t)
	if code := run([]string{"reset"}); code != 2 {
		t.Fatalf("code = %d", code)
	}
}

func TestRunParseErrors(t *testing.T) {
	isolateHome(t)
	cases := [][]string{
		{"--bogus"},
		{"--query"},
		{"-o"},
		{fixturePath(), "extra-arg"},
		{"run"},
		{},
	}
	for _, argv := range cases {
		if code := run(argv); code != 2 {
			t.Fatalf("argv %v: code = %d, want 2", argv, code)
		}
	}
}

func TestRunEmptyQueryMatchesPythonJSON(t *testing.T) {
	isolateHome(t)
	out := captureStdout(t, func() {
		if code := run([]string{fixturePath(), "--no-record", "--json", "--query", ""}); code != 0 {
			t.Errorf("code != 0")
		}
	})
	// Python prints "query": "" when --query '' is given; nil would print null.
	if !strings.Contains(out, `"query": ""`) {
		t.Fatalf("empty query lost in JSON output: %q", out)
	}
}
