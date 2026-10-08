#!/usr/bin/env bash
# Disposable kind ONLY. The desktop never installs cluster components.
set -euo pipefail
: "${KUBECONFIG:?explicit isolated kubeconfig required}"
[ "${ASTER_E2E_ALLOW_DESTRUCTIVE:-}" = "1" ]
[ "${ASTER_E2E_CONTEXT:-}" = "kind-aster-e2e" ]
[ "$(kubectl config current-context)" = "kind-aster-e2e" ]
# Independently reject non-loopback endpoints before any cluster write.
kubectl config view --minify -o json | python3 -c '
import json,sys,ipaddress,urllib.parse
c=json.load(sys.stdin); u=urllib.parse.urlparse(c["clusters"][0]["cluster"]["server"])
assert u.scheme=="https" and ipaddress.ip_address(u.hostname).is_loopback, "not an isolated loopback API server"
'
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
curl --proto '=https' --tlsv1.2 --fail --location --retry 3 --max-time 90 \
  https://github.com/kubernetes-sigs/metrics-server/releases/download/v0.9.0/components.yaml \
  -o "$work/components.yaml"
printf '%s  %s\n' 1cec29a5267809306a2c6ec74a3e449abbb705b4a8beed0c8a1963910f72c79b "$work/components.yaml" | sha256sum -c -
kubectl apply -f "$work/components.yaml"
# kind's kubelet serving certificate is not trusted by this test add-on.
# This exception is restricted to the disposable test cluster, never app config.
kubectl -n kube-system patch deployment metrics-server --type=json \
  -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'
kubectl -n kube-system rollout status deployment/metrics-server --timeout=180s
