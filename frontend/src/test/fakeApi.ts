import { vi } from 'vitest'
import type { Info, RequestEvent } from '../lib/api'

type StoredObject = {
  key: string
  body: string
  contentType: string
  acl: string
  lastModified: string
  metadata: Record<string, string>
  headers: Record<string, string>
}

export type Call = { method: string; path: string; search: URLSearchParams; body?: unknown }

const baseInfo: Info = {
  endpoint: 'http://localhost:8080',
  region: 'us-east-1',
  accessKey: 'local',
  secretKey: 'localsecret',
  verifyAuth: false,
  defaultBucket: 'laravel',
  mcp: true,
}

/**
 * Installs an in-memory implementation of the /_api JSON API behind fetch.
 * Seed it with { bucket: { key: body } }.
 */
export function fakeApi(seed: Record<string, Record<string, string>> = { laravel: {} }, info: Partial<Info> = {}) {
  const buckets = new Map<string, Map<string, StoredObject>>()
  const calls: Call[] = []
  const now = new Date('2026-10-27T12:00:00Z').toISOString()

  const put = (bucket: string, key: string, body: string, contentType = 'text/plain') =>
    buckets.get(bucket)!.set(key, { key, body, contentType, acl: 'private', lastModified: now, metadata: {}, headers: {} })
  for (const [bucket, objects] of Object.entries(seed)) {
    buckets.set(bucket, new Map())
    for (const [key, body] of Object.entries(objects)) {
      const ext = key.split('.').pop()
      put(bucket, key, body, ext === 'png' ? 'image/png' : ext === 'json' ? 'application/json' : 'text/plain')
    }
  }

  const summary = (bucket: string, o: StoredObject) => ({
    key: o.key,
    name: o.key.replace(/\/$/, '').split('/').pop(),
    size: o.body.length,
    etag: 'etag-' + o.key,
    contentType: o.contentType,
    lastModified: o.lastModified,
    acl: o.acl,
    url: `${baseInfo.endpoint}/${bucket}/${o.key}`,
  })
  const detail = (bucket: string, o: StoredObject) => ({ ...summary(bucket, o), metadata: o.metadata, headers: o.headers })

  const json = (status: number, data?: unknown) =>
    new Response(data === undefined ? null : JSON.stringify(data), {
      status,
      headers: { 'Content-Type': 'application/json' },
    })
  const notFound = (what = 'Bucket not found') => json(404, { error: what })

  const handle = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = new URL(String(input), 'http://localhost')
    const method = init?.method ?? 'GET'
    const body = typeof init?.body === 'string' ? JSON.parse(init.body) : undefined
    calls.push({ method, path: url.pathname, search: url.searchParams, body })
    const p = url.pathname
    const key = url.searchParams.get('key') ?? ''

    if (p === '/_api/info') return json(200, { ...baseInfo, ...info })
    if (p === '/_api/selftest') {
      if (!buckets.has(body.bucket)) return json(200, [{ name: 'HeadBucket', ok: false, status: 404, durationMs: 1, error: 'HTTP 404 NoSuchBucket' }])
      return json(
        200,
        ['HeadBucket', 'PutObject', 'GetObject', 'DeleteObject'].map((name) => ({ name, ok: true, status: 200, durationMs: 1.5 })),
      )
    }
    if (p === '/_api/buckets') {
      if (method === 'POST') {
        if (buckets.has(body.name)) return json(409, { error: 'A bucket with that name already exists' })
        buckets.set(body.name, new Map())
        return json(201, { name: body.name, createdAt: now, objectCount: 0, size: 0 })
      }
      return json(
        200,
        [...buckets.entries()]
          .sort(([a], [b]) => a.localeCompare(b))
          .map(([name, objs]) => ({
            name,
            createdAt: now,
            objectCount: objs.size,
            size: [...objs.values()].reduce((n, o) => n + o.body.length, 0),
          })),
      )
    }

    const m = p.match(/^\/_api\/buckets\/([^/]+)(?:\/(.+))?$/)
    if (!m) return json(404, { error: 'not found' })
    const bucket = decodeURIComponent(m[1])
    const objs = buckets.get(bucket)
    if (!objs) return notFound()

    switch (`${method} ${m[2] ?? ''}`) {
      case 'DELETE ':
        if (objs.size && url.searchParams.get('force') !== 'true') return json(409, { error: 'Bucket is not empty' })
        buckets.delete(bucket)
        return json(204)
      case 'GET objects': {
        const prefix = url.searchParams.get('prefix') ?? ''
        const folders = new Set<string>()
        const objects = []
        for (const o of [...objs.values()].sort((a, b) => a.key.localeCompare(b.key))) {
          if (!o.key.startsWith(prefix)) continue
          const rest = o.key.slice(prefix.length)
          const slash = rest.indexOf('/')
          if (slash >= 0) {
            if (rest.length > slash + 1 || slash < rest.length - 1 || o.key !== prefix) folders.add(prefix + rest.slice(0, slash + 1))
            continue
          }
          if (o.key !== prefix) objects.push(summary(bucket, o))
        }
        return json(200, {
          bucket,
          prefix,
          folders: [...folders].map((f) => ({ prefix: f, name: f.slice(prefix.length, -1) })),
          objects,
        })
      }
      case 'GET search': {
        const q = (url.searchParams.get('q') ?? '').toLowerCase()
        return json(200, [...objs.values()].filter((o) => q && o.key.toLowerCase().includes(q)).map((o) => summary(bucket, o)))
      }
      case 'GET object': {
        const o = objs.get(key)
        return o ? json(200, detail(bucket, o)) : notFound('Object not found')
      }
      case 'PATCH object': {
        const o = objs.get(key)
        if (!o) return notFound('Object not found')
        o.acl = body.acl
        return json(200, detail(bucket, o))
      }
      case 'GET raw': {
        const o = objs.get(key)
        return o ? new Response(o.body, { status: 200, headers: { 'Content-Type': o.contentType } }) : notFound()
      }
      case 'POST delete': {
        let deleted = 0
        for (const k of body.keys) if (objs.delete(k)) deleted++
        for (const prefix of body.prefixes) {
          for (const k of [...objs.keys()]) if (k.startsWith(prefix) && objs.delete(k)) deleted++
        }
        return json(200, { deleted })
      }
      case 'POST folders': {
        const prefix = body.prefix.replace(/^\/+/, '').replace(/\/?$/, '/')
        put(bucket, prefix, '')
        return json(201, { prefix, name: prefix.slice(0, -1).split('/').pop() })
      }
      case 'POST move': {
        const o = objs.get(body.from)
        if (!o) return notFound('Object not found')
        objs.delete(body.from)
        objs.set(body.to, { ...o, key: body.to })
        return json(200, { moved: 1 })
      }
      case 'POST presign':
        if (body.method === 'GET' && !objs.has(body.key)) return notFound('Object not found')
        return json(200, {
          url: `${baseInfo.endpoint}/${bucket}/${body.key}?X-Amz-Signature=abc&X-Amz-Expires=${body.expiresIn}`,
          expiresAt: now,
        })
    }
    return json(404, { error: 'not found' })
  }

  const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(handle)
  return {
    buckets,
    calls,
    fetchMock,
    put,
    called: (method: string, path: string) => calls.filter((c) => c.method === method && c.path === path),
  }
}

/** A controllable stand-in for EventSource. */
export class FakeEventSource {
  static instances: FakeEventSource[] = []
  url: string
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((e: { data: string }) => void) | null = null
  closed = false

  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }
  close() {
    this.closed = true
  }
  static latest(): FakeEventSource {
    return FakeEventSource.instances[FakeEventSource.instances.length - 1]
  }
  open() {
    this.onopen?.()
  }
  emit(e: Partial<RequestEvent>) {
    this.onmessage?.({ data: JSON.stringify(event(e)) })
  }
  fail() {
    this.onerror?.()
  }
}

let nextEventId = 1
export function event(e: Partial<RequestEvent>): RequestEvent {
  return {
    id: nextEventId++,
    time: '2026-10-27T12:00:00.000Z',
    op: 'GetObject',
    method: 'GET',
    path: '/laravel/a.txt',
    bucket: 'laravel',
    key: 'a.txt',
    status: 200,
    bytesIn: 0,
    bytesOut: 10,
    durationMs: 1.2,
    userAgent: 'aws-sdk-php/3.300.0 GuzzleHttp/7',
    ...e,
  }
}
