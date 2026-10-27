import { useQuery } from '@tanstack/react-query'
import { Command } from 'cmdk'
import * as Dialog from '@radix-ui/react-dialog'
import { Activity, Database, FolderPlus, Keyboard, Plus, Search, Settings2, SunMoon, Upload } from 'lucide-react'
import { useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import { useLocation, useNavigate } from 'react-router'
import type { useTheme } from '../app/theme'
import { useUI } from '../app/ui'
import { api, keys } from '../lib/api'
import { parentPrefix } from '../lib/format'
import { bucketPath } from './Dialogs'
import { Kbd, ObjectIcon, Spinner } from './ui'

/** Reads the bucket and prefix out of a /b/:bucket/* location. */
export function locationTarget(pathname: string): { bucket: string; prefix: string } | null {
  const m = pathname.match(/^\/b\/([^/]+)(?:\/(.*))?$/)
  if (!m) return null
  const segs = (m[2] ?? '').split('/').filter(Boolean).map(decodeURIComponent)
  return { bucket: decodeURIComponent(m[1]), prefix: segs.length ? segs.join('/') + '/' : '' }
}

function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms)
    return () => clearTimeout(t)
  }, [value, ms])
  return v
}

function Item({ icon, children, shortcut, onSelect, value }: {
  icon: ReactNode
  children: ReactNode
  shortcut?: string
  onSelect: () => void
  value?: string
}) {
  return (
    <Command.Item
      value={value}
      onSelect={onSelect}
      className="flex h-9 cursor-pointer items-center gap-2.5 rounded-md px-2.5 text-muted data-[selected=true]:bg-hover data-[selected=true]:text-fg"
    >
      <span className="grid size-4 place-items-center">{icon}</span>
      <span className="min-w-0 flex-1 truncate">{children}</span>
      {shortcut && <Kbd>{shortcut}</Kbd>}
    </Command.Item>
  )
}

const groupClass =
  'px-1.5 pb-1 [&_[cmdk-group-heading]]:px-2.5 [&_[cmdk-group-heading]]:py-1.5 [&_[cmdk-group-heading]]:text-xs [&_[cmdk-group-heading]]:text-faint'

export function CommandPalette({ theme }: { theme: ReturnType<typeof useTheme> }) {
  const ui = useUI()
  const navigate = useNavigate()
  const location = useLocation()
  const [search, setSearch] = useState('')
  const target = locationTarget(location.pathname)
  const buckets = useQuery({ queryKey: keys.buckets, queryFn: api.buckets, enabled: ui.paletteOpen })
  const q = useDebounced(search.trim(), 150)
  const objects = useQuery({
    queryKey: keys.search(target?.bucket ?? '', q),
    queryFn: () => api.search(target!.bucket, q),
    enabled: ui.paletteOpen && !!target && q.length >= 2,
  })

  const close = () => {
    ui.setPaletteOpen(false)
    setSearch('')
  }
  const run = (fn: () => void) => () => {
    close()
    fn()
  }

  return (
    <Command.Dialog
      open={ui.paletteOpen}
      onOpenChange={(o) => (o ? ui.setPaletteOpen(true) : close())}
      label="Command menu"
      shouldFilter
      overlayClassName="fixed inset-0 z-40 bg-overlay animate-fade-in"
      contentClassName="fixed top-[14vh] left-1/2 z-50 w-[calc(100vw-32px)] max-w-xl -translate-x-1/2 animate-pop-in overflow-hidden rounded-xl border border-line bg-panel shadow-pop"
    >
      <Dialog.Title className="sr-only">Command menu</Dialog.Title>
      <Dialog.Description className="sr-only">Search for buckets, objects and actions</Dialog.Description>
      <div className="flex items-center gap-2 border-b border-line px-4">
        <Search className="size-4 text-faint" />
        <Command.Input
          value={search}
          onValueChange={setSearch}
          placeholder={target ? `Search ${target.bucket} or type a command…` : 'Type a command or search…'}
          className="h-12 flex-1 bg-transparent text-sm text-fg placeholder:text-faint focus:outline-none"
        />
        {objects.isFetching && <Spinner />}
      </div>
      <Command.List className="max-h-[min(60vh,420px)] overflow-y-auto py-1.5">
        {/* Avoid flashing "No results" while an object search is still running. */}
        {!(objects.isFetching || (q !== search.trim() && search.trim().length >= 2 && !!target)) && (
          <Command.Empty className="px-4 py-6 text-center text-muted">No results</Command.Empty>
        )}

        {target && (objects.data?.length ?? 0) > 0 && (
          <Command.Group heading={`Objects in ${target.bucket}`} className={groupClass}>
            {objects.data!.map((o) => (
              <Item
                key={o.key}
                value={`object ${o.key} ${search}`}
                icon={<ObjectIcon name={o.name} contentType={o.contentType} />}
                onSelect={run(() =>
                  navigate(`${bucketPath(target.bucket, parentPrefix(o.key))}?preview=${encodeURIComponent(o.key)}`),
                )}
              >
                <span className="font-mono text-xs">{o.key}</span>
              </Item>
            ))}
          </Command.Group>
        )}

        {target && (
          <Command.Group heading="Actions" className={groupClass}>
            <Item icon={<Upload className="size-3.5" />} shortcut="U" onSelect={run(() => ui.pickFiles(target.bucket, target.prefix))}>
              Upload files…
            </Item>
            <Item
              icon={<FolderPlus className="size-3.5" />}
              shortcut="N"
              onSelect={run(() => ui.openDialog({ type: 'newFolder', bucket: target.bucket, prefix: target.prefix }))}
            >
              New folder…
            </Item>
          </Command.Group>
        )}

        <Command.Group heading="Buckets" className={groupClass}>
          {buckets.data?.map((b) => (
            <Item key={b.name} value={`bucket ${b.name}`} icon={<Database className="size-3.5" />} onSelect={run(() => navigate(bucketPath(b.name)))}>
              {b.name}
            </Item>
          ))}
          <Item icon={<Plus className="size-3.5" />} onSelect={run(() => ui.openDialog({ type: 'createBucket' }))}>
            New bucket…
          </Item>
        </Command.Group>

        <Command.Group heading="Go to" className={groupClass}>
          <Item icon={<Activity className="size-3.5" />} onSelect={run(() => navigate('/activity'))}>
            Activity
          </Item>
          <Item icon={<Settings2 className="size-3.5" />} onSelect={run(() => navigate('/setup'))}>
            Laravel setup
          </Item>
        </Command.Group>

        <Command.Group heading="Preferences" className={groupClass}>
          <Item icon={<SunMoon className="size-3.5" />} onSelect={run(theme.toggle)}>
            Toggle theme
          </Item>
          <Item icon={<Keyboard className="size-3.5" />} shortcut="?" onSelect={run(() => ui.openDialog({ type: 'shortcuts' }))}>
            Keyboard shortcuts
          </Item>
        </Command.Group>
      </Command.List>
    </Command.Dialog>
  )
}
