export interface ProxyDraft {
  scheme: string;
  host: string;
  port: string;
  username: string;
  password: string;
}

export const emptyProxy: ProxyDraft = { scheme: "http", host: "", port: "", username: "", password: "" };

export function parseProxy(raw = ""): ProxyDraft {
  try {
    const u = new URL(raw);
    return { scheme: u.protocol.slice(0, -1), host: u.hostname.replace(/^\[|\]$/g, ""), port: u.port || (u.protocol === "http:" ? "80" : u.protocol === "https:" ? "443" : ""), username: decodeURIComponent(u.username), password: decodeURIComponent(u.password) };
  } catch {
    return { ...emptyProxy };
  }
}

export function proxyURL(draft: ProxyDraft): string {
  if (!draft.host && !draft.port && !draft.username && !draft.password) return "";
  const host = draft.host.includes(":") && !draft.host.startsWith("[") ? `[${draft.host}]` : draft.host;
  // Encode credentials separately; punctuation and IPv6 cannot alter URL parts.
  const encode = (s: string) => encodeURIComponent(s).replace(/[!'()*]/g, (c) => `%${c.charCodeAt(0).toString(16).toUpperCase()}`);
  const auth = draft.username || draft.password
    ? `${encode(draft.username)}${draft.password ? `:${encode(draft.password)}` : ""}@` : "";
  return `${draft.scheme}://${auth}${host}:${draft.port}`;
}
