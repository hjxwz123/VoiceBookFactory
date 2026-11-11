import { flushSync } from 'react-dom'
import { create } from 'zustand'

type Theme = 'light' | 'dark' | 'system'
const apply = (t: Theme) => {
  const dark = t === 'dark' || (t === 'system' && matchMedia('(prefers-color-scheme: dark)').matches)
  document.documentElement.classList.toggle('dark', dark)
  document.querySelector('meta[name=theme-color]')?.setAttribute('content', dark ? '#141413' : '#f5f4f0')
}
const initial = (() => {
  try {
    return JSON.parse(localStorage.getItem('vb-theme') || '"system"') as Theme
  } catch {
    return 'system' as Theme
  }
})()
const isDark = () => document.documentElement.classList.contains('dark')
matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
  apply(useTheme.getState().theme)
  useTheme.setState({ dark: isDark() })
})

let running: ViewTransition | null = null

/** theme: what the person chose; dark: what is showing now (system follows the OS). */
export const useTheme = create<{ theme: Theme; dark: boolean; setTheme: (t: Theme) => void; toggle: (from?: { x: number; y: number }) => void }>((set, get) => ({
  theme: initial,
  dark: isDark(),
  setTheme: (t) => {
    localStorage.setItem('vb-theme', JSON.stringify(t))
    apply(t)
    set({ theme: t, dark: isDark() })
  },
  // day ↔ night. Where the browser can, the new theme spreads out in a circle from the switch that was pressed
  // (a view transition); otherwise every colour cross-fades for a moment.
  toggle: (from) => {
    const root = document.documentElement
    const next = get().dark ? 'light' : 'dark'
    const reduce = matchMedia('(prefers-reduced-motion: reduce)').matches
    if (reduce) return get().setTheme(next)
    if (from && typeof document.startViewTransition === 'function') {
      running?.skipTransition() // a second press during the reveal finishes the first one at once
      root.classList.add('vt-theme')
      const t = document.startViewTransition(() => flushSync(() => get().setTheme(next)))
      running = t
      // the page cannot be clicked during a view transition, so a press anywhere finishes it at once
      const skip = () => t.skipTransition()
      addEventListener('pointerdown', skip, { once: true, capture: true })
      t.ready.then(() => {
        const r = Math.hypot(Math.max(from.x, innerWidth - from.x), Math.max(from.y, innerHeight - from.y))
        root.animate(
          { clipPath: [`circle(0px at ${from.x}px ${from.y}px)`, `circle(${r}px at ${from.x}px ${from.y}px)`] },
          { duration: 560, easing: 'cubic-bezier(0.65, 0, 0.35, 1)', pseudoElement: '::view-transition-new(root)' },
        )
      }).catch(() => {})
      t.finished.finally(() => {
        removeEventListener('pointerdown', skip, { capture: true })
        if (running !== t) return
        running = null
        root.classList.remove('vt-theme')
      })
      return
    }
    root.classList.add('theme-fade')
    get().setTheme(next)
    setTimeout(() => root.classList.remove('theme-fade'), 360)
  },
}))
