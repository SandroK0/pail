import { useInfiniteQuery } from '@tanstack/react-query'
import { ChevronRight, CloudUpload, FolderPlus, ListFilter, RefreshCw, Trash2, Upload, X } from 'lucide-react'
import { useMemo, useRef, useState } from 'react'
import type { DragEvent, MouseEvent } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router'
import { toast } from 'sonner'
import { useUI } from '../app/ui'
import { useUploads } from '../app/uploads'
import { bucketPath } from '../components/Dialogs'
import { ObjectTable, sortRows } from '../components/ObjectTable'
import type { Row, Sort, SortKey } from '../components/ObjectTable'
import { PreviewPane } from '../components/PreviewPane'
import { Button, EmptyState, IconButton, Input, Kbd, Spinner, Tooltip, cx } from '../components/ui'
import { api, keys } from '../lib/api'
import { breadcrumbs, parentPrefix } from '../lib/format'
import { dragHasFiles, filesFromDataTransfer } from '../lib/files'
import { useHotkeys } from '../lib/hotkeys'
import { PageHeader } from './Layout'

export function BucketView() {
  const { bucket = '', '*': splat = '' } = useParams()
  const segs = splat.split('/').filter(Boolean)
  const prefix = segs.length ? segs.join('/') + '/' : ''
  // Remount per folder so selection and focus start fresh.
  return <Browser key={`${bucket}/${prefix}`} bucket={bucket} prefix={prefix} />
}

function Browser({ bucket, prefix }: { bucket: string; prefix: string }) {
  const ui = useUI()
  const { enqueue } = useUploads()
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const previewKey = params.get('preview')

  const listing = useInfiniteQuery({
    queryKey: keys.listing(bucket, prefix),
    queryFn: ({ pageParam }) => api.list(bucket, prefix, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.nextCursor,
  })

  const [sort, setSort] = useState<Sort>({ key: 'name', dir: 'asc' })
  const [filter, setFilter] = useState('')
  const [filterOpen, setFilterOpen] = useState(false)
  const filterInput = useRef<HTMLInputElement>(null)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [focusIndex, setFocusIndex] = useState(-1)
  const anchor = useRef(-1)
  const [dragging, setDragging] = useState(false)
  const dragDepth = useRef(0)

  const rows = useMemo(() => {
    const all: Row[] = []
    for (const page of listing.data?.pages ?? []) {
      for (const f of page.folders) all.push({ type: 'folder', id: f.prefix, name: f.name, folder: f })
      for (const o of page.objects) all.push({ type: 'object', id: o.key, name: o.name, object: o })
    }
    const needle = filter.trim().toLowerCase()
    return sortRows(needle ? all.filter((r) => r.name.toLowerCase().includes(needle)) : all, sort)
  }, [listing.data, filter, sort])

  const focused = rows[focusIndex] as Row | undefined
  const selectedRows = rows.filter((r) => selected.has(r.id))
  const targets = selectedRows.length ? selectedRows : focused ? [focused] : []

  const setPreview = (key: string | null) =>
    setParams(
      (p) => {
        if (key) p.set('preview', key)
        else p.delete('preview')
        return p
      },
      { replace: true },
    )

  const open = (row: Row | undefined) => {
    if (!row) return
    if (row.type === 'folder') navigate(bucketPath(bucket, row.folder.prefix))
    else setPreview(row.id)
  }
  const goUp = () => prefix && navigate(bucketPath(bucket, parentPrefix(prefix)))
  const move = (delta: number) => {
    if (!rows.length) return
    const next = Math.max(0, Math.min(rows.length - 1, focusIndex + delta))
    setFocusIndex(next)
    anchor.current = next
    // Follow the cursor while a preview is open, like Finder's Quick Look.
    if (previewKey && rows[next].type === 'object') setPreview(rows[next].id)
  }
  const toggle = (row: Row) =>
    setSelected((s) => {
      const n = new Set(s)
      if (n.has(row.id)) n.delete(row.id)
      else n.add(row.id)
      return n
    })
  const remove = (items: Row[]) => {
    if (!items.length) return
    ui.openDialog({
      type: 'delete',
      bucket,
      keys: items.filter((r) => r.type === 'object').map((r) => r.id),
      prefixes: items.filter((r) => r.type === 'folder').map((r) => r.id),
      onDone: () => {
        setSelected(new Set())
        if (previewKey && items.some((r) => r.id === previewKey)) setPreview(null)
      },
    })
  }
  const copyUrl = (row: Row | undefined) => {
    if (row?.type !== 'object') return
    navigator.clipboard?.writeText(row.object.url)
    toast.success('URL copied')
  }

  const onRowClick = (i: number, e: MouseEvent) => {
    const row = rows[i]
    if (e.shiftKey && anchor.current >= 0) {
      const [a, b] = [Math.min(anchor.current, i), Math.max(anchor.current, i)]
      setSelected(new Set(rows.slice(a, b + 1).map((r) => r.id)))
    } else if (e.metaKey || e.ctrlKey) {
      toggle(row)
      anchor.current = i
    } else {
      setSelected(new Set())
      anchor.current = i
      if (row.type === 'object') setPreview(row.id)
    }
    setFocusIndex(i)
  }

  const overlayOpen = ui.paletteOpen || ui.dialog !== null
  useHotkeys(
    {
      j: () => move(1),
      arrowdown: () => move(1),
      k: () => move(-1),
      arrowup: () => move(-1),
      enter: () => open(focused),
      arrowright: () => focused?.type === 'folder' && open(focused),
      backspace: goUp,
      arrowleft: goUp,
      space: () => {
        if (focused?.type !== 'object') return
        setPreview(previewKey === focused.id ? null : focused.id)
      },
      x: () => focused && toggle(focused),
      'mod+a': () => setSelected(new Set(rows.map((r) => r.id))),
      escape: () => {
        if (filterOpen) {
          setFilter('')
          setFilterOpen(false)
        } else if (selected.size) setSelected(new Set())
        else if (previewKey) setPreview(null)
      },
      '/': () => {
        setFilterOpen(true)
        requestAnimationFrame(() => filterInput.current?.focus())
      },
      u: () => ui.pickFiles(bucket, prefix),
      n: () => ui.openDialog({ type: 'newFolder', bucket, prefix }),
      r: () => targets.length === 1 && ui.openDialog({ type: 'rename', bucket, from: targets[0].id }),
      c: () => copyUrl(focused),
      p: () => focused?.type === 'object' && ui.openDialog({ type: 'presign', bucket, objectKey: focused.id }),
      'mod+backspace': () => remove(targets),
      delete: () => remove(targets),
    },
    !overlayOpen,
  )

  const onDragEnter = (e: DragEvent) => {
    if (!dragHasFiles(e)) return
    e.preventDefault()
    dragDepth.current++
    setDragging(true)
  }
  const onDragLeave = () => {
    dragDepth.current = Math.max(0, dragDepth.current - 1)
    if (dragDepth.current === 0) setDragging(false)
  }
  const onDrop = async (e: DragEvent) => {
    e.preventDefault()
    dragDepth.current = 0
    setDragging(false)
    const files = await filesFromDataTransfer(e.dataTransfer)
    if (files.length) enqueue(bucket, prefix, files)
  }

  const crumbs = breadcrumbs(prefix)
  const notFound = (listing.error as { status?: number } | null)?.status === 404

  return (
    <div className="flex min-h-0 flex-1">
      <section
        className="relative flex min-w-0 flex-1 flex-col"
        onDragEnter={onDragEnter}
        onDragOver={(e) => dragHasFiles(e) && e.preventDefault()}
        onDragLeave={onDragLeave}
        onDrop={onDrop}
      >
        <PageHeader
          actions={
            <>
              {selected.size > 0 ? (
                <>
                  <span className="mr-1 text-muted">{selected.size} selected</span>
                  <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>
                    Clear
                  </Button>
                  <Button size="sm" className="text-danger" onClick={() => remove(selectedRows)}>
                    <Trash2 className="size-3" /> Delete
                  </Button>
                </>
              ) : (
                <>
                  <Tooltip label="Filter" shortcut={['/']}>
                    <IconButton
                      label="Filter"
                      onClick={() => {
                        setFilterOpen((o) => !o)
                        requestAnimationFrame(() => filterInput.current?.focus())
                      }}
                    >
                      <ListFilter className="size-3.5" />
                    </IconButton>
                  </Tooltip>
                  <Tooltip label="Refresh">
                    <IconButton label="Refresh" onClick={() => listing.refetch()}>
                      <RefreshCw className={cx('size-3.5', listing.isRefetching && 'animate-spin')} />
                    </IconButton>
                  </Tooltip>
                  <Tooltip label="New folder" shortcut={['N']}>
                    <IconButton label="New folder" onClick={() => ui.openDialog({ type: 'newFolder', bucket, prefix })}>
                      <FolderPlus className="size-3.5" />
                    </IconButton>
                  </Tooltip>
                  <Tooltip label="Upload files" shortcut={['U']}>
                    <Button variant="primary" size="sm" onClick={() => ui.pickFiles(bucket, prefix)}>
                      <Upload className="size-3" /> Upload
                    </Button>
                  </Tooltip>
                </>
              )}
            </>
          }
        >
          <nav aria-label="Breadcrumb" className="flex min-w-0 items-center gap-1">
            <Link
              to={bucketPath(bucket)}
              className={cx('rounded px-1 py-0.5 font-medium hover:bg-hover', crumbs.length ? 'text-muted' : 'text-fg')}
            >
              {bucket}
            </Link>
            {crumbs.map((c, i) => (
              <span key={c.prefix} className="flex min-w-0 items-center gap-1">
                <ChevronRight className="size-3.5 shrink-0 text-faint" />
                <Link
                  to={bucketPath(bucket, c.prefix)}
                  className={cx(
                    'truncate rounded px-1 py-0.5 hover:bg-hover',
                    i === crumbs.length - 1 ? 'font-medium text-fg' : 'text-muted',
                  )}
                >
                  {c.name}
                </Link>
              </span>
            ))}
          </nav>
        </PageHeader>

        {filterOpen && (
          <div className="flex items-center gap-2 border-b border-line px-4 py-2">
            <ListFilter className="size-3.5 text-faint" />
            <Input
              ref={filterInput}
              aria-label="Filter by name"
              placeholder="Filter by name…"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Escape' || e.key === 'Enter' || e.key === 'ArrowDown') {
                  e.preventDefault()
                  if (e.key === 'Escape') {
                    setFilter('')
                    setFilterOpen(false)
                  }
                  e.currentTarget.blur()
                  if (e.key !== 'Escape') setFocusIndex(0)
                }
              }}
              className="h-7 border-0 bg-transparent px-0"
            />
            <IconButton
              label="Close filter"
              className="size-6"
              onClick={() => {
                setFilter('')
                setFilterOpen(false)
              }}
            >
              <X className="size-3.5" />
            </IconButton>
          </div>
        )}

        <div className="min-h-0 flex-1 overflow-y-auto">
          {listing.isPending ? (
            <div className="grid h-40 place-items-center">
              <Spinner />
            </div>
          ) : notFound ? (
            <EmptyState icon={<X className="size-4" />} title={`Bucket “${bucket}” not found`} />
          ) : listing.error ? (
            <EmptyState icon={<X className="size-4" />} title="Could not load objects" description={listing.error.message} />
          ) : rows.length === 0 ? (
            filter ? (
              <EmptyState icon={<ListFilter className="size-4" />} title="Nothing matches" description={`No names contain “${filter}”.`} />
            ) : (
              <EmptyState
                icon={<CloudUpload className="size-4" />}
                title={prefix ? 'This folder is empty' : 'This bucket is empty'}
                description={
                  <>
                    Drop files here, press <Kbd>U</Kbd>, or from Laravel:
                    <code className="mt-2 block rounded-md border border-line bg-raised px-2 py-1.5 font-mono text-xs text-fg">
                      Storage::put('{prefix}hello.txt', 'Hello!');
                    </code>
                  </>
                }
                action={
                  <Button variant="primary" onClick={() => ui.pickFiles(bucket, prefix)}>
                    <Upload className="size-3.5" /> Upload files
                  </Button>
                }
              />
            )
          ) : (
            <>
              <ObjectTable
                rows={rows}
                selected={selected}
                focusIndex={focusIndex}
                previewKey={previewKey}
                sort={sort}
                onSort={(key: SortKey) =>
                  setSort((s) => ({ key, dir: s.key === key && s.dir === 'asc' ? 'desc' : 'asc' }))
                }
                onRowClick={onRowClick}
                onRowOpen={(i) => open(rows[i])}
                onToggleSelect={(i) => toggle(rows[i])}
              />
              {listing.hasNextPage && (
                <div className="p-3 text-center">
                  <Button size="sm" onClick={() => listing.fetchNextPage()} disabled={listing.isFetchingNextPage}>
                    {listing.isFetchingNextPage ? 'Loading…' : 'Load more'}
                  </Button>
                </div>
              )}
            </>
          )}
        </div>

        <footer className="flex h-8 shrink-0 items-center gap-3 border-t border-line px-4 text-xs text-faint">
          <span>
            {rows.length} {rows.length === 1 ? 'item' : 'items'}
            {listing.hasNextPage && '+'}
          </span>
          <span className="ml-auto hidden items-center gap-1 sm:flex">
            <Kbd>J</Kbd>
            <Kbd>K</Kbd> navigate · <Kbd>↵</Kbd> open · <Kbd>?</Kbd> shortcuts
          </span>
        </footer>

        {dragging && (
          <div className="pointer-events-none absolute inset-2 z-20 grid animate-fade-in place-items-center rounded-xl border-2 border-dashed border-accent bg-accent-soft">
            <div className="flex flex-col items-center gap-2 text-fg">
              <CloudUpload className="size-6 text-accent" />
              <p className="font-medium">
                Drop to upload to {bucket}/{prefix}
              </p>
            </div>
          </div>
        )}
      </section>

      {previewKey && <PreviewPane key={previewKey} bucket={bucket} objectKey={previewKey} onClose={() => setPreview(null)} />}
    </div>
  )
}
