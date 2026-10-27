import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import type { RequestEvent } from '../lib/api'

export const MAX_EVENTS = 1000
const FLUSH_MS = 100

type EventsContextValue = {
  events: RequestEvent[] // newest first
  connected: boolean
  unseenErrors: number
  markSeen: () => void
  clear: () => void
}

const EventsContext = createContext<EventsContextValue | null>(null)

export const isError = (e: RequestEvent) => e.status >= 400

/**
 * Subscribes to the server's request log over SSE for the whole app, so the
 * sidebar can flag new errors while you are on another page.
 */
export function EventsProvider({ children, url = '/_api/events' }: { children: ReactNode; url?: string }) {
  const [events, setEvents] = useState<RequestEvent[]>([])
  const [connected, setConnected] = useState(false)
  const [seenId, setSeenId] = useState(0)
  const pending = useRef<RequestEvent[]>([])

  useEffect(() => {
    if (typeof EventSource === 'undefined') return
    const source = new EventSource(url)
    let timer: ReturnType<typeof setTimeout> | undefined
    const flush = () => {
      timer = undefined
      const batch = pending.current.reverse()
      pending.current = []
      setEvents((list) => [...batch, ...list].slice(0, MAX_EVENTS))
    }
    source.onopen = () => {
      // Each connection replays the server's backlog, so start fresh.
      pending.current = []
      setEvents([])
      setConnected(true)
    }
    source.onerror = () => setConnected(false)
    source.onmessage = (msg) => {
      try {
        pending.current.push(JSON.parse(msg.data))
      } catch {
        return
      }
      timer ??= setTimeout(flush, FLUSH_MS)
    }
    return () => {
      clearTimeout(timer)
      source.close()
    }
  }, [url])

  const markSeen = useCallback(() => setSeenId(events[0]?.id ?? 0), [events])
  const clear = useCallback(() => {
    setSeenId(events[0]?.id ?? 0)
    setEvents([])
  }, [events])
  const unseenErrors = useMemo(() => events.filter((e) => e.id > seenId && isError(e)).length, [events, seenId])

  const value = useMemo(
    () => ({ events, connected, unseenErrors, markSeen, clear }),
    [events, connected, unseenErrors, markSeen, clear],
  )
  return <EventsContext.Provider value={value}>{children}</EventsContext.Provider>
}

export function useEvents(): EventsContextValue {
  const ctx = useContext(EventsContext)
  if (!ctx) throw new Error('useEvents outside EventsProvider')
  return ctx
}
