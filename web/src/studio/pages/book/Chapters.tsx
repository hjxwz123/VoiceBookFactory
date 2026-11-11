import { useQuery } from '@tanstack/react-query'
import { motion } from 'framer-motion'
import { AudioLines, ChevronDown, ChevronUp, FileText, FileUp, Layers, MoreHorizontal, Pencil, Plus, Sparkles, Trash2 } from '@/components/icons'
import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { duration, num } from '@/lib/format'
import { queryClient, useRunningJobs } from '@/lib/live'
import { DragGhost, DragHandle, useDragReorder } from '@/components/reorder'
import { PlayButton } from '@/components/audio'
import { Badge, Button, Checkbox, cn, confirm, Dialog, Empty, Field, IconButton, Input, Menu, Panel, Progress, Segmented, Skeleton, Switch, Term, Textarea } from '@/components/ui'
import { can, invalidate, useChapters, useCharacters } from '../../hooks'
import { chapterRefs, chaptersFromPreview, ChapterReview, ImportCh, ManuscriptInput } from '../importer'

const stageTone: Record<string, any> = { empty: 'neutral', script: 'neutral', voicing: 'accent', voiced: 'accent', rendered: 'ok', done: 'ok', failed: 'danger', stale: 'warn' }

export default function Chapters({ book }: { book: any }) {
  const { data: chapters, isLoading } = useChapters(book.id)
  const nav = useNavigate()
  const [sel, setSel] = useState<string[]>([])
  const [gen, setGen] = useState(false)
  const [add, setAdd] = useState(false)
  const [params, setParams] = useSearchParams()
  const [imp, setImp] = useState(params.get('import') === '1')
  const [genMode, setGenMode] = useState<string | undefined>(params.get('generate') ?? undefined)
  useEffect(() => {
    const g = params.get('generate')
    if (g) {
      setGenMode(g)
      setGen(true)
      setParams({}, { replace: true })
    }
  }, [params])
  const closeImport = (v: boolean) => {
    setImp(v)
    if (!v && params.get('import')) setParams({}, { replace: true })
  }
  const [busy, setBusy] = useState('')
  const running = useRunningJobs((j) => j.bookId === book.id)
  const runningFor = (cid: string) => running.find((j) => j.chapterId === cid)
  const list = chapters ?? []
  const ids = sel.length ? sel : list.filter((c) => c.included).map((c) => c.id)
  const scope = sel.length ? `选中的 ${sel.length} 章` : '全部章节'

  const parse = async () => {
    const targets = list.filter((c) => ids.includes(c.id))
    const existing = targets.filter((c) => c.currentScriptId)
    if (existing.length && !(await confirm({ title: '重新生成画本？', description: `${existing.length} 章已有画本。重新生成会保留旧版本，可以在制作台里随时切换回去；已生成的配音会按文本匹配尽量保留。`, confirmText: '重新生成' }))) return
    setBusy('parse')
    try {
      const jobs = await api.post(`/books/${book.id}/scripts/parse`, { chapterIds: ids, mode: 'ai' })
      toast.success(`已开始生成 ${jobs.length} 章画本`)
    } finally {
      setBusy('')
    }
  }
  const render = async () => {
    setBusy('render')
    let ok = 0
    for (const id of ids) {
      try {
        await api.post(`/chapters/${id}/render`, { preset: book.settings?.loudnessPreset })
        ok++
      } catch (e: any) {
        const ch = list.find((c) => c.id === id)
        toast.error(`${ch?.title}：${e.message}`)
      }
    }
    setBusy('')
    if (ok) toast.success(`已开始合成 ${ok} 章`)
  }
  // reordering shows at once; saves go out one after another so quick repeated moves all land in order
  const saving = useRef<Promise<unknown>>(Promise.resolve())
  const pendingSaves = useRef(0)
  const reorder = (next: string[]) => {
    const key = ['chapters', book.id]
    queryClient.setQueryData<any[]>(key, (old) => {
      if (!old) return old
      const byId = new Map(old.map((c) => [c.id, c]))
      return next.map((id, i) => ({ ...byId.get(id), index: i + 1 }))
    })
    pendingSaves.current++
    saving.current = saving.current
      .then(() => api.post(`/books/${book.id}/chapters/reorder`, { ids: next }))
      .catch((e: any) => toast.error(e?.message ?? '保存顺序失败', { description: '已恢复为服务器上的顺序。' }))
      .finally(() => {
        if (--pendingSaves.current === 0) invalidate(key)
      })
  }
  const move = (i: number, d: number) => {
    const order = list.map((c) => c.id)
    const [x] = order.splice(i, 1)
    order.splice(i + d, 0, x)
    reorder(order)
  }
  const dnd = useDragReorder(list.map((c) => c.id), reorder)
  const canOrder = can(book, 'script')
  const merge = async () => {
    if (!(await confirm({ title: `合并 ${sel.length} 章？`, description: '正文会按顺序拼接为一章，画本需要重新生成。' }))) return
    await api.post(`/books/${book.id}/chapters/merge`, { ids: sel })
    toast.success(`已把 ${sel.length} 章合并成一章`)
    setSel([])
    invalidate(['chapters', book.id], ['book', book.id])
  }

  if (isLoading) return <div className="space-y-2">{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-14" />)}</div>
  return (
    <div>
      <div className="mb-4 flex flex-wrap items-center gap-2">
        {can(book, 'script') && <Button icon={<FileText className="size-4" />} loading={busy === 'parse'} onClick={parse}>生成画本</Button>}
        {can(book, 'generate') && <Button variant="primary" icon={<AudioLines className="size-4" />} onClick={() => setGen(true)}>生成配音</Button>}
        {can(book, 'post') && <Button icon={<Layers className="size-4" />} loading={busy === 'render'} onClick={render}>合成</Button>}
        <span className="text-[12.5px] text-muted">作用于{scope}</span>
        <div className="ml-auto flex items-center gap-2">
          {sel.length >= 2 && can(book, 'manage') && <Button size="sm" onClick={merge}>合并章节</Button>}
          {can(book, 'script') && <Button size="sm" icon={<FileUp className="size-4" />} onClick={() => setImp(true)}>导入书稿</Button>}
          {can(book, 'manage') && <Button size="sm" icon={<Plus className="size-4" />} onClick={() => setAdd(true)}>追加章节</Button>}
        </div>
      </div>
      {!list.length ? (
        <Panel padded={false}>
          <Empty
            icon={<FileText />}
            title="这本书还没有章节"
            description="导入整本书稿会自动识别章节；也可以一章一章手动添加正文。"
            action={<div className="flex gap-2"><Button variant="primary" icon={<FileUp className="size-4" />} onClick={() => setImp(true)}>导入书稿</Button><Button icon={<Plus className="size-4" />} onClick={() => setAdd(true)}>手动添加一章</Button></div>}
          />
        </Panel>
      ) : (
        <Panel padded={false}>
          <div className="flex items-center gap-3 border-b border-line px-4 py-2 text-xs text-muted">
            {canOrder && <span className="-ml-2 w-5" />}
            <Checkbox checked={sel.length === list.length ? true : sel.length ? 'indeterminate' : false} onChange={(v) => setSel(v ? list.map((c) => c.id) : [])} />
            <span className="w-8">序号</span>
            <span className="flex-1">章节</span>
            <span className="hidden w-44 sm:block">配音进度</span>
            <span className="hidden w-20 text-right md:block">时长</span>
            <span className="w-32 text-right">状态</span>
            <span className="w-[76px]" />
          </div>
          <ul>
            {list.map((c, i) => {
              const job = runningFor(c.id)
              const st = c.state ?? {}
              return (
                <motion.li layout={!dnd.drag} key={c.id} ref={dnd.rowRef(c.id)} className={cn('group flex items-center gap-3 border-t border-line px-4 py-3 first:border-t-0 hover:bg-sunken/40', !c.included && 'opacity-50', dnd.rowClass(c.id, i))}>
                  {canOrder && <DragHandle className="-ml-2" onPointerDown={(e) => dnd.start(e, c.id)} />}
                  <Checkbox checked={sel.includes(c.id)} onChange={(v) => setSel(v ? [...sel, c.id] : sel.filter((x) => x !== c.id))} />
                  <span className="w-8 text-xs text-faint tnum">{c.index}</span>
                  <button className="min-w-0 flex-1 text-left" onClick={() => nav(`/books/${book.id}/chapters/${c.id}`)}>
                    <div className="truncate text-[13.5px] font-medium hover:text-accent">{c.title}</div>
                    <div className="mt-0.5 text-xs text-faint">{num(c.wordCount)} 字{c.scriptStale && ' · 正文已修改，画本待同步'}{c.renderStale && c.renderId && ' · 有改动未重新合成'}</div>
                  </button>
                  <div className="hidden w-44 sm:block">
                    {job ? (
                      <div>
                        <div className="truncate text-[11.5px] text-warn">{job.progressText || job.title}</div>
                        <Progress value={job.progress} tone="signal" className="mt-1" height={4} />
                      </div>
                    ) : st.segments ? (
                      <div>
                        <div className="text-[11.5px] text-muted tnum">{st.ready}/{st.segments} 句{st.failed > 0 && <span className="text-danger"> · {st.failed} 失败</span>}</div>
                        <Progress value={st.audioPercent} className="mt-1" height={4} tone={st.failed ? 'danger' : 'accent'} />
                      </div>
                    ) : (
                      <span className="text-[11.5px] text-faint">还没有画本</span>
                    )}
                  </div>
                  <span className="hidden w-20 text-right text-xs text-muted tnum md:block">{c.durationMs ? duration(c.durationMs) : c.wordCount ? <span className="text-faint" title={`按每分钟 ${book.settings?.speechRate || 220} 字估算`}>约 {duration((c.wordCount / (book.settings?.speechRate || 220)) * 60000)}</span> : '—'}</span>
                  <span className="flex w-32 items-center justify-end gap-2">
                    {c.reviewStatus === 'passed' && <Badge tone="ok">已审</Badge>}
                    {job ? <Badge tone="signal" dot>处理中</Badge> : <Badge tone={stageTone[st.stage] ?? 'neutral'}>{st.label ?? '—'}</Badge>}
                  </span>
                  <span className="flex w-[76px] items-center justify-end gap-1">
                    <PlayButton id={'render:' + c.id} url={c.renderUrl} title={c.title} size="xs" label="试听成品" />
                    <ChapterMenu book={book} c={c} i={i} last={i === list.length - 1} move={move} />
                  </span>
                </motion.li>
              )
            })}
          </ul>
          <DragGhost drag={dnd.drag} label={list.find((c) => c.id === dnd.drag?.id)?.title ?? ''} />
        </Panel>
      )}
      <GenerateDialog open={gen} onOpenChange={(v) => { setGen(v); if (!v) setGenMode(undefined) }} book={book} chapterIds={ids} scope={scope} defaultMode={genMode} />
      <AddChapter open={add} onOpenChange={setAdd} book={book} count={list.length} />
      <ImportDialog open={imp} onOpenChange={closeImport} book={book} existing={list.length} />
    </div>
  )
}

function ChapterMenu({ book, c, i, last, move }: { book: any; c: any; i: number; last: boolean; move: (i: number, d: number) => void }) {
  const [rename, setRename] = useState(false)
  const [title, setTitle] = useState(c.title)
  const nav = useNavigate()
  const manage = can(book, 'manage')
  const order = can(book, 'script')
  return (
    <>
      <Menu
        trigger={<IconButton label="更多" size="xs" icon={<MoreHorizontal className="size-4" />} />}
        items={[
          { label: '打开制作台', icon: <Sparkles />, onSelect: () => nav(`/books/${book.id}/chapters/${c.id}`) },
          manage && { label: '重命名', icon: <Pencil />, onSelect: () => setRename(true) },
          order && i > 0 && { label: '上移', icon: <ChevronUp />, onSelect: () => move(i, -1) },
          order && !last && { label: '下移', icon: <ChevronDown />, onSelect: () => move(i, 1) },
          manage && { label: c.included ? '不纳入制作' : '纳入制作', onSelect: async () => { await api.patch(`/chapters/${c.id}`, { included: !c.included }); invalidate(['chapters', book.id]); toast.success(c.included ? `「${c.title}」不再纳入制作，批量生成和合成会跳过它` : `「${c.title}」已纳入制作`) } },
          manage && 'sep',
          manage && {
            label: '删除章节',
            icon: <Trash2 />,
            danger: true,
            onSelect: async () => {
              if (await confirm({ title: `删除「${c.title}」？`, description: '这一章的画本、配音和成品都会被删除。', danger: true, confirmText: '删除' })) {
                await api.del(`/chapters/${c.id}`)
                invalidate(['chapters', book.id], ['book', book.id])
                toast.success(`已删除「${c.title}」`)
              }
            },
          },
        ]}
      />
      <Dialog open={rename} onOpenChange={setRename} title="重命名章节" width={420} footer={<><Button onClick={() => setRename(false)}>取消</Button><Button variant="primary" disabled={!title.trim() || title === c.title} onClick={async () => { await api.patch(`/chapters/${c.id}`, { title: title.trim() }); invalidate(['chapters', book.id]); setRename(false); toast.success('已重命名') }}>保存</Button></>}>
        <Input autoFocus value={title} onChange={(e) => setTitle(e.target.value)} />
      </Dialog>
    </>
  )
}

export function GenerateDialog({ open, onOpenChange, book, chapterIds, scope, segmentIds, defaultMode }: { open: boolean; onOpenChange: (v: boolean) => void; book: any; chapterIds: string[]; scope: string; segmentIds?: string[]; defaultMode?: string }) {
  const initial = defaultMode ?? (segmentIds?.length ? 'segments' : 'missing')
  const [mode, setMode] = useState(initial)
  const [autoRender, setAutoRender] = useState(true)
  const [est, setEst] = useState<any>(null)
  const [busy, setBusy] = useState(false)
  const lexicon = useQuery<any[]>({ queryKey: ['lexicon', book.id], queryFn: () => api.get(`/books/${book.id}/lexicon`), enabled: open })
  const noLexicon = lexicon.data && !lexicon.data.some((e) => e.scope === 'book' && e.status === 'active')
  const req = (m: string) => ({ chapterIds, scope: m, segmentIds, regenerate: m === 'all' || m === 'segments', autoRender })
  const switched = useRef(false)
  useEffect(() => {
    if (open) {
      setMode(initial)
      switched.current = false
    }
  }, [open])
  useEffect(() => {
    if (!open) return
    setEst(null)
    let live = true
    api.post(`/books/${book.id}/generate/estimate`, req(mode)).then(async (r) => {
      // nothing new to voice but some lines were edited: open on "changed lines" instead of an empty estimate
      if (live && r.segments === 0 && mode === 'missing' && !switched.current) {
        switched.current = true // only once per opening, so picking "missing" by hand sticks
        const st = await api.post(`/books/${book.id}/generate/estimate`, req('stale')).catch(() => null)
        if (live && st?.segments > 0) return setMode('stale')
      }
      if (live) setEst(r)
    }).catch(() => {})
    return () => { live = false }
  }, [open, mode, chapterIds.join()])
  const go = async () => {
    setBusy(true)
    try {
      await api.post(`/books/${book.id}/generate`, req(mode))
      toast.success('已开始配音，可以在章节列表查看进度')
      onOpenChange(false)
    } finally {
      setBusy(false)
    }
  }
  const short = est && est.available < est.credits
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title="生成配音"
      description={`为${scope}的句子按角色声音生成音频。`}
      footer={<><Button onClick={() => onOpenChange(false)}>取消</Button><Button variant="primary" loading={busy} disabled={!est || est.segments === 0 || short} onClick={go}>开始生成</Button></>}
    >
      <div className="space-y-5">
        <Field label="生成范围">
          <Segmented
            value={mode}
            onChange={setMode}
            items={[
              ...(segmentIds?.length ? [{ value: 'segments', label: `选中的 ${segmentIds.length} 句` }] : []),
              { value: 'missing', label: '只生成还没有音频的' },
              { value: 'stale', label: '有改动的' },
              { value: 'failed', label: '失败的' },
              { value: 'all', label: '全部重新生成' },
            ]}
            size="sm"
          />
        </Field>
        <div className="flex items-center justify-between rounded-[10px] bg-sunken/60 px-4 py-3">
          <div>
            <div className="flex items-center gap-1 text-[13.5px] font-medium">完成后自动合成 <Term id="合成" /></div>
            <div className="text-xs text-muted">每章配完立即拼接成完整音频，方便试听。</div>
          </div>
          <Switch checked={autoRender} onChange={setAutoRender} />
        </div>
        <div className="grid grid-cols-3 gap-4 rounded-[10px] border border-line px-4 py-3">
          <div><div className="text-xs text-muted">句子</div><div className="mt-1 text-[18px] font-semibold tnum">{est ? num(est.segments) : '…'}</div></div>
          <div><div className="text-xs text-muted">预计时长</div><div className="mt-1 text-[18px] font-semibold tnum">{est ? duration(est.durationMs) : '…'}</div></div>
          <div><div className="flex items-center gap-1 text-xs text-muted">预计积分 <Term id="积分" /></div><div className={cn('mt-1 text-[18px] font-semibold tnum', short && 'text-danger')}>{est ? num(est.credits) : '…'}</div></div>
        </div>
        {noLexicon && mode === 'missing' && (
          <p className="rounded-[10px] border border-line px-4 py-3 text-[12.5px] leading-relaxed text-muted">
            还没有设置这本书的读音。先在 <Link to={`/books/${book.id}/lexicon`} onClick={() => onOpenChange(false)} className="font-medium text-accent">读音</Link> 里让 AI 挑出人名、多音字，定好再配音，可以少返工。
          </p>
        )}
        {short && <p className="text-[13px] text-danger">积分不足：可用 {num(est.available)}，请先充值或缩小范围。</p>}
        {est?.segments === 0 && <p className="text-[13px] text-muted">这个范围内没有需要生成的句子。</p>}
      </div>
    </Dialog>
  )
}

function AddChapter({ open, onOpenChange, book, count }: { open: boolean; onOpenChange: (v: boolean) => void; book: any; count: number }) {
  const [title, setTitle] = useState('')
  const [text, setText] = useState('')
  const [split, setSplit] = useState(false)
  const [busy, setBusy] = useState(false)
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title="追加章节"
      width={640}
      footer={<><Button onClick={() => onOpenChange(false)}>取消</Button><Button variant="primary" loading={busy} disabled={!text.trim()} onClick={async () => {
        setBusy(true)
        try {
          await api.post(`/books/${book.id}/chapters`, { title, text, afterIndex: count, split })
          invalidate(['chapters', book.id], ['book', book.id])
          toast.success('章节已添加')
          setTitle('')
          setText('')
          onOpenChange(false)
        } finally {
          setBusy(false)
        }
      }}>添加</Button></>}
    >
      <div className="space-y-4">
        <Field label="章节标题"><Input value={title} onChange={(e) => setTitle(e.target.value)} placeholder={`第 ${count + 1} 章`} /></Field>
        <Field label="正文"><Textarea rows={10} value={text} onChange={(e) => setText(e.target.value)} /></Field>
        <Switch checked={split} onChange={setSplit} label="正文里包含多个章节标题时自动拆分" />
      </div>
    </Dialog>
  )
}

function ImportDialog({ open, onOpenChange, book, existing }: { open: boolean; onOpenChange: (v: boolean) => void; book: any; existing: number }) {
  const characters = useCharacters(book.id)
  const [preview, setPreview] = useState<any>(null)
  const [chs, setChs] = useState<ImportCh[]>([])
  const [analyze, setAnalyze] = useState(true)
  const [busy, setBusy] = useState(false)
  const noCast = (characters.data ?? []).filter((c) => c.kind !== 'narrator').length === 0
  useEffect(() => {
    if (!open) {
      setPreview(null)
      setChs([])
    }
  }, [open])
  const load = (p: any) => {
    setPreview(p)
    setChs(chaptersFromPreview(p))
  }
  const included = chs.filter((c) => c.included)
  const go = async () => {
    setBusy(true)
    try {
      const r = await api.post(`/books/${book.id}/chapters/import`, { importId: preview.id, chapters: chapterRefs(chs), autoAnalyze: analyze && noCast })
      invalidate(['chapters', book.id], ['book', book.id], ['characters', book.id])
      toast.success(`已导入 ${r.added} 章` + (analyze && noCast ? '，AI 正在分析角色' : ''))
      onOpenChange(false)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title="导入书稿"
      description={existing ? `新章节会排在现有 ${existing} 章之后。` : '上传或粘贴正文，系统会自动识别章节。'}
      width={preview ? 880 : 640}
      footer={preview && (
        <>
          <Button variant="ghost" className="mr-auto" onClick={() => setPreview(null)}>重新选择书稿</Button>
          {noCast && <Switch checked={analyze} onChange={setAnalyze} label="导入后分析角色" />}
          <Button variant="primary" loading={busy} disabled={!included.length} onClick={go}>导入 {included.length} 章（{num(included.reduce((s, c) => s + c.wordCount, 0))} 字）</Button>
        </>
      )}
    >
      {preview ? <ChapterReview preview={preview} chs={chs} setChs={setChs} onPreview={load} maxHeight="42vh" /> : <ManuscriptInput onLoaded={load} title={book.title} compact />}
    </Dialog>
  )
}
