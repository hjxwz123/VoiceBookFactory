import { useQuery } from '@tanstack/react-query'
import { ArrowDown, ArrowUp, Check, Flag, Lock, Plus, RefreshCw, RotateCcw, Scissors, Trash2, Unlock, Upload } from '@/components/icons'
import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { useSession } from '@/lib/session'
import { clipLength, emotions, fromNow, issueStatusName, issueTypes, segTypeName } from '@/lib/format'
import { PlayButton, Recorder } from '@/components/audio'
import { Badge, Button, Chip, cn, confirm, Dialog, Empty, Field, IconButton, Input, Popover, Segmented, Select, Slider, Switch, Textarea, Tip } from '@/components/ui'
import { can, invalidate } from '../../hooks'
import { deleteSegments, moveBy, patchSegments, refetchScript, useWS } from './store'
import { ALT, MOD } from './ScriptList'
import { CueEditor, segmentCues } from '@/components/cues'

export default function Inspector({ book, chapterId, segs, chars }: { book: any; chapterId: string; segs: any[]; chars: Map<string, any> }) {
  const focus = useWS((s) => s.focus)
  const s = segs.find((x) => x.id === focus)
  if (!s)
    return (
      <Empty
        title="选择一句台词"
        description="点击左侧任意一句，在这里调整说话人、情绪、语速和读音，试听不同版本，或者直接录音。"
        className="h-full"
      />
    )
  return <SegmentInspector key={s.id} s={s} book={book} chapterId={chapterId} chars={chars} segs={segs} />
}

function Section({ title, children, actions }: { title: string; children: React.ReactNode; actions?: React.ReactNode }) {
  return (
    <section className="border-b border-line px-5 py-4">
      <div className="mb-3 flex items-center justify-between">
        <h4 className="text-xs font-medium uppercase tracking-wide text-faint">{title}</h4>
        {actions}
      </div>
      {children}
    </section>
  )
}

function SegmentInspector({ s, book, chapterId, chars, segs }: { s: any; book: any; chapterId: string; chars: Map<string, any>; segs: any[] }) {
  const editable = can(book, 'script')
  const genOK = can(book, 'generate')
  const char = chars.get(s.characterId)
  const patch = (p: Record<string, any>) => patchSegments(book.id, chapterId, [s.id], p)
  const [instruction, setInstruction] = useState(s.instruction ?? '')
  const [note, setNote] = useState(s.note ?? '')
  const [regen, setRegen] = useState(false)
  const [issue, setIssue] = useState(false)
  const takes = useQuery<any[]>({ queryKey: ['takes', s.id, s.selectedTakeId, s.audioStatus], queryFn: () => api.get(`/segments/${s.id}/takes`), enabled: !s.pending })
  const cues = segmentCues(s)
  const speed = s.speed ?? 1
  const pitch = s.pitch ?? 0
  const volume = s.volume ?? 0

  const regenerate = async () => {
    setRegen(true)
    try {
      await api.post(`/books/${book.id}/generate`, { chapterIds: [chapterId], scope: 'segments', segmentIds: [s.id], regenerate: true })
      toast.success('正在重新生成这一句')
    } finally {
      setRegen(false)
    }
  }
  const record = async (blob: Blob, origin = 'record') => {
    const id = toast.loading(origin === 'upload' ? '正在上传音频…' : '正在保存录音…')
    try {
      const r = await api.upload(`/segments/${s.id}/record`, blob, { origin }, 'take.webm')
      refetchScript(chapterId)
      invalidate(['takes', s.id])
      const tips: string[] = r.hints?.tips ?? []
      if (tips.length) toast.warning('录音已保存', { id, description: tips.join('；') })
      else toast.success(origin === 'upload' ? '音频已上传' : '录音已保存', { id })
    } catch (e: any) {
      toast.error(e?.message ?? '保存失败', { id, description: '录音没有保存成功，请再试一次。' })
    }
  }

  return (
    <div className="text-[13px]">
      <div className="border-b border-line px-5 py-4">
        <div className="flex items-center gap-2">
          <span className="size-2.5 rounded-full" style={{ background: char?.color }} />
          <span className="font-semibold">{char?.name ?? '未指定'}</span>
          <Badge>{segTypeName[s.type]}</Badge>
          {s.modified && <Badge tone="warn">已修改</Badge>}
          <div className="ml-auto flex gap-0.5">
            {editable && (
              <>
                <IconButton label={`上移（${ALT}+↑）`} size="xs" disabled={segs[0]?.id === s.id} icon={<ArrowUp className="size-3.5" />} onClick={() => { moveBy(chapterId, segs, [s.id], -1) }} />
                <IconButton label={`下移（${ALT}+↓）`} size="xs" disabled={segs[segs.length - 1]?.id === s.id} icon={<ArrowDown className="size-3.5" />} onClick={() => { moveBy(chapterId, segs, [s.id], 1) }} />
                <IconButton label={s.locked ? '解除锁定' : '锁定（批量生成时跳过）'} size="xs" icon={s.locked ? <Unlock className="size-3.5" /> : <Lock className="size-3.5" />} onClick={() => patch({ locked: !s.locked })} />
              </>
            )}
            <IconButton label="提工单" size="xs" icon={<Flag className="size-3.5" />} onClick={() => setIssue(true)} />
            {editable && !s.locked && <IconButton label="删除这一句（Delete）" size="xs" className="hover:!text-danger" icon={<Trash2 className="size-3.5" />} onClick={() => deleteSegments(book.id, chapterId, [s.id])} />}
          </div>
        </div>
        <TextWithPron s={s} book={book} chapterId={chapterId} editable={editable} />
        {s.openIssues > 0 && <SegmentIssues s={s} book={book} chapterId={chapterId} onRegenerate={genOK && s.type !== 'sfx' ? regenerate : undefined} />}
        {editable && (
          <div className="mt-3 flex flex-wrap gap-1.5">
            <Select size="sm" value={s.type} onChange={(v) => patch({ type: v })} options={Object.entries(segTypeName).filter(([k]) => k !== 'pause') as [string, string][]} className="w-24" />
            <Button size="xs" icon={<Scissors className="size-3.5" />} disabled={Array.from(s.text as string).length < 2 || s.locked} onClick={() => useWS.setState({ splitting: s.id })}>拆分</Button>
            <Tip label="在这一句上方插入新的一句">
              <Button size="xs" icon={<ArrowUp className="size-3.5" />} onClick={() => { const i = segs.findIndex((x) => x.id === s.id); useWS.getState().startDraft(i > 0 ? segs[i - 1].id : null) }}>上方插入</Button>
            </Tip>
            <Tip label={`在这一句下方插入新的一句（${MOD}+Enter）`}>
              <Button size="xs" icon={<Plus className="size-3.5" />} onClick={() => useWS.getState().startDraft(s.id)}>下方插入</Button>
            </Tip>
          </div>
        )}
      </div>

      <Section
        title="音频"
        actions={genOK && s.type !== 'sfx' && <Button size="xs" icon={<RefreshCw className="size-3.5" />} loading={regen || s.audioStatus === 'generating'} onClick={regenerate}>{s.take ? '重新生成' : '生成'}</Button>}
      >
        {s.audioStatus === 'failed' && <p className="mb-3 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">{s.lastError || '生成失败'}</p>}
        {s.qc === 'suspect' && s.qcNote && <p className="mb-3 rounded-md bg-warn-soft px-3 py-2 text-xs text-warn">自动质检：{s.qcNote}</p>}
        <div className="space-y-1.5">
          {(takes.data ?? []).map((t, i) => (
            <div key={t.id} className={cn('flex items-center gap-2.5 rounded-[9px] border px-2.5 py-2', t.selected ? 'border-accent bg-accent-soft/60' : 'border-line')}>
              <PlayButton id={'take:' + t.id} url={t.url} title={s.text} size="xs" />
              <div className="min-w-0 flex-1">
                <div className="truncate text-[12.5px] font-medium">
                  {t.origin === 'tts' ? t.voiceName || 'AI 配音' : t.origin === 'record' ? `录音 · ${t.createdBy}` : t.origin === 'align' ? '整段录音对轨' : `上传 · ${t.createdBy}`}
                  <span className="ml-1.5 font-normal text-faint">#{(takes.data?.length ?? 0) - i}</span>
                </div>
                <div className="text-[11px] text-faint">{clipLength(t.durationMs)} · {fromNow(t.createdAt)}{t.credits > 0 && ` · ${t.credits} 积分`}{t.note && ` · ${t.note}`}</div>
              </div>
              {t.selected ? (
                <Tip label="正在使用"><Check className="size-4 text-accent" /></Tip>
              ) : (
                <>
                  <Button size="xs" variant="ghost" onClick={async () => { await api.post(`/segments/${s.id}/takes/${t.id}/select`); refetchScript(chapterId); invalidate(['takes', s.id]); toast.success('已切换到这个版本') }}>使用</Button>
                  <IconButton label="删除这个版本" size="xs" icon={<Trash2 className="size-3.5" />} onClick={async () => { if (!(await confirm({ title: '删除这个版本？', description: '删除后不能恢复。', danger: true, confirmText: '删除' }))) return; await api.del(`/segments/${s.id}/takes/${t.id}`); invalidate(['takes', s.id]); toast.success('已删除这个版本') }} />
                </>
              )}
            </div>
          ))}
          {!takes.data?.length && <p className="text-xs text-faint">还没有音频。点「生成」用角色声音配音，或在下面直接录音。</p>}
        </div>
        {can(book, 'record') && (
          <div className="mt-3 flex items-center gap-2">
            <Recorder compact onDone={(b) => record(b)} />
            <label className="cursor-pointer">
              <input type="file" accept="audio/*" className="hidden" onChange={(e) => e.target.files?.[0] && record(e.target.files[0], 'upload')} />
              <span className="inline-flex h-8 items-center gap-1.5 rounded-control px-2.5 text-[12.5px] text-muted hover:bg-sunken hover:text-ink"><Upload className="size-3.5" />上传</span>
            </label>
          </div>
        )}
      </Section>

      {s.type !== 'sfx' && (
        <Section title="演绎">
          {char?.voice?.capabilities && !char.voice.capabilities.emotion && (
            <p className="mb-3 rounded-md bg-sunken px-3 py-2 text-xs text-muted">当前音色（{char.voice.modelLabel || char.voice.name}）不支持情绪和语气控制，下面的情绪设置会被忽略。可以通过语速、音高和停顿调整表现，或在「角色」里换成支持情绪的音色（Qwen-Audio、MiniMax 系列）。</p>
          )}
          <div className="flex flex-wrap gap-1.5">
            {emotions.map(([k, l]) => (
              <Chip key={k} active={(s.emotion || 'neutral') === k} onClick={editable ? () => patch({ emotion: k }) : undefined}>{l}</Chip>
            ))}
          </div>
          <div className="mt-3 flex items-center gap-3">
            <span className="w-14 text-xs text-muted">情绪强度</span>
            <Segmented size="sm" value={String(s.intensity || 1)} onChange={(v) => editable && patch({ intensity: Number(v) })} items={[{ value: '1', label: '轻' }, { value: '2', label: '中' }, { value: '3', label: '强' }]} />
          </div>
          <div className="mt-4">
            <div className="mb-1 flex items-center justify-between">
              <span className="text-xs text-muted">表演标签</span>
              {cues.length > 0 && editable && <button className="text-[11.5px] text-faint hover:text-danger" onClick={() => patch({ cues: [], tags: [] })}>全部清除</button>}
            </div>
            <p className="mb-2 text-[11.5px] text-faint">点击字与字之间的空隙插入标签，拖动标签可以换位置。{(s.cues ?? []).length > 0 && 'AI 已根据原文标出了一部分。'}</p>
            <div className="rounded-[10px] border border-line bg-sunken/40 px-3 py-1.5">
              <CueEditor text={s.text} cues={cues} disabled={!editable} onChange={(next) => patch({ cues: next, tags: [] })} />
            </div>
            {char?.voice?.capabilities && !char.voice.capabilities.inlineTags && cues.length > 0 && (
              <p className="mt-1.5 text-[11.5px] text-warn">{char.voice.capabilities.instruction ? '当前音色不支持句中标签：语气风格会转为表演指导，声音效果会被忽略。' : '当前音色不支持表演标签，合成时会忽略。'}</p>
            )}
            {char?.voice?.capabilities?.inlineTags && char.voice.capabilities.tagKeys?.length > 0 && cues.some((c) => !char.voice!.capabilities.tagKeys.includes(c.tag)) && (
              <p className="mt-1.5 text-[11.5px] text-warn">当前音色只能演出声音类标签（大笑、轻笑、叹气、咳嗽等）；句首的语气风格会转成整句情绪，其他位置的风格标签会被忽略。</p>
            )}
          </div>
          <Field label="表演指导" hint={char?.voice?.capabilities && !char.voice.capabilities.instruction ? '当前音色不支持自然语言指导' : '用自然语言告诉 AI 怎么读，例如「压低声音，带着哭腔，最后两个字拖长」'} className="mt-4">
            <Textarea rows={2} value={instruction} disabled={!editable} onChange={(e) => setInstruction(e.target.value)} onBlur={() => instruction !== (s.instruction ?? '') && patch({ instruction })} />
          </Field>
        </Section>
      )}

      <Section title="语速、音高与停顿" actions={editable && (s.speed != null || s.pitch != null || s.volume != null || s.pauseBeforeMs != null || s.pauseAfterMs != null) && <Button size="xs" variant="ghost" icon={<RotateCcw className="size-3" />} onClick={() => patch({ clearProsody: true, clearPauses: true })}>恢复默认</Button>}>
        <div className="space-y-4">
          <SliderRow label="语速" value={speed} display={`${speed.toFixed(2)}×`} min={0.6} max={1.6} step={0.05} disabled={!editable} onCommit={(v) => patch({ speed: v })} />
          <SliderRow label="音高" value={pitch} display={`${pitch > 0 ? '+' : ''}${pitch}`} min={-6} max={6} step={1} disabled={!editable} onCommit={(v) => patch({ pitch: v })} />
          <SliderRow label="音量" value={volume} display={`${volume > 0 ? '+' : ''}${volume} dB`} min={-12} max={12} step={1} disabled={!editable} onCommit={(v) => patch({ volume: v })} />
          <SliderRow label="句前停顿" value={s.pauseBeforeMs ?? 0} display={s.pauseBeforeMs == null ? '自动' : `${s.pauseBeforeMs} ms`} min={0} max={3000} step={50} disabled={!editable} onCommit={(v) => patch({ pauseBeforeMs: v })} />
          <SliderRow label="句后停顿" value={s.pauseAfterMs ?? 0} display={s.pauseAfterMs == null ? '自动' : `${s.pauseAfterMs} ms`} min={0} max={3000} step={50} disabled={!editable} onCommit={(v) => patch({ pauseAfterMs: v })} />
        </div>
      </Section>

      <Section title="备注">
        <Textarea rows={2} value={note} onChange={(e) => setNote(e.target.value)} onBlur={() => note !== (s.note ?? '') && patch({ note })} placeholder="给配音演员或后期的提示，只在制作台里显示" />
        <div className="mt-3"><Switch checked={!!s.needsReview} onChange={(v) => patch({ needsReview: v })} label="标记为待确认" /></div>
      </Section>
      <IssueDialog open={issue} onOpenChange={setIssue} book={book} chapterId={chapterId} s={s} />
    </div>
  )
}

function SliderRow({ label, value, display, min, max, step, onCommit, disabled }: { label: string; value: number; display: string; min: number; max: number; step: number; onCommit: (v: number) => void; disabled?: boolean }) {
  const [v, setV] = useState(value)
  useEffect(() => setV(value), [value])
  return (
    <div className={cn('flex items-center gap-3', disabled && 'pointer-events-none opacity-60')}>
      <span className="w-14 shrink-0 text-xs text-muted">{label}</span>
      <Slider value={v} min={min} max={max} step={step} onChange={setV} onCommit={onCommit} />
      <span className="w-14 shrink-0 text-right text-xs tnum">{v === value ? display : step < 1 ? v.toFixed(2) : v}</span>
    </div>
  )
}

/** Clicking a character opens its readings; picking one stores a pronunciation override (optionally book-wide). */
function TextWithPron({ s, book, chapterId, editable }: { s: any; book: any; chapterId: string; editable: boolean }) {
  const [at, setAt] = useState<number | null>(null)
  const chars = Array.from(s.text as string)
  const ch = at != null ? chars[at] : ''
  const readings = useQuery({ queryKey: ['pinyin', ch], queryFn: () => api.get(`/pinyin?char=${encodeURIComponent(ch)}`), enabled: !!ch })
  const prons: any[] = s.prons ?? []
  const set = async (pinyin: string, all: boolean) => {
    const next = prons.filter((p) => p.offset !== at)
    if (pinyin) next.push({ offset: at, char: ch, pinyin })
    await patchSegments(book.id, chapterId, [s.id], { prons: next })
    if (all && pinyin) {
      const r = await api.post(`/books/${book.id}/pronounce`, { char: ch, word: chars.slice(Math.max(0, at! - 1), at! + 2).join(''), pinyin, apply: true })
      toast.success(`已在全书 ${r.count} 处应用这个读音`, { description: '以后新生成的画本也想这样读，可以在图书的「读音」页添加词条。', duration: 6000 })
      refetchScript(chapterId)
    }
    setAt(null)
  }
  return (
    <div className="mt-3">
      <p className="text-[15px] leading-[1.9]">
        {chars.map((c, i) => {
          const p = prons.find((x) => x.offset === i)
          const isHan = /\p{Script=Han}/u.test(c)
          if (!editable || !isHan) return <span key={i} className={cn(p && 'text-accent')}>{c}</span>
          return (
            <Popover
              key={i}
              open={at === i}
              onOpenChange={(o) => setAt(o ? i : null)}
              width={260}
              trigger={<button className={cn('rounded-sm transition-colors hover:bg-accent-soft', p && 'text-accent underline decoration-dotted underline-offset-4', at === i && 'bg-accent-soft')}>{c}</button>}
            >
              <div className="text-[13px]">
                <div className="mb-2 font-medium">「{c}」的读音</div>
                {readings.isLoading ? (
                  <p className="text-xs text-muted">加载中…</p>
                ) : (
                  <div className="flex flex-wrap gap-1.5">
                    {(readings.data?.readings ?? []).map((r: string) => (
                      <Chip key={r} active={p?.pinyin === r} onClick={() => set(r, false)}>{r}</Chip>
                    ))}
                  </div>
                )}
                {!readings.data?.polyphonic && !readings.isLoading && <p className="mt-2 text-xs text-faint">这个字不是多音字，一般不需要调整。</p>}
                <div className="mt-3 flex justify-between border-t border-line pt-3">
                  {p ? <Button size="xs" variant="ghost" onClick={() => set('', false)}>清除</Button> : <span />}
                  {p && <Button size="xs" onClick={() => set(p.pinyin, true)}>应用到全书</Button>}
                </div>
              </div>
            </Popover>
          )
        })}
      </p>
      {editable && <p className="mt-1.5 text-[11.5px] text-faint">点击任意汉字可以指定读音；双击左侧句子可以修改文字。</p>}
    </div>
  )
}

export function IssueDialog({ open, onOpenChange, book, chapterId, s, atMs }: { open: boolean; onOpenChange: (v: boolean) => void; book: any; chapterId: string; s?: any; atMs?: number }) {
  const [f, setF] = useState({ type: 'pronunciation', title: '', description: '', severity: 'medium' })
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="提交返修工单" description={s ? `「${s.text.slice(0, 30)}${s.text.length > 30 ? '…' : ''}」` : undefined} width={480} footer={<><Button onClick={() => onOpenChange(false)}>取消</Button><Button variant="primary" onClick={async () => {
      await api.post(`/books/${book.id}/issues`, { chapterId, segmentId: s?.id ?? '', atMs, ...f, title: f.title || issueTypes.find(([k]) => k === f.type)?.[1] })
      invalidate(['issues'])
      refetchScript(chapterId)
      toast.success('工单已提交', { description: '这一句会标为「待返工」，修好后点「已修复」。' })
      onOpenChange(false)
    }}>提交</Button></>}>
      <div className="space-y-4">
        <Field label="问题类型">
          <div className="flex flex-wrap gap-1.5">{issueTypes.map(([k, l]) => <Chip key={k} active={f.type === k} onClick={() => setF({ ...f, type: k })}>{l}</Chip>)}</div>
        </Field>
        <Field label="标题"><Input value={f.title} onChange={(e) => setF({ ...f, title: e.target.value })} placeholder="一句话描述问题" /></Field>
        <Field label="说明"><Textarea rows={3} value={f.description} onChange={(e) => setF({ ...f, description: e.target.value })} placeholder="例如：「行」应读 háng" /></Field>
        <Field label="严重程度"><Segmented size="sm" value={f.severity} onChange={(v) => setF({ ...f, severity: v })} items={[{ value: 'low', label: '轻微' }, { value: 'medium', label: '一般' }, { value: 'high', label: '严重' }]} /></Field>
      </div>
    </Dialog>
  )
}

/** Open rework items on the selected line: what is wrong, and the next step for whoever looks at it. */
function SegmentIssues({ s, book, chapterId, onRegenerate }: { s: any; book: any; chapterId: string; onRegenerate?: () => Promise<void> }) {
  const me = useSession((st) => st.user)
  const { data } = useQuery<any>({ queryKey: ['issues', 'segment', s.id, s.openIssues, s.verifyingIssues], queryFn: () => api.get(`/books/${book.id}/issues?segmentId=${s.id}&status=active&pageSize=20`) })
  const [note, setNote] = useState('')
  const items: any[] = data?.items ?? []
  const reviewer = can(book, 'review') || can(book, 'manage')
  const act = async (i: any, action: string, extra: Record<string, any> = {}, ok = '已更新') => {
    await api.post(`/issues/${i.id}/action`, { action, ...extra })
    invalidate(['issues'])
    refetchScript(chapterId)
    toast.success(ok)
  }
  if (!items.length) return null
  return (
    <div className="mt-3 space-y-2">
      {items.map(({ issue: i, reporter }: any) => (
        <div key={i.id} className={cn('rounded-[10px] border px-3 py-2.5', i.status === 'verifying' ? 'border-warn/40 bg-warn-soft/50' : 'border-danger/30 bg-danger-soft/40')}>
          <div className="flex items-center gap-2">
            <Flag className={cn('size-3.5', i.status === 'verifying' ? 'text-warn' : 'text-danger')} />
            <span className="min-w-0 flex-1 truncate text-[12.5px] font-medium">{i.title}</span>
            <span className="shrink-0 text-[11px] text-faint">{issueStatusName[i.status]} · {reporter || '—'} · {fromNow(i.createdAt)}</span>
          </div>
          {i.description && <p className="mt-1 whitespace-pre-wrap text-[12px] text-muted">{i.description}</p>}
          {i.status !== 'verifying' && i.title === '需要返工' && (
            <div className="mt-2 flex flex-wrap gap-1">
              {issueTypes.filter(([k]) => k !== 'other').map(([k, l]) => <Chip key={k} onClick={() => act(i, 'note', { type: k }, `已标为「${l}」`)}>{l}</Chip>)}
            </div>
          )}
          {i.status !== 'verifying' && (
            <div className="mt-2 flex gap-1.5">
              <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder="补充说明，例如「行」应读 háng" className="h-7 text-[12px]" onKeyDown={(e) => { if (e.key === 'Enter' && note.trim()) { act(i, 'note', { note: note.trim() }, '说明已补充'); setNote('') } }} />
              <Button size="xs" disabled={!note.trim()} onClick={() => { act(i, 'note', { note: note.trim() }, '说明已补充'); setNote('') }}>补充</Button>
            </div>
          )}
          <div className="mt-2 flex flex-wrap items-center gap-1.5">
            {i.status !== 'verifying' && onRegenerate && <Button size="xs" icon={<RefreshCw className="size-3" />} onClick={onRegenerate}>重新生成这一句</Button>}
            {(i.status === 'open' || i.status === 'in_progress') && (reviewer && i.reporterId === me?.id
              ? <Button size="xs" variant="primary" onClick={() => act(i, 'close', {}, '已确认修好，工单关闭')}>已修好，关闭</Button>
              : <Button size="xs" variant="primary" onClick={() => act(i, 'submit', {}, '已提交复核')}>已修复，提交复核</Button>)}
            {i.status === 'verifying' && reviewer && <><Button size="xs" onClick={() => act(i, 'reopen', {}, '已退回重新处理')}>没修好</Button><Button size="xs" variant="primary" onClick={() => act(i, 'close', {}, '工单已关闭')}>确认关闭</Button></>}
            {i.status === 'open' && i.reporterId === me?.id && <Button size="xs" variant="ghost" className="ml-auto" onClick={() => act(i, 'withdraw', {}, '已撤回')}>撤回</Button>}
          </div>
        </div>
      ))}
    </div>
  )
}
