export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '—'
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let value = bytes / 1024
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value < 10 ? value.toFixed(1) : Math.round(value)} ${units[unit]}`
}

export function formatRelative(date: Date | string, now: Date = new Date()): string {
  const d = typeof date === 'string' ? new Date(date) : date
  const secs = Math.round((now.getTime() - d.getTime()) / 1000)
  if (secs < 10) return 'just now'
  if (secs < 60) return `${secs}s ago`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  if (days < 7) return `${days}d ago`
  return d.toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
    year: d.getFullYear() === now.getFullYear() ? undefined : 'numeric',
  })
}

export function formatDateTime(date: Date | string): string {
  const d = typeof date === 'string' ? new Date(date) : date
  return d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'medium' })
}

export function formatClock(date: Date | string): string {
  const d = typeof date === 'string' ? new Date(date) : date
  const pad = (n: number, w = 2) => String(n).padStart(w, '0')
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(), 3)}`
}

export function formatDuration(ms: number): string {
  if (ms < 1) return `${Math.round(ms * 1000)}µs`
  if (ms < 1000) return `${ms < 10 ? ms.toFixed(1) : Math.round(ms)}ms`
  return `${(ms / 1000).toFixed(2)}s`
}

export type FileKind = 'image' | 'video' | 'audio' | 'pdf' | 'text' | 'code' | 'archive' | 'other'

const extKinds: Record<string, FileKind> = {}
for (const [kind, exts] of Object.entries({
  image: 'png jpg jpeg gif webp avif svg ico bmp',
  video: 'mp4 webm mov m4v ogv',
  audio: 'mp3 wav ogg m4a flac aac',
  pdf: 'pdf',
  text: 'txt md csv tsv log',
  code: 'json js ts tsx jsx php html css xml yml yaml sql sh env ini toml go py rb blade',
  archive: 'zip gz tgz tar rar 7z',
})) {
  for (const ext of exts.split(' ')) extKinds[ext] = kind as FileKind
}

export function extension(name: string): string {
  const dot = name.lastIndexOf('.')
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : ''
}

/** Classifies a file for icons and previews, preferring the extension. */
export function fileKind(name: string, contentType = ''): FileKind {
  const byExt = extKinds[extension(name)]
  if (byExt) return byExt
  const [type, sub = ''] = contentType.split(';')[0].trim().split('/')
  if (type === 'image' || type === 'video' || type === 'audio') return type
  if (sub === 'pdf') return 'pdf'
  if (type === 'text') return 'text'
  if (sub === 'json' || sub.endsWith('+json') || sub === 'xml' || sub === 'javascript') return 'code'
  if (sub === 'zip' || sub === 'gzip' || sub === 'x-tar') return 'archive'
  return 'other'
}

export function isTextual(kind: FileKind): boolean {
  return kind === 'text' || kind === 'code'
}

/** Turns a user agent into a short client label for the request log. */
export function shortUserAgent(ua: string): string {
  if (!ua) return '—'
  const known: [RegExp, string][] = [
    [/aws-sdk-php\/(\S+)/, 'aws-sdk-php'],
    [/aws-cli\/(\S+)/, 'aws-cli'],
    [/aws-sdk-go-v2\/(\S+)/, 'aws-sdk-go-v2'],
    [/aws-sdk-js\/(\S+)/, 'aws-sdk-js'],
    [/Boto3\/(\S+)/, 'boto3'],
    [/pail-selftest/, 'self-test'],
    [/curl\/(\S+)/, 'curl'],
  ]
  for (const [re, label] of known) {
    const m = ua.match(re)
    if (m) return m[1] ? `${label} ${m[1]}` : label
  }
  if (/Mozilla\//.test(ua)) return 'browser'
  return ua.split(/\s/)[0].slice(0, 32)
}

/** Splits "a/b/c/" into breadcrumb segments with their prefixes. */
export function breadcrumbs(prefix: string): { name: string; prefix: string }[] {
  const out: { name: string; prefix: string }[] = []
  let acc = ''
  for (const part of prefix.split('/')) {
    if (!part) continue
    acc += part + '/'
    out.push({ name: part, prefix: acc })
  }
  return out
}

export function parentPrefix(prefix: string): string {
  const trimmed = prefix.replace(/\/$/, '')
  const i = trimmed.lastIndexOf('/')
  return i < 0 ? '' : trimmed.slice(0, i + 1)
}

export function baseName(key: string): string {
  const trimmed = key.replace(/\/$/, '')
  return trimmed.slice(trimmed.lastIndexOf('/') + 1)
}

export function validBucketName(name: string): string | null {
  if (name.length < 3 || name.length > 63) return 'Use 3–63 characters'
  if (!/^[a-z0-9.-]+$/.test(name)) return 'Only lowercase letters, numbers, dots and hyphens'
  if (!/^[a-z0-9]/.test(name) || !/[a-z0-9]$/.test(name)) return 'Start and end with a letter or number'
  if (name.includes('..')) return 'No consecutive dots'
  if (/^\d+\.\d+\.\d+\.\d+$/.test(name)) return 'Must not look like an IP address'
  return null
}
