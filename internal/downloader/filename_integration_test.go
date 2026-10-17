//go:build integration

package downloader

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise the real CLI, output template and after_move protocol against a
// local website. No YouTube account, proxy or external network is required.
func TestFilenameLocalYtDlp(t *testing.T) {
	bin := os.Getenv("YT_DLP_PATH")
	if bin == "" {
		bin = "yt-dlp"
	}
	if _, err := exec.LookPath(bin); err != nil {
		t.Skip("install yt-dlp or set YT_DLP_PATH for local filename integration")
	}
	for _, title := range []string{"中文 标题 日本語 한국어 😀 [test]", strings.Repeat("中😀", 300)} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, ".mp4") {
				w.Header().Set("Content-Type", "video/mp4")
				fmt.Fprint(w, "local media payload")
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<html><title>%s</title><video src="/one.mp4"></video>`, html.EscapeString(title))
			if r.URL.Path == "/playlist" {
				fmt.Fprint(w, `<video src="/two.mp4"></video>`)
			}
			fmt.Fprint(w, `</html>`)
		}))
		func() {
			defer srv.Close()
			y := NewYtDlp(bin, "")
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			for _, flow := range []string{"video", "playlist"} {
				info, err := y.Resolve(ctx, srv.URL+"/"+flow, flow)
				if err != nil {
					t.Fatal(err)
				}
				urls := []string{srv.URL + "/video"}
				if flow == "playlist" {
					if info.Playlist == nil || len(info.Playlist.Entries) != 2 {
						t.Fatalf("expected local playlist: %+v", info)
					}
					urls = nil
					for _, entry := range info.Playlist.Entries {
						urls = append(urls, entry.URL)
						// The manager downloads playlist entries individually.
					}
				}
				for _, url := range urls {
					ch := make(chan Progress, 64)
					result, err := y.Download(ctx, url, "", t.TempDir(), ch)
					if err != nil {
						t.Fatal(err)
					}
					if flow == "video" && !strings.Contains(result.Title, title) {
						t.Fatalf("CLI lost original title: %q", result.Title)
					}
					path, err := renameWithQualityTag(*result, "test")
					if err != nil || len(filepath.Base(path)) > maxFilenameBytes || !strings.Contains(filepath.Base(path), " ["+result.ID+"]_test.mp4") {
						t.Fatalf("%s: %q %v", flow, path, err)
					}
					if data, err := os.ReadFile(path); err != nil || string(data) != "local media payload" {
						t.Fatalf("media content: %q %v", data, err)
					}
				}
			}
		}()
	}
}
