package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultConfigPath  = "/kube-arr/config/transcoder.json"
	defaultPodInfoPath = "/kube-arr/podinfo"
)

// Config is written by the Helm chart into a ConfigMap.
type Config struct {
	// Mode is the media server: plex, jellyfin or emby.
	Mode string `json:"mode"`
	// Binary is the transcoder's original path, e.g.
	// "/usr/lib/plexmediaserver/Plex Transcoder". In the media server the
	// real binary has been moved to Binary + ".real"; in transcode pods the
	// image is untouched, so the real binary is at Binary.
	Binary string `json:"binary"`
	// ServerURL is how transcode pods reach the media server (Plex only),
	// e.g. "http://media-plex:32400".
	ServerURL string `json:"serverUrl"`
	// FallbackToLocal runs the transcode in the media server if the pod
	// can't be created or scheduled in time.
	FallbackToLocal bool `json:"fallbackToLocal"`
	// StartTimeoutSeconds is how long a pod may stay Pending.
	StartTimeoutSeconds int `json:"startTimeoutSeconds"`
	// PodTemplate is a Pod manifest. The dispatcher fills in the transcode
	// container's command, args, env and workingDir.
	PodTemplate map[string]any `json:"podTemplate"`
}

// PodInfo comes from the downward API, so the dispatcher can own its pods.
type PodInfo struct {
	Namespace string
	Name      string
	UID       string
}

func loadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Binary == "" || c.PodTemplate == nil {
		return nil, fmt.Errorf("%s: binary and podTemplate are required", path)
	}
	if c.StartTimeoutSeconds <= 0 {
		c.StartTimeoutSeconds = 60
	}
	return &c, nil
}

func loadPodInfo(dir string) (*PodInfo, error) {
	read := func(name string) (string, error) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		return strings.TrimSpace(string(b)), err
	}
	ns, err := read("namespace")
	if err != nil {
		return nil, err
	}
	name, err := read("name")
	if err != nil {
		return nil, err
	}
	uid, err := read("uid")
	if err != nil {
		return nil, err
	}
	return &PodInfo{Namespace: ns, Name: name, UID: uid}, nil
}
