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
            ├── service.yaml     # ClusterIP + PVCs for the notice cache and the team data
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

## Team access

The team tools (saved searches, shortlist, term packs) write to the `tedmcp-data`
volume, and anyone who can reach the endpoint can change them. Give each member of
the team a token, and the server requires one on every MCP request and records who
changed what:

```sh
kubectl -n tedmcp create secret generic tedmcp-auth \
  --from-literal=tokens="giulia:$(openssl rand -hex 24),marco:$(openssl rand -hex 24)"
kubectl -n tedmcp rollout restart deployment/tedmcp
```

Clients then send `Authorization: Bearer <token>`:

```sh
claude mcp add --transport http tedmcp https://tenders.tools.bernardoforcillo.com/mcp \
  --header "Authorization: Bearer <token>"
```

The secret is optional in the deployment, so the server starts without it — and
logs that the endpoint is open. `/healthz` and `/` never require a token.

The deployment runs one replica on purpose: the team data is a single file written
by one process. Back up `tedmcp-data` (it holds `team.json`); the cache can always be
rebuilt.
