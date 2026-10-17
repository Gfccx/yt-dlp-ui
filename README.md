# yt-dlp-ui

[English](README.md) | [简体中文](README.zh-CN.md)

Repository: [Gfccx/yt-dlp-ui](https://github.com/Gfccx/yt-dlp-ui)

A small web UI for downloading video and audio files, powered by [yt-dlp](https://github.com/yt-dlp/yt-dlp).

Paste a URL, pick a format, get a file. Works for single videos, entire playlists, and YouTube Mix/Radio URLs. Ships as a single ~15 MB static binary (or Docker image) and uses ~2–3 MB of RAM at idle.

[![ci](https://github.com/Gfccx/yt-dlp-ui/actions/workflows/ci.yml/badge.svg)](https://github.com/Gfccx/yt-dlp-ui/actions/workflows/ci.yml) [![scheduled](https://github.com/Gfccx/yt-dlp-ui/actions/workflows/scheduled.yml/badge.svg)](https://github.com/Gfccx/yt-dlp-ui/actions/workflows/scheduled.yml)

## Q&A

**Q: Does this support X?**

A: You probably want to use [tubearchivist](https://github.com/tubearchivist/tubearchivist) instead.

**Q: But I only need to Y.**

A: You probably want to use [yt-dlp](https://github.com/yt-dlp/yt-dlp) directly instead.

**Q: So, why?**

A: Laziness. I didn't want to bother installing yt-dlp on whatever device I happened to be using to download some one-off video. Existing solutions like tubearchivist were overkill for my needs and wanted more server capacity than I had. This one uses practically 0% CPU and ~2–3 MB of RAM when idling, which fits comfortably on the tiny VPS I had available.

## Features

- **Single-video downloads** with a format picker — select one video stream, one audio stream, or both; yt-dlp merges them via ffmpeg.
- **Playlist downloads** — fan out into per-video jobs with a codec-preference profile (VP9+Opus, AV1+Opus, H.264+AAC, best-available) or a custom yt-dlp format selector. Entries dedupe automatically (YouTube Mix playlists often repeat videos).
- **"Already downloaded" indicators** — the format picker and playlist picker both scan the downloads directory for the video's id and show you what you already have at what quality. Playlist re-runs default-select only the entries that are new.
- **Ambiguous URLs handled explicitly** — pasting a URL that contains both a video and a playlist (e.g. YouTube's `watch?v=X&list=Y`) presents two buttons so you pick whether to fetch the single video or the whole list.
- **Concurrent download queue** with live progress streamed over Server-Sent Events. Configurable parallelism.
- **Cancel** queued or running downloads mid-flight.
- **File browser** — list, download, and delete completed files from the browser. Deletions also remove the corresponding job entry.
- **Single-binary / single-image deployment** with the web UI embedded; no separate static-file server needed.
- **Network / proxy settings** with persistent global HTTP, HTTPS and SOCKS proxies, per-task overrides, and a backend connectivity test.
- **Multilingual audio** with separate language/quality selection, full format IDs, strict playlist language preferences, and retries retaining the chosen track and proxy.

## Filename format

Downloads are written as:

```
<title> [<id>]_<tag>_<language>.<ext>
```

- `<title>` — original Unicode video title, including Chinese, Japanese, Korean, Emoji and normal spaces. Filesystem-invalid characters are replaced, control characters removed, and trailing spaces/dots trimmed. Empty titles fall back to the video ID. Only the title is shortened to keep the full filename within 240 UTF-8 bytes.
- `<id>` — the source's video id, always in square brackets. Handy for grepping the directory regardless of title.
- `<tag>` — one of:
  - `<height>p_<vcodec>_<acodec>` for merged video+audio (e.g. `1080p_vp9_opus`, `1080p_h264_aac`, `2160p_av01_opus`).
  - `<height>p_<vcodec>` for video without audio.
  - `<abr>kbps_<acodec>` for audio-only (e.g. `128kbps_opus`).
- `<language>` — selected or resolved audio language (e.g. `zh-Hans`, `zh-Hant`, `en`); `und` if unknown, omitted for video without audio.
- `<ext>` — container chosen by yt-dlp.

Codec names are normalised to short friendly forms regardless of how yt-dlp reports them (`avc1.640028` → `h264`, `mp4a.40.2` → `aac`, `vp09.00.41.08` → `vp9`, `av01.0.08M.08` → `av01`, `hev1.1.6.*` → `h265`).

The tag is inserted _after_ the download completes from yt-dlp's `--print after_move:` output, so single-video and playlist flows produce identical filenames for identical content.

All downloads use short, unique temporary paths and raw JSON `after_move` metadata. Different audio languages have distinct final filenames. Existing files are never overwritten: collisions add ` (2)`, ` (3)`, etc. before the video ID. Existing filenames remain readable by the downloaded-video detection, file browser and delete actions.

## Quick start (Docker)

Use the Docker image **`ghcr.io/gfccx/yt-dlp-ui:latest`** to run the application. The image supports **Linux AMD64 and ARM64** and includes yt-dlp, FFmpeg and the Web UI; no local Go or Node.js installation is needed.

### Docker run

Pull the published image and start the container:

```bash
docker pull ghcr.io/gfccx/yt-dlp-ui:latest

docker run -d \
  --name yt-dlp-ui \
  --restart unless-stopped \
  -p 8080:8080 \
  -e MAX_CONCURRENT=2 \
  -v yt-dlp-downloads:/downloads \
  -v yt-dlp-config:/config \
  ghcr.io/gfccx/yt-dlp-ui:latest
```

Open [http://localhost:8080](http://localhost:8080). For a remote server, replace `localhost` with its address. To use a different host port, change `-p 8080:8080` to, for example, `-p 8081:8080`.

Downloaded files live in the `yt-dlp-downloads` volume at `/downloads`; proxy settings live in `yt-dlp-config` at `/config`. Reuse both volumes when recreating or upgrading the container. Audio language preferences are saved in the browser; task history remains in memory and clears on restart.

For a host directory, replace the downloads mount with `-v /path/on/host:/downloads`. Bind-mounted download and configuration directories must be writable by the container's `appuser` (UID 1000).

### Docker Compose

Save the following as `compose.yaml`:

```yaml
services:
  yt-dlp-ui:
    image: ghcr.io/gfccx/yt-dlp-ui:latest
    container_name: yt-dlp-ui
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      MAX_CONCURRENT: "2"
    volumes:
      - yt-dlp-downloads:/downloads
      - yt-dlp-config:/config

volumes:
  yt-dlp-downloads:
    name: yt-dlp-downloads
  yt-dlp-config:
    name: yt-dlp-config
```

Start it from the same directory:

```bash
docker compose up -d
```

Use either Docker run or Compose to manage the container. If switching from the Docker run example, remove the old container first with `docker stop yt-dlp-ui` and `docker rm yt-dlp-ui`; the named volumes are retained and reused by Compose.

### Updating the container

For Compose:

```bash
docker compose pull
docker compose up -d
```

For Docker run:

```bash
docker pull ghcr.io/gfccx/yt-dlp-ui:latest
docker stop yt-dlp-ui
docker rm yt-dlp-ui
```

Then run the original `docker run` command again with the same volume mounts. Keep the volumes to preserve downloaded files and proxy settings.

## Configuration

Server configuration is via environment variables; network settings are managed in the Web UI and saved as `CONFIG_DIR/network.json`.

| Variable | Default | Description |
| ---------------- | ------------- | ------------------------------------------------------------------------------------------------------------ |
| `PORT` | `8080` | Server listen port. |
| `DOWNLOAD_DIR` | `./downloads` | Directory for downloaded files. |
| `MAX_CONCURRENT` | `2` | Maximum parallel downloads (worker pool size). |
| `YT_DLP_PATH` | `yt-dlp` | Path or name of the yt-dlp binary. |
| `CONFIG_DIR` | `./config` | Directory for persistent Web UI network settings (`/config` in Docker). |
| `FFMPEG_PATH` | _(unset)_ | Optional path to ffmpeg. Directory containing `ffmpeg`+`ffprobe`, or a full binary path. Empty uses `$PATH`. |

In the container image, `DOWNLOAD_DIR` defaults to `/downloads` and `PORT` to `8080`. `ffmpeg` is installed on `$PATH` inside the container, so `FFMPEG_PATH` is only useful when running the binary outside Docker on a system where ffmpeg lives somewhere non-standard.

### Network / proxy settings

Open **Network / Proxy settings**, enable the global proxy, choose the protocol, and enter the host and port. Username and password are optional. Disable the proxy to select **Direct**; new installations use Direct. **Save settings** applies and persists the form. **Test proxy** tests the current form without saving it; when disabled it tests the direct route. The **Active** label always reflects the saved global route.

Supported URL schemes are `http`, `https`, `socks4`, `socks5`, and `socks5h`. HTTP proxies can tunnel HTTPS destinations with CONNECT; `https://` means TLS is also used between this server and the proxy. It requires yt-dlp's requests handler (`requests>=2.32.2`, `urllib3>=2.0.2`), included in this Docker image. When installing yt-dlp with pip outside Docker, install these into the same Python environment, for example:

```bash
python3 -m pip install -U yt-dlp 'requests[socks]>=2.32.2' 'urllib3>=2.0.2'
```

SOCKS4 resolves destinations locally to IPv4 and supports a user ID, without password authentication. SOCKS5 resolves destination names locally; SOCKS5h sends the destination name to the proxy for resolution. IPv6 proxy addresses are supported (bracketed in URLs). Credentials entered in the form are URL encoded automatically, including `@`, `:`, `+`, `/`, `#` and `%`. The API requires a full URL with an explicit port and rejects paths, queries, fragments, invalid hosts/ports, and control characters.

**Advanced · Task proxy** is available before fetching a URL and in both download pickers. Choose **Use global proxy**, **Direct for this task**, or **Custom proxy for this task**. The same choice is used for information / format extraction and submission.

At each job's submission, the backend records its selected policy and fixes the effective proxy URL. All queued and running jobs retain that snapshot after global edits; later submissions use the new global value.

Each playlist entry is a separate submission and receives its own snapshot. If global settings change while a large playlist is being queued, later entries use the new setting. The job list shows each job's actual route without credentials.

Job history still lives only in memory; saving network settings does not persist jobs.

The backend test uses Go's HTTP client to GET the fixed target `https://www.youtube.com/robots.txt`, with a 10-second overall timeout, normal TLS certificate verification, and no redirects. There is no API for choosing an arbitrary target.

It reports elapsed time, HTTP status where available, and connection, authentication, DNS, TLS, timeout, or HTTP failures. Proxy servers can hide the underlying cause (for example, remote DNS failure may appear as HTTP 502 or a generic SOCKS rejection).

Tests have a separate concurrency limit and do not occupy download workers.

This is a **Go connectivity test**, not yt-dlp extraction or a guarantee that a particular video is available. yt-dlp may use different certificate stores / handlers, and sites may still apply authentication, geo or bot restrictions. Fetching a real video URL exercises yt-dlp's actual network path.

All yt-dlp invocations receive `--proxy` explicitly, including an empty value for Direct. Inherited proxy/bypass environment variables and external yt-dlp configuration files cannot override the selected route (`--ignore-config`). Shells are never used to construct download commands.

Proxied downloads prefer yt-dlp's native downloader for HTTP/HLS/DASH. FFmpeg still merges local files normally.

Live HLS and some legacy formats require FFmpeg networking, which has limited proxy support and does not support SOCKS; HTTPS proxy support also varies by FFmpeg build. These paths cannot be guaranteed to use a SOCKS/HTTPS route.

Browser thumbnail images load from the user's browser, not through the backend proxy; this application does not separately fetch subtitles or thumbnails through other backend clients.

Inside Docker, `127.0.0.1` and `localhost` identify the container. To reach a proxy on the host, use `host.docker.internal` on Docker Desktop. On Linux, add `--add-host=host.docker.internal:host-gateway` and ensure the proxy listens on an interface reachable from the container; a loopback-only host listener is not reachable via the bridge gateway.

Network settings (including credentials) are stored in a file with mode `0600`, using an atomic replacement on save.

Settings responses omit saved passwords; leave the password blank to keep it, or select **Clear saved password** to remove it. Changing the endpoint/protocol/username requires re-entering a saved password. Credentials are omitted from job/SSE payloads and redacted from yt-dlp errors.

As with the existing download/delete API, settings have no built-in authentication: deploy this UI on a trusted network or behind an authenticated reverse proxy. Administrators with access to the configuration volume or process arguments can access proxy credentials.

### Audio language and quality

Multilingual videos show separate **音轨语言 (audio language)** and **音质 (audio quality)** controls. Auto follows yt-dlp's actual default audio selection, then its language preference and format ranking; it does not compare bitrates across languages. Each language offers its best available quality according to yt-dlp's ranking and every specific format, with complete IDs such as `140-13`, `140-23`, `251-22` and `251-23`. Video resolution is independent. Single-language videos keep the existing picker.

Labels preserve raw language codes and region/script variants. `zh-Hant` appears as **繁体中文**, or **繁体中文（原声）** when explicitly marked original. Original/default/automatic dubbing labels only come from metadata; a generic dubbed note does not imply manual dubbing. Missing language stays unknown. Raw notes, codec, sample rate, channels and complete IDs remain visible; HLS audio is retained.

The **Audio language** settings panel saves a preferred language in this browser's local storage, initializing newly opened video/playlist pickers. Each task can override it. Empty means Auto. Explicit tags match exactly, ignoring case: `zh` does not substitute `zh-Hant`, nor does `en-US` substitute `en`.

The worker refreshes metadata using the job's original proxy snapshot, validates the chosen ID/language, and downloads with an exact selector such as `251-22[language=zh-Hant]` for audio only, or `VIDEO_FORMAT_ID+251-22[language=zh-Hant]` with an available video-only ID. Its language condition also prevents ID reassignment between extraction and download from silently selecting another language. Missing languages or expired formats fail explicitly. Use a video-only stream when merging a chosen audio track: an already combined video/audio format could otherwise cause yt-dlp to ignore the extra audio.

Playlist language preferences support built-in codec profiles. Every entry resolves its own real audio ID. Codec fallback stays within the requested language; a missing language fails that entry while other queued entries continue. Strict video profiles need separate video/audio streams. Arbitrary custom selectors require Auto and retain their original behavior. **Retry** keeps the language, format/profile and proxy snapshot, including any previously resolved playlist audio ID.

Selected multilingual tracks also use FFmpeg to correct audio language tags: some YouTube Chinese Opus source files arrive tagged as English. Output stores an ISO 639 tag (e.g. `zho`/`eng`) and the original BCP 47 variant (`zh-Hant`/`en-US`) as the stream title where supported. This requires FFmpeg even for a multilingual audio-only choice; Docker already includes it. Ordinary single-language downloads retain their prior behavior.

Default YouTube player clients remain unchanged. `youtube:player_client=all` is not enabled: extra clients add requests, can require authentication/PO Tokens and can expose unusable formats. The UI cannot invent tracks omitted by the extractor. Keep yt-dlp current and consult its [YouTube arguments](https://github.com/yt-dlp/yt-dlp#youtube), [PO Token guide](https://github.com/yt-dlp/yt-dlp/wiki/PO-Token-Guide) and [JavaScript runtime setup](https://github.com/yt-dlp/yt-dlp/wiki/EJS) for missing tracks.

### When ffmpeg is actually required

yt-dlp commonly invokes ffmpeg to merge separate streams together. Live HLS and some legacy protocols can also require ffmpeg networking; see the proxy limitations above. For merging:

- **Needs ffmpeg**: any format selection with a `+` in it — a video stream merged with an audio stream. The video+audio playlist profiles can produce such selections, as does any single-video pick that pairs a video format with an audio format. On YouTube this is the common case, since anything above 720p is served as separate video-only and audio-only streams.
- **Does NOT need ffmpeg**: a single pre-combined format (e.g., YouTube's format 18, 360p avc1+mp4a bundled in one mp4), a video-only pick without an audio pair, or an audio-only pick saved to its native container (Opus → `.webm`, AAC → `.m4a`).

If you only ever download audio-only or pre-combined formats, you can run without ffmpeg installed and picks that need it will fail with a clear yt-dlp error. In practice, installing ffmpeg is cheap and recommended.

Note that the prebuilt docker image contains ffmpeg automatically, so nothing extra is required in that case.

## Playlist download profiles

When the pasted URL resolves to a playlist, the picker offers these codec preferences. Each maps to a yt-dlp `-f` expression that's passed verbatim.

| Profile | Selector |
| ------------------------------- | ------------------------------------------------ |
| VP9 + Opus (best for YouTube) | `bv[vcodec^=vp9]+ba[acodec^=opus]/bv*+ba*/b` |
| AV1 + Opus (smaller, newer) | `bv[vcodec^=av01]+ba[acodec^=opus]/bv*+ba*/b` |
| H.264 + AAC (mp4 compatibility) | `bv[vcodec^=avc1]+ba[acodec^=mp4a]/b[ext=mp4]/b` |
| Best available (any codec) | `bv*+ba*/b` |
| Audio only — Opus | `ba*[acodec^=opus]/ba*/b` |
| Audio only — AAC/m4a | `ba*[ext=m4a]/ba*/b` |

An "Advanced" section lets you paste an arbitrary yt-dlp format selector for cases the profiles don't cover.

## Development

### Prerequisites

- Go 1.25+
- Node.js 22+
- `yt-dlp` on `PATH` (or point `YT_DLP_PATH` at it)
- `ffmpeg` on `PATH` (or point `FFMPEG_PATH` at it) — only needed when yt-dlp has to merge separate video+audio streams; see [When ffmpeg is actually required](#when-ffmpeg-is-actually-required) above

### Running locally

Clone the repository if you have not already done so:

```bash
git clone https://github.com/Gfccx/yt-dlp-ui.git
cd yt-dlp-ui
```

Start the Go backend in dev mode (frontend is not embedded; Vite serves it):

```bash
go run -tags dev ./cmd/yt-dlp-ui
```

In a separate terminal, start the Vite dev server:

```bash
cd web
npm install
npm run dev
```

Open [http://localhost:5173](http://localhost:5173) — the Vite dev server proxies `/api` and `/files` requests to the Go backend on port 8080.

### Building

Build the frontend, then the Go binary:

```bash
cd web && npm ci && npm run build && cd ..
go build -trimpath -ldflags="-s -w" -o yt-dlp-ui ./cmd/yt-dlp-ui
```

Or build the container image yourself:

```bash
docker build -t yt-dlp-ui .
```

## Architecture

The project is deliberately tiny. One Go backend, one React (TypeScript + Tailwind) frontend, no database, no background workers outside of the Go process itself.

```
cmd/yt-dlp-ui/           main — wires config, manager, Gin router, graceful shutdown
internal/config/         environment-variable parsing
internal/network/        proxy validation, atomic settings storage, Go connectivity test
internal/api/            Gin handlers + Server-Sent Events stream
internal/downloader/     yt-dlp wrapper, job manager, filename contract
web/                     React SPA (Vite), embedded into the Go binary via //go:embed
```

Key invariants:

- **One filename codepath.** Both single-video and playlist downloads go through `ytdlp.Download` → `buildQualityTag` → `renameWithQualityTag` with the same metadata shape (from yt-dlp's `--print after_move:` output). Do not branch on flow type when naming files; fix the codec normaliser instead.
- **`format_id` is an opaque string.** The frontend constructs either a specific pair (`"248+251"`) or a selector expression (`"bv[vcodec^=vp9]+ba[acodec^=opus]/…"`) and the backend passes it straight to `yt-dlp -f`. No parsing on the Go side.
- **Progress is streamed, not polled.** `/api/events` is a Server-Sent Events endpoint; the frontend maintains a `Map<id, Job>` updated in place. New subscribers receive the full current state as `job:init` events on connect.
- **Jobs stay in memory for 1 hour after completion**, then get evicted by a background cleanup loop. There is no persistence — restart clears all job history. Downloaded files and saved network settings persist on disk.
