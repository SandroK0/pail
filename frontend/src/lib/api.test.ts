import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, api } from './api'

function mockFetch(status: number, body: unknown, headers: Record<string, string> = {}) {
  return vi
    .spyOn(globalThis, 'fetch')
    .mockResolvedValue(
      new Response(typeof body === 'string' ? body : JSON.stringify(body), { status, headers }),
    )
}

describe('request helpers', () => {
  it('builds encoded URLs and JSON bodies', async () => {
    const f = mockFetch(200, { bucket: 'b', prefix: 'a b/', folders: [], objects: [] })
    await api.list('my.bucket', 'a b/', 'cursor/1')
    expect(f).toHaveBeenCalledWith('/_api/buckets/my.bucket/objects?prefix=a+b%2F&cursor=cursor%2F1', expect.objectContaining({ method: 'GET' }))

    f.mockResolvedValue(new Response('{}', { status: 200 }))
    await api.setAcl('b', 'dir/x&y.txt', 'public-read')
    const [url, init] = f.mock.calls[1]
    expect(url).toBe('/_api/buckets/b/object?key=dir%2Fx%26y.txt')
    expect(init).toMatchObject({ method: 'PATCH', body: '{"acl":"public-read"}', headers: { 'Content-Type': 'application/json' } })
  })

  it('surfaces the server error message', async () => {
    mockFetch(409, { error: 'Bucket is not empty' })
    const err = await api.deleteBucket('b').catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({ status: 409, message: 'Bucket is not empty' })
  })

  it('falls back to a generic message for non-JSON errors', async () => {
    mockFetch(502, 'Bad Gateway')
    await expect(api.buckets()).rejects.toMatchObject({ status: 502, message: 'Request failed (502)' })
  })

  it('returns undefined for 204', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(null, { status: 204 }))
    await expect(api.deleteBucket('b', true)).resolves.toBeUndefined()
  })

  it('rawUrl', () => {
    expect(api.rawUrl('b', 'a/b c.txt')).toBe('/_api/buckets/b/raw?key=a%2Fb%20c.txt')
    expect(api.rawUrl('b', 'k', true)).toBe('/_api/buckets/b/raw?key=k&download=1')
  })
})

describe('text', () => {
  it('requests a range and reports truncation', async () => {
    const f = mockFetch(206, 'abc', { 'Content-Range': 'bytes 0-2/100' })
    const res = await api.text('b', 'k', 3)
    expect(res).toEqual({ text: 'abc', truncated: true })
    expect(f.mock.calls[0][1]).toEqual({ headers: { Range: 'bytes=0-2' } })
  })

  it('is not truncated when the whole file fits', async () => {
    mockFetch(206, 'abc', { 'Content-Range': 'bytes 0-2/3' })
    expect((await api.text('b', 'k', 3)).truncated).toBe(false)
  })

  it('throws on errors', async () => {
    mockFetch(404, 'nope')
    await expect(api.text('b', 'k')).rejects.toBeInstanceOf(ApiError)
  })
})

class FakeXHR {
  static last: FakeXHR
  method = ''
  url = ''
  headers: Record<string, string> = {}
  status = 0
  responseText = ''
  sent: unknown
  upload: { onprogress?: (e: { loaded: number; total: number }) => void } = {}
  onload?: () => void
  onerror?: () => void
  onabort?: () => void
  constructor() {
    FakeXHR.last = this
  }
  open(method: string, url: string) {
    this.method = method
    this.url = url
  }
  setRequestHeader(k: string, v: string) {
    this.headers[k] = v
  }
  send(body: unknown) {
    this.sent = body
  }
  abort() {
    this.onabort?.()
  }
  respond(status: number, body: unknown) {
    this.status = status
    this.responseText = JSON.stringify(body)
    this.onload?.()
  }
}

describe('upload', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('PUTs the file and reports progress', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXHR)
    const progress = vi.fn()
    const file = new File(['hello'], 'a.txt', { type: 'text/plain' })
    const p = api.upload('b', 'dir/a.txt', file, progress)
    const xhr = FakeXHR.last
    expect(xhr.method).toBe('PUT')
    expect(xhr.url).toBe('/_api/buckets/b/object?key=dir%2Fa.txt')
    expect(xhr.headers['Content-Type']).toBe('text/plain')
    expect(xhr.sent).toBe(file)
    xhr.upload.onprogress?.({ loaded: 2, total: 5 })
    expect(progress).toHaveBeenCalledWith(2, 5)
    xhr.respond(201, { key: 'dir/a.txt' })
    await expect(p).resolves.toMatchObject({ key: 'dir/a.txt' })
  })

  it('rejects with the server message', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXHR)
    const p = api.upload('b', 'k', new Blob(['x']))
    expect(FakeXHR.last.headers['Content-Type']).toBeUndefined()
    FakeXHR.last.respond(404, { error: 'Bucket not found' })
    await expect(p).rejects.toMatchObject({ status: 404, message: 'Bucket not found' })
  })

  it('aborts via the signal', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXHR)
    const ctrl = new AbortController()
    const p = api.upload('b', 'k', new Blob(['x']), undefined, ctrl.signal)
    ctrl.abort()
    await expect(p).rejects.toMatchObject({ message: 'Cancelled' })
  })

  it('rejects on network errors', async () => {
    vi.stubGlobal('XMLHttpRequest', FakeXHR)
    const p = api.upload('b', 'k', new Blob(['x']))
    FakeXHR.last.onerror?.()
    await expect(p).rejects.toMatchObject({ message: 'Network error' })
  })
})
