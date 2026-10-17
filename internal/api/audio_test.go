package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Scrin/yt-dlp-ui/internal/downloader"
	"github.com/Scrin/yt-dlp-ui/internal/network"
	"github.com/gin-gonic/gin"
)

func TestAudioAPIHelper(t *testing.T) {
	if os.Getenv("AUDIO_API_HELPER") != "1" {
		return
	}
	data, err := os.ReadFile(os.Getenv("AUDIO_API_FIXTURE"))
	if err != nil {
		os.Exit(2)
	}
	fmt.Print(string(data))
	os.Exit(0)
}

func TestAudioAPIFormatsPolicyAndRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	bin, _ := os.Executable()
	fixture, _ := filepath.Abs("../downloader/testdata/F0OkwXKcPSE.json")
	t.Setenv("AUDIO_API_HELPER", "1")
	t.Setenv("AUDIO_API_FIXTURE", fixture)
	t.Setenv("AUDIO_API_BINARY", bin)
	wrapper := filepath.Join(dir, "yt-dlp")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec \"$AUDIO_API_BINARY\" -test.run=TestAudioAPIHelper -- \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	y := downloader.NewYtDlp(wrapper, "")
	settings, _ := network.Open(filepath.Join(dir, "network.json"))
	if err := settings.Save(network.Update{Settings: network.Settings{Enabled: true, URL: "http://user:secret@proxy:7890"}}); err != nil {
		t.Fatal(err)
	}
	mgr := downloader.NewManager(y, dir, 0)
	r := gin.New()
	SetupRoutes(r, mgr, y, dir, nil, settings)
	do := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
		}
		return w
	}
	resolved := do("POST", "/api/resolve", `{"url":"https://example.com/video","mode":"video"}`, 200)
	for _, field := range []string{`"format_id":"251-22"`, `"language":"zh-Hant"`, `"format_note":"English original (default), medium"`, `"default_audio_format_id":"251-23"`} {
		if !strings.Contains(resolved.Body.String(), field) {
			t.Fatalf("API lost %s: %s", field, resolved.Body)
		}
	}
	for _, body := range []string{
		`{"url":"https://example.com/video","format_id":"248+251-22","audio":{"language":"zh-Hant","format_id":"251-22"}}`,
		`{"url":"https://example.com/video","format_id":"bv*+ba*/b","playlist_title":"Playlist","audio":{"language":"zh-Hant"}}`,
	} {
		w := do("POST", "/api/downloads", body, 202)
		var job downloader.Job
		if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
			t.Fatal(err)
		}
		if job.Audio.Language != "zh-Hant" || strings.Contains(w.Body.String(), "secret") {
			t.Fatal(w.Body)
		}
		do("DELETE", "/api/downloads/"+job.ID, "", 200)
		settings.Save(network.Update{Settings: network.Settings{Enabled: false}})
		retried := do("POST", "/api/downloads/"+job.ID+"/retry", "", 202)
		var retry downloader.Job
		json.Unmarshal(retried.Body.Bytes(), &retry)
		if retry.ID == job.ID || retry.FormatID != job.FormatID || retry.Audio != job.Audio || retry.ProxyLabel != job.ProxyLabel || retry.ProxyMode != job.ProxyMode {
			t.Fatalf("retry changed policy: %+v %+v", job, retry)
		}
		do("POST", "/api/downloads/"+retry.ID+"/retry", "", 400)
	}
	for _, body := range []string{
		`{"url":"x","format_id":"248+251-23","audio":{"language":"zh-Hant","format_id":"251-22"}}`,
		`{"url":"x","format_id":"bv+ba/b","audio":{"language":"zh-Hant"}}`,
		`{"url":"x","format_id":"bv*+ba*/b","audio":{"language":"en]/b"}}`,
	} {
		do("POST", "/api/downloads", body, 400)
	}
	do("POST", "/api/downloads/missing/retry", "", 400)
}
