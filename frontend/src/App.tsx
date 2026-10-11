import { useRef, useState } from 'react'
import type { DragEvent, FormEvent } from 'react'
import './App.css'

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
    <form className="card" onSubmit={onSubmit}>
      <h1>Upload a file</h1>
      <p className="sub">Store a file at any path.</p>

      <label className="field-label" htmlFor="path">
        Path
      </label>
      <input
        className="text-input"
        type="text"
        id="path"
        value={path}
        onChange={(e) => setPath(e.target.value)}
        placeholder="images/2026/photo.jpg"
        autoComplete="off"
      />
      <p className="hint">
        Leave empty to use the file's name. End with <code>/</code> to put it in a folder.
      </p>

      <label
        className={`drop${over ? ' over' : ''}${file ? ' has-file' : ''}`}
        onDragOver={(e) => {
          e.preventDefault()
          setOver(true)
        }}
        onDragLeave={() => setOver(false)}
        onDrop={onDrop}
      >
        <input
          ref={fileInput}
          type="file"
          onChange={(e) => setFile(e.target.files?.[0] ?? null)}
        />
        <svg viewBox="0 0 24 24" fill="none" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M12 16V4m0 0-4 4m4-4 4 4" />
          <path d="M4 16v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2" />
        </svg>
        {file ? (
          <span>
            <strong>{file.name}</strong>
            <br />
            <span className="file-size">{formatSize(file.size)}</span>
          </span>
        ) : (
          <span>
            Drop a file here or <strong>browse</strong>
          </span>
        )}
      </label>

      <button className="submit" type="submit" disabled={!file || pending}>
        {pending ? 'Uploading…' : 'Upload'}
      </button>
      <p className={`status ${status.kind}`}>{status.kind === 'pending' ? '' : status.text}</p>
    </form>
  )
}

export default App
