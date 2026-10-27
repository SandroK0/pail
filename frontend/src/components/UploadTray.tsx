import { CircleCheck, CircleX, ChevronDown, ChevronUp, X } from 'lucide-react'
import { useState } from 'react'
import { useUploads } from '../app/uploads'
import { baseName, formatBytes } from '../lib/format'
import { Button, IconButton, ObjectIcon, Spinner, cx } from './ui'

export function UploadTray() {
  const { uploads, cancel, clearFinished } = useUploads()
  const [collapsed, setCollapsed] = useState(false)
  if (uploads.length === 0) return null

  const active = uploads.filter((u) => u.status === 'queued' || u.status === 'uploading')
  const failed = uploads.filter((u) => u.status === 'error').length
  const total = uploads.reduce((n, u) => n + u.size, 0)
  const loaded = uploads.reduce((n, u) => n + (u.status === 'done' ? u.size : u.loaded), 0)
  const percent = total ? Math.round((loaded / total) * 100) : 100

  const title = active.length
    ? `Uploading ${active.length} ${active.length === 1 ? 'file' : 'files'} · ${percent}%`
    : failed
      ? `${failed} ${failed === 1 ? 'upload' : 'uploads'} failed`
      : 'Uploads complete'

  return (
    <section
      aria-label="Uploads"
      className="fixed right-4 bottom-4 z-30 w-80 animate-pop-in overflow-hidden rounded-xl border border-line bg-panel shadow-pop"
    >
      <header className="flex h-10 items-center gap-2 px-3">
        {active.length ? <Spinner /> : failed ? <CircleX className="size-3.5 text-danger" /> : <CircleCheck className="size-3.5 text-success" />}
        <span className="flex-1 font-medium">{title}</span>
        {!active.length && (
          <Button size="sm" variant="ghost" onClick={clearFinished}>
            Clear
          </Button>
        )}
        <IconButton label={collapsed ? 'Expand uploads' : 'Collapse uploads'} className="size-6" onClick={() => setCollapsed(!collapsed)}>
          {collapsed ? <ChevronUp className="size-3.5" /> : <ChevronDown className="size-3.5" />}
        </IconButton>
      </header>
      {active.length > 0 && (
        <div className="h-0.5 bg-raised">
          <div className="h-full bg-accent transition-[width] duration-200" style={{ width: `${percent}%` }} />
        </div>
      )}
      {!collapsed && (
        <ul className="max-h-64 overflow-y-auto border-t border-line py-1">
          {uploads.map((u) => (
            <li key={u.id} className="group flex h-9 items-center gap-2.5 px-3" title={u.error ?? u.key}>
              <ObjectIcon name={u.key} />
              <div className="min-w-0 flex-1">
                <p className="truncate">{baseName(u.key)}</p>
                <p className={cx('truncate text-[11px]', u.status === 'error' ? 'text-danger' : 'text-faint')}>
                  {u.status === 'error'
                    ? u.error
                    : u.status === 'uploading'
                      ? `${formatBytes(u.loaded)} of ${formatBytes(u.size)}`
                      : u.status === 'queued'
                        ? 'Waiting…'
                        : u.status === 'cancelled'
                          ? 'Cancelled'
                          : formatBytes(u.size)}
                </p>
              </div>
              {u.status === 'done' && <CircleCheck className="size-3.5 text-success" />}
              {u.status === 'error' && <CircleX className="size-3.5 text-danger" />}
              {(u.status === 'queued' || u.status === 'uploading') && (
                <IconButton label={`Cancel ${baseName(u.key)}`} className="size-6" onClick={() => cancel(u.id)}>
                  <X className="size-3.5" />
                </IconButton>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
