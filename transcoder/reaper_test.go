package main

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain lets this test binary act as the reaper when startReaper
// re-executes it, exactly as the real binary does.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == reaperArg {
		if d := os.Getenv("TEST_SA_DIR"); d != "" {
			saDir = d
		}
		os.Exit(reap(os.Args[2:]))
	}
	os.Exit(m.Run())
}

// A child process starts a reaper and is then SIGKILLed, as a media server
// does when it hard-kills a transcoder. The reaper must still delete the pod.
func TestReaperDeletesPodWhenParentIsKilled(t *testing.T) {
	if os.Getenv("KUBE_ARR_REAPER_CHILD") == "1" {
		startReaper("media", "media-plex-transcode-x1")
		time.Sleep(time.Hour) // wait to be killed
		return
	}
	api := &fakeAPI{}
	setupFakeCluster(t, api)

	child := exec.Command(os.Args[0], "-test.run=^TestReaperDeletesPodWhenParentIsKilled$")
	child.Env = append(os.Environ(), "KUBE_ARR_REAPER_CHILD=1", "TEST_SA_DIR="+saDir)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1 * time.Second)
	if err := child.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	child.Wait()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		api.mu.Lock()
		deleted := append([]string(nil), api.deleted...)
		api.mu.Unlock()
		if len(deleted) > 0 {
			if !strings.HasSuffix(deleted[0], "/media-plex-transcode-x1") {
				t.Fatalf("deleted %s", deleted[0])
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("reaper did not delete the pod after its parent was killed")
}
