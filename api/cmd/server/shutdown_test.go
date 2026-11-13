package main

import (
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/netip"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseTrustedProxies(t *testing.T) {
	got, err := parseTrustedProxies(" 10.0.0.0/8, 192.168.1.10/32 ,fd00::/8")
	want := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.168.1.10/32"), netip.MustParsePrefix("fd00::/8")}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("parseTrustedProxies = %v, %v; want %v", got, err, want)
	}
	if got, err := parseTrustedProxies(""); err != nil || len(got) != 0 {
		t.Fatalf("empty: %v, %v; want none", got, err)
	}
	for _, in := range []string{"10.0.0.1", "10.0.0.0/33", "10.0.0.0/8,", "proxy.local/32", "10.0.0.5/8"} {
		if _, err := parseTrustedProxies(in); err == nil {
			t.Errorf("parseTrustedProxies(%q) = nil error, want error", in)
		}
	}
}

func TestRunRefusesToStartWithInvalidTrustedProxies(t *testing.T) {
	const want = "TRUSTED_PROXIES must be a comma-separated list of CIDRs, e.g. 10.0.0.0/8,192.168.1.10/32"
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	env := map[string]string{
		"DATABASE_URL":         "postgres://nobody@127.0.0.1:1/none",
		"MIGRATE_DATABASE_URL": "postgres://nobody@127.0.0.1:1/none",
		"ACCESS_CODE_KEY":      key,
		"TOTP_ENC_KEY":         key,
		"MAIL_DEV_DIR":         t.TempDir(),
		"APP_BASE_URL":         "https://ce.example",
		"TRUSTED_PROXIES":      "10.0.0.1",
	}
	if err := run(func(k string) string { return env[k] }); err == nil || err.Error() != want {
		t.Fatalf("run() = %v, want %q", err, want)
	}
}

// On shutdown the server finishes in-flight requests, then stops the background loop, then drains
// the mail queue, in that order.
func TestServeShutsDownGracefully(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	inFlight, release := make(chan struct{}), make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(inFlight)
		<-release
		_, _ = io.WriteString(w, "done")
	})}
	var order []string
	var bgStopped atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error)
	go func() {
		served <- serve(ctx, srv, ln,
			func(bg context.Context) { <-bg.Done(); bgStopped.Store(true) },
			func(context.Context) error {
				if !bgStopped.Load() {
					order = append(order, "drain before background stopped")
				}
				order = append(order, "drain")
				return nil
			})
	}()
	body := make(chan string)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String())
		if err != nil {
			body <- err.Error()
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		body <- string(b)
	}()
	<-inFlight
	cancel()
	select {
	case err := <-served:
		t.Fatalf("serve returned %v with a request in flight", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if got := <-body; got != "done" {
		t.Fatalf("in-flight response = %q, want done", got)
	}
	if err := <-served; err != nil {
		t.Fatalf("serve = %v, want nil", err)
	}
	if !reflect.DeepEqual(order, []string{"drain"}) {
		t.Fatalf("shutdown order = %q, want drain after the background loop stopped", order)
	}
}
