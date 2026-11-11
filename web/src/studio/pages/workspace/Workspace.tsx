import { useQuery } from '@tanstack/react-query'
import { motion } from 'framer-motion'
import { ThemeToggle } from '@/components/theme-toggle'
import { AlertTriangle, AudioLines, Check, RotateCcw, ChevronLeft, ChevronRight, ClipboardCheck, FileText, Layers, ListMusic, MessageSquare, PanelRight, Plus, ScrollText, SlidersHorizontal, Sparkles } from '@/components/icons'
import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { duration } from '@/lib/format'
import { useLive, useLiveEvents, useRunningJobs } from '@/lib/live'
import { usePlayer } from '@/lib/player'
import { useSession } from '@/lib/session'
import { MiniPlayerBar } from '@/components/audio'
import { Avatar, Badge, Button, cn, Empty, Fade, IconButton, Kbd, Popover, Progress, Select, Skeleton, Spinner, Tip } from '@/components/ui'
import { can, useBook, useChapters, useCharacters } from '../../hooks'
import { GenerateDialog } from '../book/Chapters'
import { deleteSegments, markForRework, moveBy, patchSegments, undoLast, undoStatus, useSave, useUndo, useWS } from './store'
import ScriptList, { ALT, MOD } from './ScriptList'
import Inspector from './Inspector'
import { CommentsPanel, MixPanel, RenderPanel, ReviewPanel, SourcePanel } from './Panels'
import { useShallow } from 'zustand/react/shallow'

const panels = [
  { key: 'segment', label: '片段', icon: SlidersHorizontal },
  { key: 'source', label: '原文', icon: ScrollText },
  { key: 'mix', label: '配乐', icon: ListMusic },
  { key: 'render', label: '成品', icon: Layers },
  { key: 'review', label: '审听', icon: ClipboardCheck },
  { key: 'comments', label: '评论', icon: MessageSquare },
]

export default function Workspace() {
  useLiveEvents()
  const { bid, cid } = useParams() as { bid: string; cid: string }
  const [params] = useSearchParams()
  const nav = useNavigate()
  const book = useBook(bid)
  const chapters = useChapters(bid)
  const characters = useCharacters(bid)
  const script = useQuery({ queryKey: ['script', cid], queryFn: () => api.get(`/chapters/${cid}/script`) })
  const { panel, setPanel, setFocus } = useWS()
  const [gen, setGen] = useState(false)
  const [side, setSide] = useState(true)
  const running = useRunningJobs((j) => j.chapterId === cid)
  const me = useSession((s) => s.user)
  const presence = useLive(useShallow((s) => (s.presence[cid] ?? []).filter((p: any) => p.userId !== me?.id)))

  const segs: any[] = useMemo(() => (script.data?.segments ?? []).filter((s: any) => !s.removed), [script.data])
  const chars = useMemo(() => new Map((characters.data ?? []).map((c) => [c.id, c])), [characters.data])
  const list = chapters.data ?? []
  const idx = list.findIndex((c) => c.id === cid)
  const chapter = list[idx]
  const prev = list[idx - 1]
  const next = list[idx + 1]
  const bk = book.data
  const selected = useWS((s) => s.selected)

  useEffect(() => {
    useWS.setState({ bookId: bid, chapterId: cid, selected: [], focus: null, draft: null, editing: null, panel: params.get('panel') || 'segment' })
    undoStatus(cid)
    const seg = params.get('segment')
    if (seg) setTimeout(() => setFocus(seg), 300)
  }, [cid])

  // presence heartbeat
  const focus = useWS((s) => s.focus)
  useEffect(() => {
    const beat = () => api.post(`/chapters/${cid}/presence`, { segmentId: useWS.getState().focus ?? '' }).catch(() => {})
    beat()
    const t = setInterval(beat, 20000)
    return () => {
      clearInterval(t)
      api.post(`/chapters/${cid}/presence`, { leave: true }).catch(() => {})
    }
  }, [cid])
  useEffect(() => {
    const t = setTimeout(() => api.post(`/chapters/${cid}/presence`, { segmentId: focus ?? '' }).catch(() => {}), 600)
    return () => clearTimeout(t)
  }, [focus])

  // keyboard: ↑↓ select, Alt+↑↓ move, Enter edit, Mod+Enter add, space play, Delete remove
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const t = e.target instanceof Element ? e.target : null
      if (t?.closest('input,textarea,select,[contenteditable=true],[role=dialog],[role=menu]')) return
      if ((e.key === 'Enter' || e.key === ' ') && t?.closest('button,a,[role=button]')) return // keep native activation
      const { focus, selected } = useWS.getState()
      const i = segs.findIndex((s) => s.id === focus)
      const editable = bk && can(bk, 'script')
      if (editable && (e.metaKey || e.ctrlKey) && !e.shiftKey && e.key.toLowerCase() === 'z') {
        e.preventDefault()
        undoLast(cid)
      } else if (editable && e.altKey && (e.key === 'ArrowUp' || e.key === 'ArrowDown')) {
        e.preventDefault()
        const ids = selected.length ? selected : focus ? [focus] : []
        if (ids.length) moveBy(cid, segs, ids, e.key === 'ArrowUp' ? -1 : 1)
      } else if (editable && e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
        e.preventDefault()
        useWS.getState().startDraft(focus ?? segs[segs.length - 1]?.id ?? null)
      } else if (editable && e.key === 'Enter' && focus && !segs[i]?.locked) {
        e.preventDefault()
        useWS.getState().setEditing(focus)
      } else if (e.key === 'ArrowDown' || e.key === 'j') {
        e.preventDefault()
        const n = segs[Math.min(segs.length - 1, i + 1)]
        n && setFocus(n.id)
      } else if (e.key === 'ArrowUp' || e.key === 'k') {
        e.preventDefault()
        const n = segs[Math.max(0, i - 1)]
        n && setFocus(n.id)
      } else if (e.key === ' ' && focus) {
        e.preventDefault()
        const s = segs[i]
        if (s?.take?.url) usePlayer.getState().toggle('seg:' + s.id, s.take.url, s.text)
      } else if ((e.key === 'm' || e.key === 'M') && !e.metaKey && !e.ctrlKey && !e.altKey && bk && (can(bk, 'review') || can(bk, 'manage') || can(bk, 'comment'))) {
        // proof-listening: mark the line being heard (or the current one) for rework, playback keeps going
        const p = usePlayer.getState()
        const playingId = p.playing && p.id?.startsWith('seg:') ? p.id.slice(4) : null
        const k = playingId && segs.some((x) => x.id === playingId) ? segs.findIndex((x) => x.id === playingId) : i
        if (k >= 0) {
          e.preventDefault()
          markForRework(bid, cid, segs[k], k + 1)
        }
      } else if (editable && /^[1-9]$/.test(e.key) && !e.metaKey && !e.ctrlKey && !e.altKey) {
        // 1–9: speaker shortcut, numbered like the speaker list
        const c = Array.from(chars.values()).filter((x: any) => !x.candidate)[Number(e.key) - 1]
        const ids = (selected.length ? selected : focus ? [focus] : []).filter((id) => { const x = segs.find((y) => y.id === id); return x && !x.locked && x.characterId !== c?.id })
        if (c && ids.length) {
          e.preventDefault()
          const wasNarration = ids.filter((id) => segs.find((y) => y.id === id)?.type === 'narration')
          const rest = ids.filter((id) => !wasNarration.includes(id))
          if (c.kind === 'narrator') patchSegments(bid, cid, ids, { characterId: c.id, type: 'narration' })
          else {
            if (wasNarration.length) patchSegments(bid, cid, wasNarration, { characterId: c.id, type: 'dialogue' })
            if (rest.length) patchSegments(bid, cid, rest, { characterId: c.id })
          }
          if (ids.length > 1) toast.success(`已把 ${ids.length} 句改为「${c.name}」`)
        }
      } else if (e.key === 'Escape') useWS.getState().clear()
      else if ((e.key === 'Delete' || e.key === 'Backspace') && bk && can(bk, 'script')) {
        const ids = useWS.getState().selected.filter((id) => !segs.find((x) => x.id === id)?.locked)
        if (ids.length) {
          e.preventDefault()
          deleteSegments(bid, cid, ids)
        }
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [segs, bk, chars])

  const parse = async () => {
    await api.post(`/books/${bid}/scripts/parse`, { chapterIds: [cid], mode: 'ai' })
    toast.success('AI 正在生成画本')
  }
  const render = async () => {
    try {
      await api.post(`/chapters/${cid}/render`, { preset: bk?.settings?.loudnessPreset })
      toast.success('开始合成，完成后可以在「成品」中试听')
      setPanel('render')
    } catch (e: any) {
      if (e.code === 'missing_audio' || /缺少|没有音频/.test(e.message)) {
        toast.error(e.message, { action: { label: '仍然合成', onClick: () => api.post(`/chapters/${cid}/render`, { allowMissing: true }).then(() => setPanel('render')) } })
      } else toast.error(e.message)
    }
  }
  // read-through from the current line (or the top), the way a proof-listener works through a chapter
  const playAll = (fromStart = false) => {
    const { focus } = useWS.getState()
    const at = fromStart ? 0 : Math.max(0, segs.findIndex((s) => s.id === focus))
    const q = segs.slice(at).filter((s) => s.take?.url).map((s) => ({ id: 'seg:' + s.id, url: s.take.url, title: s.text }))
    if (!q.length) return toast(segs.some((s) => s.take?.url) ? '后面没有已配音的句子了' : '还没有可以播放的句子，先生成配音')
    const [first, ...rest] = q
    usePlayer.getState().play(first.id, first.url, first.title, rest, true)
  }

  const ready = segs.filter((s) => s.audioStatus === 'ready').length
  // the main button follows the work: voice the lines first, then put the chapter together
  const needVoice = segs.some((s) => s.type !== 'sfx' && s.type !== 'pause' && s.audioStatus !== 'ready')
  const parsing = running.find((j) => j.type === 'script_parse' || j.type === 'script_sync')

  return (
    <div className="flex h-full flex-col bg-bg">
      <header className="flex h-14 shrink-0 items-center gap-2 border-b border-line bg-surface px-3 sm:gap-3 sm:px-4">
        <Link to={`/books/${bid}/chapters`} className="flex items-center gap-1 text-[13px] text-muted hover:text-ink">
          <ChevronLeft className="size-4" />
          <span className="hidden max-w-40 truncate sm:inline">{bk?.title}</span>
        </Link>
        <span className="text-line-strong max-sm:hidden">/</span>
        <div className="flex min-w-0 items-center gap-1">
          <span className="contents max-sm:hidden"><IconButton label="上一章" size="xs" disabled={!prev} icon={<ChevronLeft className="size-4" />} onClick={() => nav(`/books/${bid}/chapters/${prev.id}`)} /></span>
          <Select size="sm" value={cid} onChange={(v) => nav(`/books/${bid}/chapters/${v}`)} options={list.map((c) => [c.id, `${c.index}. ${c.title}`] as [string, string])} className="w-32 sm:w-56" />
          <span className="contents max-sm:hidden"><IconButton label="下一章" size="xs" disabled={!next} icon={<ChevronRight className="size-4" />} onClick={() => nav(`/books/${bid}/chapters/${next.id}`)} /></span>
        </div>
        {segs.length > 0 && (
          <div className="hidden items-center gap-2 text-xs text-muted lg:flex">
            <div className="w-28"><Progress value={segs.length ? ready / segs.length : 0} height={4} /></div>
            <span className="tnum">{ready}/{segs.length} 句</span>
          </div>
        )}
        <div className="ml-auto flex min-w-0 items-center gap-2">
          <SaveStatus />
          {bk && can(bk, 'script') && <UndoButton chapterId={cid} />}
          {presence.length > 0 && (
            <div className="mr-1 flex -space-x-1.5">
              {presence.slice(0, 4).map((p: any) => (
                <Tip key={p.userId} label={`${p.nickname} 正在查看`}><span className="rounded-full ring-2 ring-surface"><Avatar name={p.nickname} src={p.avatarUrl} size={24} /></span></Tip>
              ))}
            </div>
          )}
          {bk && can(bk, 'script') && segs.length > 0 && (
            <Tip label={`在当前选中的句子下方添加（${MOD}+Enter）`}>
              <Button size="sm" variant="ghost" icon={<Plus className="size-4" />} onClick={() => useWS.getState().startDraft(useWS.getState().focus ?? segs[segs.length - 1]?.id ?? null)} className="max-md:hidden">添加句子</Button>
            </Tip>
          )}
          <Tip label="从选中的句子开始连续播放，听到问题按 M 标记，播放不会停">
            <Button size="sm" variant="ghost" icon={<AudioLines className="size-4" />} onClick={() => playAll()} className="max-md:hidden">连续播放</Button>
          </Tip>
          {bk && can(bk, 'generate') && segs.length > 0 && <Button size="sm" variant={needVoice ? 'primary' : 'secondary'} icon={<Sparkles className="size-4" />} onClick={() => setGen(true)}><span className="hidden sm:inline">{selected.length > 1 ? `生成选中 ${selected.length} 句` : needVoice ? `生成配音（${segs.length - ready} 句）` : '生成配音'}</span><span className="sm:hidden">配音</span></Button>}
          {bk && can(bk, 'post') && segs.length > 0 && <Button size="sm" variant={needVoice ? 'secondary' : 'primary'} icon={<Layers className="size-4" />} onClick={render}>{chapter?.renderId && !chapter?.renderStale ? '重新合成' : '合成'}</Button>}
          <ThemeToggle className="size-8" />
          <span className="hidden md:contents"><IconButton label={side ? '收起侧栏' : '展开侧栏'} icon={<PanelRight className="size-4" />} onClick={() => setSide(!side)} /></span>
        </div>
      </header>

      {script.data?.scriptStale && (
        <div className="flex items-center gap-3 border-b border-warn/30 bg-warn-soft px-4 py-2 text-[13px] text-warn">
          原文已修改，画本还没有同步。
          <Button size="xs" onClick={async () => { await api.post(`/chapters/${cid}/sync`); toast.success('正在同步画本') }}>同步画本</Button>
        </div>
      )}

      <div className="flex min-h-0 flex-1">
        <div className="flex min-w-0 flex-1 flex-col">
          {script.isLoading || !bk ? (
            <div className="space-y-2 p-6">{Array.from({ length: 8 }, (_, i) => <Skeleton key={i} className="h-14" />)}</div>
          ) : parsing ? (
            <div className="grid flex-1 place-items-center">
              <div className="w-80 text-center">
                <span className="eq mx-auto flex h-5 w-fit items-end text-signal [&>span]:w-1"><span /><span /><span /></span>
                <p className="mt-4 text-[14px] font-medium">{parsing.progressText || 'AI 正在阅读这一章……'}</p>
                <Progress value={parsing.progress} tone="signal" className="mt-3" height={4} />
                <p className="mt-3 text-xs text-muted">会自动识别说话人、情绪和多音字，通常不到一分钟。</p>
              </div>
            </div>
          ) : !segs.length ? (
            <Empty
              className="flex-1"
              icon={<FileText />}
              title="这一章还没有画本"
              description="画本把章节拆成一句句台词，标好谁在说、用什么情绪。AI 只做标注，不会改动原文。"
              action={can(bk, 'script') && <Button variant="primary" icon={<Sparkles className="size-4" />} onClick={parse}>AI 生成画本</Button>}
            />
          ) : (
            <ScriptList book={bk} chapterId={cid} segs={segs} chars={chars} />
          )}
          <div className="flex h-8 shrink-0 items-center gap-4 overflow-hidden whitespace-nowrap border-t border-line bg-surface px-4 text-[11.5px] text-faint">
            <span><Kbd>↑</Kbd> <Kbd>↓</Kbd> 切换句子</span>
            <span><Kbd>空格</Kbd> 播放</span>
            {bk && (can(bk, 'review') || can(bk, 'manage') || can(bk, 'comment')) && <span><Kbd>M</Kbd> 标记返工</span>}
            {bk && can(bk, 'script') && <span className="hidden lg:inline"><Kbd>1</Kbd>–<Kbd>9</Kbd> 换说话人</span>}
            {bk && can(bk, 'script') && <span className="hidden xl:inline"><Kbd>Enter</Kbd> 编辑</span>}
            <Popover width={300} align="start" trigger={<button className="shrink-0 rounded px-1 text-accent hover:underline">全部快捷键</button>}>
              <div className="space-y-1.5 text-[12.5px] text-muted">
                {[
                  ['↑ ↓', '切换句子'], ['空格', '播放 / 暂停当前句'], ['M', '把正在听的句子标为待返工'],
                  ...(bk && can(bk, 'script') ? [
                    ['1–9', '换成第 N 个说话人（可多选）'], ['Enter / 双击', '编辑文字'], [`${MOD} + Enter`, '在下方添加一句'],
                    [`${ALT} + ↑ ↓`, '上移 / 下移（也可拖动左侧把手）'], ['Shift + 点击', '多选'], ['Delete', '删除选中的句子'], [`${MOD} + Z`, '撤销'], ['右键', '更多操作'],
                  ] : []),
                  ['Esc', '取消选择'],
                ].map(([k, l]) => <div key={k} className="flex items-center justify-between gap-3"><span>{l}</span><span className="shrink-0 whitespace-nowrap"><Kbd>{k}</Kbd></span></div>)}
              </div>
            </Popover>
            {chapter?.durationMs > 0 && <span className="ml-auto pl-4">成品 {duration(chapter.durationMs)}</span>}
          </div>
        </div>

        <motion.aside initial={false} animate={{ width: side ? 400 : 0 }} transition={{ type: 'spring', stiffness: 400, damping: 40 }} className="hidden shrink-0 overflow-hidden border-l border-line bg-surface md:block">
          <div className="flex h-full w-[400px] flex-col">
            <div className="flex shrink-0 gap-0.5 border-b border-line px-2 pt-2">
              {panels.map((p) => (
                <Tip key={p.key} label={p.label}>
                  <button onClick={() => setPanel(p.key)} className={cn('relative flex h-9 flex-1 items-center justify-center gap-1.5 rounded-t-md text-[12.5px] transition-colors', panel === p.key ? 'text-ink' : 'text-muted hover:text-ink')}>
                    <p.icon className="size-4" />
                    <span className="hidden xl:inline">{p.label}</span>
                    {panel === p.key && <motion.span layoutId="ws-panel" className="absolute inset-x-1 -bottom-px h-0.5 rounded-full bg-ink" />}
                  </button>
                </Tip>
              ))}
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto">
              {bk && (
                <Fade k={panel} className="h-full">
                  {panel === 'segment' && <Inspector book={bk} chapterId={cid} segs={segs} chars={chars} />}
                  {panel === 'source' && <SourcePanel book={bk} chapterId={cid} />}
                  {panel === 'mix' && <MixPanel book={bk} chapterId={cid} segs={segs} />}
                  {panel === 'render' && <RenderPanel book={bk} chapterId={cid} onRender={render} />}
                  {panel === 'review' && <ReviewPanel book={bk} chapterId={cid} segs={segs} />}
                  {panel === 'comments' && <CommentsPanel book={bk} chapterId={cid} segs={segs} />}
                </Fade>
              )}
            </div>
          </div>
        </motion.aside>
      </div>
      {bk && <GenerateDialog open={gen} onOpenChange={setGen} book={bk} chapterIds={[cid]} scope="本章" segmentIds={selected.length > 1 ? selected : undefined} />}
      <MiniPlayerBar />
      {running.filter((j) => j !== parsing).length > 0 && (
        <div className="fixed bottom-14 right-4 z-30 w-72 rounded-panel border border-line bg-surface p-3 shadow-[var(--shadow-pop)] md:right-[416px]">
          {running.filter((j) => j !== parsing).map((j) => (
            <div key={j.id} className="mb-2 last:mb-0">
              <div className="flex justify-between text-xs"><span className="truncate">{j.title}</span><span className="text-faint tnum">{Math.round(j.progress * 100)}%</span></div>
              <Progress value={j.progress} tone="signal" className="mt-1" height={3} />
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

/** "保存中… / 已保存 / 保存失败" next to the toolbar, so edits made on a slow network never look lost. */
function SaveStatus() {
  const { pending, savedAt, failed } = useSave()
  const [recent, setRecent] = useState(false)
  useEffect(() => {
    if (!savedAt) return
    setRecent(true)
    const t = setTimeout(() => setRecent(false), 2500)
    return () => clearTimeout(t)
  }, [savedAt])
  if (pending > 0) return <span className="flex items-center gap-1.5 text-[12px] text-muted"><Spinner className="size-3.5" />保存中…</span>
  if (failed) return <Tip label={failed}><span className="flex items-center gap-1 text-[12px] text-danger"><AlertTriangle className="size-3.5" />保存失败</span></Tip>
  if (recent) return <span className="flex items-center gap-1 text-[12px] text-faint"><Check className="size-3.5 text-ok" />已保存</span>
  return null
}

function UndoButton({ chapterId }: { chapterId: string }) {
  const { count, label, chapterId: at } = useUndo()
  const n = at === chapterId ? count : 0
  return <IconButton label={n ? `撤销「${label}」（${MOD}+Z）` : '没有可以撤销的操作'} size="sm" disabled={!n} icon={<RotateCcw className="size-4" />} onClick={() => undoLast(chapterId)} />
}
