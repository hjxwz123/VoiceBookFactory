import { useQuery } from '@tanstack/react-query'
import { Archive, Copy, Trash2 } from '@/components/icons'
import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button, cn, confirm, DateInput, Field, Input, Panel, Segmented, Select, Slider, Switch, Textarea } from '@/components/ui'
import { ManageTemplatesLink, TemplatePreviewButton, templateOptions, useTemplates } from '@/components/templates'
import { can, invalidate, useChapters } from '../../hooks'
import { useLeaveGuard } from '@/components/net'

// pause presets (ms); chapter head/tail are left as they are
const RHYTHMS = {
  compact: { clauseMs: 200, sameSpeakerMs: 400, speakerChangeMs: 550, paragraphMs: 800, sceneMs: 1400 },
  standard: { clauseMs: 250, sameSpeakerMs: 550, speakerChangeMs: 700, paragraphMs: 1000, sceneMs: 1800 },
  relaxed: { clauseMs: 350, sameSpeakerMs: 750, speakerChangeMs: 900, paragraphMs: 1300, sceneMs: 2300 },
}
const RECOMMENDED_FLOW = { smooth: true, fadeMs: 30, duckHoldMs: 1500, roomTone: false, tempo: 1 }
// keep the user's own fade and hold values when smoothing is switched back on
const pick = (fl: any) => Object.fromEntries(Object.entries(fl ?? {}).filter(([k, v]) => (k === 'fadeMs' || k === 'duckHoldMs') && Number(v) > 0))

export default function BookSettings({ book }: { book: any }) {
  const nav = useNavigate()
  const initial = () => ({ title: book.title, author: book.author, narrator: book.narrator, publisher: book.publisher, isbn: book.isbn, genre: book.genre, mode: book.mode, tagsText: (book.tags ?? []).join('、'), copyright: { ...book.copyright }, settings: { ...book.settings, pause: { ...book.settings.pause }, flow: { ...(book.settings.flow ?? {}) } } })
  const [f, setF] = useState(initial)
  const [saved, setSaved] = useState(() => JSON.stringify(initial()))
  const dirty = useMemo(() => JSON.stringify(f) !== saved, [f, saved])
  useLeaveGuard(dirty, '图书设置有修改还没有保存，离开后会丢失。')
  const chapters = useChapters(book.id)
  const [busy, setBusy] = useState(false)
  const presets = useQuery<any[]>({ queryKey: ['presets'], queryFn: () => api.get('/presets') })
  const templates = useTemplates(book.studioId)
  const workflows = useQuery<any[]>({ queryKey: ['workflows', book.studioId], queryFn: () => api.get(`/studios/${book.studioId}/workflows`) })
  const [wf, setWf] = useState('')
  const editable = can(book, 'edit_info')
  const s = f.settings
  const setS = (k: string, v: any) => setF({ ...f, settings: { ...s, [k]: v } })
  const setP = (k: string, v: number) => setF({ ...f, settings: { ...s, pause: { ...s.pause, [k]: v } } })
  const setFlow = (k: string, v: any) => setF({ ...f, settings: { ...s, flow: { ...s.flow, [k]: v } } })
  const legacy = !s.flow?.smooth && !s.pause?.clauseMs
  const useRecommended = () => setF({ ...f, settings: { ...s, pause: { ...s.pause, ...RHYTHMS.standard, chapterHeadMs: s.pause.chapterHeadMs || 1000, chapterTailMs: s.pause.chapterTailMs || 2000 }, flow: { ...RECOMMENDED_FLOW, roomTone: !!s.flow?.roomTone, tempo: s.flow?.tempo || 1 } } })
  const tpl = (kind: 'intro' | 'outro') => templateOptions(templates.data, kind)

  const save = async () => {
    setBusy(true)
    try {
      const { tagsText, ...rest } = f
      await api.patch(`/books/${book.id}`, { ...rest, tags: tagsText.split(/[、,，\s]+/).filter(Boolean) })
      const before = JSON.parse(saved).settings
      setSaved(JSON.stringify(f))
      invalidate(['book', book.id], ['books'])
      // pauses, loudness, music ducking and bookends only reach the audio after re-rendering
      const soundKeys = ['pause', 'flow', 'loudnessPreset', 'duckingDb', 'introTemplateId', 'outroTemplateId', 'monologueReverb', 'aiLabel']
      const soundChanged = soundKeys.some((k) => JSON.stringify(before?.[k]) !== JSON.stringify(f.settings[k]))
      const rendered = (chapters.data ?? []).filter((c) => c.included && c.renderId)
      if (soundChanged && rendered.length && can(book, 'post')) {
        toast.success('设置已保存', {
          description: `已合成的 ${rendered.length} 章要重新合成才会用上新的停顿、语速、响度或片头片尾。不需要重新配音。`,
          duration: 10000,
          action: {
            label: '全部重新合成',
            onClick: async () => {
              const results = await Promise.allSettled(rendered.map((c) => api.post(`/chapters/${c.id}/render`, { preset: f.settings.loudnessPreset })))
              const ok = results.filter((r) => r.status === 'fulfilled').length
              if (ok) toast.success(`已开始重新合成 ${ok} 章`, { description: '在章节列表可以看到进度。' })
              if (ok < results.length) toast.error(`${results.length - ok} 章没能开始合成`, { description: '可能缺少配音，请到章节列表查看。' })
            },
          },
        })
      } else toast.success('设置已保存')
    } finally {
      setBusy(false)
    }
  }
  const pauses: [string, string, string][] = [
    ['clauseMs', '逗号处', '长句在逗号、顿号、分号处断开时'],
    ['sameSpeakerMs', '句与句之间', '同一个人说完一句、接着说下一句'],
    ['speakerChangeMs', '换人说话', '旁白和角色、角色和角色之间'],
    ['paragraphMs', '段落之间', ''],
    ['sceneMs', '场景切换', '原文里的空行或分隔符'],
    ['chapterHeadMs', '章节开头留白', ''],
    ['chapterTailMs', '章节结尾留白', ''],
  ]
  const rhythm = (Object.keys(RHYTHMS) as (keyof typeof RHYTHMS)[]).find((k) => Object.entries(RHYTHMS[k]).every(([pk, v]) => s.pause[pk] === v)) ?? 'custom'
  const tempo = s.flow?.tempo || 1

  return (
    <div className="mx-auto max-w-3xl space-y-6 pb-20">
      <Panel title="基本信息">
        <fieldset disabled={!editable} className="grid gap-4 sm:grid-cols-2">
          <Field label="书名"><Input value={f.title} onChange={(e) => setF({ ...f, title: e.target.value })} /></Field>
          <Field label="作者"><Input value={f.author} onChange={(e) => setF({ ...f, author: e.target.value })} /></Field>
          <Field label="演播署名"><Input value={f.narrator} onChange={(e) => setF({ ...f, narrator: e.target.value })} /></Field>
          <Field label="出版方"><Input value={f.publisher} onChange={(e) => setF({ ...f, publisher: e.target.value })} /></Field>
          <Field label="题材"><Input value={f.genre} onChange={(e) => setF({ ...f, genre: e.target.value })} /></Field>
          <Field label="标签" hint="用顿号分隔"><Input value={f.tagsText} onChange={(e) => setF({ ...f, tagsText: e.target.value })} /></Field>
          <Field label="制作类型" className="sm:col-span-2"><Segmented value={f.mode} onChange={(v) => setF({ ...f, mode: v })} items={[{ value: 'single', label: '单播' }, { value: 'multi', label: '多播' }, { value: 'drama', label: '广播剧' }]} size="sm" /></Field>
        </fieldset>
      </Panel>

      <Panel title="版权信息" description="发布到平台前需要填写。原创作品选择「原创」即可。">
        <fieldset disabled={!editable} className="grid gap-4 sm:grid-cols-2">
          <Field label="授权类型"><Select value={f.copyright.type || ''} onChange={(v) => setF({ ...f, copyright: { ...f.copyright, type: v } })} placeholder="请选择" options={[['original', '原创'], ['exclusive', '独家授权'], ['nonexclusive', '非独家授权'], ['public', '公版作品']]} /></Field>
          <Field label="授权方"><Input value={f.copyright.licensor} onChange={(e) => setF({ ...f, copyright: { ...f.copyright, licensor: e.target.value } })} /></Field>
          <Field label="授权开始"><DateInput value={f.copyright.startDate} onChange={(v) => setF({ ...f, copyright: { ...f.copyright, startDate: v } })} /></Field>
          <Field label="授权结束"><DateInput value={f.copyright.endDate} onChange={(v) => setF({ ...f, copyright: { ...f.copyright, endDate: v } })} placeholder="长期有效可不填" /></Field>
          <Field label="版权声明" className="sm:col-span-2"><Textarea rows={2} value={f.copyright.statement} onChange={(e) => setF({ ...f, copyright: { ...f.copyright, statement: e.target.value } })} /></Field>
        </fieldset>
      </Panel>

      <Panel title="声音与节奏" description="影响 AI 配音和合成的默认行为，改动后需要重新合成才会体现在成品里。">
        <fieldset disabled={!editable} className="space-y-5">
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="质量档"><Select value={s.qualityTier} onChange={(v) => setS('qualityTier', v)} options={[['economy', '经济'], ['standard', '标准'], ['premium', '精品']]} /></Field>
            <Field label="成品平台标准" term="响度"><Select value={s.loudnessPreset} onChange={(v) => setS('loudnessPreset', v)} options={(presets.data ?? []).map((p) => [p.key, p.name] as [string, string])} /></Field>
            {(['intro', 'outro'] as const).map((k) => {
              const id = k === 'intro' ? s.introTemplateId : s.outroTemplateId
              return (
                <Field key={k} label={k === 'intro' ? '片头' : '片尾'} hint={k === 'intro' ? <>每一集开头自动加上 · <ManageTemplatesLink /></> : '每一集结尾自动加上，个别章节可以在混音里关掉'}>
                  <div className="flex items-center gap-2">
                    <Select className="min-w-0 flex-1" value={id ?? ''} onChange={(v) => setS(k === 'intro' ? 'introTemplateId' : 'outroTemplateId', v)} options={tpl(k)} />
                    {id && <TemplatePreviewButton studioId={book.studioId} id={id} bookId={book.id} />}
                  </div>
                </Field>
              )
            })}
          </div>
          <Field label={`背景音乐避让 ${s.duckingDb} dB`} hint="有人说话时自动压低背景音乐"><Slider value={s.duckingDb} min={-24} max={0} step={1} onChange={(v) => setS('duckingDb', v)} /></Field>
          <div className="space-y-5 rounded-[12px] border border-line p-4">
            <div className="flex flex-wrap items-baseline justify-between gap-2">
              <div className="text-[14px] font-medium">停顿与语速</div>
              <div className="text-xs text-muted">只影响合成出来的成品，画本和已生成的配音不会改变</div>
            </div>
            {legacy && (
              <div className="flex flex-wrap items-center gap-3 rounded-[10px] bg-signal-soft px-3 py-2.5 text-[12.5px] text-warn">
                <span className="min-w-0 flex-1">这本书用的是旧版节奏：逗号和句号停顿一样长，句子边缘没有淡化，句间背景音乐会忽大忽小。推荐设置听起来更自然，保存并重新合成后生效。</span>
                <Button size="xs" onClick={useRecommended}>使用推荐设置</Button>
              </div>
            )}
            <Field label={<span className="flex w-full justify-between"><span>语速</span><span className="text-faint tnum">{tempo.toFixed(2)}×</span></span>} hint="整章人声合成好以后整体变速，音调不变；背景音乐和音效保持原速。改语速不用重新配音，重新合成即可。制作台里逐句试听仍是原速。">
              <div className="flex flex-wrap items-center gap-3">
                <Slider className="min-w-40 flex-1" value={tempo} min={0.7} max={1.5} step={0.05} onChange={(v) => setFlow('tempo', Math.round(v * 100) / 100)} />
                <div className="flex gap-1">{[0.9, 1, 1.1, 1.2].map((v) => <Button key={v} size="xs" variant={Math.abs(tempo - v) < 0.001 ? 'primary' : 'secondary'} onClick={() => setFlow('tempo', v)}>{v === 1 ? '原速' : `${v}×`}</Button>)}</div>
              </div>
            </Field>
            <div>
              <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
                <span className="text-[13px] font-medium">停顿节奏</span>
                <Segmented size="sm" value={rhythm} onChange={(v) => v !== 'custom' && setF({ ...f, settings: { ...s, pause: { ...s.pause, ...RHYTHMS[v as keyof typeof RHYTHMS] } } })} items={[{ value: 'compact', label: '紧凑' }, { value: 'standard', label: '标准' }, { value: 'relaxed', label: '舒缓' }, { value: 'custom', label: '自定义' }]} />
              </div>
              <div className="grid gap-x-6 gap-y-4 sm:grid-cols-2">
                {pauses.map(([k, l, hint]) => {
                  const v = k === 'clauseMs' && !s.pause.clauseMs ? s.pause.sameSpeakerMs : s.pause[k]
                  return (
                    <Field key={k} label={<span className="flex w-full justify-between"><span>{l}</span><span className="text-faint tnum">{v} ms</span></span>} hint={hint || undefined}>
                      <Slider value={v} min={k === 'clauseMs' ? 50 : 0} max={k.startsWith('chapter') ? 5000 : 3000} step={50} onChange={(nv) => setP(k, nv)} />
                    </Field>
                  )
                })}
              </div>
            </div>
            <div className="space-y-3 border-t border-line pt-4">
              <div>
                <Switch checked={!!s.flow?.smooth} onChange={(v) => setF({ ...f, settings: { ...s, flow: v ? { ...RECOMMENDED_FLOW, ...pick(s.flow), smooth: true } : { ...s.flow, smooth: false } } })} label="平滑衔接" />
                <p className="mt-1 pl-11 text-xs text-muted">每句开头结尾轻轻淡入淡出，避免生硬的切换和爆音；句间停顿不长时，背景音乐保持压低，不会忽大忽小。</p>
              </div>
              {s.flow?.smooth && (
                <div className="grid gap-x-6 gap-y-4 pl-11 sm:grid-cols-2">
                  <Field label={<span className="flex w-full justify-between"><span>句子边缘淡化</span><span className="text-faint tnum">{s.flow.fadeMs || 0} ms</span></span>}><Slider value={s.flow.fadeMs || 0} min={0} max={120} step={5} onChange={(v) => setFlow('fadeMs', v)} /></Field>
                  <Field label={<span className="flex w-full justify-between"><span>停顿短于多少时音乐保持压低</span><span className="text-faint tnum">{s.flow.duckHoldMs || 0} ms</span></span>}><Slider value={s.flow.duckHoldMs || 0} min={0} max={4000} step={100} onChange={(v) => setFlow('duckHoldMs', v)} /></Field>
                </div>
              )}
              <div>
                <Switch checked={!!s.flow?.roomTone} onChange={(v) => setFlow('roomTone', v)} label="句间加入轻微底噪" />
                <p className="mt-1 pl-11 text-xs text-muted">在人声下面铺一层很轻的环境底噪，停顿时不是完全无声，适合没有背景音乐的章节。</p>
              </div>
            </div>
          </div>
          <Field label={`估算时长用的语速 ${s.speechRate} 字/分钟`} hint="只用来估算成品时长，不改变配音速度"><Slider value={s.speechRate} min={150} max={320} step={10} onChange={(v) => setS('speechRate', v)} /></Field>
          <div className="space-y-3">
            <Switch checked={s.removeSpeechTags} onChange={(v) => setS('removeSpeechTags', v)} label="去掉“他说道”之类的提示语（广播剧常用）" />
            <Switch checked={s.monologueReverb} onChange={(v) => setS('monologueReverb', v)} label="内心独白自动加混响" />
            <Switch checked={s.autoQc} onChange={(v) => setS('autoQc', v)} label="生成后自动质检（漏读、杂音）" />
            <Switch checked={s.aiLabel} onChange={(v) => setS('aiLabel', v)} label={<span className="inline-flex items-center gap-1">在成品中加入 AI 生成内容提示</span>} />
          </div>
        </fieldset>
      </Panel>

      {can(book, 'manage') && (
        <Panel title="生产流程" description="开启后每章会按流程拆成任务分给成员（画本 → 配音 → 后期 → 审听），并支持计件结算。个人制作可以不开。">
          {book.workflowOn ? (
            <div className="flex items-center justify-between gap-4">
              <div className="text-[13px]">当前流程：{book.workflow.map((w: any) => w.name).join(' → ')}</div>
              <Button size="sm" onClick={async () => {
                if (await confirm({ title: '关闭生产流程？', description: '未完成的任务会被取消，已完成的计件记录保留。', danger: true, confirmText: '关闭' })) {
                  await api.del(`/books/${book.id}/workflow`)
                  invalidate(['book', book.id])
                }
              }}>关闭流程</Button>
            </div>
          ) : (
            <div className="flex gap-2">
              <Select value={wf} onChange={setWf} placeholder="选择流程模板" options={(workflows.data ?? []).map((w) => [w.id, `${w.name}：${w.stages.map((s: any) => s.name).join(' → ')}`] as [string, string])} className="flex-1" />
              <Button variant="primary" disabled={!wf} onClick={async () => { await api.post(`/books/${book.id}/workflow`, { templateId: wf }); invalidate(['book', book.id], ['chapters', book.id]); toast.success('已开启生产流程') }}>开启</Button>
            </div>
          )}
        </Panel>
      )}

      {can(book, 'manage') && (
        <Panel title="更多操作">
          <div className="flex flex-wrap gap-2">
            <Button icon={<Copy className="size-4" />} onClick={async () => { const b = await api.post(`/books/${book.id}/copy`); toast.success('已复制'); nav(`/books/${b.id}`) }}>复制这本书</Button>
            <Button icon={<Archive className="size-4" />} onClick={async () => { await api.patch(`/books/${book.id}`, { status: book.status === 'archived' ? 'producing' : 'archived' }); invalidate(['book', book.id]) }}>{book.status === 'archived' ? '取消归档' : '归档'}</Button>
            {can(book, 'delete') && (
              <Button variant="ghost" className="text-danger" icon={<Trash2 className="size-4" />} onClick={async () => {
                if (await confirm({ title: `删除《${book.title}》？`, description: '图书会移到回收站，30 天内可以恢复。', danger: true, confirmText: '删除' })) {
                  await api.del(`/books/${book.id}`)
                  invalidate(['books'])
                  nav('/books')
                }
              }}>删除</Button>
            )}
          </div>
        </Panel>
      )}

      {editable && (
        <div className="sticky bottom-4 flex justify-end">
          <div className={cn('flex items-center gap-3 rounded-full border bg-surface py-1.5 pl-4 pr-1.5 shadow-[var(--shadow-pop)] transition-colors', dirty ? 'border-accent/50' : 'border-line')}>
            <span className={cn('text-[12.5px]', dirty ? 'font-medium text-ink' : 'text-faint')}>{dirty ? '有修改还没保存' : '所有修改都已保存'}</span>
            {dirty && <Button size="sm" variant="ghost" onClick={() => setF(JSON.parse(saved))}>撤销修改</Button>}
            <Button variant="primary" loading={busy} disabled={!dirty} onClick={save}>保存设置</Button>
          </div>
        </div>
      )}
    </div>
  )
}
