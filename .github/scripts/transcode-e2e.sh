#!/usr/bin/env bash
# End-to-end test of transcode dispatch in a kind cluster.
# Usage: transcode-e2e.sh <plex|jellyfin|emby>
set -euo pipefail
server=$1
rel=media
deploy="deploy/$rel-$server"
case $server in
  plex)     bin="/usr/lib/plexmediaserver/Plex Transcoder" ;;
  jellyfin) bin="/usr/lib/jellyfin-ffmpeg/ffmpeg" ;;
  emby)     bin="/app/emby/bin/ffmpeg" ;;
esac
transcode_pods() { kubectl get pods -l app.kubernetes.io/component=transcode -o name; }
in_server() { kubectl exec "$deploy" -c "$server" -- "$@"; }

echo "== the transcoder was swapped for the dispatcher"
in_server test -x "$bin.real"
in_server sh -c "\"$bin\" --kube-arr-reaper 2>/dev/null; [ \$? -eq 2 ]"  # only our binary knows this flag

echo "== capability checks stay local"
in_server "$bin" -version 2>&1 | head -1 || true
[ -z "$(transcode_pods)" ] || { echo "a -version call created a pod"; exit 1; }

echo "== a dispatched call runs as a pod, streams its log back and is cleaned up"
out=$(in_server env KUBE_ARR_TRANSCODE=always "$bin" -version 2>&1) || true
echo "$out" | tail -5
echo "$out" | grep -q "dispatched transcode to pod"
for i in $(seq 1 30); do [ -z "$(transcode_pods)" ] && break; sleep 2; done
[ -z "$(transcode_pods)" ] || { echo "transcode pod left behind"; transcode_pods; exit 1; }

if [ "$server" != jellyfin ]; then
  echo "OK: $server dispatch plumbing works"
  exit 0
fi

echo "== a real transcode to HLS runs in a pod and its output lands in the server"
dir=/config/cache/transcodes
in_server sh -c "mkdir -p $dir && chown 1000:1000 $dir"
in_server "$bin" -hide_banner -f lavfi -i testsrc=duration=3:size=320x240:rate=10 \
  -c:v libx264 -preset ultrafast -f hls "$dir/ci.m3u8" 2> transcode.log || { cat transcode.log; exit 1; }
grep -q "dispatched transcode to pod" transcode.log
grep -Eq "frame=|time=" transcode.log   # ffmpeg progress streamed back
in_server test -s "$dir/ci.m3u8"
in_server sh -c "ls $dir/ci*.ts"
for i in $(seq 1 30); do [ -z "$(transcode_pods)" ] && break; sleep 2; done
[ -z "$(transcode_pods)" ] || { echo "transcode pod left behind"; exit 1; }

echo "== a hard-killed transcode still has its pod cleaned up"
in_server "$bin" -hide_banner -f lavfi -i testsrc=duration=600:size=320x240:rate=10 \
  -c:v libx264 -preset ultrafast -f hls "$dir/ci-kill.m3u8" > /dev/null 2>&1 &
for i in $(seq 1 60); do [ -n "$(transcode_pods)" ] && break; sleep 2; done
[ -n "$(transcode_pods)" ] || { echo "long transcode never created a pod"; exit 1; }
transcode_pods
in_server sh -c '
  for d in /proc/[0-9]*; do
    cmd=$(tr "\0" " " < "$d/cmdline" 2>/dev/null) || continue
    case "$cmd" in "'"$bin"' "*ci-kill*) echo "SIGKILL ${d#/proc/}"; kill -9 "${d#/proc/}" ;; esac
  done'
for i in $(seq 1 30); do [ -z "$(transcode_pods)" ] && break; sleep 2; done
[ -z "$(transcode_pods)" ] || { echo "pod survived its killed dispatcher"; transcode_pods; exit 1; }
echo "OK: jellyfin transcodes run as pods, stream progress, and are always cleaned up"
