//go:build integration

// Package integration contains end-to-end tests that exercise the real
// yt-dlp + ffmpeg toolchain against the running HTTP server. They are gated
// by the `integration` build tag so a plain `go test ./...` does not run
// them; the scheduled workflow opts in.
package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Scrin/yt-dlp-ui/internal/downloader"
)

// server is a yt-dlp-ui instance under test. BaseURL points at either a
// locally-spawned subprocess (default) or a remote instance specified via
// YT_DLP_UI_URL (used by CI to test the docker image without spawning Go
// on the runner).
type server struct {
	BaseURL string
}

// startServer returns a server pointed at YT_DLP_UI_URL when set, or
// otherwise builds the yt-dlp-ui binary fresh, picks an ephemeral port,
// and runs it with a tempdir download directory. The subprocess is sent
// SIGTERM on test cleanup.
func startServer(t *testing.T) *server {
	t.Helper()
	if url := os.Getenv("YT_DLP_UI_URL"); url != "" {
		t.Logf("using remote server: %s", url)
		waitForReady(t, url, 30*time.Second)
		return &server{BaseURL: url}
	}

	binPath := buildBinary(t)
	port := pickFreePort(t)
	downloadDir := t.TempDir()

	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("PORT=%d", port),
		fmt.Sprintf("DOWNLOAD_DIR=%s", downloadDir),
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}

	t.Cleanup(func() {
		if cmd.Process == nil {
			return
		}
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForReady(t, baseURL, 15*time.Second)
	return &server{BaseURL: baseURL}
}

// buildBinary compiles cmd/yt-dlp-ui into a tempdir using the `dev` build tag
// (so the embedded frontend is the no-op stub — the tests only need the API).
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "yt-dlp-ui")
	cmd := exec.Command("go", "build", "-tags", "dev", "-o", bin, "./cmd/yt-dlp-ui")
	// Tests run from internal/integration/; cmd/ lives at the repo root.
	cmd.Dir = "../.."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build server binary: %v\n%s", err, out)
	}
	return bin
}

func pickFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("close port listener: %v", err)
	}
	return port
}

func waitForReady(t *testing.T, baseURL string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/api/files")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("server at %s not ready within %s", baseURL, timeout)
}

// resolve calls POST /api/resolve.
func (s *server) resolve(t *testing.T, url, mode string) downloader.ResolveResult {
	t.Helper()
	return postJSON[downloader.ResolveResult](t, s.BaseURL+"/api/resolve",
		map[string]string{"url": url, "mode": mode})
}

// submitDownload calls POST /api/downloads.
func (s *server) submitDownload(t *testing.T, url, formatID, title string) downloader.Job {
	t.Helper()
	return postJSON[downloader.Job](t, s.BaseURL+"/api/downloads",
		map[string]string{"url": url, "format_id": formatID, "title": title})
}

// fileInfo mirrors the unexported shape returned by GET /api/files.
type fileInfo struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

func (s *server) listFiles(t *testing.T) []fileInfo {
	t.Helper()
	return getJSON[[]fileInfo](t, s.BaseURL+"/api/files")
}

// fetchFile downloads the named file via GET /files/<name> into a tempfile
// and returns the local path. Used so ffprobe can inspect the result even
// when the server is remote (CI docker container).
func (s *server) fetchFile(t *testing.T, name string) string {
	t.Helper()
	resp, err := http.Get(s.BaseURL + "/files/" + name)
	if err != nil {
		t.Fatalf("GET /files/%s: %v", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /files/%s: status %d", name, resp.StatusCode)
	}
	dst := filepath.Join(t.TempDir(), name)
	f, err := os.Create(dst)
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		t.Fatalf("copy %s body: %v", name, err)
	}
	return dst
}

func postJSON[T any](t *testing.T, url string, body any) T {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s: status %d, body: %s", url, resp.StatusCode, respBody)
	}
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	return out
}

func getJSON[T any](t *testing.T, url string) T {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s: status %d, body: %s", url, resp.StatusCode, respBody)
	}
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	return out
}

// sseStream is a live subscription to /api/events. Open it BEFORE submitting
// a download so no events are missed; close it via cancel() (deferred).
type sseStream struct {
	events chan downloader.JobEvent
	cancel context.CancelFunc
	errCh  chan error
}

// subscribeEvents opens an SSE connection and parses incoming `data: {...}`
// frames into JobEvents on stream.events. The stream is closed by calling
// stream.cancel() (typically via defer). Server-sent ping comments (lines
// starting with `:`) are dropped.
func (s *server) subscribeEvents(t *testing.T) *sseStream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, "GET", s.BaseURL+"/api/events", nil)
	if err != nil {
		cancel()
		t.Fatalf("build SSE request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("connect SSE: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		cancel()
		t.Fatalf("SSE status: %d", resp.StatusCode)
	}

	stream := &sseStream{
		events: make(chan downloader.JobEvent, 64),
		cancel: cancel,
		errCh:  make(chan error, 1),
	}

	go func() {
		defer resp.Body.Close()
		defer close(stream.events)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimPrefix(line, "data: ")
			var event downloader.JobEvent
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				continue
			}
			select {
			case stream.events <- event:
			case <-ctx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			stream.errCh <- err
		}
	}()

	return stream
}

// observed captures the events seen for a job until a terminal event arrives.
type observed struct {
	Progress []downloader.JobEvent
	Final    downloader.JobEvent
}

// waitForTerminal blocks until a job:complete / job:error / job:cancelled
// event arrives for jobID, or timeout elapses. Returns the captured progress
// events alongside the terminal event. Also accepts job:init with terminal
// status, in case the job finished before SSE was attached (shouldn't happen
// when subscribeEvents is called before submitDownload, but harmless).
func (s *sseStream) waitForTerminal(t *testing.T, jobID string, timeout time.Duration) observed {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	out := observed{}
	for {
		select {
		case <-deadline.C:
			t.Fatalf("timed out after %s waiting for terminal event for job %s; saw %d progress events", timeout, jobID, len(out.Progress))
		case err := <-s.errCh:
			t.Fatalf("SSE stream error: %v", err)
		case event, ok := <-s.events:
			if !ok {
				t.Fatalf("SSE stream closed before terminal event for job %s", jobID)
			}
			if event.Job == nil || event.Job.ID != jobID {
				continue
			}
			switch event.Type {
			case "job:progress":
				out.Progress = append(out.Progress, event)
			case "job:complete", "job:error", "job:cancelled":
				out.Final = event
				return out
			case "job:init":
				if event.Job.Status == downloader.StatusComplete ||
					event.Job.Status == downloader.StatusError ||
					event.Job.Status == downloader.StatusCancelled {
					out.Final = event
					return out
				}
			}
		}
	}
}

// ffprobeStream is the subset of `ffprobe -show_streams` output we assert on.
type ffprobeStream struct {
	Index     int    `json:"index"`
	CodecType string `json:"codec_type"`
	CodecName string `json:"codec_name"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
}

// ffprobeStreams returns the streams contained in the media file at path.
// Used after a download to assert that real video/audio streams landed.
func ffprobeStreams(t *testing.T, path string) []ffprobeStream {
	t.Helper()
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v\nstderr: %s", path, err, stderr.String())
	}
	var result struct {
		Streams []ffprobeStream `json:"streams"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("decode ffprobe json: %v\nraw: %s", err, out)
	}
	return result.Streams
}

func hasVideoStream(streams []ffprobeStream) bool {
	for _, s := range streams {
		if s.CodecType == "video" {
			return true
		}
	}
	return false
}

func hasAudioStream(streams []ffprobeStream) bool {
	for _, s := range streams {
		if s.CodecType == "audio" {
			return true
		}
	}
	return false
}

// smallestFormat picks the format with the smallest non-zero filesize from
// the resolved video. Falls back to the first format if every entry reports
// filesize=0 (some extractors don't populate it).
func smallestFormat(formats []downloader.Format) downloader.Format {
	var pick downloader.Format
	picked := false
	for _, f := range formats {
		if f.Filesize <= 0 {
			continue
		}
		if !picked || f.Filesize < pick.Filesize {
			pick = f
			picked = true
		}
	}
	if !picked && len(formats) > 0 {
		return formats[0]
	}
	return pick
}
