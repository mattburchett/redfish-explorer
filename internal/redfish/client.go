// Package redfish is a small HTTP client for walking a Redfish service.
package redfish

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mattburchett/redfish-explorer/internal/ojson"
)

// AuthMode describes how requests are authenticated.
type AuthMode int

const (
	AuthNone AuthMode = iota
	AuthSession
	AuthBasic
)

func (m AuthMode) String() string {
	switch m {
	case AuthSession:
		return "session"
	case AuthBasic:
		return "basic"
	}
	return "anonymous"
}

// Config holds connection parameters.
type Config struct {
	Host       string
	User       string
	Pass       string
	VerifyTLS  bool
	ForceBasic bool
	Timeout    time.Duration
}

// Client talks to one BMC.
type Client struct {
	cfg        Config
	base       *url.URL
	hc         *http.Client
	token      string
	sessionURI string
	mode       AuthMode
}

// Resource is the result of a GET.
type Resource struct {
	Path      string
	Status    int
	Headers   http.Header
	Raw       []byte
	Value     *ojson.Value
	Elapsed   time.Duration
	FetchedAt time.Time
}

// HTTPError is returned for non-2xx responses; the Resource (if any) carries the body.
type HTTPError struct {
	Status  int
	Path    string
	Message string
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("HTTP %d %s: %s", e.Status, http.StatusText(e.Status), e.Message)
	}
	return fmt.Sprintf("HTTP %d %s", e.Status, http.StatusText(e.Status))
}

const maxBody = 64 << 20

// NormalizeHost accepts "bmc", "bmc:8443", "https://bmc/" and returns a base URL.
func NormalizeHost(h string) (*url.URL, error) {
	h = strings.TrimSpace(h)
	if h == "" {
		return nil, errors.New("host is empty")
	}
	if !strings.Contains(h, "://") {
		h = "https://" + h
	}
	u, err := url.Parse(h)
	if err != nil {
		return nil, err
	}
	if u.Host == "" {
		return nil, fmt.Errorf("could not parse host from %q", h)
	}
	u.Path, u.RawPath, u.RawQuery, u.Fragment = "", "", "", ""
	return u, nil
}

// New builds a client; it does not contact the BMC.
func New(cfg Config) (*Client, error) {
	base, err := NormalizeHost(cfg.Host)
	if err != nil {
		return nil, err
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: !cfg.VerifyTLS}, //nolint:gosec // BMCs ship self-signed certs; user opts in via --verify-tls
		MaxIdleConns:        4,
		IdleConnTimeout:     60 * time.Second,
	}
	return &Client{
		cfg:  cfg,
		base: base,
		hc: &http.Client{
			Transport: tr,
			Timeout:   cfg.Timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("too many redirects")
				}
				return nil
			},
		},
	}, nil
}

// Host returns the normalized host (scheme://host[:port]).
func (c *Client) Host() string { return c.base.String() }

// Mode reports the active authentication mode.
func (c *Client) Mode() AuthMode { return c.mode }

// User returns the configured username.
func (c *Client) User() string { return c.cfg.User }

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader, auth bool) (*http.Request, error) {
	u := *c.base
	if i := strings.IndexByte(path, '?'); i >= 0 {
		u.RawQuery = path[i+1:]
		path = path[:i]
	}
	u.Path = path
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OData-Version", "4.0")
	req.Header.Set("User-Agent", "redfish-explorer/1.0")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		switch {
		case c.token != "":
			req.Header.Set("X-Auth-Token", c.token)
		case c.cfg.User != "":
			req.SetBasicAuth(c.cfg.User, c.cfg.Pass)
		}
	}
	return req, nil
}

// Login authenticates: session auth first (falls back to basic when the
// service lacks a working SessionService), or basic only when forced.
func (c *Client) Login(ctx context.Context) error {
	if c.cfg.User == "" {
		c.mode = AuthNone
		_, err := c.Get(ctx, "/redfish/v1/")
		return err
	}
	sessionsPath := "/redfish/v1/SessionService/Sessions"
	if root, err := c.getNoAuth(ctx, "/redfish/v1/"); err == nil && root.Value != nil {
		if p := root.Value.StrAt("Links.Sessions.@odata.id"); p != "" {
			sessionsPath = p
		}
	} else if err != nil {
		var he *HTTPError
		if !errors.As(err, &he) {
			return fmt.Errorf("reach %s: %w", c.base.Host, err)
		}
	}

	if !c.cfg.ForceBasic {
		err := c.sessionLogin(ctx, sessionsPath)
		if err == nil {
			c.mode = AuthSession
			return nil
		}
		var he *HTTPError
		if errors.As(err, &he) && (he.Status == http.StatusUnauthorized || he.Status == http.StatusForbidden) {
			return fmt.Errorf("login rejected: %w", err)
		}
		// Anything else (404/405/500, odd payloads): try basic.
	}
	// Basic: the sessions collection always requires auth, so it is a good probe.
	if _, err := c.Get(ctx, sessionsPath); err != nil {
		var he *HTTPError
		if errors.As(err, &he) {
			if he.Status == http.StatusUnauthorized || he.Status == http.StatusForbidden {
				return fmt.Errorf("basic auth rejected: %w", err)
			}
			// Probe path missing; fall back to a protected-ish resource.
			if _, err2 := c.Get(ctx, "/redfish/v1/Systems"); err2 != nil {
				return fmt.Errorf("basic auth probe failed: %w", err2)
			}
		} else {
			return err
		}
	}
	c.mode = AuthBasic
	return nil
}

func (c *Client) sessionLogin(ctx context.Context, sessionsPath string) error {
	payload, _ := json.Marshal(map[string]string{"UserName": c.cfg.User, "Password": c.cfg.Pass})
	req, err := c.newRequest(ctx, http.MethodPost, sessionsPath, bytes.NewReader(payload), false)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{Status: resp.StatusCode, Path: sessionsPath, Message: extractMessage(body)}
	}
	tok := resp.Header.Get("X-Auth-Token")
	if tok == "" {
		return errors.New("session created but no X-Auth-Token returned")
	}
	c.token = tok
	if loc := resp.Header.Get("Location"); loc != "" {
		if lu, err := url.Parse(loc); err == nil {
			c.sessionURI = lu.Path
		}
	}
	if c.sessionURI == "" {
		if v, err := ojson.Parse(body); err == nil {
			c.sessionURI = v.StrAt("@odata.id")
		}
	}
	return nil
}

func (c *Client) getNoAuth(ctx context.Context, path string) (*Resource, error) {
	return c.get(ctx, path, false)
}

// Get fetches a resource path (must start with "/").
func (c *Client) Get(ctx context.Context, path string) (*Resource, error) {
	return c.get(ctx, path, true)
}

func (c *Client) get(ctx context.Context, path string, auth bool) (*Resource, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, err := c.newRequest(ctx, http.MethodGet, path, nil, auth)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, friendlyNetErr(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, err
	}
	res := &Resource{
		Path:      path,
		Status:    resp.StatusCode,
		Headers:   resp.Header,
		Raw:       raw,
		Elapsed:   time.Since(start),
		FetchedAt: time.Now(),
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		if v, perr := ojson.Parse(raw); perr == nil {
			res.Value = v
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return res, &HTTPError{Status: resp.StatusCode, Path: path, Message: extractMessage(raw)}
	}
	if res.Value == nil {
		return res, fmt.Errorf("response is not JSON (%s)", resp.Header.Get("Content-Type"))
	}
	return res, nil
}

// Logout deletes the session, if one was created.
func (c *Client) Logout() {
	if c.token == "" || c.sessionURI == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := c.newRequest(ctx, http.MethodDelete, c.sessionURI, nil, true)
	if err != nil {
		return
	}
	if resp, err := c.hc.Do(req); err == nil {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
	}
	c.token = ""
}

// extractMessage pulls a human-readable message out of a Redfish error body.
func extractMessage(body []byte) string {
	v, err := ojson.Parse(body)
	if err != nil {
		s := strings.TrimSpace(string(body))
		if len(s) > 120 {
			s = s[:120] + "…"
		}
		return s
	}
	e := v.Get("error")
	if e == nil {
		return ""
	}
	if info := e.Get("@Message.ExtendedInfo"); info != nil && info.Kind == ojson.Array && len(info.Arr) > 0 {
		if m := info.Arr[0].StrAt("Message"); m != "" {
			return m
		}
	}
	return e.StrAt("message")
}

func friendlyNetErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var cve *tls.CertificateVerificationError
	var uae x509.UnknownAuthorityError
	var hne x509.HostnameError
	if errors.As(err, &cve) || errors.As(err, &uae) || errors.As(err, &hne) {
		return fmt.Errorf("TLS verification failed (%v); rerun without --verify-tls", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("timed out waiting for the BMC")
	}
	return err
}

// Patch sends a JSON merge-patch to path. etag (optional) is sent as If-Match.
func (c *Client) Patch(ctx context.Context, path string, body []byte, etag string) (*Resource, error) {
	return c.write(ctx, http.MethodPatch, path, body, etag)
}

// Post sends a JSON body to path (Redfish actions).
func (c *Client) Post(ctx context.Context, path string, body []byte) (*Resource, error) {
	return c.write(ctx, http.MethodPost, path, body, "")
}

func (c *Client) write(ctx context.Context, method, path string, body []byte, etag string) (*Resource, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, err := c.newRequest(ctx, method, path, bytes.NewReader(body), true)
	if err != nil {
		return nil, err
	}
	if etag != "" {
		req.Header.Set("If-Match", etag)
	}
	start := time.Now()
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, friendlyNetErr(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, err
	}
	res := &Resource{Path: path, Status: resp.StatusCode, Headers: resp.Header, Raw: raw, Elapsed: time.Since(start), FetchedAt: time.Now()}
	if len(bytes.TrimSpace(raw)) > 0 {
		if v, perr := ojson.Parse(raw); perr == nil {
			res.Value = v
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return res, &HTTPError{Status: resp.StatusCode, Path: path, Message: extractMessage(raw)}
	}
	return res, nil
}

// Messages collects human-readable Redfish messages from a response body,
// whether it is an error envelope or a success payload with ExtendedInfo.
func Messages(v *ojson.Value) []string {
	if v == nil {
		return nil
	}
	var out []string
	add := func(info *ojson.Value) {
		if info == nil || info.Kind != ojson.Array {
			return
		}
		for _, m := range info.Arr {
			msg := m.StrAt("Message")
			if msg == "" {
				msg = m.StrAt("MessageId")
			}
			if sev := m.StrAt("Severity"); sev != "" && sev != "OK" {
				msg = sev + ": " + msg
			}
			if res := m.StrAt("Resolution"); res != "" && res != "None" && res != "No response action is required." {
				msg += " — " + res
			}
			if msg != "" {
				out = append(out, msg)
			}
		}
	}
	add(v.Get("@Message.ExtendedInfo"))
	if e := v.Get("error"); e != nil {
		add(e.Get("@Message.ExtendedInfo"))
		if len(out) == 0 {
			if m := e.StrAt("message"); m != "" {
				out = append(out, m)
			}
		}
	}
	return out
}
