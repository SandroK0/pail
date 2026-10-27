import { describe, expect, it } from 'vitest'
import { event } from '../test/fakeApi'
import { filterEvents, operationCounts } from './Activity'

const log = [
  event({ op: 'PutObject', method: 'PUT', key: 'a.txt' }),
  event({ op: 'GetObject', key: 'a.txt' }),
  event({ op: 'GetObject', key: 'missing.txt', status: 404, errorCode: 'NoSuchKey' }),
  event({ op: 'HeadObject', method: 'HEAD', key: 'b.txt' }),
  event({ op: 'DeleteObject', method: 'DELETE', key: 'a.txt', status: 204 }),
].map((e) => ({ ...e, path: `/laravel/${e.key}` }))
const keys = (events: typeof log) => events.map((e) => `${e.op} ${e.key}`)

describe('filterEvents', () => {
  it('keeps everything with no filters', () => {
    expect(filterEvents(log, 'all', '')).toHaveLength(5)
  })

  it('filters by one or more operations', () => {
    expect(keys(filterEvents(log, 'all', '', new Set(['GetObject'])))).toEqual(['GetObject a.txt', 'GetObject missing.txt'])
    expect(filterEvents(log, 'all', '', new Set(['GetObject', 'HeadObject']))).toHaveLength(3)
  })

  it('finds a request by ID, as pasted from an app error', () => {
    const withIds = log.map((e, i) => ({ ...e, requestId: `REQ${i}ABC` }))
    expect(filterEvents(withIds, 'all', 'req2abc').map((e) => e.key)).toEqual(['missing.txt'])
  })

  it('combines operations with status and search', () => {
    expect(keys(filterEvents(log, 'errors', '', new Set(['GetObject'])))).toEqual(['GetObject missing.txt'])
    expect(filterEvents(log, 'writes', '', new Set(['GetObject']))).toEqual([])
    expect(keys(filterEvents(log, 'all', 'a.txt', new Set(['GetObject', 'DeleteObject'])))).toEqual([
      'GetObject a.txt',
      'DeleteObject a.txt',
    ])
  })
})

it('operationCounts orders by frequency, then name', () => {
  expect(operationCounts(log)).toEqual([
    ['GetObject', 2],
    ['DeleteObject', 1],
    ['HeadObject', 1],
    ['PutObject', 1],
  ])
  expect(operationCounts([])).toEqual([])
})
