package api

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Scrin/yt-dlp-ui/internal/downloader"
	"github.com/Scrin/yt-dlp-ui/internal/network"
	"github.com/gin-gonic/gin"
)

type formatRequest struct {
	URL   string         `json:"url" binding:"required"`
	Mode  string         `json:"mode"` // "", "video", or "playlist" — disambiguates URLs with both v= and list=
	Proxy network.Policy `json:"proxy"`
}

type downloadRequest struct {
	URL           string                    `json:"url" binding:"required"`
	FormatID      string                    `json:"format_id"`
	Title         string                    `json:"title"`
	PlaylistTitle string                    `json:"playlist_title"`
	Proxy         network.Policy            `json:"proxy"`
	Audio         downloader.AudioSelection `json:"audio"`
}

type fileInfo struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// handleResolveURL resolves a URL to either a single video (with formats) or
// a playlist (with flat stub entries).
func handleResolveURL(ytdlp *downloader.YtDlp, settings *network.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req formatRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "url is required"})
			return
		}

		selection, err := settings.Select(req.Proxy)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		result, err := ytdlp.Resolve(c.Request.Context(), req.URL, req.Mode, selection.URL)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, result)
	}
}

// handleStartDownload submits a new download job.
func handleStartDownload(mgr *downloader.Manager, settings *network.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req downloadRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "url is required"})
			return
		}

		selection, err := settings.Select(req.Proxy)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := req.Audio.Validate(req.FormatID); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		job := mgr.SubmitWithAudio(req.URL, req.FormatID, req.Title, req.PlaylistTitle, selection, req.Audio)
		c.JSON(http.StatusAccepted, job)
	}
}

func handleRetryDownload(mgr *downloader.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		job, err := mgr.Retry(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusAccepted, job)
	}
}

// handleListDownloads returns all jobs.
func handleListDownloads(mgr *downloader.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, mgr.List())
	}
}

// handleCancelDownload cancels a download job.
func handleCancelDownload(mgr *downloader.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if err := mgr.Cancel(id); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
	}
}

// handleListFiles lists downloaded files in the download directory.
func handleListFiles(downloadDir string) gin.HandlerFunc {
	return func(c *gin.Context) {
		entries, err := os.ReadDir(downloadDir)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read download directory"})
			return
		}

		files := make([]fileInfo, 0, len(entries))
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			// A legitimate raw title can begin with ".tmp.". Final files
			// retain their ID/tag boundary; staging files have no quality tag.
			if strings.HasPrefix(entry.Name(), ".tmp.") && !strings.Contains(entry.Name(), "]_") {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			files = append(files, fileInfo{
				Name:    entry.Name(),
				Size:    info.Size(),
				ModTime: info.ModTime(),
			})
		}

		// Sort by modification time, newest first
		sort.Slice(files, func(i, j int) bool {
			return files[i].ModTime.After(files[j].ModTime)
		})

		c.JSON(http.StatusOK, files)
	}
}

// handleDeleteFile deletes a single file from the download directory.
// Uses filepath.Clean to prevent directory traversal.
// Also removes the associated completed job from the manager so the downloads list updates.
func handleDeleteFile(downloadDir string, mgr *downloader.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := c.Param("name")
		clean := filepath.Clean(name)
		if filepath.IsAbs(clean) || clean != filepath.Base(clean) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid filename"})
			return
		}

		fullPath := filepath.Join(downloadDir, clean)
		if err := os.Remove(fullPath); err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete file"})
			return
		}

		mgr.RemoveByFilename(clean)
		c.JSON(http.StatusOK, gin.H{"status": "deleted"})
	}
}

// handleServeFile serves a single file from the download directory.
// Uses filepath.Clean to prevent directory traversal.
func handleServeFile(downloadDir string) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := strings.TrimPrefix(c.Param("filepath"), "/")
		// Clean and validate path to prevent traversal
		clean := filepath.Clean(name)
		if filepath.IsAbs(clean) || clean != filepath.Base(clean) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid filename"})
			return
		}

		fullPath := filepath.Join(downloadDir, clean)
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
			return
		}

		c.File(fullPath)
	}
}
