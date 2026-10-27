import { ArrowDown, ArrowUp, Check, Globe } from 'lucide-react'
import { useEffect, useRef } from 'react'
import type { MouseEvent } from 'react'
import type { Folder, ObjectSummary } from '../lib/api'
import { formatBytes, formatDateTime, formatRelative } from '../lib/format'
import { FolderIcon, ObjectIcon, cx } from './ui'

export type Row =
  | { type: 'folder'; id: string; name: string; folder: Folder }
  | { type: 'object'; id: string; name: string; object: ObjectSummary }

export type SortKey = 'name' | 'size' | 'modified'
export type Sort = { key: SortKey; dir: 'asc' | 'desc' }

/**
 * Folders always come first. Sorting by name orders both groups; sorting by
 * size or date orders files and keeps folders alphabetical.
 */
export function sortRows(rows: Row[], sort: Sort): Row[] {
  const dir = sort.dir === 'asc' ? 1 : -1
  const byName = (a: Row, b: Row) => a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' })
  const cmp = (a: Row, b: Row): number => {
    if (a.type !== b.type) return a.type === 'folder' ? -1 : 1
    if (sort.key === 'name') return dir * byName(a, b)
    if (a.type === 'folder' || b.type === 'folder') return byName(a, b)
    if (sort.key === 'size') return dir * (a.object.size - b.object.size) || a.name.localeCompare(b.name)
    return dir * (Date.parse(a.object.lastModified) - Date.parse(b.object.lastModified)) || a.name.localeCompare(b.name)
  }
  return [...rows].sort(cmp)
}

type Props = {
  rows: Row[]
  selected: Set<string>
  focusIndex: number
  previewKey: string | null
  sort: Sort
  onSort: (key: SortKey) => void
  onRowClick: (index: number, e: MouseEvent) => void
  onRowOpen: (index: number) => void
  onToggleSelect: (index: number) => void
}

function SortHeader({ label, sortKey, sort, onSort, className }: {
  label: string
  sortKey: SortKey
  sort: Sort
  onSort: (k: SortKey) => void
  className?: string
}) {
  const active = sort.key === sortKey
  const Arrow = sort.dir === 'asc' ? ArrowUp : ArrowDown
  return (
    <button
      type="button"
      onClick={() => onSort(sortKey)}
      aria-sort={active ? (sort.dir === 'asc' ? 'ascending' : 'descending') : undefined}
      className={cx('flex cursor-pointer items-center gap-1 hover:text-fg', active && 'text-fg', className)}
    >
      {label}
      {active && <Arrow className="size-3" />}
    </button>
  )
}

export function ObjectTable({ rows, selected, focusIndex, previewKey, sort, onSort, onRowClick, onRowOpen, onToggleSelect }: Props) {
  const focusedRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    focusedRef.current?.scrollIntoView?.({ block: 'nearest' })
  }, [focusIndex])

  return (
    <div role="grid" aria-label="Objects" aria-multiselectable className="min-w-0">
      <div
        role="row"
        className="sticky top-0 z-10 grid h-8 grid-cols-[28px_minmax(0,1fr)_96px_120px] items-center border-b border-line bg-bg/90 pr-4 pl-2 text-xs text-faint backdrop-blur"
      >
        <span />
        <SortHeader label="Name" sortKey="name" sort={sort} onSort={onSort} />
        <SortHeader label="Size" sortKey="size" sort={sort} onSort={onSort} className="justify-end" />
        <SortHeader label="Modified" sortKey="modified" sort={sort} onSort={onSort} className="justify-end" />
      </div>
      {rows.map((row, i) => {
        const isSelected = selected.has(row.id)
        const isFocused = i === focusIndex
        const isPreviewed = row.type === 'object' && row.id === previewKey
        return (
          <div
            key={row.id}
            ref={isFocused ? focusedRef : undefined}
            role="row"
            aria-selected={isSelected}
            data-focused={isFocused || undefined}
            onClick={(e) => onRowClick(i, e)}
            onDoubleClick={() => onRowOpen(i)}
            className={cx(
              'group relative grid h-9 cursor-default grid-cols-[28px_minmax(0,1fr)_96px_120px] items-center pr-4 pl-2 select-none',
              isSelected || isPreviewed ? 'bg-accent-soft' : isFocused ? 'bg-hover' : 'hover:bg-hover/60',
            )}
          >
            {isFocused && <span aria-hidden className="absolute inset-y-1 left-0 w-0.5 rounded-r bg-accent" />}
            <span role="gridcell" className="grid place-items-center">
              <button
                type="button"
                aria-label={`Select ${row.name}`}
                onClick={(e) => {
                  e.stopPropagation()
                  onToggleSelect(i)
                }}
                className={cx(
                  'grid size-3.5 cursor-pointer place-items-center rounded-[4px] border transition-opacity',
                  isSelected
                    ? 'border-accent bg-accent text-white opacity-100'
                    : 'border-line-strong opacity-0 group-hover:opacity-100',
                )}
              >
                {isSelected && <Check className="size-2.5" strokeWidth={3} />}
              </button>
            </span>
            <span role="gridcell" className="flex min-w-0 items-center gap-2.5">
              {row.type === 'folder' ? <FolderIcon /> : <ObjectIcon name={row.name} contentType={row.object.contentType} />}
              <span className="truncate">{row.name}</span>
              {row.type === 'object' && row.object.acl.startsWith('public') && (
                <Globe aria-label="Public" className="size-3 shrink-0 text-faint" />
              )}
            </span>
            <span role="gridcell" className="text-right text-muted tabular-nums">
              {row.type === 'object' ? formatBytes(row.object.size) : '—'}
            </span>
            <span
              role="gridcell"
              className="text-right text-muted"
              title={row.type === 'object' ? formatDateTime(row.object.lastModified) : undefined}
            >
              {row.type === 'object' ? formatRelative(row.object.lastModified) : ''}
            </span>
          </div>
        )
      })}
    </div>
  )
}
