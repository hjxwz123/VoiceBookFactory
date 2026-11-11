import { useQuery } from '@tanstack/react-query'
import { AnimatePresence, motion } from 'framer-motion'
import { AudioLines, Check, CircleHelp, Copy, Heart, Mic, Search, ShieldCheck, Sparkles, Store, Trash2, Wand2 } from '@/components/icons'
import { useEffect, useState } from 'react'
import { NavLink, Route, Routes, useNavigate, useSearchParams } from 'react-router-dom'
import { toast } from 'sonner'
import { api, qs } from '@/lib/api'
import { ageName, dateTime, emotions, genderName, num, tierName } from '@/lib/format'
import { usePlayer } from '@/lib/player'
import { useSession, useStudio } from '@/lib/session'
import { PlayButton, Recorder, UploadZone } from '@/components/audio'
import { Voice, VoicePlay, VoiceRow, voiceModels } from '@/components/voice'
import { Badge, Button, Checkbox, Chip, cn, confirm, Dialog, Empty, Field, IconButton, Input, PageHeader, Pagination, Panel, Progress, Segmented, Select, Sheet, Skeleton, Switch, Textarea, Tip } from '@/components/ui'
import { invalidate } from '../hooks'

export default function Voices() {
  return (
    <div>
      <PageHeader
        title="音色库"
        description="为角色挑选声音：平台内置上千种音色（每个音色对应一个合成模型），也可以用一句话让 AI 设计，或者克隆你自己的声音。"
      />
      <nav className="no-scrollbar mb-6 flex gap-1 overflow-x-auto overflow-y-hidden border-b border-line">
        {[['/voices', '浏览音色', true], ['/voices/design', 'AI 设计音色'], ['/voices/clone', '克隆声音'], ['/voices/earnings', '音色市场收益']].map(([to, l, end]) => (
          <NavLink key={to as string} to={to as string} end={!!end} className={({ isActive }) => cn('relative shrink-0 whitespace-nowrap px-3 pb-2.5 pt-1 text-[13.5px]', isActive ? 'font-medium text-ink' : 'text-muted hover:text-ink')}>
            {({ isActive }) => <>{l}{isActive && <motion.span layoutId="voice-tab" className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-ink" />}</>}
          </NavLink>
        ))}
      </nav>
      <Routes>
        <Route index element={<Library />} />
        <Route path="design" element={<Design />} />
        <Route path="clone" element={<Clone />} />
        <Route path="earnings" element={<Earnings />} />
      </Routes>
    </div>
  )
}

function Library() {
  const sid = useSession((s) => s.studioId)
  const [params, setParams] = useSearchParams()
  const [tab, setTab] = useState('system')
  const [q, setQ] = useState('')
  const [gender, setGender] = useState('')
  const [age, setAge] = useState('')
  const [tier, setTier] = useState('')
  const [vmodel, setVModel] = useState('')
  const [series, setSeries] = useState('')
  const [page, setPage] = useState(1)
  const open = params.get('voice')
  const { data, isLoading } = useQuery({ queryKey: ['voices', sid, tab, q, gender, age, tier, vmodel, series, page], queryFn: () => api.get(`/studios/${sid}/voices${qs({ tab, q, gender, age, tier, model: vmodel, series, page, pageSize: 24, group: 1 })}`), placeholderData: (p) => p })
  const reset = () => setPage(1)
  return (
    <div>
      <div className="mb-3 flex flex-wrap items-center gap-2.5">
        <Segmented value={tab} onChange={(v) => { setTab(v); reset() }} items={[{ value: 'system', label: '平台音色' }, { value: 'studio', label: '工作室' }, { value: 'mine', label: '我的' }, { value: 'favorites', label: '收藏' }, { value: 'market', label: '市场' }]} />
        <div className="ml-auto flex flex-wrap items-center gap-2">
          <Select value={vmodel} onChange={(v) => { setVModel(v); reset() }} options={voiceModels} className="w-40" />
          {tab === 'system' && <Select value={series} onChange={(v) => { setSeries(v); reset() }} options={[['', '全部系列'], ['system', '精选系统音色'], ['base', '基础音色库（600+）']]} className="w-44" />}
          <div className="w-64 max-sm:w-full"><Input icon={<Search />} placeholder="搜索：温柔、少年、播音腔……" value={q} onChange={(e) => { setQ(e.target.value); reset() }} /></div>
        </div>
      </div>
      {/* one line of filters, each group named, so the choices read as 性别 / 年龄 / 价位 rather than a wall of pills */}
      <div className="mb-5 flex flex-wrap items-center gap-x-5 gap-y-2 rounded-panel border border-line bg-surface px-4 py-2.5 shadow-[var(--shadow-card)]">
        {([
          ['性别', gender, setGender, [['', '全部'], ['female', '女声'], ['male', '男声']]],
          ['年龄', age, setAge, [['', '全部'], ['child', '儿童'], ['teen', '少年'], ['young', '青年'], ['adult', '成年'], ['middle', '中年'], ['senior', '老年']]],
          ['价位', tier, setTier, [['', '全部'], ['economy', '经济'], ['standard', '标准'], ['premium', '精品']]],
        ] as [string, string, (v: string) => void, string[][]][]).map(([label, cur, set, opts]) => (
          <div key={label} className="flex flex-wrap items-center gap-1.5">
            <span className="mr-1 text-[12px] text-faint">{label}</span>
            {opts.map(([v, l]) => <Chip key={v} active={cur === v} onClick={() => { set(v); reset() }}>{l}</Chip>)}
          </div>
        ))}
      </div>
      {isLoading ? (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">{Array.from({ length: 9 }, (_, i) => <Skeleton key={i} className="h-16" />)}</div>
      ) : !data?.items?.length ? (
        <Empty icon={<AudioLines />} title="没有找到音色" description={tab === 'mine' ? '用 AI 设计一个，或者克隆你自己的声音。' : '换个筛选条件试试。'} />
      ) : (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {data.items.map((v: Voice, i: number) => (
            <div key={v.id} className="rise-in" style={{ '--i': i } as React.CSSProperties}>
              <VoiceRow v={v} onSelect={() => setParams({ voice: v.id })} onVersion={(x) => setParams({ voice: x.id })} extra={v.favorite ? <Heart className="size-3.5 fill-danger text-danger" /> : null} />
            </div>
          ))}
        </div>
      )}
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
      <VoiceDetail id={open} onClose={() => setParams({})} onSwitch={(x) => setParams({ voice: x })} />
    </div>
  )
}

function VoiceDetail({ id, onClose, onSwitch }: { id: string | null; onClose: () => void; onSwitch: (id: string) => void }) {
  const sid = useSession((s) => s.studioId)
  const me = useSession((s) => s.user)
  const { data: v, refetch } = useQuery({ queryKey: ['voice', id], queryFn: () => api.get(`/voices/${id}`), enabled: !!id })
  const [text, setText] = useState('')
  const [emotion, setEmotion] = useState('neutral')
  const [busy, setBusy] = useState(false)
  const [edit, setEdit] = useState<any>(null)
  const [listing, setListing] = useState(false)
  useEffect(() => { if (v) setText(v.sampleText || '你好，很高兴认识你。') }, [v?.id])
  const owned = v && (v.ownerUserId === me?.id || (v.scope === 'studio' && v.studioId === sid))
  const preview = async () => {
    setBusy(true)
    try {
      const r = await api.post(`/studios/${sid}/voices/${v.id}/preview`, { text, emotion })
      usePlayer.getState().play('preview:' + v.id, r.url, v.name)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Sheet open={!!id} onOpenChange={(o) => !o && onClose()} title={v?.name ?? ''} description={v ? [v.gender !== "unknown" && genderName[v.gender], v.ageGroup !== "unknown" && ageName[v.ageGroup], tierName[v.priceTier] + '档', v.provider === 'placeholder' ? '内置演示引擎' : v.modelLabel].filter(Boolean).join(' · ') : ''} width={520}
      footer={owned && <><Button variant="ghost" className="mr-auto text-danger" icon={<Trash2 className="size-4" />} onClick={async () => {
        if (await confirm({ title: `删除「${v.name}」？`, description: '已经用这个音色生成的音频不受影响，但之后无法再用它配音。', danger: true, confirmText: '删除' })) {
          await api.del(`/studios/${sid}/voices/${v.id}`)
          invalidate(['voices'])
          onClose()
        }
      }}>删除</Button>{v.scope === 'user' && <Button icon={<Store className="size-4" />} onClick={() => setListing(true)}>{v.listed ? '管理上架' : '上架到市场'}</Button>}<Button onClick={() => setEdit({ name: v.name, tagsText: (v.tags ?? []).join('、'), description: v.description, shared: v.shared, gender: v.gender, ageGroup: v.ageGroup })}>编辑</Button></>}
    >
      {v && (
        <div className="space-y-6">
          <div className="flex items-center gap-3">
            <VoicePlay v={v} size="md" />
            <div className="min-w-0 flex-1">
              <p className="text-[13.5px] text-muted">{v.description || '暂无介绍'}</p>
              <div className="mt-1.5 flex flex-wrap gap-1">{(v.tags ?? []).map((t: string) => <Badge key={t}>{t}</Badge>)}</div>
            </div>
            <IconButton label={v.favorite ? '取消收藏' : '收藏'} icon={<Heart className={cn('size-4', v.favorite && 'fill-danger text-danger')} />} onClick={async () => { await api.post(`/voices/${v.id}/favorite`); refetch(); invalidate(['voices']) }} />
          </div>
          {(v.versions?.length ?? 0) > 1 && (
            <div>
              <div className="mb-1.5 flex items-center gap-1 text-xs font-medium text-muted">模型版本<Tip label="同一个声音在不同合成模型上的版本：Plus / 新版本音质更好、表现力更强，Flash / 旧版本更便宜。选角时可以在音色卡片上切换。"><CircleHelp className="size-3.5 text-faint" /></Tip></div>
              <Segmented size="sm" value={v.id} onChange={onSwitch} items={v.versions.map((x: any) => ({ value: x.id, label: `${x.modelLabel || x.model} · ${tierName[x.priceTier]}` }))} />
            </div>
          )}
          {v.status !== 'ready' && <p className="rounded-md bg-warn-soft px-3 py-2 text-[13px] text-warn">{v.status === 'reviewing' ? '授权材料审核中，通过后即可使用' : v.status === 'training' ? '正在训练，通常需要几分钟' : v.statusNote || v.status}</p>}
          {v.status === 'ready' && v.origin === 'cloned' && v.expiresAt && <p className="rounded-md bg-sunken px-3 py-2 text-[13px] text-muted">请在 {dateTime(v.expiresAt)} 前用这个音色生成一次配音（试听或任意一句都算），之后就长期有效；一直不用会被语音平台自动删除。</p>}
          <Panel title="试听你的文字" padded>
            <Textarea rows={3} value={text} onChange={(e) => setText(e.target.value)} maxLength={300} />
            <div className="mt-3 flex flex-wrap gap-1.5">
              {emotions.filter(([k]) => !v.emotions?.length || v.emotions.includes(k)).map(([k, l]) => <Chip key={k} active={emotion === k} onClick={() => setEmotion(k)}>{l}</Chip>)}
            </div>
            <Button className="mt-4" variant="primary" loading={busy} disabled={!text.trim() || v.status !== 'ready'} onClick={preview} icon={<AudioLines className="size-4" />}>生成试听</Button>
            <p className="mt-2 text-xs text-faint">试听每天有免费次数，超出后按字数消耗少量积分。</p>
          </Panel>
          {v.capabilities && (
            <div>
              <div className="mb-2 text-xs font-medium text-muted">这个音色支持</div>
              <div className="flex flex-wrap gap-1.5 text-xs">
                {v.capabilities.emotion && <Badge tone="accent">情绪控制</Badge>}
                {v.capabilities.instruction && <Badge tone="accent">自然语言指导</Badge>}
                {v.capabilities.inlineTags && <Badge tone="accent">语气标签</Badge>}
                {v.capabilities.phoneme && <Badge tone="accent">指定读音</Badge>}
                {v.capabilities.ssml && <Badge tone="accent">SSML</Badge>}
                {v.capabilities.dialogue && <Badge tone="accent">多人对话</Badge>}
                {!v.capabilities.emotion && <Badge>通过语速、音高和停顿调整表现</Badge>}
              </div>
              {v.model && (v.provider === 'minimax'
                ? <p className="mt-2 text-xs text-faint">这一条用 {v.modelLabel} 合成。同一个声音在音色库里还有{v.model.endsWith('-hd') ? ' Turbo 版（更快、更省积分）' : ' HD 版（音质更好）'}，可以按需要选择。</p>
                : <p className="mt-2 text-xs text-faint">这个音色只能用于 {v.modelLabel}，生成时会自动使用该模型。</p>)}
            </div>
          )}
        </div>
      )}
      <Dialog open={!!edit} onOpenChange={(o) => !o && setEdit(null)} title="编辑音色" width={460} footer={<><Button onClick={() => setEdit(null)}>取消</Button><Button variant="primary" onClick={async () => {
        await api.patch(`/studios/${sid}/voices/${v.id}`, { name: edit.name, tags: edit.tagsText.split(/[、,，\s]+/).filter(Boolean), description: edit.description, shared: edit.shared, gender: edit.gender, ageGroup: edit.ageGroup })
        setEdit(null)
        refetch()
        invalidate(['voices'])
      }}>保存</Button></>}>
        {edit && (
          <div className="space-y-4">
            <Field label="名称"><Input value={edit.name} onChange={(e) => setEdit({ ...edit, name: e.target.value })} /></Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label="性别"><Select value={edit.gender} onChange={(x) => setEdit({ ...edit, gender: x })} options={Object.entries(genderName)} /></Field>
              <Field label="年龄段"><Select value={edit.ageGroup} onChange={(x) => setEdit({ ...edit, ageGroup: x })} options={Object.entries(ageName)} /></Field>
            </div>
            <Field label="标签" hint="用顿号分隔"><Input value={edit.tagsText} onChange={(e) => setEdit({ ...edit, tagsText: e.target.value })} /></Field>
            <Field label="介绍"><Textarea rows={2} value={edit.description} onChange={(e) => setEdit({ ...edit, description: e.target.value })} /></Field>
            {v.scope === 'user' && <Switch checked={edit.shared} onChange={(x) => setEdit({ ...edit, shared: x })} label="共享给工作室成员使用" />}
          </div>
        )}
      </Dialog>
      {v && <ListingDialog open={listing} onOpenChange={setListing} v={v} onDone={refetch} />}
    </Sheet>
  )
}

function ListingDialog({ open, onOpenChange, v, onDone }: { open: boolean; onOpenChange: (o: boolean) => void; v: any; onDone: () => void }) {
  const sid = useSession((s) => s.studioId)
  const [f, setF] = useState({ pricePer1k: 50, commercial: true, intro: '' })
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="上架到音色市场" description="其他用户使用你的音色配音时，你会按字数获得收益。上架需要平台审核。" width={460} footer={<>{v.listed && <Button variant="ghost" className="mr-auto text-danger" onClick={async () => { await api.del(`/studios/${sid}/voices/${v.id}/listing`); onOpenChange(false); onDone() }}>下架</Button>}<Button onClick={() => onOpenChange(false)}>取消</Button><Button variant="primary" onClick={async () => { await api.post(`/studios/${sid}/voices/${v.id}/listing`, f); toast.success('已提交审核'); onOpenChange(false); onDone() }}>提交审核</Button></>}>
      <div className="space-y-4">
        <Field label="每千字价格（积分）" hint={`买家每使用 1000 字支付 ${f.pricePer1k} 积分，平台抽成后结算给你`}><Input type="number" min={0} value={f.pricePer1k} onChange={(e) => setF({ ...f, pricePer1k: Number(e.target.value) })} /></Field>
        <Field label="介绍"><Textarea rows={3} value={f.intro} onChange={(e) => setF({ ...f, intro: e.target.value })} placeholder="适合什么类型的角色和作品" /></Field>
        <Checkbox checked={f.commercial} onChange={(x) => setF({ ...f, commercial: x })} label="允许商用" />
      </div>
    </Dialog>
  )
}

// ---------------- Voice design ----------------
const examples = ['二十岁左右的女生，声音清甜，语速轻快，带点俏皮', '四十岁的中年男人，嗓音低沉沙哑，说话慢条斯理，有威严', '七八岁的小男孩，奶声奶气，活泼好动', '年迈的老奶奶，声音温和慈祥，略带沙哑', '冷酷的少年剑客，声音清冷，惜字如金']

function Design() {
  const sid = useSession((s) => s.studioId)
  const [params] = useSearchParams()
  const nav = useNavigate()
  const charId = params.get('character') ?? ''
  const bookId = params.get('book') ?? ''
  const [desc, setDesc] = useState('')
  const [preview, setPreview] = useState('')
  const [designId, setDesignId] = useState('')
  const [busy, setBusy] = useState(false)
  const [save, setSave] = useState<number | null>(null)
  const [name, setName] = useState('')
  const [scope, setScope] = useState('user')
  const char = useQuery({ queryKey: ['character-for-design', charId], queryFn: async () => (await api.get(`/books/${bookId}/characters`)).find((c: any) => c.id === charId), enabled: !!charId })
  useEffect(() => {
    const c = char.data
    if (c && !desc) {
      setDesc([genderName[c.gender], ageName[c.ageGroup], c.identity, c.voiceTraits || c.personality].filter((x) => x && x !== '未知').join('，'))
      setName(c.name)
    }
  }, [char.data])
  const design = useQuery({ queryKey: ['design', designId], queryFn: () => api.get(`/voice-designs/${designId}`), enabled: !!designId, refetchInterval: (q) => (q.state.data?.status === 'ready' || q.state.data?.status === 'failed' ? false : 1500) })
  const history = useQuery<any>({ queryKey: ['designs', sid], queryFn: () => api.get(`/studios/${sid}/voice-designs?pageSize=8`) })
  const optQ = useQuery<{ available: boolean; provider: string; platform: string; demo: boolean; modelLabel?: string; promptMax: number; keepHours: number }>({ queryKey: ['design-options', sid], queryFn: () => api.get(`/studios/${sid}/voices/design-options`), enabled: !!sid })
  const dopt = optQ.data
  const promptMax = dopt?.promptMax || 1000
  const d = design.data
  const start = async () => {
    setBusy(true)
    try {
      const r = await api.post(`/studios/${sid}/voices/design`, { description: desc, previewText: preview, characterId: charId })
      setDesignId(r.design.id)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="grid gap-6 lg:grid-cols-[1fr_320px]">
      <div className="space-y-6">
        <Panel>
          {char.data && <p className="mb-4 rounded-md bg-accent-soft px-3 py-2 text-[13px] text-accent">正在为《{char.data.name}》设计声音，保存后可以直接用在这个角色上。</p>}
          <Field label="描述你想要的声音" hint={dopt?.provider === 'qwen' ? '越具体越好：性别、年龄、音调、语速、情绪、音色质感、用途。只描述声音特点，不要要求模仿某个真实人物。' : '越具体越好：年龄、性别、音色质感、语速、情绪基调、口音……'}>
            <div className="relative">
              <Textarea rows={4} value={desc} maxLength={promptMax} onChange={(e) => setDesc(e.target.value)} placeholder="例如：二十岁左右的女生，声音清甜，语速轻快，带点俏皮" />
              <span className={cn('pointer-events-none absolute bottom-2 right-3 text-[11px] tnum', desc.length > promptMax * 0.9 ? 'text-warn' : 'text-faint')}>{desc.length}/{promptMax}</span>
            </div>
          </Field>
          <div className="mt-3 flex flex-wrap gap-1.5">
            {examples.map((e) => <Chip key={e} onClick={() => setDesc(e)}>{e.slice(0, 14)}…</Chip>)}
          </div>
          <Field label="试听文本（可选）" className="mt-5" hint={dopt?.provider === 'qwen' ? '15～200 字，只支持中文或英文；不符合时会用默认台词试听。' : undefined}><Input value={preview} onChange={(e) => setPreview(e.target.value)} placeholder="留空会自动生成一段适合这个声音的台词" /></Field>
          <div className="mt-5 flex items-center justify-between gap-4">
            <span className="text-xs text-faint">
              每次生成 3 个候选声音，消耗少量积分
              {dopt?.available && (dopt.demo
                ? <span className="mt-0.5 block text-warn">当前只接入了内置演示引擎，只能体验流程，不会生成真实的声音</span>
                : <span className="mt-0.5 block">由{/^[\x00-\x7f]+$/.test(dopt.platform) ? ` ${dopt.platform} ` : dopt.platform}设计{dopt.modelLabel ? `（${dopt.modelLabel}），保存后绑定这个模型，长期有效` : ''}</span>)}
            </span>
            <Button variant="primary" icon={<Wand2 className="size-4" />} loading={busy || (d && d.status !== 'ready' && d.status !== 'failed')} disabled={desc.trim().length < 4} onClick={start}>生成候选</Button>
          </div>
        </Panel>
        <AnimatePresence>
          {d && (
            <motion.div initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }}>
              <Panel title="候选声音" description={d.status === 'ready' ? `逐个试听，满意的点「保存」加入音色库。${dopt?.keepHours ? `没保存的候选 ${dopt.keepHours} 小时后会从${dopt.platform}清理。` : ''}` : undefined}>
                {d.status === 'failed' ? (
                  <p className="text-[13px] text-danger">{d.error || '生成失败'}</p>
                ) : d.status !== 'ready' ? (
                  <div className="flex items-center gap-3 py-6 text-[13px] text-muted"><span className="eq flex h-3 items-end text-signal"><span /><span /><span /></span>AI 正在设计声音…</div>
                ) : (
                  <div className="grid gap-3 sm:grid-cols-3">
                    {d.candidates.map((c: any) => (
                      <div key={c.index} className="rounded-[10px] border border-line p-4 text-center">
                        <PlayButton id={`design:${d.id}:${c.index}`} url={c.previewUrl} size="md" className="mx-auto" />
                        <div className="mt-3 text-[13px] font-medium">候选 {c.index + 1}</div>
                        {c.saved ? <Badge tone="ok" className="mt-2"><Check className="size-3" />已保存</Badge> : c.discarded ? <Badge className="mt-2">已过期</Badge> : <Button size="xs" className="mt-2" onClick={() => setSave(c.index)}>保存</Button>}
                      </div>
                    ))}
                  </div>
                )}
              </Panel>
            </motion.div>
          )}
        </AnimatePresence>
      </div>
      <Panel title="最近的设计" padded={false}>
        <ul>
          {(history.data?.items ?? []).map((h: any) => (
            <li key={h.id}><button onClick={() => { setDesignId(h.id); setDesc(h.prompt) }} className="block w-full border-t border-line px-5 py-3 text-left first:border-t-0 hover:bg-sunken/40"><div className="line-clamp-2 text-[13px]">{h.prompt}</div><div className="mt-0.5 text-[11px] text-faint">{h.candidates?.filter((c: any) => c.saved).length ?? 0} 个已保存</div></button></li>
          ))}
          {!history.data?.items?.length && <li className="px-5 py-8 text-center text-[13px] text-faint">还没有设计过</li>}
        </ul>
      </Panel>
      <Dialog open={save !== null} onOpenChange={(o) => !o && setSave(null)} title="保存到音色库" width={420} footer={<><Button onClick={() => setSave(null)}>取消</Button><Button variant="primary" disabled={!name.trim()} onClick={async () => {
        const v = await api.post(`/studios/${sid}/voice-designs/${designId}/save`, { index: save, name, scope, tags: ['AI设计'] })
        setSave(null)
        invalidate(['design', designId], ['voices'])
        if (charId) {
          await api.put(`/characters/${charId}/voice`, { voiceId: v.id })
          invalidate(['characters', bookId])
          toast.success(`已保存并设为「${char.data?.name}」的声音`)
          nav(`/books/${bookId}/characters`)
        } else toast.success('已保存到「我的」音色')
      }}>保存</Button></>}>
        <div className="space-y-4">
          <Field label="音色名称"><Input autoFocus value={name} onChange={(e) => setName(e.target.value)} /></Field>
          <Field label="保存到"><Segmented size="sm" value={scope} onChange={setScope} items={[{ value: 'user', label: '我的音色' }, { value: 'studio', label: '工作室音色' }]} /></Field>
        </div>
      </Dialog>
    </div>
  )
}

// ---------------- Voice clone ----------------
type CloneOption = { available: boolean; provider: string; platform: string; demo: boolean; minSeconds: number; maxSeconds: number; recommendSeconds: number; expiryDays: number; expiresUnused: boolean; modelLabel?: string }

const secondsText = (s: number) => (s >= 60 && s % 60 === 0 ? `${s / 60} 分钟` : `${s} 秒`)
/** Spaces around a platform name only when it is a Latin word ("由 MiniMax 复刻" / "由千问复刻"). */
const latin = (name: string) => (/^[\x00-\x7f]+$/.test(name) ? ` ${name} ` : name)

function cloneOptionNote(o: CloneOption) {
  if (o.demo) return '开发环境演示：只走一遍流程，不会真正复刻声音（正式环境不显示此选项）'
  const len = o.minSeconds && o.maxSeconds ? ` · 样本 ${o.minSeconds} 秒～${secondsText(o.maxSeconds)}` : ''
  return `由${latin(o.platform)}复刻${o.modelLabel ? `（${o.modelLabel}）` : ''}${len}`
}

function quickCloneText(o: CloneOption | undefined, fallback: string) {
  if (!o?.available || o.demo) return fallback
  if (o.provider === 'minimax') return '录 30 秒到 3 分钟，提交后约半分钟可用。适合试音、角色和日常配音。'
  if (o.maxSeconds && o.maxSeconds <= 60) return `录 10 到 ${o.recommendSeconds || 20} 秒清晰朗读效果最好（最长 ${secondsText(o.maxSeconds)}），提交后约半分钟可用。适合角色和日常配音。`
  return fallback
}

function Clone() {
  const sid = useSession((s) => s.studioId)
  const studio = useStudio()
  const nav = useNavigate()
  const [step, setStep] = useState(0)
  const [tier, setTier] = useState('quick')
  const [consent, setConsent] = useState<'self' | 'other'>('self')
  const [samples, setSamples] = useState<any[]>([])
  const [checking, setChecking] = useState(false)
  const [evidence, setEvidence] = useState('')
  const [doc, setDoc] = useState('')
  const [f, setF] = useState({ name: '', speakerName: '', identity: '', gender: 'unknown', ageGroup: 'adult', denoise: true, agree: false })
  const [busy, setBusy] = useState(false)
  const challenge = useQuery({ queryKey: ['challenge', step], queryFn: () => api.get('/voices/challenge'), enabled: consent === 'self' })
  const optionsQ = useQuery<Record<string, CloneOption>>({ queryKey: ['clone-options', sid], queryFn: () => api.get(`/studios/${sid}/voices/clone-options`), enabled: !!sid })
  const opt = optionsQ.data?.[tier]
  useEffect(() => { // start on a tier that a platform actually offers
    const o = optionsQ.data
    if (o && !o[tier]?.available && o[tier === 'quick' ? 'pro' : 'quick']?.available) setTier(tier === 'quick' ? 'pro' : 'quick')
  }, [optionsQ.data])
  const addSample = async (blob: Blob) => {
    setChecking(true)
    try {
      const r = await api.upload(`/studios/${sid}/voices/sample-check`, blob, { tier }, 'sample.webm')
      setSamples((s) => [...s, r])
    } finally {
      setChecking(false)
    }
  }
  const total = samples.reduce((s, x) => s + (x.check?.speechMs ?? 0), 0)
  const totalDur = samples.reduce((s, x) => s + (x.check?.durationMs ?? 0), 0)
  const need = (opt?.recommendSeconds || (tier === 'pro' ? 30 * 60 : 30)) * 1000
  const minMs = (opt?.minSeconds ?? 0) * 1000
  const maxMs = (opt?.maxSeconds ?? 0) * 1000
  const tooShort = minMs > 0 && totalDur < minMs
  const submit = async () => {
    setBusy(true)
    try {
      await api.post(`/studios/${sid}/voices/clone`, { ...f, tier, consentType: consent, challenge: challenge.data?.sentence, evidenceId: evidence, documentId: doc, sampleIds: samples.map((s) => s.assetId), statement: '我确认拥有该声音的合法使用权，仅用于合法用途。' })
      toast.success(consent === 'self' ? '克隆已开始，完成后会出现在「我的」音色里' : '已提交，授权材料审核通过后开始训练')
      invalidate(['voices'])
      nav('/voices')
    } finally {
      setBusy(false)
    }
  }
  const steps = ['选择方式', '录制样本', '授权验证']
  return (
    <div className="mx-auto max-w-3xl">
      <ol className="mb-6 flex items-center gap-2 text-[13px]">
        {steps.map((s, i) => (
          <li key={s} className="flex items-center gap-2">
            <span className={cn('grid size-6 place-items-center rounded-full text-xs font-semibold', i < step ? 'bg-accent text-accent-ink' : i === step ? 'bg-ink text-bg' : 'bg-sunken text-faint')}>{i < step ? <Check className="size-3.5" /> : i + 1}</span>
            <span className={i === step ? 'font-medium' : 'text-muted'}>{s}</span>
            {i < steps.length - 1 && <span className="mx-1 h-px w-8 bg-line" />}
          </li>
        ))}
      </ol>
      <AnimatePresence mode="wait">
        <motion.div key={step} initial={{ opacity: 0, x: 12 }} animate={{ opacity: 1, x: 0 }} exit={{ opacity: 0, x: -12 }} transition={{ duration: 0.2 }}>
          {step === 0 && (
            <Panel>
              <div className="grid gap-3 sm:grid-cols-2">
                {[['quick', '快速克隆', '录 30 秒到 3 分钟，几分钟内可用。适合试音和非主角。'], ['pro', '专业克隆', '提供 30 分钟以上高质量录音，还原度高，适合长篇主播。']].map(([k, t, d]) => {
                  const o = optionsQ.data?.[k]
                  const off = !!optionsQ.data && !o?.available
                  return (
                    <button key={k} disabled={off} onClick={() => setTier(k)} className={cn('rounded-[12px] border p-4 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-55', tier === k && !off ? 'border-accent bg-accent-soft' : 'border-line enabled:hover:border-line-strong')}>
                      <div className="flex items-center gap-2 text-[14px] font-semibold">{t}{k === 'pro' && studio?.planCode === 'free' && !off && <Badge tone="signal">付费套餐</Badge>}{off && <Badge>暂未开放</Badge>}</div>
                      <p className="mt-1 text-[13px] text-muted">{k === 'quick' ? quickCloneText(o, d) : d}</p>
                      {o?.available && <p className={cn('mt-2 text-[12px]', o.demo ? 'text-warn' : 'text-faint')}>{cloneOptionNote(o)}</p>}
                    </button>
                  )
                })}
              </div>
              <div className="mt-6 text-[13px] font-medium">克隆谁的声音？</div>
              <div className="mt-2 grid gap-3 sm:grid-cols-2">
                {[['self', '我自己的声音', '需要朗读一句随机验证句，确认是本人。'], ['other', '他人的声音', '需要声音主人签署的授权书，由平台人工审核。']].map(([k, t, d]) => (
                  <button key={k} onClick={() => setConsent(k as any)} className={cn('rounded-[12px] border p-4 text-left transition-colors', consent === k ? 'border-accent bg-accent-soft' : 'border-line hover:border-line-strong')}>
                    <div className="text-[14px] font-semibold">{t}</div>
                    <p className="mt-1 text-[13px] text-muted">{d}</p>
                  </button>
                ))}
              </div>
              <div className="mt-6 flex justify-end"><Button variant="primary" disabled={!!optionsQ.data && !opt?.available} onClick={() => setStep(1)}>下一步</Button></div>
            </Panel>
          )}
          {step === 1 && (
            <Panel>
              <div className="rounded-[10px] bg-sunken/60 px-4 py-3 text-[13px] text-muted">
                <div className="mb-1 font-medium text-ink">录音小贴士</div>
                在安静的房间里，离麦克风一拳距离，用平时讲故事的语气自然朗读。避免背景音乐、回声和多人说话。
              </div>
              <div className="mt-5 flex flex-wrap items-center gap-4">
                <Recorder onDone={addSample} />
                <span className="text-xs text-muted">或</span>
                <label className="cursor-pointer">
                  <input type="file" accept="audio/*" multiple className="hidden" onChange={async (e) => { for (const file of Array.from(e.target.files ?? [])) await addSample(file) }} />
                  <span className="inline-flex h-9 items-center gap-1.5 rounded-control border border-line px-3 text-[13px] hover:border-line-strong">上传音频文件</span>
                </label>
                {checking && <span className="text-xs text-muted">正在检测音质…</span>}
              </div>
              <div className="mt-5">
                <div className="flex justify-between text-xs text-muted"><span>有效人声时长</span><span className="tnum">{Math.round(total / 1000)} 秒 / 建议 {need >= 60000 ? `${need / 60000} 分钟` : `${need / 1000} 秒`}</span></div>
                <Progress value={total / need} className="mt-1.5" tone={total >= need ? 'ok' : 'accent'} />
                {samples.length > 0 && tooShort && <p className="mt-2 text-xs text-warn">{latin(opt?.platform ?? '').trimStart()}至少需要 {opt?.minSeconds} 秒的样本，现在约 {Math.round(totalDur / 1000)} 秒，再录一段吧。</p>}
                {maxMs > 0 && totalDur > maxMs && <p className="mt-2 text-xs text-muted">样本超过 {secondsText(maxMs / 1000)}，超出的部分不会用于复刻。</p>}
              </div>
              <ul className="mt-4 space-y-2">
                {samples.map((s, i) => (
                  <li key={s.assetId} className="flex items-start gap-3 rounded-[10px] border border-line px-3 py-2.5">
                    <PlayButton id={'sample:' + s.assetId} url={s.url} size="xs" />
                    <div className="min-w-0 flex-1 text-[12.5px]">
                      <div className="flex items-center gap-2">样本 {i + 1}<Badge tone={s.check.ok ? 'ok' : 'warn'}>音质 {s.check.score} 分</Badge></div>
                      {s.check.tips?.length > 0 && <div className="mt-0.5 text-xs text-muted">{s.check.tips.join('；')}</div>}
                    </div>
                    <IconButton label="移除" size="xs" icon={<Trash2 className="size-3.5" />} onClick={() => setSamples(samples.filter((x) => x !== s))} />
                  </li>
                ))}
              </ul>
              <div className="mt-6 flex justify-between"><Button onClick={() => setStep(0)}>上一步</Button><Button variant="primary" disabled={!samples.length || tooShort} onClick={() => setStep(2)}>下一步</Button></div>
            </Panel>
          )}
          {step === 2 && (
            <Panel>
              <div className="grid gap-4 sm:grid-cols-2">
                <Field label="音色名称"><Input value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} placeholder="例如：我的讲故事声音" /></Field>
                {consent === 'other' && <Field label="声音主人姓名"><Input value={f.speakerName} onChange={(e) => setF({ ...f, speakerName: e.target.value })} /></Field>}
                <Field label="性别"><Select value={f.gender} onChange={(v) => setF({ ...f, gender: v })} options={Object.entries(genderName)} /></Field>
                <Field label="年龄段"><Select value={f.ageGroup} onChange={(v) => setF({ ...f, ageGroup: v })} options={Object.entries(ageName)} /></Field>
              </div>
              {consent === 'self' ? (
                <div className="mt-6 rounded-[12px] border border-line p-5">
                  <div className="flex items-center gap-2 text-[14px] font-semibold"><ShieldCheck className="size-4 text-accent" />本人验证</div>
                  <p className="mt-1 text-[13px] text-muted">请清晰朗读下面这句话，系统会核对内容并比对声纹，确认样本是你本人的声音。</p>
                  <div className="my-4 flex items-center justify-between gap-3 rounded-[10px] bg-sunken px-4 py-3">
                    <p className="text-[17px] font-medium">{challenge.data?.sentence}</p>
                    <Button size="xs" variant="ghost" onClick={() => challenge.refetch()}>换一句</Button>
                  </div>
                  {evidence ? <Badge tone="ok"><Check className="size-3" />已录制</Badge> : (
                    <Recorder onDone={async (b) => { const a = await api.upload(`/studios/${sid}/uploads`, b, { kind: 'voice_sample' }, 'evidence.webm'); setEvidence(a.id) }} />
                  )}
                </div>
              ) : (
                <div className="mt-6 space-y-3">
                  <Field label="声音授权书" hint="需要声音主人签字，写明授权范围和期限。支持 PDF 或图片。">
                    <UploadZone accept=".pdf,image/*" onFiles={async ([file]) => { const a = await api.upload(`/studios/${sid}/uploads`, file, { kind: 'document' }); setDoc(a.id); toast.success('已上传') }}>
                      {doc ? <p className="text-[13px] text-ok">授权书已上传</p> : undefined}
                    </UploadZone>
                  </Field>
                  <Field label="身份证号（仅用于核验，加密保存）"><Input value={f.identity} onChange={(e) => setF({ ...f, identity: e.target.value })} /></Field>
                </div>
              )}
              <div className="mt-5 space-y-2.5">
                <Switch checked={f.denoise} onChange={(v) => setF({ ...f, denoise: v })} label={opt?.provider === 'minimax' || (opt?.provider === 'qwen' && opt.modelLabel) ? `训练前自动降噪（由${latin(opt.platform)}处理）` : '训练前自动降噪'} />
                {opt?.expiresUnused && <p className="text-[12px] text-muted">{latin(opt.platform).trimStart()}的复刻音色需要在 {opt.expiryDays} 天内用来生成一次配音，之后长期有效；一直不用会被平台自动删除。</p>}
                {opt?.modelLabel && !opt.demo && <p className="text-[12px] text-muted">复刻出的音色绑定 {opt.modelLabel} 模型，配音时会自动使用它，长期有效。</p>}
                <Checkbox checked={f.agree} onChange={(v) => setF({ ...f, agree: v })} label={<span className="text-[13px] text-muted">我确认拥有该声音的合法使用权，不会用于冒充他人、诈骗等违法用途；因声音授权产生的纠纷由我自行承担，与本平台无关。平台会在生成的音频中加入不可见的标识。<a href="/legal/ai" target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()} className="ml-1 text-accent hover:underline">《AI 生成内容与声音使用规范》</a></span>} />
              </div>
              <div className="mt-6 flex justify-between"><Button onClick={() => setStep(1)}>上一步</Button><Button variant="primary" loading={busy} disabled={!f.agree || (consent === 'self' ? !evidence : !doc || !f.speakerName)} onClick={submit} icon={<Mic className="size-4" />}>开始克隆</Button></div>
            </Panel>
          )}
        </motion.div>
      </AnimatePresence>
    </div>
  )
}

function Earnings() {
  const { data, refetch } = useQuery({ queryKey: ['earnings'], queryFn: () => api.get('/me/earnings') })
  const [w, setW] = useState(false)
  const [f, setF] = useState({ amount: 0, account: '' })
  return (
    <div className="space-y-6">
      <Panel>
        <div className="flex flex-wrap items-center gap-8">
          <div><div className="text-xs text-muted">累计收益</div><div className="mt-1 text-[24px] font-semibold tnum">{num(data?.income)} <span className="text-[13px] font-normal text-muted">积分</span></div></div>
          <Button variant="primary" className="ml-auto" disabled={!data?.income} onClick={() => setW(true)}>申请提现</Button>
        </div>
      </Panel>
      <Panel title="上架的音色" padded={false}>
        {!data?.listings?.length ? <Empty icon={<Store />} title="还没有上架音色" description="在「我的」音色里打开一个克隆或设计的音色，点击「上架到市场」。" /> : (
          <ul>{data.listings.map((l: any) => <li key={l.listing?.id ?? l.id} className="flex items-center gap-3 border-t border-line px-5 py-3 first:border-t-0 text-[13px]"><span className="flex-1 font-medium">{l.voiceName ?? l.voice?.name}</span><Badge tone={l.listing?.status === 'listed' ? 'ok' : 'warn'}>{l.listing?.status === 'listed' ? '已上架' : l.listing?.status === 'pending' ? '审核中' : l.listing?.status}</Badge><span className="tnum text-muted">{num(l.listing?.pricePer1k)} 积分/千字</span></li>)}</ul>
        )}
      </Panel>
      <Panel title="提现记录" padded={false}>
        {!data?.withdrawals?.length ? <Empty title="没有提现记录" className="py-8" /> : (
          <ul>{data.withdrawals.map((x: any) => <li key={x.id} className="flex items-center gap-3 border-t border-line px-5 py-3 first:border-t-0 text-[13px]"><span className="flex-1 tnum">{num(x.amount)} 积分</span><span className="text-muted">{x.account}</span><Badge>{x.status}</Badge></li>)}</ul>
        )}
      </Panel>
      <Dialog open={w} onOpenChange={setW} title="申请提现" width={420} footer={<><Button onClick={() => setW(false)}>取消</Button><Button variant="primary" onClick={async () => { await api.post('/me/withdrawals', { amount: f.amount, account: f.account }); toast.success('已提交，平台审核后打款'); setW(false); refetch() }}>提交</Button></>}>
        <div className="space-y-4">
          <Field label="提现积分" hint="按平台公布的比例折算为现金"><Input type="number" value={f.amount} onChange={(e) => setF({ ...f, amount: Number(e.target.value) })} /></Field>
          <Field label="收款账户" hint="支付宝账号或银行卡号"><Input value={f.account} onChange={(e) => setF({ ...f, account: e.target.value })} /></Field>
        </div>
      </Dialog>
    </div>
  )
}
