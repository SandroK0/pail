import { createContext, useCallback, useContext, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { filesFromInput } from '../lib/files'
import { useUploads } from './uploads'

export type DialogState =
  | { type: 'createBucket' }
  | { type: 'deleteBucket'; bucket: string }
  | { type: 'newFolder'; bucket: string; prefix: string }
  | { type: 'rename'; bucket: string; from: string }
  | { type: 'delete'; bucket: string; keys: string[]; prefixes: string[]; onDone?: () => void }
  | { type: 'presign'; bucket: string; objectKey: string }
  | { type: 'shortcuts' }

type UIContextValue = {
  dialog: DialogState | null
  openDialog: (d: DialogState) => void
  closeDialog: () => void
  paletteOpen: boolean
  setPaletteOpen: (open: boolean) => void
  /** Opens the file picker and uploads the chosen files into bucket/prefix. */
  pickFiles: (bucket: string, prefix: string) => void
}

const UIContext = createContext<UIContextValue | null>(null)

export function UIProvider({ children }: { children: ReactNode }) {
  const [dialog, setDialog] = useState<DialogState | null>(null)
  const [paletteOpen, setPaletteOpen] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  const target = useRef<{ bucket: string; prefix: string } | null>(null)
  const { enqueue } = useUploads()

  const pickFiles = useCallback((bucket: string, prefix: string) => {
    target.current = { bucket, prefix }
    input.current?.click()
  }, [])

  const closeDialog = useCallback(() => setDialog(null), [])
  const value = useMemo(
    () => ({ dialog, openDialog: setDialog, closeDialog, paletteOpen, setPaletteOpen, pickFiles }),
    [dialog, closeDialog, paletteOpen, pickFiles],
  )

  return (
    <UIContext.Provider value={value}>
      {children}
      <input
        ref={input}
        type="file"
        multiple
        hidden
        data-testid="file-picker"
        onChange={(e) => {
          if (target.current) enqueue(target.current.bucket, target.current.prefix, filesFromInput(e.target.files))
          e.target.value = ''
        }}
      />
    </UIContext.Provider>
  )
}

export function useUI(): UIContextValue {
  const ctx = useContext(UIContext)
  if (!ctx) throw new Error('useUI outside UIProvider')
  return ctx
}
