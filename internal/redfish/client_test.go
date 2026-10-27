package redfish

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/mattburchett/redfish-explorer/internal/mockbmc"
)

func TestSessionLoginAndGet(t *testing.T) {
	srv := httptest.NewServer(mockbmc.Handler(false, nil))
	defer srv.Close()
	c, err := New(Config{Host: srv.URL, User: mockbmc.User, Pass: mockbmc.Pass})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Login(ctx); err != nil {
		t.Fatal(err)
	}
	if c.Mode() != AuthSession {
		t.Fatalf("mode = %v, want session", c.Mode())
	}
	res, err := c.Get(ctx, "/redfish/v1/Systems/1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Value.StrAt("Name") != "Mock System One" {
		t.Fatalf("unexpected body: %s", res.Raw)
	}
	res, err = c.Get(ctx, "/redfish/v1/Nope")
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 404 || res == nil || res.Value == nil {
		t.Fatalf("want 404 with body, got res=%v err=%v", res, err)
	}
	c.Logout()
}

func TestBasicFallback(t *testing.T) {
	srv := httptest.NewServer(mockbmc.Handler(true, nil))
	defer srv.Close()
	c, _ := New(Config{Host: srv.URL, User: mockbmc.User, Pass: mockbmc.Pass})
	if err := c.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.Mode() != AuthBasic {
		t.Fatalf("mode = %v, want basic", c.Mode())
	}
}

func TestBadPassword(t *testing.T) {
	srv := httptest.NewServer(mockbmc.Handler(false, nil))
	defer srv.Close()
	c, _ := New(Config{Host: srv.URL, User: mockbmc.User, Pass: "wrong"})
	if err := c.Login(context.Background()); err == nil {
		t.Fatal("expected login failure")
	}
	c, _ = New(Config{Host: srv.URL, User: mockbmc.User, Pass: "wrong", ForceBasic: true})
	if err := c.Login(context.Background()); err == nil {
		t.Fatal("expected basic login failure")
	}
}

func TestNormalizeHost(t *testing.T) {
	for in, want := range map[string]string{
		"bmc":                   "https://bmc",
		"bmc:8443":              "https://bmc:8443",
		"http://bmc/redfish/v1": "http://bmc",
		"https://10.1.1.1/":     "https://10.1.1.1",
	} {
		u, err := NormalizeHost(in)
		if err != nil || u.String() != want {
			t.Errorf("NormalizeHost(%q) = %v, %v; want %s", in, u, err, want)
		}
	}
}

// TestLive runs against a real BMC when REDFISH_TEST_HOST is set.
func TestLive(t *testing.T) {
	host := os.Getenv("REDFISH_TEST_HOST")
	if host == "" {
		t.Skip("REDFISH_TEST_HOST not set")
	}
	c, err := New(Config{Host: host, User: os.Getenv("REDFISH_TEST_USER"), Pass: os.Getenv("REDFISH_TEST_PASS"), Timeout: 60 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := c.Login(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("auth mode: %s", c.Mode())
	for _, p := range []string{"/redfish/v1", "/redfish/v1/Systems", "/redfish/v1/Managers"} {
		res, err := c.Get(ctx, p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		t.Logf("GET %s -> %d in %s, %d bytes, Name=%q", p, res.Status, res.Elapsed.Round(time.Millisecond), len(res.Raw), res.Value.StrAt("Name"))
	}
	c.Logout()
}
