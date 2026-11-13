import { useVirtualizer } from '@tanstack/react-virtual'
import { AnimatePresence, motion } from 'framer-motion'
import { AlertTriangle, ArrowDown, ArrowUp, AudioLines, Combine, Copy, Drama, Flag, GripVertical, Lock, MessageSquare, MoreHorizontal, Pencil, Plus, RefreshCw, Scissors, Trash2, X } from '@/components/icons'
import { memo, useEffect, useMemo, useRef, useState } from 'react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { copy, emotionName, emotions, segTypeName } from '@/lib/format'
import { PlayButton } from '@/components/audio'
import { usePlayer } from '@/lib/player'
import { Button, cn, confirm, Dialog, Kbd, Menu, type MenuItem, Popover, Spinner, Tip } from '@/components/ui'
import { can } from '../../hooks'
import { deleteSegments, insertSegment, mergeSegments, moveBy, moveSegments, patchSegments, splitSegment, useWS, type Draft } from './store'
import { CueText, segmentCues } from '@/components/cues'

export const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)
export const MOD = isMac ? '⌘' : 'Ctrl'
export const ALT = isMac ? '⌥' : 'Alt'
const PAD = 16 // top padding of the list content (py-4)

type DragState = { ids: string[]; gap: number; x: number; y: number; label: string }
type MenuAt = { id: string; x: number; y: number; align: 'start' | 'end' }

const clip = (t: string, n: number) => (t.length > n ? t.slice(0, n) + '…' : t)

export default function ScriptList({ book, chapterId, segs, chars }: { book: any; chapterId: string; segs: any[]; chars: Map<string, any> }) {
  const parent = useRef<HTMLDivElement>(null)
  const focus = useWS((s) => s.focus)
  const selected = useWS((s) => s.selected)
  const draft = useWS((s) => s.draft)
  const splitting = useWS((s) => s.splitting)
  const editable = can(book, 'script')
  const order = useMemo(() => segs.map((s) => s.id), [segs])

  // the line being typed is shown as an extra row right after its anchor
  const draftAt = !draft ? -1 : draft.afterId ? (order.indexOf(draft.afterId) + 1 || segs.length) : 0
  const count = segs.length + (draft ? 1 : 0)
  const segAt = (i: number) => (draft && i === draftAt ? null : segs[draft && i > draftAt ? i - 1 : i])
  const v = useVirtualizer({ count, getScrollElement: () => parent.current, estimateSize: () => 64, overscan: 12, getItemKey: (i) => segAt(i)?.id ?? 'draft' })

  useEffect(() => {
    const i = order.indexOf(focus ?? '')
    if (i >= 0) v.scrollToIndex(draft && i >= draftAt ? i + 1 : i, { align: 'auto' })
  }, [focus])
  useEffect(() => {
    if (draft) v.scrollToIndex(draftAt, { align: 'auto' })
  }, [draft?.key])
  // reading along: during a read-through the playing line becomes the current one and stays in view
  useEffect(() => usePlayer.subscribe((p, prev) => {
    if (p.id === prev.id || !p.continuous || !p.id?.startsWith('seg:')) return
    const id = p.id.slice(4)
    if (!order.includes(id)) return
    const st = useWS.getState()
    if (st.editing || st.draft || document.activeElement?.closest('input,textarea,select,[contenteditable=true]')) return
    st.setFocus(id)
  }), [order])

  // ---------------- drag to reorder ----------------
  const [drag, setDrag] = useState<DragState | null>(null)
  const dragRef = useRef<DragState | null>(null)
  const gapFor = (clientY: number) => {
    const el = parent.current!
    const y = clientY - el.getBoundingClientRect().top + el.scrollTop - PAD
    const ms = v.measurementsCache
    for (let i = 0; i < ms.length; i++) if (y < ms[i].start + ms[i].size / 2) return i
    return ms.length
  }
  const startDrag = (e: React.PointerEvent, id: string) => {
    if (!editable || e.button !== 0) return
    e.preventDefault()
    e.stopPropagation()
    useWS.getState().cancelDraft()
    const sel = useWS.getState().selected
    const ids = sel.length > 1 && sel.includes(id) ? order.filter((x) => sel.includes(x)) : [id]
    if (ids.length === 1) useWS.getState().setFocus(id)
    const st: DragState = { ids, gap: -1, x: e.clientX, y: e.clientY, label: ids.length > 1 ? `移动 ${ids.length} 句` : clip(segs.find((s) => s.id === id)?.text ?? '', 16) }
    dragRef.current = st
    setDrag(st)
    document.body.style.cursor = 'grabbing'
    document.body.style.userSelect = 'none'
    const update = (x: number, y: number) => {
      const d = dragRef.current
      if (!d) return
      const n = { ...d, x, y, gap: gapFor(y) }
      dragRef.current = n
      setDrag(n)
    }
    let raf = 0
    const tick = () => { // scroll while holding the line near the top or bottom edge
      const d = dragRef.current
      const el = parent.current
      if (!d || !el) return
      const r = el.getBoundingClientRect()
      const edge = 64
      const dy = d.y < r.top + edge ? -Math.ceil((r.top + edge - d.y) / 5) : d.y > r.bottom - edge ? Math.ceil((d.y - r.bottom + edge) / 5) : 0
      if (dy) {
        el.scrollTop += dy
        update(d.x, d.y)
      }
      raf = requestAnimationFrame(tick)
    }
    const move = (ev: PointerEvent) => update(ev.clientX, ev.clientY)
    const end = (commit: boolean) => {
      cancelAnimationFrame(raf)
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
      window.removeEventListener('pointercancel', cancel)
      window.removeEventListener('keydown', key)
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
      const d = dragRef.current
      dragRef.current = null
      setDrag(null)
      if (!commit || !d || d.gap < 0) return
      const before = order.slice(0, d.gap).filter((x) => !d.ids.includes(x))
      moveSegments(chapterId, d.ids, before.length ? before[before.length - 1] : null)
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
  // drop marker (hidden where dropping would change nothing)
  let marker: number | null = null
  if (drag && drag.gap >= 0) {
    const idx = drag.ids.map((id) => order.indexOf(id)).sort((a, b) => a - b)
    const contiguous = idx[idx.length - 1] - idx[0] === idx.length - 1
    if (!(contiguous && drag.gap >= idx[0] && drag.gap <= idx[idx.length - 1] + 1)) {
      const ms = v.measurementsCache
      marker = drag.gap < ms.length ? ms[drag.gap].start : (ms[ms.length - 1]?.end ?? 0)
    }
  }

  // ---------------- row menu (… button and right click) ----------------
  const [menu, setMenu] = useState<MenuAt | null>(null)
  const openMenu = (id: string, x: number, y: number, align: 'start' | 'end') => {
    const st = useWS.getState()
    if (!st.selected.includes(id)) st.setFocus(id)
    setMenu({ id, x, y, align })
  }
  const menuItems = (id: string): (MenuItem | 'sep' | false | null)[] => {
    const s = segs.find((x) => x.id === id)
    if (!s) return []
    const sel = useWS.getState().selected
    const ids = sel.length > 1 && sel.includes(id) ? order.filter((x) => sel.includes(x)) : [id]
    const multi = ids.length > 1
    const i = order.indexOf(id)
    const texts = segs.filter((x) => ids.includes(x.id)).map((x) => x.text).join('\n')
    return tidy([
      editable && !multi && !s.locked && { label: '编辑文字', icon: <Pencil />, hint: 'Enter', onSelect: () => useWS.getState().setEditing(id) },
      editable && !multi && { label: '在上方插入一句', icon: <ArrowUp />, onSelect: () => useWS.getState().startDraft(i > 0 ? order[i - 1] : null) },
      editable && !multi && { label: '在下方插入一句', icon: <Plus />, hint: `${MOD}+Enter`, onSelect: () => useWS.getState().startDraft(id) },
      editable && !multi && !s.locked && Array.from(s.text as string).length > 1 && { label: '拆分成两句…', icon: <Scissors />, onSelect: () => useWS.setState({ splitting: id }) },
      editable && multi && { label: `合并成一句`, icon: <Combine />, onSelect: () => mergeSegments(book.id, chapterId, ids).then(() => useWS.setState({ selected: [ids[0]], focus: ids[0], anchor: ids[0] })) },
      'sep',
      editable && { label: multi ? `上移 ${ids.length} 句` : '上移', icon: <ArrowUp />, hint: `${ALT}+↑`, disabled: order.indexOf(ids[0]) === 0, onSelect: () => moveBy(chapterId, segs, ids, -1) },
      editable && { label: multi ? `下移 ${ids.length} 句` : '下移', icon: <ArrowDown />, hint: `${ALT}+↓`, disabled: order.indexOf(ids[ids.length - 1]) === order.length - 1, onSelect: () => moveBy(chapterId, segs, ids, 1) },
      'sep',
      { label: multi ? `复制 ${ids.length} 句文字` : '复制文字', icon: <Copy />, onSelect: () => copy(texts).then(() => toast.success('已复制到剪贴板'), () => toast.error('复制失败，请手动选择文字复制')) },
      editable && { label: multi ? `删除 ${ids.length} 句` : '删除', icon: <Trash2 />, hint: 'Delete', danger: true, onSelect: () => deleteSegments(book.id, chapterId, ids.filter((x) => !segs.find((y) => y.id === x)?.locked)) },
    ])
  }

  const last = order[order.length - 1] ?? null
  return (
    <div className="relative min-h-0 flex-1">
      <div ref={parent} className="h-full overflow-y-auto">
        <div className="relative mx-auto max-w-[940px] px-7 py-4" style={{ height: v.getTotalSize() + PAD * 2 + (editable ? 64 : 0) }}>
          {v.getVirtualItems().map((it) => {
            const s = segAt(it.index)
            if (!s)
              return (
                <div key="draft" data-index={it.index} ref={v.measureElement} className="absolute inset-x-7" style={{ transform: `translateY(${it.start}px)` }}>
                  <DraftRow key={draft!.key} draft={draft!} chapterId={chapterId} chars={chars} prev={draft!.afterId ? segs[draftAt - 1] : undefined} />
                </div>
              )
            const si = draft && it.index > draftAt ? it.index - 1 : it.index
            const prev = segs[si - 1]
            return (
              <div key={s.id} data-index={it.index} ref={v.measureElement} className="absolute inset-x-7" style={{ transform: `translateY(${it.start}px)` }}>
                {prev && prev.para !== s.para && <div className={cn(s.sceneBreak ? 'my-3 flex items-center gap-3 text-[11px] text-faint' : 'h-2')}>{s.sceneBreak && <><span className="h-px flex-1 bg-line" />场景切换<span className="h-px flex-1 bg-line" /></>}</div>}
                <Row
                  s={s} index={si} char={chars.get(s.characterId)} chars={chars} active={focus === s.id} selected={selected.includes(s.id)} order={order} book={book} chapterId={chapterId} editable={editable}
                  dragging={!!drag?.ids.includes(s.id)} dragActive={!!drag} onDragStart={startDrag} onMenu={openMenu} insertAbove={si > 0 ? order[si - 1] : null}
                />
              </div>
            )
          })}
          {marker != null && (
            <div className="pointer-events-none absolute inset-x-5 z-30 flex items-center" style={{ top: marker + PAD - 1 }}>
              <span className="size-2 -translate-x-1 rounded-full border-2 border-accent bg-surface" />
              <span className="h-0.5 flex-1 rounded-full bg-accent" />
            </div>
          )}
          {editable && !(draft && draft.afterId === last) && (
            <button
              onClick={() => useWS.getState().startDraft(last)}
              className="absolute inset-x-7 flex h-11 items-center justify-center gap-2 rounded-[10px] border border-dashed border-line text-[13px] text-muted transition-colors hover:border-accent hover:bg-accent-soft/40 hover:text-accent"
              style={{ top: v.getTotalSize() + PAD + 10 }}
            >
              <Plus className="size-4" />在末尾添加一句<Kbd>{MOD}+Enter</Kbd>
            </button>
          )}
        </div>
      </div>
      {drag && (
        <div className="pointer-events-none fixed z-[70] flex items-center gap-1.5 rounded-full bg-ink px-3 py-1 text-[12px] text-bg shadow-[var(--shadow-pop)]" style={{ left: drag.x + 14, top: drag.y + 12 }}>
          <GripVertical className="size-3.5" />{drag.label}
        </div>
      )}
      {menu && (
        <Menu
          key={`${menu.id}:${menu.x}:${menu.y}`}
          open
          onOpenChange={(o) => !o && setMenu(null)}
          align={menu.align}
          trigger={<span aria-hidden style={{ position: 'fixed', left: menu.x, top: menu.y, width: 1, height: 1 }} />}
          items={menuItems(menu.id)}
        />
      )}
      <SplitDialog s={segs.find((x) => x.id === splitting)} chapterId={chapterId} onClose={() => useWS.setState({ splitting: null })} />
      <BulkBar book={book} chapterId={chapterId} chars={chars} segs={segs} />
    </div>
  )
}

/** Drops separators that would end up first, last or doubled after filtering. */
function tidy(items: (MenuItem | 'sep' | false | null | undefined | 0 | '')[]): (MenuItem | 'sep')[] {
  const out: (MenuItem | 'sep')[] = []
  for (const it of items) {
    if (!it) continue
    if (it === 'sep' && (!out.length || out[out.length - 1] === 'sep')) continue
    out.push(it)
  }
  while (out[out.length - 1] === 'sep') out.pop()
  return out
}

const statusDot: Record<string, string> = { ready: 'bg-ok', none: 'bg-line-strong', queued: 'bg-signal/60', generating: 'bg-signal animate-pulse', failed: 'bg-danger', stale: 'bg-warn' }
const statusText: Record<string, string> = { ready: '已生成', none: '未生成', queued: '排队中', generating: '生成中', failed: '生成失败', stale: '有改动，待重新生成' }

const Row = memo(function Row({ s, index, char, chars, active, selected, order, book, chapterId, editable, dragging, dragActive, onDragStart, onMenu, insertAbove }: any) {
  const editing = useWS((st) => st.editing === s.id)
  const playing = usePlayer((p) => p.id === 'seg:' + s.id && p.playing)
  const [text, setText] = useState(s.text)
  const ta = useRef<HTMLTextAreaElement>(null)
  useEffect(() => setText(s.text), [s.text])
  useEffect(() => {
    if (editing && ta.current) {
      ta.current.focus()
      ta.current.setSelectionRange(text.length, text.length)
    }
  }, [editing])
  const save = async () => {
    useWS.getState().setEditing(null)
    if (text.trim() && text !== s.text) await patchSegments(book.id, chapterId, [s.id], { text })
    else if (!text.trim()) setText(s.text)
  }
  const color = char?.color ?? '#94a3b8'
  const isNarr = s.type === 'narration'
  const canEdit = editable && !s.locked
  return (
    <div
      onClick={(e) => useWS.getState().click(s.id, e, order)}
      onDoubleClick={() => canEdit && useWS.getState().setEditing(s.id)}
      onContextMenu={(e) => {
        if (editing) return
        e.preventDefault()
        onMenu(s.id, e.clientX, e.clientY, 'start')
      }}
      className={cn(
        'group relative flex flex-wrap gap-x-3 gap-y-1 rounded-[10px] border py-2.5 pl-3 pr-2 transition-[background-color,border-color,opacity] duration-100 sm:flex-nowrap',
        active ? 'border-accent/60 bg-accent-soft/60' : selected ? 'border-accent/30 bg-accent-soft/40' : 'border-transparent hover:bg-surface',
        playing && 'ring-2 ring-signal/50',
        dragging && 'opacity-40',
        s.pending && 'opacity-70',
      )}
    >
      {editable && !dragActive && (
        <button
          tabIndex={-1}
          onClick={(e) => {
            e.stopPropagation()
            useWS.getState().startDraft(insertAbove)
          }}
          className="absolute inset-x-8 -top-[7px] z-10 flex h-3.5 items-center opacity-0 transition-opacity delay-100 hover:opacity-100"
          aria-label="在这里插入一句"
        >
          <span className="h-px flex-1 bg-accent/50" />
          <span className="mx-2 flex items-center gap-1 rounded-full bg-accent px-2 py-px text-[11px] font-medium text-accent-ink"><Plus className="size-3" />插入一句</span>
          <span className="h-px flex-1 bg-accent/50" />
        </button>
      )}
      {editable && (
        <Tip label="拖动调整顺序">
          <span
            onPointerDown={(e) => onDragStart(e, s.id)}
            onClick={(e) => e.stopPropagation()}
            className={cn('absolute -left-6 top-2.5 grid h-7 w-5 cursor-grab touch-none place-items-center rounded text-faint transition-opacity hover:bg-sunken hover:text-ink active:cursor-grabbing group-hover:opacity-100', active || selected ? 'opacity-100' : 'opacity-0')}
            aria-label="拖动调整顺序"
          >
            <GripVertical className="size-4" />
          </span>
        </Tip>
      )}
      <span className="absolute inset-y-2 left-0 w-[3px] rounded-full" style={{ background: color, opacity: isNarr ? 0.35 : 1 }} />
      <div className="flex w-full shrink-0 flex-row items-center gap-2 sm:w-[104px] sm:flex-col sm:items-start sm:gap-1 sm:pt-0.5">
        <SpeakerChip s={s} char={char} chars={chars} book={book} chapterId={chapterId} editable={editable} />
        <span className="flex items-center gap-1 text-[11px] text-faint">
          <span className="tnum">{index + 1}</span>
          {!isNarr && <span>· {segTypeName[s.type]}</span>}
          {s.emotion && s.emotion !== 'neutral' && <span className="text-muted">· {emotionName[s.emotion] ?? s.emotion}{s.intensity > 1 && '+'.repeat(s.intensity - 1)}</span>}
        </span>
      </div>
      <div className="min-w-0 flex-1">
        {editing ? (
          <>
            <textarea
              ref={ta}
              value={text}
              onChange={(e) => setText(e.target.value)}
              onBlur={save}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                  e.preventDefault()
                  save()
                  useWS.getState().startDraft(s.id)
                } else if (e.key === 'Enter' && !e.shiftKey) {
                  e.preventDefault()
                  save()
                } else if (e.key === 'Escape') {
                  setText(s.text)
                  useWS.getState().setEditing(null)
                }
              }}
              onClick={(e) => e.stopPropagation()}
              onDoubleClick={(e) => e.stopPropagation()}
              rows={Math.max(1, Math.ceil(text.length / 38))}
              className="w-full resize-none rounded-md border border-accent bg-surface px-2 py-1 text-[15px] leading-[1.75] focus:outline-none"
            />
            <p className="mt-1 text-[11px] text-faint">Enter 保存 · Esc 取消 · {MOD}+Enter 保存并在下方添加</p>
          </>
        ) : (
          <p className={cn('text-[15px] leading-[1.75]', isNarr ? 'text-ink/85' : 'text-ink', s.type === 'monologue' && 'italic')}>
            <CueText text={s.text} cues={segmentCues(s)} small renderChar={(ch, i) => <PronChar ch={ch} pron={(s.prons ?? []).find((p: any) => p.offset === i)} />} />
          </p>
        )}
        {(s.qc === 'suspect' || s.lastError || s.note || s.instruction || s.openIssues > 0) && !editing && (
          <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-[11.5px]">
            {s.openIssues > 0 && (
              <span className={cn('flex items-center gap-1 font-medium', s.verifyingIssues >= s.openIssues ? 'text-warn' : 'text-danger')}>
                <Flag className="size-3" />{s.verifyingIssues >= s.openIssues ? '已修复，待复核' : '待返工'}{s.issueTitle && s.issueTitle !== '需要返工' ? `：${s.issueTitle}` : ''}{s.openIssues > 1 && `（${s.openIssues} 个）`}
              </span>
            )}
            {s.instruction && <span className="flex items-center gap-1 text-muted"><Drama className="size-3" />{s.instruction}</span>}
            {s.audioStatus === 'failed' && s.lastError && <span className="text-danger">{s.lastError}</span>}
            {s.qc === 'suspect' && s.qcNote && <span className="flex items-center gap-1 text-warn"><AlertTriangle className="size-3" />{s.qcNote}</span>}
            {s.note && <span className="flex items-center gap-1 text-muted"><MessageSquare className="size-3" />{s.note}</span>}
          </div>
        )}
      </div>
      <div className="flex shrink-0 items-start justify-end gap-1 pt-0.5 sm:w-[96px]">
        <Tip label="更多操作（也可以右键）">
          <button
            onClick={(e) => {
              e.stopPropagation()
              const r = e.currentTarget.getBoundingClientRect()
              onMenu(s.id, r.right, r.bottom - 2, 'end')
            }}
            className={cn('mt-0.5 grid size-7 place-items-center rounded-md text-faint transition-opacity hover:bg-sunken hover:text-ink', active ? 'opacity-100' : 'opacity-0 group-hover:opacity-100 focus-visible:opacity-100')}
            aria-label="更多操作"
          >
            <MoreHorizontal className="size-4" />
          </button>
        </Tip>
        {s.locked && <Tip label="已锁定，不会被批量重新生成"><Lock className="mt-2 size-3 text-faint" /></Tip>}
        {s.voiceId && <Tip label="这一句单独换了声音"><AudioLines className="mt-2 size-3 text-accent" /></Tip>}
        {s.pending ? (
          <Tip label="正在保存"><span className="grid size-7 place-items-center"><Spinner className="size-3.5" /></span></Tip>
        ) : (
          <>
            <Tip label={statusText[s.audioStatus] ?? s.audioStatus}><span className={cn('mt-2.5 size-1.5 shrink-0 rounded-full', statusDot[s.audioStatus])} /></Tip>
            {s.audioStatus === 'generating' || s.audioStatus === 'queued' ? (
              <span className="grid size-7 place-items-center"><span className="eq flex h-3 items-end text-signal"><span /><span /><span /></span></span>
            ) : (
              <PlayButton id={'seg:' + s.id} url={s.take?.url} title={s.text} size="xs" className="!size-7" />
            )}
          </>
        )}
      </div>
    </div>
  )
})

/** A new line typed in place: Enter saves, Esc cancels, Mod+Enter saves and starts the next one. */
function DraftRow({ draft, chapterId, chars, prev }: { draft: Draft; chapterId: string; chars: Map<string, any>; prev?: any }) {
  const list = Array.from(chars.values()).filter((c: any) => !c.candidate)
  const narrator = list.find((c: any) => c.kind === 'narrator')
  const [charId, setCharId] = useState<string>(prev && chars.has(prev.characterId) ? prev.characterId : narrator?.id ?? '')
  const [text, setText] = useState('')
  const [picking, setPicking] = useState(false)
  const ta = useRef<HTMLTextAreaElement>(null)
  const box = useRef<HTMLDivElement>(null)
  const live = useRef({ text, picking }) // read by the delayed blur check
  live.current = { text, picking }
  useEffect(() => {
    const t = setTimeout(() => ta.current?.focus(), 30)
    return () => clearTimeout(t)
  }, [])
  const c = chars.get(charId)
  const cancel = () => useWS.getState().cancelDraft()
  const save = (more: boolean) => {
    const t = live.current.text.trim()
    if (!t) return cancel()
    const id = insertSegment(chapterId, draft.afterId, { text: t, characterId: charId, type: !c || c.kind === 'narrator' ? 'narration' : 'dialogue' })
    useWS.setState({ focus: id, selected: [id], anchor: id, draft: more ? { afterId: id, key: Date.now() } : null })
  }
  return (
    <div ref={box} className="relative my-1 flex gap-3 rounded-[10px] border border-accent bg-surface py-2.5 pl-3 pr-2 shadow-[0_0_0_3px_rgb(15_124_112/0.12)]" onClick={(e) => e.stopPropagation()}>
      <span className="absolute inset-y-2 left-0 w-[3px] rounded-full" style={{ background: c?.color ?? '#94a3b8' }} />
      <div className="flex w-[104px] shrink-0 flex-col items-start gap-1 pt-0.5">
        <Popover
          open={picking}
          onOpenChange={(o) => {
            setPicking(o)
            if (!o) setTimeout(() => ta.current?.focus(), 0)
          }}
          width={220}
          trigger={
            <button className="flex max-w-full items-center gap-1.5 rounded-md px-1.5 py-0.5 text-[12.5px] font-medium hover:bg-sunken">
              <span className="size-2 shrink-0 rounded-full" style={{ background: c?.color ?? '#94a3b8' }} />
              <span className="truncate">{c?.name ?? '旁白'}</span>
            </button>
          }
        >
          <div className="-m-2 max-h-72 overflow-y-auto">
            {list.map((x: any) => (
              <button key={x.id} onClick={() => { setCharId(x.id); setPicking(false) }} className={cn('flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-[13px] hover:bg-sunken', x.id === charId && 'bg-sunken')}>
                <span className="size-2 rounded-full" style={{ background: x.color }} />
                <span className="flex-1 truncate">{x.name}</span>
              </button>
            ))}
          </div>
        </Popover>
        <span className="px-1.5 text-[11px] font-medium text-accent">新句子</span>
      </div>
      <div className="min-w-0 flex-1">
        <textarea
          ref={ta}
          value={text}
          placeholder="输入这一句的文字…"
          onChange={(e) => setText(e.target.value)}
          onBlur={() => setTimeout(() => {
            // clicking elsewhere saves what was typed (or drops an empty line); the speaker picker keeps it open
            if (live.current.picking || box.current?.contains(document.activeElement) || !useWS.getState().draft) return
            live.current.text.trim() ? save(false) : cancel()
          }, 150)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
              e.preventDefault()
              save(true)
            } else if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault()
              save(false)
            } else if (e.key === 'Escape') {
              e.preventDefault()
              cancel()
            }
          }}
          rows={Math.max(2, Math.ceil(text.length / 38))}
          className="w-full resize-none rounded-md border border-line bg-surface px-2 py-1 text-[15px] leading-[1.75] placeholder:text-faint focus:border-accent focus:outline-none"
        />
        <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-faint">
          <span>Enter 保存</span>
          <span>Shift+Enter 换行</span>
          <span>{MOD}+Enter 保存并继续添加</span>
          <span>Esc 取消</span>
          <span className="ml-auto flex gap-1.5">
            <Button size="xs" variant="ghost" onMouseDown={(e) => e.preventDefault()} onClick={cancel}>取消</Button>
            <Button size="xs" variant="primary" onMouseDown={(e) => e.preventDefault()} disabled={!text.trim()} onClick={() => save(false)}>保存</Button>
          </span>
        </div>
      </div>
    </div>
  )
}

function PronChar({ ch, pron }: { ch: string; pron?: { pinyin: string } }) {
  if (!pron) return <>{ch}</>
  return (
    <Tip label={`读作 ${pron.pinyin}`}>
      <span className="underline decoration-accent decoration-dotted decoration-2 underline-offset-4">{ch}</span>
    </Tip>
  )
}

function SpeakerChip({ s, char, chars, book, chapterId, editable }: any) {
  const [open, setOpen] = useState(false)
  const list = Array.from(chars.values()).filter((c: any) => !c.candidate)
  const chip = (
    <button onClick={(e) => e.stopPropagation()} disabled={!editable} className="flex max-w-full items-center gap-1.5 rounded-md px-1.5 py-0.5 text-[12.5px] font-medium transition-colors hover:bg-sunken disabled:hover:bg-transparent">
      <span className="size-2 shrink-0 rounded-full" style={{ background: char?.color ?? '#94a3b8' }} />
      <span className="truncate">{char?.name ?? '未指定'}</span>
    </button>
  )
  if (!editable) return chip
  return (
    <Popover open={open} onOpenChange={setOpen} width={220} trigger={chip}>
      <div className="-m-2 max-h-72 overflow-y-auto">
        {list.map((c: any, ci: number) => (
          <button
            key={c.id}
            onClick={async (e) => {
              e.stopPropagation()
              setOpen(false)
              await patchSegments(book.id, chapterId, [s.id], { characterId: c.id, type: c.kind === 'narrator' ? 'narration' : s.type === 'narration' ? 'dialogue' : s.type })
            }}
            className={cn('flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-[13px] hover:bg-sunken', c.id === s.characterId && 'bg-sunken')}
          >
            <span className="size-2 rounded-full" style={{ background: c.color }} />
            <span className="flex-1 truncate">{c.name}</span>
            {!c.voiceId && c.mode !== 'human' && <span className="text-[10.5px] text-warn">未选角</span>}
            {ci < 9 && <kbd className="rounded border border-line px-1 text-[10px] text-faint" title={`快捷键：选中句子后按 ${ci + 1}`}>{ci + 1}</kbd>}
          </button>
        ))}
      </div>
    </Popover>
  )
}

/** Split a line in two: click between characters to choose where. */
export function SplitDialog({ s, chapterId, onClose }: { s?: any; chapterId: string; onClose: () => void }) {
  const chars = Array.from((s?.text ?? '') as string)
  const [at, setAt] = useState(0)
  useEffect(() => {
    if (s) setAt(Math.floor(chars.length / 2))
  }, [s?.id])
  return (
    <Dialog
      open={!!s}
      onOpenChange={(o) => !o && onClose()}
      title="拆分成两句"
      description="点击文字之间的位置选择从哪里断开，竖线后面的文字会成为新的一句。"
      width={480}
      footer={<><Button onClick={onClose}>取消</Button><Button variant="primary" disabled={at <= 0 || at >= chars.length} onClick={async () => { await splitSegment(chapterId, s.id, at); onClose(); toast.success('已拆分成两句') }}>拆分</Button></>}
    >
      <p className="select-none text-[15px] leading-[2.1]">
        {chars.map((c, i) => (
          <span key={i} onClick={() => setAt(i + 1)} className="cursor-pointer rounded-sm hover:bg-accent-soft">
            {c}
            {i + 1 === at && <span className="mx-0.5 inline-block h-5 w-0.5 translate-y-1 animate-pulse bg-signal" />}
          </span>
        ))}
      </p>
      {at > 0 && at < chars.length && (
        <div className="mt-4 space-y-1.5 rounded-[10px] bg-sunken/60 px-3 py-2.5 text-[13px]">
          <div><span className="mr-2 text-faint">第一句</span>{chars.slice(0, at).join('')}</div>
          <div><span className="mr-2 text-faint">第二句</span>{chars.slice(at).join('')}</div>
        </div>
      )}
    </Dialog>
  )
}

function BulkBar({ book, chapterId, chars, segs }: { book: any; chapterId: string; chars: Map<string, any>; segs: any[] }) {
  const selected = useWS((s) => s.selected)
  const n = selected.length
  const editable = can(book, 'script')
  const clear = () => useWS.setState({ selected: [] })
  const ordered = segs.filter((s) => selected.includes(s.id)).map((s) => s.id)
  return (
    <AnimatePresence>
      {n > 1 && (
        <motion.div initial={{ y: 20, opacity: 0 }} animate={{ y: 0, opacity: 1 }} exit={{ y: 20, opacity: 0 }} transition={{ type: 'spring', stiffness: 400, damping: 32 }} className="absolute bottom-4 left-1/2 z-20 flex max-w-[calc(100%-2rem)] -translate-x-1/2 items-center gap-1 overflow-x-auto rounded-full border border-line bg-surface py-1.5 pl-4 pr-1.5 shadow-[var(--shadow-pop)]">
          <span className="mr-2 shrink-0 text-[13px] font-medium tnum">已选 {n} 句</span>
          {editable && (
            <>
              <Menu
                align="start"
                trigger={<Button size="xs" variant="ghost">改说话人</Button>}
                items={Array.from(chars.values()).filter((c: any) => !c.candidate).map((c: any) => ({ label: c.name, icon: <span className="size-2 rounded-full" style={{ background: c.color }} />, onSelect: () => patchSegments(book.id, chapterId, selected, { characterId: c.id }).then(() => toast.success(`已把 ${n} 句改为「${c.name}」`)) }))}
              />
              <Menu align="start" trigger={<Button size="xs" variant="ghost">改情绪</Button>} items={emotions.map(([k, l]) => ({ label: l, onSelect: () => patchSegments(book.id, chapterId, selected, { emotion: k }).then(() => toast.success(`已把 ${n} 句改为「${l}」`)) }))} />
              <Tip label={`${ALT}+↑ / ${ALT}+↓ 也可以移动`}>
                <span className="flex">
                  <Button size="xs" variant="ghost" icon={<ArrowUp className="size-3.5" />} onClick={() => moveBy(chapterId, segs, ordered, -1)} aria-label="上移" />
                  <Button size="xs" variant="ghost" icon={<ArrowDown className="size-3.5" />} onClick={() => moveBy(chapterId, segs, ordered, 1)} aria-label="下移" />
                </span>
              </Tip>
              <Button size="xs" variant="ghost" icon={<Combine className="size-3.5" />} onClick={async () => {
                const list = await mergeSegments(book.id, chapterId, ordered)
                if (list) {
                  useWS.setState({ selected: [ordered[0]], focus: ordered[0], anchor: ordered[0] })
                  toast.success(`已把 ${n} 句合并成一句`)
                }
              }}>合并</Button>
              <Button size="xs" variant="ghost" icon={<Lock className="size-3.5" />} onClick={() => patchSegments(book.id, chapterId, selected, { locked: true }).then(() => toast.success(`已锁定 ${n} 句，批量生成时会跳过`))}>锁定</Button>
              <Button size="xs" variant="ghost" className="text-danger" icon={<Trash2 className="size-3.5" />} onClick={async () => {
                if (!(await confirm({ title: `删除 ${n} 句？`, description: '会从画本中移除这些句子，原文不受影响。删除后可以撤销。', danger: true, confirmText: '删除' }))) return
                await deleteSegments(book.id, chapterId, selected)
                clear()
              }}>删除</Button>
            </>
          )}
          {can(book, 'generate') && (
            <Button size="xs" variant="primary" icon={<RefreshCw className="size-3.5" />} onClick={async () => {
              await api.post(`/books/${book.id}/generate`, { chapterIds: [chapterId], scope: 'segments', segmentIds: selected, regenerate: true })
              toast.success(`正在重新生成 ${n} 句`)
            }}>重新生成</Button>
          )}
          <button onClick={clear} className="ml-1 grid size-7 shrink-0 place-items-center rounded-full text-faint hover:bg-sunken hover:text-ink" aria-label="取消选择"><X className="size-4" /></button>
        </motion.div>
      )}
    </AnimatePresence>
  )
}
