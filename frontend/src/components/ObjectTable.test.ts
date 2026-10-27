import { describe, expect, it } from 'vitest'
import type { ObjectSummary } from '../lib/api'
import { sortRows } from './ObjectTable'
import type { Row } from './ObjectTable'

const folder = (name: string): Row => ({ type: 'folder', id: name + '/', name, folder: { prefix: name + '/', name } })
const object = (name: string, size: number, lastModified: string): Row => ({
  type: 'object',
  id: name,
  name,
  object: { key: name, name, size, lastModified } as ObjectSummary,
})

const rows = [
  object('b.txt', 30, '2026-01-02T00:00:00Z'),
  folder('zeta'),
  object('a10.txt', 10, '2026-01-03T00:00:00Z'),
  object('a2.txt', 20, '2026-01-01T00:00:00Z'),
  folder('alpha'),
]
const names = (r: Row[]) => r.map((x) => x.name)

describe('sortRows', () => {
  it('puts folders first and sorts names naturally', () => {
    expect(names(sortRows(rows, { key: 'name', dir: 'asc' }))).toEqual(['alpha', 'zeta', 'a2.txt', 'a10.txt', 'b.txt'])
    expect(names(sortRows(rows, { key: 'name', dir: 'desc' }))).toEqual(['zeta', 'alpha', 'b.txt', 'a10.txt', 'a2.txt'])
  })

  it('sorts files by size and date, keeping folders first', () => {
    expect(names(sortRows(rows, { key: 'size', dir: 'desc' }))).toEqual(['alpha', 'zeta', 'b.txt', 'a2.txt', 'a10.txt'])
    expect(names(sortRows(rows, { key: 'modified', dir: 'asc' }))).toEqual(['alpha', 'zeta', 'a2.txt', 'b.txt', 'a10.txt'])
  })

  it('does not mutate its input', () => {
    const copy = [...rows]
    sortRows(rows, { key: 'size', dir: 'asc' })
    expect(rows).toEqual(copy)
  })
})
