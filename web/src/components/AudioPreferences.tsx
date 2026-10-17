import { COMMON_LANGUAGES, languageName } from "../lib/audio";

export function AudioPreferences({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  return <details className="rounded-xl border border-zinc-800 bg-zinc-900 p-4">
    <summary className="cursor-pointer text-sm font-medium">音轨语言偏好 / Audio language</summary>
    <label className="mt-3 block text-xs text-zinc-400">首选语言（保存在此浏览器）
      <input aria-label="Preferred audio language" list="audio-language-preferences" value={value} onChange={e => onChange(e.target.value.trim())} placeholder="自动（默认）" className="mt-1 w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-sm text-zinc-200" />
    </label>
    <datalist id="audio-language-preferences">{COMMON_LANGUAGES.map(code => <option key={code} value={code}>{languageName(code)}</option>)}</datalist>
    <p className="mt-2 text-xs text-zinc-500">留空使用 yt-dlp 默认音轨。指定语言代码会严格匹配；不可用时显示错误，不回退到其他语言。可在每个任务中调整。</p>
  </details>;
}
