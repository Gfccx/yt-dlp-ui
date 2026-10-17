package downloader

import (
	"fmt"
	"regexp"
	"strings"
)

// AudioSelection is immutable for a job, including on retry. Empty language
// means yt-dlp's default. A nonempty language is an exact (case-insensitive)
// language tag, never an implicit fallback to another region or language.
type AudioSelection struct {
	Language string `json:"language,omitempty"`
	FormatID string `json:"format_id,omitempty"`
}

var languageTag = regexp.MustCompile(`^[A-Za-z]{2,8}(?:-[A-Za-z0-9]{1,8})*$`)
var audioFormatID = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// These are the existing playlist profiles. With an explicit language, audio
// is chosen from actual metadata and every video fallback keeps that exact ID.
// Arbitrary selectors remain opaque in Auto mode; do not try to rewrite their
// grammar or append an unrestricted fallback after a language restriction.
var languageProfiles = map[string]struct{ video, codec, ext string }{
	"bv[vcodec^=vp9]+ba[acodec^=opus]/bv*+ba*/b":     {"bv[vcodec^=vp9]", "opus", ""},
	"bv[vcodec^=av01]+ba[acodec^=opus]/bv*+ba*/b":    {"bv[vcodec^=av01]", "opus", ""},
	"bv[vcodec^=avc1]+ba[acodec^=mp4a]/b[ext=mp4]/b": {"bv[vcodec^=avc1]", "mp4a", ""},
	"bv*+ba*/b":               {"bv", "", ""},
	"ba*[acodec^=opus]/ba*/b": {"", "opus", ""},
	"ba*[ext=m4a]/ba*/b":      {"", "", "m4a"},
}

func (a AudioSelection) Validate(selector string) error {
	if a.Language != "" && !languageTag.MatchString(a.Language) {
		return fmt.Errorf("invalid audio language tag")
	}
	if a.FormatID != "" {
		if !audioFormatID.MatchString(a.FormatID) {
			return fmt.Errorf("invalid audio format ID")
		}
		parts := strings.Split(selector, "+")
		if len(parts) > 2 || parts[len(parts)-1] != a.FormatID || (len(parts) == 2 && !audioFormatID.MatchString(parts[0])) {
			return fmt.Errorf("audio format ID must match the exact download format selection")
		}
	} else if a.Language != "" {
		if _, ok := languageProfiles[selector]; !ok {
			return fmt.Errorf("explicit audio language requires a built-in playlist profile; use Auto for a custom selector")
		}
	}
	return nil
}

func hasAudio(f Format) bool { return f.ACodec != "" && f.ACodec != "none" }
func hasVideo(f Format) bool { return f.VCodec != "" && f.VCodec != "none" }
func usableAudio(f Format) bool {
	return hasAudio(f) && !hasVideo(f) && f.Protocol != "mhtml" && !strings.Contains(strings.ToLower(f.FormatNote), "storyboard")
}

func defaultAudioFormat(v VideoInfo) string {
	// yt-dlp has already evaluated its normal default selector. Prefer that
	// exact result over guessing a language from bitrate or codec.
	for _, f := range v.RequestedFormats {
		if hasAudio(f) {
			return f.FormatID
		}
	}
	for _, f := range v.Formats {
		if f.FormatID == v.FormatID && hasAudio(f) {
			return f.FormatID
		}
	}
	var best *Format
	for i := range v.Formats {
		f := &v.Formats[i]
		if !usableAudio(*f) {
			continue
		}
		if best == nil || audioPreference(*f) >= audioPreference(*best) {
			best = f
		}
	}
	if best != nil {
		return best.FormatID
	}
	return ""
}

func audioPreference(f Format) float64 {
	if f.LanguagePreference != nil {
		return *f.LanguagePreference
	}
	if strings.Contains(strings.ToLower(f.FormatNote), "(default)") {
		return 1
	}
	return 0
}

func strictAudioSelector(id, language string) string {
	if languageTag.MatchString(language) {
		return id + "[language=" + language + "]"
	}
	return id
}

// selectAudio uses yt-dlp's worst-to-best format order within one language.
// The raw ID is never split on '-' (e.g. 251-1 is different from 251-0).
func selectAudio(v VideoInfo, selector string, a AudioSelection, pinned ...string) (string, string, string, error) {
	if err := a.Validate(selector); err != nil {
		return "", "", "", err
	}
	if a.FormatID != "" {
		var selected *Format
		for i := range v.Formats {
			f := &v.Formats[i]
			if f.FormatID == a.FormatID && usableAudio(*f) {
				selected = f
				break
			}
		}
		if selected == nil {
			return "", "", "", fmt.Errorf("audio format %q is no longer available; fetch formats again", a.FormatID)
		}
		if a.Language != "" && !strings.EqualFold(selected.Language, a.Language) {
			return "", "", "", fmt.Errorf("audio format %q does not provide requested language %q", a.FormatID, a.Language)
		}
		if videoID, _, combined := strings.Cut(selector, "+"); combined {
			var video *Format
			for i := range v.Formats {
				if v.Formats[i].FormatID == videoID {
					video = &v.Formats[i]
					break
				}
			}
			if video == nil || !hasVideo(*video) {
				return "", "", "", fmt.Errorf("video format %q is no longer available; fetch formats again", videoID)
			}
			if hasAudio(*video) {
				return "", "", "", fmt.Errorf("select a video-only format to combine with the chosen audio track; this video format already contains audio")
			}
		}
		// yt-dlp extracts metadata again at download time. Guard the ID's
		// language too, so an ID reassignment cannot silently select a dub.
		selector = strings.TrimSuffix(selector, a.FormatID) + strictAudioSelector(a.FormatID, selected.Language)
		return selector, selected.Language, selected.FormatID, nil
	}
	if a.Language == "" {
		return selector, "", "", nil
	}
	profile := languageProfiles[selector]
	var best, preferred *Format
	for i := range v.Formats {
		f := &v.Formats[i]
		if !usableAudio(*f) || !strings.EqualFold(f.Language, a.Language) {
			continue
		}
		if len(pinned) > 0 && pinned[0] != "" && f.FormatID != pinned[0] {
			continue
		}
		best = f
		if (profile.codec == "" || strings.HasPrefix(f.ACodec, profile.codec)) && (profile.ext == "" || f.Ext == profile.ext) {
			preferred = f
		}
	}
	if preferred != nil {
		best = preferred
	}
	if best == nil {
		if len(pinned) > 0 && pinned[0] != "" {
			return "", "", "", fmt.Errorf("audio format %q for language %q is no longer available; retry did not switch tracks", pinned[0], a.Language)
		}
		return "", "", "", fmt.Errorf("requested audio language %q is unavailable for this video; no other language was downloaded", a.Language)
	}
	if !audioFormatID.MatchString(best.FormatID) {
		return "", "", "", fmt.Errorf("unsupported audio format ID %q", best.FormatID)
	}
	audio := strictAudioSelector(best.FormatID, best.Language)
	resolved := audio
	if profile.video != "" {
		resolved = profile.video + "+" + audio
		if profile.video != "bv" {
			resolved += "/bv+" + audio
		}
	}
	return resolved, best.Language, best.FormatID, nil
}
