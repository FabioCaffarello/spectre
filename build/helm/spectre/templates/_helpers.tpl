{{/*
Spectre Helm chart — named templates.

Cross-cutting concerns factored out of per-service templates:
labels, image references, common envs derived from Bitnami
subchart service names. See ADR-0030 §3 for the structural
rationale.
*/}}

{{/*
spectre.fullname — release-prefixed resource name.
Truncates at 63 chars to satisfy DNS-1123 label constraints.
*/}}
{{- define "spectre.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
spectre.chart — chart label value: <name>-<version>.
*/}}
{{- define "spectre.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
spectre.labels — common Kubernetes labels applied to every resource.
*/}}
{{- define "spectre.labels" -}}
helm.sh/chart: {{ include "spectre.chart" . }}
{{ include "spectre.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: spectre
{{- end }}

{{/*
spectre.selectorLabels — the stable subset used in Selector
matchers. Component label is *not* included here so call sites
add the per-component value alongside.
*/}}
{{- define "spectre.selectorLabels" -}}
app.kubernetes.io/name: {{ default .Chart.Name .Values.nameOverride }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
spectre.controlPlaneServiceAccountName — the operator's SA name.
Honours `controlPlane.serviceAccount.name` override, otherwise
synthesises `<release>-control-plane`.
*/}}
{{- define "spectre.controlPlaneServiceAccountName" -}}
{{- if .Values.controlPlane.serviceAccount.create }}
{{- default (printf "%s-control-plane" (include "spectre.fullname" .)) .Values.controlPlane.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.controlPlane.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
spectre.image — fully-qualified image reference for a service.

Usage:
  {{ include "spectre.image" (dict "service" .Values.engine "context" .) }}

Resolves to:
  <image.registry>/<service.image.repository>:<service.image.tag | .Chart.AppVersion>

The two-arg pattern keeps the helper service-agnostic — engine,
operator, and adapters all reuse it.
*/}}
{{- define "spectre.image" -}}
{{- $registry := .context.Values.image.registry -}}
{{- $repository := .service.image.repository -}}
{{- $tag := default .context.Chart.AppVersion .service.image.tag -}}
{{- printf "%s/%s:%s" $registry $repository $tag -}}
{{- end }}

{{/*
spectre.commonEnv — env vars shared across the engine + adapters.

Computed from Bitnami subchart service names following the
documented conventions:
  postgres → <release>-postgresql
  redis    → <release>-redis-master
  kafka    → <release>-kafka.<release>-kafka-headless (we expose
             the bootstrap service)
  minio    → <release>-minio

When a subchart is disabled (`<name>.enabled: false`), the
corresponding env var is **not** rendered — the user is expected
to supply the connection details via `extraEnv` against an
externally-managed instance.
*/}}
{{- define "spectre.commonEnv" -}}
{{- $fullname := include "spectre.fullname" . -}}
{{- if .Values.postgresql.enabled }}
- name: SPECTRE_POSTGRES_URL
  value: "postgres://{{ .Values.postgresql.auth.username }}:{{ .Values.postgresql.auth.password }}@{{ .Release.Name }}-postgresql:5432/{{ .Values.postgresql.auth.database }}?sslmode=disable"
{{- end }}
{{- if .Values.redis.enabled }}
- name: SPECTRE_REDIS_URL
  value: "redis://{{ .Release.Name }}-redis-master:6379"
{{- end }}
{{- if .Values.kafka.enabled }}
- name: SPECTRE_KAFKA_BROKERS
  value: "{{ .Release.Name }}-kafka:9092"
{{- end }}
{{- if .Values.minio.enabled }}
- name: SPECTRE_S3_ENDPOINT
  value: "http://{{ .Release.Name }}-minio:9000"
- name: SPECTRE_S3_ACCESS_KEY
  value: {{ .Values.minio.auth.rootUser | quote }}
- name: SPECTRE_S3_SECRET_KEY
  value: {{ .Values.minio.auth.rootPassword | quote }}
- name: SPECTRE_S3_BUCKET
  value: {{ .Values.minio.defaultBuckets | quote }}
{{- end }}
{{- end }}
