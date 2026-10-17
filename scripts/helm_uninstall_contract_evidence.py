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
        'TestHelmUninstallDeletionObservationRejectsAmbiguousReads',
        *['TestHelmUninstallDeletionObservationRejectsAmbiguousReads/' + mode for mode in
          ('typed-absence', 'wrapped-absence', 'raw-proxy-404', 'synthetic-proxy-404',
           'other-name', 'other-resource', 'other-group', 'nil-details', 'wrong-code',
           'wrong-reason', 'wrong-status', 'cancel-after-read', 'deadline-after-read',
           'expired-before-read')],
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

STATUS_TRANSITION_REQUIRED = {
    'unit-native': (
        'TestHelmStatusTransitionPreservesPayloadAndConditions',
        'TestHelmStatusTransitionRejectsStaleAndInvalid',
        'TestHelmStatusTransitionPatchPreconditions',
        'TestHelmStatusTransitionBudgetsAndCancellation',
        'TestHelmStatusTransitionSpliceOracle',
    ),
    'real-cluster-native': (
        'TestRealHelmStatusTransitionConditionalStorage',
        *['TestRealHelmStatusTransitionConditionalStorage/' + key for key in
          ('name', 'namespace', 'uid', 'rv', 'status', 'denied')],
        'TestRealHelmUninstallContractResourcesAndHistory/keep-history/status-patch-dry-run',
    ),
}

UNINSTALL_PREPARATION_REQUIRED = {
    'unit-native': (
        'TestHelmUninstallPreparationHTTPReadOnly',
        'TestHelmUninstallPreparationHTTPReadOnly/canceled',
        'TestHelmUninstallPreparationHTTPReadOnly/cluster-route',
        'TestHelmUninstallPreparationHTTPReadOnly/denied-object',
        'TestHelmUninstallPreparationHTTPReadOnly/finalizer',
        'TestHelmUninstallPreparationHTTPReadOnly/full-object',
        'TestHelmUninstallPreparationHTTPReadOnly/hook',
        'TestHelmUninstallPreparationHTTPReadOnly/invalid-session',
        'TestHelmUninstallPreparationHTTPReadOnly/keep',
        'TestHelmUninstallPreparationHTTPReadOnly/late-cancel',
        'TestHelmUninstallPreparationHTTPReadOnly/missing-marker',
        'TestHelmUninstallPreparationHTTPReadOnly/missing-object',
        'TestHelmUninstallPreparationHTTPReadOnly/newer-after',
        'TestHelmUninstallPreparationHTTPReadOnly/nondeployed',
        'TestHelmUninstallPreparationHTTPReadOnly/older-before',
        'TestHelmUninstallPreparationHTTPReadOnly/owner',
        'TestHelmUninstallPreparationHTTPReadOnly/pagination',
        'TestHelmUninstallPreparationHTTPReadOnly/proxy404',
        'TestHelmUninstallPreparationHTTPReadOnly/rv-after',
        'TestHelmUninstallPreparationHTTPReadOnly/secret-denied',
        'TestHelmUninstallPreparationHTTPReadOnly/success',
        'TestHelmUninstallPreparationHTTPReadOnly/terminating',
        'TestHelmUninstallPreparationHTTPReadOnly/uid-after',
        'TestHelmUninstallPreparationHTTPReadOnly/unacknowledged',
        'TestHelmUninstallPreparationHTTPReadOnly/unsupported-content',
        'TestHelmUninstallPreparationHTTPReadOnly/wrong-identity',
        'TestHelmUninstallPreparationHTTPReadOnly/wrong-marker',
        'TestHelmUninstallPreparationHTTPReadOnly/wrong-route',
        'TestHelmUninstallPreparationImmutableExactTargets',
        'TestHelmUninstallPreparationRejectsChangedBatch',
        'TestHelmUninstallPreparationRejectsChangedBatch/canceled',
        'TestHelmUninstallPreparationRejectsChangedBatch/cluster-scope',
        'TestHelmUninstallPreparationRejectsChangedBatch/expired',
        'TestHelmUninstallPreparationRejectsChangedBatch/finalizers',
        'TestHelmUninstallPreparationRejectsChangedBatch/hook',
        'TestHelmUninstallPreparationRejectsChangedBatch/keep',
        'TestHelmUninstallPreparationRejectsChangedBatch/marker-mismatch',
        'TestHelmUninstallPreparationRejectsChangedBatch/marker-missing',
        'TestHelmUninstallPreparationRejectsChangedBatch/missing-object',
        'TestHelmUninstallPreparationRejectsChangedBatch/missing-source',
        'TestHelmUninstallPreparationRejectsChangedBatch/newer-history',
        'TestHelmUninstallPreparationRejectsChangedBatch/object-after-snapshot',
        'TestHelmUninstallPreparationRejectsChangedBatch/object-before-snapshot',
        'TestHelmUninstallPreparationRejectsChangedBatch/older-history',
        'TestHelmUninstallPreparationRejectsChangedBatch/owners',
        'TestHelmUninstallPreparationRejectsChangedBatch/session',
        'TestHelmUninstallPreparationRejectsChangedBatch/source-rv',
        'TestHelmUninstallPreparationRejectsChangedBatch/source-uid',
        'TestHelmUninstallPreparationRejectsChangedBatch/terminating',
        'TestHelmUninstallPreparationRejectsChangedBatch/unrelated-release',
        'TestHelmUninstallPreparationRejectsChangedBatch/wrong-object',
        'TestHelmUninstallPreparationRejectsChangedBatch/wrong-resource',
        'TestHelmUninstallPreparationRejectsChangedBatch/wrong-uid',
        'TestHelmUninstallProfileBoundsAndCancellation',
        'TestHelmUninstallProfileFullContent',
        'TestHelmUninstallProfileRejectsManifestAndStorage',
        'TestHelmUninstallProfileRejectsManifestAndStorage/__Annotations:_{}',
        "TestHelmUninstallProfileRejectsManifestAndStorage/__annotations:_____helm.sh/hook-weight:_'0'",
        'TestHelmUninstallProfileRejectsManifestAndStorage/__annotations:_____helm.sh/hook:_test',
        'TestHelmUninstallProfileRejectsManifestAndStorage/__annotations:_____helm.sh/resource-policy:_unknown',
        'TestHelmUninstallProfileRejectsManifestAndStorage/__deletionTimestamp:_null',
        'TestHelmUninstallProfileRejectsManifestAndStorage/__finalizers:_[]',
        'TestHelmUninstallProfileRejectsManifestAndStorage/__generateName:_prefix',
        "TestHelmUninstallProfileRejectsManifestAndStorage/__labels:_____'invalid_label':_value",
        'TestHelmUninstallProfileRejectsManifestAndStorage/__ownerReferences:_[]',
        "TestHelmUninstallProfileRejectsManifestAndStorage/__resourceVersion:_'7'",
        "TestHelmUninstallProfileRejectsManifestAndStorage/__uid:_'old'",
        'TestHelmUninstallProfileRejectsManifestAndStorage/bad-binary',
        'TestHelmUninstallProfileRejectsManifestAndStorage/duplicate',
        'TestHelmUninstallProfileRejectsManifestAndStorage/duplicate-default-alias',
        'TestHelmUninstallProfileRejectsManifestAndStorage/empty',
        'TestHelmUninstallProfileRejectsManifestAndStorage/finalizers',
        'TestHelmUninstallProfileRejectsManifestAndStorage/foreign',
        'TestHelmUninstallProfileRejectsManifestAndStorage/grace',
        'TestHelmUninstallProfileRejectsManifestAndStorage/immutable',
        'TestHelmUninstallProfileRejectsManifestAndStorage/keep',
        'TestHelmUninstallProfileRejectsManifestAndStorage/list',
        'TestHelmUninstallProfileRejectsManifestAndStorage/overlapping-keys',
        'TestHelmUninstallProfileRejectsManifestAndStorage/owners',
        'TestHelmUninstallProfileRejectsManifestAndStorage/root-alias',
        'TestHelmUninstallProfileRejectsManifestAndStorage/secret',
        'TestHelmUninstallProfileRejectsManifestAndStorage/stale-rv',
        'TestHelmUninstallProfileRejectsManifestAndStorage/terminating',
        'TestHelmUninstallProfileRejectsManifestAndStorage/unknown-root',
        'TestHelmUninstallProfileRejectsManifestAndStorage/wrong-data-type',
        'TestHelmUninstallProfileRejectsManifestAndStorage/wrong-version',
        'TestHelmUninstallProfileRejectsUnsupportedContent',
        'TestHelmUninstallProfileRejectsUnsupportedContent/chart-policy',
        'TestHelmUninstallProfileRejectsUnsupportedContent/deleted',
        'TestHelmUninstallProfileRejectsUnsupportedContent/deleted-alias',
        'TestHelmUninstallProfileRejectsUnsupportedContent/dependencies',
        'TestHelmUninstallProfileRejectsUnsupportedContent/dependencies-alias',
        'TestHelmUninstallProfileRejectsUnsupportedContent/duplicate-file',
        'TestHelmUninstallProfileRejectsUnsupportedContent/file-count',
        'TestHelmUninstallProfileRejectsUnsupportedContent/file-path-../README',
        'TestHelmUninstallProfileRejectsUnsupportedContent/file-path-/absolute',
        'TestHelmUninstallProfileRejectsUnsupportedContent/file-path-CRDS/object.txt',
        'TestHelmUninstallProfileRejectsUnsupportedContent/file-path-charts/child.tgz',
        'TestHelmUninstallProfileRejectsUnsupportedContent/file-path-crds/object.yaml',
        'TestHelmUninstallProfileRejectsUnsupportedContent/file-path-docs/',
        'TestHelmUninstallProfileRejectsUnsupportedContent/file-path-docs/../readme',
        'TestHelmUninstallProfileRejectsUnsupportedContent/file-path-docs/./file',
        'TestHelmUninstallProfileRejectsUnsupportedContent/file-path-docs//file',
        'TestHelmUninstallProfileRejectsUnsupportedContent/file-path-docs\\crds\\x',
        'TestHelmUninstallProfileRejectsUnsupportedContent/file-path-templates/forged.yaml',
        'TestHelmUninstallProfileRejectsUnsupportedContent/files-alias',
        'TestHelmUninstallProfileRejectsUnsupportedContent/hooks',
        'TestHelmUninstallProfileRejectsUnsupportedContent/library',
        'TestHelmUninstallProfileRejectsUnsupportedContent/lock',
        'TestHelmUninstallProfileRejectsUnsupportedContent/malformed-file-base64',
        'TestHelmUninstallProfileRejectsUnsupportedContent/malformed-files',
        'TestHelmUninstallProfileRejectsUnsupportedContent/null-description',
        'TestHelmUninstallProfileRejectsUnsupportedContent/null-file',
        'TestHelmUninstallProfileRejectsUnsupportedContent/unknown-chart-field',
        'TestHelmUninstallProfileRejectsUnsupportedContent/unknown-method',
        'TestHelmUninstallProfileRejectsUnsupportedContent/unknown-release-field',
    ),
    'real-cluster-native': (
        'TestRealHelmUninstallPreparationReadOnlyProfile',
        'TestRealHelmUninstallPreparationReadOnlyProfile/keep',
        'TestRealHelmUninstallPreparationReadOnlyProfile/hook',
        'TestRealHelmUninstallPreparationReadOnlyProfile/missing-marker',
        'TestRealHelmUninstallPreparationReadOnlyProfile/wrong-marker',
        'TestRealHelmUninstallPreparationReadOnlyProfile/finalizer',
        'TestRealHelmUninstallPreparationReadOnlyProfile/owner',
        'TestRealHelmUninstallPreparationReadOnlyProfile/denied',
        'TestRealHelmUninstallPreparationReadOnlyProfile/multiple-revisions',
        'TestRealHelmUninstallContractResourcesAndHistory/keep-history/restricted-preparation',
        'TestRealHelmUninstallContractResourcesAndHistory/purge-history/restricted-preparation',
        'TestRealHelmUninstallContractResourcesAndHistory/missing-marker/restricted-preparation',
        'TestRealHelmUninstallContractResourcesAndHistory/mismatch-marker/restricted-preparation',
        'TestRealHelmUninstallContractResourcesAndHistory/recreated/restricted-preparation',
        'TestRealHelmUninstallContractResourcesAndHistory/hooks/restricted-preparation',
        'TestRealHelmUninstallContractResourcesAndHistory/no-hooks/restricted-preparation',
    ),
}

def required(suite):
    return required_for_suite(suite, [*REQUIRED[suite], *STATUS_TRANSITION_REQUIRED.get(suite, ()), *UNINSTALL_PREPARATION_REQUIRED.get(suite, ())])

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
