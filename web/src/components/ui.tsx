import * as RDialog from '@radix-ui/react-dialog'
import * as RDropdown from '@radix-ui/react-dropdown-menu'
import * as RPopover from '@radix-ui/react-popover'
import * as RTooltip from '@radix-ui/react-tooltip'
import * as RSwitch from '@radix-ui/react-switch'
import * as RSlider from '@radix-ui/react-slider'
import * as RCheckbox from '@radix-ui/react-checkbox'
import * as RSelect from '@radix-ui/react-select'
import clsx from 'clsx'
import { AnimatePresence, motion } from 'framer-motion'
import { Calendar, Check, ChevronDown, ChevronLeft, ChevronRight, ChevronUp, HelpCircle, IconContext, Loader2, X } from '@/components/icons'
import React, { createContext, forwardRef, useContext, useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { create } from 'zustand'

export const cn = clsx

// ---------------- Button ----------------
type BtnVariant = 'primary' | 'secondary' | 'ghost' | 'danger' | 'signal' | 'link'
const btnBase = 'inline-flex items-center justify-center gap-1.5 font-medium whitespace-nowrap select-none transition-[background-color,border-color,color,box-shadow,transform] duration-150 active:scale-[0.98] disabled:opacity-50 disabled:pointer-events-none'
const btnVariants: Record<BtnVariant, string> = {
  primary: 'bg-accent text-accent-ink hover:bg-[var(--accent-hover)] shadow-[inset_0_-1px_0_rgb(0_0_0/0.15),0_1px_2px_rgb(28_26_22/0.12)]',
  secondary: 'bg-surface text-ink border border-line shadow-[var(--shadow-card)] hover:border-line-strong hover:bg-sunken/60',
  ghost: 'text-muted hover:text-ink hover:bg-sunken',
  danger: 'bg-danger text-white hover:brightness-110',
  signal: 'bg-signal text-[#2a1a00] hover:brightness-105',
  link: 'text-accent hover:underline underline-offset-4 px-0',
}
const btnSizes = { xs: 'h-7 px-2 text-xs rounded-[6px]', sm: 'h-8 px-3 text-[13px] rounded-control', md: 'h-9 px-4 text-sm rounded-control', lg: 'h-11 px-5 text-[15px] rounded-[9px]' }

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: BtnVariant
  size?: keyof typeof btnSizes
  loading?: boolean
  icon?: React.ReactNode
}
/** Tracks a click handler that returns a promise: busy right away (blocks double clicks), spinner after a
 *  short delay so fast responses do not flicker. A rejected promise is re-thrown so the global error
 *  toast still shows exactly once. */
export function useBusyHandler<E>(handler?: (e: E) => any) {
  const [busy, setBusy] = useState(false)
  const [spin, setSpin] = useState(false)
  const alive = useRef(true)
  const running = useRef(false)
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])
  const run = (e: E) => {
    if (running.current) return
    const r = handler?.(e)
    if (!r || typeof r.then !== 'function') return
    running.current = true
    setBusy(true)
    const t = setTimeout(() => alive.current && setSpin(true), 160)
    const done = () => {
      clearTimeout(t)
      running.current = false
      if (alive.current) {
        setBusy(false)
        setSpin(false)
      }
    }
    r.then(done, (err: unknown) => {
      done()
      throw err
    })
  }
  return { busy, spin, run }
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button({ variant = 'secondary', size = 'md', loading, icon, children, className, disabled, onClick, ...rest }, ref) {
  const { busy, spin, run } = useBusyHandler(onClick)
  const showSpin = loading || spin
  const spinner = <Loader2 className={cn('animate-spin', size === 'xs' ? 'size-3.5' : 'size-4')} />
  return (
    <button ref={ref} className={cn(btnBase, btnVariants[variant], variant !== 'link' && btnSizes[size], busy && 'cursor-progress', className)} disabled={disabled || loading} aria-busy={busy || loading || undefined} onClick={(e) => (busy ? e.preventDefault() : run(e))} {...rest}>
      {showSpin ? spinner : icon}
      {children}
    </button>
  )
})

export const IconButton = forwardRef<HTMLButtonElement, ButtonProps & { label: string }>(function IconButton({ label, className, size = 'sm', variant = 'ghost', ...rest }, ref) {
  const sz = { xs: 'size-7', sm: 'size-8', md: 'size-9', lg: 'size-11' }[size]
  return (
    <Tip label={label}>
      <Button ref={ref} aria-label={label} variant={variant} size={size} className={cn('!px-0', sz, className)} {...rest} />
    </Tip>
  )
})

export function Spinner({ className }: { className?: string }) {
  return <Loader2 className={cn('size-4 animate-spin text-faint', className)} />
}

// ---------------- Inputs ----------------
const fieldBase = 'w-full rounded-control border border-line bg-surface text-ink placeholder:text-faint transition-[border-color,box-shadow] duration-150 focus:outline-none focus:border-accent focus:ring-3 focus:ring-accent/15 disabled:opacity-60'

export const Input = forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement> & { icon?: React.ReactNode }>(function Input({ className, icon, ...rest }, ref) {
  if (icon)
    return (
      <div className="relative">
        <span className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-faint [&>svg]:size-4">{icon}</span>
        <input ref={ref} className={cn(fieldBase, 'h-9 pl-8 pr-3', className)} {...rest} />
      </div>
    )
  return <input ref={ref} className={cn(fieldBase, 'h-9 px-3', className)} {...rest} />
})

export const Textarea = forwardRef<HTMLTextAreaElement, React.TextareaHTMLAttributes<HTMLTextAreaElement>>(function Textarea({ className, ...rest }, ref) {
  return <textarea ref={ref} className={cn(fieldBase, 'px-3 py-2 leading-relaxed resize-y min-h-20', className)} {...rest} />
})

// Radix Select items cannot have an empty value; '' (e.g. "不使用", "全部") is mapped to a sentinel.
const EMPTY_OPTION = '__empty__'
const encOpt = (v: string) => (v === '' ? EMPTY_OPTION : v)

export function Select({ value, onChange, options, className, placeholder, disabled, size = 'md' }: { value: string; onChange: (v: string) => void; options: (readonly [string, string] | [string, string])[]; className?: string; placeholder?: string; disabled?: boolean; size?: 'sm' | 'md' }) {
  // the placeholder is a prompt shown on the trigger, never a choice in the list
  const opts = options
  const current = opts.find((o) => o[0] === (value ?? ''))
  const rootValue = current ? encOpt(current[0]) : '' // '' makes Radix show nothing as selected
  return (
    <div className={cn('relative', className)}>
      <RSelect.Root value={rootValue} onValueChange={(v) => onChange(v === EMPTY_OPTION ? '' : v)} disabled={disabled}>
        <RSelect.Trigger
          className={cn(fieldBase, 'group flex cursor-pointer items-center justify-between gap-2 pl-3 pr-2.5 text-left data-[state=open]:border-accent data-[state=open]:ring-3 data-[state=open]:ring-accent/15 disabled:cursor-not-allowed', size === 'sm' ? 'h-8 text-[13px]' : 'h-9 text-sm')}
        >
          <span className={cn('min-w-0 flex-1 truncate', !current && 'text-faint')}>{current ? current[1] : placeholder ?? '请选择'}</span>
          <ChevronDown className="size-3.5 shrink-0 text-faint transition-transform duration-200 ease-[var(--ease-out)] group-data-[state=open]:rotate-180" weight="bold" />
        </RSelect.Trigger>
        <RSelect.Portal>
          <RSelect.Content
            position="popper"
            sideOffset={6}
            collisionPadding={12}
            className="z-[70] max-h-[min(340px,var(--radix-select-content-available-height))] min-w-[var(--radix-select-trigger-width)] max-w-[min(420px,calc(100vw-24px))] overflow-hidden rounded-[12px] border border-line bg-surface shadow-[var(--shadow-pop)] origin-[var(--radix-select-content-transform-origin)] data-[state=open]:animate-[pop_150ms_var(--ease-out)]"
          >
            <RSelect.ScrollUpButton className="flex h-6 items-center justify-center text-faint"><ChevronUp className="size-3.5" /></RSelect.ScrollUpButton>
            <RSelect.Viewport className="p-1">
              {opts.map(([v, l]) => (
                <RSelect.Item
                  key={encOpt(v)}
                  value={encOpt(v)}
                  className="relative flex cursor-pointer select-none items-center rounded-[8px] py-[7px] pl-3 pr-9 text-[13px] text-ink outline-none transition-colors duration-100 data-[highlighted]:bg-sunken data-[state=checked]:font-medium data-[state=checked]:text-accent data-[disabled]:opacity-40"
                >
                  <RSelect.ItemText>{l}</RSelect.ItemText>
                  <RSelect.ItemIndicator className="absolute right-3 grid place-items-center">
                    <Check className="size-3.5" weight="bold" />
                  </RSelect.ItemIndicator>
                </RSelect.Item>
              ))}
            </RSelect.Viewport>
            <RSelect.ScrollDownButton className="flex h-6 items-center justify-center text-faint"><ChevronDown className="size-3.5" /></RSelect.ScrollDownButton>
          </RSelect.Content>
        </RSelect.Portal>
      </RSelect.Root>
    </div>
  )
}

/** Free-text input with a custom suggestion list (replaces the browser's <datalist>). */
export function SuggestInput({ value, onChange, suggestions, placeholder, className, disabled }: { value: string; onChange: (v: string) => void; suggestions: string[]; placeholder?: string; className?: string; disabled?: boolean }) {
  const [open, setOpen] = useState(false)
  const [hi, setHi] = useState(0)
  const q = value.trim().toLowerCase()
  const list = suggestions.filter((x) => !q || x.toLowerCase().includes(q) || suggestions.includes(value))
  const pick = (v: string) => {
    onChange(v)
    setOpen(false)
  }
  return (
    <RPopover.Root open={open && list.length > 0} onOpenChange={setOpen}>
      <RPopover.Anchor asChild>
        <div className={cn('relative', className)}>
          <input
            value={value}
            disabled={disabled}
            placeholder={placeholder}
            autoComplete="off"
            onChange={(e) => { onChange(e.target.value); setOpen(true); setHi(0) }}
            onFocus={() => setOpen(true)}
            onBlur={() => setTimeout(() => setOpen(false), 120)}
            onKeyDown={(e) => {
              if (!list.length) return
              if (e.key === 'ArrowDown') { e.preventDefault(); setOpen(true); setHi((h) => (h + 1) % list.length) }
              else if (e.key === 'ArrowUp') { e.preventDefault(); setHi((h) => (h - 1 + list.length) % list.length) }
              else if (e.key === 'Enter' && open) { e.preventDefault(); pick(list[hi] ?? value) }
              else if (e.key === 'Escape') setOpen(false)
            }}
            className={cn(fieldBase, 'h-9 pl-3 pr-8')}
          />
          {suggestions.length > 0 && <ChevronDown className="pointer-events-none absolute right-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" weight="bold" />}
        </div>
      </RPopover.Anchor>
      <RPopover.Portal>
        <RPopover.Content align="start" sideOffset={6} collisionPadding={12} onOpenAutoFocus={(e) => e.preventDefault()} onCloseAutoFocus={(e) => e.preventDefault()}
          className="z-[70] max-h-[280px] min-w-[var(--radix-popover-trigger-width)] overflow-y-auto rounded-[12px] border border-line bg-surface p-1 shadow-[var(--shadow-pop)] data-[state=open]:animate-[pop_150ms_var(--ease-out)]">
          {list.map((x, i) => (
            <button key={x} type="button" onMouseDown={(e) => e.preventDefault()} onMouseEnter={() => setHi(i)} onClick={() => pick(x)}
              className={cn('relative flex w-full items-center rounded-[8px] py-[7px] pl-3 pr-9 text-left text-[13px] transition-colors duration-100', i === hi ? 'bg-sunken' : '', x === value ? 'font-medium text-accent' : 'text-ink')}>
              <span className="truncate">{x}</span>
              {x === value && <Check className="absolute right-3 size-3.5" weight="bold" />}
            </button>
          ))}
        </RPopover.Content>
      </RPopover.Portal>
    </RPopover.Root>
  )
}

// ---------------- Date / date-time picker ----------------
const WEEKDAYS = ['一', '二', '三', '四', '五', '六', '日']
const pad2 = (n: number) => String(n).padStart(2, '0')
function parseDateValue(v: string | undefined | null) {
  const m = /^(\d{4})-(\d{2})-(\d{2})(?:[T ](\d{2}):(\d{2}))?/.exec(v ?? '')
  return m ? { y: +m[1], m: +m[2] - 1, d: +m[3], hh: m[4] ? +m[4] : 0, mm: m[5] ? +m[5] : 0 } : null
}

/** Calendar picker. value/onChange use the native formats: "YYYY-MM-DD", or "YYYY-MM-DDTHH:mm" with `time`. */
export function DateInput({ value, onChange, time = false, placeholder, className, disabled, size = 'md', clearable = true }: { value: string; onChange: (v: string) => void; time?: boolean; placeholder?: string; className?: string; disabled?: boolean; size?: 'sm' | 'md'; clearable?: boolean }) {
  const cur = parseDateValue(value)
  const today = new Date()
  const [open, setOpen] = useState(false)
  const [view, setView] = useState<{ y: number; m: number; mode: 'days' | 'months' }>({ y: cur?.y ?? today.getFullYear(), m: cur?.m ?? today.getMonth(), mode: 'days' })
  const [hh, setHh] = useState(cur?.hh ?? 9)
  const [mm, setMm] = useState(cur?.mm ?? 0)
  const openChange = (o: boolean) => {
    if (o) {
      const c = parseDateValue(value)
      setView({ y: c?.y ?? today.getFullYear(), m: c?.m ?? today.getMonth(), mode: 'days' })
      setHh(c?.hh ?? 9)
      setMm(c?.mm ?? 0)
    }
    setOpen(o)
  }
  const emit = (y: number, m: number, d: number, h = hh, mi = mm) => onChange(`${y}-${pad2(m + 1)}-${pad2(d)}` + (time ? `T${pad2(h)}:${pad2(mi)}` : ''))
  const pick = (y: number, m: number, d: number) => {
    emit(y, m, d)
    if (!time) setOpen(false)
  }
  const shift = (n: number) => setView((v) => {
    const t = new Date(v.y, v.m + n, 1)
    return { ...v, y: t.getFullYear(), m: t.getMonth() }
  })
  // 6 weeks starting on Monday
  const first = new Date(view.y, view.m, 1)
  const start = new Date(view.y, view.m, 1 - ((first.getDay() + 6) % 7))
  const days = Array.from({ length: 42 }, (_, i) => new Date(start.getFullYear(), start.getMonth(), start.getDate() + i))
  const same = (a: Date, y: number, m: number, d: number) => a.getFullYear() === y && a.getMonth() === m && a.getDate() === d
  const label = cur ? `${cur.y}年${cur.m + 1}月${cur.d}日` + (time ? ` ${pad2(cur.hh)}:${pad2(cur.mm)}` : '') : ''
  const hours = Array.from({ length: 24 }, (_, i) => [String(i), pad2(i)] as [string, string])
  const minutes = Array.from(new Set([...Array.from({ length: 12 }, (_, i) => i * 5), mm])).sort((a, b) => a - b).map((i) => [String(i), pad2(i)] as [string, string])
  return (
    <RPopover.Root open={open} onOpenChange={openChange}>
      <RPopover.Trigger asChild disabled={disabled}>
        <button type="button" className={cn(fieldBase, 'group flex cursor-pointer items-center gap-2 pl-3 pr-2 text-left data-[state=open]:border-accent data-[state=open]:ring-3 data-[state=open]:ring-accent/15', size === 'sm' ? 'h-8 text-[13px]' : 'h-9 text-sm', className)}>
          <Calendar className="size-4 shrink-0 text-faint" />
          <span className={cn('min-w-0 flex-1 truncate tnum', !label && 'text-faint')}>{label || placeholder || (time ? '选择日期和时间' : '选择日期')}</span>
          {clearable && label && !disabled && (
            <span role="button" aria-label="清除" onPointerDown={(e) => e.stopPropagation()} onClick={(e) => { e.stopPropagation(); e.preventDefault(); onChange('') }} className="grid size-5 place-items-center rounded-full text-faint opacity-0 transition-opacity hover:bg-sunken hover:text-ink group-hover:opacity-100">
              <X className="size-3" weight="bold" />
            </span>
          )}
        </button>
      </RPopover.Trigger>
      <RPopover.Portal>
        <RPopover.Content align="start" sideOffset={6} collisionPadding={12} className="z-[70] w-[284px] rounded-[14px] border border-line bg-surface p-3 shadow-[var(--shadow-pop)] origin-[var(--radix-popover-content-transform-origin)] focus:outline-none data-[state=open]:animate-[pop_150ms_var(--ease-out)]">
          <div className="mb-2 flex items-center justify-between">
            <button type="button" onClick={() => (view.mode === 'days' ? shift(-1) : setView({ ...view, y: view.y - 1 }))} className="grid size-7 place-items-center rounded-full text-muted transition-colors hover:bg-sunken hover:text-ink" aria-label="上一页"><ChevronLeft className="size-4" /></button>
            <button type="button" onClick={() => setView({ ...view, mode: view.mode === 'days' ? 'months' : 'days' })} className="rounded-full px-3 py-1 text-[13.5px] font-semibold tnum transition-colors hover:bg-sunken">
              {view.mode === 'days' ? `${view.y}年 ${view.m + 1}月` : `${view.y}年`}
            </button>
            <button type="button" onClick={() => (view.mode === 'days' ? shift(1) : setView({ ...view, y: view.y + 1 }))} className="grid size-7 place-items-center rounded-full text-muted transition-colors hover:bg-sunken hover:text-ink" aria-label="下一页"><ChevronRight className="size-4" /></button>
          </div>
          <Fade k={view.mode + (view.mode === 'days' ? `${view.y}-${view.m}` : view.y)}>
            {view.mode === 'days' ? (
              <div className="grid grid-cols-7 gap-y-0.5 text-center">
                {WEEKDAYS.map((w) => <div key={w} className="pb-1 text-[11.5px] text-faint">{w}</div>)}
                {days.map((d) => {
                  const inMonth = d.getMonth() === view.m
                  const sel = cur && same(d, cur.y, cur.m, cur.d)
                  const isToday = same(d, today.getFullYear(), today.getMonth(), today.getDate())
                  return (
                    <button key={d.toISOString()} type="button" onClick={() => pick(d.getFullYear(), d.getMonth(), d.getDate())}
                      className={cn('mx-auto grid size-8 place-items-center rounded-full text-[12.5px] tnum transition-colors duration-100', sel ? 'bg-accent font-semibold text-accent-ink' : isToday ? 'font-semibold text-accent ring-1 ring-inset ring-accent/40 hover:bg-accent-soft' : inMonth ? 'text-ink hover:bg-sunken' : 'text-faint/70 hover:bg-sunken')}>
                      {d.getDate()}
                    </button>
                  )
                })}
              </div>
            ) : (
              <div className="grid grid-cols-3 gap-1.5 py-1">
                {Array.from({ length: 12 }, (_, i) => (
                  <button key={i} type="button" onClick={() => setView({ ...view, m: i, mode: 'days' })}
                    className={cn('h-10 rounded-[10px] text-[13px] transition-colors', view.m === i ? 'bg-accent-soft font-semibold text-accent' : 'hover:bg-sunken')}>{i + 1}月</button>
                ))}
              </div>
            )}
          </Fade>
          {time && (
            <div className="mt-3 flex items-center gap-2 rounded-[10px] bg-sunken/60 px-2.5 py-2">
              <span className="mr-auto text-[12.5px] text-muted">时间</span>
              <Select size="sm" className="w-[68px]" value={String(hh)} options={hours} onChange={(v) => { setHh(+v); if (cur) emit(cur.y, cur.m, cur.d, +v, mm) }} />
              <span className="text-faint">:</span>
              <Select size="sm" className="w-[68px]" value={String(mm)} options={minutes} onChange={(v) => { setMm(+v); if (cur) emit(cur.y, cur.m, cur.d, hh, +v) }} />
            </div>
          )}
          <div className="mt-2.5 flex items-center justify-between">
            <button type="button" onClick={() => { const t = new Date(); if (time) { setHh(t.getHours()); setMm(t.getMinutes()); emit(t.getFullYear(), t.getMonth(), t.getDate(), t.getHours(), t.getMinutes()) } else pick(t.getFullYear(), t.getMonth(), t.getDate()) }} className="rounded-full px-2.5 py-1 text-[12.5px] font-medium text-accent transition-colors hover:bg-accent-soft">{time ? '现在' : '今天'}</button>
            <div className="flex gap-1">
              {clearable && cur && <button type="button" onClick={() => { onChange(''); setOpen(false) }} className="rounded-full px-2.5 py-1 text-[12.5px] text-muted transition-colors hover:bg-sunken hover:text-ink">清除</button>}
              {time && <button type="button" onClick={() => setOpen(false)} className="rounded-full bg-accent px-3 py-1 text-[12.5px] font-medium text-accent-ink transition-[filter] hover:brightness-110">完成</button>}
            </div>
          </div>
        </RPopover.Content>
      </RPopover.Portal>
    </RPopover.Root>
  )
}

export function Field({ label, hint, error, children, className, term }: { label?: React.ReactNode; hint?: React.ReactNode; error?: string; children: React.ReactNode; className?: string; term?: string }) {
  return (
    <label className={cn('block space-y-1.5', className)}>
      {label && (
        <span className="flex items-center gap-1 text-[13px] font-medium text-ink">
          {label}
          {term && <Term id={term} />}
        </span>
      )}
      {children}
      {error ? <span className="block text-xs text-danger">{error}</span> : hint ? <span className="block text-xs text-faint">{hint}</span> : null}
    </label>
  )
}

export function Switch({ checked, onChange, disabled, label }: { checked: boolean; onChange: (v: boolean) => any; disabled?: boolean; label?: React.ReactNode }) {
  const id = useId()
  // async handlers: show the new position at once and hold it until the server answers
  const [pending, setPending] = useState<boolean | null>(null)
  const { busy, run } = useBusyHandler((v: boolean) => {
    const r = onChange(v)
    if (r && typeof r.then === 'function') {
      setPending(v)
      return r.finally(() => setPending(null))
    }
    return r
  })
  const shown = pending ?? checked
  return (
    <span className={cn('items-center gap-2', label ? 'flex min-w-0' : 'inline-flex')}>
      <RSwitch.Root id={id} checked={shown} onCheckedChange={(v) => !busy && run(v)} disabled={disabled} aria-busy={busy || undefined} className={cn('relative h-5 w-9 shrink-0 rounded-full bg-line-strong transition-colors data-[state=checked]:bg-accent disabled:opacity-50', busy && 'cursor-progress opacity-80')}>
        <RSwitch.Thumb className="block size-4 translate-x-0.5 rounded-full bg-white shadow transition-transform duration-200 data-[state=checked]:translate-x-[18px]" />
      </RSwitch.Root>
      {label && (
        <label htmlFor={id} className="cursor-pointer text-[13px] text-ink">
          {label}
        </label>
      )}
    </span>
  )
}

export function Checkbox({ checked, onChange, label, className }: { checked: boolean | 'indeterminate'; onChange: (v: boolean) => void; label?: React.ReactNode; className?: string }) {
  return (
    <span className={cn('inline-flex items-center gap-2', className)} onClick={(e) => e.stopPropagation()}>
      <RCheckbox.Root checked={checked} onCheckedChange={(v) => onChange(v === true)} className="grid size-4 shrink-0 place-items-center rounded-[4px] border border-line-strong bg-surface transition-colors data-[state=checked]:border-accent data-[state=checked]:bg-accent data-[state=indeterminate]:bg-accent data-[state=indeterminate]:border-accent">
        <RCheckbox.Indicator className="text-accent-ink">{checked === 'indeterminate' ? <span className="block h-0.5 w-2 bg-current" /> : <Check className="size-3" strokeWidth={3} />}</RCheckbox.Indicator>
      </RCheckbox.Root>
      {label && <span className="text-[13px]">{label}</span>}
    </span>
  )
}

export function Slider({ value, onChange, min = 0, max = 100, step = 1, className, onCommit }: { value: number; onChange: (v: number) => void; min?: number; max?: number; step?: number; className?: string; onCommit?: (v: number) => void }) {
  return (
    <RSlider.Root className={cn('relative flex h-5 w-full touch-none select-none items-center', className)} value={[value]} min={min} max={max} step={step} onValueChange={(v) => onChange(v[0])} onValueCommit={(v) => onCommit?.(v[0])}>
      <RSlider.Track className="relative h-1 grow rounded-full bg-line">
        <RSlider.Range className="absolute h-full rounded-full bg-accent" />
      </RSlider.Track>
      <RSlider.Thumb className="block size-4 rounded-full border-2 border-accent bg-surface shadow transition-transform hover:scale-110 focus:outline-none focus-visible:ring-3 focus-visible:ring-accent/25" />
    </RSlider.Root>
  )
}

// ---------------- Display ----------------
type Tone = 'neutral' | 'accent' | 'signal' | 'danger' | 'ok' | 'warn'
const tones: Record<Tone, string> = {
  neutral: 'bg-sunken text-muted',
  accent: 'bg-accent-soft text-accent',
  signal: 'bg-signal-soft text-warn',
  danger: 'bg-danger-soft text-danger',
  ok: 'bg-ok-soft text-ok',
  warn: 'bg-warn-soft text-warn',
}
export function Badge({ tone = 'neutral', children, className, dot }: { tone?: Tone; children: React.ReactNode; className?: string; dot?: boolean }) {
  return (
    <span className={cn('inline-flex h-5 items-center gap-1 rounded-full px-2 text-[11.5px] font-medium whitespace-nowrap', tones[tone], className)}>
      {dot && <span className="size-1.5 rounded-full bg-current" />}
      {children}
    </span>
  )
}

export function Panel({ children, className, title, actions, padded = true, description, id }: { children: React.ReactNode; className?: string; title?: React.ReactNode; actions?: React.ReactNode; padded?: boolean; description?: React.ReactNode; id?: string }) {
  return (
    <section id={id} className={cn('scroll-mt-4 rounded-panel border border-line bg-surface shadow-[var(--shadow-card)]', className)}>
      {(title || actions) && (
        <header className="flex items-center justify-between gap-3 border-b border-line px-5 py-3.5">
          <div className="min-w-0">
            <h3 className="font-serif text-[15.5px] font-semibold">{title}</h3>
            {description && <p className="mt-0.5 text-xs text-muted">{description}</p>}
          </div>
          {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
        </header>
      )}
      <div className={cn(padded && 'p-5')}>{children}</div>
    </section>
  )
}

export function PageHeader({ title, description, actions, back, children }: { title: React.ReactNode; description?: React.ReactNode; actions?: React.ReactNode; back?: React.ReactNode; children?: React.ReactNode }) {
  return (
    <div className="mb-6">
      {back}
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <h1 className="font-serif text-[26px] font-semibold leading-tight tracking-[0.01em]">{title}</h1>
          {description && <p className="mt-2 max-w-2xl text-[13.5px] leading-relaxed text-muted">{description}</p>}
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2 sm:pt-0.5">{actions}</div>}
      </div>
      {children}
    </div>
  )
}

export function Empty({ icon, title, description, action, className }: { icon?: React.ReactNode; title: string; description?: React.ReactNode; action?: React.ReactNode; className?: string }) {
  return (
    <div className={cn('flex flex-col items-center justify-center px-6 py-14 text-center', className)}>
      {icon && (
        <div className="mb-4 grid size-14 place-items-center rounded-full border border-dashed border-line-strong text-faint [&>svg]:size-6">
          <IconContext.Provider value={{ weight: 'light' }}>{icon}</IconContext.Provider>
        </div>
      )}
      <p className="font-serif text-[16px] font-semibold">{title}</p>
      {description && <p className="mt-1.5 max-w-sm text-[13px] text-muted">{description}</p>}
      {action && <div className="mt-5">{action}</div>}
    </div>
  )
}

export function Skeleton({ className }: { className?: string }) {
  return <div className={cn('skeleton h-4', className)} />
}

export function Progress({ value, tone = 'accent', className, height = 6 }: { value: number; tone?: 'accent' | 'signal' | 'ok' | 'danger'; className?: string; height?: number }) {
  const color = { accent: 'bg-accent', signal: 'bg-signal', ok: 'bg-ok', danger: 'bg-danger' }[tone]
  return (
    <div className={cn('w-full overflow-hidden rounded-full bg-sunken', className)} style={{ height }}>
      <motion.div className={cn('h-full rounded-full', color)} initial={false} animate={{ width: `${Math.max(0, Math.min(1, value)) * 100}%` }} transition={{ type: 'spring', stiffness: 120, damping: 22 }} />
    </div>
  )
}

export function Avatar({ name, src, size = 28, color }: { name?: string; src?: string; size?: number; color?: string }) {
  const ch = (name || '?').trim().slice(0, 1)
  return src ? (
    <img src={src} alt={name} className="shrink-0 rounded-full object-cover" style={{ width: size, height: size }} />
  ) : (
    <span className="inline-grid shrink-0 place-items-center rounded-full font-semibold text-white" style={{ width: size, height: size, fontSize: size * 0.42, background: color || stringColor(name || '') }}>
      {ch}
    </span>
  )
}

export function stringColor(s: string) {
  const palette = ['#2e4c80', '#5b5bd6', '#c2417a', '#b7791f', '#2f7fb8', '#7a52b3', '#8a5a44', '#c25a2e']
  let h = 0
  for (const c of s) h = (h * 31 + c.charCodeAt(0)) >>> 0
  return palette[h % palette.length]
}

export function Kbd({ children }: { children: React.ReactNode }) {
  return <kbd className="inline-flex h-5 min-w-5 items-center justify-center rounded-[4px] border border-line bg-sunken px-1 font-sans text-[11px] text-muted">{children}</kbd>
}

export function Stat({ label, value, hint, className }: { label: string; value: React.ReactNode; hint?: React.ReactNode; className?: string }) {
  return (
    <div className={cn('min-w-0', className)}>
      <div className="text-xs text-muted">{label}</div>
      <div className="mt-1 text-[22px] font-semibold tracking-[-0.01em] tnum">{value}</div>
      {hint && <div className="mt-0.5 text-xs text-faint">{hint}</div>}
    </div>
  )
}

// ---------------- Tooltip / Term ----------------
export function Tip({ label, children, side = 'top' }: { label: React.ReactNode; children: React.ReactElement; side?: 'top' | 'bottom' | 'left' | 'right' }) {
  return (
    <RTooltip.Root delayDuration={350}>
      <RTooltip.Trigger asChild>{children}</RTooltip.Trigger>
      <RTooltip.Portal>
        <RTooltip.Content side={side} sideOffset={6} className="z-[80] max-w-72 rounded-md bg-ink px-2.5 py-1.5 text-xs leading-relaxed text-bg shadow-lg data-[state=delayed-open]:animate-in">
          {label}
        </RTooltip.Content>
      </RTooltip.Portal>
    </RTooltip.Root>
  )
}

export const TERMS: Record<string, string> = {
  画本: '画本就是有声书的演播脚本：把章节拆成一句句片段，标好说话人、情绪和读音。AI 只做标注，不改原文。',
  片段: '画本里的最小单位，通常是一句台词或一段旁白，每个片段对应一段音频。',
  选角: '为书中每个角色挑选声音：AI 音色，或者指派真人配音演员。',
  对轨: '把真人整段录下的音频自动切开，对应到画本里的每一句。',
  审听: '检查成品：智能审听会找出漏读、错读和音质问题，人工审听可以随时打点提工单。',
  工单: '需要返修的问题，定位到具体句子，处理后由审听人复核关闭。',
  积分: 'AI 功能按积分计量：画本和配音按字数，设计音色、音效按次数，对轨和审听按时长。',
  合成: '把所有句子的音频按停顿规则拼接，叠加背景音乐和音效，做响度标准化后输出成品。',
  响度: '平台对音频整体音量有要求（单位 LUFS），合成时会自动标准化到所选平台的标准。',
  一键成书: '上传书稿后无人值守地完成拆章、角色分析、画本、选角、配音和合成，之后可以再进制作台精修。',
  BYOK: '使用工作室自己的平台密钥调用 AI 服务，费用由你和平台直接结算，不再扣 AI 积分。',
  旁白: '负责朗读叙述内容的声音，每本书默认有一个旁白角色。',
  表演标签: '标在句子里的语气和声音提示，例如“轻笑”“叹气”“讽刺”。AI 生成画本时会根据原文自动标注，原文本身不会被改动；Qwen-Audio 音色会按位置演出来，MiniMax 2.8 音色会演出笑声、叹气等声音类标签。',
}

export function Term({ id }: { id: string }) {
  const text = TERMS[id]
  if (!text) return null
  return (
    <Tip label={text}>
      <HelpCircle className="size-3.5 cursor-help text-faint hover:text-muted" />
    </Tip>
  )
}

// ---------------- Dialog ----------------
/** Open dialogs on their first field rather than the close button; fall back to the dialog itself. */
function focusFirstField(e: Event) {
  e.preventDefault()
  const root = e.currentTarget as HTMLElement
  const field = root.querySelector<HTMLElement>('[data-autofocus], input:not([type=hidden]):not([type=file]):not([disabled]), textarea:not([disabled])')
  ;(field ?? root).focus({ preventScroll: true })
}

/** Scrollable modal body; soft shades fade in at an edge only while content is hidden behind it. */
function ModalBody({ children, className }: { children?: React.ReactNode; className?: string }) {
  const ref = useRef<HTMLDivElement>(null)
  const inner = useRef<HTMLDivElement>(null)
  const [edge, setEdge] = useState({ top: false, bottom: false })
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const upd = () => {
      const top = el.scrollTop > 1
      const bottom = el.scrollTop + el.clientHeight < el.scrollHeight - 1
      setEdge((e) => (e.top === top && e.bottom === bottom ? e : { top, bottom }))
    }
    upd()
    el.addEventListener('scroll', upd, { passive: true })
    const ro = new ResizeObserver(upd)
    ro.observe(el)
    if (inner.current) ro.observe(inner.current)
    return () => {
      el.removeEventListener('scroll', upd)
      ro.disconnect()
    }
  }, [])
  return (
    <div className="relative flex min-h-0 flex-1 flex-col">
      <div aria-hidden className={cn('pointer-events-none absolute inset-x-0 top-0 z-10 h-5 bg-gradient-to-b from-[var(--edge-shade)] to-transparent transition-opacity duration-200', edge.top ? 'opacity-100' : 'opacity-0')} />
      <div ref={ref} className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
        <div ref={inner} className={className}>{children}</div>
      </div>
      <div aria-hidden className={cn('pointer-events-none absolute inset-x-0 bottom-0 z-10 h-5 bg-gradient-to-t from-[var(--edge-shade)] to-transparent transition-opacity duration-200', edge.bottom ? 'opacity-100' : 'opacity-0')} />
    </div>
  )
}

function ModalClose({ className }: { className?: string }) {
  return (
    <RDialog.Close aria-label="关闭" className={cn('grid size-8 shrink-0 place-items-center rounded-full text-faint transition-[background-color,color,transform] duration-150 hover:bg-sunken hover:text-ink active:scale-95', className)}>
      <X className="size-[15px]" weight="bold" />
    </RDialog.Close>
  )
}

/** Enter in a single-line field confirms the dialog, like a form: it presses the footer's main button. */
function submitOnEnter(e: React.KeyboardEvent<HTMLElement>) {
  const t = e.target as HTMLElement
  if (e.key !== 'Enter' || e.shiftKey || e.altKey || e.nativeEvent.isComposing || e.defaultPrevented) return
  if (t.tagName !== 'INPUT' || ['checkbox', 'radio', 'file', 'button', 'submit'].includes((t as HTMLInputElement).type)) return
  const footer = e.currentTarget.querySelector('[data-dialog-footer]')
  const main = [...(footer?.querySelectorAll<HTMLButtonElement>('button') ?? [])].filter((b) => /bg-accent|bg-danger/.test(b.className)).pop()
  if (!main || main.disabled) return
  e.preventDefault()
  main.click()
}

export function Dialog({ open, onOpenChange, title, description, children, footer, width = 520, className, dismissible = true }: { open: boolean; onOpenChange: (v: boolean) => void; title?: React.ReactNode; description?: React.ReactNode; children?: React.ReactNode; footer?: React.ReactNode; width?: number; className?: string; dismissible?: boolean }) {
  return (
    <RDialog.Root open={open} onOpenChange={onOpenChange}>
      <AnimatePresence>
        {open && (
          <RDialog.Portal forceMount>
            <RDialog.Overlay asChild forceMount>
              <motion.div className="fixed inset-0 z-50 bg-[var(--scrim)] backdrop-blur-[2px]" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} transition={{ duration: 0.22 }} />
            </RDialog.Overlay>
            <RDialog.Content asChild forceMount aria-describedby={undefined} onOpenAutoFocus={focusFirstField} onKeyDown={submitOnEnter} onEscapeKeyDown={(e) => !dismissible && e.preventDefault()} onPointerDownOutside={(e) => !dismissible && e.preventDefault()}>
              <motion.div
                className={cn('fixed left-1/2 top-1/2 z-50 flex max-h-[min(86dvh,calc(100dvh-48px))] w-[calc(100vw-32px)] -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-[20px] bg-surface shadow-[var(--shadow-modal)] focus:outline-none', className)}
                style={{ maxWidth: width }}
                initial={{ opacity: 0, y: 16, scale: 0.96 }}
                animate={{ opacity: 1, y: 0, scale: 1 }}
                exit={{ opacity: 0, y: 10, scale: 0.98, transition: { duration: 0.16 } }}
                transition={{ type: 'spring', stiffness: 380, damping: 30, mass: 0.9 }}
              >
                {title ? (
                  <div className="flex items-start justify-between gap-4 px-6 pb-1 pt-6">
                    <div className="min-w-0">
                      <RDialog.Title className="font-serif text-[19px] font-semibold leading-snug tracking-[0.01em]">{title}</RDialog.Title>
                      {description && <RDialog.Description className="mt-1.5 text-[13px] leading-relaxed text-muted">{description}</RDialog.Description>}
                    </div>
                    {dismissible && <ModalClose className="-mr-2 -mt-1" />}
                  </div>
                ) : (
                  dismissible && <ModalClose className="absolute right-3 top-3 z-20" />
                )}
                <ModalBody className={cn('px-6', title ? 'pb-5 pt-4' : 'py-6')}>{children}</ModalBody>
                {footer && <div data-dialog-footer className="flex items-center justify-end gap-2 px-6 pb-5 pt-2">{footer}</div>}
              </motion.div>
            </RDialog.Content>
          </RDialog.Portal>
        )}
      </AnimatePresence>
    </RDialog.Root>
  )
}

export function Sheet({ open, onOpenChange, title, description, children, footer, width = 480 }: { open: boolean; onOpenChange: (v: boolean) => void; title?: React.ReactNode; description?: React.ReactNode; children?: React.ReactNode; footer?: React.ReactNode; width?: number }) {
  return (
    <RDialog.Root open={open} onOpenChange={onOpenChange}>
      <AnimatePresence>
        {open && (
          <RDialog.Portal forceMount>
            <RDialog.Overlay asChild forceMount>
              <motion.div className="fixed inset-0 z-50 bg-[var(--scrim)] backdrop-blur-[1.5px]" initial={{ opacity: 0 }} animate={{ opacity: 0.8 }} exit={{ opacity: 0 }} transition={{ duration: 0.22 }} />
            </RDialog.Overlay>
            <RDialog.Content asChild forceMount aria-describedby={undefined} onOpenAutoFocus={focusFirstField}>
              <motion.div
                className="fixed inset-y-2 right-2 z-50 flex w-[calc(100vw-16px)] flex-col overflow-hidden rounded-[20px] bg-surface shadow-[var(--shadow-modal)] focus:outline-none"
                style={{ maxWidth: width }}
                initial={{ x: 'calc(100% + 16px)', opacity: 0.6 }}
                animate={{ x: 0, opacity: 1 }}
                exit={{ x: 'calc(100% + 16px)', opacity: 0.6 }}
                transition={{ type: 'spring', stiffness: 380, damping: 38 }}
              >
                <div className="flex items-start justify-between gap-4 px-6 pb-1 pt-5">
                  <div className="min-w-0">
                    <RDialog.Title className="truncate font-serif text-[19px] font-semibold leading-snug tracking-[0.01em]">{title}</RDialog.Title>
                    {description && <RDialog.Description className="mt-1 text-[13px] leading-relaxed text-muted">{description}</RDialog.Description>}
                  </div>
                  <ModalClose className="-mr-2" />
                </div>
                <ModalBody className="px-6 pb-6 pt-4">{children}</ModalBody>
                {footer && <div data-dialog-footer className="flex items-center justify-end gap-2 px-6 pb-5 pt-2">{footer}</div>}
              </motion.div>
            </RDialog.Content>
          </RDialog.Portal>
        )}
      </AnimatePresence>
    </RDialog.Root>
  )
}

// confirm() — promise-based confirmation dialog
type ConfirmOpts = { title: string; description?: React.ReactNode; confirmText?: string; danger?: boolean }
const useConfirmStore = create<{ opts: ConfirmOpts | null; resolve?: (v: boolean) => void }>(() => ({ opts: null }))
export function confirm(opts: ConfirmOpts) {
  return new Promise<boolean>((resolve) => useConfirmStore.setState({ opts, resolve }))
}
export function ConfirmHost() {
  const { opts, resolve } = useConfirmStore()
  const close = (v: boolean) => {
    resolve?.(v)
    useConfirmStore.setState({ opts: null, resolve: undefined })
  }
  return (
    <Dialog
      open={!!opts}
      onOpenChange={(v) => !v && close(false)}
      title={opts?.title}
      width={440}
      footer={
        <>
          <Button onClick={() => close(false)}>取消</Button>
          <Button variant={opts?.danger ? 'danger' : 'primary'} onClick={() => close(true)} autoFocus>
            {opts?.confirmText ?? '确定'}
          </Button>
        </>
      }
    >
      <div className="text-[13.5px] leading-relaxed text-muted">{opts?.description}</div>
    </Dialog>
  )
}

// ---------------- Menu / Popover ----------------
export type MenuItem = { label: React.ReactNode; icon?: React.ReactNode; onSelect: () => void; danger?: boolean; disabled?: boolean; hint?: React.ReactNode }

export function Menu({ trigger, items, align = 'end', open, onOpenChange, side }: { trigger: React.ReactNode; items: (MenuItem | 'sep' | null | false)[]; align?: 'start' | 'end'; open?: boolean; onOpenChange?: (v: boolean) => void; side?: 'top' | 'bottom' | 'left' | 'right' }) {
  return (
    <RDropdown.Root open={open} onOpenChange={onOpenChange} modal={open === undefined}>
      <RDropdown.Trigger asChild>{trigger}</RDropdown.Trigger>
      <RDropdown.Portal>
        <RDropdown.Content align={align} side={side} sideOffset={6} collisionPadding={8} className="z-[60] min-w-44 rounded-[10px] border border-line bg-surface p-1 shadow-[var(--shadow-pop)] origin-[var(--radix-dropdown-menu-content-transform-origin)] data-[state=open]:animate-[pop_140ms_var(--ease-out)]">
          {items.filter(Boolean).map((it, i) =>
            it === 'sep' ? (
              <RDropdown.Separator key={i} className="my-1 h-px bg-line" />
            ) : (
              <RDropdown.Item
                key={i}
                disabled={(it as any).disabled}
                onSelect={(it as any).onSelect}
                className={cn('flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-1.5 text-[13px] outline-none data-[highlighted]:bg-sunken data-[disabled]:opacity-40 [&>svg]:size-4', (it as any).danger ? 'text-danger' : 'text-ink')}
              >
                {(it as any).icon}
                <span className="flex-1">{(it as any).label}</span>
                {(it as any).hint && <span className="pl-4 text-[11px] text-faint">{(it as any).hint}</span>}
              </RDropdown.Item>
            ),
          )}
        </RDropdown.Content>
      </RDropdown.Portal>
    </RDropdown.Root>
  )
}

export function Popover({ trigger, children, align = 'start', width = 300, open, onOpenChange }: { trigger: React.ReactNode; children: React.ReactNode; align?: 'start' | 'end' | 'center'; width?: number; open?: boolean; onOpenChange?: (v: boolean) => void }) {
  return (
    <RPopover.Root open={open} onOpenChange={onOpenChange}>
      <RPopover.Trigger asChild>{trigger}</RPopover.Trigger>
      <RPopover.Portal>
        <RPopover.Content align={align} sideOffset={8} style={{ width }} className="z-[60] rounded-panel border border-line bg-surface p-4 shadow-[var(--shadow-pop)] focus:outline-none data-[state=open]:animate-[pop_160ms_var(--ease-out)]">
          {children}
        </RPopover.Content>
      </RPopover.Portal>
    </RPopover.Root>
  )
}

// ---------------- Tabs & Segmented ----------------
export function Tabs<T extends string>({ value, onChange, items, className }: { value: T; onChange: (v: T) => void; items: { value: T; label: React.ReactNode; count?: number }[]; className?: string }) {
  const id = useId()
  return (
    <div className={cn('no-scrollbar flex gap-1 overflow-x-auto overflow-y-hidden border-b border-line', className)} role="tablist">
      {items.map((it) => (
        <button key={it.value} role="tab" aria-selected={value === it.value} onClick={() => onChange(it.value)} className={cn('relative flex shrink-0 items-center gap-1.5 px-3 pb-2.5 pt-1 text-[13.5px] transition-colors', value === it.value ? 'font-medium text-ink' : 'text-muted hover:text-ink')}>
          {it.label}
          {it.count !== undefined && <span className="rounded-full bg-sunken px-1.5 text-[11px] text-muted tnum">{it.count}</span>}
          {value === it.value && <motion.span layoutId={id} className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-ink" transition={{ type: 'spring', stiffness: 500, damping: 40 }} />}
        </button>
      ))}
    </div>
  )
}

export function Segmented<T extends string>({ value, onChange, items, size = 'md', className }: { value: T; onChange: (v: T) => void; items: { value: T; label: React.ReactNode }[]; size?: 'sm' | 'md'; className?: string }) {
  const id = useId()
  return (
    <div className={cn('inline-flex rounded-[9px] bg-sunken p-0.5', className)}>
      {items.map((it) => (
        <button key={it.value} onClick={() => onChange(it.value)} className={cn('relative rounded-[7px] px-3 transition-colors', size === 'sm' ? 'h-7 text-xs' : 'h-8 text-[13px]', value === it.value ? 'text-ink' : 'text-muted hover:text-ink')}>
          {value === it.value && <motion.span layoutId={id} className="absolute inset-0 rounded-[7px] bg-surface shadow-[0_1px_2px_rgb(0_0_0/0.08)]" transition={{ type: 'spring', stiffness: 500, damping: 40 }} />}
          <span className="relative flex items-center gap-1.5">{it.label}</span>
        </button>
      ))}
    </div>
  )
}

// ---------------- Misc ----------------
export function Pagination({ page, pageSize, total, onChange }: { page: number; pageSize: number; total: number; onChange: (p: number) => void }) {
  const pages = Math.max(1, Math.ceil(total / pageSize))
  if (pages <= 1) return null
  return (
    <div className="flex items-center justify-between gap-3 pt-4 text-[13px] text-muted">
      <span className="tnum">共 {total} 条</span>
      <div className="flex items-center gap-1">
        <Button size="sm" disabled={page <= 1} onClick={() => onChange(page - 1)}>
          上一页
        </Button>
        <span className="px-2 tnum">
          {page} / {pages}
        </span>
        <Button size="sm" disabled={page >= pages} onClick={() => onChange(page + 1)}>
          下一页
        </Button>
      </div>
    </div>
  )
}

export function Fade({ children, k, className }: { children: React.ReactNode; k: React.Key; className?: string }) {
  return (
    <AnimatePresence mode="wait" initial={false}>
      <motion.div key={k} className={className} initial={{ opacity: 0, y: 4 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, y: -4 }} transition={{ duration: 0.16, ease: [0.22, 1, 0.36, 1] }}>
        {children}
      </motion.div>
    </AnimatePresence>
  )
}

export function Collapse({ open, children }: { open: boolean; children: React.ReactNode }) {
  return (
    <AnimatePresence initial={false}>
      {open && (
        <motion.div initial={{ height: 0, opacity: 0 }} animate={{ height: 'auto', opacity: 1 }} exit={{ height: 0, opacity: 0 }} transition={{ duration: 0.22, ease: [0.22, 1, 0.36, 1] }} className="overflow-hidden">
          {children}
        </motion.div>
      )}
    </AnimatePresence>
  )
}

export function Table({ children, className }: { children: React.ReactNode; className?: string }) {
  return (
    <div className={cn('overflow-x-auto', className)}>
      <table className="w-full text-[13px] [&_td]:border-t [&_td]:border-line [&_td]:px-4 [&_td]:py-2.5 [&_th]:whitespace-nowrap [&_th]:px-4 [&_th]:py-2 [&_th]:text-left [&_th]:text-xs [&_th]:font-medium [&_th]:text-muted [&_tbody_tr]:transition-colors [&_tbody_tr:hover]:bg-sunken/50">{children}</table>
    </div>
  )
}

export function Copyable({ text, className }: { text: string; className?: string }) {
  const [done, setDone] = useState(false)
  return (
    <button
      className={cn('inline-flex max-w-full items-center gap-1.5 truncate rounded-md bg-sunken px-2 py-1 text-left text-xs text-muted hover:text-ink', className)}
      onClick={() => {
        navigator.clipboard?.writeText(text)
        setDone(true)
        setTimeout(() => setDone(false), 1500)
      }}
    >
      <span className="truncate">{text}</span>
      <span className="shrink-0 text-accent">{done ? '已复制' : '复制'}</span>
    </button>
  )
}

const ToggleCtx = createContext<any>(null)
export const useToggleCtx = () => useContext(ToggleCtx)

export function Chip({ active, onClick, children, count, className }: { active?: boolean; onClick?: () => void; children: React.ReactNode; count?: number; className?: string }) {
  return (
    <button type="button" aria-pressed={active} onClick={onClick} className={cn('h-7 shrink-0 rounded-full border px-3 text-[12.5px] transition-[background-color,border-color,color,transform] duration-150 active:scale-[0.97]', active ? 'border-accent/60 bg-accent-soft font-medium text-accent' : 'border-line bg-surface text-muted hover:border-line-strong hover:text-ink', className)}>
      {children}
      {count != null && <span className="ml-1 text-[11px] opacity-60 tnum">{count}</span>}
    </button>
  )
}
