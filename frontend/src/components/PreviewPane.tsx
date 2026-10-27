import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Download, Link2, Pencil, Signature, Trash2, X } from 'lucide-react'
import type { ReactNode } from 'react'
import { toast } from 'sonner'
import { useUI } from '../app/ui'
import { api, keys } from '../lib/api'
import type { ObjectDetail } from '../lib/api'
import { baseName, fileKind, formatBytes, formatDateTime, isTextual } from '../lib/format'
import { Button, IconButton, ObjectIcon, Spinner, Tooltip, cx } from './ui'

function TextPreview({ bucket, objectKey, json }: { bucket: string; objectKey: string; json: boolean }) {
  const text = useQuery({ queryKey: ['text', bucket, objectKey], queryFn: () => api.text(bucket, objectKey) })
  if (text.isPending) return <Spinner className="m-4" />
  if (text.error) return <p className="p-4 text-danger">{text.error.message}</p>
  let body = text.data.text
  if (json && !text.data.truncated) {
    try {
      body = JSON.stringify(JSON.parse(body), null, 2)
    } catch {
      // show as-is
    }
  }
  const lines = body.split('\n')
  return (
    <div className="relative">
      <pre className="max-h-80 overflow-auto p-3 font-mono text-xs leading-5">
        {lines.map((line, i) => (
          <div key={i} className="flex">
            <span className="w-8 shrink-0 pr-3 text-right text-faint select-none">{i + 1}</span>
            <span className="whitespace-pre text-fg">{line || ' '}</span>
          </div>
        ))}
      </pre>
      {text.data.truncated && <p className="border-t border-line px-3 py-2 text-xs text-faint">Showing the first 256 KB.</p>}
    </div>
  )
}

function Preview({ bucket, obj }: { bucket: string; obj: ObjectDetail }) {
  const kind = fileKind(obj.key, obj.contentType)
  const src = api.rawUrl(bucket, obj.key)
  switch (kind) {
    case 'image':
      return (
        <div className="grid min-h-48 place-items-center bg-[repeating-conic-gradient(var(--raised)_0_25%,transparent_0_50%)] bg-[length:16px_16px] p-4">
          <img src={src} alt={obj.name} className="max-h-80 max-w-full rounded object-contain" />
        </div>
      )
    case 'video':
      return <video src={src} controls className="max-h-80 w-full bg-black" />
    case 'audio':
      return (
        <div className="p-4">
          <audio src={src} controls className="w-full" />
        </div>
      )
    case 'pdf':
      return <iframe src={src} title={obj.name} className="h-96 w-full bg-white" />
    default:
      if (isTextual(kind) || obj.size === 0) {
        return obj.size === 0 ? (
          <p className="p-4 text-muted">Empty file</p>
        ) : (
          <TextPreview bucket={bucket} objectKey={obj.key} json={obj.key.endsWith('.json') || obj.contentType.includes('json')} />
        )
      }
      return (
        <div className="flex flex-col items-center gap-2 p-8 text-muted">
          <ObjectIcon name={obj.name} contentType={obj.contentType} className="size-8" />
          <p>No preview for {obj.contentType}</p>
        </div>
      )
  }
}

function Field({ label, children, mono }: { label: string; children: ReactNode; mono?: boolean }) {
  return (
    <div className="grid grid-cols-[96px_minmax(0,1fr)] gap-3 py-1.5">
      <dt className="text-muted">{label}</dt>
      <dd className={cx('min-w-0 break-all text-fg', mono && 'font-mono text-xs leading-5')}>{children}</dd>
    </div>
  )
}

export function PreviewPane({ bucket, objectKey, onClose }: { bucket: string; objectKey: string; onClose: () => void }) {
  const ui = useUI()
  const qc = useQueryClient()
  const detail = useQuery({ queryKey: keys.object(bucket, objectKey), queryFn: () => api.object(bucket, objectKey) })
  const acl = useMutation({
    mutationFn: (value: 'private' | 'public-read') => api.setAcl(bucket, objectKey, value),
    onSuccess: (obj) => {
      qc.setQueryData(keys.object(bucket, objectKey), obj)
      qc.invalidateQueries({ queryKey: keys.listing(bucket) })
    },
  })
  const obj = detail.data
  const isPublic = obj?.acl.startsWith('public') ?? false

  return (
    <aside
      aria-label="Preview"
      className="flex w-[380px] shrink-0 animate-slide-in flex-col border-l border-line bg-panel"
    >
      <header className="flex h-12 shrink-0 items-center gap-2 border-b border-line pr-2 pl-4">
        <ObjectIcon name={objectKey} contentType={obj?.contentType} />
        <h2 className="min-w-0 flex-1 truncate font-medium" title={objectKey}>
          {baseName(objectKey)}
        </h2>
        <Tooltip label="Close" shortcut={['Esc']}>
          <IconButton label="Close preview" onClick={onClose}>
            <X className="size-3.5" />
          </IconButton>
        </Tooltip>
      </header>

      {detail.isPending && <Spinner className="m-4" />}
      {detail.error && <p className="p-4 text-danger">{detail.error.message}</p>}
      {obj && (
        <div className="flex-1 overflow-y-auto">
          <div className="border-b border-line">
            <Preview bucket={bucket} obj={obj} />
          </div>

          <div className="flex flex-wrap gap-1.5 border-b border-line p-3">
            <Button
              size="sm"
              onClick={() => {
                navigator.clipboard?.writeText(obj.url)
                toast.success('URL copied')
              }}
            >
              <Link2 className="size-3" /> Copy URL
            </Button>
            <Button size="sm" onClick={() => ui.openDialog({ type: 'presign', bucket, objectKey })}>
              <Signature className="size-3" /> Presign
            </Button>
            <a
              href={api.rawUrl(bucket, objectKey, true)}
              className="inline-flex h-6 items-center gap-1.5 rounded-md border border-line-strong bg-raised px-2 text-xs font-medium hover:bg-hover"
            >
              <Download className="size-3" /> Download
            </a>
            <Button size="sm" onClick={() => ui.openDialog({ type: 'rename', bucket, from: objectKey })}>
              <Pencil className="size-3" /> Rename
            </Button>
            <Button
              size="sm"
              className="text-danger"
              onClick={() => ui.openDialog({ type: 'delete', bucket, keys: [objectKey], prefixes: [], onDone: onClose })}
            >
              <Trash2 className="size-3" /> Delete
            </Button>
          </div>

          <section className="border-b border-line px-4 py-3">
            <h3 className="mb-1 text-xs font-medium text-faint">Details</h3>
            <dl>
              <Field label="Key" mono>
                {obj.key}
              </Field>
              <Field label="Size">
                {formatBytes(obj.size)} <span className="text-faint">({obj.size.toLocaleString()} bytes)</span>
              </Field>
              <Field label="Type" mono>
                {obj.contentType}
              </Field>
              <Field label="Modified">{formatDateTime(obj.lastModified)}</Field>
              <Field label="ETag" mono>
                {obj.etag}
              </Field>
              <Field label="Visibility">
                <label className="inline-flex cursor-pointer items-center gap-2">
                  <button
                    type="button"
                    role="switch"
                    aria-checked={isPublic}
                    aria-label="Public"
                    disabled={acl.isPending}
                    onClick={() => acl.mutate(isPublic ? 'private' : 'public-read')}
                    className={cx(
                      'relative h-4 w-7 cursor-pointer rounded-full transition-colors',
                      isPublic ? 'bg-accent' : 'bg-active',
                    )}
                  >
                    <span
                      className={cx(
                        'absolute top-0.5 left-0.5 size-3 rounded-full bg-white shadow transition-transform',
                        isPublic && 'translate-x-3',
                      )}
                    />
                  </button>
                  {isPublic ? 'Public' : 'Private'}
                </label>
              </Field>
            </dl>
          </section>

          {Object.keys(obj.metadata).length > 0 && (
            <section className="border-b border-line px-4 py-3">
              <h3 className="mb-1 text-xs font-medium text-faint">Metadata</h3>
              <dl>
                {Object.entries(obj.metadata).map(([k, v]) => (
                  <Field key={k} label={k} mono>
                    {v}
                  </Field>
                ))}
              </dl>
            </section>
          )}
          {Object.keys(obj.headers).length > 0 && (
            <section className="px-4 py-3">
              <h3 className="mb-1 text-xs font-medium text-faint">Headers</h3>
              <dl>
                {Object.entries(obj.headers).map(([k, v]) => (
                  <Field key={k} label={k} mono>
                    {v}
                  </Field>
                ))}
              </dl>
            </section>
          )}
        </div>
      )}
    </aside>
  )
}
