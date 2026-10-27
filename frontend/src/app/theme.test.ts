import { describe, expect, it, vi } from 'vitest'
import { locationTarget } from '../components/CommandPalette'
import { applyTheme, resolveTheme } from './theme'

describe('theme', () => {
  it('resolves system preference', () => {
    vi.spyOn(window, 'matchMedia').mockReturnValue({ matches: true } as MediaQueryList)
    expect(resolveTheme('system')).toBe('dark')
    expect(resolveTheme('light')).toBe('light')
  })

  it('applies the dark class', () => {
    applyTheme('dark')
    expect(document.documentElement).toHaveClass('dark')
    applyTheme('light')
    expect(document.documentElement).not.toHaveClass('dark')
  })
})

describe('locationTarget', () => {
  it.each([
    ['/b/laravel', { bucket: 'laravel', prefix: '' }],
    ['/b/laravel/avatars/2026', { bucket: 'laravel', prefix: 'avatars/2026/' }],
    ['/b/laravel/with%20space', { bucket: 'laravel', prefix: 'with space/' }],
    ['/activity', null],
  ])('%s', (path, want) => expect(locationTarget(path)).toEqual(want))
})
