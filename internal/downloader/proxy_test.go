package downloader

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Scrin/yt-dlp-ui/internal/network"
)

// A subprocess of this test binary acts as yt-dlp, capturing exact argv/env.
func TestProxySubprocess(t *testing.T) {
	if os.Getenv("PROXY_TEST_HELPER") != "1" {
		return
	}
	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	name := filepath.Base(args[len(args)-1])
	if os.Getenv("PROXY_CAPTURE_PHASE") == "1" {
		if slices.Contains(args, "-J") {
			name += "-resolve"
		} else {
			name += "-download"
		}
	}
	data, _ := json.Marshal(struct{ Args, Env []string }{args, os.Environ()})
	if err := os.WriteFile(filepath.Join(os.Getenv("PROXY_CAPTURE_DIR"), name+".json"), data, 0600); err != nil {
		os.Exit(2)
	}
	if os.Getenv("PROXY_FAIL") == "1" {
		for i, arg := range args {
			if arg == "--proxy" {
				u, _ := url.Parse(args[i+1])
				password, _ := u.User.Password()
				fmt.Fprintln(os.Stderr, "ERROR: proxy authentication failed:", args[i+1], password)
				os.Exit(1)
			}
		}
	}
	if os.Getenv("PROXY_HOLD") == "1" {
		for {
			if _, err := os.Stat(filepath.Join(os.Getenv("PROXY_CAPTURE_DIR"), "release")); err == nil {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	for _, arg := range args {
		if arg == "-J" {
			if fixture := os.Getenv("PROXY_RESOLVE_FIXTURE"); fixture != "" {
				data, err := os.ReadFile(fixture)
				if err != nil {
					os.Exit(2)
				}
				fmt.Print(string(data))
				os.Exit(0)
			}
			fmt.Print(`{"id":"test","title":"Test","formats":[]}`)
			os.Exit(0)
		}
	}
	if os.Getenv("PROXY_DOWNLOAD_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "ERROR: requested format is not available")
		os.Exit(1)
	}
	output := os.Getenv("PROXY_OUTPUT_PATH")
	if output == "" {
		f, err := os.CreateTemp(os.Getenv("PROXY_CAPTURE_DIR"), ".tmp.*.webm")
		if err != nil {
			os.Exit(2)
		}
		output = f.Name()
		f.Close()
	}
	result := DownloadResult{Path: output, ID: "test", Title: "Test", Height: 720, VCodec: "vp9", ACodec: "opus"}
	if raw := os.Getenv("PROXY_RESULT_JSON"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			os.Exit(2)
		}
	}
	data, _ = json.Marshal(result)
	fmt.Println("download_result=" + string(data))
	fmt.Println(`download:{"status":"downloading","_percent":50}`)
	os.Exit(0)
}

func fakeYtDlp(t *testing.T) (*YtDlp, string) {
	t.Helper()
	dir := t.TempDir()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROXY_TEST_BINARY", bin)
	t.Setenv("PROXY_TEST_HELPER", "1")
	t.Setenv("PROXY_CAPTURE_DIR", dir)
	wrapper := filepath.Join(dir, "yt-dlp")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec \"$PROXY_TEST_BINARY\" -test.run=TestProxySubprocess -- \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return NewYtDlp(wrapper, ""), dir
}

func capture(t *testing.T, dir, name string) (args, env []string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(dir, name+".json"))
		if err == nil {
			var c struct{ Args, Env []string }
			if json.Unmarshal(data, &c) == nil {
				return c.Args, c.Env
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("subprocess did not start")
	return nil, nil
}

func assertProxy(t *testing.T, args, env []string, expected string) {
	t.Helper()
	count := 0
	for i, arg := range args {
		if arg == "--proxy" {
			count++
			if args[i+1] != network.CLIProxy(expected) {
				t.Errorf("wrong proxy argument: %q", args[i+1])
			}
		}
	}
	if count != 1 || args[len(args)-2] != "--" {
		t.Fatalf("unsafe argv: %q", args)
	}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		switch strings.ToLower(key) {
		case "http_proxy", "https_proxy", "all_proxy":
			if value != expected {
				t.Errorf("environment proxy mismatch: %s", key)
			}
		case "no_proxy":
			t.Error("inherited proxy bypass")
		}
	}
}

func TestProxyArgumentsForResolveAndDownload(t *testing.T) {
	y, dir := fakeYtDlp(t)
	t.Setenv("HTTPS_PROXY", "http://inherited:8888")
	t.Setenv("no_proxy", "*")
	for _, raw := range []string{"", "http://user:p%40ss@[::1]:7890", "https://proxy:443", "socks4://user@proxy:1080", "socks5://proxy:1080", "socks5h://user:plus+pass@proxy:1080"} {
		for _, mode := range []string{"", "video", "playlist"} {
			// Shell-looking input must remain a single argument, after --.
			videoURL := "https://example.com/$(touch should-not-exist)"
			if _, err := y.Resolve(context.Background(), videoURL, mode, raw); err != nil {
				t.Fatal(err)
			}
			args, env := capture(t, dir, "$(touch should-not-exist)")
			assertProxy(t, args, env, raw)
			if args[len(args)-1] != videoURL {
				t.Fatal("URL changed")
			}
		}
		ch := make(chan Progress, 8)
		if _, err := y.Download(context.Background(), "https://example.com/download", "bv+ba/b", dir, ch, raw); err != nil {
			t.Fatal(err)
		}
		args, env := capture(t, dir, "download")
		assertProxy(t, args, env, raw)
		if len(ch) == 0 {
			t.Fatal("progress broken")
		}
	}
}

func TestProxyErrorsDoNotExposeCredentials(t *testing.T) {
	y, dir := fakeYtDlp(t)
	t.Setenv("PROXY_FAIL", "1")
	raw := "socks5h://private-user:p%40ss+word@localhost:1080"
	_, resolveErr := y.Resolve(context.Background(), "https://example.com/failure", "video", raw)
	_, downloadErr := y.Download(context.Background(), "https://example.com/failure", "18", dir, make(chan Progress, 8), raw)
	for _, err := range []error{resolveErr, downloadErr} {
		if err == nil {
			t.Fatal("expected proxy authentication failure")
		}
		message := err.Error()
		if !strings.Contains(message, "authentication failed") {
			t.Fatal("diagnostics lost")
		}
		for _, secret := range []string{"private-user", "p%40ss", "p@ss", "word"} {
			if strings.Contains(message, secret) {
				t.Fatalf("credential leaked: %s", message)
			}
		}
	}
}

func TestQueuedAndRunningJobsKeepProxySnapshot(t *testing.T) {
	y, dir := fakeYtDlp(t)
	t.Setenv("PROXY_HOLD", "1")
	settings, _ := network.Open(filepath.Join(t.TempDir(), "network.json"))
	old := "http://user:secret@proxy:7890"
	if err := settings.Save(network.Update{Settings: network.Settings{Enabled: true, URL: old}}); err != nil {
		t.Fatal(err)
	}
	m := NewManager(y, dir, 0)
	events, unsubscribe := m.Subscribe("test")
	defer unsubscribe()
	selection, _ := settings.Select(network.Policy{})
	running := m.Submit("https://example.com/running", "18", "running", "", selection)
	queued := m.Submit("https://example.com/queued", "bv+ba/b", "queued", "Playlist", selection)
	done := make(chan struct{})
	go func() { m.processJob(<-m.queue); close(done) }()
	capture(t, dir, "running")
	if err := settings.Save(network.Update{Settings: network.Settings{Enabled: true, URL: "socks5h://new-proxy:1080"}}); err != nil {
		t.Fatal(err)
	}
	next, _ := settings.Select(network.Policy{})
	future := m.Submit("https://example.com/future", "18", "future", "", next)
	if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("running job stuck")
	}
	m.processJob(<-m.queue)
	m.processJob(<-m.queue)
	for _, name := range []string{"running", "queued"} {
		args, env := capture(t, dir, name)
		assertProxy(t, args, env, old)
	}
	args, env := capture(t, dir, "future")
	assertProxy(t, args, env, next.URL)
	for _, job := range []*Job{running, queued, future} {
		actual, _ := m.Get(job.ID)
		if actual.Status != StatusComplete {
			t.Fatalf("job failed: %+v", actual)
		}
		actual.Status = StatusError
		unchanged, _ := m.Get(job.ID)
		if unchanged.Status != StatusComplete {
			t.Fatal("Get exposed mutable job")
		}
	}
	data, _ := json.Marshal(m.List())
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "user@") {
		t.Fatalf("job leaked credentials: %s", data)
	}
	for len(events) > 0 {
		event := <-events
		data, _ := json.Marshal(event)
		if strings.Contains(string(data), "secret") {
			t.Fatal("SSE leaked credentials")
		}
	}
	// A queued cancellation cannot be restarted by processJob.
	cancelled := m.Submit("https://example.com/cancelled", "18", "cancelled", "", selection)
	if err := m.Cancel(cancelled.ID); err != nil {
		t.Fatal(err)
	}
	m.processJob(<-m.queue)
	if _, err := os.Stat(filepath.Join(dir, "cancelled.json")); !os.IsNotExist(err) {
		t.Fatal("cancelled job ran")
	}
}
