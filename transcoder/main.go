// kube-arr-transcoder stands in for a media server's transcoder (Plex
// Transcoder, or ffmpeg for Jellyfin and Emby). Real transcodes run as
// Kubernetes pods; quick capability checks run locally.
//
// Inspired by munnerz/kube-plex, extended to Jellyfin and Emby.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

const reaperArg = "--kube-arr-reaper"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "install":
			os.Exit(install(os.Args[2:]))
		case reaperArg:
			os.Exit(reap(os.Args[2:]))
		}
	}
	os.Exit(run(os.Args[1:]))
}

func logf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "[kube-arr-transcoder] "+format+"\n", a...)
}

// install copies this binary to dest. The chart's init container uses it
// to hand the dispatcher to the media server through a shared emptyDir.
func install(args []string) int {
	if len(args) != 1 {
		logf("usage: kube-arr-transcoder install <dest>")
		return 2
	}
	self, err := os.Executable()
	if err != nil {
		logf("install: %v", err)
		return 1
	}
	in, err := os.Open(self)
	if err != nil {
		logf("install: %v", err)
		return 1
	}
	defer in.Close()
	out, err := os.OpenFile(args[0], os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		logf("install: %v", err)
		return 1
	}
	if _, err := io.Copy(out, in); err != nil {
		logf("install: %v", err)
		return 1
	}
	if err := out.Close(); err != nil {
		logf("install: %v", err)
		return 1
	}
	logf("installed to %s", args[0])
	return 0
}

// runLocal replaces this process with the real transcoder in the media server.
func runLocal(cfg *Config, args []string) int {
	real := cfg.Binary + ".real"
	argv := append([]string{cfg.Binary}, args...)
	err := syscall.Exec(real, argv, os.Environ())
	logf("cannot run %s locally: %v", real, err)
	return 1
}

func run(args []string) int {
	cfgPath := os.Getenv("KUBE_ARR_TRANSCODER_CONFIG")
	if cfgPath == "" {
		cfgPath = defaultConfigPath
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		// Without config there is no binary path to fall back to.
		logf("config: %v", err)
		return 1
	}
	if !shouldDispatch(args) {
		return runLocal(cfg, args)
	}
	code, err := dispatch(cfg, args)
	if err == nil {
		return code
	}
	if cfg.FallbackToLocal && errors.Is(err, errNotStarted) {
		logf("%v; transcoding locally instead", err)
		return runLocal(cfg, args)
	}
	logf("%v", err)
	return 1
}

var errNotStarted = errors.New("transcode pod did not start")

func dispatch(cfg *Config, args []string) (int, error) {
	infoDir := os.Getenv("KUBE_ARR_PODINFO")
	if infoDir == "" {
		infoDir = defaultPodInfoPath
	}
	info, err := loadPodInfo(infoDir)
	if err != nil {
		return 0, fmt.Errorf("%w: pod info: %v", errNotStarted, err)
	}
	kube, err := newKubeClient()
	if err != nil {
		return 0, fmt.Errorf("%w: %v", errNotStarted, err)
	}
	workDir, _ := os.Getwd()
	pod, err := buildPod(cfg, info, args, forwardEnv(os.Environ()), workDir)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", errNotStarted, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	created, err := kube.createPod(ctx, info.Namespace, pod)
	if err != nil {
		return 0, fmt.Errorf("%w: create pod: %v", errNotStarted, err)
	}
	name := created.Metadata.Name
	logf("dispatched transcode to pod %s/%s", info.Namespace, name)

	// Whatever happens to this process from here, even SIGKILL, the reaper
	// deletes the pod once we're gone.
	if os.Getenv("KUBE_ARR_NO_REAPER") == "" {
		stopReaper := startReaper(info.Namespace, name)
		defer stopReaper()
	}

	cleanup := func(grace int) {
		dctx, dcancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer dcancel()
		if err := kube.deletePod(dctx, info.Namespace, name, grace); err != nil {
			logf("delete pod %s: %v", name, err)
		}
	}

	// Stop requests: signals, or ffmpeg's "q" on stdin (Jellyfin and Emby
	// send it to end a transcode cleanly).
	stop := make(chan string, 2)
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() { stop <- (<-sigs).String() }()
	go func() {
		r := bufio.NewReader(os.Stdin)
		for {
			b, err := r.ReadByte()
			if err != nil {
				return
			}
			if b == 'q' {
				stop <- "q on stdin"
				return
			}
		}
	}()

	result := make(chan struct {
		code int
		err  error
	}, 1)
	go func() {
		code, err := waitAndStream(ctx, kube, cfg, info.Namespace, name)
		result <- struct {
			code int
			err  error
		}{code, err}
	}()

	select {
	case why := <-stop:
		logf("stopping transcode (%s)", why)
		cancel()
		cleanup(5)
		if why == "q on stdin" {
			return 0, nil // ffmpeg exits 0 when asked to quit
		}
		return 255, nil
	case r := <-result:
		cleanup(0)
		return r.code, r.err
	}
}

// waitAndStream waits for the pod to start, streams its log to stderr (the
// media servers parse ffmpeg progress from it), and returns its exit code.
func waitAndStream(ctx context.Context, kube *kubeClient, cfg *Config, ns, name string) (int, error) {
	deadline := time.Now().Add(time.Duration(cfg.StartTimeoutSeconds) * time.Second)
	streaming := false
	logsDone := make(chan struct{})
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		pod, err := kube.getPod(ctx, ns, name)
		if err != nil {
			return 0, fmt.Errorf("get pod %s: %w", name, err)
		}
		phase := pod.Status.Phase
		if !streaming && (phase == "Running" || phase == "Succeeded" || phase == "Failed") {
			streaming = true
			go func() {
				defer close(logsDone)
				if err := kube.followLogs(ctx, ns, name, transcodeContainer, os.Stderr); err != nil && ctx.Err() == nil {
					logf("log stream: %v", err)
				}
			}()
		}
		switch phase {
		case "Succeeded", "Failed":
			select {
			case <-logsDone:
			case <-time.After(10 * time.Second):
			}
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.State.Terminated != nil {
					return cs.State.Terminated.ExitCode, nil
				}
			}
			if phase == "Succeeded" {
				return 0, nil
			}
			return 1, fmt.Errorf("pod %s failed: %s %s", name, pod.Status.Reason, pod.Status.Message)
		case "Pending", "":
			for _, cs := range pod.Status.ContainerStatuses {
				if w := cs.State.Waiting; w != nil && (w.Reason == "ErrImagePull" || w.Reason == "ImagePullBackOff" || w.Reason == "CreateContainerConfigError" || w.Reason == "InvalidImageName") {
					_ = kube.deletePod(context.Background(), ns, name, 0)
					return 0, fmt.Errorf("%w: %s: %s", errNotStarted, w.Reason, w.Message)
				}
			}
			if time.Now().After(deadline) {
				_ = kube.deletePod(context.Background(), ns, name, 0)
				return 0, fmt.Errorf("%w: still pending after %ds", errNotStarted, cfg.StartTimeoutSeconds)
			}
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-tick.C:
		}
	}
}

// startReaper launches a detached copy of this binary that deletes the pod
// as soon as this process exits, however it exits. It waits on a pipe:
// when this process dies, the kernel closes our end and the reaper wakes.
func startReaper(ns, name string) func() {
	r, w, err := os.Pipe()
	if err != nil {
		logf("reaper: %v (pod %s may outlive a killed transcode)", err, name)
		return func() {}
	}
	self, err := os.Executable()
	if err != nil {
		logf("reaper: %v", err)
		r.Close()
		w.Close()
		return func() {}
	}
	cmd := exec.Command(self, reaperArg, ns, name)
	cmd.ExtraFiles = []*os.File{r}
	cmd.Stdout, cmd.Stderr = nil, nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		logf("reaper: %v", err)
		r.Close()
		w.Close()
		return func() {}
	}
	r.Close()
	go cmd.Wait() // reap the child if we outlive it
	return func() { w.Close() }
}

// reap blocks until its parent's pipe closes, then deletes the pod.
func reap(args []string) int {
	if len(args) != 2 {
		return 2
	}
	ns, name := args[0], args[1]
	signal.Ignore(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	pipe := os.NewFile(3, "parent")
	_, _ = io.Copy(io.Discard, pipe)
	kube, err := newKubeClient()
	if err != nil {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for attempt := 0; attempt < 5; attempt++ {
		if err := kube.deletePod(ctx, ns, name, 0); err == nil {
			return 0
		}
		time.Sleep(2 * time.Second)
	}
	return 1
}
