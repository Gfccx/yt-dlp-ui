package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Scrin/yt-dlp-ui/internal/downloader"
	"github.com/Scrin/yt-dlp-ui/internal/network"
	"github.com/gin-gonic/gin"
)

func TestNetworkSettingsAndDownloadAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	s, _ := network.Open(filepath.Join(dir, "config", "network.json"))
	y := downloader.NewYtDlp("does-not-exist", "")
	m := downloader.NewManager(y, dir, 0)
	r := gin.New()
	SetupRoutes(r, m, y, dir, nil, s)
	do := func(method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w
	}
	w := do("GET", "/api/settings/network", "", 200)
	if !strings.Contains(w.Body.String(), `"effective":"Direct"`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Body)
	}
	do("PUT", "/api/settings/network", `{"enabled":true,"url":"http://alice:secret@proxy:7890"}`, 200)
	w = do("GET", "/api/settings/network", "", 200)
	if strings.Contains(w.Body.String(), "secret") || !strings.Contains(w.Body.String(), `"password_set":true`) {
		t.Fatal(w.Body)
	}
	do("PUT", "/api/settings/network", `{"enabled":true,"url":"http://alice@proxy:7890","keep_password":true}`, 200)
	if s.Current().URL != "http://alice:secret@proxy:7890" {
		t.Fatal("password not preserved")
	}
	do("PUT", "/api/settings/network", `{"enabled":true,"url":"http://alice@elsewhere:7890","keep_password":true}`, 400)
	do("PUT", "/api/settings/network", `{"enabled":true,"url":"bad secret"}`, 400)
	for _, body := range []string{
		`{"url":"https://example.com","proxy":{"mode":"custom","url":"invalid"}}`,
		`{"url":"https://example.com","proxy":{"mode":"wrong"}}`,
	} {
		do("POST", "/api/downloads", body, 400)
		do("POST", "/api/resolve", body, 400)
	}
	for _, tc := range []struct{ policy, label, mode string }{
		{"", "http://proxy:7890", "global"},
		{`,"proxy":{"mode":"direct"}`, "Direct", "direct"},
		{`,"proxy":{"mode":"custom","url":"socks5h://bob:private@custom:1080"}`, "socks5h://custom:1080", "custom"},
	} {
		w := do("POST", "/api/downloads", `{"url":"https://example.com","format_id":"bv+ba/b","playlist_title":"Playlist"`+tc.policy+`}`, 202)
		var job downloader.Job
		if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
			t.Fatal(err)
		}
		if job.ProxyLabel != tc.label || job.ProxyMode != tc.mode || job.Status != downloader.StatusQueued || job.PlaylistTitle != "Playlist" {
			t.Fatal(w.Body)
		}
		if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "private") {
			t.Fatal("leaked credentials")
		}
	}
	do("PUT", "/api/settings/network", `{"enabled":false,"url":""}`, 200)
	w = do("GET", "/api/downloads", "", 200)
	if !strings.Contains(w.Body.String(), "http://proxy:7890") {
		t.Fatal("queued job changed")
	}
	do("POST", "/api/settings/network/test", `{"enabled":true,"url":"bad"}`, 400)
	// A refused local port cannot be replaced by an arbitrary supplied test URL.
	w = do("POST", "/api/settings/network/test", `{"enabled":true,"url":"http://127.0.0.1:1","target":"http://127.0.0.1/private"}`, 200)
	var result network.TestResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Target != network.TestTarget || result.OK || result.Category != "connection" {
		t.Fatal(w.Body)
	}
	if s.Current().Enabled {
		t.Fatal("testing modified saved settings")
	}
	if len(m.List()) != 3 {
		t.Fatal("test interfered with download queue")
	}
}

func TestNetworkRequestSizeLimit(t *testing.T) {
	s, _ := network.Open(filepath.Join(t.TempDir(), "network.json"))
	r := gin.New()
	r.PUT("/settings", handleSaveNetwork(s))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(`{"url":"`+strings.Repeat("x", 9000)+`"}`)))
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
