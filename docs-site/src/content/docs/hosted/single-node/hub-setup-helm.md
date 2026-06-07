---
title: Hub Setup (Helm)
description: Deploy the Scion Hub to a Kubernetes cluster with the official Helm chart and Gateway API.
---

**What you will learn**: How to deploy the Scion Hub on Kubernetes using the
upstream Helm chart, expose it through a Gateway API `Gateway` you already
operate, and connect external Runtime Brokers (laptops, dedicated VMs,
hardware) to it.

This is the recommended path for any team that already has a Kubernetes
platform with a Gateway API implementation. For single-VM deployments without
Kubernetes, see [Hub Setup on GCE](/scion/hosted/single-node/hub-setup-gce/) or the
[Hub Server](/scion/hosted/single-node/hub-server/) reference.

## What the chart deploys

The chart at `deploy/helm/scion-hub/` ships a small, opinionated set of
resources designed to be safe by default:

| Kind | Purpose |
| :--- | :--- |
| `Deployment` (1 replica, `Recreate` strategy) | Hub API + Web Dashboard in a single pod |
| `Service` (`ClusterIP`) | Stable cluster-internal endpoint |
| `HTTPRoute` (Gateway API v1) | Attaches the Service to a `Gateway` you own |
| `Secret` | Session signing key + (optional) dev token / OAuth secrets / Discord webhook |
| `ConfigMap` | `settings.yaml` mounted at `/home/scion/.scion/settings.yaml` |
| `PersistentVolumeClaim` (RWO) | sqlite database, template storage, cache |
| `ServiceAccount` | Pod identity (no RBAC bindings — Hub does not call the K8s API) |
| `PodDisruptionBudget` *(opt-in)* | Protects the single replica from voluntary node drains |
| `NetworkPolicy` *(opt-in)* | Restricts ingress to the Gateway namespace |
| `ReferenceGrant` *(opt-in)* | When the `Gateway` lives in a different namespace |

What the chart explicitly does **not** ship:

- A `Gateway` or `GatewayClass`. These are platform-team infrastructure with
  their own TLS configuration and lifecycle — bring your own.
- A Runtime Broker. Brokers run **outside** the cluster (or as a separate
  workload — see [Runtime Broker](/scion/hosted/ha/runtime-broker/)). The
  chart-rendered Hub does not enable the colocated broker, so an in-cluster
  Hub never competes for the same broker ID with external brokers.
- Postgres. SQLite only, single-replica only. Multi-replica Postgres is on the
  roadmap (`server.database.driver: postgres` is wired in `settings.yaml` but
  not yet implemented in the Hub binary as of `appVersion` 0.1.0).
- A Telegram plugin. Add it as a sidecar or via `extraVolumes` + an init
  container once the API stabilizes.

## Prerequisites

### Kubernetes & Gateway API

- Kubernetes **1.29+** (Gateway API `v1` graduated to GA in 1.30; on 1.29 you
  install the CRDs out-of-band):
  ```bash
  kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.0/standard-install.yaml
  ```
- A Gateway API implementation. Any conformant one works — these are tested:
  - **Istio** (1.23+, native Gateway API)
  - **Cilium** (1.16+ with `gatewayAPI.enabled=true`)
  - **Envoy Gateway**
  - **NGINX Gateway Fabric**
  - **kgateway**

- An existing `Gateway` with at least one HTTPS Listener bound to a TLS
  certificate Secret. If you do not have one yet, here's the minimal shape —
  adapt to your `GatewayClass`:

  ```yaml
  apiVersion: gateway.networking.k8s.io/v1
  kind: Gateway
  metadata:
    name: prod-gateway
    namespace: gateway-system
  spec:
    gatewayClassName: <your-gatewayclass>   # e.g. cilium, istio, envoy-gateway
    listeners:
      - name: https
        protocol: HTTPS
        port: 443
        hostname: "hub.example.com"
        tls:
          mode: Terminate
          certificateRefs:
            - kind: Secret
              name: hub-tls   # your cert + key in this namespace
        allowedRoutes:
          namespaces:
            from: Selector
            selector:
              matchLabels:
                gateway.scion.dev/allow-route: "true"
  ```

  Then label the namespace where you'll install scion-hub:
  ```bash
  kubectl create namespace scion
  kubectl label namespace scion gateway.scion.dev/allow-route=true
  ```

### Container image

A canonical upstream registry has not been pinned. Build and push the image
yourself (one-time):

```bash
# From a clone of the scion repo:
image-build/scripts/build-images.sh \
  --target hub \
  --registry ghcr.io/your-org \
  --push
```

This produces `ghcr.io/your-org/scion-hub:<tag>` and dependent base layers.
See [Building Custom Images](/scion/local/custom-images/).

### Storage class

A default `StorageClass` (or `persistence.storageClass=<name>`) capable of
`ReadWriteOnce` provisioning. The sqlite database lives on this volume; the
Hub re-creates schema on first start.

## Quick install (development)

For a fast local check on a dev cluster (kind, minikube, k3d), skip the
Gateway entirely and port-forward:

```bash
git clone https://github.com/GoogleCloudPlatform/scion
cd scion

helm install scion-hub deploy/helm/scion-hub \
  --namespace scion --create-namespace \
  --set image.repository=ghcr.io/your-org/scion-hub \
  --set image.tag=latest \
  --set gateway.enabled=false \
  --set persistence.enabled=false

kubectl port-forward -n scion svc/scion-hub 9080:9080
curl http://localhost:9080/healthz
```

The `dev` auth mode auto-generates a token. Read it out of the pod:

```bash
kubectl exec -n scion deploy/scion-hub -- cat /home/scion/.scion/dev-token
```

## Production install

### 1. Build the values file

Save as `values-prod.yaml`:

```yaml
image:
  repository: ghcr.io/your-org/scion-hub
  tag: "v0.1.0"

hub:
  publicUrl: https://hub.example.com
  adminEmails:
    - you@example.com
  authorizedDomains:
    - example.com

auth:
  mode: oauth
  # Bring the OAuth client_id/secret in via an externally-managed Secret —
  # works with external-secrets-operator, sealed-secrets, helm-secrets, etc.
  existingSecret: scion-hub-auth

gateway:
  hostnames: [hub.example.com]
  parentRefs:
    - name: prod-gateway
      namespace: gateway-system
      sectionName: https     # match the Listener name on the Gateway

persistence:
  enabled: true
  size: 20Gi
  storageClass: fast-ssd
  annotations:
    helm.sh/resource-policy: keep   # do NOT delete the PVC on uninstall

imageRegistry: ghcr.io/your-org      # brokers will pull agent images from here

podDisruptionBudget:
  enabled: true
  minAvailable: 1

networkPolicy:
  enabled: true
  ingressFrom:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: gateway-system

serviceAccount:
  create: true
  automountServiceAccountToken: false
```

### 2. Prepare the auth Secret

The chart consumes a single Secret with well-known keys. Only ship the keys you
actually use:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: scion-hub-auth
  namespace: scion
type: Opaque
stringData:
  session-secret: "<openssl rand -hex 32>"
  oauth-google-web-client-id:     "..."
  oauth-google-web-client-secret: "..."
  oauth-google-cli-client-id:     "..."
  oauth-google-cli-client-secret: "..."
  # oauth-github-web-client-id, oauth-github-web-client-secret, …
```

In a GitOps workflow, render this from an external-secrets `ExternalSecret`
or a `SealedSecret`.

### 3. Install

```bash
helm install scion-hub deploy/helm/scion-hub \
  --namespace scion --create-namespace \
  -f values-prod.yaml

kubectl rollout status -n scion deploy/scion-hub
helm test -n scion scion-hub
curl https://hub.example.com/healthz
```

### 4. Connect a Runtime Broker

On the machine that will execute agents (laptop, dedicated VM, etc.):

```bash
export SCION_HUB_ENDPOINT=https://hub.example.com

# First-time CLI auth — uses Google/GitHub OAuth if configured on the Hub.
scion auth login

# Register the broker (HMAC handshake — stored in ~/.scion/broker-credentials.json).
scion broker register

# Authorize the broker as a provider for one or more projects.
scion broker provide
scion broker status
```

See [Runtime Broker](/scion/hosted/ha/runtime-broker/) and
[Multi-Broker Setup](/scion/hosted/ha/multi-broker/) for the broker-side flow.

## Upgrading

```bash
helm upgrade scion-hub deploy/helm/scion-hub -n scion -f values-prod.yaml
```

A `Recreate` rollout briefly takes the Hub offline (PVC release → new pod
attach). For multi-second downtime tolerance, schedule upgrades in a window.

The chart computes content checksums of the rendered `ConfigMap` and
chart-managed `Secret` into the Pod template's annotations, so changes there
roll the pod automatically. External `Secret`s referenced via
`auth.existingSecret` do **not** trigger a roll — restart manually after
updating them:

```bash
kubectl rollout restart -n scion deploy/scion-hub
```

## Common customizations

### Use an existing PVC

```yaml
persistence:
  enabled: true
  existingClaim: my-precreated-claim
```

### Cross-namespace Gateway

If your Gateway lives in a different namespace, enable the `ReferenceGrant`
template and apply it on the **Gateway's** namespace (Helm cannot render
resources into a foreign namespace, so the chart emits the manifest and the
operator applies it once):

```yaml
gateway:
  parentRefs:
    - name: prod-gateway
      namespace: gateway-system
  referenceGrant:
    enabled: true
    fromNamespace: gateway-system
```

```bash
helm template scion-hub deploy/helm/scion-hub -n scion -f values-prod.yaml \
  --show-only templates/referencegrant.yaml \
  | kubectl apply -n gateway-system -f -
```

### Add extra Hub env vars

```yaml
extraEnv:
  - name: SCION_DEBUG
    value: "1"
  - name: SCION_LOG_GCP
    value: "true"
```

### Override the HTTPRoute rules

Default is a single `PathPrefix=/` rule. To split paths or add filters:

```yaml
gateway:
  rules:
    - matches:
        - path: { type: PathPrefix, value: / }
      filters:
        - type: RequestHeaderModifier
          requestHeaderModifier:
            set: [{ name: X-Forwarded-Proto, value: https }]
      backendRefs:
        - name: scion-hub
          port: 9080
```

(When you override `rules`, you must include the `backendRefs` — the chart
default no longer applies.)

## Troubleshooting

| Symptom | Likely cause | Fix |
| :--- | :--- | :--- |
| `helm install` fails with `image.repository is required` | No image set | `--set image.repository=ghcr.io/your-org/scion-hub` |
| `helm install` fails with `gateway.parentRefs[0].name is required` | No Gateway selected | Set `gateway.parentRefs`, or `gateway.enabled=false` for port-forward testing |
| Pod CrashLoopBackOff with `permission denied` writing to `/home/scion/.scion` | PVC `fsGroup` mismatch | Confirm `podSecurityContext.fsGroup: 1000` and `fsGroupChangePolicy: OnRootMismatch`; some storage drivers don't honor `fsGroup` — use a CSI driver that does |
| `/healthz` returns 503 | Hub is up but DB is provisioning | Check `kubectl logs`; first start takes ~10 s. The `startupProbe` allows up to 2.5 min |
| HTTPRoute Accepted but routing fails | Listener `allowedRoutes` excludes this namespace | Match the Gateway's `allowedRoutes.namespaces.selector` (e.g. label `gateway.scion.dev/allow-route=true`) |
| Broker `register` returns 401 | Wrong dev token / OAuth not signed in | `kubectl exec deploy/scion-hub -- cat /home/scion/.scion/dev-token` or sign in via the Web UI |
| `helm uninstall` deletes the database | Default behavior for unannotated PVCs | Always set `persistence.annotations.helm.sh/resource-policy: keep` for production |

## Security notes

- The container runs as **UID 1000**, non-root, with `readOnlyRootFilesystem:
  true` and **all Linux capabilities dropped**. The default `seccompProfile`
  is `RuntimeDefault` on both the Pod and the container.
- `automountServiceAccountToken: false` — the Hub does not call the
  Kubernetes API at runtime. No `Role`/`RoleBinding` is created.
- `networkPolicy.enabled=true` restricts ingress to the listed namespaces.
  Combine with mesh sidecar authorization for layered defense.
- TLS is terminated at the Gateway, not at the Hub. The Gateway → Pod hop is
  plaintext HTTP inside the cluster network — use a service mesh with mTLS
  if your threat model requires it.
- Secrets are mounted via `valueFrom.secretKeyRef`, not as files. Use a
  CSI secrets driver (e.g. Vault Agent Injector, AWS Secrets Manager CSI)
  via `extraEnvFrom` if you need short-lived secret rotation.

## Reference

- Chart source: [`deploy/helm/scion-hub/`](https://github.com/GoogleCloudPlatform/scion/tree/main/deploy/helm/scion-hub)
- Values reference: [`values.yaml`](https://github.com/GoogleCloudPlatform/scion/blob/main/deploy/helm/scion-hub/values.yaml) (every key documented inline)
- Server configuration reference: [Server Config](/scion/reference/server-config/)
- Gateway API: <https://gateway-api.sigs.k8s.io/>
