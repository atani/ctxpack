package ctxpack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"
)

const (
	MaxFetchSize = 50 * 1024 * 1024
	MaxFileSize  = 100 * 1024 * 1024
	UserAgent    = "ctxpack/0.1 (+https://github.com/atani/ctxpack)"
)

var (
	blockTags = map[string]bool{
		"address": true, "article": true, "aside": true, "blockquote": true, "br": true, "dd": true, "details": true,
		"div": true, "dl": true, "dt": true, "figcaption": true, "figure": true, "footer": true, "form": true,
		"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "header": true, "hr": true,
		"li": true, "main": true, "nav": true, "ol": true, "p": true, "pre": true, "section": true, "table": true,
		"tbody": true, "td": true, "tfoot": true, "th": true, "thead": true, "tr": true, "ul": true,
	}
	dropTags    = map[string]bool{"script": true, "style": true, "noscript": true, "svg": true, "canvas": true, "iframe": true}
	headingTags = map[string]bool{"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true}
	dropHints   = map[string]bool{
		"ad": true, "ads": true, "advert": true, "banner": true, "breadcrumb": true, "cookie": true, "footer": true,
		"header": true, "menu": true, "modal": true, "nav": true, "newsletter": true, "promo": true, "recommend": true,
		"related": true, "share": true, "sidebar": true, "social": true, "subscribe": true, "tracking": true,
	}
	spaceRe        = regexp.MustCompile(`[ \t]+`)
	lineSpaceRe    = regexp.MustCompile(` *\n *`)
	manyNewlinesRe = regexp.MustCompile(`\n{3,}`)
	emptyHeadingRe = regexp.MustCompile(`(?m)^# +$`)
	wordRe         = regexp.MustCompile(`\w+`)
)

type TokenStats struct {
	RawHTMLTokens    int     `json:"raw_html_tokens"`
	CleanTextTokens  int     `json:"clean_text_tokens"`
	FinalTokens      int     `json:"final_tokens"`
	SavedTokens      int     `json:"saved_tokens"`
	ReductionPercent float64 `json:"reduction_percent"`
}

type PackResult struct {
	SourceURL string
	Title     *string
	FetchedAt string
	Content   string
	Stats     TokenStats
	Query     *string
}

type JSONResult struct {
	OK      bool              `json:"ok"`
	Source  map[string]string `json:"source"`
	Title   *string           `json:"title"`
	Query   *string           `json:"query"`
	Content map[string]string `json:"content"`
	Stats   TokenStats        `json:"stats"`
}

func (r PackResult) ToJSONResult() JSONResult {
	return JSONResult{
		OK:      true,
		Source:  map[string]string{"url": r.SourceURL, "fetched_at": r.FetchedAt},
		Title:   r.Title,
		Query:   r.Query,
		Content: map[string]string{"format": "markdown", "text": r.Content},
		Stats:   r.Stats,
	}
}

func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	cjk, total := 0, 0
	for _, r := range text {
		total++
		if (r >= 0x3040 && r <= 0x30ff) || (r >= 0x3400 && r <= 0x9fff) {
			cjk++
		}
	}
	nonCJK := total - cjk
	v := int(math.Round(float64(nonCJK)/4.0 + float64(cjk)*0.8))
	if v < 1 {
		return 1
	}
	return v
}

func newStats(raw, clean, final string) TokenStats {
	rawT := EstimateTokens(raw)
	cleanT := EstimateTokens(clean)
	finalT := EstimateTokens(final)
	saved := rawT - finalT
	if saved < 0 {
		saved = 0
	}
	reduction := 0.0
	if rawT > 0 {
		reduction = math.Round((float64(saved)/float64(rawT))*1000) / 10
	}
	return TokenStats{RawHTMLTokens: rawT, CleanTextTokens: cleanT, FinalTokens: finalT, SavedTokens: saved, ReductionPercent: reduction}
}

func FetchURL(url string, timeout time.Duration) (string, error) {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", UserAgent)
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.ContentLength > MaxFetchSize {
		return "", fmt.Errorf("response too large: %d bytes (limit %d)", res.ContentLength, MaxFetchSize)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, MaxFetchSize+1))
	if err != nil {
		return "", err
	}
	if len(body) > MaxFetchSize {
		return "", fmt.Errorf("response exceeds %d bytes", MaxFetchSize)
	}
	return string(bytes.ToValidUTF8(body, []byte("\uFFFD"))), nil
}

func ReadSource(source string, stdin io.Reader) (string, string, error) {
	if source == "-" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", "", err
		}
		return "stdin", string(bytes.ToValidUTF8(b, []byte("\uFFFD"))), nil
	}
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		s, err := FetchURL(source, 20*time.Second)
		return source, s, err
	}
	f, err := os.Open(source)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return "", "", err
	}
	if len(b) > MaxFileSize {
		return "", "", fmt.Errorf("file too large: exceeds %d bytes", MaxFileSize)
	}
	return source, string(bytes.ToValidUTF8(b, []byte("\uFFFD"))), nil
}

func HTMLToMarkdown(input string) (*string, string) {
	doc, err := html.Parse(strings.NewReader(input))
	if err != nil {
		return nil, NormalizeText(input)
	}
	var parts []string
	var titleParts []string
	var walk func(*html.Node, bool, bool)
	walk = func(n *html.Node, skipping bool, inTitle bool) {
		if n.Type == html.ElementNode {
			tag := strings.ToLower(n.Data)
			if tag == "title" {
				inTitle = true
			}
			if skipping || dropTags[tag] || looksNoisy(tag, n.Attr) {
				skipping = true
			}
			if !skipping {
				if headingTags[tag] {
					level := int(tag[1] - '0')
					parts = append(parts, "\n"+strings.Repeat("#", level)+" ")
				} else if tag == "li" {
					parts = append(parts, "\n- ")
				} else if blockTags[tag] {
					parts = append(parts, "\n")
				}
			}
		}
		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				if inTitle {
					titleParts = append(titleParts, text)
				} else if !skipping {
					parts = append(parts, text+" ")
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, skipping, inTitle)
		}
		if n.Type == html.ElementNode && !skipping && blockTags[strings.ToLower(n.Data)] {
			parts = append(parts, "\n")
		}
	}
	walk(doc, false, false)
	title := NormalizeInline(strings.Join(titleParts, " "))
	var titlePtr *string
	if title != "" {
		titlePtr = &title
	}
	return titlePtr, NormalizeText(strings.Join(parts, ""))
}

func looksNoisy(tag string, attrs []html.Attribute) bool {
	if tag == "nav" || tag == "footer" || tag == "aside" {
		return true
	}
	var blob strings.Builder
	for _, a := range attrs {
		k := strings.ToLower(a.Key)
		if k == "class" || k == "id" || k == "role" {
			blob.WriteString(" ")
			blob.WriteString(strings.ToLower(a.Val))
		}
	}
	for _, tok := range strings.FieldsFunc(blob.String(), func(r rune) bool { return !(unicode.IsLetter(r) || unicode.IsDigit(r)) }) {
		if dropHints[tok] {
			return true
		}
	}
	return false
}

func NormalizeInline(text string) string {
	return strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(text, " "))
}

func NormalizeText(text string) string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	text = spaceRe.ReplaceAllString(text, " ")
	text = lineSpaceRe.ReplaceAllString(text, "\n")
	text = manyNewlinesRe.ReplaceAllString(text, "\n\n")
	text = emptyHeadingRe.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

func SplitSections(markdown string) []string {
	lines := strings.Split(markdown, "\n")
	var sections []string
	var cur []string
	flush := func() {
		if s := strings.TrimSpace(strings.Join(cur, "\n")); s != "" {
			sections = append(sections, s)
		}
		cur = nil
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "#") && strings.Contains(line, " ") {
			flush()
		}
		cur = append(cur, line)
	}
	flush()
	if len(sections) > 0 {
		return sections
	}
	for _, p := range strings.Split(markdown, "\n\n") {
		if s := strings.TrimSpace(p); s != "" {
			sections = append(sections, s)
		}
	}
	return sections
}

func queryTerms(query string) []string {
	words := wordRe.FindAllString(query, -1)
	terms := make([]string, 0, len(words))
	for _, w := range words {
		if len([]rune(w)) >= 2 {
			terms = append(terms, strings.ToLower(w))
		}
	}
	return terms
}

func ScoreSection(section string, query *string) float64 {
	if query == nil {
		return 0
	}
	return scoreSectionTerms(section, queryTerms(*query))
}

func scoreSectionTerms(section string, terms []string) float64 {
	hay := strings.ToLower(section)
	score := 0.0
	for _, t := range terms {
		score += float64(strings.Count(hay, t) * 2)
	}
	if strings.HasPrefix(section, "#") {
		first := strings.ToLower(strings.SplitN(section, "\n", 2)[0])
		for _, t := range terms {
			if strings.Contains(first, t) {
				score += 3
			}
		}
	}
	return score
}

func ApplyQuery(markdown string, query *string) string {
	if query == nil {
		return markdown
	}
	sections := SplitSections(markdown)
	if len(sections) <= 1 {
		return markdown
	}
	terms := queryTerms(*query)
	type scored struct {
		score   float64
		idx     int
		section string
	}
	var relevant []scored
	selected := map[int]bool{}
	for i, s := range sections {
		if sc := scoreSectionTerms(s, terms); sc > 0 {
			relevant = append(relevant, scored{sc, i, s})
			selected[i] = true
		}
	}
	if len(relevant) == 0 {
		return markdown
	}
	sort.SliceStable(relevant, func(i, j int) bool { return relevant[i].score > relevant[j].score })
	ordered := make([]string, 0, len(sections))
	for _, r := range relevant {
		ordered = append(ordered, r.section)
	}
	for i, s := range sections {
		if !selected[i] {
			ordered = append(ordered, s)
		}
	}
	return strings.Join(ordered, "\n\n")
}

func Pack(source string, query *string, stdin io.Reader) (PackResult, error) {
	sourceURL, raw, err := ReadSource(source, stdin)
	if err != nil {
		return PackResult{}, err
	}
	var title *string
	var clean string
	if strings.Contains(raw, "<") && strings.Contains(raw, ">") {
		title, clean = HTMLToMarkdown(raw)
	} else {
		clean = NormalizeText(raw)
	}
	final := ApplyQuery(clean, query)
	return PackResult{SourceURL: sourceURL, Title: title, FetchedAt: time.Now().UTC().Format(time.RFC3339Nano), Content: final, Stats: newStats(raw, clean, final), Query: query}, nil
}

func ResultToMarkdown(result PackResult, includeStats bool) string {
	lines := []string{"---"}
	if result.Title != nil {
		b, _ := json.Marshal(*result.Title)
		lines = append(lines, "title: "+string(b))
	}
	source, _ := json.Marshal(result.SourceURL)
	fetched, _ := json.Marshal(result.FetchedAt)
	lines = append(lines, "source_url: "+string(source), "fetched_at: "+string(fetched))
	if result.Query != nil {
		b, _ := json.Marshal(*result.Query)
		lines = append(lines, "query: "+string(b))
	}
	if includeStats {
		lines = append(lines,
			fmt.Sprintf("raw_html_tokens: %d", result.Stats.RawHTMLTokens),
			fmt.Sprintf("clean_text_tokens: %d", result.Stats.CleanTextTokens),
			fmt.Sprintf("final_tokens: %d", result.Stats.FinalTokens),
			fmt.Sprintf("saved_tokens: %d", result.Stats.SavedTokens),
			fmt.Sprintf("reduction_percent: %.1f", result.Stats.ReductionPercent),
		)
	}
	lines = append(lines, "---", "", result.Content)
	return strings.TrimSpace(strings.Join(lines, "\n")) + "\n"
}

func StatsTable(result PackResult) string {
	return fmt.Sprintf("Raw input:   %s tokens\nClean text:  %s tokens\nFinal:       %s tokens\nSaved:       %s tokens\nReduction:   %.1f%%\n", comma(result.Stats.RawHTMLTokens), comma(result.Stats.CleanTextTokens), comma(result.Stats.FinalTokens), comma(result.Stats.SavedTokens), result.Stats.ReductionPercent)
}

func DefaultStatsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ctxpack", "stats.jsonl")
}

func RecordRun(result PackResult, statsPath string) error {
	if statsPath == "" {
		statsPath = DefaultStatsPath()
	}
	if err := os.MkdirAll(filepath.Dir(statsPath), 0o755); err != nil {
		return err
	}
	row := map[string]any{"source_url": result.SourceURL, "title": result.Title, "fetched_at": result.FetchedAt, "query": result.Query, "stats": result.Stats}
	b, err := json.Marshal(row)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(statsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

func LoadHistory(statsPath string) ([]map[string]any, error) {
	if statsPath == "" {
		statsPath = DefaultStatsPath()
	}
	b, err := os.ReadFile(statsPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row map[string]any
		if json.Unmarshal([]byte(line), &row) == nil {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

type HistorySummary struct {
	Runs             int     `json:"runs"`
	RawInputTokens   int     `json:"raw_input_tokens"`
	CleanTextTokens  int     `json:"clean_text_tokens"`
	FinalTokens      int     `json:"final_tokens"`
	SavedTokens      int     `json:"saved_tokens"`
	ReductionPercent float64 `json:"reduction_percent"`
}

func SummarizeHistory(rows []map[string]any) HistorySummary {
	s := HistorySummary{Runs: len(rows)}
	for _, row := range rows {
		stats, _ := row["stats"].(map[string]any)
		s.RawInputTokens += number(stats["raw_html_tokens"])
		s.CleanTextTokens += number(stats["clean_text_tokens"])
		s.FinalTokens += number(stats["final_tokens"])
	}
	s.SavedTokens = s.RawInputTokens - s.FinalTokens
	if s.SavedTokens < 0 {
		s.SavedTokens = 0
	}
	if s.RawInputTokens > 0 {
		s.ReductionPercent = math.Round((float64(s.SavedTokens)/float64(s.RawInputTokens))*1000) / 10
	}
	return s
}

func ResetHistory(statsPath string) (int, error) {
	if statsPath == "" {
		statsPath = DefaultStatsPath()
	}
	rows, err := LoadHistory(statsPath)
	if err != nil {
		return 0, err
	}
	if err := os.Remove(statsPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	return len(rows), nil
}

func HistoryTable(s HistorySummary) string {
	return fmt.Sprintf("Runs:        %s\nRaw input:   %s tokens\nClean text:  %s tokens\nFinal:       %s tokens\nSaved:       %s tokens\nReduction:   %.1f%%\n", comma(s.Runs), comma(s.RawInputTokens), comma(s.CleanTextTokens), comma(s.FinalTokens), comma(s.SavedTokens), s.ReductionPercent)
}

func number(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}

func comma(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	pre := len(s) % 3
	if pre == 0 {
		pre = 3
	}
	out = append(out, s[:pre]...)
	for i := pre; i < len(s); i += 3 {
		out = append(out, ',')
		out = append(out, s[i:i+3]...)
	}
	return string(out)
}
