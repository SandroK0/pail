import { describe, expect, it } from 'vitest'
import { dragHasFiles, filesFromDataTransfer, filesFromInput } from './files'

const file = (name: string) => new File(['x'], name)

function fileEntry(name: string) {
  return { isFile: true, isDirectory: false, name, file: (ok: (f: File) => void) => ok(file(name)) }
}

// Returns children in batches of two, like Chrome's 100-entry batches.
function dirEntry(name: string, children: unknown[]) {
  return {
    isFile: false,
    isDirectory: true,
    name,
    createReader: () => {
      let i = 0
      return {
        readEntries: (ok: (e: unknown[]) => void) => {
          ok(children.slice(i, i + 2))
          i += 2
        },
      }
    },
  }
}

function transfer(entries: unknown[]): DataTransfer {
  return {
    items: entries.map((e) => ({ webkitGetAsEntry: () => e })),
    files: [],
  } as unknown as DataTransfer
}

describe('filesFromDataTransfer', () => {
  it('walks dropped folders and keeps their structure', async () => {
    const dt = transfer([
      fileEntry('top.txt'),
      dirEntry('photos', [fileEntry('a.jpg'), fileEntry('b.jpg'), fileEntry('c.jpg'), dirEntry('2026', [fileEntry('d.jpg')])]),
    ])
    const got = await filesFromDataTransfer(dt)
    expect(got.map((f) => f.path)).toEqual(['top.txt', 'photos/a.jpg', 'photos/b.jpg', 'photos/c.jpg', 'photos/2026/d.jpg'])
    expect(got[1].file.name).toBe('a.jpg')
  })

  it('falls back to plain files without the entry API', async () => {
    const dt = { items: [], files: [file('one.txt'), file('two.txt')] } as unknown as DataTransfer
    expect((await filesFromDataTransfer(dt)).map((f) => f.path)).toEqual(['one.txt', 'two.txt'])
  })
})

it('filesFromInput uses webkitRelativePath when picking a folder', () => {
  const nested = file('a.txt')
  Object.defineProperty(nested, 'webkitRelativePath', { value: 'dir/a.txt' })
  const list = [file('b.txt'), nested] as unknown as FileList
  expect(filesFromInput(list).map((f) => f.path)).toEqual(['b.txt', 'dir/a.txt'])
  expect(filesFromInput(null)).toEqual([])
})

it('dragHasFiles', () => {
  expect(dragHasFiles({ dataTransfer: { types: ['Files'] } as unknown as DataTransfer })).toBe(true)
  expect(dragHasFiles({ dataTransfer: { types: ['text/plain'] } as unknown as DataTransfer })).toBe(false)
  expect(dragHasFiles({ dataTransfer: null })).toBe(false)
})
