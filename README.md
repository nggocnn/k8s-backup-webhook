# Velero Backup Webhook

A Kubernetes admission webhook that automatically manages Velero backup resources for namespaces matching target labels.

## Overview

This webhook listens for namespace lifecycle events (`CREATE`, `UPDATE`, `DELETE`) and performs Velero operations:

- Create a Velero `Schedule`.
- Trigger an immediate Velero `Backup`.
- Delete the Velero `Schedule` when namespace is no longer a target or is deleted.

This gives you policy-driven, label-based backup automation without requiring manual schedule management per namespace.

## How It Works

1. Kubernetes API server sends admission requests to `POST /validate`.
2. Webhook parses the namespace object and checks labels.
3. If labels match target criteria, webhook calls Kubernetes API (in-cluster auth) to create Velero resources.
4. Webhook always returns `Allowed: true` admission response.

Health endpoint:

- `GET /health` returns `200 OK`.

## Target Label Criteria

A namespace is treated as a backup target when both conditions are true:

- `namespace.oam.dev/target` exists and is non-empty.
- `usage.oam.dev/runtime=target`.

## Event Behavior

- `CREATE`
  - If target labels match: create `Schedule`, then create immediate `Backup`.
- `UPDATE`
  - If transitions into target: create `Schedule` and immediate `Backup`.
  - If transitions out of target: delete `Schedule`.
- `DELETE`
  - If old namespace matched target: delete `Schedule`.

## Naming Convention

Resource names use `BACKUP_SUFFIX` (default `backup`):

- Schedule: `<namespace>-<backup_suffix>`
- Backup: `<namespace>-<backup_suffix>-<timestamp>`

Example with namespace `payments` and default suffix:

- Schedule: `payments-backup`
- Backup: `payments-backup-20260222091530`

## Repository Layout

- `velero-backup-webhook/main.go` - webhook server and admission logic.
- `velero-backup-webhook/Dockerfile` - hardened container image build.
- `velero-backup-webhook/go.mod`, `velero-backup-webhook/go.sum` - Go module files.
- `webhook-service.yaml` - RBAC, ServiceAccount, Deployment, Service, `MutatingWebhookConfiguration`.
- `webhook-cert-manager.yaml` - cert-manager issuers/certificates used for TLS.

## Configuration

Environment variables:

- `VELERO_NAMESPACE` (default: `velero`)
- `CRON_EXPRESSION` (default: `@every 1h`)
- `CSI_SNAPSHOT_TIMEOUT` (default: `10m`)
- `STORAGE_LOCATION` (default: `default`)
- `BACKUP_TTL` (default: `720h0m0s`)
- `DEFAULT_VOLUMES_TO_FS_BACKUP` (default: `true`)
- `BACKUP_SUFFIX` (default: `backup`)
- `LOG_FORMAT` (default: `text`, valid: `text|json`)
- `LOG_LEVEL` (default: `info`)

## Build

Build image from repository root:

```bash
docker build -t <registry>/velero-backup-webhook:latest -f velero-backup-webhook/Dockerfile velero-backup-webhook
```

Build binary locally:

```bash
cd velero-backup-webhook
go build -o ../bin/velero-backup-webhook .
```

## Deployment

1. Build and push image.
2. Update image in `webhook-service.yaml` (`nggocnn/velero-backup-webhook:latest`).
3. Apply cert-manager resources:

```bash
kubectl apply -f webhook-cert-manager.yaml
```

4. Apply webhook resources:

```bash
kubectl apply -f webhook-service.yaml
```

5. Verify resources:

```bash
kubectl -n default get sa velero-backup-webhook-sa
kubectl -n default get deploy velero-backup-webhook
kubectl -n default get svc velero-backup-webhook-service
kubectl get mutatingwebhookconfigurations velero-backup-webhook-config
kubectl -n default get secret velero-backup-webhook-cert
```

## TLS and Certificates

- Webhook reads cert files from:
  - `/etc/admission-webhook/tls/tls.crt`
  - `/etc/admission-webhook/tls/tls.key`
- `webhook-cert-manager.yaml` creates:
  - Certificate: `velero-backup-webhook-server`
  - Secret: `velero-backup-webhook-cert`
- Deployment mounts the same secret at `/etc/admission-webhook/tls`.
- CA bundle injection is configured with:
  - `cert-manager.io/inject-ca-from: default/velero-backup-webhook-server`

## Security Posture

Current baseline hardening:

- Distroless runtime image, non-root user (`UID/GID 65532`).
- Read-only root filesystem in pod security context.
- All Linux capabilities dropped except `NET_BIND_SERVICE` (bind to 443).
- Runtime seccomp profile set to `RuntimeDefault`.
- Resource requests/limits set in deployment.
- Go module verification in Docker build (`go mod verify`).

## Validate End-to-End

Create target namespace:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: example-target
  labels:
    namespace.oam.dev/target: my-target
    usage.oam.dev/runtime: target
```

Apply and verify:

```bash
kubectl apply -f namespace.yaml
kubectl -n velero get schedules
kubectl -n velero get backups
kubectl -n default logs deploy/velero-backup-webhook
```

Update namespace to remove target behavior:

```bash
kubectl label ns example-target usage.oam.dev/runtime-
kubectl -n velero get schedules
```

## Known Limitations / Operational Notes

- Side effects happen synchronously during admission handling.
  - Impact: Velero/Kubernetes API latency can increase admission latency.
- Admission response is always `Allowed: true`.
  - Impact: webhook does not reject namespace operations on business logic.
- Webhook uses in-cluster auth and requires correct RBAC + Velero CRDs installed.

## Troubleshooting

- TLS handshake/certificate errors:
  - Check `velero-backup-webhook-cert` secret and webhook CA injection annotation.
- Webhook timeouts:
  - Check pod logs and Kubernetes API connectivity from pod.
- No Velero resources created:
  - Verify namespace labels and `VELERO_NAMESPACE`.
  - Ensure Velero CRDs exist (`schedules.velero.io`, `backups.velero.io`).
- RBAC errors in logs:
  - Verify `velero-backup-webhook-role` and binding to `velero-backup-webhook-sa`.
