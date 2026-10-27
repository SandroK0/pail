/** A file to upload and the path to store it under, relative to the drop target. */
export type PickedFile = { file: File; path: string }

// Minimal shapes of the (non-standard but universal) FileSystem entry API.
type Entry = {
  isFile: boolean
  isDirectory: boolean
  name: string
  file?: (ok: (f: File) => void, err: (e: unknown) => void) => void
  createReader?: () => { readEntries: (ok: (entries: Entry[]) => void, err: (e: unknown) => void) => void }
}

async function walk(entry: Entry, base: string, out: PickedFile[]): Promise<void> {
  if (entry.isFile && entry.file) {
    const file = await new Promise<File>((ok, err) => entry.file!(ok, err))
    out.push({ file, path: base + entry.name })
    return
  }
  if (entry.isDirectory && entry.createReader) {
    const reader = entry.createReader()
    // readEntries returns batches until it returns an empty one.
    for (;;) {
      const batch = await new Promise<Entry[]>((ok, err) => reader.readEntries(ok, err))
      if (batch.length === 0) break
      for (const child of batch) await walk(child, base + entry.name + '/', out)
    }
  }
}

/** Collects dropped files, descending into dropped folders and keeping their structure. */
export async function filesFromDataTransfer(dt: DataTransfer): Promise<PickedFile[]> {
  const entries: Entry[] = []
  for (const item of Array.from(dt.items ?? [])) {
    const entry = (item as DataTransferItem & { webkitGetAsEntry?: () => Entry | null }).webkitGetAsEntry?.()
    if (entry) entries.push(entry)
  }
  if (entries.length === 0) {
    return Array.from(dt.files ?? []).map((file) => ({ file, path: file.name }))
  }
  const out: PickedFile[] = []
  for (const entry of entries) await walk(entry, '', out)
  return out
}

export function filesFromInput(list: FileList | null): PickedFile[] {
  return Array.from(list ?? []).map((file) => ({
    file,
    path: (file as File & { webkitRelativePath?: string }).webkitRelativePath || file.name,
  }))
}

export function dragHasFiles(e: { dataTransfer: DataTransfer | null }): boolean {
  return Array.from(e.dataTransfer?.types ?? []).includes('Files')
}
