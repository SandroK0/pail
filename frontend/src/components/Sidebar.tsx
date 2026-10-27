import * as Dropdown from '@radix-ui/react-dropdown-menu'
import { useQuery } from '@tanstack/react-query'
import { Activity, Database, Ellipsis, Keyboard, Moon, Plus, Search, Settings2, Sun, Trash2 } from 'lucide-react'
import { NavLink, useParams } from 'react-router'
import { useEvents } from '../app/events'
import type { useTheme } from '../app/theme'
import { useUI } from '../app/ui'
import { api, keys } from '../lib/api'
import { formatBytes } from '../lib/format'
import { modKey } from '../lib/hotkeys'
import { IconButton, Kbd, Spinner, Tooltip, cx } from './ui'

function navClass({ isActive }: { isActive: boolean }) {
  return cx(
    'group flex h-7 items-center gap-2 rounded-md px-2 transition-colors',
    isActive ? 'bg-active text-fg' : 'text-muted hover:bg-hover hover:text-fg',
  )
}

export function Sidebar({ theme }: { theme: ReturnType<typeof useTheme> }) {
  const ui = useUI()
  const { connected, unseenErrors } = useEvents()
  const { bucket: current } = useParams()
  const buckets = useQuery({ queryKey: keys.buckets, queryFn: api.buckets })

  return (
    <aside aria-label="Sidebar" className="flex w-60 shrink-0 flex-col border-r border-line bg-panel">
      <div className="flex h-12 items-center gap-2 px-4">
        <div className="grid size-5 place-items-center rounded-md bg-accent text-white">
          <Database className="size-3" strokeWidth={2.5} />
        </div>
        <span className="font-semibold tracking-tight">Pail</span>
        <span className="ml-auto text-[11px] text-faint">local</span>
      </div>

      <div className="px-3 pb-2">
        <button
          type="button"
          onClick={() => ui.setPaletteOpen(true)}
          className="flex h-7 w-full cursor-pointer items-center gap-2 rounded-md border border-line bg-raised px-2 text-muted transition-colors hover:border-line-strong hover:text-fg"
        >
          <Search className="size-3.5" />
          <span>Search…</span>
          <span className="ml-auto flex gap-0.5">
            <Kbd>{modKey}</Kbd>
            <Kbd>K</Kbd>
          </span>
        </button>
      </div>

      <nav className="flex-1 overflow-y-auto px-3 py-2">
        <div className="flex h-7 items-center justify-between px-2 text-xs font-medium text-faint">
          <span>Buckets</span>
          <Tooltip label="New bucket">
            <IconButton label="New bucket" className="size-5" onClick={() => ui.openDialog({ type: 'createBucket' })}>
              <Plus className="size-3.5" />
            </IconButton>
          </Tooltip>
        </div>
        {buckets.isPending && (
          <div className="px-2 py-1">
            <Spinner />
          </div>
        )}
        {buckets.error && <p className="px-2 py-1 text-danger">Could not load buckets</p>}
        <ul className="space-y-px">
          {buckets.data?.map((b) => (
            <li key={b.name} className="group/bucket relative">
              <NavLink
                to={`/b/${encodeURIComponent(b.name)}`}
                className={() => navClass({ isActive: b.name === current })}
                title={`${b.objectCount} objects · ${formatBytes(b.size)}`}
              >
                <Database className="size-3.5 shrink-0" />
                <span className="truncate">{b.name}</span>
                <span className="ml-auto text-xs text-faint tabular-nums group-hover/bucket:invisible">
                  {b.objectCount}
                </span>
              </NavLink>
              <Dropdown.Root>
                <Dropdown.Trigger asChild>
                  <IconButton
                    label={`${b.name} options`}
                    className="absolute top-0.5 right-1 size-6 opacity-0 group-hover/bucket:opacity-100 data-[state=open]:opacity-100"
                  >
                    <Ellipsis className="size-3.5" />
                  </IconButton>
                </Dropdown.Trigger>
                <Dropdown.Portal>
                  <Dropdown.Content
                    align="start"
                    sideOffset={4}
                    className="z-50 min-w-40 animate-pop-in rounded-lg border border-line bg-raised p-1 shadow-pop"
                  >
                    <Dropdown.Item
                      onSelect={() => ui.openDialog({ type: 'deleteBucket', bucket: b.name })}
                      className="flex h-7 cursor-pointer items-center gap-2 rounded px-2 text-danger outline-none data-highlighted:bg-hover"
                    >
                      <Trash2 className="size-3.5" />
                      Delete bucket…
                    </Dropdown.Item>
                  </Dropdown.Content>
                </Dropdown.Portal>
              </Dropdown.Root>
            </li>
          ))}
        </ul>

        <div className="mt-4 flex h-7 items-center px-2 text-xs font-medium text-faint">Workspace</div>
        <ul className="space-y-px">
          <li>
            <NavLink to="/activity" className={navClass}>
              <Activity className="size-3.5" />
              <span>Activity</span>
              <span className="ml-auto flex items-center gap-1.5">
                {unseenErrors > 0 && (
                  <span
                    aria-label={`${unseenErrors} new errors`}
                    className="rounded-full bg-danger/15 px-1.5 text-[11px] font-medium text-danger tabular-nums"
                  >
                    {unseenErrors}
                  </span>
                )}
                <span
                  title={connected ? 'Live' : 'Disconnected'}
                  className={cx('size-1.5 rounded-full', connected ? 'bg-success' : 'bg-faint')}
                />
              </span>
            </NavLink>
          </li>
          <li>
            <NavLink to="/setup" className={navClass}>
              <Settings2 className="size-3.5" />
              <span>Laravel setup</span>
            </NavLink>
          </li>
        </ul>
      </nav>

      <div className="flex items-center gap-1 border-t border-line px-3 py-2">
        <Tooltip label="Toggle theme">
          <IconButton label="Toggle theme" onClick={theme.toggle}>
            {theme.resolved === 'dark' ? <Sun className="size-3.5" /> : <Moon className="size-3.5" />}
          </IconButton>
        </Tooltip>
        <Tooltip label="Keyboard shortcuts" shortcut={['?']}>
          <IconButton label="Keyboard shortcuts" onClick={() => ui.openDialog({ type: 'shortcuts' })}>
            <Keyboard className="size-3.5" />
          </IconButton>
        </Tooltip>
      </div>
    </aside>
  )
}
