// Optional browser integration: build the Go executable, set YTDLP_UI_BINARY.
// PLAYWRIGHT_MODULE / CHROMIUM_EXECUTABLE can use an existing installation.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
assert(process.env.YTDLP_UI_BINARY, "Set YTDLP_UI_BINARY to the freshly built Go executable");
const artifacts = mkdtempSync(path.join(tmpdir(), "yt-dlp-audio-browser-"));
const fixture = fileURLToPath(new URL("../../internal/downloader/testdata/F0OkwXKcPSE.json", import.meta.url));
const fake = path.join(artifacts, "yt-dlp");
writeFileSync(fake, readFileSync(new URL("yt_dlp_fixture.py", import.meta.url)), { mode: 0o700 });
const port = await new Promise(resolve => {
  const listener = createServer();
  listener.listen(0, "127.0.0.1", () => { const { port } = listener.address(); listener.close(() => resolve(port)); });
});
const base = "http://127.0.0.1:" + port;
const server = spawn(process.env.YTDLP_UI_BINARY, [], { env: { ...process.env, PORT: String(port), DOWNLOAD_DIR: path.join(artifacts, "downloads"), CONFIG_DIR: path.join(artifacts, "config"), YT_DLP_PATH: fake, AUDIO_FIXTURE: fixture, AUDIO_CAPTURE: path.join(artifacts, "args.jsonl"), AUDIO_FAIL_ONCE: path.join(artifacts, "failed-once") }, stdio: ["ignore", "pipe", "pipe"] });
let logs = "";
server.stdout.on("data", data => { logs += data; });
server.stderr.on("data", data => { logs += data; });
let browser;
const waitFor = async (predicate, name) => {
  for (let i = 0; i < 150; i++) {
    const value = await predicate();
    if (value) return value;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error("Timed out: " + name);
};
const jobs = async () => (await fetch(base + "/api/downloads")).json();
try {
  await waitFor(async () => { try { return (await fetch(base + "/api/files")).ok; } catch { return false; } }, "backend ready");
  await fetch(base + "/api/settings/network", { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ enabled: true, url: "socks5h://fixture:private@proxy.invalid:1080" }) });
  browser = await chromium.launch({ headless: true, ...(process.env.CHROMIUM_EXECUTABLE ? { executablePath: process.env.CHROMIUM_EXECUTABLE } : {}) });
  const page = await browser.newPage({ viewport: { width: 1100, height: 1050 } });
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  const requests = [];
  page.on("request", request => { if (request.url().endsWith("/api/downloads") && request.method() === "POST") requests.push(request.postDataJSON()); });
  await page.goto(base);
  const form = page.locator("form");
  const fetchVideo = async (url = "https://video.invalid/test") => {
    await form.locator('input[type="url"]').fill(url);
    await form.getByRole("button", { name: "Fetch", exact: true }).click();
    await page.getByRole("heading", { name: "Audio fixture", exact: true }).waitFor();
  };
  await fetchVideo();
  const language = page.getByLabel("Audio track language", { exact: true });
  assert.equal(await language.inputValue(), "");
  assert.match(await language.locator("option:checked").textContent(), /英语（原声）.*en/);
  await language.selectOption("en");
  assert.equal(await page.getByRole("radio").filter({ hasText: "ID 251-22" }).count(), 0);
  await page.getByRole("radio").filter({ hasText: "ID 140-23" }).click();
  await page.getByRole("button", { name: "Download", exact: true }).click();
  await waitFor(async () => (await jobs()).find(job => job.status === "complete" && job.audio_format_id === "140-23"), "English AAC downloaded");
  assert.equal(requests[0].format_id, "248+140-23");
  assert.equal(requests[0].audio.language, "en");
  assert.deepEqual(requests[0].proxy, { mode: "global" });

  await fetchVideo();
  await page.getByLabel("Audio track language", { exact: true }).selectOption("zh-Hant");
  await page.getByRole("button", { name: "Download", exact: true }).click();
  const chinese = await waitFor(async () => (await jobs()).find(job => job.status === "complete" && job.audio_format_id === "251-22"), "Traditional Chinese downloaded");
  assert.equal(requests[1].format_id, "248+251-22");
  assert.match(chinese.filename, /_zh-Hant\.webm$/);

  const preferenceDetails = page.locator("details").filter({ has: page.getByText("音轨语言偏好 / Audio language", { exact: true }) });
  await preferenceDetails.locator("summary").click();
  await page.getByLabel("Preferred audio language", { exact: true }).fill("ja");
  await fetchVideo();
  await page.getByRole("alert").filter({ hasText: "所选音轨语言 ja 不可用" }).waitFor();
  assert.equal(await page.getByRole("button", { name: "Download", exact: true }).isDisabled(), true);
  await page.getByLabel("Preferred audio language", { exact: true }).fill("zh-Hant");
  await page.reload();
  assert.equal(await page.evaluate(() => localStorage.getItem("yt-dlp-ui.audio-language")), "zh-Hant");

  await form.locator('input[type="url"]').fill("https://video.invalid/watch?v=fixture&list=fixture");
  await form.getByRole("button", { name: "Playlist", exact: true }).click();
  await page.getByRole("heading", { name: "Language playlist", exact: true }).waitFor();
  assert.equal(await page.getByLabel("Playlist audio language").inputValue(), "zh-Hant");
  await page.getByRole("button", { name: "Download 3 videos", exact: true }).click();
  await waitFor(async () => {
    const list = await jobs();
    return list.some(j => j.title === "Chinese track" && j.status === "complete") && list.some(j => j.title === "Missing Chinese" && j.status === "error") && list.some(j => j.title === "Retryable track" && j.status === "error");
  }, "playlist strict language, remaining entries continue");
  const oldRetryJob = (await jobs()).find(j => j.title === "Retryable track");
  await page.getByRole("article", { name: "Retryable track", exact: true }).getByRole("button", { name: "Retry", exact: true }).click();
  const retried = await waitFor(async () => (await jobs()).find(j => j.title === "Retryable track" && j.status === "complete"), "retry retains language");
  assert.equal(retried.format_id, oldRetryJob.format_id);
  assert.equal(retried.audio_format_id, oldRetryJob.audio_format_id);
  assert.equal(retried.proxy_label, oldRetryJob.proxy_label);
  assert.equal(retried.audio_language, "zh-Hant");

  await preferenceDetails.locator("summary").click();
  await page.getByLabel("Preferred audio language", { exact: true }).fill("");
  await fetchVideo("https://video.invalid/single");
  assert.equal(await page.getByLabel("Audio track language", { exact: true }).count(), 0);
  await fetchVideo("https://video.invalid/test");
  // Wait for the picker's entrance animation before capturing visual evidence.
  await page.waitForTimeout(500);
  await page.screenshot({ path: path.join(artifacts, "desktop.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
  await page.screenshot({ path: path.join(artifacts, "mobile.png"), fullPage: true });
  const commands = readFileSync(path.join(artifacts, "args.jsonl"), "utf8").trim().split("\n").map(line => JSON.parse(line));
  for (const command of commands) assert.equal(command[command.indexOf("--proxy") + 1], "socks5h://fixture:private@proxy.invalid:1080");
  const downloads = commands.filter(command => command.includes("-f"));
  assert(downloads.some(command => command[command.indexOf("-f") + 1] === "248+140-23[language=en]"));
  assert(downloads.some(command => command[command.indexOf("-f") + 1] === "248+251-22[language=zh-Hant]"));
  assert(!downloads.some(command => command.at(-1).includes("english-only")));
  assert.deepEqual(errors, []);
  console.log("PASS: browser → API → queue → exact yt-dlp argv; language/quality, preference, playlist, retry, proxy, desktop/mobile");
  console.log("Artifacts: " + artifacts);
} catch (error) {
  writeFileSync(path.join(artifacts, "server.log"), logs);
  console.error("Artifacts: " + artifacts);
  throw error;
} finally {
  if (browser) await browser.close();
  server.kill("SIGTERM");
}
