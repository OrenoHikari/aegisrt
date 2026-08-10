#!/usr/bin/env python3

"""Trusted local capabilities executed only through the CAPSuleRT Runtime."""

import argparse
import csv
import json
import os
import statistics
from pathlib import Path
from typing import Any


MAX_TEXT_PREVIEW = 32 * 1024
MAX_DIRECTORY_ENTRIES = 500


def required_path(name: str) -> Path:
    value = os.environ.get(name, "").strip()
    if not value:
        raise RuntimeError(f"required environment variable {name} is missing")
    return Path(value).resolve()


def emit(event: str, **fields: object) -> None:
    print(
        json.dumps(
            {
                "source": "cognitive-agent",
                "event": event,
                "pid": os.getpid(),
                "task_id": os.environ.get("CAPSULE_TASK_ID", ""),
                "capability": os.environ.get("CAPSULE_TASK_CAPABILITY", ""),
                **fields,
            },
            ensure_ascii=False,
        ),
        flush=True,
    )


def task_description() -> str:
    return os.environ.get("CAPSULE_TASK_DESCRIPTION", "").strip()


def arguments() -> dict[str, Any]:
    raw = os.environ.get("CAPSULE_TASK_ARGUMENTS_JSON", "").strip() or "{}"
    value = json.loads(raw)
    if not isinstance(value, dict):
        raise RuntimeError("structured task arguments must be a JSON object")
    return value


def ensure_beneath(path: Path, root: Path, label: str) -> None:
    try:
        path.resolve().relative_to(root.resolve())
    except ValueError as error:
        raise RuntimeError(f"{label} escapes configured root: {path}") from error


def scoped_argument_path() -> Path:
    value = arguments().get("path")
    if not isinstance(value, str) or not value.strip():
        raise RuntimeError("required string argument path is missing")
    root = required_path("CAPSULE_CAPABILITY_ROOT")
    path = Path(value).resolve()
    ensure_beneath(path, root, "capability path")
    return path


def dependency_results() -> list[tuple[str, dict[str, Any], str]]:
    raw = os.environ.get("AEGIS_DEPENDENCY_OUTPUTS_JSON", "").strip()
    if not raw:
        raise RuntimeError("verified dependency output metadata is missing")
    dependencies = json.loads(raw)
    if not dependencies:
        raise RuntimeError("no dependency outputs were supplied")

    collected: list[tuple[str, dict[str, Any], str]] = []
    for agent_id in sorted(dependencies):
        output = dependencies[agent_id]
        if not output.get("verified"):
            raise RuntimeError(f"dependency {agent_id} is not verified")
        commit_path = Path(output["commit_path"]).resolve()
        manifest_path = Path(output["manifest_path"]).resolve()
        ensure_beneath(manifest_path, commit_path, "manifest path")
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        allowed = {artifact["path"] for artifact in manifest.get("files", [])}

        structured: dict[str, Any] = {}
        text = ""
        if "result.json" in allowed:
            result_path = (commit_path / "result.json").resolve()
            ensure_beneath(result_path, commit_path, "result path")
            decoded = json.loads(result_path.read_text(encoding="utf-8"))
            if isinstance(decoded, dict):
                structured = decoded
        if "result.txt" in allowed:
            text_path = (commit_path / "result.txt").resolve()
            ensure_beneath(text_path, commit_path, "result path")
            text = text_path.read_text(encoding="utf-8", errors="replace")
        if not structured and not text:
            raise RuntimeError(f"dependency {agent_id} has no readable result artifact")
        collected.append((agent_id, structured, text))
    return collected


def filesystem_stat() -> tuple[dict[str, Any], str]:
    path = scoped_argument_path()
    if not path.exists():
        result = {"path": str(path), "exists": False, "kind": "missing"}
        return result, f"Path does not exist: {path}\n"
    info = path.stat()
    kind = "directory" if path.is_dir() else "file" if path.is_file() else "other"
    result = {
        "path": str(path),
        "exists": True,
        "kind": kind,
        "size_bytes": info.st_size,
        "extension": path.suffix.lower(),
        "modified_unix": int(info.st_mtime),
    }
    return result, json.dumps(result, ensure_ascii=False, indent=2) + "\n"


def filesystem_list() -> tuple[dict[str, Any], str]:
    path = scoped_argument_path()
    if not path.exists():
        result = {"path": str(path), "exists": False, "entries": []}
        return result, f"Directory does not exist: {path}\n"
    if not path.is_dir():
        raise RuntimeError(f"filesystem.list path is not a directory: {path}")
    entries: list[dict[str, Any]] = []
    for child in sorted(path.iterdir(), key=lambda item: item.name)[:MAX_DIRECTORY_ENTRIES]:
        entries.append(
            {
                "name": child.name,
                "kind": "directory" if child.is_dir() else "file" if child.is_file() else "other",
                "size_bytes": child.stat().st_size,
                "extension": child.suffix.lower(),
            }
        )
    result = {
        "path": str(path),
        "exists": True,
        "entries": entries,
        "truncated": len(list(path.iterdir())) > MAX_DIRECTORY_ENTRIES,
    }
    lines = [f"Directory: {path}"]
    lines.extend(f"- {entry['name']} ({entry['kind']}, {entry['size_bytes']} bytes)" for entry in entries)
    return result, "\n".join(lines) + "\n"


def context_file_bytes(path: Path) -> bytes:
    if not path.exists() or not path.is_file():
        raise RuntimeError(f"input file does not exist or is not regular: {path}")
    inputs_raw = os.environ.get("AEGIS_CONTEXT_INPUTS", "").strip()
    if inputs_raw:
        inputs = Path(inputs_raw).resolve()
        files = sorted(item for item in inputs.rglob("*.ctx") if item.is_file())
        if len(files) == 1:
            return files[0].read_bytes()
    return path.read_bytes()


def inspect_file() -> tuple[dict[str, Any], str]:
    path = scoped_argument_path()
    data = context_file_bytes(path)
    text = data.decode("utf-8", errors="replace")
    preview = text[:MAX_TEXT_PREVIEW]
    result = {
        "path": str(path),
        "bytes": len(data),
        "lines": len(text.splitlines()),
        "extension": path.suffix.lower(),
        "content": preview,
        "truncated": len(text) > len(preview),
    }
    rendered = (
        f"File: {path}\nBytes: {len(data)}\nLines: {len(text.splitlines())}\n\n"
        f"Content:\n{preview.rstrip()}\n"
    )
    return result, rendered


def value_type(value: Any) -> str:
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "boolean"
    if isinstance(value, (int, float)):
        return "number"
    if isinstance(value, dict):
        return "object"
    if isinstance(value, list):
        return "array"
    return "string"


def normalize_rows(payload: Any) -> list[dict[str, Any]]:
    if isinstance(payload, list):
        return [item if isinstance(item, dict) else {"value": item} for item in payload]
    if isinstance(payload, dict):
        for value in payload.values():
            if isinstance(value, list) and all(isinstance(item, dict) for item in value):
                return value
        return [payload]
    return [{"value": payload}]


def profile_rows(rows: list[dict[str, Any]]) -> dict[str, Any]:
    fields = sorted({str(key) for row in rows for key in row})[:100]
    types: dict[str, list[str]] = {}
    missing: dict[str, int] = {}
    statistics_result: dict[str, dict[str, float | int]] = {}
    for field in fields:
        values = [row.get(field) for row in rows]
        types[field] = sorted({value_type(value) for value in values if value is not None}) or ["null"]
        missing[field] = sum(value is None or value == "" for value in values)
        numeric = [float(value) for value in values if isinstance(value, (int, float)) and not isinstance(value, bool)]
        if numeric:
            statistics_result[field] = {
                "count": len(numeric),
                "min": min(numeric),
                "max": max(numeric),
                "mean": statistics.fmean(numeric),
            }
    return {
        "rows": len(rows),
        "fields": fields,
        "types": types,
        "missing_values": missing,
        "statistics": statistics_result,
        "sample": rows[:5],
    }


def data_inspect() -> tuple[dict[str, Any], str]:
    path = scoped_argument_path()
    data = context_file_bytes(path)
    extension = path.suffix.lower()
    if extension == ".json":
        payload = json.loads(data.decode("utf-8"))
        rows = normalize_rows(payload)
        data_format = "json"
    elif extension == ".csv":
        decoded = data.decode("utf-8-sig")
        rows = [dict(row) for row in csv.DictReader(decoded.splitlines())]
        data_format = "csv"
        for row in rows:
            for key, value in list(row.items()):
                if value is None or value == "":
                    continue
                try:
                    row[key] = float(value) if "." in value else int(value)
                except ValueError:
                    pass
    else:
        raise RuntimeError(f"data.inspect supports only CSV or JSON, got {extension or 'no extension'}")
    result = {"path": str(path), "format": data_format, **profile_rows(rows)}
    rendered = (
        f"Data file: {path}\nFormat: {data_format}\nRows: {result['rows']}\n"
        f"Fields: {', '.join(result['fields'])}\n"
        f"Types: {json.dumps(result['types'], ensure_ascii=False)}\n"
        f"Missing values: {json.dumps(result['missing_values'], ensure_ascii=False)}\n"
        f"Statistics: {json.dumps(result['statistics'], ensure_ascii=False)}\n"
        f"Sample: {json.dumps(result['sample'], ensure_ascii=False)}\n"
    )
    return result, rendered


def analyze() -> tuple[dict[str, Any], str]:
    sources = dependency_results()
    facts: list[str] = []
    numeric_summary: dict[str, Any] = {}
    for agent_id, structured, text in sources:
        if structured.get("format"):
            facts.append(
                f"{agent_id} contains {structured.get('rows', 0)} {structured['format'].upper()} rows "
                f"with fields {', '.join(structured.get('fields', []))}."
            )
            numeric_summary.update(structured.get("statistics", {}))
        elif structured.get("entries") is not None:
            names = [entry.get("name", "") for entry in structured.get("entries", [])]
            facts.append(f"{agent_id} observed workspace entries: {', '.join(names)}.")
        elif text:
            facts.append(f"{agent_id}: {text.strip()[:500]}")
    if not facts:
        facts.append("Verified dependencies contained no analyzable facts.")
    result = {
        "question": arguments().get("question", task_description()),
        "source_tasks": [agent_id for agent_id, _, _ in sources],
        "facts": facts,
        "numeric_summary": numeric_summary,
    }
    rendered = "Analysis\n" + "\n".join(f"- {fact}" for fact in facts)
    if numeric_summary:
        rendered += "\nNumeric statistics: " + json.dumps(numeric_summary, ensure_ascii=False)
    return result, rendered + "\n"


def summarize() -> tuple[dict[str, Any], str]:
    sources = dependency_results()
    parts = [text.strip() for _, _, text in sources if text.strip()]
    if not parts:
        parts = [json.dumps(value, ensure_ascii=False) for _, value, _ in sources]
    body = "\n\n".join(parts)
    if len(body) > 4000:
        body = body[:4000].rstrip() + "\n[truncated]"
    focus = arguments().get("question") or task_description()
    summary = (
        f"Goal: {focus}\n"
        f"Based on verified tasks: {', '.join(agent_id for agent_id, _, _ in sources)}\n\n{body}"
    )
    return {"summary": summary, "source_tasks": [item[0] for item in sources]}, summary + "\n"


def main() -> None:
    parser = argparse.ArgumentParser(description="CAPSuleRT cognitive capability worker")
    parser.add_argument(
        "--action",
        choices=("filesystem_list", "filesystem_stat", "inspect_file", "data_inspect", "analyze", "summarize"),
        required=True,
    )
    args = parser.parse_args()
    staging = required_path("AEGIS_OUTPUT_STAGING")
    staging.mkdir(parents=True, exist_ok=True)
    emit("started")

    handlers = {
        "filesystem_list": filesystem_list,
        "filesystem_stat": filesystem_stat,
        "inspect_file": inspect_file,
        "data_inspect": data_inspect,
        "analyze": analyze,
        "summarize": summarize,
    }
    result, rendered = handlers[args.action]()
    json_output = staging / "result.json"
    text_output = staging / "result.txt"
    json_output.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    text_output.write_text(rendered, encoding="utf-8")
    emit("completed", output=str(text_output), bytes=len(rendered.encode("utf-8")))


if __name__ == "__main__":
    main()
