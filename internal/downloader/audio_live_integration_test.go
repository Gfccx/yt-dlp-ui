//go:build integration

package downloader

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Opt in explicitly: this test contacts YouTube and downloads two audio files.
// ffprobe verifies real audio streams; after_move reports the actual resolved
// language/ID, and ffprobe verifies the corrected output language tags.
func TestYouTubeAudioTracksLive(t *testing.T) {
	if os.Getenv("YT_DLP_AUDIO_LIVE") != "1" {
		t.Skip("set YT_DLP_AUDIO_LIVE=1 to download real YouTube audio")
	}
	bin := os.Getenv("YT_DLP_PATH")
	if bin == "" {
		bin = "yt-dlp"
	}
	if _, err := exec.LookPath(bin); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Fatal(err)
	}
	y := NewYtDlp(bin, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	proxy := os.Getenv("YT_DLP_AUDIO_PROXY")
	url := "https://www.youtube.com/watch?v=F0OkwXKcPSE"
	info, err := y.Resolve(ctx, url, "video", proxy)
	if err != nil {
		t.Fatal(err)
	}
	if info.Video == nil {
		t.Fatal("missing video metadata")
	}
	var hashes [][32]byte
	for _, tc := range []struct{ id, language, codec, tag string }{
		{"251-22", "zh-Hant", "opus", "zho"},
		{"251-23", "en", "opus", "eng"},
	} {
		selector, language, audioID, err := selectAudio(*info.Video, tc.id, AudioSelection{Language: tc.language, FormatID: tc.id})
		if err != nil {
			t.Fatal(err)
		}
		ch := make(chan Progress, 32)
		done := make(chan struct{})
		go func() {
			for range ch {
			}
			close(done)
		}()
		result, err := y.download(ctx, url, selector, t.TempDir(), ch, proxy, audioID, language)
		<-done
		if err != nil {
			t.Fatal(err)
		}
		if result.Language != language || result.FormatID != tc.id {
			t.Fatalf("wrong actual track: %+v", result)
		}
		data, err := os.ReadFile(result.Path)
		if err != nil || len(data) < 1024 {
			t.Fatalf("missing media: %v", err)
		}
		hashes = append(hashes, sha256.Sum256(data))
		out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "stream=codec_type,codec_name:stream_tags=language", "-of", "json", result.Path).Output()
		if err != nil {
			t.Fatal(err)
		}
		var probe struct {
			Streams []struct {
				CodecType string            `json:"codec_type"`
				CodecName string            `json:"codec_name"`
				Tags      map[string]string `json:"tags"`
			} `json:"streams"`
		}
		if err := json.Unmarshal(out, &probe); err != nil {
			t.Fatal(err)
		}
		if len(probe.Streams) != 1 || probe.Streams[0].CodecType != "audio" || probe.Streams[0].CodecName != tc.codec {
			t.Fatalf("unexpected media streams: %s", out)
		}
		if probe.Streams[0].Tags["language"] != tc.tag {
			t.Fatalf("wrong output language metadata: %s", out)
		}
		t.Logf("verified actual format=%s language=%s bytes=%d ffprobe=%s", result.FormatID, result.Language, len(data), out)
	}
	if hashes[0] == hashes[1] {
		t.Fatal("different language tracks produced identical bytes")
	}
}
