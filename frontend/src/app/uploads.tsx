import { useQueryClient } from '@tanstack/react-query'
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { api, keys } from '../lib/api'
import type { PickedFile } from '../lib/files'

export const CONCURRENCY = 3

export type UploadStatus = 'queued' | 'uploading' | 'done' | 'error' | 'cancelled'

export type Upload = {
  id: number
  bucket: string
  key: string
  size: number
  loaded: number
  status: UploadStatus
  error?: string
}

type UploadsContextValue = {
  uploads: Upload[]
  enqueue: (bucket: string, prefix: string, files: PickedFile[]) => void
  cancel: (id: number) => void
  clearFinished: () => void
}

const UploadsContext = createContext<UploadsContextValue | null>(null)

let nextId = 1

export function UploadsProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient()
  const [uploads, setUploads] = useState<Upload[]>([])
  const files = useRef(new Map<number, File>())
  const aborts = useRef(new Map<number, AbortController>())

  const update = useCallback((id: number, patch: Partial<Upload>) => {
    setUploads((list) => list.map((u) => (u.id === id ? { ...u, ...patch } : u)))
  }, [])

  const enqueue = useCallback((bucket: string, prefix: string, picked: PickedFile[]) => {
    const added = picked.map(({ file, path }) => {
      const id = nextId++
      files.current.set(id, file)
      return { id, bucket, key: prefix + path, size: file.size, loaded: 0, status: 'queued' as const }
    })
    setUploads((list) => [...list, ...added])
  }, [])

  const start = useCallback(
    (u: Upload) => {
      const file = files.current.get(u.id)
      if (!file) return
      const ctrl = new AbortController()
      aborts.current.set(u.id, ctrl)
      update(u.id, { status: 'uploading' })
      api
        .upload(u.bucket, u.key, file, (loaded) => update(u.id, { loaded }), ctrl.signal)
        .then(() => {
          update(u.id, { status: 'done', loaded: u.size })
          queryClient.invalidateQueries({ queryKey: keys.listing(u.bucket) })
          queryClient.invalidateQueries({ queryKey: keys.buckets })
        })
        .catch((err: Error) => {
          if (!ctrl.signal.aborted) update(u.id, { status: 'error', error: err.message })
        })
        .finally(() => {
          files.current.delete(u.id)
          aborts.current.delete(u.id)
        })
    },
    [queryClient, update],
  )

  // Start queued uploads while fewer than CONCURRENCY are running.
  useEffect(() => {
    const active = uploads.filter((u) => u.status === 'uploading').length
    const queued = uploads.filter((u) => u.status === 'queued' && !aborts.current.has(u.id))
    queued.slice(0, Math.max(0, CONCURRENCY - active)).forEach(start)
  }, [uploads, start])

  const cancel = useCallback(
    (id: number) => {
      aborts.current.get(id)?.abort()
      files.current.delete(id)
      update(id, { status: 'cancelled' })
    },
    [update],
  )

  const clearFinished = useCallback(() => {
    setUploads((list) => list.filter((u) => u.status === 'queued' || u.status === 'uploading'))
  }, [])

  const value = useMemo(() => ({ uploads, enqueue, cancel, clearFinished }), [uploads, enqueue, cancel, clearFinished])
  return <UploadsContext.Provider value={value}>{children}</UploadsContext.Provider>
}

export function useUploads(): UploadsContextValue {
  const ctx = useContext(UploadsContext)
  if (!ctx) throw new Error('useUploads outside UploadsProvider')
  return ctx
}
