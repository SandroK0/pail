import { fireEvent, render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { matches, useHotkeys } from './hotkeys'
import type { Bindings } from './hotkeys'

const key = (k: string, mods: Partial<KeyboardEventInit> = {}) => new KeyboardEvent('keydown', { key: k, ...mods })

describe('matches', () => {
  // jsdom is not a Mac, so "mod" is Ctrl.
  it.each([
    [key('j'), 'j', true],
    [key('J', { shiftKey: true }), 'j', true],
    [key('j', { ctrlKey: true }), 'j', false],
    [key('k', { ctrlKey: true }), 'mod+k', true],
    [key('k'), 'mod+k', false],
    [key('?', { shiftKey: true }), '?', true],
    [key('x'), 'shift+x', false],
    [key(' '), 'space', true],
    [key('Escape'), 'escape', true],
    [key('Backspace', { ctrlKey: true }), 'mod+backspace', true],
    [key('ArrowDown'), 'arrowdown', true],
    [key('a', { altKey: true }), 'a', false],
  ])('%o vs %s → %s', (e, combo, want) => expect(matches(e, combo)).toBe(want))
})

function Harness({ bindings, enabled }: { bindings: Bindings; enabled?: boolean }) {
  useHotkeys(bindings, enabled)
  return <input aria-label="field" />
}

describe('useHotkeys', () => {
  it('fires bindings and prevents the default action', () => {
    const j = vi.fn()
    render(<Harness bindings={{ j }} />)
    const e = key('j', { cancelable: true })
    window.dispatchEvent(e)
    expect(j).toHaveBeenCalledOnce()
    expect(e.defaultPrevented).toBe(true)
  })

  it('ignores plain keys while typing but not mod combos or Escape', () => {
    const j = vi.fn()
    const palette = vi.fn()
    const esc = vi.fn()
    const { getByLabelText } = render(<Harness bindings={{ j, 'mod+k': palette, escape: esc }} />)
    const input = getByLabelText('field')
    fireEvent.keyDown(input, { key: 'j' })
    fireEvent.keyDown(input, { key: 'k', ctrlKey: true })
    fireEvent.keyDown(input, { key: 'Escape' })
    expect(j).not.toHaveBeenCalled()
    expect(palette).toHaveBeenCalledOnce()
    expect(esc).toHaveBeenCalledOnce()
  })

  it('does nothing when disabled', () => {
    const j = vi.fn()
    render(<Harness bindings={{ j }} enabled={false} />)
    fireEvent.keyDown(window, { key: 'j' })
    expect(j).not.toHaveBeenCalled()
  })

  it('uses the latest handlers without re-binding', () => {
    const first = vi.fn()
    const second = vi.fn()
    const { rerender } = render(<Harness bindings={{ j: first }} />)
    rerender(<Harness bindings={{ j: second }} />)
    fireEvent.keyDown(window, { key: 'j' })
    expect(first).not.toHaveBeenCalled()
    expect(second).toHaveBeenCalledOnce()
  })

  it('skips events another handler already handled', () => {
    const j = vi.fn()
    render(<Harness bindings={{ j }} />)
    const e = key('j', { cancelable: true })
    e.preventDefault()
    window.dispatchEvent(e)
    expect(j).not.toHaveBeenCalled()
  })
})
