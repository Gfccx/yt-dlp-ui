import type { Format, VideoInfo } from "../types";

export const AUTO_LANGUAGE = "";
export const UNKNOWN_LANGUAGE = "__unknown__";
export const COMMON_LANGUAGES = ["zh-Hant", "zh-Hans", "zh", "zh-CN", "zh-TW", "en", "en-US", "ja", "ko", "es", "fr", "de"];
export const AUDIO_PREFERENCE_KEY = "yt-dlp-ui.audio-language";

export function languageName(code: string): string {
  if (!code || code === UNKNOWN_LANGUAGE) return "语言未知";
  // Preserve the original tag for display and matching, even if Intl needs
  // canonicalization or the extractor returns a tag Intl does not recognize.
  const chinese: Record<string, string> = { "zh-hant": "繁体中文", "zh-hans": "简体中文", "zh": "中文", "zh-cn": "中文（中国大陆）", "zh-tw": "中文（台湾）" };
  if (chinese[code.toLowerCase()]) return chinese[code.toLowerCase()];
  try { return new Intl.DisplayNames(["zh-CN"], { type: "language" }).of(code) || code; }
  catch { return code; }
}

export function trackKind(note = ""): string {
  if (/\bdubbed-auto\b|\bauto[- ]dubbed\b|\bauto[- ]dubbing\b/i.test(note)) return "自动配音";
  if (/\boriginal\b/i.test(note)) return "原声";
  if (/\bdubbed-manual\b|\bmanually dubbed\b/i.test(note)) return "人工配音";
  if (/\bdubbed\b|\bdubbing\b/i.test(note)) return "配音";
  return "";
}

export function audioLabel(f: Format): string {
  const kind = trackKind(f.format_note);
  return `${languageName(f.language || "")}${kind ? `（${kind}）` : ""}${f.language ? ` · ${f.language}` : ""}${/\(default\)/i.test(f.format_note || "") ? " · 默认" : ""}`;
}

export function hasAudio(f: Format): boolean { return !!f.acodec && f.acodec !== "none"; }
export function hasVideo(f: Format): boolean { return !!f.vcodec && f.vcodec !== "none"; }
export function usableFormat(f: Format): boolean {
  return f.protocol !== "mhtml" && !/storyboard/i.test(f.format_note || "") && (hasAudio(f) || hasVideo(f));
}
export function audioFormats(v: VideoInfo): Format[] { return v.formats.filter(f => usableFormat(f) && hasAudio(f) && !hasVideo(f)); }

export function languageKey(f: Format): string { return f.language || UNKNOWN_LANGUAGE; }
export function sameLanguage(a: string, b: string): boolean { return a.toLowerCase() === b.toLowerCase(); }

export function audioLanguages(v: VideoInfo): string[] {
  const languages: string[] = [];
  for (const f of audioFormats(v)) {
    const key = languageKey(f);
    if (!languages.some(l => sameLanguage(l, key))) languages.push(key);
  }
  return languages;
}

export function defaultAudio(v: VideoInfo): Format | undefined {
  const formats = audioFormats(v);
  const defaultID = v.default_audio_format_id || v.requested_formats?.find(hasAudio)?.format_id;
  const explicit = formats.find(f => f.format_id === defaultID);
  if (explicit) return explicit;
  // yt-dlp returns formats from worst to best. Equal preference retains its
  // ranking rather than comparing bitrate across different codecs/languages.
  return formats.reduce<Format | undefined>((best, f) => {
    const preference = (item: Format) => item.language_preference ?? (/\(default\)/i.test(item.format_note || "") ? 1 : 0);
    return !best || preference(f) >= preference(best) ? f : best;
  }, undefined);
}

export function languageFormats(v: VideoInfo, language: string): Format[] {
  const key = language || (defaultAudio(v) ? languageKey(defaultAudio(v)!) : UNKNOWN_LANGUAGE);
  return audioFormats(v).filter(f => sameLanguage(languageKey(f), key));
}

export function bestAudio(v: VideoInfo, language: string): Format | undefined {
  // Auto follows yt-dlp's selected track exactly (which can carry a track
  // preference beyond its language). Explicit language uses yt-dlp ranking.
  if (!language) return defaultAudio(v);
  return languageFormats(v, language).at(-1);
}

export function readAudioPreference(): string {
  try { return localStorage.getItem(AUDIO_PREFERENCE_KEY) || ""; } catch { return ""; }
}
export function saveAudioPreference(language: string): void {
  try { localStorage.setItem(AUDIO_PREFERENCE_KEY, language); } catch { /* Storage may be disabled. */ }
}
