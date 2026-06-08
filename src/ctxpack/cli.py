from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from urllib.error import URLError

from .core import (
    history_table,
    load_history,
    pack,
    record_run,
    reset_history,
    result_to_markdown,
    stats_table,
    summarize_history,
)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="ctxpack",
        description="Token-aware context extractor for AI agents.",
    )
    parser.add_argument("source", nargs="?", help="URL, local HTML/Markdown file, '-' for stdin, or command: stats/reset")
    parser.add_argument("extra", nargs="*", help=argparse.SUPPRESS)
    parser.add_argument("--query", help="Move sections related to this task toward the top")
    parser.add_argument("--json", action="store_true", help="Output structured JSON for agent tool consumption")
    parser.add_argument("--stats", action="store_true", help="Include per-run token savings with the packed content")
    parser.add_argument("--reset", action="store_true", help="With `stats`, reset cumulative stats after printing them")
    parser.add_argument("--yes", action="store_true", help="With `reset`, reset without an interactive prompt")
    parser.add_argument("--no-record", action="store_true", help="Do not add this run to cumulative stats")
    parser.add_argument("-o", "--output", help="Write output to a file instead of stdout")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)

    if args.source == "run":
        if not args.extra:
            print("ctxpack: source is required. Try `ctxpack run URL` or `ctxpack URL`.", file=sys.stderr)
            return 2
        args.source = args.extra[0]
        if len(args.extra) > 1:
            print(f"ctxpack: unexpected arguments: {' '.join(args.extra[1:])}", file=sys.stderr)
            return 2
    elif args.extra:
        print(f"ctxpack: unexpected arguments: {' '.join(args.extra)}", file=sys.stderr)
        return 2

    if args.source == "stats":
        rows = load_history()
        summary = summarize_history(rows)
        output = json.dumps(summary, ensure_ascii=False, indent=2) + "\n" if args.json else history_table(summary)
        if args.reset:
            reset_history()
            output += "Stats reset.\n"
        print(output, end="")
        return 0

    if args.source == "reset":
        if not args.yes:
            print("Use `ctxpack reset --yes` to reset cumulative stats.", file=sys.stderr)
            return 2
        count = reset_history()
        print(f"Reset {count} recorded run(s).")
        return 0

    if not args.source:
        print("ctxpack: source is required. Try `ctxpack URL` or `ctxpack stats`.", file=sys.stderr)
        return 2

    try:
        result = pack(args.source, query=args.query)
    except (URLError, TimeoutError, ConnectionError) as exc:
        print(f"ctxpack: network error (retriable): {exc}", file=sys.stderr)
        return 1
    except FileNotFoundError as exc:
        print(f"ctxpack: file not found: {exc.filename or exc}", file=sys.stderr)
        return 2
    except (ValueError, OSError) as exc:
        print(f"ctxpack: {exc}", file=sys.stderr)
        return 1

    if not args.no_record:
        record_run(result)

    if args.json:
        payload = result.to_dict()
        output = json.dumps(payload, ensure_ascii=False, indent=2) + "\n"
    else:
        output = result_to_markdown(result, include_stats=args.stats)
        if args.stats:
            output += "\n## Token savings for this run\n\n```text\n" + stats_table(result) + "```\n"

    if args.output:
        Path(args.output).write_text(output, encoding="utf-8")
    else:
        print(output, end="")
    return 0


if __name__ == "__main__":  # pragma: no cover
    raise SystemExit(main())
