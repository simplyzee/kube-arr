#!/usr/bin/env bash
# End-to-end test of transcode dispatch in a kind cluster.
# Usage: transcode-e2e.sh <plex|jellyfin|emby>
set -euo pipefail
server=$1
rel=media
deploy="deploy/$rel-$server"

# How each server runs its transcoder. Emby's ffmpeg has a relative ELF
# interpreter (lib/ld-linux...), so like Emby we run it from /app/emby with
# its libraries on the path. Real transcodes go to the server's default
# transcode directory inside /config.
workdir=/
envs=""
outdir=""
case $server in
  plex)     bin="/usr/lib/plexmediaserver/Plex Transcoder" ;;
  jellyfin) bin="/usr/lib/jellyfin-ffmpeg/ffmpeg"; outdir=/config/cache/transcodes ;;
  emby)     bin="/app/emby/bin/ffmpeg"; workdir=/app/emby; outdir=/config/transcoding-temp
            envs="LD_LIBRARY_PATH=/app/emby/lib:/app/emby/extra/lib" ;;
esac

transcode_pods() { kubectl get pods -l app.kubernetes.io/component=transcode -o name; }
show_pods() {
  kubectl get pods -l app.kubernetes.io/component=transcode \
    -o jsonpath='{range .items[*]}{.metadata.name} {.status.phase} cwd={.spec.containers[0].workingDir} args={.spec.containers[0].args}{"\n"}{end}'
}
wait_no_pods() {
  for _ in $(seq 1 45); do [ -z "$(transcode_pods)" ] && return 0; sleep 2; done
  echo "transcode pods still running:"; show_pods; return 1
}
in_server() { kubectl exec "$deploy" -c "$server" -- "$@"; }
# run_bin [VAR=value ...] -- <args>: run the transcoder the way the server does.
# Arguments are simple words here, so plain word splitting is enough.
run_bin() {
  local extra=""
  while [ "$1" != "--" ]; do extra+=" $1"; shift; done
  shift
  in_server sh -c "cd $workdir && $envs$extra exec \"$bin\" $*"
}

echo "== transcodes the server started by itself (start-up probes)"
startup=$(show_pods)
echo "$startup"
if [ -n "${GITHUB_ACTIONS:-}" ] && [ -n "$startup" ]; then
  echo "::notice title=$server start-up transcode pods::$(echo "$startup" | sed 's/%/%25/g' | awk '{printf "%s%%0A", $0}')"
fi
wait_no_pods

echo "== the transcoder was swapped for the dispatcher"
in_server test -x "$bin.real"
in_server sh -c "\"$bin\" --kube-arr-reaper 2>/dev/null; [ \$? -eq 2 ]"  # only our binary knows this flag

echo "== capability checks stay local"
out=$(run_bin -- -version 2>&1) || true
echo "$out" | head -3
if echo "$out" | grep -q "dispatched transcode to pod"; then echo "a -version call was dispatched"; exit 1; fi
if echo "$out" | grep -q "cannot run"; then echo "the real transcoder didn't run locally"; exit 1; fi

echo "== a dispatched call runs as a pod and is cleaned up"
out=$(run_bin KUBE_ARR_TRANSCODE=always -- -version 2>&1) || true
echo "$out" | tail -5
echo "$out" | grep -q "dispatched transcode to pod"
wait_no_pods

if [ -z "$outdir" ]; then
  echo "OK: $server dispatch plumbing works"
  exit 0
fi

echo "== a real transcode to HLS runs in a pod and its output lands in the server"
in_server sh -c "mkdir -p $outdir && chown 1000:1000 $outdir"
run_bin -- -hide_banner -f lavfi -i testsrc=duration=3:size=320x240:rate=10 \
  -c:v libx264 -preset ultrafast -f hls "$outdir/ci.m3u8" 2> transcode.log || { cat transcode.log; exit 1; }
cat transcode.log | tail -5
grep -q "dispatched transcode to pod" transcode.log
grep -Eq "frame=|time=" transcode.log   # ffmpeg progress streamed back
in_server test -s "$outdir/ci.m3u8"
in_server sh -c "ls $outdir/ci*.ts"
wait_no_pods

echo "== a hard-killed transcode still has its pod cleaned up"
run_bin -- -hide_banner -f lavfi -i testsrc=duration=600:size=320x240:rate=10 \
  -c:v libx264 -preset ultrafast -f hls "$outdir/ci-kill.m3u8" > /dev/null 2>&1 &
for _ in $(seq 1 60); do [ -n "$(transcode_pods)" ] && break; sleep 2; done
[ -n "$(transcode_pods)" ] || { echo "long transcode never created a pod"; exit 1; }
show_pods
in_server sh -c '
  for d in /proc/[0-9]*; do
    cmd=$(tr "\0" " " < "$d/cmdline" 2>/dev/null) || continue
    case "$cmd" in "'"$bin"' "*ci-kill*) echo "SIGKILL ${d#/proc/}"; kill -9 "${d#/proc/}" ;; esac
  done'
wait_no_pods || { echo "pod survived its killed dispatcher"; exit 1; }
echo "OK: $server transcodes run as pods, stream progress, and are always cleaned up"
