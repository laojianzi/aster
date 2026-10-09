#!/usr/bin/env bash
# This script owns only the dedicated kind cluster and temporary kubeconfig it creates.
set -euo pipefail
command -v kind >/dev/null
command -v kubectl >/dev/null
command -v go >/dev/null
root="$(cd "$(dirname "$0")/.." && pwd)"
name="aster-e2e-$$"
work="$(mktemp -d)"
cleanup() {
  kind delete cluster --name "$name" || true
  rm -rf "$work"
}
trap cleanup EXIT
export KUBECONFIG="$work/kubeconfig"
export ASTER_E2E_CONTEXT="kind-$name"
export ASTER_E2E_ALLOW_DESTRUCTIVE=1
export ASTER_TEST_ARTIFACTS="${ASTER_TEST_ARTIFACTS:-$root/artifacts/local-e2e}"
image="${ASTER_KIND_IMAGE:-kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5}"
kind create cluster --name "$name" --kubeconfig "$KUBECONFIG" --image "$image" --wait 120s
bash "$root/scripts/install-e2e-metrics.sh"
mkdir -p "$ASTER_TEST_ARTIFACTS"
cd "$root"
kubectl version > "$ASTER_TEST_ARTIFACTS/environment.txt"
git rev-parse HEAD >> "$ASTER_TEST_ARTIFACTS/environment.txt"
go test -race -tags=e2e -shuffle=on -count=1 -timeout=12m -json ./e2e/... ./internal/ui/... | tee "$ASTER_TEST_ARTIFACTS/tests.jsonl"
python3 scripts/test_evidence.py "$ASTER_TEST_ARTIFACTS/tests.jsonl" --suite real-cluster-native --require TestNativeScaleTracksActualReadiness --require TestNativeWorkbenchEditAgainstRealCluster --require TestNativeLogsAndPortForwardLifecycle --require TestNativePodCommandLifecycleAgainstRealCluster --require TestRealPodCommandExitOutputAndNoImplicitShell --require TestRealPodCommandRejectsReadOnlyIdentity --require TestRealPodCommandCancellationDoesNotInventAnExitCode --require TestRealPodCommandDeadlineDoesNotClaimTermination --require TestRealMetricsPodNodeAndIndependentRBAC --require TestNativeMetricsAgainstRealServerAndTeardown
