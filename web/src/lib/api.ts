import type { Job, FileInfo, Format, ResolveResult, ProxyPolicy, NetworkSettings, NetworkUpdate, ProxyTestResult, AudioSelection } from "../types";

async function request<T>(url: string, options?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    headers: { "Content-Type": "application/json" },
    ...options,
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(body.error || res.statusText);
  }
  return res.json();
}

export type ResolveMode = "" | "video" | "playlist";

export async function resolveUrl(
  url: string,
  mode: ResolveMode = "",
  proxy: ProxyPolicy = { mode: "global" }
): Promise<ResolveResult> {
  return request<ResolveResult>("/api/resolve", {
    method: "POST",
    body: JSON.stringify({ url, mode, proxy }),
  });
}

export async function startDownload(
  url: string,
  video: Format | null,
  audio: Format | null,
  title: string,
  proxy: ProxyPolicy = { mode: "global" },
  audioSelection?: AudioSelection
): Promise<Job> {
  if (!video && !audio) {
    throw new Error("Select a video format, an audio format, or both");
  }

  const formatId = [video?.format_id, audio?.format_id]
    .filter((id): id is string => Boolean(id))
    .join("+");

  return request<Job>("/api/downloads", {
    method: "POST",
    body: JSON.stringify({ url, format_id: formatId, title, proxy, audio: audioSelection }),
  });
}

// startPlaylistItemDownload submits a single video from a playlist, using a
// raw yt-dlp format selector (e.g. "bv*[vcodec^=vp9]+ba*[acodec^=opus]/b")
// instead of pre-picked format IDs. The backend fills in the filename
// quality tag post-download from yt-dlp-reported metadata.
export async function startPlaylistItemDownload(
  url: string,
  formatSelector: string,
  title: string,
  playlistTitle: string,
  proxy: ProxyPolicy = { mode: "global" },
  audioLanguage = ""
): Promise<Job> {
  return request<Job>("/api/downloads", {
    method: "POST",
    body: JSON.stringify({
      url,
      format_id: formatSelector,
      title,
      playlist_title: playlistTitle,
      proxy,
      audio: { language: audioLanguage },
    }),
  });
}

export function retryDownload(id: string): Promise<Job> {
  return request<Job>(`/api/downloads/${id}/retry`, { method: "POST" });
}

export function getNetworkSettings(): Promise<NetworkSettings> {
  return request("/api/settings/network");
}

export function saveNetworkSettings(settings: NetworkUpdate): Promise<NetworkSettings> {
  return request("/api/settings/network", { method: "PUT", body: JSON.stringify(settings) });
}

export function testNetwork(settings: NetworkUpdate): Promise<ProxyTestResult> {
  return request("/api/settings/network/test", { method: "POST", body: JSON.stringify(settings) });
}

export async function listDownloads(): Promise<Job[]> {
  return request<Job[]>("/api/downloads");
}

export async function cancelDownload(id: string): Promise<void> {
  await request<unknown>(`/api/downloads/${id}`, { method: "DELETE" });
}

export async function listFiles(): Promise<FileInfo[]> {
  return request<FileInfo[]>("/api/files");
}

export async function deleteFile(name: string): Promise<void> {
  await request<unknown>(`/api/files/${encodeURIComponent(name)}`, { method: "DELETE" });
}
