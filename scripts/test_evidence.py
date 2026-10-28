#!/usr/bin/env python3
"""Validate executable Go test evidence; absence and skipped tests are not passes."""
from __future__ import annotations

from helm_native_archive_evidence import AUTOMATIC as HELM_NATIVE_ARCHIVE_REQUIRED
from helm_install_recovery_evidence import AUTOMATIC as HELM_INSTALL_RECOVERY_REQUIRED
from helm_native_install_evidence import AUTOMATIC as HELM_NATIVE_INSTALL_REQUIRED

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


HELM_RENDER_REQUIRED = {'unit-native': ('TestHelmChartSnapshotBoundaries', 'TestHelmDistributionAndLocalFileBoundaries', 'TestHelmEnvironmentAndOutputBudgets', 'TestHelmRealTemplateSnapshotAndFailure', 'TestRenderHelperPipeLimitsCancellationAndPrivacy', 'TestNativeHelmRenderRequiresImportAndPerObjectReview', 'TestNativeHelmDiscardFailureAndMountedHistory', 'TestNativeHelmMinimumWindowStaleResultsAndTeardown', 'TestNativeHelmLifecycleOutputRemainsRejected'), 'native-os-interactions': ('TestNativeHelmRenderRequiresImportAndPerObjectReview', 'TestNativeHelmDiscardFailureAndMountedHistory', 'TestNativeHelmMinimumWindowStaleResultsAndTeardown', 'TestNativeHelmLifecycleOutputRemainsRejected'), 'real-cluster-native': ('TestNativeHelmRenderedDeliveryAgainstRealCluster', 'TestNativeHelmRenderRequiresImportAndPerObjectReview', 'TestNativeHelmDiscardFailureAndMountedHistory', 'TestNativeHelmMinimumWindowStaleResultsAndTeardown', 'TestNativeHelmLifecycleOutputRemainsRejected')}


HELM_METADATA_REQUIRED = {
    "unit-native": (
        "TestHelmReleaseMetadataProjectionAndNumericOrdering",
        "TestHelmReleaseMetadataMalformedAndBudgetBoundaries",
        "TestHelmReleaseMetadataTransportAndPagination",
        "TestHelmReleaseMetadataRejectsFailuresWithoutFallback",
        "TestHelmReleaseMetadataCancellationRedirectAndInvalidNamespace",
        "TestNativeHelmMetadataReadOnlyAndMinimumWindow",
        "TestNativeHelmMetadataScopeCancellationAndStaleResults",
        "TestNativeHelmMetadataFailureClearsPreviousSnapshot",
    ),
    "native-os-interactions": (
        "TestNativeHelmMetadataReadOnlyAndMinimumWindow",
        "TestNativeHelmMetadataScopeCancellationAndStaleResults",
        "TestNativeHelmMetadataFailureClearsPreviousSnapshot",
    ),
    "real-cluster-native": (
        "TestRealHelmReleaseMetadataReadOnlyPaginationAndRBAC",
        "TestNativeHelmReleaseMetadataAgainstRealCluster",
    ),
}


HELM_INSPECTION_REQUIRED = {'unit-native': ('TestHelmInspectionBoundIdentityAndPrivateProjection', 'TestHelmInspectionMalformedAndAmbiguousPayloads', 'TestHelmInspectionEncodingBudgetsAndCancellation', 'TestHelmInspectionLateBudgetsAndInFlightCancellation', 'TestHelmInspectionExactGetConsentAndCurrentIdentity', 'TestHelmInspectionPrivateErrorsNoRetryOrFallback', 'TestHelmInspectionStaleStorageAndCancellation', 'TestNativeHelmInspectionExplicitConsentAndPrivateSummary', 'TestNativeHelmInspectionStaleCompletionAndTeardown', 'TestNativeHelmInspectionFailureCancellationAndStaleObject'), 'native-os-interactions': ('TestNativeHelmInspectionExplicitConsentAndPrivateSummary', 'TestNativeHelmInspectionStaleCompletionAndTeardown', 'TestNativeHelmInspectionFailureCancellationAndStaleObject'), 'real-cluster-native': ('TestRealHelmInspectionReadOnlyIdentityAndRBAC', 'TestNativeHelmInspectionAgainstRealCluster')}

HELM_UNINSTALL_PREVIEW_REQUIRED = {'unit-native': ('TestHelmUninstallPreviewImmutableIdentityExpiryAndPrivacy', 'TestHelmUninstallPreviewRejectsAmbiguityAndLateFailure', 'TestHelmUninstallPreviewBudgetsAndCancellation', 'TestHelmUninstallPreviewExactReadAndPrivateFailures', 'TestHelmUninstallPreviewStaleRedirectAndCancellation', 'TestNativeHelmUninstallPreviewConsentAndExpiry', 'TestNativeHelmUninstallPreviewStaleCompletionAndBusyConsent', 'TestNativeHelmUninstallPreviewFailuresAndCancellation', 'TestNativeHelmUninstallPreviewScaledInventory'), 'native-os-interactions': ('TestNativeHelmUninstallPreviewConsentAndExpiry', 'TestNativeHelmUninstallPreviewStaleCompletionAndBusyConsent', 'TestNativeHelmUninstallPreviewFailuresAndCancellation', 'TestNativeHelmUninstallPreviewScaledInventory'), 'real-cluster-native': ('TestRealHelmUninstallPreviewReadOnlyAndStaleIdentity', 'TestNativeHelmUninstallPreviewAgainstRealCluster')}

HELM_LIVE_OBSERVATION_REQUIRED = {'native-os-interactions': ('TestNativeHelmLiveObservationConsentAndExpiry',
                            'TestNativeHelmLiveObservationStaleResults',
                            'TestNativeHelmLiveObservationFailureClearsResult',
                            'TestNativeHelmLiveObservationScaledInventory'),
 'real-cluster-native': ('TestRealHelmLiveObservationMetadataAndIdentity',
                         'TestNativeHelmLiveObservationAgainstRealCluster'),
 'unit-native': ('TestHelmLiveLatestRevisionConsistency',
                 'TestHelmLiveObservationImmutableExpiry',
                 'TestHelmLiveReadOnlyMetadataAndHistory',
                 'TestHelmLiveDiscoveryEnvelope',
                 'TestHelmLiveRejectsRoutesAndPrivateFailures',
                 'TestHelmLiveCancellationAndBudgets',
                 'TestNativeHelmLiveObservationConsentAndExpiry',
                 'TestNativeHelmLiveObservationStaleResults',
                 'TestNativeHelmLiveObservationFailureClearsResult',
                 'TestNativeHelmLiveObservationScaledInventory')}


# Require complete named native cases, including children. A passing parent does
# not establish that a cancellation, stale-selection or platform-layout case ran.
HELM_NATIVE_JOURNAL_NAMES = (
    'TestNativeHelmJournalStablePrivateHomePath',
    'TestNativeHelmJournalHistoryCreatesPrivateStoreAndReleasesLock',
    'TestNativeHelmJournalHistoryKeepsOutcomesIntegrityAndOriginalBytes',
    'TestNativeHelmJournalCancelledWorkersDoNotCreateStorage',
    *['TestNativeHelmJournalCancelledWorkersDoNotCreateStorage/' + case
      for case in ('history', 'execution')],
    'TestNativeHelmJournalBusyWorkersPreserveOwnerLock',
    'TestNativeHelmJournalFailedExecutionAcknowledgementReleasesLock',
    'TestNativeHelmJournalInventoryFailureDoesNotPublishPartialHistory',
    *['TestNativeHelmJournalInventoryFailureDoesNotPublishPartialHistory/' + case
      for case in ('unexpected-file', 'record-over-budget')],
    'TestNativeHelmJournalHistoryDoesNotClearUnresolvedAdmission',
)

HELM_NATIVE_UI_NAMES = (
    'TestNativeHelmUninstallExplicitConfirmations',
    'TestNativeHelmUninstallQueuedInputAndComposition',
    *['TestNativeHelmUninstallQueuedInputAndComposition/' + case
      for case in ('busy-consent', 'exact-text-and-composition')],
    'TestNativeHelmUninstallStaleCallbacks',
    *['TestNativeHelmUninstallStaleCallbacks/' + case
      for case in ('selection', 'refresh', 'close', 'namespace', 'context',
                   'backend', 'session', 'directory', 'disconnect',
                   'credentials', 'teardown')],
    'TestNativeHelmUninstallCancellationAndJournalRelease',
    *['TestNativeHelmUninstallCancellationAndJournalRelease/' + case
      for case in ('stop', 'close', 'disconnect', 'teardown')],
    'TestNativeHelmUninstallExpiryAndPrivateFailure',
    *['TestNativeHelmUninstallExpiryAndPrivateFailure/' + case
      for case in ('expiry', 'private-failure')],
    'TestNativeHelmRecoveryOfflineAndFreshObservation',
    'TestNativeHelmRecoveryCorruptionAndBusyStore',
    *['TestNativeHelmRecoveryCorruptionAndBusyStore/' + case
      for case in ('corrupt', 'busy')],
    'TestNativeHelmUninstallMinimumWindow',
    *[f'TestNativeHelmUninstallMinimumWindow/scale-{scale}-rtl-{rtl}'
      for scale in ('1', '1.25', '1.5') for rtl in ('false', 'true')],
)

HELM_NATIVE_LIFECYCLE_NAMES = (
    'TestNativeHelmUninstallParentContextRevokesDryRun',
    *['TestNativeHelmUninstallParentContextRevokesDryRun/' + case
      for case in ('connection', 'application')],
    'TestNativeHelmUninstallLatePhaseCallbacks',
    *['TestNativeHelmUninstallLatePhaseCallbacks/' + phase + '/' + scope
      for phase in ('dry-run', 'recovery') for scope in ('new-pane', 'session')],
    'TestNativeHelmUninstallWorkspaceJournalIsolation',
)

HELM_NATIVE_UNINSTALL_NAMES = (
    *HELM_NATIVE_JOURNAL_NAMES, *HELM_NATIVE_UI_NAMES, *HELM_NATIVE_LIFECYCLE_NAMES,
)
HELM_NATIVE_UNINSTALL_REQUIRED = {
    suite: HELM_NATIVE_UNINSTALL_NAMES
    for suite in ('unit-native', 'native-os-interactions',
                  'migration-checked', 'migration-production')
}
HELM_NATIVE_UNINSTALL_REQUIRED['real-cluster-native'] = (
    *HELM_NATIVE_UNINSTALL_NAMES,
    'TestNativeHelmUninstallAgainstRealCluster',
    'TestNativeHelmRecoveryAgainstRealCluster',
)


# Read-only admission is a separate increment. The accepted native uninstall
# inventory above remains unchanged, including both original real-cluster roots.
HELM_JOURNAL_ADMISSION_NAMES = (
    'TestNativeHelmAdmissionReview',
    'TestNativeHelmAdmissionHistoryAndMinimumWindow',
    'TestNativeHelmAdmissionLifecycle',
    *['TestNativeHelmAdmissionLifecycle/' + case
      for case in ('namespace', 'session', 'selection', 'backend', 'journal-path',
                   'credential-expiry', 'preparation-expiry', 'stop',
                   'window-close', 'pane-reopen', 'application-cancel',
                   'connection-cancel')],
    'TestNativeHelmAdmissionNoExecutionAuthority',
)
HELM_JOURNAL_ADMISSION_REQUIRED = {
    suite: HELM_JOURNAL_ADMISSION_NAMES
    for suite in ('unit-native', 'native-os-interactions',
                  'migration-checked', 'migration-production')
}
HELM_JOURNAL_ADMISSION_REQUIRED['real-cluster-native'] = (
    *HELM_JOURNAL_ADMISSION_NAMES,
    'TestNativeHelmJournalAdmissionAgainstRealCluster',
)


def required_for_suite(suite: str, explicit: list[str]) -> list[str]:
    return list(dict.fromkeys([*explicit, *INTEGER_MAP_REQUIRED.get(suite, ()), *INTEGER_SET_REQUIRED.get(suite, ()), *MANIFEST_CLIPBOARD_REQUIRED.get(suite, ()), *BOOLEAN_COLLECTIONS_REQUIRED.get(suite, ()), *HELM_RENDER_REQUIRED.get(suite, ()), *HELM_METADATA_REQUIRED.get(suite, ()), *HELM_INSPECTION_REQUIRED.get(suite, ()), *HELM_UNINSTALL_PREVIEW_REQUIRED.get(suite, ()), *HELM_LIVE_OBSERVATION_REQUIRED.get(suite, ()), *HELM_NATIVE_UNINSTALL_REQUIRED.get(suite, ()), *HELM_JOURNAL_ADMISSION_REQUIRED.get(suite, ()), *HELM_NATIVE_ARCHIVE_REQUIRED.get(suite, ()), *HELM_INSTALL_RECOVERY_REQUIRED.get(suite, ()), *HELM_NATIVE_INSTALL_REQUIRED.get(suite, ())]))


def summarize(path: Path, required: list[str], suite: str) -> dict:
    started: set[tuple[str, str]] = set()
    invalid_lifecycle: set[tuple[str, str]] = set()
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
                    if key in started:
                        invalid_lifecycle.add(key)
                    started.add(key)
                    completed.pop(key, None)
                elif action in {"pass", "fail", "skip"}:
                    if key not in started or key in completed:
                        invalid_lifecycle.add(key)
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
        "invalid_lifecycle_events": sorted(f"{pkg}:{name}" for pkg, name in invalid_lifecycle),
    }
    result["valid"] = bool(started) and not any((missing, failures, skipped, unfinished, failed_packages, incomplete_packages, invalid_lifecycle))
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
