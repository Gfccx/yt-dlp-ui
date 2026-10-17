//go:build integration

package integration

import (
	"testing"
	"time"

	"github.com/Scrin/yt-dlp-ui/internal/downloader"
)

// Test target URLs.
//
// Internet Archive is the hard-gate target: yt-dlp ships a dedicated
// archive.org extractor, the items are public-domain, and archive.org has no
// anti-bot posture toward CI traffic. BigBuckBunny_124 is a known-stable
// identifier (Blender's Big Buck Bunny — uploaded 2011, license CC-BY 3.0).
//
// YouTube is the warn-only target: jNQXAC9IVRw is "Me at the zoo", the
// first-ever YouTube video, 19 seconds long, published 2005-04-23, and
// unlikely to be removed or geo-locked. CI runner IPs are frequently
// rate-limited by YouTube, so failures here in CI are typically network
// issues, not yt-dlp regressions.
const (
	internetArchiveURL = "https://archive.org/details/BigBuckBunny_124"
	youtubeURL         = "https://www.youtube.com/watch?v=jNQXAC9IVRw"

	// Force the audio+video merge path on the YouTube test by using a yt-dlp
	// selector that demands separate streams. This exercises ffmpeg, which
	// is invoked by yt-dlp via --ffmpeg-location. Capped at 144p to keep the
	// download tiny (~300 KB).
	youtubeMergeSelector = "bv*[height<=144]+ba/b"

	resolveTimeout  = 60 * time.Second
	downloadTimeout = 5 * time.Minute
)

func TestResolve_InternetArchive(t *testing.T) {
	s := startServer(t)
	res := s.resolve(t, internetArchiveURL, "")

	if res.Type != "video" {
		t.Fatalf("expected type=video, got %q", res.Type)
	}
	if res.Video == nil {
		t.Fatal("expected video info, got nil")
	}
	if res.Video.ID == "" {
		t.Error("video id empty")
	}
	if res.Video.Title == "" {
		t.Error("video title empty")
	}
	if len(res.Video.Formats) == 0 {
		t.Error("no formats returned")
	}
	t.Logf("resolved %q (%d formats)", res.Video.Title, len(res.Video.Formats))
}

func TestFullDownload_InternetArchive(t *testing.T) {
	s := startServer(t)
	res := s.resolve(t, internetArchiveURL, "")
	if res.Video == nil || len(res.Video.Formats) == 0 {
		t.Fatal("resolve returned no formats")
	}

	format := smallestFormat(res.Video.Formats)
	t.Logf("picked format %s (%s, %d bytes)", format.FormatID, format.Resolution, format.Filesize)

	stream := s.subscribeEvents(t)
	defer stream.cancel()

	job := s.submitDownload(t, internetArchiveURL, format.FormatID, res.Video.Title)
	if job.ID == "" {
		t.Fatal("submitted job has empty id")
	}
	if job.Status != downloader.StatusQueued && job.Status != downloader.StatusDownloading {
		t.Errorf("unexpected initial job status %q", job.Status)
	}

	result := stream.waitForTerminal(t, job.ID, downloadTimeout)
	if result.Final.Type == "job:error" {
		t.Fatalf("download failed: %s", result.Final.Job.Error)
	}
	if result.Final.Job.Status != downloader.StatusComplete {
		t.Fatalf("expected complete, got status %q", result.Final.Job.Status)
	}
	if result.Final.Job.Filename == "" {
		t.Fatal("completed job has empty filename")
	}

	// Pins the contract that the download path emits at least one progress
	// event with a recognizable status string ("downloading"). This is the
	// most likely yt-dlp surface to regress silently — without it, progress
	// updates would be silently dropped.
	sawDownloading := false
	for _, ev := range result.Progress {
		if ev.Job != nil && ev.Job.Status == downloader.StatusDownloading {
			sawDownloading = true
			break
		}
	}
	if !sawDownloading {
		t.Errorf("never saw a job:progress with status=downloading (saw %d progress events)", len(result.Progress))
	}

	// Confirm the file made it onto disk and the API can list it.
	files := s.listFiles(t)
	if !containsFile(files, result.Final.Job.Filename) {
		t.Fatalf("downloaded file %q missing from /api/files (got %d files)", result.Final.Job.Filename, len(files))
	}

	// Verify the bytes are a real media file with at least one stream.
	local := s.fetchFile(t, result.Final.Job.Filename)
	streams := ffprobeStreams(t, local)
	if len(streams) == 0 {
		t.Fatal("ffprobe reported no streams in downloaded file")
	}
	if !hasVideoStream(streams) {
		t.Errorf("expected a video stream in archive.org download; ffprobe streams: %+v", streams)
	}
	t.Logf("downloaded %s, %d streams", result.Final.Job.Filename, len(streams))
}

func TestResolve_YouTube(t *testing.T) {
	s := startServer(t)
	res := s.resolve(t, youtubeURL, "video")

	if res.Type != "video" {
		t.Fatalf("expected type=video, got %q", res.Type)
	}
	if res.Video == nil {
		t.Fatal("expected video info, got nil")
	}
	if res.Video.ID == "" {
		t.Error("video id empty")
	}
	if res.Video.Title == "" {
		t.Error("video title empty")
	}
	if len(res.Video.Formats) == 0 {
		t.Error("no formats returned")
	}
	t.Logf("resolved %q (%d formats)", res.Video.Title, len(res.Video.Formats))
}

func TestFullDownload_YouTube(t *testing.T) {
	s := startServer(t)
	stream := s.subscribeEvents(t)
	defer stream.cancel()

	// Submit directly with a selector — no resolve roundtrip needed, and the
	// merge selector exercises ffmpeg via yt-dlp's --ffmpeg-location.
	job := s.submitDownload(t, youtubeURL, youtubeMergeSelector, "Me at the zoo")
	if job.ID == "" {
		t.Fatal("submitted job has empty id")
	}

	result := stream.waitForTerminal(t, job.ID, downloadTimeout)
	if result.Final.Type == "job:error" {
		t.Fatalf("download failed: %s", result.Final.Job.Error)
	}
	if result.Final.Job.Status != downloader.StatusComplete {
		t.Fatalf("expected complete, got status %q", result.Final.Job.Status)
	}
	if result.Final.Job.Filename == "" {
		t.Fatal("completed job has empty filename")
	}

	files := s.listFiles(t)
	if !containsFile(files, result.Final.Job.Filename) {
		t.Fatalf("downloaded file %q missing from /api/files", result.Final.Job.Filename)
	}

	local := s.fetchFile(t, result.Final.Job.Filename)
	streams := ffprobeStreams(t, local)

	// The merge selector forces separate video + audio formats, which yt-dlp
	// muxes via ffmpeg. Both must be present in the output container.
	if !hasVideoStream(streams) {
		t.Errorf("no video stream in YouTube merge download; streams: %+v", streams)
	}
	if !hasAudioStream(streams) {
		t.Errorf("no audio stream in YouTube merge download; streams: %+v", streams)
	}
	t.Logf("downloaded %s, %d streams", result.Final.Job.Filename, len(streams))
}

func containsFile(files []fileInfo, name string) bool {
	for _, f := range files {
		if f.Name == name {
			return true
		}
	}
	return false
}
