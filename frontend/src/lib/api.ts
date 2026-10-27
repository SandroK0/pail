export type Bucket = {
  name: string
  createdAt: string
  objectCount: number
  size: number
}

export type ObjectSummary = {
  key: string
  name: string
  size: number
  etag: string
  contentType: string
  lastModified: string
  acl: string
  url: string
}

export type ObjectDetail = ObjectSummary & {
  metadata: Record<string, string>
  headers: Record<string, string>
}

export type Folder = { prefix: string; name: string }

export type Listing = {
  bucket: string
  prefix: string
  folders: Folder[]
  objects: ObjectSummary[]
  nextCursor?: string
}

export type Info = {
  endpoint: string
  region: string
  accessKey: string
  secretKey: string
  verifyAuth: boolean
  defaultBucket: string
}

export type SelftestStep = {
  name: string
  ok: boolean
  status: number
  durationMs: number
  error?: string
}

export type RequestEvent = {
  id: number
  time: string
  op: string
  method: string
  path: string
  bucket?: string
  key?: string
  status: number
  errorCode?: string
  errorMessage?: string
  bytesIn: number
  bytesOut: number
  durationMs: number
  accessKey?: string
  presigned?: boolean
  userAgent?: string
}

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) {
    let message = `Request failed (${res.status})`
    try {
      message = (await res.json()).error ?? message
    } catch {
      // not JSON
    }
    throw new ApiError(res.status, message)
  }
  if (res.status === 204) return undefined as T
  return res.json()
}

const b = (bucket: string) => `/_api/buckets/${encodeURIComponent(bucket)}`
const k = (key: string) => `key=${encodeURIComponent(key)}`

export const api = {
  info: () => request<Info>('GET', '/_api/info'),
  buckets: () => request<Bucket[]>('GET', '/_api/buckets'),
  createBucket: (name: string) => request<Bucket>('POST', '/_api/buckets', { name }),
  deleteBucket: (bucket: string, force = false) =>
    request<void>('DELETE', `${b(bucket)}${force ? '?force=true' : ''}`),

  list: (bucket: string, prefix: string, cursor?: string) => {
    const q = new URLSearchParams({ prefix })
    if (cursor) q.set('cursor', cursor)
    return request<Listing>('GET', `${b(bucket)}/objects?${q}`)
  },
  search: (bucket: string, q: string) =>
    request<ObjectSummary[]>('GET', `${b(bucket)}/search?q=${encodeURIComponent(q)}`),
  object: (bucket: string, key: string) => request<ObjectDetail>('GET', `${b(bucket)}/object?${k(key)}`),
  setAcl: (bucket: string, key: string, acl: 'private' | 'public-read') =>
    request<ObjectDetail>('PATCH', `${b(bucket)}/object?${k(key)}`, { acl }),
  remove: (bucket: string, keys: string[], prefixes: string[] = []) =>
    request<{ deleted: number }>('POST', `${b(bucket)}/delete`, { keys, prefixes }),
  createFolder: (bucket: string, prefix: string) => request<Folder>('POST', `${b(bucket)}/folders`, { prefix }),
  move: (bucket: string, from: string, to: string) =>
    request<{ moved: number }>('POST', `${b(bucket)}/move`, { from, to }),
  presign: (bucket: string, key: string, method: 'GET' | 'PUT', expiresIn: number) =>
    request<{ url: string; expiresAt: string }>('POST', `${b(bucket)}/presign`, { key, method, expiresIn }),
  selftest: (bucket: string) => request<SelftestStep[]>('POST', '/_api/selftest', { bucket }),

  rawUrl: (bucket: string, key: string, download = false) =>
    `${b(bucket)}/raw?${k(key)}${download ? '&download=1' : ''}`,

  /** Fetches up to `limit` bytes of an object as text, for previews. */
  async text(bucket: string, key: string, limit = 256 * 1024): Promise<{ text: string; truncated: boolean }> {
    const res = await fetch(api.rawUrl(bucket, key), { headers: { Range: `bytes=0-${limit - 1}` } })
    if (!res.ok) throw new ApiError(res.status, `Could not load file (${res.status})`)
    const text = await res.text()
    const total = Number(res.headers.get('Content-Range')?.split('/')[1] ?? text.length)
    return { text, truncated: res.status === 206 && total > limit }
  },

  /** Uploads a file with progress, using XHR because fetch has no upload progress. */
  upload(
    bucket: string,
    key: string,
    file: Blob,
    onProgress?: (loaded: number, total: number) => void,
    signal?: AbortSignal,
  ): Promise<ObjectDetail> {
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest()
      xhr.open('PUT', `${b(bucket)}/object?${k(key)}`)
      if (file.type) xhr.setRequestHeader('Content-Type', file.type)
      xhr.upload.onprogress = (e) => onProgress?.(e.loaded, e.total)
      xhr.onload = () => {
        let body: { error?: string } & Partial<ObjectDetail> = {}
        try {
          body = JSON.parse(xhr.responseText)
        } catch {
          // not JSON
        }
        if (xhr.status >= 200 && xhr.status < 300) resolve(body as ObjectDetail)
        else reject(new ApiError(xhr.status, body.error ?? `Upload failed (${xhr.status})`))
      }
      xhr.onerror = () => reject(new ApiError(0, 'Network error'))
      xhr.onabort = () => reject(new ApiError(0, 'Cancelled'))
      signal?.addEventListener('abort', () => xhr.abort())
      xhr.send(file)
    })
  },
}

/** Query keys, kept in one place so invalidation stays consistent. */
export const keys = {
  info: ['info'] as const,
  buckets: ['buckets'] as const,
  listing: (bucket: string, prefix?: string) =>
    prefix === undefined ? (['listing', bucket] as const) : (['listing', bucket, prefix] as const),
  object: (bucket: string, key: string) => ['object', bucket, key] as const,
  search: (bucket: string, q: string) => ['search', bucket, q] as const,
}
