package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strings"

	"github.com/atani/ctxpack/internal/ctxpack"
)

var version = "0.2.0" // x-release-please-version

func main() { os.Exit(run(os.Args[1:])) }

func run(argv []string) int {
	opts, args, err := parse(argv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
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

	if source == "stats" {
		rows, err := ctxpack.LoadHistory("")
		if err != nil {
			fmt.Fprintf(os.Stderr, "ctxpack: %v\n", err)
			return 1
		}
		summary := ctxpack.SummarizeHistory(rows)
		var out string
		if opts.jsonOut {
			b, _ := json.MarshalIndent(summary, "", "  ")
			out = string(b) + "\n"
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
	if source == "reset" {
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
	var query *string
	if opts.query != "" {
		query = &opts.query
	}
	result, err := ctxpack.Pack(source, query, os.Stdin)
	if err != nil {
		return classifyError(err)
	}
	if !opts.noRecord {
		if err := ctxpack.RecordRun(result, ""); err != nil {
			fmt.Fprintf(os.Stderr, "ctxpack: %v\n", err)
			return 1
		}
	}
	var out string
	if opts.jsonOut {
		b, _ := json.MarshalIndent(result.ToJSONResult(), "", "  ")
		out = string(b) + "\n"
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

type options struct {
	query    string
	jsonOut  bool
	stats    bool
	reset    bool
	yes      bool
	noRecord bool
	output   string
	version  bool
}

func parse(argv []string) (options, []string, error) {
	var opts options
	var args []string
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		switch {
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
		case arg == "--query" || arg == "-query":
			i++
			if i >= len(argv) {
				return opts, nil, fmt.Errorf("flag needs an argument: %s", arg)
			}
			opts.query = argv[i]
		case strings.HasPrefix(arg, "--query="):
			opts.query = strings.TrimPrefix(arg, "--query=")
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

func classifyError(err error) int {
	var pathErr *fs.PathError
	var netErr net.Error
	if errors.As(err, &pathErr) && errors.Is(pathErr.Err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "ctxpack: file not found: %s\n", pathErr.Path)
		return 2
	}
	if errors.As(err, &netErr) {
		fmt.Fprintf(os.Stderr, "ctxpack: network error (retriable): %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "ctxpack: %v\n", err)
	return 1
}
