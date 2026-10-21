#!/usr/bin/env bash
# console.sh: forward the AI gateway console to http://localhost:3000 and keep
# it open (Ctrl-C stops it). Passwords: ./demo-password.sh, ./admin-password.sh
set -euo pipefail
cd "$(dirname "$0")"
export KUBECONFIG=$PWD/artifacts/kubeconfig
if lsof -nP -iTCP:3000 -sTCP:LISTEN >/dev/null 2>&1; then
  echo "localhost:3000 is already in use (a port-forward still running?):" >&2
  lsof -nP -iTCP:3000 -sTCP:LISTEN >&2
  exit 1
fi
cat <<MSG
Console: http://localhost:3000   (use localhost:3000 exactly)
  tenant demo:    tenant "demo" (not "lab"), Sign in as tenant admin, user admin, ./demo-password.sh
  platform admin: Sign in as platform admin, user admin, ./admin-password.sh
Ctrl-C stops the forward.
MSG
exec kubectl port-forward svc/f5-aigateway-console 3000:3000
