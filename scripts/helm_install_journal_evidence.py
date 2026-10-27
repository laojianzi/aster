"""Explicit additive requirements for v2 install WALs; not inferred from logs."""
SCHEMA = ('binding','candidate','preparation','namespace','namespace-uid','namespace-rv',
          'release','storage-name','storage-digest','target-digest','target-name','no-target',
          'duplicates','too-many','zero-time','expiry','extended-expiry','rv-budget')
TRANSITIONS = ('negative-index','out-of-range','out-of-order','wrong-request-digest','invalid-request-digest',
               'duplicate-intent','response-without-intent','completion-as-response','observe-without-response',
               'wrong-index','missing-uid','missing-rv','rejection-identity','unknown-identity','replay-unknown',
               'clear-unknown','replay-rejected','wrong-observed-uid','wrong-observed-rv','response-twice',
               'advance-pending','duplicate-object-uid','deploy-replacement','deploy-unchanged-rv',
               'finish-before-start','legacy-intent','legacy-result')
PREFIX = ('unknown-version','legacy-action','mixed-version','precreate-uid','precreate-rv',
          'extra-plan','wrong-previous','concatenated')
ADMISSION = ('install-to-install','install-to-uninstall','uninstall-to-install','legacy-uid-only',
             'different-binding','different-namespace','different-release','namespace-replaced','legacy-different-uid')
ARCHIVE = ('completed','rejected-no-effect','not-started','unknown','pending','partial','unclosed','forged-archive','record-limit')
CRASH = ('start','secret-intent','secret-response','target-intent','target-response','deploy-intent','deploy-response','complete')

def cases(root, modes):
    return (root, *(root + '/' + x for x in modes))

INSTALL_JOURNAL_NAMES = (
    'TestHelmInstallJournalRoundTrip',
    *cases('TestHelmInstallJournalSchema', SCHEMA),
    *cases('TestHelmInstallJournalTransitions', TRANSITIONS),
    *cases('TestHelmInstallJournalCheckedPrefix', PREFIX),
    *cases('TestHelmInstallJournalSharedAdmission', ADMISSION),
    *cases('TestHelmInstallJournalArchiveAndQuotas', ARCHIVE),
    *cases('TestHelmInstallJournalDurableFailures', (
        *(p + '/' + k for p in ('start','intent','response','observation','finish')
          for k in ('short-write','write-error','sync-error')), 'directory-sync')),
    'TestHelmInstallJournalConcurrentLease',
    'TestHelmInstallJournalLegacyEncodingAndWrongRecovery',
    'TestHelmInstallJournalProcessHelper',
    *cases('TestHelmInstallJournalProcessCrashRecovery', CRASH),
    'TestHelmInstallJournalDowngradeAndMixedVersion',
    *cases('TestHelmInstallJournalFileIntegrity', ('prefix-rewrite','file-replacement','lock-replacement','lock-content','corrupt-unrelated')),
    'TestHelmInstallJournalMaxTargetsAndCancellation',
    'TestHelmInstallJournalLegacyReleaseScope',
)
INSTALL_JOURNAL_REQUIRED = {'unit-native': (
    'TestHelmInstallDryRunFixtureConsumesBodyBeforeHook',
    *cases('TestHelmInstallDryRunCancellationOneShot', ('cancel', 'revoke')),
    *INSTALL_JOURNAL_NAMES,
    *cases('TestHelmInstallJournalRejectsUninstallRecovery', ('active','archived','corrupt-prefix')),
)}
