import { useState } from "react";
import type { ProxyPolicy } from "../types";
import { ProxyFields } from "./ProxyFields";
import { parseProxy, proxyURL } from "../lib/proxy";

export function TaskProxy({ value, onChange, disabled = false }: {
  value: ProxyPolicy;
  onChange: (value: ProxyPolicy) => void;
  disabled?: boolean;
}) {
  const [draft, setDraft] = useState(() => parseProxy(value.url));
  return (
    <details className="my-3 rounded-lg border border-zinc-800 px-3 py-2 text-sm">
      <summary className="cursor-pointer text-zinc-400">Advanced · Task proxy: {value.mode === "global" ? "Use global proxy" : value.mode === "direct" ? "Direct" : "Custom"}</summary>
      <div className="mt-3 space-y-3">
        <label className="block space-y-1 text-xs text-zinc-400">Task network route
          <select aria-label="Task network route" value={value.mode} disabled={disabled} onChange={(e) => {
            const mode = e.target.value as ProxyPolicy["mode"];
            onChange(mode === "custom" ? { mode, url: proxyURL(draft) } : { mode });
          }} className="w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm">
            <option value="global">Use global proxy (default)</option><option value="direct">Direct for this task</option><option value="custom">Custom proxy for this task</option>
          </select>
        </label>
        {value.mode === "custom" && <ProxyFields value={draft} disabled={disabled} onChange={(next) => { setDraft(next); onChange({ mode: "custom", url: proxyURL(next) }); }} />}
        <p className="text-xs text-zinc-500">Applies to information fetching and downloads. Each job keeps the route selected when queued, even if global settings change.</p>
      </div>
    </details>
  );
}
