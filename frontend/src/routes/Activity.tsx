import * as Dropdown from '@radix-ui/react-dropdown-menu'
import { Activity as ActivityIcon, Check, ChevronDown, Pause, Play, Search, Trash2, X } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { Link } from 'react-router'
import { isError, useEvents } from '../app/events'
import { bucketPath } from '../components/Dialogs'
import { Badge, Button, EmptyState, IconButton, Input, Tooltip, cx } from '../components/ui'
import type { RequestEvent } from '../lib/api'
import { formatBytes, formatClock, formatDuration, parentPrefix, shortUserAgent } from '../lib/format'
import { PageHeader } from './Layout'

type StatusFilter = 'all' | 'errors' | 'writes'

const writeMethods = new Set(['PUT', 'POST', 'DELETE'])

export function filterEvents(
  events: RequestEvent[],
  status: StatusFilter,
  text: string,
  ops: ReadonlySet<string> = new Set(),
): RequestEvent[] {
  const needle = text.trim().toLowerCase()
  return events.filter((e) => {
    if (ops.size > 0 && !ops.has(e.op)) return false
    if (status === 'errors' && !isError(e)) return false
    if (status === 'writes' && !writeMethods.has(e.method)) return false
    if (!needle) return true
    return [e.op, e.bucket, e.key, e.path, e.errorCode, e.userAgent, e.requestId].some((v) =>
      v?.toLowerCase().includes(needle),
    )
  })
}

/** Operations in the log, most frequent first. */
export function operationCounts(events: RequestEvent[]): [string, number][] {
  const counts = new Map<string, number>()
  for (const e of events) counts.set(e.op, (counts.get(e.op) ?? 0) + 1)
  return [...counts].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
}

function OperationFilter({
  events,
  selected,
  onChange,
}: {
  events: RequestEvent[]
  selected: ReadonlySet<string>
  onChange: (ops: Set<string>) => void
}) {
  const counts = useMemo(() => {
    const list = operationCounts(events)
    // Keep selected operations listed even once they leave the log, so they can be unticked.
    for (const op of selected) if (!list.some(([o]) => o === op)) list.push([op, 0])
    return list
  }, [events, selected])
  const toggle = (op: string) => {
    const next = new Set(selected)
    if (next.has(op)) next.delete(op)
    else next.add(op)
    onChange(next)
  }
  const label =
    selected.size === 0 ? 'All operations' : selected.size === 1 ? [...selected][0] : `${selected.size} operations`

  return (
    <Dropdown.Root>
      <Dropdown.Trigger asChild>
        <Button size="sm" aria-label="Filter by operation" className={cx(selected.size > 0 && 'border-accent/60 text-accent')}>
          {label}
          <ChevronDown className="size-3 opacity-60" />
        </Button>
      </Dropdown.Trigger>
      <Dropdown.Portal>
        <Dropdown.Content
          align="start"
          sideOffset={4}
          className="z-50 max-h-80 min-w-56 animate-pop-in overflow-y-auto rounded-lg border border-line bg-raised p-1 shadow-pop"
        >
          {counts.length === 0 && <p className="px-2 py-1.5 text-muted">No requests yet</p>}
          {counts.map(([op, n]) => (
            <Dropdown.CheckboxItem
              key={op}
              checked={selected.has(op)}
              onCheckedChange={() => toggle(op)}
              onSelect={(e) => e.preventDefault()} // keep the menu open for multi-select
              className="group flex h-7 cursor-pointer items-center gap-2 rounded px-2 outline-none data-highlighted:bg-hover"
            >
              <span className="grid size-3.5 place-items-center rounded-[4px] border border-line-strong group-data-[state=checked]:border-accent group-data-[state=checked]:bg-accent">
                <Dropdown.ItemIndicator>
                  <Check className="size-2.5 text-white" strokeWidth={3} />
                </Dropdown.ItemIndicator>
              </span>
              <span className="flex-1">{op}</span>
              <span className="text-xs text-faint tabular-nums">{n}</span>
            </Dropdown.CheckboxItem>
          ))}
          {selected.size > 0 && (
            <>
              <Dropdown.Separator className="my-1 h-px bg-line" />
              <Dropdown.Item
                onSelect={() => onChange(new Set())}
                className="flex h-7 cursor-pointer items-center rounded px-2 text-muted outline-none data-highlighted:bg-hover data-highlighted:text-fg"
              >
                Show all operations
              </Dropdown.Item>
            </>
          )}
        </Dropdown.Content>
      </Dropdown.Portal>
    </Dropdown.Root>
  )
}

function StatusBadge({ status }: { status: number }) {
  return (
    <Badge tone={status >= 500 ? 'danger' : status >= 400 ? 'warning' : status >= 300 ? 'neutral' : 'success'}>
      {status}
    </Badge>
  )
}

const methodColor: Record<string, string> = {
  GET: 'text-[#5fb3d9]',
  HEAD: 'text-muted',
  PUT: 'text-[#4cb782]',
  POST: 'text-[#b78de8]',
  DELETE: 'text-danger',
  OPTIONS: 'text-faint',
}

function DetailRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[110px_minmax(0,1fr)] gap-3 py-1.5">
      <dt className="text-muted">{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

function Detail({ event, onClose }: { event: RequestEvent; onClose: () => void }) {
  const canOpen = event.bucket && event.key && event.status < 400 && event.method !== 'DELETE'
  return (
    <aside aria-label="Request detail" className="flex w-[380px] shrink-0 animate-slide-in flex-col border-l border-line bg-panel">
      <header className="flex h-12 items-center gap-2 border-b border-line pr-2 pl-4">
        <h2 className="flex-1 font-medium">{event.op}</h2>
        <IconButton label="Close detail" onClick={onClose}>
          <X className="size-3.5" />
        </IconButton>
      </header>
      <dl className="flex-1 overflow-y-auto px-4 py-3">
        <DetailRow label="Operation">{event.op}</DetailRow>
        <DetailRow label="Request">
          <span className="font-mono text-xs break-all">
            {event.method} {event.path}
          </span>
        </DetailRow>
        <DetailRow label="Status">
          <StatusBadge status={event.status} />
        </DetailRow>
        {event.errorCode && (
          <DetailRow label="Error">
            <span className="text-danger">
              {event.errorCode}: {event.errorMessage}
            </span>
          </DetailRow>
        )}
        <DetailRow label="Duration">{formatDuration(event.durationMs)}</DetailRow>
        <DetailRow label="Sent / received">
          {formatBytes(event.bytesIn)} in · {formatBytes(event.bytesOut)} out
        </DetailRow>
        <DetailRow label="Access key">
          {event.accessKey ? (
            <span className="font-mono text-xs">
              {event.accessKey}
              {event.presigned && ' (presigned)'}
            </span>
          ) : (
            'anonymous'
          )}
        </DetailRow>
        <DetailRow label="Client">
          <span className="font-mono text-xs break-all">{event.userAgent || '—'}</span>
        </DetailRow>
        <DetailRow label="Time">{new Date(event.time).toLocaleString()}</DetailRow>
        {event.requestId && (
          <DetailRow label="Request ID">
            <span className="font-mono text-xs">{event.requestId}</span>
          </DetailRow>
        )}
      </dl>
      {canOpen && (
        <div className="border-t border-line p-3">
          <Link
            to={`${bucketPath(event.bucket!, parentPrefix(event.key!))}?preview=${encodeURIComponent(event.key!)}`}
            className="text-accent hover:underline"
          >
            Open object →
          </Link>
        </div>
      )}
    </aside>
  )
}

export function Activity() {
  const { events, connected, markSeen, clear } = useEvents()
  const [status, setStatus] = useState<StatusFilter>('all')
  const [text, setText] = useState('')
  const [ops, setOps] = useState<Set<string>>(new Set())
  const [paused, setPaused] = useState<RequestEvent[] | null>(null)
  const [selectedId, setSelectedId] = useState<number | null>(null)

  // Viewing the log counts as seeing its errors.
  useEffect(() => {
    markSeen()
  }, [markSeen])

  const source = paused ?? events
  const shown = useMemo(() => filterEvents(source, status, text, ops), [source, status, text, ops])
  const selected = source.find((e) => e.id === selectedId)
  const newWhilePaused = paused ? events.length - paused.length : 0

  return (
    <div className="flex min-h-0 flex-1">
      <section className="flex min-w-0 flex-1 flex-col">
        <PageHeader
          actions={
            <>
              <Tooltip label={paused ? 'Resume' : 'Pause'}>
                <Button size="sm" onClick={() => setPaused(paused ? null : events)}>
                  {paused ? <Play className="size-3" /> : <Pause className="size-3" />}
                  {paused ? `Resume${newWhilePaused > 0 ? ` (${newWhilePaused} new)` : ''}` : 'Pause'}
                </Button>
              </Tooltip>
              <Tooltip label="Clear log">
                <IconButton
                  label="Clear log"
                  onClick={() => {
                    clear()
                    setPaused(null)
                    setSelectedId(null)
                  }}
                >
                  <Trash2 className="size-3.5" />
                </IconButton>
              </Tooltip>
            </>
          }
        >
          <h1 className="font-medium">Activity</h1>
          <span className="ml-2 flex items-center gap-1.5 text-xs text-muted">
            <span className={cx('size-1.5 rounded-full', connected ? 'animate-pulse bg-success' : 'bg-faint')} />
            {connected ? 'Live' : 'Reconnecting…'}
          </span>
        </PageHeader>

        <div className="flex items-center gap-2 border-b border-line px-4 py-2">
          <div role="tablist" aria-label="Filter by status" className="flex rounded-md border border-line-strong p-0.5">
            {(['all', 'errors', 'writes'] as const).map((s) => (
              <button
                key={s}
                role="tab"
                type="button"
                aria-selected={status === s}
                onClick={() => setStatus(s)}
                className={cx(
                  'h-6 cursor-pointer rounded px-2.5 text-xs font-medium capitalize',
                  status === s ? 'bg-active text-fg' : 'text-muted hover:text-fg',
                )}
              >
                {s}
              </button>
            ))}
          </div>
          <OperationFilter events={source} selected={ops} onChange={setOps} />
          <div className="relative max-w-xs flex-1">
            <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-faint" />
            <Input
              aria-label="Search requests"
              placeholder="Search operation, key, error, request ID…"
              value={text}
              onChange={(e) => setText(e.target.value)}
              className="h-7 pl-8"
            />
          </div>
          <span className="ml-auto text-xs text-faint">{shown.length} requests</span>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto">
          {shown.length === 0 ? (
            <EmptyState
              icon={<ActivityIcon className="size-4" />}
              title={events.length ? 'No matching requests' : 'Waiting for requests'}
              description={
                events.length ? undefined : 'Every S3 call your app makes shows up here as it happens. Try Storage::put() in tinker.'
              }
            />
          ) : (
            <table className="w-full table-fixed border-collapse">
              <thead className="sticky top-0 z-10 bg-bg/90 text-left text-xs text-faint backdrop-blur">
                <tr className="h-8 border-b border-line">
                  <th className="w-28 pl-4 font-normal">Time</th>
                  <th className="w-48 font-normal">Operation</th>
                  <th className="font-normal">Object</th>
                  <th className="w-16 font-normal">Status</th>
                  <th className="w-20 text-right font-normal">Size</th>
                  <th className="hidden w-20 text-right font-normal lg:table-cell">Duration</th>
                  <th className="hidden w-44 pr-4 pl-4 font-normal xl:table-cell">Client</th>
                </tr>
              </thead>
              <tbody>
                {shown.map((e) => (
                  <tr
                    key={e.id}
                    onClick={() => setSelectedId(e.id)}
                    className={cx(
                      'h-9 cursor-default border-b border-line/50',
                      e.id === selectedId ? 'bg-accent-soft' : 'hover:bg-hover/60',
                    )}
                  >
                    <td className="pl-4 font-mono text-xs text-faint tabular-nums">{formatClock(e.time)}</td>
                    <td className="truncate">
                      <span className={cx('mr-2 font-mono text-[11px] font-medium', methodColor[e.method] ?? 'text-muted')}>
                        {e.method}
                      </span>
                      {e.op}
                    </td>
                    <td className="truncate font-mono text-xs" title={e.path}>
                      {e.bucket ? (
                        <>
                          <span className="text-faint">{e.bucket}/</span>
                          {e.key}
                        </>
                      ) : (
                        <span className="text-faint">/</span>
                      )}
                      {e.errorCode && <span className="ml-2 font-sans text-danger">{e.errorCode}</span>}
                    </td>
                    <td>
                      <StatusBadge status={e.status} />
                    </td>
                    <td className="text-right text-muted tabular-nums">
                      {formatBytes(e.method === 'GET' ? e.bytesOut : e.bytesIn)}
                    </td>
                    <td className="hidden text-right text-muted tabular-nums lg:table-cell">{formatDuration(e.durationMs)}</td>
                    <td className="hidden truncate pr-4 pl-4 text-muted xl:table-cell">{shortUserAgent(e.userAgent ?? '')}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </section>
      {selected && <Detail event={selected} onClose={() => setSelectedId(null)} />}
    </div>
  )
}
