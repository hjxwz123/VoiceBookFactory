import { useQuery } from '@tanstack/react-query'
import { AudioLines, CheckCircle2, Download, EyeOff, Flag, Music, Plus, RefreshCw, RotateCcw, Trash2, XCircle } from '@/components/icons'
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { toast } from 'sonner'
import { api, downloadAuthed } from '@/lib/api'
import { duration, fromNow, timecode } from '@/lib/format'
import { usePlayer } from '@/lib/player'
import { AssetPicker } from '@/components/asset'
import { PlayButton, Waveform } from '@/components/audio'
import { Avatar, Badge, Button, cn, confirm, Dialog, Empty, Field, IconButton, Kbd, Slider, Spinner, Switch, Term, Textarea } from '@/components/ui'
import { isFixed, kindLabel, TemplatePreviewButton, useTemplates } from '@/components/templates'
import { can, invalidate } from '../../hooks'
import { refetchScript, useWS } from './store'

const Pad = ({ children, className }: { children: React.ReactNode; className?: string }) => <div className={cn('px-5 py-4', className)}>{children}</div>

// ---------------- Source text ----------------
export function SourcePanel({ book, chapterId }: { book: any; chapterId: string }) {
  const { data } = useQuery({ queryKey: ['chapter', chapterId], queryFn: () => api.get(`/chapters/${chapterId}`) })
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => { if (data) setText(data.text ?? '') }, [data?.text])
  const editable = can(book, 'script')
  if (!data) return <Pad><Spinner /></Pad>
  return (
    <Pad className="flex h-full flex-col">
      <p className="mb-3 text-xs text-muted">这是这一章的原文。修改原文后点击保存，画本里对应的段落会提示同步，已配好的句子会尽量保留。</p>
      <Textarea value={text} disabled={!editable} onChange={(e) => setText(e.target.value)} className="min-h-0 flex-1 !resize-none text-[14px] leading-[1.9]" />
      {editable && (
        <div className="mt-3 flex items-center justify-between">
          <span className="text-xs text-faint tnum">{Array.from(text).length} 字</span>
          <Button size="sm" variant="primary" disabled={text === data.text} loading={busy} onClick={async () => {
            setBusy(true)
            try {
              const r = await api.put(`/chapters/${chapterId}/text`, { text })
              invalidate(['chapter', chapterId], ['chapters', book.id])
              refetchScript(chapterId)
              toast.success(r.changedParagraphs ? `已保存，${r.changedParagraphs} 段有变化` : '已保存')
              if (r.scriptStale && (await confirm({ title: '现在同步画本吗？', description: '只会重新分析有变化的段落，其他句子和音频保持不变。', confirmText: '同步' }))) {
                await api.post(`/chapters/${chapterId}/sync`)
                toast.success('正在同步画本')
              }
            } finally {
              setBusy(false)
            }
          }}>保存原文</Button>
        </div>
      )}
    </Pad>
  )
}

// ---------------- Mix (BGM / SFX tracks) ----------------
const uid = () => Math.random().toString(36).slice(2, 10)

/** Per-chapter intro/outro toggle, naming the template chosen in the book settings. */
function BookendSwitch({ book, kind, checked, disabled, onChange }: { book: any; kind: 'intro' | 'outro'; checked: boolean; disabled?: boolean; onChange: (v: boolean) => void }) {
  const { data } = useTemplates(book.studioId)
  const id = kind === 'intro' ? book.settings?.introTemplateId : book.settings?.outroTemplateId
  const t = (data ?? []).find((v) => v.template.id === id)
  const label = kindLabel(kind)
  if (!id || (data && !t)) {
    return (
      <div className="flex items-center justify-between gap-3 rounded-[9px] bg-sunken/50 px-3 py-2.5 text-[12.5px] text-muted">
        <span>这本书还没有选{label}</span>
        <Link to={`/books/${book.id}/settings`} className="shrink-0 text-accent hover:underline">去图书设置选择</Link>
      </div>
    )
  }
  return (
    <div className="flex items-center gap-2">
      <div className="min-w-0 flex-1">
        <Switch checked={checked} disabled={disabled} onChange={onChange} label={<span className="flex min-w-0 items-center gap-1.5">本章加{label}<span className="truncate text-muted">· {t?.template.name ?? '…'}{t && isFixed(t.template) ? '（固定音频）' : ''}</span></span>} />
      </div>
      <TemplatePreviewButton studioId={book.studioId} id={id} bookId={book.id} size="sm" />
    </div>
  )
}

export function MixPanel({ book, chapterId, segs }: { book: any; chapterId: string; segs: any[] }) {
  const { data } = useQuery({ queryKey: ['timeline', chapterId], queryFn: () => api.get(`/chapters/${chapterId}/timeline`) })
  const [tracks, setTracks] = useState<any[]>([])
  const [ducking, setDucking] = useState(-12)
  const [intro, setIntro] = useState(false)
  const [outro, setOutro] = useState(false)
  const [pick, setPick] = useState<{ track: string; kind: string[] } | null>(null)
  const [dirty, setDirty] = useState(false)
  const [busy, setBusy] = useState(false)
  const focus = useWS((s) => s.focus)
  const editable = can(book, 'post')
  useEffect(() => {
    if (!data || dirty) return
    setTracks((data.tracks ?? []).map(({ resolved, ...t }: any) => ({ ...t, clips: t.clips ?? [], effects: t.effects ?? [] })))
    setDucking(data.duckingDb ?? -12)
    setIntro(!!data.intro)
    setOutro(!!data.outro)
  }, [data])
  const upd = (fn: (t: any[]) => any[]) => {
    setTracks(fn)
    setDirty(true)
  }
  const save = async () => {
    setBusy(true)
    try {
      await api.put(`/chapters/${chapterId}/mix`, { tracks, duckingDb: ducking, intro, outro, version: data?.mixVersion ?? 0 })
      setDirty(false)
      invalidate(['timeline', chapterId], ['chapters', book.id])
      toast.success('已保存，重新合成后生效')
    } finally {
      setBusy(false)
    }
  }
  const segText = (id: string) => segs.find((s) => s.id === id)?.text?.slice(0, 16)
  if (!data) return <Pad><Spinner /></Pad>
  const ensureSfx = () => (tracks.find((t) => t.kind === 'sfx') ? tracks : [...tracks, { id: 'sfx', kind: 'sfx', name: '音效', volume: 0, muted: false, duck: false, effects: [], clips: [] }])

  return (
    <div>
      <Pad>
        <p className="text-xs text-muted">背景音乐会铺满整章并在有人说话时自动压低；音效挂在某一句上，跟着那一句走，改稿后位置也不会错。</p>
        <div className="mt-4 space-y-3">
          <BookendSwitch book={book} kind="intro" checked={intro} disabled={!editable} onChange={(v) => { setIntro(v); setDirty(true) }} />
          <BookendSwitch book={book} kind="outro" checked={outro} disabled={!editable} onChange={(v) => { setOutro(v); setDirty(true) }} />
        </div>
        <div className="mt-4 flex items-center gap-3">
          <span className="w-20 shrink-0 whitespace-nowrap text-xs text-muted">说话时压低</span>
          <Slider value={ducking} min={-24} max={0} step={1} onChange={(v) => { setDucking(v); setDirty(true) }} />
          <span className="w-14 shrink-0 whitespace-nowrap text-right text-xs tnum">{ducking} dB</span>
        </div>
      </Pad>
      {tracks.map((t) => (
        <div key={t.id} className="border-t border-line px-5 py-4">
          <div className="flex items-center gap-2">
            <Music className="size-4 text-muted" />
            <span className="text-[13px] font-medium">{t.name}</span>
            <div className="ml-auto flex items-center gap-2">
              <Switch checked={!t.muted} onChange={(v) => upd((ts) => ts.map((x) => (x.id === t.id ? { ...x, muted: !v } : x)))} />
            </div>
          </div>
          <div className="mt-3 flex items-center gap-3">
            <span className="w-20 text-xs text-muted">轨道音量</span>
            <Slider value={t.volume} min={-30} max={6} step={1} onChange={(v) => upd((ts) => ts.map((x) => (x.id === t.id ? { ...x, volume: v } : x)))} />
            <span className="w-12 text-right text-xs tnum">{t.volume} dB</span>
          </div>
          <div className="mt-3 space-y-1.5">
            {t.clips.map((c: any) => (
              <div key={c.id} className="flex items-center gap-2 rounded-[9px] border border-line px-2.5 py-2">
                <div className="min-w-0 flex-1">
                  <div className="truncate text-[12.5px] font-medium">{c.name}</div>
                  <div className="truncate text-[11px] text-faint">{c.fill ? '铺满整章，循环播放' : c.anchorSegmentId ? `跟随「${segText(c.anchorSegmentId) ?? '已删除的句子'}」` : `从 ${timecode(c.startMs)} 开始`}</div>
                </div>
                <input type="number" value={c.volume} onChange={(e) => upd((ts) => ts.map((x) => (x.id === t.id ? { ...x, clips: x.clips.map((y: any) => (y.id === c.id ? { ...y, volume: Number(e.target.value) } : y)) } : x)))} className="h-7 w-14 rounded-md border border-line bg-surface px-1.5 text-xs tnum" title="音量 dB" />
                <IconButton label="移除" size="xs" icon={<Trash2 className="size-3.5" />} onClick={() => upd((ts) => ts.map((x) => (x.id === t.id ? { ...x, clips: x.clips.filter((y: any) => y.id !== c.id) } : x)))} />
              </div>
            ))}
          </div>
          {editable && (
            <Button size="xs" className="mt-2" icon={<Plus className="size-3.5" />} onClick={() => setPick({ track: t.id, kind: t.kind === 'bgm' ? ['bgm', 'ambience'] : ['sfx', 'ambience'] })}>
              {t.kind === 'bgm' ? '添加背景音乐' : focus ? '在当前句添加音效' : '添加音效'}
            </Button>
          )}
        </div>
      ))}
      {editable && !tracks.find((t) => t.kind === 'sfx') && (
        <Pad className="border-t border-line"><Button size="xs" variant="ghost" icon={<Plus className="size-3.5" />} onClick={() => upd(() => ensureSfx())}>添加音效轨</Button></Pad>
      )}
      {editable && (
        <div className="sticky bottom-0 flex items-center justify-between border-t border-line bg-surface px-5 py-3">
          <span className="text-xs text-faint">{dirty ? '有未保存的改动' : '已保存'}</span>
          <Button size="sm" variant="primary" disabled={!dirty} loading={busy} onClick={save}>保存混音</Button>
        </div>
      )}
      <AssetPicker
        open={!!pick}
        onOpenChange={(v) => !v && setPick(null)}
        kinds={pick?.kind}
        title={pick?.track === 'bgm' ? '选择背景音乐' : '选择音效'}
        onPick={(a) => {
          const isBgm = tracks.find((t) => t.id === pick!.track)?.kind === 'bgm'
          const clip = { id: uid(), assetId: a.id, name: a.name, startMs: 0, offsetMs: 0, durationMs: a.durationMs, fill: isBgm, loop: isBgm, volume: 0, fadeInMs: isBgm ? 1500 : 0, fadeOutMs: isBgm ? 2500 : 0, effects: [], anchorSegmentId: isBgm ? undefined : focus || undefined }
          upd((ts) => ts.map((x) => (x.id === pick!.track ? { ...x, clips: [...x.clips, clip] } : x)))
          setPick(null)
        }}
      />
    </div>
  )
}

// ---------------- Renders ----------------
export function RenderPanel({ book, chapterId, onRender }: { book: any; chapterId: string; onRender: () => void }) {
  const { data } = useQuery<any[]>({ queryKey: ['renders', chapterId], queryFn: () => api.get(`/chapters/${chapterId}/renders`) })
  const cur = (data ?? []).find((r) => r.current) ?? data?.[0]
  const peaks = useQuery({ queryKey: ['peaks', cur?.peaksAssetId], queryFn: () => api.get(`/assets/${cur.peaksAssetId}/peaks`), enabled: !!cur?.peaksAssetId })
  const { id, time, duration: d, seek } = usePlayer()
  const playing = id === 'render:' + cur?.id
  if (!data) return <Pad><Spinner /></Pad>
  if (!data.length)
    return <Empty title="还没有合成过" description="合成会把所有句子按停顿规则拼接起来，叠加背景音乐和音效，并把整体响度调整到平台标准。" action={can(book, 'post') && <Button variant="primary" onClick={onRender}>开始合成</Button>} className="h-full" />
  return (
    <div>
      {cur && (
        <Pad>
          <div className="flex items-center gap-3">
            <PlayButton id={'render:' + cur.id} url={cur.status === 'ready' ? cur.url : null} title="章节成品" size="md" />
            <div className="min-w-0 flex-1">
              <div className="text-[13.5px] font-semibold">当前成品</div>
              <div className="text-xs text-muted">{duration(cur.durationMs)} · {cur.report?.presetName} · {fromNow(cur.createdAt)}</div>
            </div>
            {cur.status === 'ready' && <a href={cur.downloadUrl}><IconButton label="下载" icon={<Download className="size-4" />} /></a>}
          </div>
          {cur.status === 'rendering' ? (
            <p className="mt-3 flex items-center gap-2 text-xs text-warn"><Spinner className="text-warn" />正在合成…</p>
          ) : cur.status === 'failed' ? (
            <p className="mt-3 text-xs text-danger">{cur.error}</p>
          ) : (
            peaks.data && <Waveform className="mt-4" peaks={peaks.data.peaks} progress={playing && d ? time / d : 0} height={56} onSeek={(r) => (playing ? seek(r * d) : usePlayer.getState().play('render:' + cur.id, cur.url, '章节成品'))} />
          )}
          {cur.status === 'ready' && (
            <div className="mt-3 flex gap-2">
              <button onClick={() => downloadAuthed(`/chapters/${chapterId}/subtitles?format=srt`, 'subtitles.srt')} className="text-xs text-accent hover:underline">下载 SRT 字幕</button>
              <button onClick={() => downloadAuthed(`/chapters/${chapterId}/subtitles?format=lrc`, 'subtitles.lrc')} className="text-xs text-accent hover:underline">LRC</button>
            </div>
          )}
        </Pad>
      )}
      {cur?.report?.checks && (
        <div className="border-t border-line px-5 py-4">
          <div className="mb-3 flex items-center gap-2">
            <h4 className="text-xs font-medium uppercase tracking-wide text-faint">技术指标</h4>
            <Term id="响度" />
            <Badge tone={cur.report.passed ? 'ok' : 'warn'} className="ml-auto">{cur.report.passed ? '全部达标' : '有不达标项'}</Badge>
          </div>
          <ul className="space-y-2">
            {cur.report.checks.map((c: any) => (
              <li key={c.key} className="flex items-start gap-2 text-[12.5px]">
                {c.ok ? <CheckCircle2 className="mt-0.5 size-3.5 shrink-0 text-ok" /> : <XCircle className="mt-0.5 size-3.5 shrink-0 text-warn" />}
                <div className="min-w-0 flex-1">
                  <div className="flex justify-between gap-2"><span>{c.name}</span><span className="text-muted tnum">{c.value}</span></div>
                  {!c.ok && <div className="text-[11.5px] text-faint">标准 {c.target}。{c.tip}</div>}
                </div>
              </li>
            ))}
          </ul>
        </div>
      )}
      <div className="border-t border-line px-5 py-4">
        <div className="mb-2 flex items-center justify-between">
          <h4 className="text-xs font-medium uppercase tracking-wide text-faint">历史版本</h4>
          {can(book, 'post') && <Button size="xs" icon={<RefreshCw className="size-3.5" />} onClick={onRender}>重新合成</Button>}
        </div>
        <ul className="space-y-1.5">
          {data.map((r) => (
            <li key={r.id} className="flex items-center gap-2.5 text-[12.5px]">
              <PlayButton id={'render:' + r.id} url={r.status === 'ready' ? r.url : null} size="xs" title="历史版本" />
              <span className="flex-1">{fromNow(r.createdAt)} · {duration(r.durationMs)}</span>
              {r.current ? <Badge tone="accent">当前</Badge> : r.status === 'ready' && can(book, 'post') && (
                <Button size="xs" variant="ghost" icon={<RotateCcw className="size-3" />} onClick={async () => { await api.post(`/chapters/${chapterId}/renders/${r.id}/restore`); invalidate(['renders', chapterId], ['chapters', book.id]) }}>恢复</Button>
              )}
            </li>
          ))}
        </ul>
      </div>
    </div>
  )
}

// ---------------- Smart review ----------------
const findingName: Record<string, string> = { missing: '漏读', extra: '多读', wrong: '错读', pause: '停顿异常', volume: '音量异常', clipping: '爆音', noise: '底噪', silence: '静音过长', speed: '语速异常' }

export function ReviewPanel({ book, chapterId, segs }: { book: any; chapterId: string; segs: any[] }) {
  const { data } = useQuery<any[]>({ queryKey: ['reviews', chapterId], queryFn: () => api.get(`/chapters/${chapterId}/reviews`) })
  const [busy, setBusy] = useState(false)
  const [conclude, setConclude] = useState<'pass' | 'fail' | null>(null)
  const [reason, setReason] = useState('')
  const [hideIgnored, setHideIgnored] = useState(true)
  const run = data?.[0]
  const reviewer = can(book, 'review')
  const segText = (id: string) => segs.find((s) => s.id === id)?.text
  const start = async () => {
    setBusy(true)
    try {
      await api.post(`/chapters/${chapterId}/review`)
      invalidate(['reviews', chapterId])
      toast.success('智能审听已开始，会逐句比对原文和音频')
    } finally {
      setBusy(false)
    }
  }
  const act = async (fid: string, action: string) => {
    await api.post(`/reviews/${run.id}/findings/${fid}`, { action })
    invalidate(['reviews', chapterId], ['issues'])
  }
  const findings = (run?.findings ?? []).filter((f: any) => !hideIgnored || !f.ignored)
  // pickups: lines marked while listening, and lines already redone that wait for a final listen
  const focus = useWS((st) => st.focus)
  const flagged = segs.filter((x) => x.openIssues > 0)
  const needFix = flagged.filter((x) => (x.verifyingIssues ?? 0) < x.openIssues)
  const toVerify = flagged.filter((x) => (x.verifyingIssues ?? 0) >= x.openIssues)
  const lineNo = (id: string) => segs.findIndex((x) => x.id === id) + 1
  const play = (list: any[]) => {
    const q = list.filter((x) => x.take?.url).map((x) => ({ id: 'seg:' + x.id, url: x.take.url, title: x.text }))
    if (!q.length) return toast('这些句子还没有音频')
    const [first, ...rest] = q
    usePlayer.getState().play(first.id, first.url, first.title, rest, true)
  }
  const regenerate = async () => {
    await api.post(`/books/${book.id}/generate`, { chapterIds: [chapterId], scope: 'segments', segmentIds: needFix.map((x) => x.id), regenerate: true })
    toast.success(`正在重新生成 ${needFix.length} 句`, { description: '生成好后这些句子会变成「待复核」，复听没问题再关闭。' })
  }
  const closeVerified = async () => {
    const r = await api.get(`/books/${book.id}/issues?chapterId=${chapterId}&status=verifying&pageSize=200`)
    const items: any[] = r.items ?? []
    await Promise.all(items.map((it) => api.post(`/issues/${it.issue.id}/action`, { action: 'close' })))
    invalidate(['issues'])
    refetchScript(chapterId)
    toast.success(`已关闭 ${items.length} 个工单`)
  }
  return (
    <div>
      <Pad className="border-b border-line">
        <h4 className="text-[13px] font-medium">人工审听</h4>
        <p className="mt-1 text-xs leading-relaxed text-muted">对照左侧文字从头听一遍。听到读错、漏读、情绪不对，按 <Kbd>M</Kbd> 标记这一句，播放不会停；听完再统一返工。</p>
        <div className="mt-3 flex gap-2">
          <Button size="sm" icon={<AudioLines className="size-4" />} onClick={() => play(segs)}>从头开始审听</Button>
          {focus && <Button size="sm" variant="ghost" onClick={() => play(segs.slice(Math.max(0, lineNo(focus) - 1)))}>从选中句开始</Button>}
        </div>
        {flagged.length > 0 && (
          <div className="mt-4 rounded-[10px] border border-line">
            <div className="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2">
              <span className="text-[12.5px] font-medium">待返工 {needFix.length} 句{toVerify.length > 0 && <span className="font-normal text-muted"> · 待复核 {toVerify.length} 句</span>}</span>
              <div className="ml-auto flex gap-1">
                {needFix.length > 0 && can(book, 'generate') && <Button size="xs" variant="primary" icon={<RefreshCw className="size-3" />} onClick={regenerate}>重新生成 {needFix.length} 句</Button>}
                {toVerify.length > 0 && <Button size="xs" onClick={() => play(toVerify)}>复听</Button>}
                {toVerify.length > 0 && reviewer && <Button size="xs" onClick={closeVerified}>全部确认关闭</Button>}
              </div>
            </div>
            <ul className="max-h-56 overflow-y-auto">
              {flagged.map((x) => (
                <li key={x.id}>
                  <button onClick={() => useWS.getState().setFocus(x.id)} className="flex w-full items-start gap-2 px-3 py-1.5 text-left hover:bg-sunken/50">
                    <span className="w-6 shrink-0 text-[11px] text-faint tnum">{lineNo(x.id)}</span>
                    <span className="min-w-0 flex-1 truncate text-[12px]">{x.text}</span>
                    <Badge tone={(x.verifyingIssues ?? 0) >= x.openIssues ? 'warn' : 'danger'}>{(x.verifyingIssues ?? 0) >= x.openIssues ? '待复核' : x.issueTitle || '待返工'}</Badge>
                  </button>
                </li>
              ))}
            </ul>
          </div>
        )}
      </Pad>
      <Pad>
        <h4 className="mb-1 text-[13px] font-medium">智能审听</h4>
        <div className="flex items-start gap-3">
          <p className="flex-1 text-xs text-muted">智能审听会用语音识别逐句比对原文，找出漏读、错读、多读，以及爆音、底噪等音质问题。</p>
          {reviewer && <Button size="sm" variant="primary" loading={busy || run?.status === 'running' || run?.status === 'queued'} onClick={start}>{run ? '重新审听' : '开始审听'}</Button>}
        </div>
        {run?.status === 'done' && (
          <div className="mt-4 grid grid-cols-3 gap-3 rounded-[10px] bg-sunken/60 px-4 py-3 text-center">
            <div><div className="text-[18px] font-semibold tnum">{run.summary.segments}</div><div className="text-[11px] text-muted">句子</div></div>
            <div><div className={cn('text-[18px] font-semibold tnum', run.summary.flagged && 'text-warn')}>{run.summary.flagged}</div><div className="text-[11px] text-muted">有问题的句子</div></div>
            <div><div className="text-[18px] font-semibold tnum">{run.summary.total}</div><div className="text-[11px] text-muted">问题</div></div>
          </div>
        )}
        {run && !run.usedAsr && run.status === 'done' && <p className="mt-2 text-[11.5px] text-faint">未配置语音识别，只检查了音质问题。</p>}
      </Pad>
      {run?.status === 'done' && (
        <div className="border-t border-line">
          <div className="flex items-center justify-between px-5 py-2.5">
            <h4 className="text-xs font-medium uppercase tracking-wide text-faint">发现的问题</h4>
            <Switch checked={hideIgnored} onChange={setHideIgnored} label={<span className="text-xs text-muted">隐藏已忽略</span>} />
          </div>
          {!findings.length ? (
            <p className="px-5 pb-6 text-center text-[13px] text-muted">没有发现问题 🎉</p>
          ) : (
            <ul>
              {findings.map((f: any) => (
                <li key={f.id} className={cn('cursor-pointer border-t border-line px-5 py-3 hover:bg-sunken/40', f.ignored && 'opacity-50')} onClick={() => useWS.getState().setFocus(f.segmentId)}>
                  <div className="flex items-center gap-2">
                    <Badge tone={f.severity === 'high' ? 'danger' : f.severity === 'medium' ? 'warn' : 'neutral'}>{findingName[f.type] ?? f.type}</Badge>
                    <span className="min-w-0 flex-1 truncate text-[12.5px]">{f.message}</span>
                  </div>
                  <p className="mt-1.5 line-clamp-2 text-xs text-muted">「{segText(f.segmentId)}」</p>
                  {(f.expected || f.actual) && <p className="mt-1 text-[11.5px]"><span className="text-faint">原文</span> {f.expected || '—'} <span className="ml-2 text-faint">听到</span> <span className="text-warn">{f.actual || '—'}</span></p>}
                  {reviewer && (
                    <div className="mt-2 flex gap-1" onClick={(e) => e.stopPropagation()}>
                      {f.issueId ? <Badge tone="accent">已提工单</Badge> : <Button size="xs" icon={<Flag className="size-3" />} onClick={() => act(f.id, 'issue')}>提工单</Button>}
                      <Button size="xs" variant="ghost" icon={<RefreshCw className="size-3" />} onClick={async () => { await api.post(`/books/${book.id}/generate`, { chapterIds: [chapterId], scope: 'segments', segmentIds: [f.segmentId], regenerate: true }); toast.success('正在重新生成') }}>重新生成</Button>
                      <Button size="xs" variant="ghost" icon={<EyeOff className="size-3" />} onClick={() => act(f.id, f.ignored ? 'unignore' : 'ignore')}>{f.ignored ? '取消忽略' : '忽略'}</Button>
                    </div>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
      {reviewer && (
        <div className="sticky bottom-0 flex gap-2 border-t border-line bg-surface px-5 py-3">
          <Button size="sm" className="flex-1" onClick={() => setConclude('fail')}>打回返修</Button>
          <Button size="sm" variant="primary" className="flex-1" onClick={() => setConclude('pass')}>审听通过</Button>
        </div>
      )}
      <Dialog open={!!conclude} onOpenChange={(v) => !v && setConclude(null)} title={conclude === 'pass' ? '确认这一章审听通过？' : '打回返修'} width={440} footer={<><Button onClick={() => setConclude(null)}>取消</Button><Button variant={conclude === 'pass' ? 'primary' : 'danger'} onClick={async () => {
        await api.post(`/chapters/${chapterId}/conclude`, { pass: conclude === 'pass', reason })
        setConclude(null)
        setReason('')
        invalidate(['chapters', book.id], ['book', book.id])
        toast.success(conclude === 'pass' ? '已通过' : '已打回')
      }}>确定</Button></>}>
        {conclude === 'pass' ? <p className="text-[13px] text-muted">通过后这一章就可以导出和发布了。还有未关闭的工单时无法通过。</p> : <Field label="打回原因"><Textarea rows={3} value={reason} onChange={(e) => setReason(e.target.value)} /></Field>}
      </Dialog>
    </div>
  )
}

// ---------------- Comments ----------------
export function CommentsPanel({ book, chapterId, segs }: { book: any; chapterId: string; segs: any[] }) {
  const { data } = useQuery<any[]>({ queryKey: ['comments', chapterId], queryFn: () => api.get(`/chapters/${chapterId}/comments`) })
  const focus = useWS((s) => s.focus)
  const [body, setBody] = useState('')
  const [showResolved, setShowResolved] = useState(false)
  const seg = segs.find((s) => s.id === focus)
  const list = (data ?? []).filter((c) => showResolved || !c.resolved)
  const send = async () => {
    if (!body.trim()) return
    await api.post(`/books/${book.id}/comments`, { chapterId, segmentId: focus ?? '', body })
    setBody('')
    invalidate(['comments', chapterId])
  }
  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center justify-between px-5 py-3">
        <span className="text-xs text-muted">{list.length} 条评论</span>
        <Switch checked={showResolved} onChange={setShowResolved} label={<span className="text-xs text-muted">显示已解决</span>} />
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {!list.length ? (
          <Empty title="还没有评论" description="选中一句后在下方留言，可以用 @昵称 提醒成员。分享链接里的外部反馈也会出现在这里。" />
        ) : (
          list.map((c) => (
            <div key={c.id} className={cn('border-t border-line px-5 py-3', c.resolved && 'opacity-55')}>
              <div className="flex items-center gap-2">
                <Avatar name={c.authorName} src={c.avatarUrl} size={22} />
                <span className="text-[12.5px] font-medium">{c.authorName}</span>
                {c.external && <Badge>外部</Badge>}
                <span className="text-[11px] text-faint">{fromNow(c.createdAt)}</span>
                <div className="ml-auto">
                  <Button size="xs" variant="ghost" onClick={async () => { await api.patch(`/comments/${c.id}`, { resolved: !c.resolved }); invalidate(['comments', chapterId]) }}>{c.resolved ? '重新打开' : '解决'}</Button>
                </div>
              </div>
              {c.segmentId && (
                <button onClick={() => useWS.getState().setFocus(c.segmentId)} className="mt-1.5 line-clamp-1 text-left text-xs text-muted hover:text-accent">「{segs.find((s) => s.id === c.segmentId)?.text ?? '已删除的句子'}」</button>
              )}
              {c.atMs != null && <span className="mt-1 block text-xs text-muted">在 {timecode(c.atMs)}</span>}
              <p className="mt-1 whitespace-pre-wrap text-[13px]">{c.body}</p>
            </div>
          ))
        )}
      </div>
      {can(book, 'comment') && (
        <div className="border-t border-line p-4">
          {seg && <div className="mb-2 truncate text-[11.5px] text-muted">评论「{seg.text}」</div>}
          <Textarea rows={2} value={body} onChange={(e) => setBody(e.target.value)} placeholder={seg ? '写下你的意见…' : '先在左侧选择一句，或直接写下对本章的意见'} onKeyDown={(e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) send() }} />
          <div className="mt-2 flex justify-end"><Button size="sm" variant="primary" disabled={!body.trim()} onClick={send}>发送</Button></div>
        </div>
      )}
    </div>
  )
}
