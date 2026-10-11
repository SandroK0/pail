import { useRef, useState } from 'react'
import type { DragEvent, FormEvent } from 'react'

type Status = { kind: 'idle' | 'pending' | 'ok' | 'err'; text: string }

function formatSize(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

function App() {
  const [path, setPath] = useState('')
  const [file, setFile] = useState<File | null>(null)
  const [over, setOver] = useState(false)
  const [status, setStatus] = useState<Status>({ kind: 'idle', text: '' })
  const fileInput = useRef<HTMLInputElement>(null)

  function onDrop(e: DragEvent<HTMLLabelElement>) {
    e.preventDefault()
    setOver(false)
    const dropped = e.dataTransfer.files[0]
    if (dropped) setFile(dropped)
  }

  async function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    if (!file) return

    const body = new FormData()
    body.append('path', path)
    body.append('file', file)

    setStatus({ kind: 'pending', text: 'Uploading…' })
    try {
      const res = await fetch('/api/upload', { method: 'POST', body })
      const text = await res.text()
      let message = text.trim()
      try {
        message = JSON.parse(text).message ?? message
      } catch {
        // plain-text response (e.g. from http.Error)
      }
      setStatus({ kind: res.ok ? 'ok' : 'err', text: message })
      if (res.ok) {
        setPath('')
        setFile(null)
        if (fileInput.current) fileInput.current.value = ''
      }
    } catch (err) {
      setStatus({ kind: 'err', text: `Upload failed: ${(err as Error).message}` })
    }
  }

  const pending = status.kind === 'pending'

  return (
    <div className="grid min-h-screen place-items-center bg-zinc-100 p-4 text-[15px] text-zinc-900 antialiased dark:bg-zinc-950 dark:text-zinc-50">
      <form
        className="w-full max-w-110 rounded-2xl border border-zinc-200 bg-white p-8 shadow-[0_1px_2px_rgba(0,0,0,0.04),0_12px_32px_rgba(0,0,0,0.06)] dark:border-zinc-800 dark:bg-zinc-900"
        onSubmit={onSubmit}
      >
        <h1 className="mb-1 text-xl font-semibold tracking-tight">Upload a file</h1>
        <p className="mb-7 text-zinc-500 dark:text-zinc-400">Store a file at any path.</p>

        <label className="mb-1.5 block text-[13px] font-semibold" htmlFor="path">
          Path
        </label>
        <input
          className="w-full rounded-lg border border-zinc-300 bg-transparent px-3 py-2.5 transition placeholder:text-zinc-400 focus:border-zinc-900 focus:ring-3 focus:ring-zinc-900/10 focus:outline-none dark:border-zinc-700 dark:placeholder:text-zinc-500 dark:focus:border-zinc-50 dark:focus:ring-zinc-50/10"
          type="text"
          id="path"
          value={path}
          onChange={(e) => setPath(e.target.value)}
          placeholder="images/2026/photo.jpg"
          autoComplete="off"
        />
        <p className="mt-1.5 mb-5.5 text-[13px] text-zinc-500 dark:text-zinc-400">
          Leave empty to use the file's name. End with{' '}
          <code className="rounded bg-zinc-900/7 px-1.5 py-px text-xs dark:bg-zinc-50/7">/</code> to put it in a
          folder.
        </p>

        <label
          className={`mb-5.5 flex cursor-pointer flex-col items-center gap-2.5 rounded-xl border-[1.5px] px-4 py-8 text-center text-zinc-500 transition hover:border-zinc-900 hover:bg-zinc-900/3 dark:text-zinc-400 dark:hover:border-zinc-50 dark:hover:bg-zinc-50/3 ${
            over ? 'border-zinc-900 bg-zinc-900/3 dark:border-zinc-50 dark:bg-zinc-50/3' : 'border-zinc-300 dark:border-zinc-700'
          } ${file ? 'border-solid' : 'border-dashed'}`}
          onDragOver={(e) => {
            e.preventDefault()
            setOver(true)
          }}
          onDragLeave={() => setOver(false)}
          onDrop={onDrop}
        >
          <input
            ref={fileInput}
            className="hidden"
            type="file"
            onChange={(e) => setFile(e.target.files?.[0] ?? null)}
          />
          <svg
            className="size-7 stroke-current"
            viewBox="0 0 24 24"
            fill="none"
            strokeWidth="1.5"
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden="true"
          >
            <path d="M12 16V4m0 0-4 4m4-4 4 4" />
            <path d="M4 16v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2" />
          </svg>
          {file ? (
            <span>
              <strong className="font-semibold break-all text-zinc-900 dark:text-zinc-50">{file.name}</strong>
              <br />
              <span className="text-[13px]">{formatSize(file.size)}</span>
            </span>
          ) : (
            <span>
              Drop a file here or <strong className="font-semibold text-zinc-900 dark:text-zinc-50">browse</strong>
            </span>
          )}
        </label>

        <button
          className="w-full cursor-pointer rounded-lg bg-zinc-900 p-2.75 font-semibold text-zinc-50 transition enabled:hover:bg-zinc-700 disabled:cursor-default disabled:opacity-50 dark:bg-zinc-50 dark:text-zinc-900 dark:enabled:hover:bg-zinc-300"
          type="submit"
          disabled={!file || pending}
        >
          {pending ? 'Uploading…' : 'Upload'}
        </button>
        <p
          className={`mt-4 min-h-[1.5em] text-sm break-all ${
            status.kind === 'ok' ? 'text-green-700 dark:text-green-400' : status.kind === 'err' ? 'text-red-700 dark:text-red-400' : ''
          }`}
        >
          {pending ? '' : status.text}
        </p>
      </form>
    </div>
  )
}

export default App
