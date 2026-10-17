package main

import (
	"os"
	"strings"
)

// infoFlags are quick capability queries the media servers run at start-up
// and when checking hardware. They must run locally and return instantly.
var infoFlags = map[string]bool{
	"-version": true, "--version": true,
	"-h": true, "-help": true, "--help": true,
	"-buildconf": true, "-formats": true, "-muxers": true, "-demuxers": true,
	"-devices": true, "-codecs": true, "-decoders": true, "-encoders": true,
	"-bsfs": true, "-protocols": true, "-filters": true, "-pix_fmts": true,
	"-layouts": true, "-sample_fmts": true, "-colors": true, "-hwaccels": true,
}

// shouldDispatch decides whether a transcoder call becomes a pod.
//
// Real transcodes write to a file (HLS/DASH segments, thumbnails) under the
// shared config or transcode volume, which a pod can do just as well.
// Capability queries, and anything streaming its output back over a pipe,
// stay local because only the local process can hand that output back.
//
// KUBE_ARR_TRANSCODE=always|never overrides the decision (used in tests).
func shouldDispatch(args []string) bool {
	switch strings.ToLower(os.Getenv("KUBE_ARR_TRANSCODE")) {
	case "always":
		return true
	case "never":
		return false
	}
	if len(args) <= 1 {
		return false
	}
	for _, a := range args {
		if infoFlags[a] {
			return false
		}
	}
	out := args[len(args)-1]
	switch {
	case out == "-", out == "/dev/null", strings.HasPrefix(out, "pipe:"):
		return false
	case strings.HasPrefix(out, "-"):
		// The last argument is a flag, not an output: not a normal transcode.
		return false
	}
	return true
}

// rewriteArgs points Plex's callbacks at the media server's Service, since
// 127.0.0.1 inside a transcode pod is the pod itself.
func rewriteArgs(mode, serverURL string, args []string) []string {
	out := make([]string, len(args))
	copy(out, args)
	if mode != "plex" || serverURL == "" {
		return out
	}
	serverURL = strings.TrimSuffix(serverURL, "/")
	for i, a := range out {
		for _, local := range []string{"http://127.0.0.1:32400", "http://localhost:32400"} {
			if strings.Contains(a, local) {
				a = strings.ReplaceAll(a, local, serverURL)
			}
		}
		out[i] = a
	}
	return out
}

// forwardEnv copies the transcoder's environment into the pod. The media
// servers pass codec paths, library paths and tokens this way. Kubernetes
// injects its own service variables, and ours are only for this process.
func forwardEnv(environ []string) []map[string]string {
	var env []map[string]string
	seen := map[string]bool{}
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" || seen[k] {
			continue
		}
		if strings.HasPrefix(k, "KUBERNETES_") || strings.HasPrefix(k, "KUBE_ARR_") || k == "HOSTNAME" {
			continue
		}
		seen[k] = true
		env = append(env, map[string]string{"name": k, "value": v})
	}
	return env
}
