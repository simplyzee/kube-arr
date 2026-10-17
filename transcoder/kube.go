package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// saDir holds the pod's service account token and CA (a var for tests).
var saDir = "/var/run/secrets/kubernetes.io/serviceaccount"

// kubeClient is a deliberately tiny in-cluster Kubernetes client: create,
// get and delete pods, and follow their logs. No dependencies.
type kubeClient struct {
	base      string
	tokenPath string
	http      *http.Client
	stream    *http.Client
}

func newKubeClient() (*kubeClient, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	base := "https://kubernetes.default.svc"
	if host != "" && port != "" {
		base = "https://" + net.JoinHostPort(host, port)
	}
	ca, err := os.ReadFile(saDir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("read service account CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("invalid service account CA")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	return &kubeClient{
		base:      base,
		tokenPath: saDir + "/token",
		http:      &http.Client{Transport: transport, Timeout: 30 * time.Second},
		stream:    &http.Client{Transport: transport}, // no timeout: log streams last as long as the transcode
	}, nil
}

type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string { return fmt.Sprintf("kubernetes API %d: %s", e.Status, e.Message) }

func isNotFound(err error) bool {
	ae, ok := err.(*apiError)
	return ok && ae.Status == http.StatusNotFound
}

func (k *kubeClient) do(ctx context.Context, c *http.Client, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, k.base+path, r)
	if err != nil {
		return nil, err
	}
	// Re-read the token every time: projected tokens rotate.
	token, err := os.ReadFile(k.tokenPath)
	if err != nil {
		return nil, fmt.Errorf("read service account token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var status struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(msg, &status) == nil && status.Message != "" {
			return nil, &apiError{resp.StatusCode, status.Message}
		}
		return nil, &apiError{resp.StatusCode, strings.TrimSpace(string(msg))}
	}
	return resp, nil
}

func podsPath(ns string) string { return "/api/v1/namespaces/" + url.PathEscape(ns) + "/pods" }

// Pod is the subset of a Pod's status the dispatcher needs.
type Pod struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Status struct {
		Phase             string `json:"phase"`
		Reason            string `json:"reason"`
		Message           string `json:"message"`
		ContainerStatuses []struct {
			State struct {
				Waiting *struct {
					Reason  string `json:"reason"`
					Message string `json:"message"`
				} `json:"waiting"`
				Terminated *struct {
					ExitCode int    `json:"exitCode"`
					Reason   string `json:"reason"`
				} `json:"terminated"`
			} `json:"state"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

func (k *kubeClient) createPod(ctx context.Context, ns string, pod map[string]any) (*Pod, error) {
	resp, err := k.do(ctx, k.http, http.MethodPost, podsPath(ns), pod)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var p Pod
	return &p, json.NewDecoder(resp.Body).Decode(&p)
}

func (k *kubeClient) getPod(ctx context.Context, ns, name string) (*Pod, error) {
	resp, err := k.do(ctx, k.http, http.MethodGet, podsPath(ns)+"/"+url.PathEscape(name), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var p Pod
	return &p, json.NewDecoder(resp.Body).Decode(&p)
}

func (k *kubeClient) deletePod(ctx context.Context, ns, name string, graceSeconds int) error {
	body := map[string]any{"gracePeriodSeconds": graceSeconds, "propagationPolicy": "Background"}
	resp, err := k.do(ctx, k.http, http.MethodDelete, podsPath(ns)+"/"+url.PathEscape(name), body)
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return err
	}
	resp.Body.Close()
	return nil
}

// followLogs streams the container's log to w until the container exits.
func (k *kubeClient) followLogs(ctx context.Context, ns, name, container string, w io.Writer) error {
	q := url.Values{"follow": {"true"}, "container": {container}}
	resp, err := k.do(ctx, k.stream, http.MethodGet, podsPath(ns)+"/"+url.PathEscape(name)+"/log?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(w, resp.Body)
	return err
}
