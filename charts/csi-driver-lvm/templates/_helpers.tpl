{{/* vim: set filetype=mustache: */}}

{{- define "csi-driver-lvm.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "csi-driver-lvm.fullname" -}}
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

{{- define "csi-driver-lvm.plugin.fullname" -}}
{{- include "csi-driver-lvm.fullname" . }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "csi-driver-lvm.labels" -}}
release: {{ .Release.Name }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{ if .Values.labels }}
{{ .Values.labels | toYaml}}
{{- end }}
{{- end }}

{{- define "csi-driver-lvm.csi-driver.fullname" -}}
{{- default (include "csi-driver-lvm.fullname" .) .Values.lvm.driverName | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "csi-driver-lvm.csi-driver.vgname" -}}
{{- default (include "csi-driver-lvm.fullname" .) .Values.lvm.vgName | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "csi-driver-lvm.serviceaccount.fullname" -}}
{{- include "csi-driver-lvm.fullname" . }}
{{- end }}
{{- define "csi-driver-lvm.clusterrole.fullname" -}}
{{- include "csi-driver-lvm.fullname" . }}
{{- end }}
{{- define "csi-driver-lvm.clusterrolebinding.fullname" -}}
{{- include "csi-driver-lvm.fullname" . }}
{{- end }}
{{- define "csi-driver-lvm.podsecuritypolicy.fullname" -}}
{{- include "csi-driver-lvm.fullname" . }}
{{- end }}
{{- define "csi-driver-lvm.role.fullname" -}}
{{- include "csi-driver-lvm.fullname" . }}
{{- end }}
{{- define "csi-driver-lvm.rolebinding.fullname" -}}
{{- include "csi-driver-lvm.fullname" . }}
{{- end }}
{{- define "csi-driver-lvm.daemonset.fullname" -}}
{{- include "csi-driver-lvm.fullname" . }}
{{- end }}
{{- define "csi-driver-lvm.daemonset.selectorlabels" -}}
app: {{ include "csi-driver-lvm.fullname" . }}
{{- end }}

{{/*
    EVICTION CONTROLLER
*/}}
{{- define "csi-driver-lvm.eviction.fullname" -}}
{{- printf "%s-controller" (include "csi-driver-lvm.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- define "csi-driver-lvm.eviction.serviceaccount.fullname" -}}
{{- include "csi-driver-lvm.eviction.fullname" . }}
{{- end }}
{{- define "csi-driver-lvm.eviction.clusterrole.fullname" -}}
{{- include "csi-driver-lvm.eviction.fullname" . }}
{{- end }}
{{- define "csi-driver-lvm.eviction.clusterrolebinding.fullname" -}}
{{- include "csi-driver-lvm.eviction.fullname" . }}
{{- end }}
{{- define "csi-driver-lvm.eviction.deployment.fullname" -}}
{{- include "csi-driver-lvm.eviction.fullname" . }}
{{- end }}
{{- define "csi-driver-lvm.eviction.selectorlabels" -}}
app: {{ include "csi-driver-lvm.eviction.fullname" . }}
{{- end }}

{{/*
    STORAGECLASS
*/}}
{{- define "csi-driver-lvm.storageclass.fullname" -}}
{{- printf "%s-%s" (include "csi-driver-lvm.fullname" .global ) .storageclass.name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "csi-driver-lvm.storageclass.parameters" -}}
{{- .storageclass.parameters | toYaml }}
{{- if and .storageclass.parameters.encryption (eq .storageclass.parameters.encryption "true" ) }}
{{- if empty (index .storageclass.parameters "csi.storage.k8s.io/node-stage-secret-name") }}
csi.storage.k8s.io/node-stage-secret-name: {{ include "csi-driver-lvm.storageclass.encryption.secret.fullname" . }}
{{- end }}
{{- if empty (index .storageclass.parameters "csi.storage.k8s.io/node-stage-secret-namespace") }}
csi.storage.k8s.io/node-stage-secret-namespace: {{ include "csi-driver-lvm.storageclass.encryption.secret.namespace" . }}
{{- end }}
{{- end }}
{{- end }}

{{- define "csi-driver-lvm.storageclass.encryption.secret.fullname" -}}
{{- if not (empty (index .storageclass.parameters "csi.storage.k8s.io/node-stage-secret-name")) }}
{{- index .storageclass.parameters "csi.storage.k8s.io/node-stage-secret-name" }}
{{- else }}
{{- printf "%s-%s" (include "csi-driver-lvm.fullname" .global ) .storageclass.name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "csi-driver-lvm.storageclass.encryption.secret.namespace" -}}
{{- if not (empty (index .storageclass.parameters "csi.storage.k8s.io/node-stage-secret-namespace")) }}
{{- index .storageclass.parameters "csi.storage.k8s.io/node-stage-secret-namespace" }}
{{- else }}
{{- .global.Release.Namespace }}
{{- end }}
{{- end }}