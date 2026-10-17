{{/* vim: set filetype=mustache: */}}

{{- define "kube-arr.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kube-arr.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Every app the chart knows about, in display order.
*/}}
{{- define "kube-arr.allApps" -}}
{{- list "sonarr" "radarr" "lidarr" "bazarr" "prowlarr" "jackett" "plex" "jellyfin" "emby" | toJson -}}
{{- end -}}

{{/*
Enabled apps as a JSON list. Media servers are enabled through
.Values.mediaServer rather than their own flag.
*/}}
{{- define "kube-arr.enabledApps" -}}
{{- $enabled := list -}}
{{- range $name := include "kube-arr.allApps" . | fromJsonArray -}}
{{- if has $name (list "plex" "jellyfin" "emby") -}}
{{- if eq $.Values.mediaServer $name -}}{{- $enabled = append $enabled $name -}}{{- end -}}
{{- else if (index $.Values $name).enabled -}}
{{- $enabled = append $enabled $name -}}
{{- end -}}
{{- end -}}
{{- $enabled | toJson -}}
{{- end -}}

{{/*
An app's settings: appDefaults merged with the app's own block.
Usage: include "kube-arr.app" (dict "root" $ "name" "sonarr") | fromYaml
*/}}
{{- define "kube-arr.app" -}}
{{- $defaults := deepCopy .root.Values.appDefaults -}}
{{- $own := deepCopy (index .root.Values .name) -}}
{{- mergeOverwrite $defaults $own | toYaml -}}
{{- end -}}

{{/*
Resource name for an app: <release>-<app>, e.g. media-sonarr.
*/}}
{{- define "kube-arr.appName" -}}
{{- printf "%s-%s" .root.Release.Name .name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kube-arr.selectorLabels" -}}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/part-of: {{ include "kube-arr.name" .root }}
{{- end -}}

{{- define "kube-arr.labels" -}}
{{ include "kube-arr.selectorLabels" . }}
helm.sh/chart: {{ include "kube-arr.chart" .root }}
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
{{- end -}}

{{/*
Labels shared by the whole release, for resources that aren't one app.
*/}}
{{- define "kube-arr.releaseLabels" -}}
app.kubernetes.io/name: {{ include "kube-arr.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
helm.sh/chart: {{ include "kube-arr.chart" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "kube-arr.mediaClaimName" -}}
{{- .Values.media.existingClaim | default (printf "%s-media" .Release.Name | trunc 63 | trimSuffix "-") -}}
{{- end -}}

{{/*
Which media source is in use: rclone (kube-rclone mount), claim, nfs,
hostPath or managed (chart-created PVC).
*/}}
{{- define "kube-arr.mediaSource" -}}
{{- if .Values.rclone.enabled -}}rclone
{{- else if .Values.media.existingClaim -}}claim
{{- else if .Values.media.nfs.server -}}nfs
{{- else if .Values.media.hostPath.path -}}hostPath
{{- else -}}managed
{{- end -}}
{{- end -}}

{{/*
Fail early with clear messages.
*/}}
{{- define "kube-arr.validate" -}}
{{- if not (has .Values.mediaServer (list "plex" "jellyfin" "emby" "none")) -}}
{{- fail (printf "kube-arr: mediaServer must be plex, jellyfin, emby or none (got %q)" .Values.mediaServer) -}}
{{- end -}}
{{- $sources := 0 -}}
{{- if .Values.media.existingClaim }}{{ $sources = add1 $sources }}{{ end -}}
{{- if .Values.media.nfs.server }}{{ $sources = add1 $sources }}{{ end -}}
{{- if .Values.media.hostPath.path }}{{ $sources = add1 $sources }}{{ end -}}
{{- if .Values.rclone.enabled }}{{ $sources = add1 $sources }}{{ end -}}
{{- if gt $sources 1 -}}
{{- fail "kube-arr: set only one of media.existingClaim, media.nfs.server, media.hostPath.path or rclone.enabled" -}}
{{- end -}}
{{- if .Values.rclone.enabled -}}
{{- $r := .Values.rclone.rclone -}}
{{- if not (or $r.config $r.existingSecret) -}}
{{- fail "kube-arr: rclone.enabled needs your rclone config. Pass it with --set-file rclone.rclone.config=./rclone.conf or set rclone.rclone.existingSecret" -}}
{{- end -}}
{{- if not (hasPrefix "/" $r.path) -}}
{{- fail "kube-arr: rclone.rclone.path must be an absolute path on the node, e.g. /mnt/disks/rclone" -}}
{{- end -}}
{{- end -}}
{{- if and .Values.media.nfs.server (not .Values.media.nfs.path) -}}
{{- fail "kube-arr: media.nfs.path is required when media.nfs.server is set" -}}
{{- end -}}
{{- if .Values.ingress.enabled -}}
{{- range $name := include "kube-arr.enabledApps" $ | fromJsonArray -}}
{{- $app := include "kube-arr.app" (dict "root" $ "name" $name) | fromYaml -}}
{{- if and $app.ingress.enabled (not $app.ingress.hostname) (not $.Values.ingress.domain) -}}
{{- fail (printf "kube-arr: ingress is enabled but %s has no hostname. Set ingress.domain or %s.ingress.hostname" $name $name) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- if not (include "kube-arr.enabledApps" . | fromJsonArray) -}}
{{- fail "kube-arr: no apps are enabled. Enable at least one, e.g. --set sonarr.enabled=true" -}}
{{- end -}}
{{- end -}}
