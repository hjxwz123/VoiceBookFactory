import { useQuery } from '@tanstack/react-query'
import { X } from '@/components/icons'
import React, { useState } from 'react'
import { api } from '@/lib/api'
import { cn, Popover, Tip } from './ui'

export type Cue = { at: number; tag: string }
export type PerfTag = { key: string; label: string; kind: 'style' | 'sound'; hint: string }

// earlier whole-line tags (Chinese) -> cue keys
export const legacyTagMap: Record<string, string> = {
  笑: 'laughing', 冷笑: 'snorts', 叹气: 'sighing', 哭腔: 'crying', 哽咽: 'crying', 喘息: 'gasp', 轻声: 'whispers', 气声: 'asmr', 惊呼: 'gasp', 咳嗽: 'cough', 清嗓: 'clears throat', 咯咯笑: 'giggles',
}

export function usePerfTags() {
  const { data } = useQuery<PerfTag[]>({ queryKey: ['perf-tags'], queryFn: () => api.get('/performance-tags'), staleTime: Infinity })
  const list = data ?? []
  const byKey = new Map(list.map((t) => [t.key, t]))
  return { list, byKey }
}

/** Cues of a segment, folding the earlier whole-line tags in at the start. */
export function segmentCues(s: { cues?: Cue[]; tags?: string[] }): Cue[] {
  const legacy = (s.tags ?? []).map((t) => legacyTagMap[t]).filter(Boolean).map((tag) => ({ at: 0, tag }))
  return [...legacy, ...(s.cues ?? [])].sort((a, b) => a.at - b.at)
}

export function CueChip({ tag, small, onRemove, draggable, onDragStart, className }: { tag?: PerfTag; small?: boolean; onRemove?: () => void; draggable?: boolean; onDragStart?: (e: React.DragEvent) => void; className?: string }) {
  if (!tag) return null
  const sound = tag.kind === 'sound'
  return (
    <Tip label={`${sound ? '声音' : '语气'}：${tag.hint}${draggable ? '（可拖动到其他位置）' : ''}`}>
      <span
        draggable={draggable}
        onDragStart={onDragStart}
        onClick={(e) => e.stopPropagation()}
        className={cn(
          'mx-0.5 inline-flex select-none items-center gap-0.5 rounded-[5px] align-[0.12em] font-medium leading-none not-italic',
          small ? 'px-1 py-[2px] text-[10.5px]' : 'px-1.5 py-[3px] text-[11.5px]',
          sound ? 'bg-sunken text-muted ring-1 ring-line' : 'bg-signal-soft text-warn',
          draggable && 'cursor-grab active:cursor-grabbing',
          className,
        )}
      >
        {sound ? '♪' : '◆'}
        {tag.label}
        {onRemove && (
          <button onClick={(e) => { e.stopPropagation(); onRemove() }} className="-mr-0.5 ml-0.5 rounded hover:text-danger" aria-label="移除标签">
            <X className="size-3" />
          </button>
        )}
      </span>
    </Tip>
  )
}

/** Read-only text with inline cue markers. */
export function CueText({ text, cues, small, renderChar }: { text: string; cues: Cue[]; small?: boolean; renderChar?: (ch: string, i: number) => React.ReactNode }) {
  const { byKey } = usePerfTags()
  const chars = Array.from(text)
  if (!cues.length && !renderChar) return <>{text}</>
  const at = new Map<number, Cue[]>()
  cues.forEach((c) => at.set(Math.min(c.at, chars.length), [...(at.get(Math.min(c.at, chars.length)) ?? []), c]))
  const out: React.ReactNode[] = []
  for (let i = 0; i <= chars.length; i++) {
    for (const c of at.get(i) ?? []) out.push(<CueChip key={`c${i}${c.tag}`} tag={byKey.get(c.tag)} small={small} />)
    if (i < chars.length) out.push(renderChar ? <React.Fragment key={i}>{renderChar(chars[i], i)}</React.Fragment> : chars[i])
  }
  return <>{out}</>
}

/** Editable cues: click a gap between characters to insert, drag a marker to move it, × to remove. */
export function CueEditor({ text, cues, onChange, disabled }: { text: string; cues: Cue[]; onChange: (c: Cue[]) => void; disabled?: boolean }) {
  const { list, byKey } = usePerfTags()
  const chars = Array.from(text)
  const [open, setOpen] = useState<number | null>(null)
  const [dragFrom, setDragFrom] = useState<number | null>(null)
  const [over, setOver] = useState<number | null>(null)
  const add = (pos: number, tag: string) => {
    onChange([...cues, { at: pos, tag }].sort((a, b) => a.at - b.at))
    setOpen(null)
  }
  const move = (idx: number, pos: number) => onChange(cues.map((c, i) => (i === idx ? { ...c, at: pos } : c)).sort((a, b) => a.at - b.at))
  const remove = (idx: number) => onChange(cues.filter((_, i) => i !== idx))

  const gap = (pos: number) => {
    if (disabled) return null
    const active = open === pos || over === pos
    // zero-width slot: the text keeps its normal spacing, the hit area straddles the character boundary
    return (
      <span key={`g${pos}`} className="relative inline-block h-[1.3em] w-0 align-[-0.2em]">
        <Popover
          open={open === pos}
          onOpenChange={(o) => setOpen(o ? pos : null)}
          width={340}
          trigger={
            <button
              onDragOver={(e) => { if (dragFrom !== null) { e.preventDefault(); setOver(pos) } }}
              onDragLeave={() => setOver(null)}
              onDrop={(e) => { e.preventDefault(); if (dragFrom !== null) move(dragFrom, pos); setDragFrom(null); setOver(null) }}
              className="group absolute inset-y-0 -left-[5px] z-[1] w-[10px] cursor-text focus:outline-none"
              aria-label="在这里插入表演标签"
            >
              <span className={cn('absolute inset-y-0 left-1/2 w-[2px] -translate-x-1/2 rounded-full transition-colors duration-150', active ? 'bg-signal' : 'bg-transparent group-hover:bg-accent/70 group-focus-visible:bg-accent')} />
            </button>
          }
        >
          <TagPalette list={list} onPick={(k) => add(pos, k)} />
        </Popover>
      </span>
    )
  }

  const at = new Map<number, number[]>()
  cues.forEach((c, i) => {
    const p = Math.min(c.at, chars.length)
    at.set(p, [...(at.get(p) ?? []), i])
  })
  const out: React.ReactNode[] = []
  for (let i = 0; i <= chars.length; i++) {
    out.push(gap(i))
    for (const idx of at.get(i) ?? []) {
      out.push(
        <CueChip
          key={`c${idx}`}
          tag={byKey.get(cues[idx].tag)}
          draggable={!disabled}
          onDragStart={(e) => { e.dataTransfer.effectAllowed = 'move'; e.dataTransfer.setData('text/plain', String(idx)); setDragFrom(idx) }}
          onRemove={disabled ? undefined : () => remove(idx)}
        />,
      )
    }
    if (i < chars.length) out.push(<span key={i}>{chars[i]}</span>)
  }
  return <p className="text-[15px] leading-[2.1]" onDragEnd={() => { setDragFrom(null); setOver(null) }}>{out}</p>
}

export function TagPalette({ list, onPick }: { list: PerfTag[]; onPick: (key: string) => void }) {
  const group = (kind: 'style' | 'sound', title: string, desc: string) => (
    <div>
      <div className="mb-1.5 flex items-baseline gap-2"><span className="text-[12.5px] font-medium">{title}</span><span className="text-[11px] text-faint">{desc}</span></div>
      <div className="flex flex-wrap gap-1">
        {list.filter((t) => t.kind === kind).map((t) => (
          <Tip key={t.key} label={t.hint}>
            <button onClick={() => onPick(t.key)} className={cn('rounded-md px-2 py-1 text-[12px] transition-colors', kind === 'style' ? 'bg-signal-soft text-warn hover:brightness-95' : 'bg-sunken text-ink hover:bg-line')}>{t.label}</button>
          </Tip>
        ))}
      </div>
    </div>
  )
  return (
    <div className="space-y-3">
      {group('style', '语气风格', '影响标签之后的内容')}
      {group('sound', '声音效果', '在这个位置发出声音')}
    </div>
  )
}
