# cwc-admin-access

Restrict access to sensitive data (how-to #1) — bearer token + mTLS to CWC admin API

- Rating: **green**

## What it does

Demonstrates the dual-gate access control that BNK puts in front
of CWC's sensitive admin endpoints:

  - mTLS at the TLS layer (CA + client cert + client key).
  - Bearer token at the HTTP layer (Authorization header).

Both materials are produced by the deploy-flo phase already:

  - cwc-license-client-certs Secret in f5-cne-core has
    ca-root-cert, client-cert, client-key (PEM).
  - cwc-auth-token Secret in f5-cne-core has the token
    string.

The scenario spawns a tiny curl-equipped probe Deployment in
scn-cwcadmin namespace with both secrets projected as files
and the token as an env var. Verify does two requests:

  1. Bearer token PRESENT — expects HTTP 200 with the
     documented JSON body (LicenseStatus + DigitalAssetID).
  2. Bearer token MISSING — expects 401/403 (the actual
     "restrict" part of "restrict access to sensitive data").

No new BNK CRs are introduced; this is a pure runtime-access
verification.

Cleanup deletes the scn-cwcadmin namespace.

## Manifests

- `01-namespace.yaml`
- `02-probe.yaml`

Not every file is applied unconditionally by `ocibnkctl scenario run`; check the
description above for ordering and waits between steps.
