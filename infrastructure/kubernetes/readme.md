# Kubernetes manifests

GitOps deployment for tedmcp, reconciled by Flux onto a Traefik + cert-manager cluster.

```
infrastructure/
└── kubernetes/
    ├── repository.yaml          # GitRepository + Flux Kustomization (bootstrap only)
    ├── kustomization.yaml       # sync entry point
    └── tedmcp/
        ├── namespace.yaml
        └── main/                # stable channel
            ├── deployment.yaml
            ├── service.yaml     # ClusterIP + PVC for notice cache
            ├── certificate.yaml # cert-manager TLS
            ├── ingress.yaml     # Traefik IngressRoute
            └── update.yaml      # ImageRepository + ImagePolicy + ImageUpdateAutomation
```

## Flux

`repository.yaml` defines the `GitRepository` (`flux-system/autoted-repository`, SSH, branch `main`, secret `autoted-auth`) and the `Kustomization` that reconciles `./infrastructure/kubernetes`.

Image rollout is automated:

- `ImageRepository` scans `bernardoforcillo/tedmcp` every minute.
- `ImagePolicy` selects the newest tag matching `^<YYYYMMDDHHMMSS>-<sha>$` (pushed by the CI on every merge to `main`).
- `ImageUpdateAutomation` rewrites the `# {"$imagepolicy": ...}` marker in `deployment.yaml` and commits back to `main`.

## Bootstrap

```bash
# 1. Create the SSH auth secret (deploy key with write access, needed by image automation)
flux create secret git autoted-auth \
  --namespace flux-system \
  --url ssh://git@github.com/bernardoforcillo/autoted

# 2. Apply the Flux source + sync (everything else follows from git)
kubectl apply -f infrastructure/kubernetes/repository.yaml
```

## Manual apply (without Flux)

```bash
kubectl apply -k infrastructure/kubernetes
```

## Required cluster components

- [Flux](https://fluxcd.io) — GitOps engine
- [Traefik](https://traefik.io) — ingress controller
- [cert-manager](https://cert-manager.io) — TLS certificates (`ClusterIssuer: letsencrypt-prod`)

## GitHub Actions secrets

| Secret | Value |
| --- | --- |
| `DOCKERHUB_USERNAME` | `bernardoforcillo` |
| `DOCKERHUB_TOKEN` | DockerHub access token (read/write) |
