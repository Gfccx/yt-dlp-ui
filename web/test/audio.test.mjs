import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import test from "node:test";
import ts from "typescript";
import { buildSync } from "esbuild";
import { renderToStaticMarkup } from "react-dom/server";
import { createElement } from "react";

// Use the existing TypeScript compiler and Node test runner; no test framework.
const loadTS = async (path) => {
  const source = readFileSync(new URL(path, import.meta.url), "utf8");
  const { outputText } = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } });
  return import(`data:text/javascript;base64,${Buffer.from(outputText).toString("base64")}`);
};
const audio = await loadTS("../src/lib/audio.ts");
const api = await loadTS("../src/lib/api.ts");
const fixture = JSON.parse(readFileSync(new URL("../../internal/downloader/testdata/F0OkwXKcPSE.json", import.meta.url), "utf8"));
const video = { ...fixture, default_audio_format_id: "251-23", formats: [...fixture.formats, { format_id: "248", vcodec: "vp9", acodec: "none", height: 1080, ext: "webm", protocol: "https" }] };
const track = id => fixture.formats.find(f => f.format_id === id);

test("regression: six exact audio IDs survive; equal bitrates never merge languages", () => {
  assert.deepEqual(audio.audioFormats(video).map(f => f.format_id), ["140-12", "140-13", "140-23", "251-21", "251-22", "251-23"]);
  assert.deepEqual(audio.audioLanguages(video), ["zh-Hans", "zh-Hant", "en"]);
  assert.equal(track("140-12").abr, track("140-13").abr);
  assert.equal(track("251-21").abr, track("251-22").abr);
  assert.equal(audio.defaultAudio(video).format_id, "251-23");
  assert.equal(audio.bestAudio(video, "en").format_id, "251-23");
  assert.equal(audio.bestAudio(video, "zh-Hans").format_id, "251-21");
  assert.equal(audio.bestAudio(video, "zh-Hant").format_id, "251-22");
  assert.deepEqual(audio.languageFormats(video, "zh-Hant").map(f => f.format_id), ["140-13", "251-22"]);
});

test("metadata controls original/default/automatic/manual labels; never infer from quality", () => {
  assert.equal(audio.audioLabel(track("251-23")), "英语（原声） · en · 默认");
  assert.equal(audio.audioLabel(track("251-22")), "繁体中文 · zh-Hant");
  // Synthetic original note separately covers the Chinese label; the real
  // video's Chinese tracks do not claim original/default/dubbing type.
  assert.equal(audio.audioLabel({ ...track("251-22"), format_note: "Chinese (Traditional) original (default), medium" }), "繁体中文（原声） · zh-Hant · 默认");
  assert.equal(audio.trackKind("English (dubbed-auto), medium"), "自动配音");
  assert.equal(audio.trackKind("English (dubbed), medium"), "配音");
  assert.equal(audio.trackKind("English (dubbed-manual)"), "人工配音");
  assert.equal(audio.trackKind("129kbps, high"), "");
  assert.equal(audio.trackKind("(default), medium"), "");
  assert.doesNotMatch(audio.audioLabel(track("251-22")), /原声|默认|配音/);
});

test("default selection honors yt-dlp even when a different language has higher bitrate", () => {
  const changed = structuredClone(video);
  changed.formats[0].abr = 999;
  assert.equal(audio.bestAudio(changed, "").format_id, "251-23");
  delete changed.default_audio_format_id;
  delete changed.requested_formats;
  assert.equal(audio.defaultAudio(changed).language, "en");
});

test("region tags are preserved and matched exactly, without base/region fallback", () => {
  const formats = ["zh", "zh-CN", "zh-TW", "zh-Hant", "en", "en-US", "ja"].map(language => ({ format_id: language, language, vcodec: "none", acodec: "opus" }));
  const regional = { formats };
  assert.equal(audio.audioLanguages(regional).length, 7);
  for (const code of formats.map(f => f.language)) assert.equal(audio.bestAudio(regional, code).language, code);
  assert.equal(audio.bestAudio(video, "zh-CN"), undefined);
  assert.equal(audio.bestAudio(video, "ja"), undefined);
  assert.equal(audio.bestAudio(video, "ZH-hant").format_id, "251-22");
  assert.equal(audio.languageName("not_a_real_tag"), "not_a_real_tag");
});

test("single/unknown language, missing optional fields and HLS audio remain usable", () => {
  const single = { formats: [{ format_id: "one", vcodec: "none", acodec: "aac", protocol: "m3u8_native" }] };
  assert.equal(audio.audioLanguages(single).length, 1);
  assert.equal(audio.bestAudio(single, "").format_id, "one");
  assert.equal(audio.audioLabel(single.formats[0]), "语言未知");
  assert.equal(audio.bestAudio(single, "en"), undefined);
});

test("API uses exact video+audio IDs and includes selected language and proxy", async () => {
  const original = globalThis.fetch;
  const bodies = [];
  globalThis.fetch = async (url, options) => { bodies.push({ url, body: JSON.parse(options.body) }); return { ok: true, json: async () => ({ id: "job" }) }; };
  try {
    const proxy = { mode: "custom", url: "socks5h://proxy:1080" };
    await api.startDownload("https://video.invalid/id", video.formats.at(-1), track("251-22"), "Fixture", proxy, { language: "zh-Hant", format_id: "251-22" });
    assert.equal(bodies[0].body.format_id, "248+251-22");
    assert.deepEqual(bodies[0].body.audio, { language: "zh-Hant", format_id: "251-22" });
    assert.deepEqual(bodies[0].body.proxy, proxy);
    await api.startPlaylistItemDownload("https://video.invalid/id", "bv*+ba*/b", "Fixture", "List", proxy, "en");
    assert.deepEqual(bodies[1].body.audio, { language: "en" });
    assert.equal(bodies[1].body.format_id, "bv*+ba*/b");
    assert.deepEqual(bodies[1].body.proxy, proxy);
    await api.startDownload("url", video.formats.at(-1), track("140-23"), "Single");
    assert.equal(bodies[2].body.format_id, "248+140-23");
    assert.equal(bodies[2].body.audio, undefined);
  } finally { globalThis.fetch = original; }
});

const compiled = buildSync({ entryPoints: [new URL("../src/components/FormatPicker.tsx", import.meta.url).pathname], bundle: true, jsx: "automatic", platform: "node", format: "cjs", write: false, external: ["react", "react-dom", "framer-motion"] }).outputFiles[0].text;
const module = { exports: {} };
new Function("require", "module", "exports", compiled)(createRequire(import.meta.url), module, module.exports);
const { FormatPicker } = module.exports;
const renderPicker = (info, preferredLanguage = "") => renderToStaticMarkup(createElement(FormatPicker, { videoInfo: info, preferredLanguage, initialProxy: { mode: "global" }, onClose() {} }));

test("rendered multilingual picker separates language/quality and shows original metadata and full IDs", () => {
  const html = renderPicker(video);
  assert.match(html, /aria-label="Audio track language"/);
  assert.match(html, /aria-label="Audio quality"/);
  assert.match(html, /英语（原声）/);
  assert.match(html, /繁体中文/);
  assert.match(html, /zh-Hant/);
  assert.match(html, /251-23/);
  assert.match(html, /140-23/);
  assert.match(html, /English original \(default\), medium/);
  const chinese = renderPicker(video, "zh-Hant");
  assert.match(chinese, /251-22/);
  assert.match(chinese, /140-13/);
  assert.doesNotMatch(chinese, /繁体中文（原声）/);
});

test("automatic dub metadata stays visible without changing the original default track", () => {
  const autoDub = { ...video, formats: [...video.formats, { ...fixture.formats[0], format_id: "251-auto", language: "ja", format_note: "Japanese (dubbed-auto), medium" }] };
  const html = renderPicker(autoDub);
  assert.match(html, /自动配音/);
  assert.match(html, /ja/);
  assert.equal(audio.defaultAudio(autoDub).format_id, "251-23");
  assert.equal(audio.bestAudio(autoDub, "ja").format_id, "251-auto");
});

test("single-language picker keeps original controls; unavailable preference blocks download visibly", () => {
  const single = { ...video, formats: video.formats.filter(f => f.language === "zh-Hant" || f.format_id === "248") };
  assert.doesNotMatch(renderPicker(single), /aria-label="Audio track language"/);
  const unavailable = renderPicker(video, "ja");
  assert.match(unavailable, /所选音轨语言 ja 不可用/);
  assert.match(unavailable, /disabled=""/);
});
