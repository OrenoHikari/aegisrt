#!/usr/bin/env python3

import argparse
import hashlib
import json
import os
from pathlib import Path


def emit(event: str, **fields: object) -> None:
    print(
        json.dumps(
            {
                "source": "workspace-agent",
                "event": event,
                "pid": os.getpid(),
                **fields,
            }
        ),
        flush=True,
    )


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def required_environment(name: str) -> Path:
    value = os.environ.get(name)

    if not value:
        raise RuntimeError(
            f"required environment variable {name} is missing"
        )

    return Path(value).resolve()


def main() -> None:
    parser = argparse.ArgumentParser()

    parser.add_argument(
        "--agent-label",
        required=True,
    )

    args = parser.parse_args()

    workspace = required_environment("AEGIS_WORKSPACE_ROOT")
    inputs = required_environment("AEGIS_CONTEXT_INPUTS")
    private = required_environment("AEGIS_CONTEXT_PRIVATE")
    manifest = required_environment("AEGIS_CONTEXT_MANIFEST")

    if Path.cwd().resolve() != workspace:
        raise RuntimeError(
            f"unexpected working directory: {Path.cwd()}"
        )

    input_files = sorted(inputs.rglob("*.ctx"))
    private_files = sorted(private.rglob("*.ctx"))

    if not input_files:
        raise RuntimeError("no read-only contexts were materialized")

    if len(input_files) != len(private_files):
        raise RuntimeError(
            "read-only and private context counts do not match"
        )

    input_data = input_files[0].read_bytes()
    private_initial = private_files[0].read_bytes()

    if input_data != private_initial:
        raise RuntimeError(
            "private context does not initially match read-only input"
        )

    private_modified = (
        f"private update by {args.agent_label}\n"
    ).encode()

    private_files[0].write_bytes(private_modified)

    input_after = input_files[0].read_bytes()
    private_after = private_files[0].read_bytes()

    if input_after != input_data:
        raise RuntimeError(
            "modifying private context changed read-only input"
        )

    emit(
        "result",
        agent_label=args.agent_label,
        workspace=str(workspace),
        manifest=str(manifest),
        input_file=str(input_files[0]),
        private_file=str(private_files[0]),
        input_sha256=sha256(input_after),
        private_sha256=sha256(private_after),
        private_diverged=private_after != input_after,
        context_count=len(input_files),
    )


if __name__ == "__main__":
    main()
