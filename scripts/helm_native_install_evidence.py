"""Additive evidence for the native revision-1 first-install workflow.

Only the native controller and worker connect the already accepted backend.
Historical requirements remain byte-for-byte and every new named case must
finish once in each applicable ordinary, production or real-cluster stream.
"""


def cases(root, modes):
    return (root, *(root + '/' + mode for mode in modes))


NATIVE_NAMES = (
    'TestNativeHelmInstallIndependentConfirmations',
    *cases('TestNativeHelmInstallFrozenInputs', (
        'modify-after-capture', 'remove-after-capture',
    )),
    *cases('TestNativeHelmInstallQueuedInputAndComposition', (
        'busy-consent', 'exact-text-and-composition', 'edited-input',
    )),
    *cases('TestNativeHelmInstallScopeRevocation', (
        'backend', 'session', 'namespace', 'context', 'journal-path', 'input',
        'stop', 'close-pane', 'disconnect', 'connection-cancel', 'expiry',
    )),
    *cases('TestNativeHelmInstallLateCallbacks', ('capture', 'prepare', 'dry-run')),
    'TestNativeHelmInstallDryRunOriginSurvivesWorker',
    *cases('TestNativeHelmInstallUncertainResultsAndFreshRecovery', (
        'pending-secret', 'create-target', 'final-patch', 'rejected-target',
    )),
    *cases('TestNativeHelmInstallMinimumWindow', ('scale-1-ltr', 'scale-1.5-rtl')),
    *cases('TestNativeHelmInstallJournalCancellation', (
        'nil-context', 'cancelled-before-open',
    )),
    *cases('TestNativeHelmInstallSharedJournalAdmission', (
        'busy', 'unresolved-v1', 'unresolved-v2', 'capacity',
    )),
    'TestNativeHelmInstallClosedWindowPreservesWAL',
)

REAL_NAMES = (
    *cases('TestNativeHelmInstallAgainstRealCluster', (
        'completed', 'conflict', 'response-loss',
    )),
)

AUTOMATIC = {suite: NATIVE_NAMES for suite in (
    'unit-native', 'native-os-interactions', 'real-cluster-native',
    'migration-checked', 'migration-production',
)}
NATIVE_INSTALL_REQUIRED = {'real-cluster-native': REAL_NAMES}

# Only these new paths are excluded by the historical inventories. The new
# scope freezes all 263 accepted test files / 278 paths at actual main85e9.
ADDED_TEST_FILES = {
    'internal/ui/helm_install_ui_fixture_test.go',
    'internal/ui/helm_install_ui_test.go',
    'internal/ui/helm_install_lifecycle_test.go',
    'internal/ui/helm_install_journal_test.go',
    'internal/ui/helm_install_e2e_test.go',
}


def additions(suite):
    return set(AUTOMATIC.get(suite, ())) | set(NATIVE_INSTALL_REQUIRED.get(suite, ()))
