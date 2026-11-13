import { useQuery } from '@tanstack/react-query'
import { Check, Loader2, Pause, Play, Wand2, X } from '@/components/icons'
import { useEffect, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { duration, fromNow, num } from '@/lib/format'
import { useSession } from '@/lib/session'
import { UploadZone } from '@/components/audio'
import { AssetPicker } from '@/components/asset'
import { Voice, VoicePicker } from '@/components/voice'
import { Badge, Button, cn, confirm, Empty, Fade, Field, Input, PageHeader, Panel, Progress, Segmented, Select, Switch, Term } from '@/components/ui'
import { ManageTemplatesLink, templateOptions, useTemplates } from '@/components/templates'
import { CopyrightNotice } from '@/components/legal'
import { invalidate, useChapters } from '../hooks'
import { BookCover } from './Books'

const finished = ['completed', 'succeeded', 'failed', 'canceled']
const stepName: Record<string, string> = { script: '写画本', tts: '配音', post: '后期', render: '合成' }

export default function AutoBook() {
  const sid = useSession((s) => s.studioId)
  const [params, setParams] = useSearchParams()
  const runId = params.get('run')
  const runs = useQuery<any[]>({ queryKey: ['pipelines', sid], queryFn: () => api.get(`/studios/${sid}/pipelines`), refetchInterval: 5000 })
  return (
    <div>
      <PageHeader title="一键成书" description="上传书稿，选好风格，剩下的交给 AI：拆章节、分析角色、选角、写画本、配音、配乐、合成。完成后随时可以进制作台精修任何一句。" />
      <Fade k={runId ?? 'new'}>{runId ? <RunView id={runId} onBack={() => setParams({})} /> : <Setup onStarted={(id) => setParams({ run: id })} />}</Fade>
      {!runId && !!runs.data?.length && (
        <Panel title="历史任务" className="mt-8" padded={false}>
          <ul>
            {runs.data.map((r) => (
              <li key={r.id}>
                <button onClick={() => setParams({ run: r.id })} className="flex w-full items-center gap-4 border-t border-line px-5 py-3 text-left first:border-t-0 hover:bg-sunken/40">
                  <BookCover book={{ title: r.book.title, cover: r.book.cover }} className="w-8" />
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-[13.5px] font-medium">{r.book.title}</div>
                    <div className="text-xs text-faint">{fromNow(r.createdAt)} · {num(r.usedCredits)} 积分</div>
                  </div>
                  <div className="w-40"><Progress value={r.progress} tone={runTone(r.status)} height={4} /></div>
                  <RunBadge status={r.status} waitReason={r.waitReason} />
                </button>
              </li>
            ))}
          </ul>
        </Panel>
      )}
    </div>
  )
}

function RunBadge({ status, waitReason }: { status: string; waitReason?: string }) {
  const m: Record<string, [string, any]> = { queued: ['排队中', 'neutral'], running: ['进行中', 'signal'], paused: ['已暂停', 'warn'], waiting: [waitReason === 'credits' ? '等待充值' : '等待确认', 'warn'], completed: ['已完成', 'ok'], succeeded: ['已完成', 'ok'], failed: ['未完成', 'danger'], canceled: ['已取消', 'neutral'] }
  return <Badge tone={m[status]?.[1]} dot={status === 'running'}>{m[status]?.[0] ?? status}</Badge>
}

const runTone = (status: string) => (status === 'failed' ? 'danger' : status === 'completed' || status === 'succeeded' ? 'ok' : 'signal')

function Setup({ onStarted }: { onStarted: (id: string) => void }) {
  const sid = useSession((s) => s.studioId)
  const [bookId, setBookId] = useState('')
  const [source, setSource] = useState<'upload' | 'existing'>('upload')
  const [busy, setBusy] = useState(false)
  const [cfg, setCfg] = useState({ mode: 'multi', tier: 'standard', narratorVoiceId: '', bgm: true, bgmAssetId: '', introTemplateId: '', outroTemplateId: '', preset: 'generic', pauseAfterCast: false, removeSpeechTags: false })
  const [narrator, setNarrator] = useState<Voice | null>(null)
  const [bgm, setBgm] = useState<any>(null)
  const [pickV, setPickV] = useState(false)
  const [pickM, setPickM] = useState(false)
  const [est, setEst] = useState<any>(null)
  const [range, setRange] = useState<'all' | 'part'>('all')
  const [from, setFrom] = useState(1)
  const [to, setTo] = useState(1)
  const books = useQuery({ queryKey: ['books', sid, 'autobook'], queryFn: () => api.get(`/studios/${sid}/books?pageSize=100`) })
  const chapters = useChapters(bookId || undefined)
  const included = (chapters.data ?? []).filter((c) => c.included)
  const presets = useQuery<any[]>({ queryKey: ['presets'], queryFn: () => api.get('/presets') })
  const templates = useTemplates(sid ?? undefined)
  const set = (k: string, v: any) => setCfg({ ...cfg, [k]: v })
  // "他说道" lines are usually dropped in a radio drama and kept in an audiobook; either can be changed
  const setMode = (v: string) => setCfg({ ...cfg, mode: v, removeSpeechTags: v === 'drama' })
  const lo = Math.min(Math.max(1, from), Math.max(1, included.length))
  const hi = Math.min(Math.max(lo, to), included.length)
  const picked = range === 'part' ? included.slice(lo - 1, hi) : included
  const chapterIds = range === 'part' ? picked.map((c) => c.id) : []
  const rangeKey = chapterIds.join(',')
  useEffect(() => {
    setRange('all')
    setFrom(1)
    setTo(1)
  }, [bookId])
  useEffect(() => {
    if (included.length && to === 1) setTo(Math.min(included.length, 10))
  }, [included.length])
  useEffect(() => {
    if (!bookId) return setEst(null)
    setEst(null)
    api.post(`/books/${bookId}/pipeline/estimate`, { ...cfg, chapterIds }).then(setEst).catch(() => {})
  }, [bookId, cfg.tier, cfg.mode, rangeKey])

  const upload = async (files: File[]) => {
    setBusy(true)
    try {
      const p = await api.upload(`/studios/${sid}/imports`, files[0])
      const b = await api.post(`/studios/${sid}/books`, { importId: p.id, title: p.title || files[0].name.replace(/\.[^.]+$/, ''), author: p.author, mode: cfg.mode, tier: cfg.tier, autoAnalyze: false, chapters: p.chapters.map((c: any, i: number) => ({ sources: [i], title: c.title, included: c.included })) })
      setBookId(b.id)
      invalidate(['books'])
      toast.success(`《${b.title}》已导入，共 ${p.chapters.filter((c: any) => c.included).length} 章`)
    } catch (e: any) {
      toast.error(e.message)
    } finally {
      setBusy(false)
    }
  }
  const start = async () => {
    setBusy(true)
    try {
      const r = await api.post(`/books/${bookId}/pipeline`, { ...cfg, chapterIds })
      invalidate(['pipelines'])
      onStarted(r.id)
    } finally {
      setBusy(false)
    }
  }
  const book = (books.data?.items ?? []).find((b: any) => b.id === bookId)
  const short = est && est.available < (est.minCredits ?? est.credits)
  const partly = est && !short && est.available < est.credits
  const tpl = (kind: 'intro' | 'outro') => templateOptions(templates.data, kind)

  return (
    <div className="grid gap-6 lg:grid-cols-[1fr_340px]">
      <div className="space-y-6">
        <Panel title="1. 选择书稿">
          <Segmented size="sm" value={source} onChange={(v) => { setSource(v); setBookId('') }} items={[{ value: 'upload', label: '上传新书稿' }, { value: 'existing', label: '使用已有图书' }]} />
          <div className="mt-4">
            {source === 'upload' ? (
              bookId && book ? (
                <div className="flex items-center gap-3 rounded-[10px] border border-accent bg-accent-soft px-4 py-3">
                  <BookCover book={book} className="w-9" />
                  <div className="flex-1 text-[13.5px] font-medium">{book.title}</div>
                  <Button size="xs" variant="ghost" onClick={() => setBookId('')}>换一本</Button>
                </div>
              ) : (
                <UploadZone accept=".txt,.md,.docx,.epub,.pdf" onFiles={upload} hint="txt、docx、epub、pdf，自动识别章节">{busy ? <p className="flex items-center gap-2 text-[13px] text-muted"><Loader2 className="size-4 animate-spin" />正在导入…</p> : undefined}</UploadZone>
              )
            ) : (
              <Select value={bookId} onChange={setBookId} placeholder="选择图书" options={(books.data?.items ?? []).map((b: any) => [b.id, b.title] as [string, string])} />
            )}
            {bookId && included.length > 0 && (
              <div className="mt-4 space-y-3 rounded-[10px] border border-line px-4 py-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="text-[13.5px] font-medium">制作范围</span>
                  <Segmented size="sm" value={range} onChange={setRange} items={[{ value: 'all', label: `全部 ${included.length} 章` }, { value: 'part', label: '指定章节' }]} />
                </div>
                {range === 'part' && (
                  <div className="space-y-2">
                    <div className="flex flex-wrap items-center gap-2 text-[13px]">
                      <span className="text-muted">从第</span>
                      <span className="w-20"><Input type="number" min={1} max={included.length} value={from} onChange={(e) => setFrom(Number(e.target.value) || 1)} className="tnum" aria-label="起始章节序号" /></span>
                      <span className="text-muted">章到第</span>
                      <span className="w-20"><Input type="number" min={1} max={included.length} value={to} onChange={(e) => setTo(Number(e.target.value) || 1)} className="tnum" aria-label="结束章节序号" /></span>
                      <span className="text-muted">章</span>
                      <Button size="xs" variant="ghost" onClick={() => { setFrom(1); setTo(Math.min(3, included.length)) }}>先试做前 3 章</Button>
                    </div>
                    <p className="text-xs text-muted">
                      {picked.length ? <>「{picked[0].title}」至「{picked[picked.length - 1].title}」，共 {picked.length} 章 {num(picked.reduce((n, c) => n + (c.wordCount || 0), 0))} 字。做完后可以接着选后面的章节。</> : '请选择章节'}
                    </p>
                  </div>
                )}
              </div>
            )}
            <CopyrightNotice className="mt-3">请确认书稿是你的原创、已取得有声改编和发行授权，或已进入公有领域；自动挑选的配乐、音效来自平台素材库，其许可信息仅供参考。本平台不审查版权，因此产生的纠纷由你自行承担。</CopyrightNotice>
          </div>
        </Panel>
        <Panel title="2. 制作风格">
          <div className="space-y-5">
            <Field label="类型">
              <Segmented value={cfg.mode} onChange={setMode} items={[{ value: 'single', label: '单播' }, { value: 'multi', label: '多播有声书' }, { value: 'drama', label: '广播剧' }]} />
            </Field>
            <Field label="质量档" hint="决定 AI 选角时使用的音色价位">
              <Segmented value={cfg.tier} onChange={(v) => set('tier', v)} items={[{ value: 'economy', label: '经济' }, { value: 'standard', label: '标准' }, { value: 'premium', label: '精品' }]} />
            </Field>
            <Field label="旁白声音" hint="不选的话 AI 会根据题材挑选">
              <Button size="sm" onClick={() => setPickV(true)}>{narrator?.name ?? '让 AI 选择'}</Button>
            </Field>
            <div className="flex items-center justify-between">
              <div><div className="text-[13.5px] font-medium">自动配背景音乐</div><div className="text-xs text-muted">根据每章情绪从音乐库挑选，说话时自动压低</div></div>
              <Switch checked={cfg.bgm} onChange={(v) => set('bgm', v)} />
            </div>
            {cfg.bgm && <Field label="指定音乐（可选）"><Button size="sm" onClick={() => setPickM(true)}>{bgm?.name ?? '由 AI 按情绪挑选'}</Button></Field>}
            <div className="grid gap-4 sm:grid-cols-3">
              <Field label="片头" hint={<ManageTemplatesLink />}><Select value={cfg.introTemplateId} onChange={(v) => set('introTemplateId', v)} options={tpl('intro')} /></Field>
              <Field label="片尾"><Select value={cfg.outroTemplateId} onChange={(v) => set('outroTemplateId', v)} options={tpl('outro')} /></Field>
              <Field label="平台标准" term="响度"><Select value={cfg.preset} onChange={(v) => set('preset', v)} options={(presets.data ?? []).map((p) => [p.key, p.name] as [string, string])} /></Field>
            </div>
            <div className="space-y-3 border-t border-line pt-4">
              <Switch checked={cfg.pauseAfterCast} onChange={(v) => set('pauseAfterCast', v)} label="选角完成后暂停，让我先确认声音再继续" />
              {cfg.mode !== 'single' && (
                <div>
                  <Switch checked={cfg.removeSpeechTags} onChange={(v) => set('removeSpeechTags', v)} label="去掉“他说道”之类的提示语" />
                  <p className="mt-1 pl-11 text-xs text-muted">每个角色都有自己的声音时，听众能听出是谁在说话，这类提示语可以省掉。只影响配音，书稿原文不变。</p>
                </div>
              )}
            </div>
          </div>
        </Panel>
      </div>
      <div>
        <Panel title="预估" className="lg:sticky lg:top-4">
          {!bookId ? (
            <p className="text-[13px] text-muted">选择书稿后显示预计消耗和时长。</p>
          ) : !est ? (
            <p className="text-[13px] text-muted">计算中…</p>
          ) : (
            <div className="space-y-4">
              <div className="grid grid-cols-2 gap-4">
                <div><div className="text-xs text-muted">字数 · {est.chapters} 章</div><div className="mt-1 text-[18px] font-semibold tnum">{num(est.words)}</div></div>
                <div><div className="text-xs text-muted">成品时长</div><div className="mt-1 text-[18px] font-semibold tnum">约 {duration(est.audioMs)}</div></div>
                <div><div className="flex items-center gap-1 text-xs text-muted">预计积分 <Term id="积分" /></div><div className={cn('mt-1 text-[18px] font-semibold tnum', short && 'text-danger')}>{num(est.credits)}</div></div>
                <div><div className="text-xs text-muted">处理时间</div><div className="mt-1 text-[18px] font-semibold tnum">约 {est.processingMinutes} 分钟</div></div>
              </div>
              <div className="space-y-1 rounded-[10px] bg-sunken/60 px-3 py-2.5 text-xs text-muted">
                <div className="flex justify-between"><span>角色分析</span><span className="tnum">{num(est.breakdown?.characters)}</span></div>
                <div className="flex justify-between"><span>画本</span><span className="tnum">{num(est.breakdown?.script)}</span></div>
                <div className="flex justify-between"><span>配音</span><span className="tnum">{num(est.breakdown?.tts)}</span></div>
              </div>
              {short && <p className="text-[13px] text-danger">积分不足：开始至少需要 {num(est.minCredits)} 积分，可用 {num(est.available)}。<Link to="/studio/billing" className="underline">去充值</Link></p>}
              {partly && <p className="rounded-[10px] bg-signal-soft px-3 py-2 text-[12.5px] leading-relaxed text-warn">现有 {num(est.available)} 积分不够做完全部章节。会按顺序先做前面的章节，积分用完时自动暂停，充值后点「继续」接着做。</p>}
              <Button variant="primary" size="lg" className="w-full" icon={<Wand2 className="size-4" />} loading={busy} disabled={short} onClick={start}>开始一键成书</Button>
              <p className="text-center text-[11.5px] text-faint">几章同时制作，做完一章就能先听。可以随时暂停或取消，只按实际用量扣积分。</p>
            </div>
          )}
        </Panel>
      </div>
      <VoicePicker open={pickV} onOpenChange={setPickV} title="选择旁白声音" current={cfg.narratorVoiceId} onPick={(v) => { setNarrator(v.id ? v : null); set('narratorVoiceId', v.id); setPickV(false) }} />
      <AssetPicker open={pickM} onOpenChange={setPickM} kinds={['bgm']} title="选择背景音乐" onPick={(a) => { setBgm(a); set('bgmAssetId', a.id); setPickM(false) }} />
    </div>
  )
}

function RunView({ id, onBack }: { id: string; onBack: () => void }) {
  const nav = useNavigate()
  const { data: r, refetch } = useQuery({ queryKey: ['pipeline-run', id], queryFn: () => api.get(`/pipelines/${id}`), refetchInterval: (q) => (finished.includes(q.state.data?.status) ? false : 2000) })
  const act = async (action: string) => {
    if (action === 'cancel' && !(await confirm({ title: '取消一键成书？', description: '已完成的章节会保留，只按实际用量扣积分。', danger: true, confirmText: '取消任务' }))) return
    await api.post(`/pipelines/${id}/action`, { action })
    refetch()
  }
  if (!r) return <Panel><p className="text-muted">加载中…</p></Panel>
  const active = r.status === 'running' || r.status === 'queued'
  const done = r.status === 'completed' || r.status === 'succeeded'
  const waitingCast = r.status === 'waiting' && r.waitReason !== 'credits'
  const waitingCredits = r.status === 'waiting' && r.waitReason === 'credits'
  const chapters: any[] = r.chapters ?? []
  const left = chapters.filter((c) => c.status !== 'done').length
  const failed = chapters.filter((c) => c.status === 'failed')
  return (
    <div className="space-y-6">
      <button onClick={onBack} className="text-[13px] text-muted hover:text-ink">← 新建任务</button>
      <Panel>
        <div className="flex flex-wrap items-center gap-4">
          <BookCover book={{ title: r.book.title, cover: r.book.cover }} className="w-14" />
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2"><h2 className="font-serif text-[19px] font-semibold">{r.book.title}</h2><RunBadge status={r.status} waitReason={r.waitReason} /></div>
            <p className="mt-0.5 text-[13px] text-muted">{chapters.length ? `${chapters.length - left}/${chapters.length} 章已完成 · ` : ''}已用 {duration(r.elapsedMs)} · 已消耗 {num(r.usedCredits)} / 预计 {num(r.estCredits)} 积分</p>
          </div>
          <div className="flex gap-2">
            {active && <Button icon={<Pause className="size-4" />} onClick={() => act('pause')}>暂停</Button>}
            {(r.status === 'paused' || r.status === 'failed' || waitingCredits) && <Button variant="primary" icon={<Play className="size-4" />} onClick={() => act('resume')}>{r.status === 'failed' ? '重做未完成的章' : '继续'}</Button>}
            {(active || r.status === 'paused' || r.status === 'waiting') && <Button variant="ghost" icon={<X className="size-4" />} onClick={() => act('cancel')}>取消</Button>}
            {done && <Button variant="primary" onClick={() => nav(`/books/${r.bookId}`)}>打开图书</Button>}
          </div>
        </div>
        <Progress value={r.progress} tone={runTone(r.status)} className="mt-5" height={6} />
        {active && r.progressText && <p className="mt-2 text-[13px] text-muted">{r.progressText}</p>}
        {r.error && !waitingCredits && <p className="mt-2 rounded-md bg-danger-soft px-3 py-2 text-[13px] text-danger">{r.error}</p>}
      </Panel>
      {waitingCast && (
        <div className="flex flex-wrap items-center gap-4 rounded-panel border border-accent bg-accent-soft px-5 py-4">
          <div className="flex-1"><div className="text-[14px] font-semibold">选角完成，等待你确认</div><div className="text-[13px] text-muted">去试听每个角色的声音，不满意可以直接更换。确认后开始逐章制作。</div></div>
          <Button onClick={() => nav(`/books/${r.bookId}/characters`)}>查看选角</Button>
          <Button variant="primary" onClick={() => act('confirm')}>确认，继续</Button>
        </div>
      )}
      {waitingCredits && (
        <div className="flex flex-wrap items-center gap-4 rounded-panel border border-signal/40 bg-signal-soft px-5 py-4">
          <div className="flex-1"><div className="text-[14px] font-semibold">积分用完了，已暂停</div><div className="text-[13px] text-muted">还有 {left} 章没有制作，已完成的章节可以先听。充值后点「继续」从下一章接着做。</div></div>
          <Button onClick={() => nav('/studio/billing')}>去充值</Button>
          <Button variant="primary" onClick={() => act('resume')}>继续</Button>
        </div>
      )}
      <Panel padded={false}>
        <ol>
          {r.stages.map((s: any, i: number) => (
            <li key={s.key} className="flex items-center gap-4 border-t border-line px-5 py-4 first:border-t-0">
              <span className={cn('grid size-7 shrink-0 place-items-center rounded-full text-xs font-semibold', s.status === 'done' ? 'bg-accent text-accent-ink' : s.status === 'running' ? 'bg-signal-soft text-warn' : s.status === 'failed' ? 'bg-danger-soft text-danger' : 'bg-sunken text-faint')}>
                {s.status === 'done' ? <Check className="size-4" strokeWidth={3} /> : s.status === 'running' ? <span className="eq flex h-3 items-end"><span /><span /><span /></span> : i + 1}
              </span>
              <span className={cn('flex-1 text-[14px]', s.status === 'pending' ? 'text-muted' : 'font-medium')}>{s.name}</span>
              {typeof s.total === 'number' && s.total > 0 && <span className="text-xs text-muted tnum">{s.done}/{s.total} 章</span>}
              {(s.status === 'running' || s.status === 'paused') && typeof s.progress === 'number' && <div className="w-40"><Progress value={s.progress} tone="signal" height={4} /></div>}
            </li>
          ))}
        </ol>
      </Panel>
      {chapters.length > 0 && (
        <Panel title="各章进度" actions={<span className="flex items-center gap-3 text-xs text-muted"><Legend className="bg-ok-soft" label="已完成" /><Legend className="bg-signal-soft" label="制作中" /><Legend className="bg-danger-soft" label="未完成" /><Legend className="bg-sunken" label="未开始" /></span>}>
          <div className="flex flex-wrap gap-1.5">
            {chapters.map((c) => (
              <Link key={c.id} to={`/books/${r.bookId}/chapters/${c.id}`} title={[c.title, c.status === 'running' && stepName[c.step] ? `正在${stepName[c.step]}` : '', c.error].filter(Boolean).join(' · ')} className={cn('grid h-7 min-w-7 place-items-center rounded-md px-1.5 text-xs tnum transition-colors', c.status === 'done' ? 'bg-ok-soft text-ok' : c.status === 'failed' ? 'bg-danger-soft text-danger' : c.status === 'running' ? 'bg-signal-soft text-warn' : 'bg-sunken text-faint')}>{c.index}</Link>
            ))}
          </div>
          {failed.length > 0 && (
            <ul className="mt-4 space-y-1.5 border-t border-line pt-3 text-[13px]">
              {failed.map((c) => (
                <li key={c.id} className="flex gap-2">
                  <Link to={`/books/${r.bookId}/chapters/${c.id}`} className="shrink-0 font-medium text-danger hover:underline">{c.title}</Link>
                  <span className="text-muted">{c.error}</span>
                </li>
              ))}
            </ul>
          )}
        </Panel>
      )}
      {done && <Empty title="全部完成" description="现在可以试听成品、进制作台精修，或者直接导出发布。" action={<Button variant="primary" onClick={() => nav(`/books/${r.bookId}/deliver`)}>导出发布</Button>} />}
    </div>
  )
}

function Legend({ className, label }: { className: string; label: string }) {
  return (
    <span className="flex items-center gap-1">
      <span className={cn('size-2.5 rounded-[3px]', className)} />
      {label}
    </span>
  )
}
