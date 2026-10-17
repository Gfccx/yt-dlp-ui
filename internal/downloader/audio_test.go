package downloader

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Scrin/yt-dlp-ui/internal/network"
)

func audioFixture(t *testing.T) VideoInfo {
	t.Helper()
	data, err := os.ReadFile("testdata/F0OkwXKcPSE.json")
	if err != nil {
		t.Fatal(err)
	}
	var v VideoInfo
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	// Synthetic video formats for selection tests, not claimed as live video formats.
	v.Formats = append(v.Formats, Format{FormatID: "248", VCodec: "vp9", ACodec: "none", Height: 1080}, Format{FormatID: "18", VCodec: "avc1", ACodec: "mp4a", Language: "zh-Hant", Height: 360})
	return v
}

func TestResolvePreservesMultilingualMetadata(t *testing.T) {
	y, _ := fakeYtDlp(t)
	path, _ := filepath.Abs("testdata/F0OkwXKcPSE.json")
	t.Setenv("PROXY_RESOLVE_FIXTURE", path)
	result, err := y.Resolve(context.Background(), "https://example.com/regression", "video")
	if err != nil {
		t.Fatal(err)
	}
	v := result.Video
	if v.DefaultAudioFormatID != "251-23" || len(v.Formats) != 6 {
		t.Fatalf("%+v", v)
	}
	f := v.Formats[1]
	if f.FormatID != "140-13" || f.Language != "zh-Hant" || f.LanguagePreference == nil || *f.LanguagePreference != -1 || f.AudioChannels != 2 || f.FormatNote != "Chinese (Traditional), medium" || f.ACodec != "mp4a.40.2" || f.ABR != 129.534 || f.Ext != "m4a" || f.Protocol != "https" || f.ASR != 44100 || f.Filesize != 3134855 {
		t.Fatalf("metadata lost: %+v", f)
	}
	data, _ := json.Marshal(v)
	for _, field := range []string{`"language":"zh-Hant"`, `"language_preference":10`, `"format_id":"251-22"`, `"audio_channels":2`, `"asr":48000`, `"filesize":3134855`, `"format_note":"English original (default), medium"`} {
		if !strings.Contains(string(data), field) {
			t.Errorf("API metadata missing %s", field)
		}
	}
}

func TestAudioSelection(t *testing.T) {
	v := audioFixture(t)
	for _, tc := range []struct {
		name, selector, lang, audio, want string
		fail                              bool
	}{
		{"exact suffix", "248+251-22", "zh-Hant", "251-22", "248+251-22[language=zh-Hant]", false},
		{"same bitrate Simplified Chinese", "140-12", "zh-Hans", "140-12", "140-12[language=zh-Hans]", false},
		{"same bitrate Chinese", "140-13", "zh-Hant", "140-13", "140-13[language=zh-Hant]", false},
		{"wrong language", "251-23", "zh-Hant", "251-23", "", true},
		{"missing ID", "251", "zh-Hant", "251", "", true},
		{"no unrestricted fallback", "248+251-22/248+251-23", "zh-Hant", "251-22", "", true},
		{"embedded audio cannot override", "18+251-23", "en", "251-23", "", true},
		{"playlist best", "bv*+ba*/b", "zh-Hant", "", "bv+251-22[language=zh-Hant]", false},
		{"playlist codec", "bv[vcodec^=vp9]+ba[acodec^=opus]/bv*+ba*/b", "en", "", "bv[vcodec^=vp9]+251-23[language=en]/bv+251-23[language=en]", false},
		{"playlist AAC", "ba*[ext=m4a]/ba*/b", "zh-Hant", "", "140-13[language=zh-Hant]", false},
		{"playlist missing", "bv*+ba*/b", "ja", "", "", true},
		{"region exact", "bv*+ba*/b", "zh-TW", "", "", true},
		{"no base language guess", "bv*+ba*/b", "zh", "", "", true},
		{"case insensitive tags", "bv*+ba*/b", "ZH-hant", "", "bv+251-22[language=zh-Hant]", false},
		{"custom Auto retained", "bv[height<=720]+ba/b", "", "", "bv[height<=720]+ba/b", false},
		{"custom explicit rejected", "bv[height<=720]+ba/b", "en", "", "", true},
		{"selector injection rejected", "bv*+ba*/b", "en]/b", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, _, err := selectAudio(v, tc.selector, AudioSelection{Language: tc.lang, FormatID: tc.audio})
			if (err != nil) != tc.fail || (!tc.fail && got != tc.want) {
				t.Fatalf("got %q, %v; want %q failure=%v", got, err, tc.want, tc.fail)
			}
		})
	}
}

func TestSingleUnknownAndRegionalAudio(t *testing.T) {
	var v VideoInfo
	if err := json.Unmarshal([]byte(`{"formats":[{"format_id":"a","vcodec":"none","acodec":"aac","asr":44100,"filesize":1234,"protocol":"m3u8_native"}]}`), &v); err != nil {
		t.Fatal(err)
	}
	if defaultAudioFormat(v) != "a" || v.Formats[0].LanguagePreference != nil || v.Formats[0].ASR != 44100 || v.Formats[0].Filesize != 1234 {
		t.Fatal(v)
	}
	if _, _, _, err := selectAudio(v, "a", AudioSelection{FormatID: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := selectAudio(v, "a", AudioSelection{FormatID: "a", Language: "en"}); err == nil {
		t.Fatal("guessed a missing language")
	}
	for _, lang := range []string{"zh", "zh-CN", "zh-TW", "zh-Hant", "en", "en-US", "ja"} {
		v.Formats = append(v.Formats, Format{FormatID: lang, ACodec: "opus", VCodec: "none", Language: lang})
	}
	for _, lang := range []string{"zh", "zh-CN", "zh-TW", "zh-Hant", "en", "en-US", "ja"} {
		got, actual, id, err := selectAudio(v, "ba*[acodec^=opus]/ba*/b", AudioSelection{Language: lang})
		if err != nil || got != lang+"[language="+lang+"]" || actual != lang || id != lang {
			t.Fatalf("%s %s %s %v", got, actual, id, err)
		}
	}
}

func TestDefaultAudioUsesMetadataNotBitrate(t *testing.T) {
	v := audioFixture(t)
	v.Formats[0].ABR = 999
	if defaultAudioFormat(v) != "251-23" {
		t.Fatal("ignored yt-dlp's requested audio")
	}
	v.RequestedFormats = nil
	if defaultAudioFormat(v) != "251-23" {
		t.Fatal("ignored language preference")
	}
	for i := range v.Formats {
		v.Formats[i].LanguagePreference = nil
	}
	if defaultAudioFormat(v) != "251-23" {
		t.Fatal("ignored explicit default note")
	}
}

func TestAudioJobsRetryAndProxySnapshots(t *testing.T) {
	y, dir := fakeYtDlp(t)
	v := audioFixture(t)
	fixture := filepath.Join(dir, "fixture.json")
	data, _ := json.Marshal(v)
	if err := os.WriteFile(fixture, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROXY_RESOLVE_FIXTURE", fixture)
	t.Setenv("PROXY_CAPTURE_PHASE", "1")
	t.Setenv("PROXY_DOWNLOAD_FAIL", "1")
	proxy := network.Selection{Mode: "custom", URL: "socks5h://user:private@proxy:1080"}
	m := NewManager(y, dir, 0)
	events, unsub := m.Subscribe("audio")
	defer unsub()
	for _, tc := range []struct {
		name, selector string
		audio          AudioSelection
	}{
		{"single", "248+251-22", AudioSelection{Language: "zh-Hant", FormatID: "251-22"}},
		{"playlist", "bv*+ba*/b", AudioSelection{Language: "zh-Hant"}},
	} {
		job := m.SubmitWithAudio("https://example.com/"+tc.name, tc.selector, tc.name, "Playlist", proxy, tc.audio)
		m.processJob(<-m.queue)
		failed, _ := m.Get(job.ID)
		if failed.Status != StatusError || failed.AudioFormatID != "251-22" {
			t.Fatalf("%+v", failed)
		}
		retry, err := m.Retry(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if retry.Audio != job.Audio || retry.FormatID != job.FormatID || retry.proxyURL != proxy.URL {
			t.Fatal("retry changed selected audio or proxy")
		}
		t.Setenv("PROXY_DOWNLOAD_FAIL", "0")
		output := filepath.Join(dir, ".tmp.Audio_[F0OkwXKcPSE].webm")
		if err := os.WriteFile(output, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PROXY_OUTPUT_PATH", output)
		m.processJob(<-m.queue)
		actual, _ := m.Get(retry.ID)
		if actual.Status != StatusComplete || actual.AudioLanguage != "zh-Hant" || !strings.HasSuffix(actual.QualityTag, "_zh-Hant") {
			t.Fatalf("%+v", actual)
		}
		for _, phase := range []string{"-resolve", "-download"} {
			args, env := capture(t, dir, tc.name+phase)
			assertProxy(t, args, env, proxy.URL)
			if phase == "-download" {
				i := slices.Index(args, "-f")
				if i < 0 || !strings.Contains(args[i+1], "+251-22[language=zh-Hant]") || strings.Contains(args[i+1], "251-23") {
					t.Fatal(args)
				}
				output := slices.Index(args, "-o")
				if output < 0 || !strings.HasPrefix(args[output+1], ".tmp.") {
					t.Fatal("tracks share temporary paths", args)
				}
				metadata := slices.Index(args, "--postprocessor-args")
				if !slices.Contains(args, "--embed-metadata") || metadata < 0 || !strings.Contains(args[metadata+1], "language=zho") || !strings.Contains(args[metadata+1], "title=zh-Hant") {
					t.Fatal("incorrect output audio language tags", args)
				}
			}
		}
		t.Setenv("PROXY_DOWNLOAD_FAIL", "1")
	}
	for len(events) > 0 {
		data, _ := json.Marshal(<-events)
		if strings.Contains(string(data), "private") {
			t.Fatal("credentials leaked into SSE")
		}
	}
}

func TestDifferentAudioTracksUseIsolatedPathsAndDefaultMetadataOptionsStayUnchanged(t *testing.T) {
	y, dir := fakeYtDlp(t)
	templates := make(map[string]bool)
	for _, tc := range []struct{ id, lang, tag string }{{"251-22", "zh-Hant", "zho"}, {"251-23", "en", "eng"}} {
		_, err := y.download(context.Background(), "https://example.com/"+tc.id, strictAudioSelector(tc.id, tc.lang), dir, make(chan Progress, 8), "", tc.id, tc.lang)
		if err != nil {
			t.Fatal(err)
		}
		args, _ := capture(t, dir, tc.id)
		output := slices.Index(args, "-o")
		if output < 0 {
			t.Fatal(args)
		}
		template := args[output+1]
		if templates[template] {
			t.Fatal("different language downloads share a temporary filename")
		}
		templates[template] = true
		metadata := slices.Index(args, "--postprocessor-args")
		if metadata < 0 || !strings.Contains(args[metadata+1], "language="+tc.tag) {
			t.Fatal(args)
		}
	}
	_, err := y.Download(context.Background(), "https://example.com/legacy", "18", dir, make(chan Progress, 8))
	if err != nil {
		t.Fatal(err)
	}
	args, _ := capture(t, dir, "legacy")
	if slices.Contains(args, "--embed-metadata") || !strings.HasPrefix(args[slices.Index(args, "-o")+1], ".tmp.") {
		t.Fatal("ordinary single-track downloads changed", args)
	}
}

func TestUnavailableLanguageNeverDownloadsAndPinnedRetryNeverChangesTrack(t *testing.T) {
	y, dir := fakeYtDlp(t)
	path, _ := filepath.Abs("testdata/F0OkwXKcPSE.json")
	t.Setenv("PROXY_RESOLVE_FIXTURE", path)
	t.Setenv("PROXY_CAPTURE_PHASE", "1")
	m := NewManager(y, dir, 0)
	job := m.SubmitWithAudio("https://example.com/missing", "bv*+ba*/b", "Missing", "Playlist", network.Selection{Mode: "direct"}, AudioSelection{Language: "ja"})
	m.processJob(<-m.queue)
	got, _ := m.Get(job.ID)
	if got.Status != StatusError || !strings.Contains(got.Error, `"ja" is unavailable`) {
		t.Fatal(got)
	}
	if _, err := os.Stat(filepath.Join(dir, "missing-download.json")); !os.IsNotExist(err) {
		t.Fatal("download started without requested language")
	}
	v := audioFixture(t)
	v.Formats = append(v.Formats, Format{FormatID: "new", Language: "zh-Hant", ACodec: "opus", VCodec: "none"})
	gotSelector, _, id, err := selectAudio(v, "bv*+ba*/b", AudioSelection{Language: "zh-Hant"}, "251-22")
	if err != nil || id != "251-22" || gotSelector != "bv+251-22[language=zh-Hant]" {
		t.Fatalf("retry changed format: %s %v", gotSelector, err)
	}
	if _, _, _, err := selectAudio(v, "bv*+ba*/b", AudioSelection{Language: "zh-Hant"}, "gone"); err == nil {
		t.Fatal("retry fell back to another audio track")
	}
}
