#!/usr/bin/env python3
"""Require actual journal tests on each OS without replacing native evidence."""
import argparse
from collections import Counter
import json
from pathlib import Path
import sys

from test_evidence import summarize

PACKAGE = 'github.com/laojianzi/aster/internal/helmjournal'
REQUIRED = (
    'TestHelmJournalRoundTripAndImmutableCopies',
    'TestHelmJournalProcessCrashRecovery',
    'TestHelmJournalProcessLockExclusion',
    'TestHelmJournalWriteAndSyncFailures',
    'TestHelmJournalStrictFramesAndTransitions',
    'TestHelmJournalBudgetsAndUnresolvedScope',
    'TestHelmJournalPrivateFilesAndPaths',
    'TestHelmJournalConcurrentLeaseAndClose',
)


def validate(path):
    starts, passes, packages = Counter(), Counter(), Counter()
    unexpected = set()
    with Path(path).open(encoding='utf-8') as stream:
        for number, line in enumerate(stream, 1):
            if not line.strip():
                continue
            try:
                event = json.loads(line)
            except json.JSONDecodeError as exc:
                raise ValueError(f'invalid journal JSON at line {number}') from exc
            if not isinstance(event, dict):
                raise ValueError(f'invalid journal event at line {number}')
            action, package, name = event.get('Action'), event.get('Package'), event.get('Test')
            if name is not None:
                if not isinstance(name, str) or not name:
                    raise ValueError(f'invalid journal test name at line {number}')
                if package != PACKAGE:
                    unexpected.add(str(package))
                if action == 'run':
                    starts[name] += 1
                elif action == 'pass':
                    passes[name] += 1
            elif action == 'pass':
                if package != PACKAGE:
                    unexpected.add(str(package))
                packages[str(package)] += 1

    result = summarize(Path(path), list(REQUIRED), 'helm-journal-platform')
    result['required_tests'] = list(REQUIRED)
    result['unexpected_packages'] = sorted(unexpected)
    result['nonunique_test_events'] = sorted(
        name for name in starts.keys() | passes.keys()
        if starts[name] != 1 or passes[name] != 1
    )
    result['package_pass_count'] = packages[PACKAGE]
    result['valid'] = (result['valid'] and not unexpected
                       and not result['nonunique_test_events']
                       and packages[PACKAGE] == 1)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('jsonl', type=Path)
    args = parser.parse_args()
    try:
        result = validate(args.jsonl)
    except (OSError, ValueError) as exc:
        print(str(exc), file=sys.stderr)
        return 1
    args.jsonl.with_name('helm-journal-summary.json').write_text(
        json.dumps(result, indent=2) + '\n', encoding='utf-8')
    print(json.dumps(result))
    return 0 if result['valid'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
