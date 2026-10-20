"""Exact native consumers for restricted uninstall and local admission review.

This is a source-boundary regression helper, not a substitute for exercising
the native confirmations, lifecycle cancellation and actual backend requests.
"""
from pathlib import Path
import re


CALL_SITES = {
    'PrepareHelmUninstall': {'internal/ui/helm_uninstall.go': 1},
    'AcknowledgeHelmUninstallDryRun': {'internal/ui/helm_uninstall.go': 1},
    'PreflightHelmUninstall': {'internal/ui/helm_uninstall.go': 1},
    'AcknowledgeHelmUninstallReconciliation': {'internal/ui/helm_uninstall.go': 1},
    'ObserveHelmUninstallRecovery': {'internal/ui/helm_uninstall.go': 1},
    'OpenHelmUninstallJournal': {'internal/ui/helm_uninstall_journal.go': 2},
    'AcknowledgeHelmUninstallExecution': {'internal/ui/helm_uninstall_journal.go': 1},
    'ExecuteHelmUninstall': {'internal/ui/helm_uninstall_journal.go': 1},
    'ReviewHelmUninstallJournalAdmission': {'internal/ui/helm_admission.go': 1},
}


def production_calls(root: Path, name: str) -> dict[str, int]:
    """Count the admitted production calls; tests do not activate the product."""
    pattern = re.compile(r'\b' + re.escape(name) + r'\s*\(')
    found = {}
    for directory in ('cmd', 'internal/ui'):
        for path in (root / directory).rglob('*.go'):
            if path.name.endswith('_test.go'):
                continue
            count = len(pattern.findall(path.read_text()))
            if count:
                found[path.relative_to(root).as_posix()] = count
    return found
