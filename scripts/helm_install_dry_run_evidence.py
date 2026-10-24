"""Committed, additive requirements for install server preflight; not log inference."""
CREATE_MODES = (
    'valid', 'generated-metadata', 'changed-data', 'wrong-name', 'wrong-namespace',
    'wrong-kind', 'wrong-api', 'changed-label', 'changed-annotation', 'extra-annotation',
    'extra-root', 'finalizer', 'owner', 'deleting', 'generation', 'generate-name',
    'immutable', 'missing-uid', 'bad-uid', 'missing-time', 'bad-time', 'unknown-metadata',
    'bad-managed-fields', 'wrong-manager', 'empty-managed-fields', 'duplicate-root',
    'duplicate-meta', 'trailing', 'invalid-utf8', 'null-root', 'too-large', 'cancelled', 'nil-context',
)
HTTP_MODES = (
    'success', 'missing-create', 'pre-namespace', 'post-namespace', 'post-target',
    'post-history', 'deny-secret', 'deny-target', 'conflict', 'retry-after', 'redirect',
    'warning', 'compressed', 'wrong-type', 'wrong-status', 'bad-payload', 'wrong-identity',
    'malformed', 'oversized', 'cancelled', 'nil-context', 'wrong-session', 'wrong-backend',
    'candidate-copy', 'candidate-replaced', 'preparation-replaced', 'revoked',
    'cancel-first', 'revoke-first',
)
CLUSTER_MODES = (
    'admitted', 'unacknowledged', 'cancelled', 'revoked', 'deny-secret', 'deny-target',
    'release-race', 'target-race', 'history-race', 'namespace-race',
)
INSTALL_DRY_RUN_REQUIRED = {
    'unit-native': (
        'TestHelmInstallCreateFrozenBodies', 'TestHelmInstallCreatePlanLimits',
        *('TestHelmInstallCreatePlanLimits/' + x for x in (
            'nil-context', 'cancelled', 'expired', 'zero-candidate', '32-targets', 'ordered-binary')),
        'TestHelmInstallCreateStrictResponses',
        *('TestHelmInstallCreateStrictResponses/' + k for k in ('Secret', 'ConfigMap')),
        *('TestHelmInstallCreateStrictResponses/' + k + '/' + x for k in ('Secret', 'ConfigMap') for x in CREATE_MODES),
        'TestHelmInstallCreateDeterministic', 'TestHelmInstallDryRunHTTP',
        *('TestHelmInstallDryRunHTTP/' + x for x in HTTP_MODES),
        'TestHelmInstallDryRunAcknowledgement',
        *('TestHelmInstallDryRunAcknowledgement/' + x for x in (
            'unacknowledged', 'nil-context', 'nil-preparation', 'nil-candidate', 'wrong-session', 'expired', 'cancelled')),
        'TestHelmInstallDryRunConcurrentCopies',
        'TestHelmInstallDryRunRejectsReplacedPreparationCandidate',
        'TestHelmInstallDryRunTransportNoReplayOrErrorBody',
        *('TestHelmInstallDryRunTransportNoReplayOrErrorBody/' + str(x) for x in (301,307,401,403,409,422,429,500,503)),
        'TestHelmInstallDryRunTransportBudgetAndRoutes',
        *('TestHelmInstallDryRunTransportBudgetAndRoutes/' + x for x in (
            'nil-context', 'cancelled', 'zero-budget', 'empty-body', 'oversized-body',
            'wrong-resource', 'wrong-namespace', 'oversized-stream', 'trailer-warning',
            'short-body', 'duplicate-type', 'duplicate-encoding', 'uncompressed')),
    ),
    'real-cluster-native': (
        'TestRealHelmInstallDryRunNonPersistence',
        *('TestRealHelmInstallDryRunNonPersistence/' + x for x in CLUSTER_MODES),
    ),
}
