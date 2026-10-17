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
{{- if and .Values.transcode.enabled (eq .Values.mediaServer "none") -}}
{{- fail "kube-arr: transcode.enabled needs a media server. Set mediaServer to plex, jellyfin or emby" -}}
{{- end -}}
{{- if not (include "kube-arr.enabledApps" . | fromJsonArray) -}}
{{- fail "kube-arr: no apps are enabled. Enable at least one, e.g. --set sonarr.enabled=true" -}}
{{- end -}}
{{- end -}}

{{/*
The shared media volume (a list item without the leading "- "), used by the
apps and by transcode pods.
*/}}
{{- define "kube-arr.mediaVolume" -}}
{{- $source := include "kube-arr.mediaSource" . -}}
name: media
{{- if eq $source "nfs" }}
nfs:
  server: {{ .Values.media.nfs.server }}
  path: {{ .Values.media.nfs.path }}
{{- else if eq $source "hostPath" }}
hostPath:
  path: {{ .Values.media.hostPath.path }}
  type: Directory
{{- else if eq $source "rclone" }}
hostPath:
  # Mounted by the kube-rclone DaemonSet on every node.
  path: {{ .Values.rclone.rclone.path }}
  type: DirectoryOrCreate
{{- else }}
persistentVolumeClaim:
  claimName: {{ include "kube-arr.mediaClaimName" . }}
{{- end }}
{{- end -}}

{{/*
Mount for the shared media volume. Usage:
include "kube-arr.mediaMount" (dict "root" $ "readOnly" true)
*/}}
{{- define "kube-arr.mediaMount" -}}
{{- $source := include "kube-arr.mediaSource" .root -}}
name: media
mountPath: {{ .root.Values.media.mountPath }}
{{- if .readOnly }}
readOnly: true
{{- end }}
{{- if eq $source "hostPath" }}
mountPropagation: {{ .root.Values.media.hostPath.mountPropagation }}
{{- else if eq $source "rclone" }}
# See the FUSE mount kube-rclone makes on the node.
mountPropagation: HostToContainer
{{- end }}
{{- end -}}

{{/*
Transcode dispatch is on when enabled and a media server is running.
*/}}
{{- define "kube-arr.transcodeEnabled" -}}
{{- if and .Values.transcode.enabled (has .Values.mediaServer (list "plex" "jellyfin" "emby")) -}}true{{- end -}}
{{- end -}}

{{/*
Where each media server image keeps its transcoder.
*/}}
{{- define "kube-arr.transcodeBinary" -}}
{{- $defaults := dict "plex" "/usr/lib/plexmediaserver/Plex Transcoder" "jellyfin" "/usr/lib/jellyfin-ffmpeg/ffmpeg" "emby" "/app/emby/bin/ffmpeg" -}}
{{- .Values.transcode.binary | default (get $defaults .Values.mediaServer) -}}
{{- end -}}

{{- define "kube-arr.dispatcherImage" -}}
{{- $img := .Values.transcode.dispatcher.image -}}
{{- printf "%s:%s" $img.repository (toString ($img.tag | default .Chart.Version)) -}}
{{- end -}}

{{/*
Pod template for transcode pods. The dispatcher fills in the transcode
container's command, args, env and workingDir.
*/}}
{{- define "kube-arr.transcodePodTemplate" -}}
{{- $server := .Values.mediaServer -}}
{{- $ctx := dict "root" . "name" $server -}}
{{- $app := include "kube-arr.app" $ctx | fromYaml -}}
{{- $fullName := include "kube-arr.appName" $ctx -}}
{{- $t := .Values.transcode -}}
metadata:
  generateName: {{ $fullName }}-transcode-
  labels:
    app.kubernetes.io/name: {{ $server }}-transcode
    app.kubernetes.io/instance: {{ .Release.Name }}
    app.kubernetes.io/part-of: {{ include "kube-arr.name" . }}
    app.kubernetes.io/component: transcode
  {{- with $t.podAnnotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
spec:
  restartPolicy: Never
  activeDeadlineSeconds: {{ int $t.maxDurationSeconds }}
  automountServiceAccountToken: false
  enableServiceLinks: false
  {{- with .Values.global.imagePullSecrets }}
  imagePullSecrets:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  securityContext:
    {{- $psc := deepCopy (.Values.global.podSecurityContext | default dict) }}
    {{- $_ := set $psc "runAsUser" (int .Values.global.puid) }}
    {{- $_ := set $psc "runAsGroup" (int .Values.global.pgid) }}
    {{- toYaml $psc | nindent 4 }}
  {{- with ($t.nodeSelector | default .Values.global.nodeSelector) }}
  nodeSelector:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  {{- with ($t.tolerations | default .Values.global.tolerations) }}
  tolerations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  {{- if $t.affinity }}
  affinity:
    {{- toYaml $t.affinity | nindent 4 }}
  {{- else if $t.sameNodeAsServer }}
  affinity:
    podAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        - topologyKey: kubernetes.io/hostname
          labelSelector:
            matchLabels:
              app.kubernetes.io/name: {{ $server }}
              app.kubernetes.io/instance: {{ .Release.Name }}
  {{- end }}
  containers:
    - name: transcode
      image: "{{ $app.image.repository }}:{{ $app.image.tag }}"
      imagePullPolicy: {{ $app.image.pullPolicy }}
      env:
        - name: TZ
          value: {{ .Values.global.timezone | quote }}
        {{- with $t.env }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
      {{- with $t.resources }}
      resources:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with $app.securityContext }}
      securityContext:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      volumeMounts:
        - name: config
          mountPath: /config
        {{- if $app.mountMedia }}
        - {{- include "kube-arr.mediaMount" (dict "root" . "readOnly" $app.mediaReadOnly) | nindent 10 }}
        {{- end }}
        {{- with $app.extraVolumeMounts }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
  volumes:
    - name: config
      persistentVolumeClaim:
        claimName: {{ $app.config.existingClaim | default (printf "%s-config" $fullName) }}
    {{- if $app.mountMedia }}
    - {{- include "kube-arr.mediaVolume" . | nindent 6 }}
    {{- end }}
    {{- with $app.extraVolumes }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
{{- end -}}

{{/*
The dispatcher's config file.
*/}}
{{- define "kube-arr.transcodeConfig" -}}
{{- $server := .Values.mediaServer -}}
{{- $ctx := dict "root" . "name" $server -}}
{{- $app := include "kube-arr.app" $ctx | fromYaml -}}
{{- $cfg := dict
  "mode" $server
  "binary" (include "kube-arr.transcodeBinary" .)
  "fallbackToLocal" .Values.transcode.fallbackToLocal
  "startTimeoutSeconds" (int .Values.transcode.startTimeoutSeconds)
  "podTemplate" (include "kube-arr.transcodePodTemplate" . | fromYaml)
-}}
{{- if eq $server "plex" -}}
{{- $_ := set $cfg "serverUrl" (printf "http://%s:%v" (include "kube-arr.appName" $ctx) $app.port) -}}
{{- end -}}
{{- toPrettyJson $cfg -}}
{{- end -}}
