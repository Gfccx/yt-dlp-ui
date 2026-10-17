# yt-dlp-ui

[English](README.md) | [简体中文](README.zh-CN.md)

项目仓库：[Gfccx/yt-dlp-ui](https://github.com/Gfccx/yt-dlp-ui)

一个基于 [yt-dlp](https://github.com/yt-dlp/yt-dlp) 的轻量视频、音频下载 Web UI。

粘贴链接、选择格式，即可下载文件。支持单个视频、完整播放列表和 YouTube Mix / Radio 链接。前端嵌入 Go 二进制，可通过单个静态可执行文件或 Docker 镜像部署。项目原有介绍中的二进制大小约为 15 MB，空闲内存占用约为 2–3 MB；实际值会随构建和运行环境变化。

[![ci](https://github.com/Gfccx/yt-dlp-ui/actions/workflows/ci.yml/badge.svg)](https://github.com/Gfccx/yt-dlp-ui/actions/workflows/ci.yml) [![scheduled](https://github.com/Gfccx/yt-dlp-ui/actions/workflows/scheduled.yml/badge.svg)](https://github.com/Gfccx/yt-dlp-ui/actions/workflows/scheduled.yml)

## 常见问题

**问：是否支持更复杂的视频管理功能？**

可以考虑 [Tube Archivist](https://github.com/tubearchivist/tubearchivist)。本项目主要用于临时下载视频和音频。

**问：如果只需要 yt-dlp 的某个高级选项呢？**

可以直接使用 [yt-dlp 命令行](https://github.com/yt-dlp/yt-dlp)。

**问：为什么开发这个项目？**

项目最初是为了方便在不同设备上偶尔下载视频，避免在每台设备上安装 yt-dlp，也希望能够在资源有限的 VPS 上运行一个简单的下载界面。

## 功能

- **单视频下载**：通过格式选择器选择视频流、音频流或两者组合，需要时由 FFmpeg 合并。
- **播放列表下载**：将列表拆分为独立任务，支持 VP9 + Opus、AV1 + Opus、H.264 + AAC、最佳可用格式，以及自定义 yt-dlp 格式表达式。列表中的重复视频会自动去重。
- **已下载提示**：格式选择器和播放列表选择器会扫描下载目录，根据视频 ID 展示已有文件及其质量。重新下载列表时，默认仅勾选尚未下载的视频。
- **明确处理歧义链接**：对于同时包含视频与播放列表参数的链接，例如 YouTube 的 `watch?v=X&list=Y`，界面提供单视频和播放列表两个选项。
- **并发下载队列**：支持配置并发数，通过 Server-Sent Events（SSE）实时展示进度。
- **取消任务**：支持取消排队或正在运行的下载。
- **文件管理**：在浏览器中查看、下载和删除已完成文件；删除文件时也会移除对应任务记录。
- **单文件或单镜像部署**：Web UI 嵌入 Go 二进制，无需额外的静态文件服务。
- **网络 / 代理设置**：支持持久化全局 HTTP、HTTPS、SOCKS 代理、单任务代理覆盖和后端连接测试。
- **多语言音轨**：语言与音质分别选择，完整保留格式 ID，支持播放列表严格语言偏好和保留音轨、代理快照的重试。

## 文件命名格式

下载文件采用以下命名方式：

```text
<title> [<id>]_<tag>_<language>.<ext>
```

- `<title>`：原始视频标题，保留中文、日文、韩文、Emoji 和正常空格。替换文件系统不允许的字符，移除控制字符并清理尾随空格及句点；空标题回退到视频 ID。仅截短标题，确保完整文件名不超过 240 个 UTF-8 字节。
- `<id>`：来源网站的视频 ID，始终放在方括号内，便于搜索和识别已有文件。
- `<tag>`：视频和音频合并时为 `<height>p_<vcodec>_<acodec>`，例如 `1080p_vp9_opus`、`1080p_h264_aac` 或 `2160p_av01_opus`；纯视频为 `<height>p_<vcodec>`；纯音频为 `<abr>kbps_<acodec>`，例如 `128kbps_opus`。
- `<language>`：所选或实际下载的音频语言，例如 `zh-Hans`、`zh-Hant`、`en`；未知语言为 `und`，纯视频不添加语言。
- `<ext>`：由 yt-dlp 选择的封装格式后缀。

编码名称会统一为简短形式，例如 `avc1.640028` → `h264`、`mp4a.40.2` → `aac`、`vp09.00.41.08` → `vp9`、`av01.0.08M.08` → `av01`、`hev1.1.6.*` → `h265`。

质量标签根据 yt-dlp 的 `--print after_move:` 输出，在下载及后处理完成后添加。因此，相同内容通过单视频或播放列表流程下载时，会使用相同的命名规则。

所有下载使用独立的短临时路径，并从 JSON `after_move` 元数据读取原始标题。不同音频语言生成不同文件名；如目标文件已存在，则在视频 ID 前添加 ` (2)`、` (3)` 等序号，避免覆盖。原有文件仍支持已下载检测、浏览和删除。

## 快速开始（Docker）

使用 Docker 镜像 **`ghcr.io/gfccx/yt-dlp-ui:latest`** 运行应用。镜像支持 **Linux AMD64 和 ARM64**，内置 yt-dlp、FFmpeg 和 Web UI，无需在宿主机安装 Go 或 Node.js。

### 使用 Docker run

拉取已发布的镜像并启动容器：

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

打开 [http://localhost:8080](http://localhost:8080)。部署在远程服务器时，将 `localhost` 替换为服务器地址。需要更换宿主机端口时，可将 `-p 8080:8080` 改为 `-p 8081:8080`。

下载文件保存在 `yt-dlp-downloads` 卷中，对应 `/downloads`；代理设置保存在 `yt-dlp-config` 卷中，对应 `/config`。重建或升级容器时继续使用这两个卷，即可保留文件和代理设置。音轨语言偏好保存在浏览器中；任务历史仅存于内存，重启后清空。

如需将下载文件直接保存到宿主机目录，可改用 `-v /path/on/host:/downloads`。挂载的下载目录和配置目录需要允许容器中的 `appuser`（UID 1000）写入。

### 使用 Docker Compose

将以下内容保存为 `compose.yaml`：

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

在该文件所在目录运行：

```bash
docker compose up -d
```

Docker run 与 Compose 任选一种方式管理容器。从上述 Docker run 示例切换到 Compose 时，先执行 `docker stop yt-dlp-ui` 和 `docker rm yt-dlp-ui` 移除旧容器；命名卷会保留，Compose 将继续使用同名卷。

### 更新容器

使用 Compose 时：

```bash
docker compose pull
docker compose up -d
```

使用 Docker run 时：

```bash
docker pull ghcr.io/gfccx/yt-dlp-ui:latest
docker stop yt-dlp-ui
docker rm yt-dlp-ui
```

随后重新执行原来的 `docker run` 命令，继续挂载相同的卷。保留这两个卷即可保留下载文件和代理设置。

## 配置

服务配置通过环境变量提供；网络设置通过 Web UI 管理，保存到 `CONFIG_DIR/network.json`。

| 环境变量 | 默认值 | 说明 |
| ---------------- | ------------- | ------------------------------------------------------------------------ |
| `PORT` | `8080` | 服务监听端口。 |
| `DOWNLOAD_DIR` | `./downloads` | 下载文件保存目录，Docker 中默认为 `/downloads`。 |
| `MAX_CONCURRENT` | `2` | 最大并发下载数，即工作池大小。 |
| `YT_DLP_PATH` | `yt-dlp` | yt-dlp 可执行文件的路径或名称。 |
| `CONFIG_DIR` | `./config` | Web UI 网络设置的持久化目录，Docker 中默认为 `/config`。 |
| `FFMPEG_PATH` | 未设置 | FFmpeg 路径，可以是包含 `ffmpeg` / `ffprobe` 的目录，也可以是完整的可执行文件路径；留空时从 `$PATH` 查找。 |

Docker 镜像已经在 `$PATH` 中安装 FFmpeg。通常只有在 Docker 外运行、且 FFmpeg 安装在非标准位置时，才需要设置 `FFMPEG_PATH`。

### 网络 / 代理设置

打开 **Network / Proxy settings（网络 / 代理设置）**，启用全局代理，选择协议并填写主机和端口。用户名和密码可选。禁用代理即为 **Direct（直连）**，新安装默认直连。

**Save settings（保存设置）** 用于应用并持久化当前配置。**Test proxy（测试代理）** 使用当前表单测试连接，不会保存配置；禁用代理时，该按钮测试直连。**Active（当前生效）** 始终显示已保存的全局路由。

支持的协议：

| 模式 | 说明 |
| ------- | ------------------------------------------ |
| Direct | 直连，不使用代理。 |
| HTTP | 使用 HTTP 代理，可以通过 CONNECT 隧道访问 HTTPS 目标。 |
| HTTPS | 服务与代理之间也使用 TLS；需要 yt-dlp 的 requests 网络处理器。 |
| SOCKS4 | 在本地将目标域名解析为 IPv4；仅支持用户 ID，不支持密码认证。 |
| SOCKS5 | 在本地解析目标域名。 |
| SOCKS5h | 将目标域名交给代理服务器解析。 |

HTTPS 代理需要 `requests>=2.32.2` 和 `urllib3>=2.0.2`，Docker 镜像已包含这些依赖。如果通过 pip 在 Docker 外安装 yt-dlp，请在同一个 Python 环境中安装：

```bash
python3 -m pip install -U yt-dlp 'requests[socks]>=2.32.2' 'urllib3>=2.0.2'
```

代理地址支持 IPv6，URL 中的 IPv6 地址需要加方括号。通过表单输入的认证信息会自动进行 URL 编码，包括 `@`、`:`、`+`、`/`、`#` 和 `%`。API 接收包含明确端口的完整代理 URL，拒绝路径、查询参数、片段、无效主机 / 端口和控制字符。例如：

```text
http://192.168.1.100:7890
https://proxy.example.com:443
socks4://user@192.168.1.100:1080
socks5://192.168.1.100:1080
socks5h://[2001:db8::1]:1080
http://username:password@192.168.1.100:7890
```

**单任务策略**：在获取链接之前，以及单视频、播放列表下载选择器中，都可以展开 **Advanced · Task proxy（高级设置 · 任务代理）**，选择使用全局代理、当前任务直连或当前任务使用自定义代理。该选择用于信息 / 格式提取和任务提交。

每个任务在提交时记录所选策略，并固定当时实际使用的代理 URL。之后修改全局配置，不会改变已经排队或运行中的任务；后续提交的任务使用新的全局配置。

播放列表中的每个视频独立提交并固定代理，因此，如果在较大列表提交过程中修改全局配置，后续条目会使用新配置。任务列表会显示各任务的实际路由，不包含认证信息。

任务历史仍只保存在内存中，持久化网络设置不会保存任务历史。

**连接测试**：后端使用 Go HTTP 客户端访问固定目标 `https://www.youtube.com/robots.txt`，整体超时为 10 秒，正常校验 TLS 证书，且不跟随重定向。API 不提供任意测试目标 URL 参数。

结果包含耗时、可获取时的 HTTP 状态码，以及连接、认证、DNS、TLS、超时或 HTTP 错误分类。代理服务器可能隐藏底层原因，例如远端 DNS 失败可能表现为 HTTP 502 或普通 SOCKS 连接拒绝。

连接测试有独立的并发限制，不占用下载工作池。

这是 **Go 连通性测试**，不能保证 yt-dlp 成功提取某个视频。yt-dlp 可能使用不同的证书存储或网络处理器，目标网站仍可能要求登录，或施加地区、机器人检测限制。获取真实视频链接会验证 yt-dlp 的实际网络路径。

所有 yt-dlp 调用都会显式传入 `--proxy`，直连时传入空值。进程继承的代理 / 绕过代理环境变量和外部 yt-dlp 配置文件不会覆盖所选路由（使用 `--ignore-config`）。下载命令通过子进程参数构造，不使用 shell 拼接。

代理下载优先使用 yt-dlp 原生 HTTP / HLS / DASH 下载器；FFmpeg 仍可正常合并本地文件。

直播 HLS 和部分旧协议需要 FFmpeg 自行联网，而 FFmpeg 不支持 SOCKS 代理，HTTPS 代理支持也依赖具体构建，因此无法保证这些路径使用 SOCKS / HTTPS 代理。

缩略图由用户浏览器直接加载，不经过后端代理；本项目没有通过其他后端客户端单独下载字幕或缩略图。

**Docker 中访问宿主机代理**：容器中的 `127.0.0.1` 和 `localhost` 指向容器自身。在 Docker Desktop 中可使用 `host.docker.internal`；Linux 下可添加 `--add-host=host.docker.internal:host-gateway`。宿主机代理需要监听容器可访问的网络接口，仅监听宿主机回环地址的代理无法通过桥接网关访问。

网络设置（包括认证信息）保存在权限为 `0600` 的文件中，保存时采用原子替换。

设置接口不会返回已保存的密码；密码框留空会保留密码，选择 **Clear saved password（清除已保存密码）** 可移除密码。修改代理地址、协议或用户名时，需要重新输入已保存的密码。

任务 / SSE 响应不包含代理认证信息，yt-dlp 错误中的认证信息也会脱敏。

与现有下载 / 删除 API 一样，设置接口没有内置身份验证，请部署在可信网络或带有认证的反向代理后方。能够访问配置卷或进程参数的管理员仍可读取代理认证信息。

### 音轨语言与音质

多语言视频分别展示 **音轨语言** 和 **音质**。“自动（默认）”优先使用 yt-dlp 实际选中的默认音轨，其次使用语言偏好与格式排序，不跨语言比较码率。每种语言可选择按 yt-dlp 排序的最高可用音质，或逐条选择具体格式。视频清晰度独立选择；只有一种音轨语言的视频沿用原有选择器。

界面保留 `zh`、`zh-CN`、`zh-TW`、`zh-Hant`、`en`、`en-US` 等原始语言代码。`zh-Hant` 显示为“繁体中文”；元数据明确标记原声时显示为“繁体中文（原声）”。原声、默认、自动配音等标记只依据明确元数据，普通 dubbed 备注不会被推断为人工配音。缺少语言时显示“语言未知”，不根据码率推测。原始备注、编码、采样率、声道数和完整 ID 均可查看，不会将 `251-22` 截断为 `251`，也不会合并同码率的不同语言。HLS 音频格式不再被统一过滤。

顶部 **音轨语言偏好 / Audio language** 面板将首选语言保存到当前浏览器的 localStorage，初始化后续打开的视频或播放列表选择器，每个任务可单独调整。留空使用自动模式。指定代码严格匹配、忽略大小写：`zh` 不会静默替换为 `zh-Hant`，`en-US` 也不会替换为 `en`。

后端在下载工作线程中使用任务固定的代理重新获取元数据，验证语言和完整格式 ID，再传入例如仅音频的 `251-22[language=zh-Hant]`，或合并视频的 `VIDEO_FORMAT_ID+251-22[language=zh-Hant]`；其中视频 ID 应从实际可用的独立视频流中选择。语言条件也能防止解析与下载之间 ID 重新分配后误选其他语言。语言不存在或格式失效会明确报错。与指定音频合并时应选择不含音频的视频流；已有内嵌音频的视频可能导致 yt-dlp 忽略额外音频，界面会阻止这种冲突。

播放列表指定语言支持内置编码预设：每个视频解析真实音轨 ID，允许同语言内回退编码，不回退语言。缺少目标语言的条目标记失败，其余队列继续；严格语言的视频预设需要独立视频流与音频流。自定义任意格式表达式请将语言留空，保留 yt-dlp 原有行为。失败或取消的任务可点击 **Retry**，保留语言、格式 / 预设及原代理快照；已解析的播放列表音轨 ID 也会固定保留。

明确选择的多语言音轨会通过 FFmpeg 修正音频语言标签：部分 YouTube 中文 Opus 源文件实际标为英语。输出使用 ISO 639 标签，例如中文 `zho`、英语 `eng`，并在容器支持时用音轨标题保留 `zh-Hant`、`en-US` 等完整代码。因此，多语言音频单独下载也需要 FFmpeg，Docker 已包含；普通单语言下载保留原有行为。

实现保留 yt-dlp 默认 YouTube player client，不无条件启用 `youtube:player_client=all`。额外 client 会增加请求，可能要求认证或 PO Token，也可能返回不可下载格式。UI 无法补造提取器没有返回的音轨。缺失音轨时请更新 yt-dlp，并查看官方 [YouTube 参数](https://github.com/yt-dlp/yt-dlp#youtube)、[PO Token 指南](https://github.com/yt-dlp/yt-dlp/wiki/PO-Token-Guide)和 [JavaScript 运行时配置](https://github.com/yt-dlp/yt-dlp/wiki/EJS)。

### 何时需要 FFmpeg

FFmpeg 常用于合并独立的视频和音频流：

- **需要合并**：同时选择视频与音频流，例如 `248+251`。播放列表中的视频 / 音频组合预设也可能触发合并。YouTube 的高分辨率视频通常以独立流提供。
- **通常不需要 FFmpeg**：直接下载已包含音视频的单一格式，例如 YouTube 的格式 18；下载纯视频；将纯音频保存为原生封装，例如 Opus → `.webm`、AAC → `.m4a`。

此外，直播 HLS 和部分旧协议可能需要 FFmpeg 执行网络下载，具体代理限制见上一节。如果只下载不需要合并或 FFmpeg 网络下载的格式，可以不安装 FFmpeg；需要 FFmpeg 的任务会由 yt-dlp 报错。通常建议安装，Docker 镜像已自动包含。

## 播放列表下载预设

链接解析为播放列表后，可选择以下编码偏好。各预设对应的 yt-dlp `-f` 表达式会原样传给后端：

| 预设 | 格式表达式 |
| ----------------------- | ------------------------------------------------ |
| VP9 + Opus（适合 YouTube） | `bv[vcodec^=vp9]+ba[acodec^=opus]/bv*+ba*/b` |
| AV1 + Opus（较新编码，文件通常更小） | `bv[vcodec^=av01]+ba[acodec^=opus]/bv*+ba*/b` |
| H.264 + AAC（MP4 兼容性） | `bv[vcodec^=avc1]+ba[acodec^=mp4a]/b[ext=mp4]/b` |
| 最佳可用格式（不限编码） | `bv*+ba*/b` |
| 纯音频 — Opus | `ba*[acodec^=opus]/ba*/b` |
| 纯音频 — AAC / M4A | `ba*[ext=m4a]/ba*/b` |

也可以在高级设置中填写自定义 yt-dlp 格式表达式，覆盖所选预设。

## 开发

### 环境要求

- Go 1.25+；工具链会根据 `go.mod` 选择所需版本。
- Node.js 22+。
- `$PATH` 中可找到 `yt-dlp`，或设置 `YT_DLP_PATH`。
- `$PATH` 中可找到 `ffmpeg`，或设置 `FFMPEG_PATH`；需要合并独立音视频或执行相应网络下载时使用。

### 本地运行

如果尚未克隆仓库：

```bash
git clone https://github.com/Gfccx/yt-dlp-ui.git
cd yt-dlp-ui
```

以开发模式启动 Go 后端，前端由 Vite 提供：

```bash
go run -tags dev ./cmd/yt-dlp-ui
```

在另一个终端启动前端：

```bash
cd web
npm install
npm run dev
```

打开 [http://localhost:5173](http://localhost:5173)。Vite 会将 `/api` 和 `/files` 请求代理到本地 8080 端口的 Go 后端。

### 构建

先构建前端，再构建 Go 二进制：

```bash
cd web && npm ci && npm run build && cd ..
go build -trimpath -ldflags="-s -w" -o yt-dlp-ui ./cmd/yt-dlp-ui
```

或者在仓库根目录构建 Docker 镜像：

```bash
docker build -t yt-dlp-ui .
```

## 架构

项目使用一个 Go 后端和一个 React（TypeScript + Tailwind）前端，没有数据库，也没有独立于 Go 进程的后台工作服务。

```text
cmd/yt-dlp-ui/           程序入口：配置、任务管理器、Gin 路由、优雅退出
internal/config/         环境变量解析
internal/network/        代理校验、原子配置存储、Go 连通性测试
internal/api/            Gin API 处理器与 SSE 事件流
internal/downloader/     yt-dlp 封装、下载任务管理、文件命名规则
web/                     React SPA，通过 //go:embed 嵌入 Go 二进制
```

主要约定：

- **统一文件命名流程**：单视频和播放列表都通过 `ytdlp.Download` → `buildQualityTag` → `renameWithQualityTag` 生成文件名，使用同一份 yt-dlp 下载后元数据。命名问题应修复公共逻辑，不按下载入口分支处理。
- **`format_id` 是不透明字符串**：前端可以构造具体格式组合，如 `248+251`，也可以传入格式表达式，如 `bv[vcodec^=vp9]+ba[acodec^=opus]/…`。后端直接传给 `yt-dlp -f`，不自行解析。
- **通过事件流更新进度**：`/api/events` 使用 SSE；前端通过 `Map<id, Job>` 维护任务状态。新订阅者连接后，会先收到当前任务的 `job:init` 事件。
- **任务历史仅存于内存**：任务完成后保留约 1 小时，再由后台清理循环移除；重启会清除任务历史。下载文件与网络配置保存在磁盘上，可通过 Docker 卷持久化。
