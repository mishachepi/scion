# Stage 1: Build the web frontend assets
FROM node:22-alpine AS frontend
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --ignore-scripts
COPY web/ .
# npm run build already runs copy:shoelace-icons, vite build, and copy:client
RUN npm run build

# Stage 2: Build the Scion Hub binary (with embedded web assets)
FROM golang:1.26.1-alpine AS builder
WORKDIR /app
ENV GOWORK=off

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Copy built frontend assets into the embed location
COPY --from=frontend /web/dist/client web/dist/client

# Build a static binary (CGO_ENABLED=0) so it runs on the debian runtime image
# without musl/glibc mismatch from the Alpine builder.
RUN CGO_ENABLED=0 go build -o /scion ./cmd/scion/

# Stage 3: Create a minimal runtime image
FROM debian:bookworm-slim AS runtime
WORKDIR /app

# Install runtime dependencies used by the Hub broker and Cloud Run IAP exec path.
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates git openssh-client && rm -rf /var/lib/apt/lists/*

# Copy the binary from the builder stage
COPY --from=builder /scion /usr/local/bin/scion

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/scion"]

# Stage 4: Hub image for the Kubernetes chart (deploy/helm/scion-hub).
#
# The chart's image contract (see the chart's values.yaml, image.repository):
# non-root, uid 1000, web UI embedded, no baked KUBECONFIG. The web UI is
# embedded because the builder stage compiles without the no_embed_web tag and
# copies web/dist/client into the embed location. Nothing here writes a
# kubeconfig; in-cluster the hub uses the pod's service account.
#
# uid/gid 1000 matches the chart's securityContext defaults
# (hub.securityContext.runAsUser/runAsGroup), and a real passwd entry with a
# home directory is required because the hub resolves its state directory from
# the user's home (~/.scion) — the chart mounts its volumes there (hub.home).
#
#   docker build --target hub-gke -t <registry>/scion-hub-gke:<tag> .
#
# This stage is last, so a build without --target also produces it; the
# root-running stage above remains reachable as --target runtime.
FROM runtime AS hub-gke
RUN useradd -m -d /home/scion -u 1000 scion \
    && mkdir -p /home/scion/.scion \
    && chown -R scion:scion /home/scion
ENV HOME=/home/scion
USER scion
WORKDIR /home/scion
