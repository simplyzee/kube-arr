package main

import (
	"encoding/json"
	"fmt"
)

const transcodeContainer = "transcode"

// buildPod fills the chart's pod template with this transcode's command,
// arguments, environment and working directory, and makes the media
// server's pod its owner so it is garbage-collected with the server.
func buildPod(cfg *Config, info *PodInfo, args []string, env []map[string]string, workDir string) (map[string]any, error) {
	// Deep-copy the template so repeated calls never share state.
	raw, err := json.Marshal(cfg.PodTemplate)
	if err != nil {
		return nil, err
	}
	var pod map[string]any
	if err := json.Unmarshal(raw, &pod); err != nil {
		return nil, err
	}
	pod["apiVersion"] = "v1"
	pod["kind"] = "Pod"

	meta, _ := pod["metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
		pod["metadata"] = meta
	}
	if info != nil && info.UID != "" {
		meta["ownerReferences"] = []any{map[string]any{
			"apiVersion":         "v1",
			"kind":               "Pod",
			"name":               info.Name,
			"uid":                info.UID,
			"blockOwnerDeletion": false,
		}}
	}

	spec, _ := pod["spec"].(map[string]any)
	if spec == nil {
		return nil, fmt.Errorf("pod template has no spec")
	}
	containers, _ := spec["containers"].([]any)
	var c map[string]any
	for _, item := range containers {
		if m, ok := item.(map[string]any); ok && m["name"] == transcodeContainer {
			c = m
		}
	}
	if c == nil {
		return nil, fmt.Errorf("pod template has no %q container", transcodeContainer)
	}

	c["command"] = []string{cfg.Binary}
	c["args"] = rewriteArgs(cfg.Mode, cfg.ServerURL, args)
	// Keep any env the chart set (e.g. TZ), then add the transcoder's own.
	existing, _ := c["env"].([]any)
	names := map[string]bool{}
	for _, e := range existing {
		if m, ok := e.(map[string]any); ok {
			if n, ok := m["name"].(string); ok {
				names[n] = true
			}
		}
	}
	for _, e := range env {
		if !names[e["name"]] {
			existing = append(existing, e)
		}
	}
	c["env"] = existing
	if workDir != "" {
		c["workingDir"] = workDir
	}
	return pod, nil
}
