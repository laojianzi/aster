#!/usr/bin/env python3
"""Run the named migration contract in both builds; absent/skipped cases fail."""
import os
from pathlib import Path
import re
import subprocess
import sys

from test_evidence import HELM_JOURNAL_ADMISSION_NAMES, HELM_NATIVE_UNINSTALL_NAMES

ROOT = Path(__file__).resolve().parents[1]
REQUIRED = [
    *HELM_NATIVE_UNINSTALL_NAMES,
    *HELM_JOURNAL_ADMISSION_NAMES,

    'TestNativeHelmLiveObservationConsentAndExpiry',
    'TestNativeHelmLiveObservationStaleResults',
    'TestNativeHelmLiveObservationFailureClearsResult',
    'TestNativeHelmLiveObservationScaledInventory',

    'TestNativeHelmUninstallPreviewConsentAndExpiry',
    'TestNativeHelmUninstallPreviewStaleCompletionAndBusyConsent',
    'TestNativeHelmUninstallPreviewFailuresAndCancellation',
    'TestNativeHelmUninstallPreviewScaledInventory',

    "TestNativeMyGo055ExplicitClockAndCurrentFrameShortcut",
    "TestNativeMyGo055SplitListReportsCurrentSelectionOnce",
    "TestNativeMyGo05ScaledNavigationStaysAboveStatus",
    "TestNativeMyGo05GuardedChoiceTransitions",
    "TestNativeMyGo05PendingSensitiveFieldsDoNotInvalidateReview",
    "TestNativeMyGo05QueuedRenewalOptInDoesNotExpandScope",
    "TestNativeMyGo05BusyTransitionRejectsQueuedFields",
    "TestNativeMyGo05BusyTransitionCannotRestoreConsumedConsent",
    "TestNativeMyGo05ScaledWorkbenchKeepsActionsVisible",
    'TestNativeHelmInspectionExplicitConsentAndPrivateSummary',
    'TestNativeHelmInspectionStaleCompletionAndTeardown',
    'TestNativeHelmInspectionFailureCancellationAndStaleObject',

    "TestNativeHelmMetadataReadOnlyAndMinimumWindow",
    "TestNativeHelmMetadataScopeCancellationAndStaleResults",
    "TestNativeHelmMetadataFailureClearsPreviousSnapshot",
    'TestNativeHelmRenderRequiresImportAndPerObjectReview',
    'TestNativeHelmDiscardFailureAndMountedHistory',
    'TestNativeHelmMinimumWindowStaleResultsAndTeardown',
    'TestNativeHelmLifecycleOutputRemainsRejected',
    'TestNativeSchemaBooleanCollectionsHistoryAndNavigation',
    'TestNativeSchemaBooleanCollectionsMinimumWindowIncomplete',
    'TestNativeSchemaBooleanCollectionsStaleResults',
    'TestNativeManifestClipboardPreservesHistoryAndSelection',
    'TestNativeManifestClipboardMinimumWindowAndComposition',
    'TestNativeManifestClipboardStaleCallbacksAndTeardown',
    'TestNativeManifestClipboardRevokesUnsentPreview',
    'TestNativeSchemaIntegerSetHistoryAndNavigation',
    'TestNativeSchemaIntegerSetMinimumWindowIncomplete',
    'TestNativeSchemaIntegerMapKeysHistoryAndNavigation',
    'TestNativeSchemaIntegerMapKeysMinimumWindowIncomplete',
    'TestNativeMyGo048ChunkedBufferPreservesMixedBytesAndHistory',
    'TestNativeSchemaListSetPreservesDraftAndHistory',
    'TestNativeSchemaListSetMinimumWindowAndIncomplete',
    'TestNativeSchemaListMapPreservesDraftAndHistory',
    'TestNativeSchemaListMapMinimumWindowAndIncomplete',
    'TestNativeSchemaEnumPreservesDraftAndHistory',
    'TestNativeSchemaEnumMinimumWindowAndIncomplete',
    'TestNativeSchemaFormatPreservesDraftAndHistory',
    'TestNativeSchemaFormatMinimumWindowAndIncomplete',
    'TestNativeSchemaPatternPreservesDraftAndHistory',
    'TestNativeSchemaPatternMinimumWindowAndIncomplete',
    'TestNativeSchemaMultipleOfPreservesNumbersAndHistory',
    'TestNativeSchemaMultipleOfMinimumWindowAndIncomplete',
    'TestNativeDeliveryImportPreviewAndConfirm',
    'TestNativeDeliveryPartialUnknownAndReviewAgain',
    'TestNativeDeliveryStaleWorkAndWorkspaceIsolation',
    'TestNativeDeliveryMinimumWindowUnicodeAndBudget',

    'TestNativeSchemaConstraintsPreserveNumbersDraftAndHistory',
    'TestNativeSchemaConstraintsMinimumWindowAndNoValueDisclosure',

    'TestNativeMyGo045PhysicalShortcutsPreserveScopedHistory',
    'TestNativeMyGo045UnicodeCrossLineSelectionPreservesUndo',

    'TestNativeOIDCSignOutReviewTeardownAndMinimumWindow',
    'TestNativeOIDCSignOutOriginalIssuerWithoutRenewal',
    'TestNativeOIDCSignOutFailureIsRedactedAndNeverReplayed',
    'TestNativeOIDCSignOutStaleLeaseContextPendingAndWindow',
    'TestNativeOIDCSignOutConfirmationHistoryAndRenewalReplacement',
    'TestNativeOIDCSignOutDoesNotEraseOtherWorkspace',
    'TestNativeOIDCSignOutPersistentServicesDispatchOnce',

    'TestNativeMyGo04EscapeDismissesWithoutChoosingButton',
    'TestNativeMyGo04ToastExpiresAfterClosingDialog',
    'TestNativeMyGo04SpinnerUsesExplicitTextColor',
    'TestNativeMyGo04ProductionSurfaceInventory',
    'TestManifestBufferImmutableConcurrentSnapshotsAndExportBounds',
    'TestManifestBufferIdleRevisionChecksAllocateNothing',
    'TestNativeManifestBufferStableIdentitySnapshotAndUndo',
    'TestNativeManifestBufferPreeditNoOpPreservesSemanticRevision',
    'TestNativeManifestBufferQueuedSchemaSeesUnrenderedEdit',
    'TestNativeManifestBufferUnrenderedChangeRevokesPreparedPlan',
    'TestNativeManifestBufferOversizeCannotBecomeEmptyPreview',
    'TestNativeManifestBufferOversizeIsPreservedUntilExplicitRestore',

    'TestNativeFindTextInputNormalizesCRLFBeforeSearch',
    'TestNativeFindLiteralNavigationUndoAndWrap',
    'TestNativeFindQueuedResultsCannotCrossEpochs',
    'TestNativeFindSingleAdmissionAndOneShotSelection',
    'TestNativeFindCompositionDefersAndRejectsChangedDraft',
    'TestNativeFindReadOnlyBudgetsAndMinimumWindow',
    'TestNativeFindQueuedSelectionCannotCrossDraftOrQuery',
    'TestNativeFindDeferredSelectionLosesFocusAndPreservesTruncation',

    'TestNativeSchemaLocationMenuCannotApplyStaleSelection',
    'TestNativeSchemaSourceCoordinatesAcrossJSONAndCRLF',
    'TestNativeSchemaDiagnosticNavigationPreservesDraftAndHistory',
    'TestNativeSchemaMissingFieldNavigatesToLabeledParent',
    'TestNativeSchemaQueuedNavigationCannotCrossDraftIdentityOrTab',
    'TestNativeSchemaNavigationRespectsCompositionAndIsOneShot',
    'TestNativeSchemaLocationControlsAtMinimumWindow',
    'TestNativeSchemaLocationsUnavailableDoNotBlockReadOnlyHelp',
    'TestNativeSchemaLocationLookupIsEpochBoundAndReadOnly',
    'TestNativeEditorUndoCannotCrossResourceIdentity',
    'TestNativeCommandUndoCannotCrossResourceIdentity',
    'TestNativeMigrationTextFieldsDoNotShareHistory',
    'TestNativeMigrationReadOnlyRejectsTypingAndUndo',
    'TestNativeMigrationDisabledCommandControlsIgnoreInput',
    'TestNativeMigrationResourceSwitchDropsComposition',
    'TestNativeMigrationRowReorderDoesNotRetargetClick',
    'TestNativeMigrationActionsRunAfterConstructionOnce',
    'TestNativeMigrationTransientMessagePreservesEditorHistory',
    'TestNativeMigrationConstructorsAreKeyed',
    'TestNativeOIDCRenewalReviewReplacementAndMinimumWindow',
    'TestNativeOIDCRenewalCancellationDisconnectAndWindowIsolation',
    'TestNativeOIDCRenewalChangedTargetFailsBeforePost',
    'TestNativeOIDCRenewalExpiryClearsPendingAndCapability',
    'TestNativeOIDCRenewalRetargetBeforeConfirmationCannotConnect',
    'TestNativeOIDCRenewalConfirmationSurvivesUnrelatedFrame',
    'TestNativeOIDCRenewalCannotReplaceAfterUnpumpedExpiry',
    'TestNativeOIDCRenewalFailuresNeverExposeUntrustedErrors',
]

TERMINAL_REQUIRED = [
    "TestTerminalNativeMyGo055RuntimeCopyAndImagePasteStayDenied",
    "TestTerminalNativeMyGo05ClipboardReadsNeverEvaluateProviders",
    "TestTerminalNativeMyGo05MultiformatPasteIsBoundedTextOnly",
    "TestTerminalNativeMyGo05ControlCVRemainRemote",
    'TestTerminalNativeMyGo048WheelRoutingAndClose',
    'TestTerminalNativeRemoteLinksCannotLaunchLocalApplications',
    'TestTerminalNativePasteCannotBypassInputBudget',
    'TestTerminalNativeUnicodeInputAndOSC52CannotWriteClipboard',
]


def root_test_pattern(required):
    # Go interprets '/' as nested subtest patterns. Select each whole root once,
    # then require every complete root/subcase name in the independent JSON gate.
    roots = dict.fromkeys(name.split('/', 1)[0] for name in required)
    return '^(' + '|'.join(re.escape(name) for name in roots) + ')$'


if __name__ == '__main__':
    output = Path(os.environ.get('ASTER_TEST_ARTIFACTS', ROOT / 'artifacts'))
    output.mkdir(parents=True, exist_ok=True)
    head = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True)
    (output / 'COMMIT').write_text(head)
    for mode in ('checked', 'production'):
        for component, package, required in (
                ('migration', './internal/ui', REQUIRED),
                ('migration-terminal', './internal/nativeterm', TERMINAL_REQUIRED)):
            path = output / (component + '-' + mode + '.jsonl')
            args = ['go', 'test', '-count=1', '-timeout=3m', '-json', package,
                    '-run', root_test_pattern(required)]
            if mode == 'production':
                args.insert(2, '-tags=mygo_noinspector')
            with path.open('w', encoding='utf-8') as log:
                result = subprocess.run(args, cwd=ROOT, stdout=log)
            # Preserve missing-case evidence even when the Go process fails.
            verify = [sys.executable, 'scripts/test_evidence.py', str(path),
                      '--suite', component + '-' + mode]
            for name in required:
                verify.extend(['--require', name])
            validation = subprocess.run(verify, cwd=ROOT)
            if result.returncode or validation.returncode:
                raise SystemExit('migration verification failed: ' + component + '/' + mode)
