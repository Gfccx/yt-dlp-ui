import type { ProxyDraft } from "../lib/proxy";

const inputClass = "w-full min-w-0 rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm outline-none focus:border-zinc-600 disabled:opacity-50";

export function ProxyFields({ value, onChange, passwordSet = false, disabled = false }: {
  value: ProxyDraft;
  onChange: (draft: ProxyDraft) => void;
  passwordSet?: boolean;
  disabled?: boolean;
}) {
  const update = (key: keyof ProxyDraft, text: string) => onChange({ ...value, [key]: text });
  return (
    <fieldset disabled={disabled} className="space-y-3">
      <legend className="sr-only">Proxy connection</legend>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <label className="space-y-1 text-xs text-zinc-400">Protocol
          <select aria-label="Protocol" className={inputClass} value={value.scheme} onChange={(e) => onChange({ ...value, scheme: e.target.value, password: e.target.value === "socks4" ? "" : value.password })}>
            <option value="http">HTTP</option><option value="https">HTTPS (TLS to proxy)</option>
            <option value="socks4">SOCKS4</option><option value="socks5">SOCKS5 (local DNS)</option><option value="socks5h">SOCKS5h (proxy DNS)</option>
          </select>
        </label>
        <label className="space-y-1 text-xs text-zinc-400">Host / IPv6 address
          <input className={inputClass} value={value.host} placeholder="192.168.1.100" autoComplete="off" spellCheck={false} onChange={(e) => update("host", e.target.value)} />
        </label>
        <label className="space-y-1 text-xs text-zinc-400">Port
          <input className={inputClass} value={value.port} placeholder="7890" inputMode="numeric" onChange={(e) => update("port", e.target.value)} />
        </label>
      </div>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <label className="space-y-1 text-xs text-zinc-400">Username (optional)
          <input className={inputClass} value={value.username} autoComplete="off" onChange={(e) => update("username", e.target.value)} />
        </label>
        <label className="space-y-1 text-xs text-zinc-400">Password (optional)
          <input type="password" className={inputClass} value={value.password} autoComplete="new-password" disabled={value.scheme === "socks4"} placeholder={value.scheme === "socks4" ? "SOCKS4 supports a user ID only" : passwordSet ? "Saved password — leave blank to keep" : ""} onChange={(e) => update("password", e.target.value)} />
        </label>
      </div>
    </fieldset>
  );
}
