#!/usr/bin/env bash
# demo-password.sh: the console password of tenant demo's admin.
# Sign in: tenant "demo" (replace the proposed "lab"), "Sign in as tenant admin", user admin.
set -euo pipefail
cd "$(dirname "$0")"
export KUBECONFIG=$PWD/artifacts/kubeconfig
tenant=${1:-demo}
kubectl get secret "aitenant-$tenant-admin" -o jsonpath='{.data.password}' | base64 -d; echo
