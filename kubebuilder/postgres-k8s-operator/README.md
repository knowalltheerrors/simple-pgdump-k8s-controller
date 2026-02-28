# postgres-k8s-operator

## Overview
`postgres-k8s-operator` is a simple Kubernetes operator (Kubebuilder/controller-runtime in Go) that watches `BackupJob` Custom Resources and creates a Kubernetes `batch/v1 Job` to run `pg_dump` against a PostgreSQL database (either inside the cluster or external). The dump is then uploaded to **S3-compatible object storage** such as **AWS S3**, **MinIO**, or **Scality RING**.

---

## Description
This project provides a small, practical example of an operator pattern:

- **CRD (`BackupJob`)** describes *what* backup you want (Postgres host/db/user + credentials in Secrets + bucket settings).
- **Controller** watches `BackupJob` resources and creates a Kubernetes **Job**.
- **Backup runner** container executes `pg_dump` and uploads the file to S3-compatible storage.

The operator updates `.status` fields to reflect progress (`Pending`, `Running`, `Succeeded`, `Failed`).  
To re-run a backup without deleting the CR, you can change `spec.runId` (which bumps `metadata.generation`).

> Note: The operator/controller image and the backup-runner image are different images.

---

## Prerequisites
- A Kubernetes cluster
- `kubectl` configured and pointing to your cluster
- `make` and `docker` (for building/pushing images)
- `go` installed (for `make generate` / `make manifests`), **or** run these targets in a Go container

---

## Generate CRDs (from Go types)
CRDs are generated from `api/v1alpha1/*_types.go` by `controller-gen` when you run:

```bash
make generate
make manifests
```

This will create/update CRD YAML files under:

- `config/crd/bases/`

---

## Apply the generated CRDs to the cluster
Apply the CRDs:

```bash
kubectl apply -f config/crd/bases/
```

Verify CRD exists:

```bash
kubectl get crd | grep backupjobs
```

---

## Build and push the operator (controller-manager) image
Build and push the operator image using Kubebuilder’s Makefile:

```bash
make docker-build docker-push IMG=docker.io/<your-user>/pgdump-k8s-operator:latest
```

> Tip: Using immutable tags (e.g. `v0.1.0`) is better than `latest` for production.

---

## Deploy the operator into the cluster (kustomize)
Before applying manifests, set the controller image in `config/default`:

```bash
cd config/default
kustomize edit set image controller=docker.io/<your-user>/pgdump-k8s-operator:latest
```

Now apply the deployment + RBAC + namespace + metrics service:

```bash
kubectl apply -k .
```

Verify the controller is running:

```bash
kubectl get pods -n postgres-k8s-operator-system
kubectl logs -n postgres-k8s-operator-system deploy/postgres-k8s-operator-controller-manager -c manager
```

---

## (Required for backup image) Build and push the backup-runner image
The Kubernetes Job created by the operator runs a **backup-runner** image that contains:
- `pg_dump` (postgresql-client)
- the `backup-runner` Go binary that uploads to S3-compatible storage

Example:

```bash
docker build -t docker.io/<your-user>/postgres-backup-runner:latest -f PostgresRunner.Dockerfile .
docker push docker.io/<your-user>/postgres-backup-runner:latest
```

In your `BackupJob` CR, set:

```yaml
spec:
  image: docker.io/<your-user>/postgres-backup-runner:latest
```

---

## Quick Test (create a BackupJob)
1) Create Secrets (Postgres password + object store keys)
2) Apply a `BackupJob` resource
3) Confirm the operator created a Kubernetes Job

Example commands:

```bash
kubectl get backupjobs -n default
kubectl get jobs -n default
kubectl logs -n default job/<job-name> -c backup-runner
```

---

## Example `BackupJob` CR

> Update the placeholders (`<your-user>`, Postgres host/db/user, secrets, endpoint) to match your environment.

```yaml
apiVersion: dbops.example.com/v1alpha1
kind: BackupJob
metadata:
  name: demo-backup
  namespace: default
spec:
  # backup-runner image (this is not operator/controller image)
  image: docker.io/<your-user>/postgres-backup-runner:latest

  postgres:
    host: postgres.default.svc.cluster.local   # or external hostname/IP
    port: 5432
    database: mydb
    username: myuser
    sslMode: disable
    passwordSecretRef:
      name: pg-conn-secret
      key: password

  storage:
    provider: s3
    bucket: pg-backups
    prefix: demo
    # For AWS S3 you can omit endpoint; for MinIO/Scality set it:
    endpoint: "https://minio.minio.svc.cluster.local:9000"
    region: us-east-1
    forcePathStyle: true
    accessKeySecretRef:
      name: objectstore-secret
      key: accessKey
    secretKeySecretRef:
      name: objectstore-secret
      key: secretKey

  # Kubernetes will delete the Job after it finishes (seconds)
  ttlSecondsAfterFinished: 300
```

Apply it:

```bash
kubectl apply -f backupjob.yaml
```

Watch status and Jobs:

```bash
kubectl get backupjobs -n default
kubectl get jobs -n default
kubectl get pods -n default -l job-name=demo-backup-1
kubectl logs -n default job/demo-backup-1 -c backup-runner
```

---

## Notes on HTTPS (MinIO/Scality)
If your S3-compatible endpoint uses a **self-signed** or **internal CA** certificate, your backup-runner container must trust that CA.
Recommended approach: Put your internal CA into the backup-runner image and run `update-ca-certificates` during image build.

---