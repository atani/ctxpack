package ctxpack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestEstimateTokensHandlesEmptyAndCJK(t *testing.T) {
	if EstimateTokens("") != 0 {
		t.Fatal("empty should be zero")
	}
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
}

func TestQueryTieBreaksKeepDocumentOrder(t *testing.T) {
	markdown := "## Alpha\nshared\n\n## Beta\nshared\n\n## Gamma\nother"
	q := "shared"
	ordered := ApplyQuery(markdown, &q)
	if strings.Index(ordered, "## Alpha") > strings.Index(ordered, "## Beta") {
		t.Fatalf("tie order changed: %s", ordered)
	}
}

func TestReadSourceRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.html")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxFileSize + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadSource(path, strings.NewReader("")); err == nil {
		t.Fatal("expected oversized file error")
	}
}
