#!/usr/bin/env bash
# admin-password.sh: the console password of the platform admin, who creates
# and deletes tenants. Sign in: "Sign in as platform admin", user admin (no tenant).
set -euo pipefail
cd "$(dirname "$0")"
export KUBECONFIG=$PWD/artifacts/kubeconfig
kubectl get secret f5-aigateway-admin -o jsonpath='{.data.password}' | base64 -d; echo
