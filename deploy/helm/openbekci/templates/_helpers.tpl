{{- define "openbekci.name" -}}
{{- .Release.Name | trunc 50 | trimSuffix "-" -}}
{{- end -}}

{{- define "openbekci.labels" -}}
app.kubernetes.io/name: openbekci
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "openbekci.selector" -}}
app.kubernetes.io/name: openbekci
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{- define "openbekci.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end -}}

{{/* Secret keys a long-running process (server, worker) reads. */}}
{{- define "openbekci.appKeys" -}}
{{- $k := list "DATABASE_URL" "PLATFORM_DATABASE_URL" "SECRET_KEY" "ENCRYPTION_KEY" -}}
{{- if .Values.metrics.enabled }}{{- $k = append $k "METRICS_TOKEN" -}}{{- end -}}
{{- toJson $k -}}
{{- end -}}

{{/* Environment of a workload; takes {root, keys}: one OPENBEKCI_<KEY>_FILE per secret file. */}}
{{- define "openbekci.env" -}}
- name: OPENBEKCI_LISTEN_ADDR
  value: ":8080"
- name: OPENBEKCI_AUTO_MIGRATE
  value: "false"
{{- range $k := .keys }}
- name: OPENBEKCI_{{ $k }}_FILE
  value: /secrets/{{ index $.root.Values.secrets.keys $k }}
{{- end }}
{{- range $k, $v := .root.Values.otel }}
- name: {{ $k }}
  value: {{ $v | quote }}
{{- end }}
{{- with .root.Values.extraEnv }}
{{ toYaml . }}
{{- end }}
{{- end -}}

{{- define "openbekci.envFrom" -}}
- configMapRef:
    name: {{ include "openbekci.name" . }}
{{- with .Values.extraEnvFrom }}
{{ toYaml . }}
{{- end }}
{{- end -}}

{{/* The Secret as files; takes {root, keys}. */}}
{{- define "openbekci.secretVolume" -}}
- name: secrets
  secret:
    secretName: {{ required "secrets.existingSecret is required" .root.Values.secrets.existingSecret }}
    items:
      {{- range $k := .keys }}
      - key: {{ index $.root.Values.secrets.keys $k }}
        path: {{ index $.root.Values.secrets.keys $k }}
      {{- end }}
{{- end -}}

{{- define "openbekci.dataVolume" -}}
- name: data
  {{- if .Values.persistence.enabled }}
  persistentVolumeClaim:
    claimName: {{ .Values.persistence.existingClaim | default (include "openbekci.name" .) }}
  {{- else }}
  emptyDir: {}
  {{- end }}
{{- end -}}
