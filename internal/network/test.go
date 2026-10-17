package network

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

const TestTarget = "https://www.youtube.com/robots.txt"
const TestTimeout = 10 * time.Second

type TestResult struct {
	OK        bool   `json:"ok"`
	Category  string `json:"category"`
	Message   string `json:"message"`
	ElapsedMS int64  `json:"elapsed_ms"`
	Status    int    `json:"status,omitempty"`
	Target    string `json:"target"`
	Effective string `json:"effective"`
	Engine    string `json:"engine"`
}

type testError struct{ category, message string }

func (e *testError) Error() string { return e.message }

func classify(err error) (string, string) {
	var known *testError
	var dns *net.DNSError
	var ne net.Error
	var cert *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	var unknown x509.UnknownAuthorityError
	message := strings.ToLower(err.Error())
	switch {
	case errors.As(err, &known):
		return known.category, known.message
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "timeout", "Connection timed out (10 second limit)."
	case errors.As(err, &dns):
		return "dns", "DNS resolution failed. Check the proxy hostname and DNS configuration."
	case errors.As(err, &cert), errors.As(err, &record), errors.As(err, &unknown), strings.Contains(message, "tls:"):
		return "tls", "TLS negotiation or certificate verification failed."
	case strings.Contains(message, "authentication"), strings.Contains(message, "username/password"), strings.Contains(message, "no acceptable authentication"):
		return "authentication", "Proxy authentication failed. Check the username and password."
	case errors.Is(err, context.Canceled):
		return "cancelled", "Connection test was cancelled."
	default:
		return "connection", "Could not connect through the selected route. Check the address, port and proxy availability."
	}
}

// Test checks a fixed target only. This is a Go HTTP test, not a site extraction
// test; no user-supplied destination or redirect is followed.
func Test(ctx context.Context, cfg Settings) TestResult {
	return testTarget(ctx, cfg, TestTarget)
}

func testTarget(ctx context.Context, cfg Settings, target string) TestResult {
	started := time.Now()
	raw := ""
	if cfg.Enabled {
		raw = cfg.URL
	}
	r := TestResult{Target: target, Effective: Label(raw), Engine: "go"}
	ctx, cancel := context.WithTimeout(ctx, TestTimeout)
	defer cancel()
	transport, err := testTransport(raw)
	if err == nil {
		defer transport.CloseIdleConnections()
		client := &http.Client{
			Transport:     transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		}
		var req *http.Request
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err == nil {
			var resp *http.Response
			resp, err = client.Do(req)
			if err == nil {
				resp.Body.Close()
				r.Status = resp.StatusCode
				switch {
				case resp.StatusCode == http.StatusProxyAuthRequired:
					err = &testError{"authentication", "Proxy authentication failed (HTTP 407)."}
				case resp.StatusCode < 200 || resp.StatusCode >= 300:
					err = &testError{"http", "Test target returned HTTP " + strconv.Itoa(resp.StatusCode) + ". Redirects are not followed."}
				default:
					r.OK, r.Category, r.Message = true, "success", "Test target reached successfully."
				}
			}
		}
	}
	if err != nil {
		r.Category, r.Message = classify(err)
	}
	r.ElapsedMS = time.Since(started).Milliseconds()
	return r
}

func testTransport(raw string) (*http.Transport, error) {
	dialer := &net.Dialer{Timeout: TestTimeout}
	// A new Transport deliberately ignores HTTP_PROXY/HTTPS_PROXY/ALL_PROXY.
	t := &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: TestTimeout, ResponseHeaderTimeout: TestTimeout}
	if raw == "" {
		return t, nil
	}
	u, err := ParseURL(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		t.Proxy = http.ProxyURL(u)
		t.OnProxyConnectResponse = func(_ context.Context, _ *url.URL, _ *http.Request, res *http.Response) error {
			if res.StatusCode == http.StatusProxyAuthRequired {
				return &testError{"authentication", "Proxy authentication failed (HTTP 407)."}
			}
			if res.StatusCode != http.StatusOK {
				return &testError{"http", "Proxy CONNECT returned HTTP " + strconv.Itoa(res.StatusCode) + "."}
			}
			return nil
		}
		return t, nil
	}
	if u.Scheme == "socks4" {
		t.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) {
			return dialSOCKS4(ctx, dialer, u, address)
		}
		return t, nil
	}
	var auth *proxy.Auth
	if u.User != nil {
		password, _ := u.User.Password()
		auth = &proxy.Auth{User: u.User.Username(), Password: password}
	}
	socks, err := proxy.SOCKS5("tcp", u.Host, auth, dialer)
	if err != nil {
		return nil, err
	}
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if u.Scheme == "socks5" {
			address, err := resolveAddress(ctx, address, false)
			if err != nil {
				return nil, err
			}
			return socks.(proxy.ContextDialer).DialContext(ctx, network, address)
		}
		return socks.(proxy.ContextDialer).DialContext(ctx, network, address)
	}
	return t, nil
}

func resolveAddress(ctx context.Context, address string, ipv4 bool) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", err
	}
	family := "ip"
	if ipv4 {
		family = "ip4"
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, family, host)
	if err != nil {
		return "", err
	}
	if len(ips) == 0 {
		return "", &net.DNSError{Err: "no usable address", Name: host}
	}
	return net.JoinHostPort(ips[0].String(), port), nil
}

func dialSOCKS4(ctx context.Context, dialer *net.Dialer, u *url.URL, address string) (net.Conn, error) {
	address, err := resolveAddress(ctx, address, true)
	if err != nil {
		return nil, err
	}
	host, port, _ := net.SplitHostPort(address)
	ip := net.ParseIP(host).To4()
	if ip == nil {
		return nil, &testError{"dns", "SOCKS4 requires an IPv4 target address."}
	}
	conn, err := dialer.DialContext(ctx, "tcp", u.Host)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	request := []byte{4, 1, 0, 0}
	n, _ := strconv.Atoi(port)
	binary.BigEndian.PutUint16(request[2:4], uint16(n))
	request = append(request, ip...)
	if u.User != nil {
		request = append(request, u.User.Username()...)
	}
	request = append(request, 0)
	if _, err = conn.Write(request); err == nil {
		reply := make([]byte, 8)
		_, err = io.ReadFull(conn, reply)
		if err == nil && reply[1] != 90 {
			if reply[1] == 92 || reply[1] == 93 {
				err = &testError{"authentication", "SOCKS4 user ID was rejected."}
			} else {
				err = &testError{"connection", "SOCKS4 proxy rejected the connection."}
			}
		}
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	return conn, nil
}
