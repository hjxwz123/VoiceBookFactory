import { useRef, useState } from 'react'
import { GripVertical } from './icons'
import { cn, Tip } from './ui'

type DragState = { id: string; gap: number; x: number; y: number }

function scrollParent(el: HTMLElement | null): HTMLElement {
  for (let p = el?.parentElement; p; p = p.parentElement) {
    const oy = getComputedStyle(p).overflowY
    if ((oy === 'auto' || oy === 'scroll') && p.scrollHeight > p.clientHeight) return p
  }
  return (document.scrollingElement as HTMLElement) ?? document.documentElement
}

/**
 * Drag-to-reorder for ordinary (non-virtual) lists. Rows register with `rowRef(id)`, the handle gets
 * `handle(id)`. While dragging, `gap` is the slot the item would drop into; `onReorder` receives the new
 * order of ids (only when it actually changed). Holding the item near the edge scrolls the list; Esc
 * cancels.
 */
export function useDragReorder(ids: string[], onReorder: (next: string[]) => void) {
  const rows = useRef(new Map<string, HTMLElement>())
  const [drag, setDrag] = useState<DragState | null>(null)
  const cur = useRef<DragState | null>(null)

  const gapAt = (y: number) => {
    for (let i = 0; i < ids.length; i++) {
      const el = rows.current.get(ids[i])
      if (!el) continue
      const r = el.getBoundingClientRect()
      if (y < r.top + r.height / 2) return i
    }
    return ids.length
  }

  const start = (e: React.PointerEvent, id: string) => {
    if (e.button !== 0) return
    e.preventDefault()
    e.stopPropagation()
    const scroller = scrollParent(rows.current.get(id) ?? null)
    const set = (s: DragState | null) => {
      cur.current = s
      setDrag(s)
    }
    set({ id, gap: -1, x: e.clientX, y: e.clientY })
    document.body.style.cursor = 'grabbing'
    document.body.style.userSelect = 'none'
    let raf = 0
    const tick = () => {
      const d = cur.current
      if (!d) return
      const r = scroller === document.scrollingElement ? { top: 0, bottom: window.innerHeight } : scroller.getBoundingClientRect()
      const edge = 60
      const dy = d.y < r.top + edge ? -Math.ceil((r.top + edge - d.y) / 5) : d.y > r.bottom - edge ? Math.ceil((d.y - r.bottom + edge) / 5) : 0
      if (dy) {
        scroller.scrollTop += dy
        set({ ...d, gap: gapAt(d.y) })
      }
      raf = requestAnimationFrame(tick)
    }
    const move = (ev: PointerEvent) => cur.current && set({ ...cur.current, x: ev.clientX, y: ev.clientY, gap: gapAt(ev.clientY) })
    const end = (commit: boolean) => {
      cancelAnimationFrame(raf)
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
      window.removeEventListener('pointercancel', cancel)
      window.removeEventListener('keydown', key)
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
      const d = cur.current
      set(null)
      if (!commit || !d || d.gap < 0) return
      const rest = ids.filter((x) => x !== d.id)
      const at = ids.slice(0, d.gap).filter((x) => x !== d.id).length
      const next = [...rest.slice(0, at), d.id, ...rest.slice(at)]
      if (next.some((x, i) => x !== ids[i])) onReorder(next)
    }
    const up = () => end(true)
    const cancel = () => end(false)
    const key = (ev: KeyboardEvent) => ev.key === 'Escape' && end(false)
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
    window.addEventListener('pointercancel', cancel)
    window.addEventListener('keydown', key)
    raf = requestAnimationFrame(tick)
  }

  // the drop line is hidden where dropping would not change anything
  const from = drag ? ids.indexOf(drag.id) : -1
  const gap = drag && drag.gap >= 0 && drag.gap !== from && drag.gap !== from + 1 ? drag.gap : -1
  return {
    drag,
    rowRef: (id: string) => (el: HTMLElement | null) => {
      if (el) rows.current.set(id, el)
      else rows.current.delete(id)
    },
    start,
    /** classes for row i: dimmed while dragged, accent line where it would land */
    rowClass: (id: string, i: number) =>
      cn(drag?.id === id && 'opacity-40', gap === i && 'shadow-[inset_0_2px_0_var(--color-accent)]', gap === ids.length && i === ids.length - 1 && 'shadow-[inset_0_-2px_0_var(--color-accent)]'),
  }
}

/** The grip a row is dragged by (visible on row hover, always on touch screens). */
export function DragHandle({ onPointerDown, label = '拖动调整顺序', className }: { onPointerDown: (e: React.PointerEvent) => void; label?: string; className?: string }) {
  return (
    <Tip label={label}>
      <span
        role="button"
        aria-label={label}
        onPointerDown={onPointerDown}
        onClick={(e) => e.stopPropagation()}
        className={cn('grid h-7 w-5 shrink-0 cursor-grab touch-none place-items-center rounded text-faint transition-opacity hover:bg-sunken hover:text-ink active:cursor-grabbing [@media(hover:hover)]:opacity-0 [@media(hover:hover)]:group-hover:opacity-100', className)}
      >
        <GripVertical className="size-4" />
      </span>
    </Tip>
  )
}

/** Small pill following the pointer while dragging. */
export function DragGhost({ drag, label }: { drag: { x: number; y: number } | null; label: string }) {
  if (!drag) return null
  return (
    <div className="pointer-events-none fixed z-[70] flex items-center gap-1.5 rounded-full bg-ink px-3 py-1 text-[12px] text-bg shadow-[var(--shadow-pop)]" style={{ left: drag.x + 14, top: drag.y + 12 }}>
      <GripVertical className="size-3.5" />
      {label}
    </div>
  )
}
