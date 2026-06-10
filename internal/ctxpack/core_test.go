package ctxpack

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/japanese"
)

func fixturePath() string { return filepath.Join("..", "..", "tests", "fixture.html") }

func TestPackRemovesObviousNoiseAndReportsSavings(t *testing.T) {
	result, err := Pack(fixturePath(), nil, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if result.Title == nil || *result.Title != "Agent Token Budget Guide" {
		t.Fatalf("title = %#v", result.Title)
	}
	if strings.Contains(result.Content, "console.log") || strings.Contains(result.Content, "Home Pricing Blog") {
		t.Fatalf("noise was not removed:\n%s", result.Content)
	}
	if !strings.Contains(result.Content, "AI agents should avoid reading raw HTML") {
		t.Fatalf("missing article content:\n%s", result.Content)
	}
	if result.Stats.RawHTMLTokens <= result.Stats.FinalTokens || result.Stats.SavedTokens <= 0 || result.Stats.ReductionPercent <= 0 {
		t.Fatalf("bad stats: %+v", result.Stats)
	}
}

func TestQueryMovesRelevantSectionFirst(t *testing.T) {
	q := "pricing"
	result, err := Pack(fixturePath(), &q, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.Content, "## Pricing") {
		t.Fatalf("content did not start with pricing:\n%s", result.Content)
	}
}

func TestCJKQueryReordersSections(t *testing.T) {
	markdown := "## はじめに\n概要です\n\n## 料金プラン\n料金の詳細はこちら\n\n## その他\n補足"
	q := "料金"
	ordered := ApplyQuery(markdown, &q)
	if !strings.HasPrefix(ordered, "## 料金プラン") {
		t.Fatalf("CJK query did not reorder sections:\n%s", ordered)
	}
}

func TestMarkdownStatsIncludeTokenSavings(t *testing.T) {
	result, err := Pack(fixturePath(), nil, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	output := ResultToMarkdown(result, true)
	if !strings.Contains(output, "saved_tokens:") || !strings.Contains(output, "reduction_percent:") {
		t.Fatalf("missing stats:\n%s", output)
	}
}

func TestJSONOutputShape(t *testing.T) {
	result, err := Pack(fixturePath(), nil, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(result.ToJSONResult())
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(b, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["ok"] != true {
		t.Fatalf("bad ok: %#v", payload["ok"])
	}
	content := payload["content"].(map[string]any)
	if content["format"] != "markdown" {
		t.Fatalf("bad format: %#v", content["format"])
	}
}

func TestCumulativeStatsAndReset(t *testing.T) {
	tmp := t.TempDir()
	statsPath := filepath.Join(tmp, "stats.jsonl")
	result, err := Pack(fixturePath(), nil, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordRun(result, statsPath); err != nil {
		t.Fatal(err)
	}
	q := "pricing"
	result, err = Pack(fixturePath(), &q, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordRun(result, statsPath); err != nil {
		t.Fatal(err)
	}
	rows, err := LoadHistory(statsPath)
	if err != nil {
		t.Fatal(err)
	}
	summary := SummarizeHistory(rows)
	if summary.Runs != 2 || summary.SavedTokens <= 0 {
		t.Fatalf("bad summary: %+v", summary)
	}
	if got, err := ResetHistory(statsPath); err != nil || got != 2 {
		t.Fatalf("reset = %d, %v", got, err)
	}
	rows, err = LoadHistory(statsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows after reset = %d", len(rows))
	}
}

func TestLoadHistoryReadsPythonFormatLines(t *testing.T) {
	// A line as written by the original Python implementation must keep
	// loading: same keys, stats nested under "stats".
	line := `{"source_url": "https://example.com", "title": "T", "fetched_at": "2026-01-01T00:00:00+00:00", "query": null, "stats": {"raw_html_tokens": 100, "clean_text_tokens": 40, "final_tokens": 40, "saved_tokens": 60, "reduction_percent": 60.0}}`
	statsPath := filepath.Join(t.TempDir(), "stats.jsonl")
	if err := os.WriteFile(statsPath, []byte(line+"\n\nnot json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, err := LoadHistory(statsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	summary := SummarizeHistory(rows)
	if summary.RawInputTokens != 100 || summary.SavedTokens != 60 || summary.ReductionPercent != 60.0 {
		t.Fatalf("bad summary: %+v", summary)
	}
}

func TestEstimateTokensHandlesEmptyAndCJK(t *testing.T) {
	if EstimateTokens("") != 0 {
		t.Fatal("empty should be zero")
	}
	// 6 CJK characters at ~0.8 tokens each: round(6*0.8) = 5.
	if EstimateTokens("日本語テスト") != 5 {
		t.Fatalf("unexpected CJK token estimate: %d", EstimateTokens("日本語テスト"))
	}
}

func TestReductionPercentIsZeroWhenNothingSaved(t *testing.T) {
	plain, err := Pack("-", nil, strings.NewReader("just plain words with no markup at all"))
	if err != nil {
		t.Fatal(err)
	}
	if plain.Stats.SavedTokens != 0 || plain.Stats.ReductionPercent != 0 {
		t.Fatalf("bad plain stats: %+v", plain.Stats)
	}
}

func TestVoidElementsInsideDroppedNavDoNotSwallowContent(t *testing.T) {
	html := "<body><nav><input type='text'><img src='x'>Menu Home</nav><article><p>REAL ARTICLE CONTENT</p></article></body>"
	_, markdown := HTMLToMarkdown(html)
	if !strings.Contains(markdown, "REAL ARTICLE CONTENT") {
		t.Fatalf("missing real content: %s", markdown)
	}
	if strings.Contains(markdown, "Menu Home") {
		t.Fatalf("kept menu: %s", markdown)
	}
}

func TestDeeplyNestedHTMLDoesNotPanic(t *testing.T) {
	// The iterative walk removes unbounded recursion by construction; this is
	// a regression canary for deep trees. Depth is kept moderate because
	// html.Parse itself slows superlinearly at extreme nesting.
	var b strings.Builder
	b.WriteString("<body>")
	const depth = 10000
	for i := 0; i < depth; i++ {
		b.WriteString("<div>")
	}
	b.WriteString("deep text")
	for i := 0; i < depth; i++ {
		b.WriteString("</div>")
	}
	b.WriteString("</body>")
	_, markdown := HTMLToMarkdown(b.String())
	if !strings.Contains(markdown, "deep text") {
		t.Fatalf("lost content in deep nesting: %q", markdown)
	}
}

func TestYAMLFrontMatterIsSafeForTrickyTitles(t *testing.T) {
	html := "<html><head><title>--- not\na real\tdelimiter</title></head><body><p>Body.</p></body></html>"
	result, err := Pack("-", nil, strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	md := ResultToMarkdown(result, false)
	if !strings.HasPrefix(md, "---\n") || !strings.Contains(md, "Body.") {
		t.Fatalf("bad markdown: %s", md)
	}
	// The title must not introduce an extra front-matter delimiter line:
	// exactly one closing "---" after the opening one.
	if strings.Count(md, "\n---\n") > 1 {
		t.Fatalf("title broke the front matter delimiters:\n%s", md)
	}
}

func TestQueryTieBreaksKeepDocumentOrder(t *testing.T) {
	markdown := "## Alpha\nshared\n\n## Beta\nshared\n\n## Gamma\nother"
	q := "shared"
	ordered := ApplyQuery(markdown, &q)
	if strings.Index(ordered, "## Alpha") > strings.Index(ordered, "## Beta") {
		t.Fatalf("tie order changed: %s", ordered)
	}
}

func TestSplitSectionsOnlyTreatsATXHeadingsAsBoundaries(t *testing.T) {
	markdown := "## Real Heading\n#include <stdio.h>\n#define MAX 10\n####### seven hashes is not a heading\nbody"
	sections := SplitSections(markdown)
	if len(sections) != 1 {
		t.Fatalf("non-heading # lines split sections: %#v", sections)
	}
}

func TestReadSourceRejectsOversizedFile(t *testing.T) {
	orig := MaxFileSize
	MaxFileSize = 1024
	t.Cleanup(func() { MaxFileSize = orig })
	path := filepath.Join(t.TempDir(), "big.html")
	if err := os.WriteFile(path, bytesOfSize(MaxFileSize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadSource(path, strings.NewReader("")); err == nil {
		t.Fatal("expected oversized file error")
	}
}

func bytesOfSize(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return b
}

func TestFetchURLRejectsHTTPErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "<html><body>not found page</body></html>", http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := FetchURL(srv.URL, 5*time.Second)
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected HTTPStatusError, got %v", err)
	}
	if statusErr.Code != http.StatusNotFound {
		t.Fatalf("code = %d", statusErr.Code)
	}
}

func TestFetchURLDecodesDeclaredCharset(t *testing.T) {
	sjis, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte("<html><body><p>日本語のページ</p></body></html>"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=Shift_JIS")
		w.Write(sjis)
	}))
	defer srv.Close()
	body, err := FetchURL(srv.URL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "日本語のページ") {
		t.Fatalf("Shift_JIS body was not decoded: %q", body)
	}
}

func TestFetchURLDefaultsToUTF8WithoutCharset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body><p>UTF-8 のままで届く</p></body></html>")
	}))
	defer srv.Close()
	body, err := FetchURL(srv.URL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "UTF-8 のままで届く") {
		t.Fatalf("UTF-8 body was mangled: %q", body)
	}
}

func TestFetchURLRejectsOversizedDeclaredContentLength(t *testing.T) {
	orig := MaxFetchSize
	MaxFetchSize = 1024
	t.Cleanup(func() { MaxFetchSize = orig })
	payload := bytesOfSize(MaxFetchSize + 100)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		w.Write(payload)
	}))
	defer srv.Close()
	if _, err := FetchURL(srv.URL, 5*time.Second); err == nil {
		t.Fatal("expected oversized response error")
	}
}

func TestFetchURLRejectsOversizedChunkedBody(t *testing.T) {
	orig := MaxFetchSize
	MaxFetchSize = 1024
	t.Cleanup(func() { MaxFetchSize = orig })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Flush before writing the body to force chunked transfer encoding
		// (ContentLength -1 on the client), so only the body-size check can
		// catch the overflow.
		w.(http.Flusher).Flush()
		w.Write(bytesOfSize(MaxFetchSize + 100))
	}))
	defer srv.Close()
	_, err := FetchURL(srv.URL, 5*time.Second)
	if err == nil {
		t.Fatal("expected oversized response error")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected the body-size check to trigger, got: %v", err)
	}
}
