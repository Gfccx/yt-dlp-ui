package api

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/Scrin/yt-dlp-ui/internal/downloader"
	"github.com/gin-gonic/gin"
)

func TestUnicodeFilesListServeAndDelete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	mgr := downloader.NewManager(nil, dir, 0)
	r := gin.New()
	r.GET("/api/files", handleListFiles(dir))
	r.GET("/files/*filepath", handleServeFile(dir))
	r.DELETE("/api/files/:name", handleDeleteFile(dir, mgr))
	for _, name := range []string{
		"中文 日本語 한국어 😀 [title] 100% #&= [sUKtrvt0ihg]_1080p_av01_aac_zh-Hans.mp4",
		".tmp.原标题 [id]_720p_vp9_opus_en.webm",
		"legacy_[oldID]_1080p_vp9_opus.webm",
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("media"), 0600); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(dir, ".tmp.in-progress.mp4"), nil, 0600)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/api/files", nil))
			var files []fileInfo
			if err := json.Unmarshal(w.Body.Bytes(), &files); err != nil || w.Code != 200 || len(files) != 1 || files[0].Name != name {
				t.Fatalf("listing: %d %s (%v)", w.Code, w.Body, err)
			}
			w = httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/files/"+url.PathEscape(name), nil))
			if w.Code != 200 || w.Body.String() != "media" {
				t.Fatalf("serve: %d %s", w.Code, w.Body)
			}
			w = httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/files/"+url.PathEscape(name), nil))
			if w.Code != 200 {
				t.Fatalf("delete: %d %s", w.Code, w.Body)
			}
			if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
				t.Fatal("file was not deleted", err)
			}
		})
	}
}
