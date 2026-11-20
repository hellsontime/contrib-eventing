{{/* Names are prefixed with the release, so they stay apart from the platform's own in a shared namespace. */}}
{{- define "eventing.name" -}}
{{ .ctx.Release.Name }}-{{ .component }}
{{- end }}

{{- define "eventing.selectorLabels" -}}
app.kubernetes.io/name: eventing
app.kubernetes.io/instance: {{ .ctx.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{- define "eventing.labels" -}}
{{ include "eventing.selectorLabels" . }}
app.kubernetes.io/version: {{ .ctx.Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .ctx.Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .ctx.Chart.Name .ctx.Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "eventing.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{- define "eventing.ingressURL" -}}
http://{{ include "eventing.name" (dict "ctx" . "component" "ingress") }}.{{ .Release.Namespace }}.svc.cluster.local
{{- end }}

{{- define "eventing.containerSecurityContext" -}}
allowPrivilegeEscalation: false
capabilities: {drop: [ALL]}
readOnlyRootFilesystem: true
runAsNonRoot: true
{{- end }}

{{- define "eventing.podSpec" -}}
securityContext:
  seccompProfile: {type: RuntimeDefault}
{{- with .Values.hostAliases }}
hostAliases:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end }}
