# scion-hub Helm chart

Helm chart for deploying the **Scion Hub** (central control plane) into a
Kubernetes cluster.

> **Scope**
>
> This chart deploys the Hub API + Web Dashboard only. **Runtime Brokers**
> (which execute agents) are intentionally out of scope — they run outside the
> cluster (laptops, dedicated VMs, Steam Decks, etc.) and connect to the Hub
> over its public URL. See the upstream
> [Runtime Broker guide](https://scion-ai.dev/hub-user/runtime-broker/).

## TL;DR

```bash
helm install scion-hub ./deploy/helm/scion-hub \
  --namespace scion \
  --create-namespace \
  --set image.repository=ghcr.io/your-org/scion-hub \
  --set image.tag=latest \
  --set hub.publicUrl=https://hub.example.com \
  --set gateway.hostnames[0]=hub.example.com \
  --set gateway.parentRefs[0].name=my-gateway \
  --set gateway.parentRefs[0].namespace=gateway-system
```

## Prerequisites

- Kubernetes **1.29+** (Gateway API GA'd in 1.30; the chart itself works on
  1.29 too, just install Gateway API CRDs separately).
- An installed and working Gateway API implementation
  (Istio, Cilium, Envoy Gateway, NGINX Gateway Fabric, kgateway, …).
- A `Gateway` resource with a TLS Listener already provisioned by your platform
  team. This chart **does not create a Gateway** — it only attaches an
  HTTPRoute to one.
- A default StorageClass (or set `persistence.storageClass`).

See the [Hub Setup (Helm) guide](https://scion-ai.dev/hub-admin/hub-setup-helm/)
for end-to-end instructions, including a Cilium / Istio / Envoy Gateway
walkthrough.

## Values

Everything is documented inline in [`values.yaml`](./values.yaml). The schema
in [`values.schema.json`](./values.schema.json) catches the most common
mistakes at install time. Highlights:

| Path | Type | Default | Notes |
|---|---|---|---|
| `image.repository` | string | `""` | **Required** — chart refuses to install without it |
| `hub.publicUrl` | string | `""` | Public HTTPS URL (used in OAuth callbacks + links) |
| `auth.mode` | string | `dev` | `dev` for single-tenant, `oauth` for multi-user |
| `gateway.enabled` | bool | `true` | Set to `false` if you proxy externally |
| `gateway.parentRefs[].name` | string | `""` | **Required when** `gateway.enabled=true` |
| `persistence.enabled` | bool | `true` | Disable only for ephemeral testing |
| `imageRegistry` | string | `""` | Registry brokers should pull agent images from |
| `auth.existingSecret` | string | `""` | Bring your own secret instead of the chart-managed one |

## Examples

### Development install (no Gateway, port-forward)

```bash
helm install scion-hub ./deploy/helm/scion-hub \
  --set image.repository=ghcr.io/your-org/scion-hub \
  --set gateway.enabled=false \
  --set persistence.enabled=false
kubectl port-forward svc/scion-hub 9080:9080
curl http://localhost:9080/healthz
```

### Production install with Google OAuth

```yaml
# values-prod.yaml
image:
  repository: ghcr.io/your-org/scion-hub
  tag: v0.1.0

hub:
  publicUrl: https://hub.example.com
  adminEmails:
    - me@example.com
  authorizedDomains:
    - example.com

auth:
  mode: oauth
  existingSecret: scion-hub-auth   # external-secrets / sealed-secrets / etc.

gateway:
  hostnames: [hub.example.com]
  parentRefs:
    - name: prod-gateway
      namespace: gateway-system
      sectionName: https

persistence:
  size: 20Gi
  storageClass: fast-ssd
  annotations:
    helm.sh/resource-policy: keep

imageRegistry: ghcr.io/your-org

podDisruptionBudget:
  enabled: true
  minAvailable: 1

networkPolicy:
  enabled: true
  ingressFrom:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: gateway-system
```

```bash
helm install scion-hub ./deploy/helm/scion-hub \
  --namespace scion \
  --create-namespace \
  -f values-prod.yaml
helm test scion-hub -n scion
```

## Verifying

```bash
kubectl rollout status -n scion deploy/scion-hub
helm test scion-hub -n scion
curl https://hub.example.com/healthz
```

## License

Apache-2.0 — same as the rest of the Scion repository.
