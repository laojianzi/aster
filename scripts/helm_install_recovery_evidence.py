"""Additive evidence for independent metadata observation of install history.

Local history and current GET results are separate facts. These requirements do
not enable install execution, retry, repair, rollback or clearing an unresolved
record. The accepted install execution and v1 recovery gates remain unchanged.
"""


def cases(root, modes):
    return (root, *(root + '/' + str(mode) for mode in modes))


BACKEND_NAMES = (
    *cases('TestHelmInstallReconciliationBounds', (
        'consent-deadline', 'context-deadline', 'empty-response-budget', 'list-count-limit',
        'list-page-bytes', 'list-page-items', 'list-page-limit', 'list-total-bytes',
        'maximum-targets-default-rate',
    )),
    'TestHelmInstallReconciliationConcurrentConsumption',
    *cases('TestHelmInstallReconciliationConsent', (
        'corrupt-recovery', 'different-binding', 'different-namespace', 'invalid-digest',
        'invalid-namespace', 'invalid-session', 'nil-backend', 'nil-recovery', 'no-ack',
        'unknown-binding', 'zero-backend', 'zero-recovery',
    )),
    *cases('TestHelmInstallReconciliationConsumption', (
        'cancelled-context', 'changed-connection-binding', 'different-backend',
        'different-namespace', 'different-session', 'discarded', 'expired', 'future-created',
        'nil-consent', 'nil-context', 'nil-recovery', 'zero-consent', 'zero-recovery',
    )),
    *cases('TestHelmInstallReconciliationGuardPersistence', (
        'deploy-pending', 'deploy-unknown', 'incomplete', 'partial', 'secret-pending',
        'secret-unknown', 'target-unknown',
    )),
    *cases('TestHelmInstallReconciliationHTTP', (
        'deploy-intent-rv', 'deploy-pending-rv', 'list-control-rv', 'list-control-uid',
        'list-duplicate', 'list-empty-null', 'list-forbidden', 'list-not-acceptable',
        'list-page-duplicate', 'list-page-repeat', 'list-page-rv', 'list-page-token',
        'list-pagination', 'list-secret-data', 'namespace-absent-cause',
        'namespace-absent-listmeta', 'namespace-absent-null-details', 'namespace-absent-retry',
        'namespace-absent-uid', 'namespace-absent-wrong-group', 'namespace-absent-wrong-kind',
        'namespace-absent-wrong-name', 'namespace-control-rv', 'namespace-control-uid',
        'namespace-forbidden', 'namespace-full-object', 'namespace-not-acceptable',
        'namespace-null-metadata', 'namespace-proxy404', 'namespace-unknown-field',
        'namespace-wrong-name', 'namespace-wrong-namespace', 'storage-absent',
        'storage-absent-cause', 'storage-absent-listmeta', 'storage-absent-null-details',
        'storage-absent-retry', 'storage-absent-uid', 'storage-absent-wrong-group',
        'storage-absent-wrong-kind', 'storage-absent-wrong-name', 'storage-control-rv',
        'storage-control-uid', 'storage-duplicate-content-type', 'storage-duplicate-encoding',
        'storage-duplicate-field', 'storage-empty-rv', 'storage-empty-uid', 'storage-encoding',
        'storage-forbidden', 'storage-full-object', 'storage-invalid-json',
        'storage-missing-content-type', 'storage-not-acceptable', 'storage-null',
        'storage-null-metadata', 'storage-oversized-length', 'storage-oversized-stream',
        'storage-proxy404', 'storage-redirect', 'storage-replaced', 'storage-retry',
        'storage-secret-data', 'storage-trailer-warning', 'storage-truncated',
        'storage-unauthorized', 'storage-unknown-field', 'storage-unknown-matching-markers',
        'storage-unknown-unlabeled', 'storage-unlabeled', 'storage-warning',
        'storage-wrong-content-type', 'storage-wrong-name', 'storage-wrong-namespace',
        'success', 'target-absent', 'target-absent-cause', 'target-absent-listmeta',
        'target-absent-null-details', 'target-absent-retry', 'target-absent-uid',
        'target-absent-wrong-group', 'target-absent-wrong-kind', 'target-absent-wrong-name',
        'target-control-uid', 'target-duplicate-field', 'target-flags', 'target-forbidden',
        'target-marker-mismatch', 'target-marker-missing', 'target-modified',
        'target-replacement', 'target-secret-data', 'target-trailer-warning',
        'target-unknown-matching-markers',
    )),
    *cases('TestHelmInstallReconciliationNamespaceHistory', (
        'history-added-after', 'history-existing-newer', 'history-missing-storage',
        'history-rv-after', 'history-status-after', 'history-storage-mismatch',
        'history-unrelated-progress', 'namespace-absent', 'namespace-replaced',
        'namespace-replaced-after', 'namespace-rv-progress', 'namespace-terminating',
        'namespace-terminating-after', 'storage-absent-after', 'storage-replaced-after',
        'storage-rv-after', 'storage-unlabeled-after',
    )),
    *cases('TestHelmInstallReconciliationReadBoundary', (
        'active-cancelled', 'dispatch-cancelled', 'name-traversal', 'namespace-traversal',
        'rate-denied', 'unplanned-namespace', 'unplanned-resource', 'unplanned-secret',
        'unplanned-target',
    )),
    *cases('TestHelmInstallReconciliationRevocation', (
        'final-namespace', 'final-storage', 'first-target', 'initial-namespace',
        'initial-storage',
    )),
    *cases('TestHelmInstallReconciliationSnapshotBinding', (
        'archived', 'defensive-accessors', 'fresh-backend-after-expiry', 'grant-copy',
        'recovery-copy', 'same-id-reload', 'wrapper-retarget',
    )),
    'TestHelmInstallRecoveryEmptyHandles',
    *cases('TestHelmInstallRecoveryInventory', (
        'active-mixed', 'archived-ineligible-direct', 'archived-ineligible-dispatch',
        'archived-mixed', 'corrupt-v2', 'unidentified-corrupt',
    )),
    *cases('TestHelmInstallRecoveryJournal', (
        'archived', 'complete', 'corrupt', 'deploy-pending', 'deploy-unknown', 'incomplete',
        'not-started', 'partial', 'rejected', 'secret-pending', 'secret-unknown',
        'target-pending', 'target-unknown',
    )),
)

NATIVE_NAMES = (
    'TestNativeHelmInstallRecoveryMixedHistory',
    'TestNativeHelmInstallRecoveryHistoryOutcomes',
    *cases('TestNativeHelmInstallRecoveryHistoryFailures', (
        'active-corrupt-prefix', 'unknown-prefix', 'archived-corrupt',
        'oversized', 'busy', 'cancelled',
    )),
    'TestNativeHelmInstallRecoveryOfflineAndFreshObservation',
    *cases('TestNativeHelmInstallRecoveryHistoryAndCurrentFactsRemainSeparate', (
        'NotStarted', 'AcceptedPending', 'Partial', 'Unknown',
    )),
    'TestNativeHelmInstallRecoveryObservationFailureKeepsHistory',
    *cases('TestNativeHelmInstallRecoveryObservationLifecycle', (
        'stop', 'close-pane', 'close-window', 'backend', 'session',
        'namespace', 'context', 'path', 'connection',
    )),
    *cases('TestNativeHelmInstallRecoveryLateObservation', (
        'new-pane', 'session', 'same-id-new-snapshot',
    )),
    'TestNativeHelmInstallRecoveryCorruptHistoryCannotObserve',
)

REAL_NAMES = (
    'TestRealHelmInstallRecoveryCompleted',
    *cases('TestRealHelmInstallRecoveryAfterCommittedResponseLoss', (
        'secret-create', 'target-create', 'final-patch',
    )),
    *cases('TestRealHelmInstallRecoveryChangedObjects', (
        'target-modified', 'target-replacement', 'target-absent',
        'storage-unlabeled', 'storage-replacement', 'storage-absent',
    )),
    *cases('TestRealHelmInstallRecoveryGrantScope', (
        'wrong-backend', 'wrong-session', 'wrong-namespace',
        'different-snapshot', 'cancelled', 'revoked',
    )),
    'TestRealHelmInstallRecoveryNamespaceAdvance',
)

# The existing native jobs already run every ordinary UI test. Migration uses
# the same additions in its explicit root selection, in both build modes.
AUTOMATIC = {suite: NATIVE_NAMES for suite in (
    'unit-native', 'native-os-interactions', 'real-cluster-native',
    'migration-checked', 'migration-production',
)}
INSTALL_RECOVERY_REQUIRED = {
    'unit-native': BACKEND_NAMES,
    'real-cluster-native': REAL_NAMES,
}

# Only fixed, newly introduced paths may be exempted from prior scope censuses.
# The new scope independently freezes every accepted c178e3f path/mode/byte.
ADDED_TEST_FILES = {
    'internal/kube/helm_install_recovery_test.go',
    'internal/kube/helm_install_reconciliation_fixture_test.go',
    'internal/kube/helm_install_reconciliation_test.go',
    'internal/ui/helm_install_recovery_history_test.go',
    'internal/ui/helm_install_recovery_fixture_test.go',
    'internal/ui/helm_install_recovery_ui_test.go',
    'e2e/helm_install_recovery_test.go',
}


def additions(suite):
    return set(AUTOMATIC.get(suite, ())) | set(INSTALL_RECOVERY_REQUIRED.get(suite, ()))
