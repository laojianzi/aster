#!/usr/bin/env python3
"""Require actual journal tests on each OS without replacing native evidence."""
import argparse
from collections import Counter
import json
from pathlib import Path
import sys

from test_evidence import summarize
from helm_journal_archive_evidence import ARCHIVE_REQUIRED
from helm_native_archive_evidence import IDENTITY_NAMES

PACKAGE = 'github.com/laojianzi/aster/internal/helmjournal'
RECOVERY_REQUIRED = (
    'TestHelmJournalRoundTripAndImmutableCopies',
    'TestHelmJournalProcessCrashRecovery',
    'TestHelmJournalProcessLockExclusion',
    'TestHelmJournalWriteAndSyncFailures',
    'TestHelmJournalStrictFramesAndTransitions',
    'TestHelmJournalBudgetsAndUnresolvedScope',
    'TestHelmJournalPrivateFilesAndPaths',
    'TestHelmJournalConcurrentLeaseAndClose',
)

# Admission checks supplement the original eight recovery roots. Keep their
# separate inventories so neither scope can silently replace the other's names.
ADMISSION_REQUIRED = (
    'TestHelmJournalAdmissionReadOnly',
    'TestHelmJournalAdmissionHistoryPolicy',
    *['TestHelmJournalAdmissionHistoryPolicy/' + case
      for case in ('unclosed-not-started', 'unclosed-completed', 'closed-not-started',
                   'closed-rejected', 'closed-unknown', 'closed-pending',
                   'closed-partial', 'closed-completed')],
    'TestHelmJournalAdmissionExactScope',
    *['TestHelmJournalAdmissionExactScope/' + case
      for case in ('binding', 'namespace', 'storage-uid', 'resource-version', 'preparation-id')],
    'TestHelmJournalAdmissionIntegrity',
    *['TestHelmJournalAdmissionIntegrity/' + case
      for case in ('unrelated-corrupt-record', 'invalid-directory-entry', 'changed-lock-no-poison')],
    'TestHelmJournalAdmissionLifecycle',
    *['TestHelmJournalAdmissionLifecycle/' + case
      for case in ('nil-handle', 'zero-handle', 'closed-handle', 'copied-closed-handle',
                   'poisoned-store', 'active-writer', 'blocked-sync')],
    'TestHelmJournalAdmissionContext',
    *['TestHelmJournalAdmissionContext/' + case
      for case in ('nil', 'cancelled', 'expired', 'bounded-scan')],
    'TestHelmJournalAdmissionExistingOnly',
    *['TestHelmJournalAdmissionExistingOnly/' + case
      for case in ('missing-directory', 'missing-parent', 'missing-lock',
                   'missing-lock-with-history', 'invalid-path-and-scope',
                   'private-path', 'redirects', 'hardlinks')],
    'TestHelmJournalAdmissionProcessLock',
    'TestHelmJournalAdmissionRechecksAfterReview',
    'TestHelmJournalAdmissionCleanupFailure',
    *['TestHelmJournalAdmissionCleanupFailure/' + case for case in ('no-blocker', 'blocked')],
    'TestHelmJournalAdmissionCapacity',
    *['TestHelmJournalAdmissionCapacity/' + case
      for case in ('record-limit', 'entry-limit', 'operation-bytes',
                   'store-bytes', 'reservation-boundary')],
)
REQUIRED = (*RECOVERY_REQUIRED, *ADMISSION_REQUIRED, *ARCHIVE_REQUIRED, *IDENTITY_NAMES)


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
