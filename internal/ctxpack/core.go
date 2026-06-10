// Package ctxpack turns noisy HTML (remote or local) into compact Markdown
// for AI-agent consumption, estimates the token savings, and keeps a
// cumulative history of runs.
package ctxpack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// MaxFetchSize and MaxFileSize guard against memory exhaustion from very
// large remote responses or local files. They are variables (not constants)
// so tests can lower them to exercise the limits without multi-megabyte
// fixtures.
var (
	MaxFetchSize = 50 * 1024 * 1024
	MaxFileSize  = 100 * 1024 * 1024
)

// UserAgent identifies ctxpack to remote servers. Deliberately version-free
// so it cannot drift from the release version embedded in the binary.
const UserAgent = "ctxpack (+https://github.com/atani/ctxpack)"

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
	// Go's \w and \s are ASCII-only (unlike Python's re on str), so word and
	// whitespace classes are spelled out to keep CJK queries and full-width
	// spaces working after the port.
	wordRe        = regexp.MustCompile(`[\p{L}\p{N}_]+`)
	inlineSpaceRe = regexp.MustCompile(`[\s\p{Z}\x{85}]+`)
	headingLineRe = regexp.MustCompile(`^#{1,6} `)
)

// TokenStats reports the token estimates before and after packing one source.
type TokenStats struct {
	RawHTMLTokens    int     `json:"raw_html_tokens"`
	CleanTextTokens  int     `json:"clean_text_tokens"`
	FinalTokens      int     `json:"final_tokens"`
	SavedTokens      int     `json:"saved_tokens"`
	ReductionPercent float64 `json:"reduction_percent"`
}

// PackResult is the outcome of packing one source.
type PackResult struct {
	SourceURL string
	Title     *string
	FetchedAt string
	Content   string
	Stats     TokenStats
	Query     *string
}

// JSONResult is the stable agent-facing JSON schema printed by --json.
type JSONResult struct {
	OK      bool              `json:"ok"`
	Source  map[string]string `json:"source"`
	Title   *string           `json:"title"`
	Query   *string           `json:"query"`
	Content map[string]string `json:"content"`
	Stats   TokenStats        `json:"stats"`
}

// ToJSONResult converts the result into the --json output schema.
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

// EstimateTokens returns a fast model-agnostic token estimate suitable for
// before/after comparison.
//
// Heuristic: CJK characters count as ~0.8 tokens each, everything else as
// ~1 token per 4 characters. This is an approximation for relative
// before/after savings, not an exact per-model token count.
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

// savedTokens never reports negative savings, even when packing grows the
// estimate (e.g. heading markers added to sparse text).
func savedTokens(raw, final int) int {
	if raw > final {
		return raw - final
	}
	return 0
}

// reductionPercent returns saved/raw as a percentage rounded to one decimal.
func reductionPercent(saved, raw int) float64 {
	if raw <= 0 {
		return 0
	}
	return math.Round((float64(saved)/float64(raw))*1000) / 10
}

func newStats(raw, clean, final string) TokenStats {
	rawT := EstimateTokens(raw)
	cleanT := EstimateTokens(clean)
	finalT := EstimateTokens(final)
	saved := savedTokens(rawT, finalT)
	return TokenStats{
		RawHTMLTokens:    rawT,
		CleanTextTokens:  cleanT,
		FinalTokens:      finalT,
		SavedTokens:      saved,
		ReductionPercent: reductionPercent(saved, rawT),
	}
}

// HTTPStatusError reports a non-success HTTP response. The CLI treats it as a
// retriable network error, matching the Python version's HTTPError handling.
type HTTPStatusError struct {
	Code   int
	Status string
}

func (e *HTTPStatusError) Error() string {
	return "unexpected HTTP status: " + e.Status
}

// FetchURL downloads url and returns its body decoded to UTF-8.
//
// Non-2xx/3xx responses become an *HTTPStatusError instead of being packed as
// content. The size limit applies to the raw (pre-decode) bytes, and the
// response's declared charset (Content-Type header) is honored with UTF-8 as
// the fallback, both matching the original Python implementation.
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
	if res.StatusCode >= 400 {
		return "", &HTTPStatusError{Code: res.StatusCode, Status: res.Status}
	}
	if res.ContentLength > int64(MaxFetchSize) {
		return "", fmt.Errorf("response too large: %d bytes (limit %d)", res.ContentLength, MaxFetchSize)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, int64(MaxFetchSize)+1))
	if err != nil {
		return "", err
	}
	if len(raw) > MaxFetchSize {
		return "", fmt.Errorf("response exceeds %d bytes", MaxFetchSize)
	}
	return decodeBody(raw, res.Header.Get("Content-Type")), nil
}

// decodeBody converts raw response bytes to UTF-8 using the charset declared
// in the Content-Type header, defaulting to UTF-8 when absent or unknown.
// Deliberately no content sniffing: the HTML-spec default of windows-1252
// would mangle the many UTF-8 pages that omit a charset declaration.
func decodeBody(raw []byte, contentType string) string {
	if _, params, err := mime.ParseMediaType(contentType); err == nil {
		if label, ok := params["charset"]; ok {
			if enc, _ := charset.Lookup(label); enc != nil {
				if decoded, err := enc.NewDecoder().Bytes(raw); err == nil {
					raw = decoded
				}
			}
		}
	}
	return string(bytes.ToValidUTF8(raw, []byte("�")))
}

// ReadSource resolves source ("-" for stdin, http(s) URL, or file path) and
// returns a display name plus the UTF-8 content.
func ReadSource(source string, stdin io.Reader) (string, string, error) {
	if source == "-" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", "", err
		}
		return "stdin", string(bytes.ToValidUTF8(b, []byte("�"))), nil
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
	// Read one byte past the limit instead of trusting Stat() so the cap also
	// applies to special files (e.g. /dev/zero, FIFOs) whose length cannot be
	// learned up front.
	b, err := io.ReadAll(io.LimitReader(f, int64(MaxFileSize)+1))
	if err != nil {
		return "", "", err
	}
	if len(b) > MaxFileSize {
		return "", "", fmt.Errorf("file too large: exceeds %d bytes", MaxFileSize)
	}
	return source, string(bytes.ToValidUTF8(b, []byte("�"))), nil
}

// walkFrame is one step of the iterative DOM traversal in HTMLToMarkdown.
// An explicit stack (rather than recursion) keeps deeply nested untrusted
// HTML from exhausting the goroutine stack.
type walkFrame struct {
	node     *html.Node
	skipping bool
	inTitle  bool
	// exit marks a post-order visit: emit the trailing newline for a block
	// element after all of its children have been processed.
	exit bool
}

// HTMLToMarkdown strips boilerplate from HTML and returns the document title
// (nil when absent) plus a compact Markdown rendering of the main content.
func HTMLToMarkdown(input string) (*string, string) {
	doc, err := html.Parse(strings.NewReader(input))
	if err != nil {
		return nil, NormalizeText(input)
	}
	var parts []string
	var titleParts []string
	stack := []walkFrame{{node: doc}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.exit {
			parts = append(parts, "\n")
			continue
		}
		n, skipping, inTitle := f.node, f.skipping, f.inTitle
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
		// The exit frame is pushed before the children so it pops after all
		// of them (LIFO); children are pushed in reverse to pop in document
		// order.
		if n.Type == html.ElementNode && !skipping && blockTags[strings.ToLower(n.Data)] {
			stack = append(stack, walkFrame{exit: true})
		}
		for c := n.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, walkFrame{node: c, skipping: skipping, inTitle: inTitle})
		}
	}
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

// NormalizeInline collapses all whitespace runs (including Unicode spaces
// such as U+3000) into single spaces and trims the result.
func NormalizeInline(text string) string {
	return strings.TrimSpace(inlineSpaceRe.ReplaceAllString(text, " "))
}

// NormalizeText canonicalizes line endings and collapses excess blank lines
// and indentation left over from HTML extraction.
func NormalizeText(text string) string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	text = spaceRe.ReplaceAllString(text, " ")
	text = lineSpaceRe.ReplaceAllString(text, "\n")
	text = manyNewlinesRe.ReplaceAllString(text, "\n\n")
	text = emptyHeadingRe.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

// SplitSections splits Markdown into sections at ATX headings (1-6 "#"
// followed by a space, mirroring Python's `^#{1,6} ` rule). When no headings
// exist it falls back to paragraph (blank-line) splitting.
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
		if headingLineRe.MatchString(line) {
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

// queryTerms lowercases the query and keeps word-ish tokens of two or more
// runes. wordRe is Unicode-aware so CJK queries produce usable terms.
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

// scoreSectionTerms scores relevance by case-insensitive substring matches:
// +2 per body occurrence (overlaps counted) and an extra +3 per term found in
// a leading heading. No stemming or semantic matching is performed.
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

// ApplyQuery moves likely relevant sections first while keeping the full
// compact context: sections are reordered by descending relevance score, ties
// keep their original document order (stable sort), and nothing is dropped.
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

// Pack reads source, converts HTML to compact Markdown (plain text is only
// normalized), applies the optional query reordering, and reports the token
// savings.
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

// ResultToMarkdown renders the packed content with a YAML front-matter
// header. String values are JSON-quoted so titles containing "---" or
// newlines cannot break the front-matter delimiters.
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

// StatsTable renders one run's token savings as an aligned text table.
func StatsTable(result PackResult) string {
	return fmt.Sprintf("Raw input:   %s tokens\nClean text:  %s tokens\nFinal:       %s tokens\nSaved:       %s tokens\nReduction:   %.1f%%\n", comma(result.Stats.RawHTMLTokens), comma(result.Stats.CleanTextTokens), comma(result.Stats.FinalTokens), comma(result.Stats.SavedTokens), result.Stats.ReductionPercent)
}

// DefaultStatsPath returns ~/.ctxpack/stats.jsonl, or an error when the home
// directory cannot be determined (it must not fall back to the working
// directory, which would scatter .ctxpack/ directories silently).
func DefaultStatsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".ctxpack", "stats.jsonl"), nil
}

// RunRecord is one recorded run in the stats history file. The JSON schema
// matches the Python version, so existing stats.jsonl files keep loading.
type RunRecord struct {
	SourceURL string     `json:"source_url"`
	Title     *string    `json:"title"`
	FetchedAt string     `json:"fetched_at"`
	Query     *string    `json:"query"`
	Stats     TokenStats `json:"stats"`
}

func resolveStatsPath(statsPath string) (string, error) {
	if statsPath != "" {
		return statsPath, nil
	}
	return DefaultStatsPath()
}

// RecordRun appends the run to the history file (default: ~/.ctxpack/stats.jsonl).
// The file is created 0600 (directory 0700) because source URLs and queries
// amount to a browsing history.
func RecordRun(result PackResult, statsPath string) error {
	statsPath, err := resolveStatsPath(statsPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(statsPath), 0o700); err != nil {
		return err
	}
	row := RunRecord{SourceURL: result.SourceURL, Title: result.Title, FetchedAt: result.FetchedAt, Query: result.Query, Stats: result.Stats}
	b, err := json.Marshal(row)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(statsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// LoadHistory reads the history file, skipping blank and corrupt lines.
// A missing file is an empty history, not an error.
func LoadHistory(statsPath string) ([]RunRecord, error) {
	statsPath, err := resolveStatsPath(statsPath)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(statsPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rows []RunRecord
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row RunRecord
		if json.Unmarshal([]byte(line), &row) == nil {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// HistorySummary aggregates token savings across all recorded runs.
type HistorySummary struct {
	Runs             int     `json:"runs"`
	RawInputTokens   int     `json:"raw_input_tokens"`
	CleanTextTokens  int     `json:"clean_text_tokens"`
	FinalTokens      int     `json:"final_tokens"`
	SavedTokens      int     `json:"saved_tokens"`
	ReductionPercent float64 `json:"reduction_percent"`
}

// SummarizeHistory totals the per-run stats into one summary.
func SummarizeHistory(rows []RunRecord) HistorySummary {
	s := HistorySummary{Runs: len(rows)}
	for _, row := range rows {
		s.RawInputTokens += row.Stats.RawHTMLTokens
		s.CleanTextTokens += row.Stats.CleanTextTokens
		s.FinalTokens += row.Stats.FinalTokens
	}
	s.SavedTokens = savedTokens(s.RawInputTokens, s.FinalTokens)
	s.ReductionPercent = reductionPercent(s.SavedTokens, s.RawInputTokens)
	return s
}

// ResetHistory deletes the history file and returns how many runs it held.
func ResetHistory(statsPath string) (int, error) {
	statsPath, err := resolveStatsPath(statsPath)
	if err != nil {
		return 0, err
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

// HistoryTable renders the cumulative summary as an aligned text table.
func HistoryTable(s HistorySummary) string {
	return fmt.Sprintf("Runs:        %s\nRaw input:   %s tokens\nClean text:  %s tokens\nFinal:       %s tokens\nSaved:       %s tokens\nReduction:   %.1f%%\n", comma(s.Runs), comma(s.RawInputTokens), comma(s.CleanTextTokens), comma(s.FinalTokens), comma(s.SavedTokens), s.ReductionPercent)
}

// comma formats n with thousands separators (1234567 -> "1,234,567").
func comma(n int) string {
	s := strconv.Itoa(n)
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
