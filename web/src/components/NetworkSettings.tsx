import { useEffect, useState } from "react";
import type { NetworkSettings as Settings, ProxyTestResult } from "../types";
import { getNetworkSettings, saveNetworkSettings, testNetwork } from "../lib/api";
import { ProxyFields } from "./ProxyFields";
import { emptyProxy, parseProxy, proxyURL } from "../lib/proxy";

export function NetworkSettings() {
  const [saved, setSaved] = useState<Settings | null>(null);
  const [draft, setDraft] = useState({ ...emptyProxy });
  const [enabled, setEnabled] = useState(false);
  const [clearPassword, setClearPassword] = useState(false);
  const [busy, setBusy] = useState<"save" | "test" | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [result, setResult] = useState<ProxyTestResult | null>(null);

  useEffect(() => {
    let cancelled = false;
    getNetworkSettings().then((settings) => {
      if (cancelled) return;
      setSaved(settings); setEnabled(settings.enabled); setDraft(parseProxy(settings.url));
    }).catch((err: Error) => { if (!cancelled) setError(err.message); });
    return () => { cancelled = true; };
  }, []);

  const changed = () => { setNotice(""); setResult(null); setError(""); };
  const run = async (action: "save" | "test") => {
    setBusy(action); setError(""); setNotice(""); setResult(null);
    try {
      const update = { enabled, url: proxyURL(draft), keep_password: Boolean(saved?.password_set && !clearPassword && !draft.password) };
      if (action === "test") {
        setResult(await testNetwork(update));
      } else {
        const settings = await saveNetworkSettings(update);
        setSaved(settings); setDraft(parseProxy(settings.url)); setClearPassword(false);
        setNotice("Settings saved. Existing queued and running jobs keep their route.");
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Network request failed");
    } finally { setBusy(null); }
  };

  return (
    <details className="rounded-xl border border-zinc-800 bg-zinc-900 p-4">
      <summary className="cursor-pointer text-sm font-medium text-zinc-300">Network / Proxy settings
        <span className="mt-1 block break-all text-xs font-normal text-zinc-500">Active: {saved?.effective ?? "Loading…"}</span>
      </summary>
      <div className="mt-4 space-y-4">
        <label className="flex items-center gap-2 text-sm text-zinc-300">
          <input type="checkbox" checked={enabled} disabled={!saved || !!busy} onChange={(e) => { setEnabled(e.target.checked); changed(); }} className="accent-zinc-200" /> Enable global proxy
        </label>
        <p className="text-xs text-zinc-500">Disabled means Direct. Save to apply changes; testing uses the form without saving.</p>
        <ProxyFields value={draft} disabled={!saved || !!busy} passwordSet={saved?.password_set && !clearPassword} onChange={(next) => { setDraft(next); changed(); }} />
        {saved?.password_set && <label className="flex items-center gap-2 text-xs text-zinc-400"><input type="checkbox" checked={clearPassword} disabled={!!busy} onChange={(e) => { setClearPassword(e.target.checked); changed(); }} /> Clear saved password</label>}
        <div className="flex flex-wrap gap-3">
          <button type="button" onClick={() => run("save")} disabled={!saved || !!busy} className="rounded-lg bg-zinc-100 px-4 py-2 text-sm font-medium text-zinc-900 disabled:opacity-50">{busy === "save" ? "Saving…" : "Save settings"}</button>
          <button type="button" onClick={() => run("test")} disabled={!saved || !!busy} className="rounded-lg border border-zinc-700 px-4 py-2 text-sm text-zinc-300 disabled:opacity-50">{busy === "test" ? "Testing…" : enabled ? "Test proxy" : "Test direct connection"}</button>
        </div>
        <p className="text-xs text-zinc-500">Go HTTPS connectivity check · youtube.com/robots.txt · 10s timeout · no redirects. A successful check does not guarantee yt-dlp site extraction or region access. HTTPS uses TLS to the proxy; SOCKS4 supports a user ID only.</p>
        <p className="text-xs text-zinc-500">In Docker, localhost refers to the container. Use a reachable proxy address, such as host.docker.internal on Docker Desktop.</p>
        <p className="text-xs text-zinc-500">HTTPS proxies require a yt-dlp build with requests support (included in Docker). FFmpeg live/legacy downloads may not support SOCKS or HTTPS proxies.</p>
        {notice && <p role="status" className="text-xs text-emerald-400">{notice}</p>}
        {error && <p role="alert" className="text-sm text-red-400">{error}</p>}
        {result && <div role="status" className={`space-y-1 rounded-lg border border-zinc-800 p-3 text-xs ${result.ok ? "text-emerald-400" : "text-red-400"}`}>
          <p>{result.ok ? "Connected" : result.category} · {result.elapsed_ms} ms{result.status ? ` · HTTP ${result.status}` : ""}</p>
          <p>{result.message}</p><p className="break-all text-zinc-500">{result.effective} → {result.target}</p>
        </div>}
      </div>
    </details>
  );
}
