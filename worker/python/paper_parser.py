#!/usr/bin/env python3
"""Bounded pypdf adapter for the registered CAPSuleRT paper.parse capability."""

import argparse
import io
import json
import sys


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--max-pages", type=int, required=True)
    parser.add_argument("--max-chars", type=int, required=True)
    args = parser.parse_args()
    if args.max_pages < 1 or args.max_pages > 256:
        raise ValueError("invalid max-pages")
    if args.max_chars < 1 or args.max_chars > 2_000_000:
        raise ValueError("invalid max-chars")

    data = sys.stdin.buffer.read(20 * 1024 * 1024 + 1)
    if len(data) > 20 * 1024 * 1024:
        raise ValueError("PDF exceeds parser input limit")
    if not data.startswith(b"%PDF-"):
        raise ValueError("PDF signature is missing")

    try:
        from pypdf import PdfReader
    except ImportError as exc:
        print("optional dependency pypdf is not installed", file=sys.stderr)
        return 3

    reader = PdfReader(io.BytesIO(data), strict=False)
    pages = []
    remaining = args.max_chars
    truncated = len(reader.pages) > args.max_pages
    for number, page in enumerate(reader.pages[: args.max_pages], start=1):
        text = page.extract_text() or ""
        if len(text) > remaining:
            text = text[:remaining]
            truncated = True
        pages.append({"number": number, "text": text})
        remaining -= len(text)
        if remaining <= 0:
            if number < len(reader.pages):
                truncated = True
            break
    json.dump(
        {
            "parser": "pypdf",
            "page_count": len(reader.pages),
            "truncated": truncated,
            "pages": pages,
        },
        sys.stdout,
        ensure_ascii=False,
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:  # bounded error text is captured by the Go worker
        print(f"paper parser error: {exc}", file=sys.stderr)
        raise SystemExit(2)
