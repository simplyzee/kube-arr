package main

import (
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestShouldDispatch(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"version query", []string{"-version"}, false},
		{"encoders query", []string{"-hide_banner", "-encoders"}, false},
		{"hwaccels query", []string{"-hide_banner", "-hwaccels"}, false},
		{"no args", nil, false},
		{"pipe to stdout", []string{"-i", "in.mkv", "-f", "srt", "-"}, false},
		{"pipe protocol", []string{"-i", "in.mkv", "-f", "mp4", "pipe:1"}, false},
		{"null output", []string{"-i", "in.mkv", "-f", "null", "/dev/null"}, false},
		{"trailing flag", []string{"-i", "in.mkv", "-y"}, false},
		{"jellyfin hls", []string{"-i", "file:/data/media/tv/a.mkv", "-c:v", "libx264", "-f", "hls", "/config/cache/transcodes/abc.m3u8"}, true},
		{"plex dash", []string{"-i", "/data/m.mkv", "-f", "dash", "dash"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldDispatch(c.args); got != c.want {
				t.Fatalf("shouldDispatch(%q) = %v, want %v", c.args, got, c.want)
			}
		})
	}
}

func TestShouldDispatchOverride(t *testing.T) {
	t.Setenv("KUBE_ARR_TRANSCODE", "always")
	if !shouldDispatch([]string{"-version"}) {
		t.Fatal("always should dispatch")
	}
	t.Setenv("KUBE_ARR_TRANSCODE", "never")
	if shouldDispatch([]string{"-i", "a", "out.m3u8"}) {
		t.Fatal("never should not dispatch")
	}
}

func TestRewriteArgsPlex(t *testing.T) {
	in := []string{
		"-progressurl", "http://127.0.0.1:32400/video/:/transcode/session/x/progress",
		"-manifest_name", "http://localhost:32400/video/:/transcode/session/x/manifest",
		"-i", "/data/m.mkv",
	}
	got := rewriteArgs("plex", "http://media-plex:32400/", in)
	want := []string{
		"-progressurl", "http://media-plex:32400/video/:/transcode/session/x/progress",
		"-manifest_name", "http://media-plex:32400/video/:/transcode/session/x/manifest",
		"-i", "/data/m.mkv",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if in[1] != "http://127.0.0.1:32400/video/:/transcode/session/x/progress" {
		t.Fatal("input slice was modified")
	}
}

func TestRewriteArgsOnlyPlex(t *testing.T) {
	in := []string{"-i", "http://127.0.0.1:32400/x"}
	if got := rewriteArgs("jellyfin", "http://media-jellyfin:8096", in); !reflect.DeepEqual(got, in) {
		t.Fatalf("jellyfin args changed: %q", got)
	}
}

func TestForwardEnv(t *testing.T) {
	env := forwardEnv([]string{
		"PATH=/usr/bin", "KUBERNETES_SERVICE_HOST=10.0.0.1", "KUBE_ARR_TRANSCODE=always",
		"HOSTNAME=media-plex-abc", "X_PLEX_TOKEN=secret", "EQ=a=b", "PATH=/dup", "BROKEN",
	})
	got := map[string]string{}
	for _, e := range env {
		got[e["name"]] = e["value"]
	}
	want := map[string]string{"PATH": "/usr/bin", "X_PLEX_TOKEN": "secret", "EQ": "a=b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func testConfig() *Config {
	return &Config{
		Mode:                "plex",
		Binary:              "/usr/lib/plexmediaserver/Plex Transcoder",
		ServerURL:           "http://media-plex:32400",
		FallbackToLocal:     true,
		StartTimeoutSeconds: 5,
		PodTemplate: map[string]any{
			"metadata": map[string]any{"generateName": "media-plex-transcode-", "labels": map[string]any{"app": "x"}},
			"spec": map[string]any{
				"restartPolicy": "Never",
				"containers": []any{map[string]any{
					"name":  "transcode",
					"image": "lscr.io/linuxserver/plex:1",
					"env":   []any{map[string]any{"name": "TZ", "value": "Europe/London"}},
				}},
			},
		},
	}
}

func TestBuildPod(t *testing.T) {
	cfg := testConfig()
	info := &PodInfo{Namespace: "media", Name: "media-plex-0", UID: "uid-1"}
	env := []map[string]string{{"name": "TZ", "value": "UTC"}, {"name": "X_PLEX_TOKEN", "value": "t"}}
	pod, err := buildPod(cfg, info, []string{"-progressurl", "http://127.0.0.1:32400/p", "out"}, env, "/config/transcode/s1")
	if err != nil {
		t.Fatal(err)
	}
	c := pod["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	if !reflect.DeepEqual(c["command"], []string{cfg.Binary}) {
		t.Errorf("command = %v", c["command"])
	}
	if !reflect.DeepEqual(c["args"], []string{"-progressurl", "http://media-plex:32400/p", "out"}) {
		t.Errorf("args = %v", c["args"])
	}
	if c["workingDir"] != "/config/transcode/s1" {
		t.Errorf("workingDir = %v", c["workingDir"])
	}
	b, _ := json.Marshal(c["env"])
	if !strings.Contains(string(b), `"Europe/London"`) || strings.Contains(string(b), `"UTC"`) || !strings.Contains(string(b), "X_PLEX_TOKEN") {
		t.Errorf("env = %s (chart env should win, transcoder env should be added)", b)
	}
	owners := pod["metadata"].(map[string]any)["ownerReferences"].([]any)
	if owners[0].(map[string]any)["uid"] != "uid-1" {
		t.Errorf("ownerReferences = %v", owners)
	}
	// The template itself must stay untouched.
	if _, ok := cfg.PodTemplate["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)["command"]; ok {
		t.Error("template was mutated")
	}
}

func TestLoadConfigAndPodInfo(t *testing.T) {
	dir := t.TempDir()
	b, _ := json.Marshal(testConfig())
	p := filepath.Join(dir, "c.json")
	os.WriteFile(p, b, 0o644)
	cfg, err := loadConfig(p)
	if err != nil || cfg.Mode != "plex" {
		t.Fatalf("loadConfig: %v %v", cfg, err)
	}
	for k, v := range map[string]string{"namespace": "media\n", "name": "pod-a", "uid": "u-1"} {
		os.WriteFile(filepath.Join(dir, k), []byte(v), 0o644)
	}
	info, err := loadPodInfo(dir)
	if err != nil || info.Namespace != "media" || info.UID != "u-1" {
		t.Fatalf("loadPodInfo: %+v %v", info, err)
	}
	os.WriteFile(p, []byte(`{"mode":"plex"}`), 0o644)
	if _, err := loadConfig(p); err == nil {
		t.Fatal("expected error for missing binary/podTemplate")
	}
}

// fakeAPI is a minimal Kubernetes API: pods go Pending -> Running ->
// Succeeded and serve a fixed log.
type fakeAPI struct {
	mu      sync.Mutex
	created map[string]any
	gets    int32
	deleted []string
	exit    int
	pending bool // stay Pending forever
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer test-token" {
		w.WriteHeader(401)
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces/media/pods":
		var pod map[string]any
		json.NewDecoder(r.Body).Decode(&pod)
		f.mu.Lock()
		f.created = pod
		f.mu.Unlock()
		w.WriteHeader(201)
		fmt.Fprint(w, `{"metadata":{"name":"media-plex-transcode-x1"}}`)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/log"):
		if r.URL.Query().Get("follow") != "true" || r.URL.Query().Get("container") != "transcode" {
			w.WriteHeader(400)
			return
		}
		fmt.Fprint(w, "frame=  100 fps=50 time=00:00:04.00\n")
	case r.Method == http.MethodGet:
		n := atomic.AddInt32(&f.gets, 1)
		phase := "Pending"
		if !f.pending && n >= 2 {
			phase = "Running"
		}
		if !f.pending && n >= 4 {
			phase = "Succeeded"
			if f.exit != 0 {
				phase = "Failed"
			}
			fmt.Fprintf(w, `{"status":{"phase":%q,"containerStatuses":[{"state":{"terminated":{"exitCode":%d}}}]}}`, phase, f.exit)
			return
		}
		fmt.Fprintf(w, `{"status":{"phase":%q}}`, phase)
	case r.Method == http.MethodDelete:
		f.mu.Lock()
		f.deleted = append(f.deleted, r.URL.Path)
		f.mu.Unlock()
		fmt.Fprint(w, `{}`)
	default:
		w.WriteHeader(404)
	}
}

func setupFakeCluster(t *testing.T, api *fakeAPI) *Config {
	t.Helper()
	srv := httptest.NewTLSServer(api)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	t.Setenv("KUBERNETES_SERVICE_HOST", u.Hostname())
	t.Setenv("KUBERNETES_SERVICE_PORT", u.Port())
	t.Setenv("KUBE_ARR_NO_REAPER", "1")

	sa := t.TempDir()
	saDir = sa
	t.Cleanup(func() { saDir = "/var/run/secrets/kubernetes.io/serviceaccount" })
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	os.WriteFile(filepath.Join(sa, "ca.crt"), ca, 0o644)
	os.WriteFile(filepath.Join(sa, "token"), []byte("test-token\n"), 0o600)

	info := t.TempDir()
	for k, v := range map[string]string{"namespace": "media", "name": "media-plex-0", "uid": "uid-1"} {
		os.WriteFile(filepath.Join(info, k), []byte(v), 0o644)
	}
	t.Setenv("KUBE_ARR_PODINFO", info)
	return testConfig()
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stderr
	os.Stderr = w
	fn()
	w.Close()
	os.Stderr = old
	b, _ := io.ReadAll(r)
	return string(b)
}

func TestDispatchSucceeds(t *testing.T) {
	api := &fakeAPI{}
	cfg := setupFakeCluster(t, api)
	var code int
	var err error
	out := captureStderr(t, func() {
		code, err = dispatch(cfg, []string{"-progressurl", "http://127.0.0.1:32400/p", "-i", "/data/a.mkv", "dash"})
	})
	if err != nil || code != 0 {
		t.Fatalf("dispatch = %d, %v\n%s", code, err, out)
	}
	if !strings.Contains(out, "time=00:00:04.00") {
		t.Errorf("pod log not streamed to stderr:\n%s", out)
	}
	c := api.created["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	if fmt.Sprint(c["args"]) != "[-progressurl http://media-plex:32400/p -i /data/a.mkv dash]" {
		t.Errorf("args sent = %v", c["args"])
	}
	if len(api.deleted) == 0 {
		t.Error("pod was not deleted after completion")
	}
}

func TestDispatchPropagatesExitCode(t *testing.T) {
	api := &fakeAPI{exit: 1}
	cfg := setupFakeCluster(t, api)
	var code int
	captureStderr(t, func() { code, _ = dispatch(cfg, []string{"-i", "a", "out.m3u8"}) })
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

func TestDispatchPendingTimesOut(t *testing.T) {
	api := &fakeAPI{pending: true}
	cfg := setupFakeCluster(t, api)
	cfg.StartTimeoutSeconds = 1
	var err error
	captureStderr(t, func() { _, err = dispatch(cfg, []string{"-i", "a", "out.m3u8"}) })
	if err == nil || !strings.Contains(err.Error(), errNotStarted.Error()) {
		t.Fatalf("err = %v, want errNotStarted", err)
	}
	if len(api.deleted) == 0 {
		t.Error("pending pod was not deleted")
	}
}
