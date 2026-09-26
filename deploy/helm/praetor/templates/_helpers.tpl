{{/*
Chart name, overridable by nameOverride. Truncated to the 63-character limit a
Kubernetes object name allows, with a trailing dash removed so truncation cannot
produce an invalid name.
*/}}
{{- define "praetor.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Release-scoped resource name. fullnameOverride wins outright; otherwise the name
is "<release>-<chart name>". With both overrides empty that is the name this
chart has always rendered, so such a release keeps its Deployment and Service
across an upgrade; a set override now renames them (docs/guides/helm-chart.md).
*/}}
{{- define "praetor.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "praetor.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{/*
Labels every object carries. selectorLabels is the immutable subset the
Deployment selector and the Service selector match on; nothing else belongs
there, because a selector cannot be edited after the object exists.
*/}}
{{- define "praetor.selectorLabels" -}}
app.kubernetes.io/name: {{ include "praetor.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "praetor.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "praetor.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
ServiceAccount the pod runs as. When the chart creates the account, an explicit
serviceAccount.name wins and the release-scoped name is the default, so two
releases in one namespace never collide. When the chart creates nothing, the
pod falls back to the namespace's "default" account rather than naming an
account that does not exist.
*/}}
{{- define "praetor.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "praetor.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}
