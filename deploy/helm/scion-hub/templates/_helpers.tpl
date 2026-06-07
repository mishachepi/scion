{{/*
Chart name (allows nameOverride).
*/}}
{{- define "scion-hub.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully qualified app name. Uses fullnameOverride first, then a release-name
combination. Capped at 63 chars to fit DNS-1123 limits.
*/}}
{{- define "scion-hub.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Chart label — e.g. "scion-hub-0.1.0".
*/}}
{{- define "scion-hub.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Standard recommended Kubernetes labels.
https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/
*/}}
{{- define "scion-hub.labels" -}}
helm.sh/chart: {{ include "scion-hub.chart" . }}
{{ include "scion-hub.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: scion
{{- with .Values.commonLabels }}
{{ toYaml . }}
{{- end }}
{{- end -}}

{{/*
Selector labels — stable across upgrades, used by Services/Deployments.
Do NOT add chart version or app version here; that breaks rolling upgrades.
*/}}
{{- define "scion-hub.selectorLabels" -}}
app.kubernetes.io/name: {{ include "scion-hub.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: hub
{{- end -}}

{{/*
Annotations applied to every resource. Merges commonAnnotations.
*/}}
{{- define "scion-hub.annotations" -}}
{{- with .Values.commonAnnotations }}
{{ toYaml . }}
{{- end }}
{{- end -}}

{{/*
ServiceAccount name to use.
*/}}
{{- define "scion-hub.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "scion-hub.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Image reference. Uses image.tag if set, falls back to .Chart.AppVersion.
Fails the install when image.repository is empty (forces explicit choice).
*/}}
{{- define "scion-hub.image" -}}
{{- if not .Values.image.repository -}}
{{- fail "image.repository is required (e.g. ghcr.io/your-org/scion-hub). See README." -}}
{{- end -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}

{{/*
Name of the Secret holding auth material — either chart-managed or external.
*/}}
{{- define "scion-hub.authSecretName" -}}
{{- if .Values.auth.existingSecret -}}
{{- .Values.auth.existingSecret -}}
{{- else -}}
{{- printf "%s-auth" (include "scion-hub.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Name of the chart-managed ConfigMap holding settings.yaml.
*/}}
{{- define "scion-hub.configMapName" -}}
{{- printf "%s-config" (include "scion-hub.fullname" .) -}}
{{- end -}}

{{/*
Validate the configuration is internally consistent. Called from NOTES.txt and
each major template to surface misconfiguration with a helpful message instead
of a runtime crash.

Order matters — checks fail-fast on the first match, so we list them from
"most fundamental" (image) to "least" (sub-feature toggles).
*/}}
{{- define "scion-hub.validate" -}}
{{- /* 1. Image must be set. */ -}}
{{- if not .Values.image.repository -}}
{{- fail "image.repository is required (e.g. ghcr.io/your-org/scion-hub). See README for upstream image build instructions." -}}
{{- end -}}

{{- /* 2. Gateway parentRefs must have a non-empty `name`. */ -}}
{{- if .Values.gateway.enabled -}}
  {{- $refs := default (list) .Values.gateway.parentRefs -}}
  {{- if eq (len $refs) 0 -}}
    {{- fail "gateway.enabled=true requires at least one entry in gateway.parentRefs." -}}
  {{- end -}}
  {{- range $i, $ref := $refs -}}
    {{- if not $ref.name -}}
      {{- fail (printf "gateway.parentRefs[%d].name is required when gateway.enabled=true." $i) -}}
    {{- end -}}
  {{- end -}}
{{- end -}}

{{- /* 3. OAuth mode needs at least one credential pair (or an existingSecret). */ -}}
{{- if eq .Values.auth.mode "oauth" -}}
  {{- if not .Values.auth.existingSecret -}}
    {{- $g := .Values.auth.oauth.google.web -}}
    {{- $gh := .Values.auth.oauth.github.web -}}
    {{- if and (not (and $g.clientId $g.clientSecret)) (not (and $gh.clientId $gh.clientSecret)) -}}
      {{- fail "auth.mode=oauth requires at least one of auth.oauth.{google,github}.web.{clientId,clientSecret}, or auth.existingSecret pointing to an externally-managed Secret." -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- end -}}
