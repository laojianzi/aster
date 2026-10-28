"""Additive, privacy-safe capture diagnostics for the blocking Windows failures.

The accepted native-install 42+4 census is unchanged. These separate names
must finish in every applicable existing stream; no workflow budget or old
mandatory requirement is replaced.
"""


def cases(root, modes):
    return (root, *(root + '/' + mode for mode in modes))


RENDER_NAMES = (
    *cases('TestHelmRenderDiagnosticsValidation', (
        'nil-context', 'early-cancel', 'early-deadline', 'invalid-request',
        'busy', 'input-snapshot', 'distribution',
    )),
    *cases('TestHelmRenderDiagnosticsPreparationFailure', ('tempdir', 'private-files')),
    *cases('TestHelmRenderDiagnosticsRealHelper', ('success', 'render-error', 'output-limit')),
    'TestHelmRenderDiagnosticsPrivacyAndBounds',
    'TestHelmRenderDiagnosticsSnapshotIsolation',
)

HELPER_NAMES = (
    *cases('TestRenderHelperDiagnostics', (
        'success', 'nonzero', 'start-failure', 'parent-cancel', 'parent-deadline',
        'helper-timeout', 'stdout-limit', 'stderr-limit', 'nil-context', 'invalid-request',
    )),
    'TestRenderHelperDiagnosticsPrivacyAndBounds',
    'TestRenderHelperDiagnosticsCancellationRace',
)

UI_NAMES = (
    'TestNativeHelmRenderDiagnosticsFailurePrivacy',
    *cases('TestNativeHelmRenderDiagnosticsEarlyFailures', ('invalid-chart', 'parent-cancel')),
    'TestNativeHelmRenderDiagnosticsStaleCallback',
)

AUTOMATIC = {
    'unit-native': (*RENDER_NAMES, *HELPER_NAMES, *UI_NAMES),
    'native-os-interactions': (*HELPER_NAMES, *UI_NAMES),
    'real-cluster-native': UI_NAMES,
    'migration-checked': UI_NAMES,
    'migration-production': UI_NAMES,
}

ADDED_TEST_FILES = {
    'internal/credentialexec/render_diagnostics_test.go',
    'internal/helmrender/diagnostics_test.go',
    'internal/ui/helm_render_diagnostics_test.go',
}


def additions(suite):
    return set(AUTOMATIC.get(suite, ()))
