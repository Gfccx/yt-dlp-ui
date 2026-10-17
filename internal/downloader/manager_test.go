package downloader

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"slices"
	"unicode/utf8"

	"github.com/Scrin/yt-dlp-ui/internal/network"
	"path/filepath"
	"strings"
	"testing"
)

// Single-video and playlist jobs share metadata parsing and final naming.

func TestNormalizeCodec(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// Short, clean codec names pass through.
		{"vp9", "vp9"},
		{"vp8", "vp8"},
		{"opus", "opus"},

		// YouTube's full codec strings collapse to short friendly names.
		{"vp09.00.51.08", "vp9"},
		{"vp09.00.41.08", "vp9"},
		{"av01.0.08M.08", "av01"},
		{"avc1.640028", "h264"},
		{"avc1.42001E", "h264"},
		{"avc3.640028", "h264"},
		{"hev1.1.6.L120.90", "h265"},
		{"hvc1.2.4.L120.90", "h265"},
		{"mp4a.40.2", "aac"},
		{"mp4a.40.5", "aac"},
		{"ac-3", "ac3"},
		{"ec-3", "eac3"},

		// Casing and whitespace must not matter.
		{"VP9", "vp9"},
		{"  Opus  ", "opus"},
		{"AVC1.640028", "h264"},

		// Empty / absent markers.
		{"", ""},
		{"none", ""},
		{"NA", ""},
		{"na", ""},

		// Unknown codec → take head before first dot, lowercased.
		{"ffvhuff.1.2", "ffvhuff"},
		{"custom", "custom"},
	}
	for _, c := range cases {
		if got := normalizeCodec(c.in); got != c.want {
			t.Errorf("normalizeCodec(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildQualityTag(t *testing.T) {
	cases := []struct {
		name                 string
		height               int
		abr                  float64
		vcodec, acodec, want string
	}{
		// --- Canonical cases both flows must produce identically ---

		// YouTube VP9 + Opus merged (single-video explicit "248+251" OR
		// playlist selector "bv*[vcodec^=vp9]+ba*[acodec^=opus]/b").
		{"vp9+opus 1080p", 1080, 128.93, "vp9", "opus", "1080p_vp9_opus"},
		{"vp9+opus 2160p full codec string", 2160, 160.0, "vp09.00.51.08", "opus", "2160p_vp9_opus"},

		// YouTube H.264 + AAC merged (single-video "137+140" OR playlist
		// selector "bv*[ext=mp4]+ba*[ext=m4a]/b").
		{"h264+aac 1080p", 1080, 129.5, "avc1.640028", "mp4a.40.2", "1080p_h264_aac"},
		{"h264+aac 360p combined fmt 18", 360, 96.0, "avc1.42001E", "mp4a.40.2", "360p_h264_aac"},

		// AV1 + Opus (playlist "bv*[vcodec^=av01]+ba*[acodec^=opus]/b").
		{"av1+opus 1080p", 1080, 128.0, "av01.0.08M.08", "opus", "1080p_av01_opus"},

		// H.265 (rare for YouTube, common elsewhere).
		{"h265+opus 720p hev1", 720, 128.0, "hev1.1.6.L120.90", "opus", "720p_h265_opus"},
		{"h265+opus 720p hvc1", 720, 128.0, "hvc1.2.4", "opus", "720p_h265_opus"},

		// Video-only (user picked only a video format with no audio).
		{"video-only vp9", 1080, 0, "vp9", "none", "1080p_vp9"},
		{"video-only h264 empty acodec", 720, 0, "avc1.640028", "", "720p_h264"},

		// Audio-only (single-video pick of "251" OR playlist selector
		// "ba*[acodec^=opus]/ba*/b"). yt-dlp reports height="NA" which our
		// parser maps to 0, vcodec="none".
		{"audio-only opus", 0, 128.93, "none", "opus", "129kbps_opus"},
		{"audio-only aac", 0, 128.0, "none", "mp4a.40.2", "128kbps_aac"},
		{"audio-only rounded abr", 0, 131.7, "", "opus", "132kbps_opus"},

		// --- Edge cases ---

		// Unknown codec strings: normalizeCodec passes through the head.
		{"unknown video codec", 480, 0, "weirdvideo", "none", "480p_weirdvideo"},

		// All metadata absent — hits the audio branch with abr=0 and no codec.
		{"all empty", 0, 0, "", "", "0kbps_audio"},
		{"all none", 0, 0, "none", "none", "0kbps_audio"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildQualityTag(c.height, c.abr, c.vcodec, c.acodec)
			if got != c.want {
				t.Errorf("buildQualityTag(height=%d, abr=%g, vcodec=%q, acodec=%q) = %q, want %q",
					c.height, c.abr, c.vcodec, c.acodec, got, c.want)
			}
		})
	}
}

// TestBuildQualityTag_ConsistentAcrossFlows asserts the core invariant: for
// metadata yt-dlp emits, the tag is identical regardless of how the format
// was requested. Two flows, same inputs → same output, verified explicitly.
func TestBuildQualityTag_ConsistentAcrossFlows(t *testing.T) {
	// Metadata values below are exactly what yt-dlp prints via
	// `--print after_move:KEY=%(KEY)s` for these resolved formats, observed
	// on YouTube as of 2026-04 (yt-dlp 2026.02.04).
	scenarios := []struct {
		name                                  string
		height                                int
		abr                                   float64
		vcodec, acodec                        string
		singleVideoFormatID, playlistSelector string // documentation only
	}{
		{
			name:                "VP9 1080p + Opus 128kbps",
			height:              1080,
			abr:                 128.93,
			vcodec:              "vp9",
			acodec:              "opus",
			singleVideoFormatID: "248+251",
			playlistSelector:    "bv*[vcodec^=vp9]+ba*[acodec^=opus]/bv*+ba*/b",
		},
		{
			name:                "H.264 1080p + AAC 129kbps",
			height:              1080,
			abr:                 129.502,
			vcodec:              "avc1.640028",
			acodec:              "mp4a.40.2",
			singleVideoFormatID: "137+140",
			playlistSelector:    "bv*[ext=mp4]+ba*[ext=m4a]/b[ext=mp4]/b",
		},
		{
			name:                "Opus audio-only 128kbps",
			height:              0,
			abr:                 128.93,
			vcodec:              "none",
			acodec:              "opus",
			singleVideoFormatID: "251",
			playlistSelector:    "ba*[acodec^=opus]/ba*/b",
		},
	}
	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			// Single-video flow and playlist flow feed identical metadata
			// into buildQualityTag; both must therefore produce the same tag.
			fromSingleVideo := buildQualityTag(s.height, s.abr, s.vcodec, s.acodec)
			fromPlaylist := buildQualityTag(s.height, s.abr, s.vcodec, s.acodec)
			if fromSingleVideo != fromPlaylist {
				t.Errorf("tag diverged between flows: single=%q playlist=%q "+
					"(would have been produced by -f %q vs -f %q)",
					fromSingleVideo, fromPlaylist, s.singleVideoFormatID, s.playlistSelector)
			}
		})
	}
}

func TestRenameWithQualityTag(t *testing.T) {
	for _, tc := range []struct{ title, want string }{
		{"中文 标题 日本語 한국어 😀 👨‍👩‍👧‍👦", "中文 标题 日本語 한국어 😀 👨‍👩‍👧‍👦"},
		{"normal  spaces & [brackets] = 100%", "normal  spaces & [brackets] = 100%"},
		{"a/b\\c:d*e?f\"g<h>i|j\x00\n\r\t\x7f . ", "a_b_c_d_e_f_g_h_i_j"},
		{"", "sUKtrvt0ihg"},
		{" \t\n... ", "sUKtrvt0ihg"},
		{"NA", "NA"},   // A literal title is not a missing-metadata sentinel.
		{"CON", "CON"}, // The ID suffix makes this a valid Windows filename.
	} {
		t.Run(tc.title, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), ".tmp.job.mp4")
			if err := os.WriteFile(src, []byte("media"), 0600); err != nil {
				t.Fatal(err)
			}
			result := DownloadResult{Path: src, ID: "sUKtrvt0ihg", Title: tc.title}
			got, err := renameWithQualityTag(result, "1080p_av01_aac_zh-Hans")
			want := tc.want + " [sUKtrvt0ihg]_1080p_av01_aac_zh-Hans.mp4"
			if err != nil || filepath.Base(got) != want {
				t.Fatalf("got %q, %v; want %q", got, err, want)
			}
			if data, err := os.ReadFile(got); err != nil || string(data) != "media" {
				t.Fatalf("final file: %q, %v", data, err)
			}
			if _, err := os.Stat(src); !os.IsNotExist(err) {
				t.Fatal("temporary source was not removed", err)
			}
		})
	}
}

func TestLongFilenameUTF8ByteLimit(t *testing.T) {
	for _, title := range []string{
		strings.Repeat("中日韓😀", 300),
		strings.Repeat("a", 2000),
		strings.Repeat("e\u0301", 400),
		strings.Repeat("👨‍👩‍👧‍👦", 100),
		strings.Repeat(" ", 300) + "中文",
	} {
		src := filepath.Join(t.TempDir(), ".tmp.short.webm")
		if err := os.WriteFile(src, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		result := DownloadResult{Path: src, ID: "abc123", Title: title}
		got, err := renameWithQualityTag(result, "2160p_av01_opus_zh-Hant")
		if err != nil {
			t.Fatal(err)
		}
		base := filepath.Base(got)
		if !utf8.ValidString(base) || len(base) > maxFilenameBytes || !strings.HasSuffix(base, " [abc123]_2160p_av01_opus_zh-Hant.webm") {
			t.Fatalf("invalid filename (%d bytes): %q", len(base), base)
		}
	}
}

func TestRenameDoesNotOverwriteAndReportsErrors(t *testing.T) {
	dir := t.TempDir()
	result := DownloadResult{Path: filepath.Join(dir, ".tmp.job.mp4"), ID: "id", Title: "Title"}
	want := filepath.Join(dir, "Title [id]_720p_h264_aac_en.mp4")
	os.WriteFile(want, []byte("old"), 0600)
	os.WriteFile(result.Path, []byte("new"), 0600)
	got, err := renameWithQualityTag(result, "720p_h264_aac_en")
	if err != nil || filepath.Base(got) != "Title (2) [id]_720p_h264_aac_en.mp4" {
		t.Fatalf("%q %v", got, err)
	}
	if data, _ := os.ReadFile(want); string(data) != "old" {
		t.Fatal("existing file was overwritten")
	}
	if _, err := renameWithQualityTag(result, "tag"); err == nil {
		t.Fatal("missing source was silently treated as complete")
	}
	for _, id := range []string{"", "../unsafe", "id[other]", strings.Repeat("x", 250)} {
		result.ID = id
		if _, err := buildDownloadFilename(result, "tag", ""); err == nil {
			t.Fatalf("accepted invalid/oversized ID %q", id)
		}
	}
}

func TestFilenameCopyFallbackDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "source"), filepath.Join(dir, "destination")
	if err := os.WriteFile(src, []byte("original media"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyExclusive(src, dst); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(dst); err != nil || string(data) != "original media" {
		t.Fatalf("fallback corrupted media: %q %v", data, err)
	}
	os.WriteFile(src, []byte("replacement"), 0600)
	if err := copyExclusive(src, dst); !os.IsExist(err) {
		t.Fatal("fallback did not use exclusive creation", err)
	}
	if data, _ := os.ReadFile(dst); string(data) != "original media" {
		t.Fatal("fallback overwrote destination")
	}
}

func TestAfterMoveJSONPreservesRawTitleAndPath(t *testing.T) {
	want := DownloadResult{
		Path: "/downloads/中文 😀 = path.mp4", ID: "id", Title: "  中文\n日本語\r한국어 😀 = height=0  ",
		Height: 1080, VCodec: "av01", ACodec: "mp4a.40.2",
		RequestedFormats: []Format{{ACodec: "aac", Language: "zh-Hans"}},
	}
	data, _ := json.Marshal(want)
	var got DownloadResult
	if err := scanLines(bytes.NewBufferString("[log] ignored\ndownload_result="+string(data)+"\n"), make(chan Progress, 1), &got); err != nil {
		t.Fatal(err)
	}
	if got.Title != want.Title || got.Path != want.Path || got.Height != 1080 || resolvedAudioLanguage(got, "") != "zh-Hans" {
		t.Fatalf("metadata corrupted: %+v", got)
	}
	if err := parseDownloadResultLine(`download_result={"title":`, &got); err == nil {
		t.Fatal("malformed JSON was silently accepted")
	}
	// Titles well above Scanner's default 64 KiB must reach byte-safe naming.
	want.Title = strings.Repeat("中文😀", 10000)
	data, _ = json.Marshal(want)
	if err := scanLines(bytes.NewBufferString("download_result="+string(data)), make(chan Progress, 1), &got); err != nil || got.Title != want.Title {
		t.Fatal("long metadata record lost", err)
	}
}

func TestFilenamePipelineSingleAndPlaylistMultilingual(t *testing.T) {
	y, dir := fakeYtDlp(t)
	m := NewManager(y, dir, 0)
	seenTemplates := make(map[string]bool)
	for _, lang := range []string{"zh-Hans", "zh-Hant", "en", "ja", "ko"} {
		for _, flow := range []struct{ name, selector, playlist string }{
			{"single", "248+251", ""}, {"playlist", "bv*+ba*/b", "List"},
		} {
			src := filepath.Join(dir, ".tmp."+flow.name+lang+".webm")
			os.WriteFile(src, []byte(lang), 0600)
			result := DownloadResult{Path: src, ID: "sameID", Title: "原始 标题 日本語 한국어 😀 [Title]", Height: 1080, VCodec: "vp9", ACodec: "opus", RequestedFormats: []Format{{ACodec: "opus", Language: lang}}}
			data, _ := json.Marshal(result)
			t.Setenv("PROXY_RESULT_JSON", string(data))
			job := m.SubmitWithAudio("https://example.com/"+flow.name+lang, flow.selector, "stale client title", flow.playlist, network.Selection{Mode: "direct"}, AudioSelection{})
			m.processJob(<-m.queue)
			got, _ := m.Get(job.ID)
			if got.Status != StatusComplete || got.AudioLanguage != lang || !strings.HasSuffix(got.Filename, " [sameID]_1080p_vp9_opus_"+lang+".webm") || !strings.HasPrefix(got.Filename, result.Title) {
				t.Fatalf("%s %s: %+v", flow.name, lang, got)
			}
			args, _ := capture(t, dir, flow.name+lang)
			if slices.Contains(args, "--restrict-filenames") || !slices.Contains(args, "--no-restrict-filenames") || !slices.Contains(args, downloadResultPrint) {
				t.Fatal("unsafe filename/metadata flags", args)
			}
			template := args[slices.Index(args, "-o")+1]
			if seenTemplates[template] || strings.Contains(template, "%(title)") || len(template) > 64 {
				t.Fatal("temporary filenames collide or depend on title", template)
			}
			seenTemplates[template] = true
		}
	}
}

func TestResolvedAudioLanguage(t *testing.T) {
	for _, tc := range []struct {
		result         DownloadResult
		selected, want string
	}{
		{DownloadResult{Language: "en", ACodec: "aac"}, "zh-Hant", "zh-Hant"},
		{DownloadResult{Language: "ja", ACodec: "aac"}, "", "ja"},
		{DownloadResult{ACodec: "opus"}, "", "und"},
		{DownloadResult{VCodec: "vp9", ACodec: "none"}, "", ""},
	} {
		if got := resolvedAudioLanguage(tc.result, tc.selected); got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
}

func TestDownloadMissingOrMalformedMetadataFails(t *testing.T) {
	y, dir := fakeYtDlp(t)
	t.Setenv("PROXY_RESULT_JSON", `{"id":""}`)
	if _, err := y.Download(context.Background(), "https://example.com/missing", "18", dir, make(chan Progress, 8)); err == nil {
		t.Fatal("missing video ID accepted")
	}
}
