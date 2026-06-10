// Command ctxpack is a token-aware context extractor for AI agents.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strings"

	"github.com/atani/ctxpack/internal/ctxpack"
)

var version = "0.3.0" // x-release-please-version

const usage = `Usage: ctxpack [SOURCE] [flags]

Token-aware context extractor for AI agents.

Source:
  URL, local HTML/Markdown file, '-' for stdin, or a subcommand:
  run SOURCE    Same as 'ctxpack SOURCE'
  stats         Show cumulative token savings
  reset         Reset cumulative stats (requires --yes)

Flags:
  --query TEXT   Move sections related to this task toward the top
  --json         Output structured JSON for agent tool consumption
  --stats        Include per-run token savings with the packed content
  --reset        With 'stats', reset cumulative stats after printing them
  --yes          With 'reset', reset without an interactive prompt
  --no-record    Do not add this run to cumulative stats
  -o, --output FILE  Write output to a file instead of stdout
  --version      Show version
  -h, --help     Show this help
`

func main() { os.Exit(run(os.Args[1:])) }

func run(argv []string) int {
	opts, args, err := parse(argv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctxpack: %v\n", err)
		return 2
	}
	if opts.help {
		fmt.Print(usage)
		return 0
	}
	if opts.version {
		fmt.Printf("ctxpack %s\n", version)
		return 0
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "ctxpack: source is required. Try `ctxpack URL` or `ctxpack stats`.")
		return 2
	}
	source := args[0]
	if source == "run" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "ctxpack: source is required. Try `ctxpack run URL` or `ctxpack URL`.")
			return 2
		}
		source = args[1]
		if len(args) > 2 {
			fmt.Fprintf(os.Stderr, "ctxpack: unexpected arguments: %s\n", strings.Join(args[2:], " "))
			return 2
		}
	} else if len(args) > 1 {
		fmt.Fprintf(os.Stderr, "ctxpack: unexpected arguments: %s\n", strings.Join(args[1:], " "))
		return 2
	}

	switch source {
	case "stats":
		return runStats(opts)
	case "reset":
		return runReset(opts)
	default:
		return runPack(source, opts)
	}
}

func runStats(opts options) int {
	rows, err := ctxpack.LoadHistory("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctxpack: %v\n", err)
		return 1
	}
	summary := ctxpack.SummarizeHistory(rows)
	var out string
	if opts.jsonOut {
		out, err = toJSON(summary)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ctxpack: %v\n", err)
			return 1
		}
	} else {
		out = ctxpack.HistoryTable(summary)
	}
	if opts.reset {
		if _, err := ctxpack.ResetHistory(""); err != nil {
			fmt.Fprintf(os.Stderr, "ctxpack: %v\n", err)
			return 1
		}
		out += "Stats reset.\n"
	}
	fmt.Print(out)
	return 0
}

func runReset(opts options) int {
	if !opts.yes {
		fmt.Fprintln(os.Stderr, "Use `ctxpack reset --yes` to reset cumulative stats.")
		return 2
	}
	count, err := ctxpack.ResetHistory("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctxpack: %v\n", err)
		return 1
	}
	fmt.Printf("Reset %d recorded run(s).\n", count)
	return 0
}

func runPack(source string, opts options) int {
	result, err := ctxpack.Pack(source, opts.query, os.Stdin)
	if err != nil {
		return classifyError(err)
	}
	if !opts.noRecord {
		// A failed history append must not discard the successfully packed
		// content, so warn and keep going.
		if err := ctxpack.RecordRun(result, ""); err != nil {
			fmt.Fprintf(os.Stderr, "ctxpack: warning: could not record run stats: %v\n", err)
		}
	}
	var out string
	if opts.jsonOut {
		out, err = toJSON(result.ToJSONResult())
		if err != nil {
			fmt.Fprintf(os.Stderr, "ctxpack: %v\n", err)
			return 1
		}
	} else {
		out = ctxpack.ResultToMarkdown(result, opts.stats)
		if opts.stats {
			out += "\n## Token savings for this run\n\n```text\n" + ctxpack.StatsTable(result) + "```\n"
		}
	}
	if opts.output != "" {
		if err := os.WriteFile(opts.output, []byte(out), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "ctxpack: cannot write output: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Print(out)
	return 0
}

// toJSON marshals v indented and without HTML escaping, so <, > and & in the
// content survive byte-for-byte like Python's ensure_ascii=False output.
func toJSON(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return buf.String(), nil
}

type options struct {
	// query distinguishes "flag absent" (nil) from "--query ''" (pointer to
	// empty string) to keep the --json output identical to the Python CLI.
	query    *string
	jsonOut  bool
	stats    bool
	reset    bool
	yes      bool
	noRecord bool
	output   string
	version  bool
	help     bool
}

// parse is a hand-rolled flag parser because stdlib flag stops scanning at
// the first positional argument, while this CLI (like Python's argparse)
// accepts flags both before and after the source.
func parse(argv []string) (options, []string, error) {
	var opts options
	var args []string
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		switch {
		case arg == "--help" || arg == "-h":
			opts.help = true
		case arg == "--version":
			opts.version = true
		case arg == "--json":
			opts.jsonOut = true
		case arg == "--stats":
			opts.stats = true
		case arg == "--reset":
			opts.reset = true
		case arg == "--yes":
			opts.yes = true
		case arg == "--no-record":
			opts.noRecord = true
		case arg == "--query":
			i++
			if i >= len(argv) {
				return opts, nil, fmt.Errorf("flag needs an argument: %s", arg)
			}
			opts.query = &argv[i]
		case strings.HasPrefix(arg, "--query="):
			v := strings.TrimPrefix(arg, "--query=")
			opts.query = &v
		case arg == "-o" || arg == "--output":
			i++
			if i >= len(argv) {
				return opts, nil, fmt.Errorf("flag needs an argument: %s", arg)
			}
			opts.output = argv[i]
		case strings.HasPrefix(arg, "--output="):
			opts.output = strings.TrimPrefix(arg, "--output=")
		case strings.HasPrefix(arg, "-") && arg != "-":
			return opts, nil, fmt.Errorf("unknown flag: %s", arg)
		default:
			args = append(args, arg)
		}
	}
	return opts, args, nil
}

// classifyError maps failures to the Python CLI's exit codes: 2 for a missing
// file (usage-level error), 1 for network problems (printed as retriable,
// HTTP error statuses included) and everything else.
func classifyError(err error) int {
	var pathErr *fs.PathError
	var netErr net.Error
	var statusErr *ctxpack.HTTPStatusError
	if errors.As(err, &pathErr) && errors.Is(pathErr.Err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "ctxpack: file not found: %s\n", pathErr.Path)
		return 2
	}
	if errors.As(err, &netErr) || errors.As(err, &statusErr) {
		fmt.Fprintf(os.Stderr, "ctxpack: network error (retriable): %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "ctxpack: %v\n", err)
	return 1
}
