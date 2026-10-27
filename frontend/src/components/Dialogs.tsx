import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Check, Copy } from 'lucide-react'
import { useState } from 'react'
import type { FormEvent } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { toast } from 'sonner'
import { useUI } from '../app/ui'
import type { DialogState } from '../app/ui'
import { api, keys } from '../lib/api'
import { baseName, parentPrefix, validBucketName } from '../lib/format'
import { modKey } from '../lib/hotkeys'
import { Button, Input, Kbd, Modal, cx } from './ui'

export function bucketPath(bucket: string, prefix = '') {
  const segs = prefix.split('/').filter(Boolean).map(encodeURIComponent)
  return `/b/${encodeURIComponent(bucket)}${segs.length ? '/' + segs.join('/') : ''}`
}

/** Renders whichever dialog is open in the UI context. */
export function Dialogs() {
  const { dialog, closeDialog } = useUI()
  if (!dialog) return null
  const props = { onClose: closeDialog }
  switch (dialog.type) {
    case 'createBucket':
      return <CreateBucketDialog {...props} />
    case 'deleteBucket':
      return <DeleteBucketDialog {...props} bucket={dialog.bucket} />
    case 'newFolder':
      return <NewFolderDialog {...props} {...dialog} />
    case 'rename':
      return <RenameDialog {...props} {...dialog} />
    case 'delete':
      return <DeleteDialog {...props} {...dialog} />
    case 'presign':
      return <PresignDialog {...props} {...dialog} />
    case 'shortcuts':
      return <ShortcutsDialog {...props} />
  }
}

type Props<T extends DialogState['type']> = Omit<Extract<DialogState, { type: T }>, 'type'> & { onClose: () => void }

function ErrorText({ error }: { error: unknown }) {
  if (!error) return null
  return <p className="mt-2 text-danger">{(error as Error).message}</p>
}

function CreateBucketDialog({ onClose }: Props<'createBucket'>) {
  const [name, setName] = useState('')
  const navigate = useNavigate()
  const qc = useQueryClient()
  const create = useMutation({
    mutationFn: () => api.createBucket(name),
    onSuccess: (b) => {
      qc.invalidateQueries({ queryKey: keys.buckets })
      toast.success(`Created bucket ${b.name}`)
      onClose()
      navigate(bucketPath(b.name))
    },
  })
  const problem = name ? validBucketName(name) : null
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (name && !problem) create.mutate()
  }
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title="New bucket"
      description="Buckets are top-level containers, like AWS_BUCKET in your .env."
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" type="submit" form="create-bucket" disabled={!name || !!problem || create.isPending}>
            Create bucket
          </Button>
        </>
      }
    >
      <form id="create-bucket" onSubmit={submit}>
        <Input
          autoFocus
          aria-label="Bucket name"
          placeholder="my-app-uploads"
          value={name}
          onChange={(e) => setName(e.target.value.toLowerCase())}
          className="font-mono"
        />
        <p className={cx('mt-2 text-xs', problem ? 'text-warning' : 'text-faint')}>
          {problem ?? 'Lowercase letters, numbers, dots and hyphens.'}
        </p>
        <ErrorText error={create.error} />
      </form>
    </Modal>
  )
}

function DeleteBucketDialog({ bucket, onClose }: Props<'deleteBucket'>) {
  const [confirm, setConfirm] = useState('')
  const navigate = useNavigate()
  const qc = useQueryClient()
  const del = useMutation({
    mutationFn: () => api.deleteBucket(bucket, true),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: keys.buckets })
      qc.removeQueries({ queryKey: keys.listing(bucket) })
      toast.success(`Deleted bucket ${bucket}`)
      onClose()
      navigate('/')
    },
  })
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Delete ${bucket}?`}
      description="This permanently deletes the bucket and every object in it."
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="danger" disabled={confirm !== bucket || del.isPending} onClick={() => del.mutate()}>
            Delete bucket
          </Button>
        </>
      }
    >
      <label className="mb-1.5 block text-muted">
        Type <span className="font-mono text-fg">{bucket}</span> to confirm
      </label>
      <Input autoFocus aria-label="Confirm bucket name" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
      <ErrorText error={del.error} />
    </Modal>
  )
}

function NewFolderDialog({ bucket, prefix, onClose }: Props<'newFolder'>) {
  const [name, setName] = useState('')
  const qc = useQueryClient()
  const create = useMutation({
    mutationFn: () => api.createFolder(bucket, prefix + name),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: keys.listing(bucket) })
      onClose()
    },
  })
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title="New folder"
      description={
        <>
          In <span className="font-mono text-fg">{bucket}/{prefix}</span>
        </>
      }
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" type="submit" form="new-folder" disabled={!name.trim() || create.isPending}>
            Create folder
          </Button>
        </>
      }
    >
      <form
        id="new-folder"
        onSubmit={(e) => {
          e.preventDefault()
          if (name.trim()) create.mutate()
        }}
      >
        <Input autoFocus aria-label="Folder name" placeholder="avatars" value={name} onChange={(e) => setName(e.target.value)} />
        <ErrorText error={create.error} />
      </form>
    </Modal>
  )
}

function RenameDialog({ bucket, from, onClose }: Props<'rename'>) {
  const isFolder = from.endsWith('/')
  const [to, setTo] = useState(from)
  const qc = useQueryClient()
  const [, setParams] = useSearchParams()
  const move = useMutation({
    mutationFn: () => api.move(bucket, from, to),
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: keys.listing(bucket) })
      qc.removeQueries({ queryKey: keys.object(bucket, from) })
      toast.success(isFolder ? `Moved ${res.moved} objects` : `Renamed to ${baseName(to)}`)
      if (!isFolder) {
        setParams((p) => {
          if (p.get('preview') === from) p.set('preview', to)
          return p
        })
      }
      onClose()
    },
  })
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={isFolder ? 'Move folder' : 'Rename or move'}
      description="Edit the full key. Changing the folder part moves it."
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" type="submit" form="rename" disabled={!to || to === from || move.isPending}>
            {isFolder ? 'Move' : 'Rename'}
          </Button>
        </>
      }
    >
      <form
        id="rename"
        onSubmit={(e) => {
          e.preventDefault()
          if (to && to !== from) move.mutate()
        }}
      >
        <Input
          autoFocus
          aria-label="New key"
          value={to}
          onChange={(e) => setTo(e.target.value)}
          onFocus={(e) => {
            // Select just the name, like a file manager.
            const start = parentPrefix(from).length
            e.target.setSelectionRange(start, isFolder ? from.length - 1 : from.length)
          }}
          className="font-mono"
        />
        <ErrorText error={move.error} />
      </form>
    </Modal>
  )
}

function DeleteDialog({ bucket, keys: objectKeys, prefixes, onDone, onClose }: Props<'delete'>) {
  const qc = useQueryClient()
  const del = useMutation({
    mutationFn: () => api.remove(bucket, objectKeys, prefixes),
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: keys.listing(bucket) })
      qc.invalidateQueries({ queryKey: keys.buckets })
      toast.success(`Deleted ${res.deleted} ${res.deleted === 1 ? 'object' : 'objects'}`)
      onDone?.()
      onClose()
    },
  })
  const names = [...prefixes, ...objectKeys]
  const label =
    names.length === 1 ? baseName(names[0]) + (prefixes.length ? '/' : '') : `${names.length} items`
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Delete ${label}?`}
      description={
        prefixes.length > 0
          ? 'Folders are deleted with everything inside them. This cannot be undone.'
          : 'This cannot be undone.'
      }
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="danger" autoFocus disabled={del.isPending} onClick={() => del.mutate()}>
            Delete
          </Button>
        </>
      }
    >
      {names.length > 1 && (
        <ul className="max-h-40 overflow-y-auto rounded-md border border-line bg-raised p-2 font-mono text-xs text-muted">
          {names.map((n) => (
            <li key={n} className="truncate">
              {n}
            </li>
          ))}
        </ul>
      )}
      <ErrorText error={del.error} />
    </Modal>
  )
}

const expiries = [
  { label: '5 minutes', seconds: 300 },
  { label: '15 minutes', seconds: 900 },
  { label: '1 hour', seconds: 3600 },
  { label: '1 day', seconds: 86400 },
  { label: '7 days', seconds: 604800 },
]

export function CopyField({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <div className="flex items-start gap-2 rounded-md border border-line bg-raised p-2">
      <code className="min-w-0 flex-1 font-mono text-xs leading-5 break-all whitespace-pre-wrap text-fg">{value}</code>
      <Button
        size="sm"
        variant="ghost"
        aria-label={label}
        onClick={() => {
          navigator.clipboard?.writeText(value)
          setCopied(true)
          setTimeout(() => setCopied(false), 1200)
        }}
      >
        {copied ? <Check className="size-3.5 text-success" /> : <Copy className="size-3.5" />}
      </Button>
    </div>
  )
}

function PresignDialog({ bucket, objectKey, onClose }: Props<'presign'>) {
  const [method, setMethod] = useState<'GET' | 'PUT'>('GET')
  const [expiresIn, setExpiresIn] = useState(900)
  const presign = useMutation({ mutationFn: () => api.presign(bucket, objectKey, method, expiresIn) })
  return (
    <Modal
      open
      onOpenChange={(o) => !o && onClose()}
      title="Presigned URL"
      description={<span className="font-mono">{objectKey}</span>}
      className="max-w-lg"
      footer={
        <>
          <Button onClick={onClose}>Done</Button>
          <Button variant="primary" onClick={() => presign.mutate()} disabled={presign.isPending}>
            Generate
          </Button>
        </>
      }
    >
      <div className="flex items-center gap-3">
        <div role="radiogroup" aria-label="Method" className="flex rounded-md border border-line-strong p-0.5">
          {(['GET', 'PUT'] as const).map((m) => (
            <button
              key={m}
              type="button"
              role="radio"
              aria-checked={method === m}
              onClick={() => {
                setMethod(m)
                presign.reset()
              }}
              className={cx(
                'h-6 cursor-pointer rounded px-2.5 text-xs font-medium',
                method === m ? 'bg-active text-fg' : 'text-muted hover:text-fg',
              )}
            >
              {m === 'GET' ? 'Download (GET)' : 'Upload (PUT)'}
            </button>
          ))}
        </div>
        <select
          aria-label="Expires in"
          value={expiresIn}
          onChange={(e) => {
            setExpiresIn(Number(e.target.value))
            presign.reset()
          }}
          className="h-7 rounded-md border border-line-strong bg-panel px-2 text-fg"
        >
          {expiries.map((x) => (
            <option key={x.seconds} value={x.seconds}>
              Expires in {x.label}
            </option>
          ))}
        </select>
      </div>
      {presign.data && (
        <div className="mt-3 space-y-2">
          <CopyField value={presign.data.url} label="Copy URL" />
          {method === 'PUT' && (
            <CopyField value={`curl -X PUT --upload-file ./file '${presign.data.url}'`} label="Copy curl command" />
          )}
          <p className="text-xs text-faint">Same as Storage::temporary{method === 'PUT' ? 'Upload' : ''}Url() in Laravel.</p>
        </div>
      )}
      <ErrorText error={presign.error} />
    </Modal>
  )
}

const shortcutGroups: { title: string; items: [string[], string][] }[] = [
  {
    title: 'General',
    items: [
      [[modKey, 'K'], 'Command menu'],
      [['?'], 'Keyboard shortcuts'],
      [['Esc'], 'Close / clear selection'],
    ],
  },
  {
    title: 'Browsing',
    items: [
      [['J'], 'Next item'],
      [['K'], 'Previous item'],
      [['↵'], 'Open folder / preview file'],
      [['⌫'], 'Go up a folder'],
      [['Space'], 'Toggle preview'],
      [['X'], 'Select item'],
      [[modKey, 'A'], 'Select all'],
      [['/'], 'Filter'],
    ],
  },
  {
    title: 'Actions',
    items: [
      [['U'], 'Upload files'],
      [['N'], 'New folder'],
      [['R'], 'Rename / move'],
      [['C'], 'Copy URL'],
      [['P'], 'Presigned URL'],
      [[modKey, '⌫'], 'Delete'],
    ],
  },
]

function ShortcutsDialog({ onClose }: Props<'shortcuts'>) {
  return (
    <Modal open onOpenChange={(o) => !o && onClose()} title="Keyboard shortcuts" className="max-w-lg">
      <div className="grid grid-cols-2 gap-x-8 gap-y-5">
        {shortcutGroups.map((g) => (
          <section key={g.title} className={g.title === 'Browsing' ? 'row-span-2' : ''}>
            <h3 className="mb-2 text-xs font-medium text-faint">{g.title}</h3>
            <ul className="space-y-1.5">
              {g.items.map(([combo, label]) => (
                <li key={label} className="flex items-center justify-between gap-4">
                  <span className="text-muted">{label}</span>
                  <span className="flex gap-0.5">
                    {combo.map((k) => (
                      <Kbd key={k}>{k}</Kbd>
                    ))}
                  </span>
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
    </Modal>
  )
}
