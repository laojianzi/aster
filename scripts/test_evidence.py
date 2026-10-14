#!/usr/bin/env python3
"""Validate executable Go test evidence; absence and skipped tests are not passes."""
from __future__ import annotations

import argparse
import json
from pathlib import Path
import sys


# Scope-specific mandatory names supplement, never replace, workflow --require
# arguments. Keep additions here so the three existing CI suites cannot report
# success while omitting an admitted feature increment.
INTEGER_MAP_REQUIRED = {
    "unit-native": (
        "TestSchemaIntegerMapKeysExactIdentity",
        "TestSchemaIntegerMapKeysRuntimeAndMetadataBoundaries",
        "TestSchemaIntegerMapKeysBudgetsReferencesAndPreservation",
        "TestNativeSchemaIntegerMapKeysHistoryAndNavigation",
        "TestNativeSchemaIntegerMapKeysMinimumWindowIncomplete",
    ),
    "native-os-interactions": (
        "TestNativeSchemaIntegerMapKeysHistoryAndNavigation",
        "TestNativeSchemaIntegerMapKeysMinimumWindowIncomplete",
    ),
    "real-cluster-native": (
        "TestRealSchemaIntegerMapKeysAgreeWithDryRun",
        "TestNativeSchemaIntegerMapKeysAgainstRealCluster",
    ),
}


INTEGER_SET_REQUIRED = {
    "unit-native": (
        "TestSchemaIntegerSetExactIdentity",
        "TestSchemaIntegerSetRuntimeAndMetadataBoundaries",
        "TestSchemaIntegerSetBudgetsReferencesAndPreservation",
        "TestNativeSchemaIntegerSetHistoryAndNavigation",
        "TestNativeSchemaIntegerSetMinimumWindowIncomplete",
    ),
    "native-os-interactions": (
        "TestNativeSchemaIntegerSetHistoryAndNavigation",
        "TestNativeSchemaIntegerSetMinimumWindowIncomplete",
    ),
    "real-cluster-native": (
        "TestRealSchemaIntegerSetAgreeWithDryRun",
        "TestNativeSchemaIntegerSetAgainstRealCluster",
    ),
}


MANIFEST_CLIPBOARD_REQUIRED = {
    "unit-native": (
        "TestClipboardBudgetConservativeBounds",
        "TestNativeManifestClipboardPreservesHistoryAndSelection",
        "TestNativeManifestClipboardMinimumWindowAndComposition",
        "TestNativeManifestClipboardStaleCallbacksAndTeardown",
        "TestNativeManifestClipboardRevokesUnsentPreview",
    ),
    "native-os-interactions": (
        "TestNativeManifestClipboardPreservesHistoryAndSelection",
        "TestNativeManifestClipboardMinimumWindowAndComposition",
        "TestNativeManifestClipboardStaleCallbacksAndTeardown",
        "TestNativeManifestClipboardRevokesUnsentPreview",
    ),
    "real-cluster-native": (
        "TestNativeManifestClipboardAgainstRealCluster",
    ),
}


BOOLEAN_COLLECTIONS_REQUIRED = {
    "unit-native": (
        "TestSchemaBooleanCollectionsFiniteIdentity",
        "TestSchemaBooleanCollectionsRuntimeAndMetadataBoundaries",
        "TestSchemaBooleanCollectionsBudgetsReferencesAndPreservation",
        "TestNativeSchemaBooleanCollectionsHistoryAndNavigation",
        "TestNativeSchemaBooleanCollectionsMinimumWindowIncomplete",
        "TestNativeSchemaBooleanCollectionsStaleResults",
    ),
    "native-os-interactions": (
        "TestNativeSchemaBooleanCollectionsHistoryAndNavigation",
        "TestNativeSchemaBooleanCollectionsMinimumWindowIncomplete",
        "TestNativeSchemaBooleanCollectionsStaleResults",
    ),
    "real-cluster-native": (
        "TestRealSchemaBooleanCollectionsAgreeWithDryRun",
        "TestNativeSchemaBooleanSetAgainstRealCluster",
        "TestNativeSchemaBooleanMapKeysAgainstRealCluster",
    ),
}


def required_for_suite(suite: str, explicit: list[str]) -> list[str]:
    return list(dict.fromkeys([*explicit, *INTEGER_MAP_REQUIRED.get(suite, ()), *INTEGER_SET_REQUIRED.get(suite, ()), *MANIFEST_CLIPBOARD_REQUIRED.get(suite, ()), *BOOLEAN_COLLECTIONS_REQUIRED.get(suite, ())]))


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
        result = summarize(args.jsonl, required_for_suite(args.suite, args.require), args.suite)
    except (OSError, ValueError) as exc:
        print(str(exc), file=sys.stderr)
        return 1
    output = args.jsonl.with_name("summary.json")
    output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(result, ensure_ascii=False))
    return 0 if result["valid"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
