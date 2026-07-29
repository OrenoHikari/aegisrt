#!/usr/bin/env python3

import argparse
import json
import os
import sys
from pathlib import Path


def emit(event: str, **fields: object) -> None:
    print(
        json.dumps(
            {
                "source": "transaction-agent",
                "event": event,
                "pid": os.getpid(),
                **fields,
            }
        ),
        flush=True,
    )


def required_path(name: str) -> Path:
    value = os.environ.get(name)

    if not value:
        raise RuntimeError(
            f"required environment variable {name} is missing"
        )

    return Path(value).resolve()


def main() -> None:
    parser = argparse.ArgumentParser()

    parser.add_argument(
        "--mode",
        choices=("success", "fail", "symlink"),
        required=True,
    )

    parser.add_argument(
        "--label",
        required=True,
    )

    args = parser.parse_args()

    staging = required_path("AEGIS_OUTPUT_STAGING")
    transaction_id = os.environ.get(
        "AEGIS_OUTPUT_TRANSACTION_ID",
        "",
    )

    results = staging / "results"
    results.mkdir(parents=True, exist_ok=True)

    payload = {
        "agent_label": args.label,
        "mode": args.mode,
        "transaction_id": transaction_id,
        "working_directory": str(Path.cwd().resolve()),
        "workspace_root": os.environ.get(
            "AEGIS_WORKSPACE_ROOT",
            "",
        ),
    }

    result_path = results / "answer.json"

    result_path.write_text(
        json.dumps(payload, indent=2) + "\n",
        encoding="utf-8",
    )

    summary_path = results / "summary.txt"
    summary_path.write_text(
        f"completed by {args.label}\n",
        encoding="utf-8",
    )

    emit(
        "staged",
        mode=args.mode,
        label=args.label,
        staging=str(staging),
        transaction_id=transaction_id,
        result_file=str(result_path),
    )

    if args.mode == "symlink":
        link_path = results / "hostname-link"
        link_path.symlink_to("/etc/hostname")

        emit(
            "symlink_created",
            path=str(link_path),
        )

        return

    if args.mode == "fail":
        emit(
            "intentional_failure",
            label=args.label,
        )

        sys.exit(7)

    emit(
        "completed",
        label=args.label,
    )


if __name__ == "__main__":
    main()
