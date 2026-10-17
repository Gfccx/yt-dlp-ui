package network

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestHTTPProxyAndDirect(t *testing.T) {
	seen := make(chan *http.Request, 4)
	p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r
		if r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("user:p@ss")) {
			w.WriteHeader(407)
			return
		}
		w.WriteHeader(204)
	}))
	defer p.Close()
	u, _ := url.Parse(p.URL)
	u.User = url.UserPassword("user", "p@ss")
	r := testTarget(context.Background(), Settings{Enabled: true, URL: u.String()}, "http://target.invalid/check")
	if !r.OK || r.Status != 204 || r.Effective != p.URL {
		t.Fatalf("proxy not used: %+v", r)
	}
	if req := <-seen; req.URL.String() != "http://target.invalid/check" {
		t.Fatal(req.URL)
	}
	r = testTarget(context.Background(), Settings{Enabled: true, URL: p.URL}, "http://target.invalid/check")
	if r.Category != "authentication" {
		t.Fatalf("auth not distinguished: %+v", r)
	}
	<-seen

	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("credentials sent to target")
		}
		w.WriteHeader(200)
	}))
	defer direct.Close()
	t.Setenv("HTTP_PROXY", p.URL)
	t.Setenv("HTTPS_PROXY", p.URL)
	t.Setenv("ALL_PROXY", p.URL)
	r = testTarget(context.Background(), Settings{Enabled: false, URL: p.URL}, direct.URL)
	if !r.OK || r.Effective != "Direct" {
		t.Fatalf("direct inherited environment proxy: %+v", r)
	}
	select {
	case <-seen:
		t.Fatal("direct test used proxy")
	default:
	}
}

func TestCONNECTErrorsTLSAndRedirects(t *testing.T) {
	for _, status := range []int{407, 502} {
		p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodConnect {
				t.Error("not CONNECT")
			}
			w.WriteHeader(status)
		}))
		r := Test(context.Background(), Settings{Enabled: true, URL: p.URL})
		p.Close()
		want := "http"
		if status == 407 {
			want = "authentication"
		}
		if r.Category != want {
			t.Fatalf("CONNECT %d: %+v", status, r)
		}
	}
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer tlsServer.Close()
	r := testTarget(context.Background(), Settings{}, tlsServer.URL)
	if r.Category != "tls" {
		t.Fatalf("TLS error: %+v", r)
	}
	r = testTarget(context.Background(), Settings{Enabled: true, URL: tlsServer.URL}, "http://target.invalid")
	if r.Category != "tls" {
		t.Fatalf("proxy TLS error: %+v", r)
	}

	// A trusted HTTPS proxy receives TLS before the absolute HTTP request.
	transport, _ := testTransport(tlsServer.URL)
	defer transport.CloseIdleConnections()
	pool := x509.NewCertPool()
	pool.AddCert(tlsServer.Certificate())
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	client := &http.Client{Transport: transport, Timeout: time.Second}
	resp, err := client.Get("http://target.invalid/check")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}

	redirectHit := false
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/private" {
			redirectHit = true
		}
		http.Redirect(w, req, "/private", 302)
	}))
	defer redirect.Close()
	r = testTarget(context.Background(), Settings{}, redirect.URL)
	if r.Category != "http" || r.Status != 302 || redirectHit {
		t.Fatalf("redirect followed: %+v", r)
	}
}

func TestTimeoutAndClassification(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	r := testTarget(ctx, Settings{}, s.URL)
	if r.Category != "timeout" || r.ElapsedMS > 2000 {
		t.Fatalf("timeout not bounded: %+v", r)
	}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&net.DNSError{Err: "no such host", Name: "proxy.invalid"}, "dns"},
		{context.DeadlineExceeded, "timeout"}, {context.Canceled, "cancelled"},
		{errors.New("username/password authentication failed"), "authentication"},
		{errors.New("connection refused with SECRET"), "connection"},
	} {
		category, message := classify(tc.err)
		if category != tc.want || strings.Contains(message, "SECRET") {
			t.Fatalf("%v: %s %s", tc.err, category, message)
		}
	}
}

// SOCKS mock returns a real HTTP response after validating the wire handshake.
// A .invalid destination proves remote DNS is delegated without a local lookup.
func socksMock(t *testing.T, scheme, addressType string, rejectAuth bool) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		read := func(n int) []byte {
			b := make([]byte, n)
			if _, err := io.ReadFull(conn, b); err != nil {
				t.Error(err)
			}
			return b
		}
		if scheme == "socks4" {
			head := read(8)
			if head[0] != 4 || head[1] != 1 {
				t.Error("bad SOCKS4 request")
			}
			var user []byte
			for {
				b := read(1)[0]
				if b == 0 {
					break
				}
				user = append(user, b)
			}
			if string(user) != "user" {
				t.Error("wrong SOCKS4 user")
			}
			status := byte(90)
			if rejectAuth {
				status = 93
			}
			conn.Write([]byte{0, status, 0, 0, 0, 0, 0, 0})
			if rejectAuth {
				return
			}
		} else {
			head := read(2)
			read(int(head[1]))
			conn.Write([]byte{5, 2})
			auth := read(2)
			user := read(int(auth[1]))
			pass := read(int(read(1)[0]))
			if string(user) != "user" || string(pass) != "p@ss" {
				t.Error("wrong SOCKS5 credentials")
			}
			status := byte(0)
			if rejectAuth {
				status = 1
			}
			conn.Write([]byte{1, status})
			if rejectAuth {
				return
			}
			head = read(4)
			if head[0] != 5 || head[1] != 1 {
				t.Error("bad SOCKS5 request")
			}
			switch head[3] {
			case 1:
				read(4)
				if addressType == "domain" {
					t.Error("expected remote DNS")
				}
			case 4:
				read(16)
				if addressType == "domain" {
					t.Error("expected remote DNS")
				}
			case 3:
				host := read(int(read(1)[0]))
				if string(host) != "target.invalid" || addressType != "domain" {
					t.Error("expected local DNS")
				}
			default:
				t.Error("bad address type")
				return
			}
			read(2)
			conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80})
		}
		buf := make([]byte, 4096)
		conn.Read(buf)
		fmt.Fprint(conn, "HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n")
	}()
	user := "user:p%40ss"
	if scheme == "socks4" {
		user = "user"
	}
	return scheme + "://" + user + "@" + l.Addr().String()
}

func TestSOCKSRoutingAndAuthentication(t *testing.T) {
	for _, scheme := range []string{"socks4", "socks5", "socks5h"} {
		for _, reject := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reject=%v", scheme, reject), func(t *testing.T) {
				target, addressType := "http://localhost:80/check", "ip"
				if scheme == "socks5h" {
					target, addressType = "http://target.invalid:80/check", "domain"
				}
				raw := socksMock(t, scheme, addressType, reject)
				r := testTarget(context.Background(), Settings{Enabled: true, URL: raw}, target)
				if reject && r.Category != "authentication" || !reject && !r.OK {
					t.Fatalf("%+v", r)
				}
			})
		}
	}
	// Local DNS must fail before attempting a connection to the SOCKS server.
	r := testTarget(context.Background(), Settings{Enabled: true, URL: "socks5://127.0.0.1:1"}, "http://target.invalid/check")
	if r.Category != "dns" {
		t.Fatalf("local DNS failure not reported: %+v", r)
	}
}
