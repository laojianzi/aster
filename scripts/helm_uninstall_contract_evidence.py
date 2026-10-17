#!/usr/bin/env python3
"""Additional contract gate; never replaces any existing suite validator."""
import argparse
import json
from pathlib import Path
from test_evidence import required_for_suite, summarize

REQUIRED = {
    'unit-native': (
        'TestHelmUninstallBinaryDryRunAndRetention',
        *['TestHelmUninstallBinaryDryRunAndRetention/' + mode for mode in
          ('owned', 'keep', 'missing-marker', 'mismatch-marker', 'read-denied', 'storage-denied', 'malformed')],
        'TestHelmUninstallBinaryRaceAndPartialEffects',
        'TestHelmUninstallBinaryRaceAndPartialEffects/replace-after-read',
        'TestHelmUninstallBinaryRaceAndPartialEffects/partial',
        'TestHelmUninstallBinaryCancellationIsNotRollback',
        'TestHelmUninstallDeletionObservationFailsClosed',
        *['TestHelmUninstallDeletionObservationFailsClosed/' + mode for mode in
          ('eventual-exact-absence', 'persistent-object', 'replaced-uid', 'denied-not-absent', 'canceled')],
    ),
    'real-cluster-native': (
        'TestRealHelmUninstallContractResourcesAndHistory',
        *['TestRealHelmUninstallContractResourcesAndHistory/' + mode for mode in
          ('keep-history', 'purge-history', 'missing-marker', 'mismatch-marker', 'recreated', 'hooks', 'no-hooks')],
    ),
}

def required(suite):
    return required_for_suite(suite, list(REQUIRED[suite]))

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('jsonl', type=Path)
    parser.add_argument('--suite', choices=REQUIRED, required=True)
    args = parser.parse_args()
    result = summarize(args.jsonl, required(args.suite), args.suite)
    # Preserve the existing whole-suite summary rather than replacing it.
    args.jsonl.with_name('helm-uninstall-contract-summary.json').write_text(
        json.dumps(result, indent=2) + '\n', encoding='utf-8')
    print(json.dumps(result))
    return 0 if result['valid'] else 1

if __name__ == '__main__':
    raise SystemExit(main())
