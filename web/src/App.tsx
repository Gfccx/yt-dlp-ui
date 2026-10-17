import { useState } from "react";
import type { ResolveResult, ProxyPolicy } from "./types";
import { useSSE } from "./hooks/useSSE";
import { UrlInput } from "./components/UrlInput";
import { FormatPicker } from "./components/FormatPicker";
import { PlaylistPicker } from "./components/PlaylistPicker";
import { DownloadList } from "./components/DownloadList";
import { FileList } from "./components/FileList";
import { NetworkSettings } from "./components/NetworkSettings";
import { AudioPreferences } from "./components/AudioPreferences";
import { readAudioPreference, saveAudioPreference } from "./lib/audio";

function App() {
  const { jobs, connected } = useSSE();
  const [resolved, setResolved] = useState<ResolveResult | null>(null);
  const [resolvedProxy, setResolvedProxy] = useState<ProxyPolicy>({ mode: "global" });
  const [resolveVersion, setResolveVersion] = useState(0);
  const [preferredLanguage, setPreferredLanguage] = useState(readAudioPreference);

  const jobList = Array.from(jobs.values()).sort(
    (a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime()
  );

  const hasActiveJobs = jobList.some(
    (j) => j.status === "downloading" || j.status === "processing"
  );

  return (
    <div className="min-h-screen bg-zinc-950 text-zinc-100">
      <div className="mx-auto max-w-4xl px-4 py-8">
        <header className="mb-8">
          <h1 className="text-3xl font-bold tracking-tight">yt-dlp-ui</h1>
          <p className="mt-1 text-sm text-zinc-500">
            Download video and audio files
            <span
              className={`ml-2 inline-block h-2 w-2 rounded-full ${
                connected ? "bg-emerald-500" : "bg-red-500"
              }`}
              title={connected ? "Connected" : "Disconnected"}
            />
          </p>
        </header>

        <main className="space-y-6">
          <NetworkSettings />
          <AudioPreferences value={preferredLanguage} onChange={value => { setPreferredLanguage(value); saveAudioPreference(value); }} />
          <UrlInput onResult={(result, proxy) => { setResolved(result); setResolvedProxy(proxy); setResolveVersion((v) => v + 1); }} />

          {resolved?.type === "video" && (
            <FormatPicker
              key={resolveVersion}
              videoInfo={resolved.video}
              initialProxy={resolvedProxy}
              preferredLanguage={preferredLanguage}
              onClose={() => setResolved(null)}
            />
          )}

          {resolved?.type === "playlist" && (
            <PlaylistPicker
              key={resolveVersion}
              playlist={resolved.playlist}
              initialProxy={resolvedProxy}
              preferredLanguage={preferredLanguage}
              onClose={() => setResolved(null)}
            />
          )}

          {jobList.length > 0 && <DownloadList jobs={jobList} />}

          <FileList refreshTrigger={hasActiveJobs} />
        </main>
      </div>
    </div>
  );
}

export default App;
