import { useEffect, useRef } from 'react'

export const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)

/** Display label for the platform's command key. */
export const modKey = isMac ? '⌘' : 'Ctrl'

function isTyping(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  return target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName)
}

/**
 * Matches a key event against a combo such as "j", "shift+?", "mod+k" or
 * "mod+backspace". "mod" is ⌘ on macOS and Ctrl elsewhere.
 */
export function matches(e: KeyboardEvent, combo: string): boolean {
  const parts = combo.toLowerCase().split('+')
  const key = parts.pop()!
  const want = {
    mod: parts.includes('mod'),
    shift: parts.includes('shift'),
    alt: parts.includes('alt'),
  }
  const mod = isMac ? e.metaKey : e.ctrlKey
  if (mod !== want.mod || e.altKey !== want.alt) return false
  // Shift is implied by symbols like "?", so only enforce it when asked.
  if (want.shift && !e.shiftKey) return false
  const pressed = e.key.toLowerCase()
  return pressed === key || (key === 'space' && pressed === ' ') || (key === 'esc' && pressed === 'escape')
}

export type Bindings = Record<string, (e: KeyboardEvent) => void>

/**
 * Binds global shortcuts. Plain keys are ignored while typing in a field;
 * combos with mod and Escape always fire.
 */
export function useHotkeys(bindings: Bindings, enabled = true) {
  const ref = useRef(bindings)
  useEffect(() => {
    ref.current = bindings
  })
  useEffect(() => {
    if (!enabled) return
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented) return
      for (const [combo, fn] of Object.entries(ref.current)) {
        if (!matches(e, combo)) continue
        const global = combo.includes('mod+') || combo === 'escape' || combo === 'esc'
        if (!global && isTyping(e.target)) continue
        e.preventDefault()
        fn(e)
        return
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [enabled])
}
