package gotenberg_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/gotenberg"
	"go.yaml.in/yaml/v3"
)

func TestConvertUnavailable(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/forms/libreoffice/convert" || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		file, header, err := r.FormFile("files")
		if err != nil {
			t.Error(err)
			return
		}
		defer file.Close()
		body, _ := io.ReadAll(file)
		if header.Filename != "report.docx" || string(body) != "fixture" {
			t.Errorf("unexpected file: %q %q", header.Filename, body)
		}
		if count.Add(1) == 2 {
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("private report content"))
	}))
	defer server.Close()
	client := gotenberg.New(server.URL)
	_, err := client.Convert(context.Background(), []byte("fixture"))
	if err == nil || strings.Contains(err.Error(), "private report content") {
		t.Fatalf("unavailable error leaks response or missing: %v", err)
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	if _, err := gotenberg.New(closed.URL).Convert(context.Background(), []byte("fixture")); err == nil {
		t.Fatal("expected an error for a refused connection")
	}
	var forwarded atomic.Bool
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Store(true) }))
	defer receiver.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, receiver.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	_, _ = gotenberg.New(redirect.URL).Convert(context.Background(), []byte("fixture"))
	if forwarded.Load() {
		t.Fatal("converter redirected the report to another host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = client.Convert(ctx, []byte("fixture"))
	if err == nil {
		t.Fatal("expected an error when the request is cancelled")
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer slow.Close()
	started := time.Now()
	_, err = gotenberg.New(slow.URL).Convert(context.Background(), []byte("fixture"))
	if err == nil || time.Since(started) < 29*time.Second || time.Since(started) > 40*time.Second {
		t.Fatalf("expected 30-second timeout, got %v after %v", err, time.Since(started))
	}
}

func TestPinnedGotenbergCompose(t *testing.T) {
	data, err := os.ReadFile("../../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var compose struct {
		Services map[string]struct {
			Image       string `yaml:"image"`
			Ports       []any  `yaml:"ports"`
			NetworkMode string `yaml:"network_mode"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &compose); err != nil {
		t.Fatal(err)
	}
	svc, ok := compose.Services["gotenberg"]
	if !ok || !regexp.MustCompile(`^gotenberg/gotenberg:[0-9]+\.[0-9]+\.[0-9]+@sha256:[a-f0-9]{64}$`).MatchString(svc.Image) {
		t.Fatalf("un-pinned Gotenberg image: %q", svc.Image)
	}
	if len(svc.Ports) != 0 || svc.NetworkMode == "host" {
		t.Fatalf("Gotenberg is exposed: %+v", svc)
	}
	dev, err := os.ReadFile("../../../compose.dev.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var override struct {
		Services map[string]struct {
			Ports []string `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(dev, &override); err != nil {
		t.Fatal(err)
	}
	ports := override.Services["gotenberg"].Ports
	if len(ports) != 1 || ports[0] != "127.0.0.1:3000:3000" {
		t.Fatalf("development ports must be loopback-only: %v", ports)
	}
}
