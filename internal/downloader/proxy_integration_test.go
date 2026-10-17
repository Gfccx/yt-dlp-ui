//go:build integration

package downloader

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestProxyLocalYtDlp runs the actual extractor and downloader through local
// authenticated proxies. No public website or upstream proxy is required.
func TestProxyLocalYtDlp(t *testing.T) {
	bin := os.Getenv("YT_DLP_PATH")
	if bin == "" {
		bin = "yt-dlp"
	}
	resolved, err := exec.LookPath(bin)
	if err != nil {
		t.Skip("install yt-dlp or set YT_DLP_PATH to run local proxy integration tests")
	}
	for _, scheme := range []string{"direct", "http", "https", "socks4", "socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			dir := t.TempDir()
			user, password := "test-user", "p+@ss:/"
			media := []byte("local proxy integration payload")
			var requests atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if scheme == "http" || scheme == "https" {
					if r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+password)) {
						w.WriteHeader(407)
						return
					}
				}
				if r.URL.Path == "/playlist" {
					w.Header().Set("Content-Type", "text/html")
					fmt.Fprint(w, `<html><title>Local playlist</title><video src="/one.mp4"></video><video src="/two.mp4"></video></html>`)
					return
				}
				w.Header().Set("Content-Type", "video/mp4")
				w.Header().Set("Content-Length", fmt.Sprint(len(media)))
				w.Write(media)
			})
			var proxyURL string
			target := "http://127.0.0.1:1"
			binary := resolved
			if scheme == "direct" || scheme == "http" || scheme == "https" {
				var server *httptest.Server
				if scheme == "https" {
					server = httptest.NewTLSServer(handler)
				} else {
					server = httptest.NewServer(handler)
				}
				defer server.Close()
				if scheme == "direct" {
					target = server.URL
				} else {
					u, _ := url.Parse(server.URL)
					u.User = url.UserPassword(user, password)
					proxyURL = u.String()
				}
				if scheme == "https" {
					ca := filepath.Join(dir, "ca.pem")
					if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
						t.Fatal(err)
					}
					t.Setenv("SSL_CERT_FILE", ca)
					// Use system roots plus this fixture CA, rather than certifi.
					t.Setenv("PROXY_REAL_YTDLP", resolved)
					binary = filepath.Join(dir, "yt-dlp")
					if err := os.WriteFile(binary, []byte("#!/bin/sh\nexec \"$PROXY_REAL_YTDLP\" --compat-options no-certifi \"$@\"\n"), 0700); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				proxyURL = localSOCKSProxy(t, scheme, user, password, handler)
				if scheme == "socks5h" {
					target = "http://destination.invalid:80"
				}
			}
			y := NewYtDlp(binary, "")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			videoURL := target + "/video.mp4"
			info, err := y.Resolve(ctx, videoURL, "video", proxyURL)
			if err != nil {
				t.Fatal(err)
			}
			if info.Video == nil || len(info.Video.Formats) == 0 {
				t.Fatal("no video formats")
			}
			ch := make(chan Progress, 100)
			result, err := y.Download(ctx, videoURL, "best", dir, ch, proxyURL)
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(result.Path)
			if err != nil || string(got) != string(media) {
				t.Fatalf("wrong downloaded bytes: %v", err)
			}
			playlist, err := y.Resolve(ctx, target+"/playlist", "playlist", proxyURL)
			if err != nil {
				t.Fatal(err)
			}
			if playlist.Playlist == nil || len(playlist.Playlist.Entries) != 2 {
				t.Fatal("playlist path failed")
			}
			if requests.Load() < 3 {
				t.Fatal("yt-dlp bypassed proxy")
			}
		})
	}
}

func localSOCKSProxy(t *testing.T, scheme, user, password string, handler http.Handler) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	t.Cleanup(func() { l.Close(); wg.Wait() })
	go func() {
		defer wg.Done()
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				read := func(n int) []byte {
					b := make([]byte, n)
					if _, err := io.ReadFull(conn, b); err != nil {
						return nil
					}
					return b
				}
				if scheme == "socks4" {
					if len(read(8)) != 8 {
						return
					}
					var got []byte
					for {
						b := read(1)
						if len(b) == 0 {
							return
						}
						if b[0] == 0 {
							break
						}
						got = append(got, b[0])
					}
					if string(got) != user {
						return
					}
					conn.Write([]byte{0, 90, 0, 0, 0, 0, 0, 0})
				} else {
					head := read(2)
					if len(head) != 2 {
						return
					}
					read(int(head[1]))
					conn.Write([]byte{5, 2})
					auth := read(2)
					if len(auth) != 2 {
						return
					}
					gotUser := read(int(auth[1]))
					size := read(1)
					if len(size) == 0 {
						return
					}
					gotPassword := read(int(size[0]))
					if string(gotUser) != user || string(gotPassword) != password {
						return
					}
					conn.Write([]byte{1, 0})
					head = read(4)
					if len(head) != 4 {
						return
					}
					switch head[3] {
					case 1:
						read(4)
					case 4:
						read(16)
					case 3:
						size := read(1)
						if len(size) == 0 {
							return
						}
						read(int(size[0]))
					default:
						return
					}
					read(2)
					conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80})
				}
				req, err := http.ReadRequest(bufio.NewReader(conn))
				if err != nil {
					return
				}
				defer req.Body.Close()
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				res := w.Result()
				defer res.Body.Close()
				res.Close = true
				res.Write(conn)
			}()
		}
	}()
	u, _ := url.Parse(scheme + "://" + l.Addr().String())
	if strings.HasPrefix(scheme, "socks5") {
		u.User = url.UserPassword(user, password)
	} else {
		u.User = url.User(user)
	}
	return u.String()
}
