package downloader

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Leave headroom below the common 255-byte component limit. Only the title
// is shortened: the video ID, language, quality and extension remain intact.
const maxFilenameBytes = 240

func sanitizeFilenamePart(s string) string {
	s = strings.ToValidUTF8(s, "_")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		if strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, s)
	return strings.TrimRightFunc(s, func(r rune) bool { return r == '.' || unicode.IsSpace(r) })
}

func truncateUTF8(s string, budget int) string {
	if len(s) <= budget {
		return s
	}
	for budget > 0 && !utf8.RuneStart(s[budget]) {
		budget--
	}
	return s[:budget]
}

func buildDownloadFilename(result DownloadResult, tag, collision string) (string, error) {
	// Reject unusable IDs instead of silently changing the deduplication key.
	id := result.ID
	if id == "" || sanitizeFilenamePart(id) != id || strings.ContainsAny(id, "[]") {
		return "", fmt.Errorf("invalid video ID for filename")
	}
	ext := filepath.Ext(result.Path)
	if ext == "" || sanitizeFilenamePart(ext) != ext {
		return "", fmt.Errorf("invalid download extension")
	}
	suffix := collision + " [" + id + "]"
	if tag != "" {
		suffix += "_" + sanitizeFilenamePart(tag)
	}
	suffix += ext
	budget := maxFilenameBytes - len(suffix)
	if budget < len(id) {
		return "", fmt.Errorf("video ID and format metadata exceed filename byte limit")
	}
	title := sanitizeFilenamePart(result.Title)
	if strings.TrimSpace(title) == "" {
		title = id
	}
	title = sanitizeFilenamePart(truncateUTF8(title, budget))
	if strings.TrimSpace(title) == "" {
		title = id
	}
	return title + suffix, nil
}

// All single-video and playlist jobs finalize here, using raw metadata rather
// than reverse-parsing yt-dlp's sanitized temporary filename. Linking within
// the download directory atomically avoids overwriting an existing version.
func renameWithQualityTag(result DownloadResult, tag string) (string, error) {
	for n := 0; ; n++ {
		collision := ""
		if n > 0 {
			collision = fmt.Sprintf(" (%d)", n+1)
		}
		name, err := buildDownloadFilename(result, tag, collision)
		if err != nil {
			return "", err
		}
		dst := filepath.Join(filepath.Dir(result.Path), name)
		if err := linkOrCopyExclusive(result.Path, dst); os.IsExist(err) {
			continue
		} else if err != nil {
			return "", fmt.Errorf("finalize download filename: %w", err)
		}
		if err := os.Remove(result.Path); err != nil {
			return "", fmt.Errorf("remove temporary download: %w", err)
		}
		return dst, nil
	}
}

func linkOrCopyExclusive(src, dst string) error {
	if err := os.Link(src, dst); err == nil || os.IsExist(err) {
		return err
	}
	// Some download volumes (e.g. FAT or network shares) do not support hard
	// links. Exclusive creation keeps the same no-overwrite guarantee there.
	return copyExclusive(src, dst)
}

func copyExclusive(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(dst)
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	return nil
}

func resolvedAudioLanguage(result DownloadResult, selected string) string {
	if languageTag.MatchString(selected) {
		return selected
	}
	for _, f := range result.RequestedFormats {
		if hasAudio(f) && languageTag.MatchString(f.Language) {
			return f.Language
		}
	}
	if languageTag.MatchString(result.Language) {
		return result.Language
	}
	if normalizeCodec(result.ACodec) != "" {
		return "und"
	}
	return ""
}
