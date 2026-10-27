import { describe, expect, it } from 'vitest'
import {
  baseName,
  breadcrumbs,
  extension,
  fileKind,
  formatBytes,
  formatClock,
  formatDuration,
  formatRelative,
  parentPrefix,
  shortUserAgent,
  validBucketName,
} from './format'

describe('formatBytes', () => {
  it.each([
    [0, '0 B'],
    [1023, '1023 B'],
    [1024, '1.0 KB'],
    [1536, '1.5 KB'],
    [20 * 1024, '20 KB'],
    [5 * 1024 * 1024, '5.0 MB'],
    [3 * 1024 ** 4, '3.0 TB'],
    [-1, '—'],
    [Number.NaN, '—'],
  ])('%d → %s', (n, want) => expect(formatBytes(n)).toBe(want))
})

describe('formatRelative', () => {
  const now = new Date('2026-10-27T12:00:00Z')
  it.each([
    ['2026-10-27T11:59:55Z', 'just now'],
    ['2026-10-27T11:59:30Z', '30s ago'],
    ['2026-10-27T11:15:00Z', '45m ago'],
    ['2026-10-27T07:00:00Z', '5h ago'],
    ['2026-10-25T12:00:00Z', '2d ago'],
  ])('%s → %s', (d, want) => expect(formatRelative(d, now)).toBe(want))

  it('falls back to a date after a week', () => {
    expect(formatRelative('2026-09-01T12:00:00Z', now)).toMatch(/Sep/)
    expect(formatRelative('2024-09-01T12:00:00Z', now)).toMatch(/2024/)
  })
})

describe('formatDuration', () => {
  it.each([
    [0.25, '250µs'],
    [3.456, '3.5ms'],
    [42.4, '42ms'],
    [1500, '1.50s'],
  ])('%d → %s', (ms, want) => expect(formatDuration(ms)).toBe(want))
})

it('formatClock pads to milliseconds', () => {
  expect(formatClock(new Date(2026, 0, 2, 3, 4, 5, 6))).toBe('03:04:05.006')
})

describe('fileKind', () => {
  it.each([
    ['photo.JPG', '', 'image'],
    ['clip.mp4', '', 'video'],
    ['song.mp3', '', 'audio'],
    ['doc.pdf', '', 'pdf'],
    ['notes.md', '', 'text'],
    ['data.json', '', 'code'],
    ['app.blade', '', 'code'],
    ['backup.tgz', '', 'archive'],
    ['noext', 'image/webp', 'image'],
    ['noext', 'application/pdf', 'pdf'],
    ['noext', 'text/csv; charset=utf-8', 'text'],
    ['noext', 'application/vnd.api+json', 'code'],
    ['noext', 'application/octet-stream', 'other'],
    ['.env', '', 'other'],
  ])('%s (%s) → %s', (name, type, want) => expect(fileKind(name, type)).toBe(want))

  it('extension ignores dotfiles', () => {
    expect(extension('.gitignore')).toBe('')
    expect(extension('a.tar.GZ')).toBe('gz')
  })
})

describe('shortUserAgent', () => {
  it.each([
    ['aws-sdk-php/3.336.0 OS/Darwin GuzzleHttp/7 lang/php#8.4', 'aws-sdk-php 3.336.0'],
    ['aws-cli/2.17.0 md/awscrt', 'aws-cli 2.17.0'],
    ['aws-sdk-go-v2/1.36.0 os/macos', 'aws-sdk-go-v2 1.36.0'],
    ['go-objectstore-selftest', 'self-test'],
    ['curl/8.7.1', 'curl 8.7.1'],
    ['Mozilla/5.0 (Macintosh)', 'browser'],
    ['custom-client/1.0 extra', 'custom-client/1.0'],
    ['', '—'],
  ])('%s → %s', (ua, want) => expect(shortUserAgent(ua)).toBe(want))
})

describe('prefix helpers', () => {
  it('breadcrumbs', () => {
    expect(breadcrumbs('')).toEqual([])
    expect(breadcrumbs('a/b/')).toEqual([
      { name: 'a', prefix: 'a/' },
      { name: 'b', prefix: 'a/b/' },
    ])
  })
  it('parentPrefix', () => {
    expect(parentPrefix('a/b/')).toBe('a/')
    expect(parentPrefix('a/')).toBe('')
    expect(parentPrefix('a/b/c.txt')).toBe('a/b/')
    expect(parentPrefix('c.txt')).toBe('')
  })
  it('baseName', () => {
    expect(baseName('a/b/c.txt')).toBe('c.txt')
    expect(baseName('a/b/')).toBe('b')
    expect(baseName('top')).toBe('top')
  })
})

describe('validBucketName', () => {
  it.each([
    ['laravel', null],
    ['my-app.uploads', null],
    ['ab', 'Use 3–63 characters'],
    ['Has_Upper', 'Only lowercase letters, numbers, dots and hyphens'],
    ['-dash', 'Start and end with a letter or number'],
    ['a..b', 'No consecutive dots'],
    ['10.0.0.1', 'Must not look like an IP address'],
  ])('%s', (name, want) => expect(validBucketName(name)).toBe(want))
})
