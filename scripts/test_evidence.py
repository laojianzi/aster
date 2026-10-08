#!/usr/bin/env python3
"""Validate executable Go test evidence; absence and skipped tests are not passes."""
from __future__ import annotations

import argparse
import json
from pathlib import Path
import sys


def summarize(path: Path, required: list[str], suite: str) -> dict:
    started: set[tuple[str, str]] = set()
    completed: dict[tuple[str, str], str] = {}
    failed_packages: set[str] = set()
    successful_packages: set[str] = set()
    failed_seen: set[tuple[str, str]] = set()
    skipped_seen: set[tuple[str, str]] = set()
    with path.open(encoding="utf-8") as source:
        for line_number, line in enumerate(source, 1):
            if not line.strip():
                continue
            try:
                event = json.loads(line)
            except json.JSONDecodeError as exc:
                raise ValueError(f"invalid test JSON at line {line_number}") from exc
            action, package, test = event.get("Action"), event.get("Package", ""), event.get("Test")
            if test:
                key = package, test
                if action == "run":
                    started.add(key)
                    completed.pop(key, None)
                elif action in {"pass", "fail", "skip"}:
                    completed[key] = action
                    if action == "fail":
                        failed_seen.add(key)
                    if action == "skip":
                        skipped_seen.add(key)
            elif action == "fail":
                failed_packages.add(package)
            elif action == "pass":
                successful_packages.add(package)
    passed_names = {test for (_, test), action in completed.items() if action == "pass"}
    missing = sorted(set(required) - passed_names)
    failures = sorted(f"{pkg}:{name}" for pkg, name in failed_seen)
    skipped = sorted(f"{pkg}:{name}" for pkg, name in skipped_seen)
    unfinished = sorted(f"{pkg}:{name}" for pkg, name in started - completed.keys())
    incomplete_packages = sorted({pkg for pkg, _ in started} - successful_packages)
    result = {
        "suite": suite,
        "passed_tests": sum(action == "pass" for action in completed.values()),
        "failed_tests": failures,
        "skipped_tests": skipped,
        "unfinished_tests": unfinished,
        "failed_packages": sorted(failed_packages),
        "incomplete_packages": incomplete_packages,
        "missing_required_tests": missing,
    }
    result["valid"] = bool(started) and not any((missing, failures, skipped, unfinished, failed_packages, incomplete_packages))
    return result


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("jsonl", type=Path)
    parser.add_argument("--suite", required=True)
    parser.add_argument("--require", action="append", default=[])
    args = parser.parse_args()
    try:
        result = summarize(args.jsonl, args.require, args.suite)
    except (OSError, ValueError) as exc:
        print(str(exc), file=sys.stderr)
        return 1
    output = args.jsonl.with_name("summary.json")
    output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(result, ensure_ascii=False))
    return 0 if result["valid"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
