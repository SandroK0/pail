import { useCallback, useEffect, useState } from 'react'

export type ThemePref = 'light' | 'dark' | 'system'

const STORAGE_KEY = 'objectstore:theme'

function readPref(): ThemePref {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    if (v === 'light' || v === 'dark' || v === 'system') return v
  } catch {
    // storage unavailable
  }
  return 'system'
}

function systemDark(): boolean {
  return typeof window.matchMedia === 'function' && window.matchMedia('(prefers-color-scheme: dark)').matches
}

export function resolveTheme(pref: ThemePref): 'light' | 'dark' {
  return pref === 'system' ? (systemDark() ? 'dark' : 'light') : pref
}

export function applyTheme(pref: ThemePref) {
  document.documentElement.classList.toggle('dark', resolveTheme(pref) === 'dark')
}

export function useTheme() {
  const [pref, setPref] = useState<ThemePref>(readPref)

  useEffect(() => {
    applyTheme(pref)
    try {
      localStorage.setItem(STORAGE_KEY, pref)
    } catch {
      // storage unavailable
    }
    if (pref !== 'system' || typeof window.matchMedia !== 'function') return
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const onChange = () => applyTheme('system')
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [pref])

  const toggle = useCallback(() => setPref((p) => (resolveTheme(p) === 'dark' ? 'light' : 'dark')), [])
  return { pref, resolved: resolveTheme(pref), setPref, toggle }
}
