{{- define "externalImages.csiAttacher" -}}
{{- if semverCompare ">=1.17-0" .Capabilities.KubeVersion.Version -}}
{{- print "k8s.gcr.io/sig-storage/csi-attacher:v3.3.0" -}}
{{- else -}}
{{- print "k8s.gcr.io/sig-storage/csi-attacher:v2.2.1" -}}
{{- end -}}
{{- end -}}

{{- define "externalImages.csiProvisioner" -}}
{{- if semverCompare ">=1.20-0" .Capabilities.KubeVersion.Version -}}
{{- print "k8s.gcr.io/sig-storage/csi-provisioner:v3.0.0" -}}
{{- else if semverCompare ">=1.17-0" .Capabilities.KubeVersion.Version -}}
{{- print "k8s.gcr.io/sig-storage/csi-provisioner:v2.2.2" -}}
{{- else -}}
{{- print "k8s.gcr.io/sig-storage/csi-provisioner:v1.6.1" -}}
{{- end -}}
{{- end }}

{{- define "externalImages.csiLivenessprobe" -}}
{{- print "k8s.gcr.io/sig-storage/livenessprobe:v2.4.0" -}}
{{- end }}

{{- define "externalImages.csiResizer" -}}
{{- print "k8s.gcr.io/sig-storage/csi-resizer:v1.3.0" -}}
{{- end }}

{{- define "externalImages.csiNodeDriverRegistrar" -}}
{{- print "k8s.gcr.io/sig-storage/csi-node-driver-registrar:v2.3.0" -}}
{{- end }}
