"""Independent additive census for #101 native archive integration.

Old evidence groups and their frozen digests remain unchanged. These names are
required in addition to them; none is a replacement or a waived old case.
"""
IDENTITY_NAMES = (
    'TestHelmJournalExistingArchiveOpen',
    *['TestHelmJournalExistingArchiveOpen/' + n for n in
      ('missing-directory', 'missing-lock', 'busy', 'valid')],
    'TestHelmJournalLoadedArchiveIdentity',
    *['TestHelmJournalLoadedArchiveIdentity/' + n for n in
      ('reopened-original', 'copied-identity', 'same-bytes-replacement',
       'directory-replacement', 'changed-bytes', 'archived')],
    'TestHelmJournalIdentifiedCorruptionHasNoToken',
)
WORKER_NAMES = (
    'TestHelmJournalArchiveSessionRoundTrip',
    'TestHelmJournalArchiveSessionCleanup',
    *['TestHelmJournalArchiveSessionCleanup/' + n for n in
      ('cancel', 'deadline', 'wrong-id', 'no-ack', 'copy-cancel', 'parent-cancel')],
    'TestHelmJournalArchiveSessionCopiesSingleUse',
    'TestHelmJournalArchiveSessionRejectsStaleAndUnknown',
    *['TestHelmJournalArchiveSessionRejectsStaleAndUnknown/' + n for n in
      ('unknown', 'same-bytes-replacement', 'wrong-directory',
       'changed-after-review', 'cancelled-start', 'nil-history')],
)
NATIVE_NAMES = (
    'TestNativeHelmArchiveLateCancelPreservesVerifiedResult',
    'TestNativeHelmArchiveMinimumWindowReview',
    'TestNativeHelmArchiveConnectionCancellation',
    'TestNativeHelmArchiveDifferentHistoryCannotConfirm',
    'TestNativeHelmArchiveExactConfirmation',
    'TestNativeHelmArchiveCompositionAndAcknowledgement',
    'TestNativeHelmArchiveCancellationAndScope',
    *['TestNativeHelmArchiveCancellationAndScope/' + n for n in
      ('stop', 'close-pane', 'close-window', 'path', 'backend', 'selection')],
    'TestNativeHelmArchiveDroppedCallbacksReleaseLock',
    'TestNativeHelmArchiveUnknownAndMinimumWindow',
    'TestNativeHelmArchiveDoesNotRequestCluster',
)
REAL_NAMES = (
    'TestNativeHelmArchiveAgainstRealCluster',
    'TestNativeHelmArchiveAgainstRealCluster/completed',
    'TestNativeHelmArchiveAgainstRealCluster/committed-response-loss',
)
AUTOMATIC = {suite: NATIVE_NAMES for suite in (
    'unit-native', 'native-os-interactions', 'migration-checked', 'migration-production')}
AUTOMATIC['real-cluster-native'] = (*NATIVE_NAMES, *REAL_NAMES)
AUTOMATIC['unit-native'] = (*IDENTITY_NAMES, *WORKER_NAMES, *NATIVE_NAMES)


def additions(suite):
    return set(AUTOMATIC.get(suite, ()))
