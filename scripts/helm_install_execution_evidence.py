"""Explicit requirements for independent persistent install execution."""
def cases(root, modes):
    return (root, *(root + '/' + str(x) for x in modes))

AUTHORITY = ('unacknowledged','nil-context','cancelled','wrong-session','wrong-backend',
             'candidate-copy','candidate-replaced','revoked','origin-revoked','closed-store',
             'expired','report-replaced','store-replaced')
FAULTS = ('reject','conflict','proxy409','retry-after','redirect','loss','malformed','missing-rv',
          'wrong-content','warning','encoding','content-type','trailer','truncated',
          'observe-loss','observe-change','cancel','revoke','origin-revoke')
INSTALL_EXECUTION_REQUIRED = {
    'unit-native': (
        *cases('TestHelmInstallExecutionSuccess', (1,32)),
        *cases('TestHelmInstallExecutionAuthority', AUTHORITY),
        *cases('TestHelmInstallExecutionFaults', (str(p)+'/'+m for p in (1,2,3) for m in FAULTS)),
        *cases('TestHelmInstallExecutionReadGuards', ('no-patch','namespace-changed','history-conflict')),
        'TestHelmInstallExecutionCopiedConsent',
        *cases('TestHelmInstallExecutionDeployedMetadata', ('managed-null','managed-array')),
        *cases('TestHelmInstallExecutionJournalFailure', ('intent','response','observe','revoke-after-intent')),
        *cases('TestHelmInstallExecutionSharedAdmission', ('unresolved-install','closed')),
        'TestHelmInstallExecutionCorruptAppend',
        'TestHelmInstallExecutionProcessHelper',
        *cases('TestHelmInstallExecutionProcessCrash', (1,2,3)),
    ),
    'real-cluster-native': cases('TestRealHelmInstallExecution', ('success','storage-race','target-race','committed-response-loss')),
}
ADDED_TEST_FILES = {
    'internal/kube/helm_install_execution_fixture_test.go',
    'internal/kube/helm_install_execution_test.go',
    'internal/kube/helm_install_execution_journal_test.go',
    'internal/kube/helm_install_execution_process_test.go',
    'e2e/helm_install_execution_test.go',
}
