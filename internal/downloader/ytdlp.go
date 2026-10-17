package downloader

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Scrin/yt-dlp-ui/internal/network"
	"github.com/google/uuid"
	"golang.org/x/text/language"
)

// VideoInfo is the single-video shape of `yt-dlp -J --flat-playlist`'s JSON
// output (populated when Resolve determines the URL points at a video, not
// a playlist). Includes the full list of available formats.
type VideoInfo struct {
	ID                   string   `json:"id"`
	Title                string   `json:"title"`
	Description          string   `json:"description"`
	Thumbnail            string   `json:"thumbnail"`
	Duration             float64  `json:"duration"`
	Uploader             string   `json:"uploader"`
	URL                  string   `json:"webpage_url"`
	Formats              []Format `json:"formats"`
	FormatID             string   `json:"format_id,omitempty"`
	Language             string   `json:"language,omitempty"`
	RequestedFormats     []Format `json:"requested_formats,omitempty"`
	DefaultAudioFormatID string   `json:"default_audio_format_id,omitempty"`
}

// PlaylistInfo is a flat-playlist listing returned by `yt-dlp -J --flat-playlist`.
type PlaylistInfo struct {
	ID       string          `json:"id"`
	Title    string          `json:"title"`
	Uploader string          `json:"uploader"`
	Entries  []PlaylistEntry `json:"entries"`
}

// PlaylistEntry is a single video stub within a PlaylistInfo.
type PlaylistEntry struct {
	ID       string  `json:"id"`
	URL      string  `json:"url"`
	Title    string  `json:"title"`
	Duration float64 `json:"duration"`
}

// ResolveResult is the result of resolving a URL: either a single video with
// full formats, or a playlist with stub entries.
type ResolveResult struct {
	Type     string        `json:"type"` // "video" or "playlist"
	Video    *VideoInfo    `json:"video,omitempty"`
	Playlist *PlaylistInfo `json:"playlist,omitempty"`
}

// Format represents a single available format.
type Format struct {
	FormatID           string   `json:"format_id"`
	Ext                string   `json:"ext"`
	Resolution         string   `json:"resolution"`
	Width              int      `json:"width"`
	Height             int      `json:"height"`
	FPS                float64  `json:"fps"`
	VCodec             string   `json:"vcodec"`
	ACodec             string   `json:"acodec"`
	Filesize           int64    `json:"filesize"`
	FilesizeStr        string   `json:"filesize_approx_str,omitempty"`
	TBR                float64  `json:"tbr"`
	ABR                float64  `json:"abr"`
	VBR                float64  `json:"vbr"`
	FormatNote         string   `json:"format_note"`
	Protocol           string   `json:"protocol"`
	Language           string   `json:"language"`
	LanguagePreference *float64 `json:"language_preference"`
	AudioChannels      int      `json:"audio_channels"`
	ASR                float64  `json:"asr"`
	FilesizeApprox     int64    `json:"filesize_approx"`
}

// Progress represents a parsed progress update from yt-dlp.
type Progress struct {
	Status          string  `json:"status"`
	DownloadedBytes int64   `json:"downloaded_bytes"`
	TotalBytes      int64   `json:"total_bytes"`
	TotalEstimate   int64   `json:"total_bytes_estimate"`
	Percent         float64 `json:"_percent"` // pre-calculated by yt-dlp
	Speed           float64 `json:"speed"`
	ETA             float64 `json:"eta"`
	Elapsed         float64 `json:"elapsed"`
	Filename        string  `json:"filename"`
	Postprocessor   string  `json:"postprocessor,omitempty"`
}

// YtDlp wraps the yt-dlp CLI.
type YtDlp struct {
	BinaryPath string
	// FfmpegPath, when non-empty, is passed to yt-dlp via --ffmpeg-location
	// during downloads so the merger/post-processor can be found without
	// relying on $PATH. Accepts a directory or a full binary path.
	FfmpegPath string
}

// NewYtDlp creates a new yt-dlp wrapper. ffmpegPath may be empty, in which
// case yt-dlp locates ffmpeg/ffprobe via $PATH as usual.
func NewYtDlp(binaryPath, ffmpegPath string) *YtDlp {
	return &YtDlp{BinaryPath: binaryPath, FfmpegPath: ffmpegPath}
}

// Resolve inspects a URL and returns either a single video (with full format
// list) or a playlist (with flat stub entries, cheap for long playlists).
// mode disambiguates URLs that contain both a video and a playlist (e.g.
// YouTube "watch?v=X&list=Y"): "video" forces single-video, "playlist" forces
// the whole playlist, "" lets yt-dlp apply its own default (playlist).
func (y *YtDlp) Resolve(ctx context.Context, url, mode string, proxyURL ...string) (*ResolveResult, error) {
	args := []string{
		"-J",
		"--flat-playlist",
		"--no-warnings",
		"--color", "never",
	}
	switch mode {
	case "video":
		args = append(args, "--no-playlist")
	case "playlist":
		args = append(args, "--yes-playlist")
	}
	raw := selectedProxy(proxyURL)
	args = append(args, "--ignore-config", "--proxy", network.CLIProxy(raw), "--", url)
	cmd := exec.CommandContext(ctx, y.BinaryPath, args...)
	cmd.Env = subprocessEnv(raw)

	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("yt-dlp error: %s", network.Redact(string(exitErr.Stderr), raw))
		}
		return nil, fmt.Errorf("failed to run yt-dlp: %w", err)
	}

	var peek struct {
		Type string `json:"_type"`
	}
	if err := json.Unmarshal(out, &peek); err != nil {
		return nil, fmt.Errorf("failed to parse yt-dlp output: %w", err)
	}

	if peek.Type == "playlist" || peek.Type == "multi_video" {
		var pl PlaylistInfo
		if err := json.Unmarshal(out, &pl); err != nil {
			return nil, fmt.Errorf("failed to parse playlist: %w", err)
		}
		return &ResolveResult{Type: "playlist", Playlist: &pl}, nil
	}

	var vi VideoInfo
	if err := json.Unmarshal(out, &vi); err != nil {
		return nil, fmt.Errorf("failed to parse video: %w", err)
	}
	vi.DefaultAudioFormatID = defaultAudioFormat(vi)
	return &ResolveResult{Type: "video", Video: &vi}, nil
}

// parseProgressLine attempts to parse a yt-dlp output line as a progress event.
// It handles bare JSON, prefixed JSON (download:/postprocess:), and the default
// text format ("[download]  12.3% of ...").
func parseProgressLine(line string) (Progress, bool) {
	switch {
	case strings.HasPrefix(line, "{"):
		var p Progress
		if json.Unmarshal([]byte(line), &p) == nil {
			return p, true
		}
	case strings.HasPrefix(line, "download:"):
		var p Progress
		if json.Unmarshal([]byte(line[len("download:"):]), &p) == nil {
			return p, true
		}
	case strings.HasPrefix(line, "postprocess:"):
		var p Progress
		if json.Unmarshal([]byte(line[len("postprocess:"):]), &p) == nil {
			return p, true
		}
	case strings.HasPrefix(line, "[download]"):
		// Default text format: "[download]  12.3% of   9.63MiB at   6.55MiB/s ETA 00:01"
		if fields := strings.Fields(line); len(fields) >= 2 {
			if pct, err := strconv.ParseFloat(strings.TrimSuffix(fields[1], "%"), 64); err == nil {
				return Progress{Status: "downloading", Percent: pct}, true
			}
		}
	}
	return Progress{}, false
}

// DownloadResult holds the final filepath and the yt-dlp-reported metadata
// for the resolved format(s), emitted via --print after_move: tagged lines.
type DownloadResult struct {
	Path             string   `json:"filepath"`
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	Height           int      `json:"height"`
	VCodec           string   `json:"vcodec"`
	ACodec           string   `json:"acodec"`
	ABR              float64  `json:"abr"`
	Language         string   `json:"language"`
	FormatID         string   `json:"format_id"`
	RequestedFormats []Format `json:"requested_formats"`
}

// scanLines reads lines from r, parses progress events and sends them to ch.
// "downloading" events are throttled to at most one per 100ms; all other status
// events (finished, started, etc.) are sent immediately.
// The JSON after_move record preserves raw Unicode, whitespace and newlines.
func scanLines(r io.Reader, ch chan<- Progress, result *DownloadResult) error {
	const minInterval = 100 * time.Millisecond
	var lastSent time.Time

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		// yt-dlp may separate progress updates with \r on some versions.
		// Split so each part is processed individually.
		for part := range strings.SplitSeq(scanner.Text(), "\r") {
			line := strings.TrimSpace(part)
			if line == "" {
				continue
			}
			if p, ok := parseProgressLine(line); ok {
				if p.Status != "downloading" || time.Since(lastSent) >= minInterval {
					ch <- p
					if p.Status == "downloading" {
						lastSent = time.Now()
					}
				}
				continue
			}
			if result != nil {
				if err := parseDownloadResultLine(line, result); err != nil {
					return err
				}
			}
		}
	}
	return scanner.Err()
}

// A single JSON record avoids interpreting title newlines or '=' as protocol
// delimiters. Project only the fields needed for naming, not the entire manifest.
const downloadResultPrint = "after_move:download_result=%(.{filepath,id,title,height,vcodec,acodec,abr,language,format_id,requested_formats})j"

func parseDownloadResultLine(line string, r *DownloadResult) error {
	const prefix = "download_result="
	if !strings.HasPrefix(line, prefix) {
		return nil
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, prefix)), r); err != nil {
		return fmt.Errorf("invalid yt-dlp after_move metadata: %w", err)
	}
	return nil
}

// Each invocation gets a short isolated path, including concurrent Auto jobs.
// Titles and extractor IDs never participate in temporary filesystem paths.
const downloadOutputTemplate = ".tmp.%s.%%(ext)s"

// Download starts a download and sends progress updates to progressCh.
// Returns the final filepath plus yt-dlp-reported format metadata (height,
// vcodec, acodec, abr) so the caller can build a clean filename quality tag
// after merge/post-processing, when the resolved codecs are known.
//
// FormatID is passed to yt-dlp's -f flag unchanged — it may be a specific
// format id ("248+251") for single-video downloads or a selector expression
// ("bv*[vcodec^=vp9]+ba*[acodec^=opus]/b") for playlist downloads. yt-dlp
// treats both uniformly and emits the same post-merge metadata, so the
// filename tag built by the caller is flow-independent by construction.
func (y *YtDlp) Download(ctx context.Context, url, formatID, outputDir string, progressCh chan<- Progress, proxyURL ...string) (*DownloadResult, error) {
	return y.download(ctx, url, formatID, outputDir, progressCh, selectedProxy(proxyURL), "", "")
}

func (y *YtDlp) download(ctx context.Context, url, formatID, outputDir string, progressCh chan<- Progress, raw, audioID, audioLanguage string) (*DownloadResult, error) {
	defer close(progressCh)
	outputTemplate := fmt.Sprintf(downloadOutputTemplate, uuid.NewString())

	args := []string{
		"--color", "never",
		"--newline",
		"--no-warnings",
		"--no-playlist",
		"--progress",
		"--progress-template", "download:%(progress)j",
		"--progress-template", "postprocess:%(progress)j",
		"--no-restrict-filenames",
		"--encoding", "utf-8",
		"--no-part",
		"--no-mtime",
		"-P", outputDir,
		"-o", outputTemplate,
		"--print", downloadResultPrint,
	}

	if formatID != "" {
		args = append(args, "-f", formatID)
	}
	if audioID != "" && languageTag.MatchString(audioLanguage) {
		// YouTube sometimes labels every source WebM stream as "eng", even
		// for Chinese. Write the selected track's metadata explicitly. ISO 639-2
		// fits MP4 as well as Matroska; retain its BCP 47 variant in the title.
		tag, err := language.Parse(audioLanguage)
		if err == nil {
			base, _ := tag.Base()
			args = append(args, "--embed-metadata", "--no-embed-info-json", "--postprocessor-args",
				"Metadata+ffmpeg_o:-metadata:s:a:0 language="+base.ISO3()+" -metadata:s:a:0 title="+audioLanguage)
		}
	}
	if y.FfmpegPath != "" {
		args = append(args, "--ffmpeg-location", y.FfmpegPath)
	}

	if raw != "" {
		// Prefer yt-dlp's own HTTP/HLS/DASH clients, which support SOCKS and
		// HTTPS proxies. Some live/legacy protocols still require FFmpeg.
		args = append(args, "--downloader", "native")
	}
	args = append(args, "--ignore-config", "--proxy", network.CLIProxy(raw), "--", url)

	cmd := exec.CommandContext(ctx, y.BinaryPath, args...)
	// Disable Python's block buffering so progress lines flush immediately.
	cmd.Env = append(subprocessEnv(raw), "PYTHONUNBUFFERED=1")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start yt-dlp: %w", err)
	}

	// Scan stderr in a goroutine so stdout is drained concurrently.
	// stderr carries progress only; our tagged prints go to stdout.
	var wg sync.WaitGroup
	stderrTail := &tailBuffer{}
	var stderrErr error
	wg.Go(func() {
		stderrErr = scanLines(io.TeeReader(stderr, stderrTail), progressCh, nil)
		if stderrErr != nil {
			_ = cmd.Process.Kill()
		}
	})

	// Scan stdout for progress events and the tagged metadata prints.
	result := &DownloadResult{}
	scanErr := scanLines(stdout, progressCh, result)
	if scanErr != nil {
		_ = cmd.Process.Kill()
	}

	// Wait for the stderr goroutine to finish draining before cmd.Wait()
	// closes the pipes. When the process exits its stderr fd is closed,
	// causing EOF on our read pipe and unblocking scanner.Scan().
	wg.Wait()

	waitErr := cmd.Wait()
	if scanErr != nil {
		return nil, scanErr
	}
	if stderrErr != nil {
		return nil, stderrErr
	}
	if waitErr != nil {
		return nil, fmt.Errorf("yt-dlp exited with error: %w: %s", waitErr, network.Redact(strings.TrimSpace(string(stderrTail.data)), raw))
	}
	if result.Path == "" || result.ID == "" {
		return nil, fmt.Errorf("yt-dlp did not report the final filepath and video ID")
	}
	return result, nil
}

// Retain bounded diagnostics without holding full progress logs in memory.
type tailBuffer struct{ data []byte }

func (b *tailBuffer) Write(p []byte) (int, error) {
	const limit = 8192
	n := len(p)
	if len(p) >= limit {
		b.data = append(b.data[:0], p[len(p)-limit:]...)
		return n, nil
	}
	if excess := len(b.data) + len(p) - limit; excess > 0 {
		b.data = b.data[excess:]
	}
	b.data = append(b.data, p...)
	return n, nil
}

func selectedProxy(values []string) string {
	if len(values) > 0 {
		return values[0]
	}
	return ""
}

// Keep inherited environment/config proxies from overriding a task snapshot.
// yt-dlp forwards --proxy to supported external downloaders; these variables
// also cover HTTP(S)-aware helpers. SOCKS support in FFmpeg is limited.
func subprocessEnv(raw string) []string {
	env := make([]string, 0, len(os.Environ())+6)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToLower(key) {
		case "http_proxy", "https_proxy", "all_proxy", "no_proxy":
			continue
		}
		env = append(env, entry)
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		env = append(env, key+"="+raw)
	}
	return env
}
