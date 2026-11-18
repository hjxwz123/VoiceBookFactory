import { useEffect, useRef, useState } from 'react'
import { ArrowDown, ArrowUp } from '@/components/icons'
import { cn } from '@/components/ui'

// Small, quiet charts for the admin dashboard: 2px lines with a 10% wash, thin bars with rounded ends,
// recessive hairline grids, a crosshair tooltip on hover. Series colours come from --chart-1/--chart-2.

export const compact = (v: number) => {
  const a = Math.abs(v)
  if (a >= 1e8) return `${+(v / 1e8).toFixed(1)}亿`
  if (a >= 1e4) return `${+(v / 1e4).toFixed(1)}万`
  return v.toLocaleString('zh-CN', { maximumFractionDigits: a < 10 && a % 1 ? 2 : 0 })
}

/** Clean axis ticks: 0 and 3 steps of 1, 2 or 5 × 10^n. */
function ticks(max: number, integer: boolean): number[] {
  if (max <= 0) return [0, 1]
  const raw = max / 3
  const pow = 10 ** Math.floor(Math.log10(raw))
  let step = [1, 2, 5, 10].map((m) => m * pow).find((s) => s >= raw) ?? raw
  if (integer) step = Math.max(1, Math.round(step)) // counts of people or things: no half steps
  const out = []
  for (let v = 0; v <= max + step * 0.001; v += step) out.push(v)
  if (out[out.length - 1] < max) out.push(out[out.length - 1] + step)
  return out
}

function useWidth<T extends HTMLElement>() {
  const ref = useRef<T>(null)
  const [w, setW] = useState(0)
  useEffect(() => {
    if (!ref.current) return
    const ro = new ResizeObserver(([e]) => setW(e.contentRect.width))
    ro.observe(ref.current)
    return () => ro.disconnect()
  }, [])
  return [ref, w] as const
}

export type Series = { key: string; label: string; values: number[]; color?: 1 | 2 }

/** Daily trend: one or two series that share a unit (never two scales). */
export function TrendChart({ days, series, format = compact, height = 180 }: { days: string[]; series: Series[]; format?: (v: number) => string; height?: number }) {
  const [ref, width] = useWidth<HTMLDivElement>()
  const [hover, setHover] = useState<number | null>(null)
  const padL = 44, padR = 12, padT = 10, padB = 24
  const w = Math.max(width, 200), h = height
  const max = Math.max(0, ...series.flatMap((s) => s.values))
  const ys = ticks(max, series.every((s) => s.values.every((v) => Number.isInteger(v))))
  const top = ys[ys.length - 1] || 1
  const n = days.length
  const x = (i: number) => padL + (n <= 1 ? 0 : (i / (n - 1)) * (w - padL - padR))
  const y = (v: number) => padT + (1 - v / top) * (h - padT - padB)
  const color = (s: Series, i: number) => `var(--chart-${s.color ?? (i === 0 ? 1 : 2)})`
  const path = (vals: number[]) => vals.map((v, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join('')
  const onMove = (e: React.PointerEvent) => {
    const r = (e.currentTarget as SVGElement).getBoundingClientRect()
    const i = Math.round(((e.clientX - r.left - padL) / (w - padL - padR)) * (n - 1))
    setHover(Math.max(0, Math.min(n - 1, i)))
  }
  const labelIdx = n > 1 ? [0, Math.floor((n - 1) / 2), n - 1] : [0]
  return (
    <div ref={ref} className="relative">
      {series.length > 1 && (
        <div className="mb-2 flex flex-wrap gap-4 text-[12px] text-muted">
          {series.map((s, i) => <span key={s.key} className="inline-flex items-center gap-1.5"><span className="h-[3px] w-3.5 rounded-full" style={{ background: color(s, i) }} />{s.label}</span>)}
        </div>
      )}
      {width > 0 && (
        <svg width={w} height={h} className="block touch-none select-none" onPointerMove={onMove} onPointerLeave={() => setHover(null)} role="img" aria-label={series.map((s) => s.label).join('、')}>
          {ys.map((v) => (
            <g key={v}>
              <line x1={padL} x2={w - padR} y1={y(v)} y2={y(v)} stroke="var(--line)" strokeWidth={1} />
              <text x={padL - 8} y={y(v)} dy="0.32em" textAnchor="end" className="fill-[var(--faint)] text-[10.5px] tnum">{format(v)}</text>
            </g>
          ))}
          {labelIdx.map((i) => <text key={i} x={x(i)} y={h - 6} textAnchor={i === 0 ? 'start' : i === n - 1 ? 'end' : 'middle'} className="fill-[var(--faint)] text-[10.5px] tnum">{days[i]?.slice(5)}</text>)}
          {series.length === 1 && <path d={`${path(series[0].values)}L${x(n - 1)},${y(0)}L${x(0)},${y(0)}Z`} fill={color(series[0], 0)} opacity={0.1} />}
          {series.map((s, i) => <path key={s.key} d={path(s.values)} fill="none" stroke={color(s, i)} strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />)}
          {hover === null && series.map((s, i) => <circle key={s.key} cx={x(n - 1)} cy={y(s.values[n - 1] ?? 0)} r={4} fill={color(s, i)} stroke="var(--surface)" strokeWidth={2} />)}
          {hover !== null && (
            <g>
              <line x1={x(hover)} x2={x(hover)} y1={padT} y2={h - padB} stroke="var(--line-strong)" strokeWidth={1} />
              {series.map((s, i) => <circle key={s.key} cx={x(hover)} cy={y(s.values[hover] ?? 0)} r={4.5} fill={color(s, i)} stroke="var(--surface)" strokeWidth={2} />)}
            </g>
          )}
        </svg>
      )}
      {hover !== null && width > 0 && (
        <div className="pointer-events-none absolute top-1 z-10 min-w-32 rounded-[10px] border border-line bg-surface px-3 py-2 text-[12px] shadow-[var(--shadow-lift)]"
          style={{ left: Math.min(Math.max(x(hover) + 12, 0), w - 150) }}>
          <div className="mb-1 text-faint tnum">{days[hover]}</div>
          {series.map((s, i) => (
            <div key={s.key} className="flex items-center justify-between gap-4">
              <span className="inline-flex items-center gap-1.5 text-muted"><span className="size-2 rounded-full" style={{ background: color(s, i) }} />{s.label}</span>
              <span className="font-medium text-ink tnum">{format(s.values[hover] ?? 0)}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

export type Part = { key: string; label: string; value: number; extra?: number }

/** Parts of a total, largest first, as thin horizontal bars. */
export function BarList({ items, format = compact, limit = 8, empty = '暂无数据', sub }: { items: Part[]; format?: (v: number) => string; limit?: number; empty?: string; sub?: (p: Part) => string | undefined }) {
  const list = items.filter((p) => p.value > 0)
  const shown = list.slice(0, limit)
  const rest = list.slice(limit).reduce((a, p) => a + p.value, 0)
  if (rest > 0) shown.push({ key: '__other', label: `其他 ${list.length - limit} 项`, value: rest })
  const total = list.reduce((a, p) => a + p.value, 0)
  const max = Math.max(1, ...shown.map((p) => p.value))
  if (!shown.length) return <p className="py-6 text-center text-[12.5px] text-faint">{empty}</p>
  return (
    <ul className="space-y-2.5">
      {shown.map((p) => (
        <li key={p.key} className="group" title={`${p.label}：${format(p.value)}（${((p.value / total) * 100).toFixed(1)}%）`}>
          <div className="flex items-baseline justify-between gap-3 text-[12.5px]">
            <span className="min-w-0 truncate text-ink">{p.label}{sub?.(p) && <span className="ml-1.5 text-faint">{sub(p)}</span>}</span>
            <span className="shrink-0 tnum"><span className="font-medium">{format(p.value)}</span><span className="ml-1.5 text-faint">{((p.value / total) * 100).toFixed(0)}%</span></span>
          </div>
          <div className="mt-1 h-2 rounded-r-[4px] bg-transparent">
            <div className="h-full rounded-r-[4px] bg-[var(--chart-1)] transition-[width,opacity] duration-500 group-hover:opacity-80" style={{ width: `${Math.max(1.5, (p.value / max) * 100)}%` }} />
          </div>
        </li>
      ))}
    </ul>
  )
}

/** Steps people pass through, each with its share of the first step and of the step before. */
export function Funnel({ steps }: { steps: Part[] }) {
  const first = steps[0]?.value || 0
  return (
    <ol className="space-y-3">
      {steps.map((s, i) => {
        const ofFirst = first ? s.value / first : 0
        const prev = i ? steps[i - 1].value : 0
        return (
          <li key={s.key} title={`${s.label}：${s.value} 人`}>
            <div className="flex items-baseline justify-between gap-3 text-[12.5px]">
              <span className="text-ink">{s.label}</span>
              <span className="tnum"><span className="font-medium">{compact(s.value)}</span><span className="ml-2 text-faint">{i === 0 ? '100%' : `${(ofFirst * 100).toFixed(1)}%`}{i > 0 && prev > 0 && ` · 上一步 ${((s.value / prev) * 100).toFixed(0)}%`}</span></span>
            </div>
            <div className="mt-1 h-3 rounded-r-[4px]"><div className="h-full rounded-r-[4px] bg-[var(--chart-1)]" style={{ width: `${s.value > 0 ? Math.max(1.5, ofFirst * 100) : 0}%`, opacity: 1 - i * 0.12 }} /></div>
          </li>
        )
      })}
    </ol>
  )
}

/** A headline number with its change against the previous period of the same length. */
export function Kpi({ label, value, cur, prev, goodUp = true, hint, className }: { label: string; value: React.ReactNode; cur?: number; prev?: number; goodUp?: boolean; hint?: React.ReactNode; className?: string }) {
  let delta: React.ReactNode = null
  if (cur !== undefined && prev !== undefined) {
    if (prev === 0) delta = cur > 0 ? <span className="text-faint">上期为 0</span> : <span className="text-faint">与上期持平</span>
    else {
      const d = (cur - prev) / prev
      const up = d > 0
      const good = d === 0 ? null : up === goodUp
      delta = (
        <span className={cn('inline-flex items-center gap-0.5 font-medium', good === null ? 'text-faint' : good ? 'text-ok' : 'text-danger')}>
          {d !== 0 && (up ? <ArrowUp className="size-3" /> : <ArrowDown className="size-3" />)}
          {d === 0 ? '持平' : `${Math.abs(d * 100).toFixed(Math.abs(d) < 0.1 ? 1 : 0)}%`}
          <span className="ml-1 font-normal text-faint">较上期</span>
        </span>
      )
    }
  }
  return (
    <div className={cn('min-w-0', className)}>
      <div className="text-xs text-muted">{label}</div>
      <div className="mt-1 text-[22px] font-semibold tracking-[-0.01em] tnum">{value}</div>
      <div className="mt-0.5 flex flex-wrap gap-x-2 text-[11.5px]">{delta}{hint && <span className="text-faint">{hint}</span>}</div>
    </div>
  )
}
