import { AnimatePresence, motion } from 'framer-motion'
import { Combine, ImagePlus, MoreHorizontal, Plus, Sparkles, Trash2, UserRound, Users, Wand2 } from '@/components/icons'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { ageName, genderName, kindName, num } from '@/lib/format'
import { useRunningJobs } from '@/lib/live'
import { usePlayer } from '@/lib/player'
import { ModelBadge, Voice, VoicePicker, VoicePlay } from '@/components/voice'
import { Avatar, Badge, Button, Chip, cn, confirm, Dialog, Empty, Field, IconButton, Input, Menu, Panel, Segmented, Select, Sheet, Skeleton, Slider, Switch, Term, Textarea } from '@/components/ui'
import { can, invalidate, useCharacters, useMembers } from '../../hooks'

export default function Characters({ book }: { book: any }) {
  const { data, isLoading } = useCharacters(book.id)
  const [filter, setFilter] = useState('all')
  const [busy, setBusy] = useState('')
  const [edit, setEdit] = useState<any>(null)
  const [pick, setPick] = useState<any>(null)
  const [add, setAdd] = useState(false)
  const analyzing = useRunningJobs((j) => j.bookId === book.id && j.type === 'character_analysis')
  const casting = can(book, 'casting')
  const list = (data ?? []).filter((c) => (filter === 'all' ? !c.candidate : filter === 'uncast' ? !c.candidate && !c.voiceId && c.mode !== 'human' : filter === 'human' ? c.mode === 'human' : c.candidate))
  const uncast = (data ?? []).filter((c) => !c.candidate && !c.voiceId && c.mode !== 'human').length
  const candidates = (data ?? []).filter((c) => c.candidate).length

  const analyze = async () => {
    setBusy('analyze')
    try {
      await api.post(`/books/${book.id}/characters/analyze`, {})
      toast.success('AI 正在分析角色，大约需要 1 分钟')
    } finally {
      setBusy('')
    }
  }
  const autoCast = async () => {
    setBusy('cast')
    try {
      await api.post(`/books/${book.id}/casting`, { apply: true, characterIds: (data ?? []).filter((c) => !c.voiceId && !c.candidate && c.mode !== 'human').map((c) => c.id) })
      invalidate(['characters', book.id], ['book', book.id])
      toast.success('AI 已为未选角的角色挑选了声音，可以逐个试听调整')
    } finally {
      setBusy('')
    }
  }
  const setVoice = async (c: any, v: Voice) => {
    if (c.voiceId && c.voiceId !== v.id) {
      const impact = await api.get(`/characters/${c.id}/recast-impact?voiceId=${v.id}`)
      if (impact.segments > 0 && !(await confirm({ title: `更换「${c.name}」的声音？`, description: `已经生成的 ${num(impact.segments)} 句（${num(impact.chars)} 字）需要重新配音，预计消耗 ${num(impact.credits)} 积分。旧音频会保留为历史版本。`, confirmText: '更换' }))) return
    }
    const r = await api.put(`/characters/${c.id}/voice`, { voiceId: v.id })
    setPick(null)
    invalidate(['characters', book.id], ['book', book.id])
    toast.success(v.id ? `已为「${c.name}」选择「${v.name}」` + (r.staleSegments ? `，${r.staleSegments} 句待重新生成` : '') : '已清除声音')
  }

  if (isLoading) return <div className="space-y-2">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-20" />)}</div>
  return (
    <div>
      <div className="mb-4 flex flex-wrap items-center gap-2">
        <Segmented
          value={filter}
          onChange={setFilter}
          items={[
            { value: 'all', label: `全部 ${(data ?? []).length - candidates}` },
            { value: 'uncast', label: `未选角 ${uncast}` },
            { value: 'human', label: '真人配音' },
            ...(candidates ? [{ value: 'candidate', label: `候选 ${candidates}` }] : []),
          ]}
          size="sm"
        />
        <Term id="选角" />
        <div className="ml-auto flex gap-2">
          {casting && <Button icon={<Sparkles className="size-4" />} loading={busy === 'analyze' || analyzing.length > 0} onClick={analyze}>{data?.length && data.length > 1 ? '重新分析角色' : 'AI 分析角色'}</Button>}
          {casting && uncast > 0 && <Button variant="primary" icon={<Wand2 className="size-4" />} loading={busy === 'cast'} onClick={autoCast}>AI 一键选角</Button>}
          {casting && <IconButton label="添加角色" variant="secondary" size="md" icon={<Plus className="size-4" />} onClick={() => setAdd(true)} />}
        </div>
      </div>
      {analyzing.length > 0 && (
        <div className="mb-4 flex items-center gap-3 rounded-panel border border-signal/40 bg-signal-soft px-4 py-3 text-[13px]">
          <span className="eq flex h-3 items-end text-warn"><span /><span /><span /></span>
          {analyzing[0].progressText || 'AI 正在阅读书稿、识别角色……'}
        </div>
      )}
      {!list.length ? (
        <Panel padded={false}>
          <Empty icon={<Users />} title={filter === 'all' ? '还没有角色' : '这里没有角色'} description={filter === 'all' ? '让 AI 读一遍书稿，它会找出所有会说话的角色并写好人物小传。' : undefined} action={filter === 'all' && casting && <Button variant="primary" icon={<Sparkles className="size-4" />} onClick={analyze}>AI 分析角色</Button>} />
        </Panel>
      ) : (
        <div className="grid gap-3 md:grid-cols-2">
          <AnimatePresence initial={false}>
            {list.map((c) => (
              <motion.div key={c.id} layout initial={{ opacity: 0, scale: 0.98 }} animate={{ opacity: 1, scale: 1 }} exit={{ opacity: 0 }} className="rounded-panel border border-line bg-surface p-4">
                <div className="flex items-start gap-3">
                  <Avatar name={c.name} src={c.avatarUrl} color={c.color} size={40} />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <button onClick={() => setEdit(c)} className="truncate text-[14px] font-semibold hover:text-accent">{c.name}</button>
                      <Badge>{kindName[c.kind] ?? c.kind}</Badge>
                      {c.claimStatus === 'pending' && <Badge tone="warn">有人申请认领</Badge>}
                    </div>
                    <p className="mt-0.5 text-xs text-muted">{[genderName[c.gender], ageName[c.ageGroup], c.identity].filter((x) => x && x !== '未知').join(' · ') || '—'} · {c.lineCount} 句 · {num(c.charCount)} 字</p>
                    {c.profile && <p className="mt-1.5 line-clamp-2 text-[12.5px] leading-relaxed text-muted">{c.profile}</p>}
                  </div>
                  <CharMenu book={book} c={c} all={data ?? []} onEdit={() => setEdit(c)} />
                </div>
                <div className="mt-3 flex items-center gap-2 rounded-[10px] bg-sunken/60 px-3 py-2">
                  {c.mode === 'human' ? (
                    <>
                      <UserRound className="size-4 text-muted" />
                      <span className="flex-1 truncate text-[13px]">{c.assignee ? `真人配音：${c.assignee.nickname}` : '真人配音，待指派'}</span>
                    </>
                  ) : c.voice ? (
                    <>
                      <VoicePlay v={c.voice} size="xs" />
                      <span className="flex min-w-0 flex-1 items-center gap-1.5 truncate text-[13px]">{c.voice.name}<ModelBadge v={c.voice} /><span className="truncate text-xs text-faint">{(c.voice.tags ?? []).slice(0, 2).join(' · ')}</span></span>
                    </>
                  ) : (
                    <span className="flex-1 text-[13px] text-warn">还没有选择声音</span>
                  )}
                  {casting && (
                    <>
                      <Audition c={c} />
                      <Button size="xs" variant={c.voiceId || c.mode === 'human' ? 'secondary' : 'primary'} onClick={() => setPick(c)}>{c.voiceId ? '更换' : '选择声音'}</Button>
                    </>
                  )}
                  {!casting && c.mode === 'human' && !c.assigneeId && c.claimStatus !== 'pending' && can(book, 'record') && (
                    <Button size="xs" onClick={async () => { await api.post(`/characters/${c.id}/claim`); toast.success('已提交认领申请'); invalidate(['characters', book.id]) }}>认领角色</Button>
                  )}
                </div>
                {casting && c.claimStatus === 'pending' && (
                  <div className="mt-2 flex items-center justify-end gap-2 text-[12.5px]">
                    <span className="mr-auto text-muted">有成员申请为这个角色配音</span>
                    <Button size="xs" onClick={async () => { await api.post(`/characters/${c.id}/claim/review`, { approve: false }); invalidate(['characters', book.id]) }}>拒绝</Button>
                    <Button size="xs" variant="primary" onClick={async () => { await api.post(`/characters/${c.id}/claim/review`, { approve: true }); invalidate(['characters', book.id]) }}>同意</Button>
                  </div>
                )}
              </motion.div>
            ))}
          </AnimatePresence>
        </div>
      )}
      <VoicePicker open={!!pick} onOpenChange={(v) => !v && setPick(null)} title={pick ? `为「${pick.name}」选择声音` : ''} current={pick?.voiceId} suggestions={pick?.casting?.map((s: any) => ({ ...s, voice: s.voice }))} gender={pick?.gender} onPick={(v) => setVoice(pick, v)} />
      <EditCharacter book={book} c={edit} onClose={() => setEdit(null)} />
      <AddCharacter book={book} open={add} onOpenChange={setAdd} />
    </div>
  )
}

function Audition({ c }: { c: any }) {
  const [busy, setBusy] = useState(false)
  if (!c.voiceId) return null
  return (
    <Button size="xs" variant="ghost" loading={busy} onClick={async () => {
      setBusy(true)
      try {
        const r = await api.post(`/characters/${c.id}/audition`, { voiceId: c.voiceId })
        usePlayer.getState().play('aud:' + c.id, r.url, `${c.name}：${r.text}`)
      } finally {
        setBusy(false)
      }
    }}>用台词试听</Button>
  )
}

function CharMenu({ book, c, all, onEdit }: { book: any; c: any; all: any[]; onEdit: () => void }) {
  const [merge, setMerge] = useState(false)
  const [into, setInto] = useState('')
  if (!can(book, 'casting')) return null
  return (
    <>
      <Menu
        trigger={<IconButton label="更多" size="xs" icon={<MoreHorizontal className="size-4" />} />}
        items={[
          { label: '编辑角色', onSelect: onEdit },
          c.kind !== 'narrator' && { label: '合并到其他角色', icon: <Combine />, onSelect: () => setMerge(true) },
          { label: c.candidate ? '设为正式角色' : '设为候选（不配音）', onSelect: async () => { await api.patch(`/characters/${c.id}`, { candidate: !c.candidate }); invalidate(['characters', book.id]) } },
          { label: 'AI 生成头像', icon: <ImagePlus />, onSelect: async () => { await api.post(`/characters/${c.id}/avatar/generate`); toast.success('正在生成头像') } },
          c.kind !== 'narrator' && 'sep',
          c.kind !== 'narrator' && {
            label: '删除角色',
            icon: <Trash2 />,
            danger: true,
            onSelect: async () => {
              if (await confirm({ title: `删除「${c.name}」？`, description: '这个角色的台词会改由旁白朗读。', danger: true, confirmText: '删除' })) {
                await api.del(`/characters/${c.id}`)
                invalidate(['characters', book.id])
              }
            },
          },
        ]}
      />
      <Dialog open={merge} onOpenChange={setMerge} title={`把「${c.name}」合并到…`} description="常用于同一个人物被识别成了两个名字（比如本名和外号）。台词会全部归到目标角色。" width={440} footer={<><Button onClick={() => setMerge(false)}>取消</Button><Button variant="primary" disabled={!into} onClick={async () => { await api.post(`/characters/${c.id}/merge`, { intoId: into }); setMerge(false); invalidate(['characters', book.id]); toast.success('已合并') }}>合并</Button></>}>
        <Select value={into} onChange={setInto} placeholder="选择目标角色" options={all.filter((x) => x.id !== c.id).map((x) => [x.id, x.name] as [string, string])} />
      </Dialog>
    </>
  )
}

function EditCharacter({ book, c, onClose }: { book: any; c: any; onClose: () => void }) {
  const members = useMembers()
  const [f, setF] = useState<any>(null)
  const [busy, setBusy] = useState(false)
  const [desc, setDesc] = useState(false)
  const nav = useNavigate()
  if (c && (!f || f.id !== c.id)) setTimeout(() => setF({ ...c, aliasesText: (c.aliases ?? []).join('、'), speed: c.params?.speed ?? 1, pitch: c.params?.pitch ?? 0 }))
  const set = (k: string, v: any) => setF({ ...f, [k]: v })
  const save = async () => {
    setBusy(true)
    try {
      await api.patch(`/characters/${c.id}`, {
        name: f.name, aliases: f.aliasesText.split(/[、,，\s]+/).filter(Boolean), kind: f.kind, gender: f.gender, ageGroup: f.ageGroup, identity: f.identity, personality: f.personality, voiceTraits: f.voiceTraits, profile: f.profile,
        params: { speed: f.speed, pitch: f.pitch }, color: f.color, mode: f.mode, assigneeId: f.mode === 'human' ? f.assigneeId : '', useCrowdPool: f.useCrowdPool,
      })
      invalidate(['characters', book.id])
      toast.success('已保存')
      onClose()
    } finally {
      setBusy(false)
    }
  }
  return (
    <Sheet open={!!c} onOpenChange={(v) => !v && onClose()} title={c?.name} width={520} footer={<><Button onClick={onClose}>取消</Button><Button variant="primary" loading={busy} onClick={save}>保存</Button></>}>
      {f && (
        <div className="space-y-5">
          <div className="grid grid-cols-2 gap-4">
            <Field label="名字"><Input value={f.name} onChange={(e) => set('name', e.target.value)} /></Field>
            <Field label="别名" hint="用顿号分隔"><Input value={f.aliasesText} onChange={(e) => set('aliasesText', e.target.value)} /></Field>
            <Field label="类型"><Select value={f.kind} onChange={(v) => set('kind', v)} options={Object.entries(kindName)} /></Field>
            <Field label="性别"><Select value={f.gender} onChange={(v) => set('gender', v)} options={Object.entries(genderName)} /></Field>
            <Field label="年龄段"><Select value={f.ageGroup} onChange={(v) => set('ageGroup', v)} options={Object.entries(ageName)} /></Field>
            <Field label="身份"><Input value={f.identity} onChange={(e) => set('identity', e.target.value)} placeholder="如：贾府千金" /></Field>
          </div>
          <Field label="性格"><Input value={f.personality} onChange={(e) => set('personality', e.target.value)} /></Field>
          <Field label="声音特点" hint="AI 选角和设计音色时会参考这里">
            <div className="flex gap-2">
              <Input value={f.voiceTraits} onChange={(e) => set('voiceTraits', e.target.value)} placeholder="如：清亮柔弱，语速偏慢" />
              <Button size="md" loading={desc} onClick={async () => {
                setDesc(true)
                try {
                  const r = await api.post(`/characters/${c.id}/describe-voice`)
                  set('voiceTraits', r.description)
                } finally {
                  setDesc(false)
                }
              }}>AI 描述</Button>
            </div>
          </Field>
          <Field label="人物小传"><Textarea rows={4} value={f.profile} onChange={(e) => set('profile', e.target.value)} /></Field>
          <Field label="配音方式">
            <Segmented value={f.mode || 'ai'} onChange={(v) => set('mode', v)} items={[{ value: 'ai', label: 'AI 音色' }, { value: 'human', label: '真人配音' }]} size="sm" />
          </Field>
          {f.mode === 'human' ? (
            <Field label="配音演员" hint="被指派的成员会在「我的台词」里看到这个角色的所有台词。">
              <Select value={f.assigneeId || ''} onChange={(v) => set('assigneeId', v)} placeholder="选择工作室成员" options={(members.data ?? []).map((m: any) => [m.userId, m.nickname] as [string, string])} />
            </Field>
          ) : (
            <>
              <Field label={`语速 ${f.speed.toFixed(2)}×`}><Slider value={f.speed} min={0.7} max={1.4} step={0.05} onChange={(v) => set('speed', v)} /></Field>
              <Field label={`音高 ${f.pitch > 0 ? '+' : ''}${f.pitch}`}><Slider value={f.pitch} min={-6} max={6} step={1} onChange={(v) => set('pitch', v)} /></Field>
              {c.kind === 'crowd' && <Switch checked={f.useCrowdPool} onChange={(v) => set('useCrowdPool', v)} label="群演自动从路人音色池随机分配" />}
            </>
          )}
          <Field label="标识颜色">
            <div className="flex gap-2">
              {['#64748b', '#ec4899', '#0ea5e9', '#f59e0b', '#10b981', '#8b5cf6', '#ef4444', '#14b8a6', '#a16207'].map((col) => (
                <button key={col} onClick={() => set('color', col)} className={cn('size-6 rounded-full ring-offset-2 ring-offset-surface transition', f.color === col && 'ring-2 ring-ink')} style={{ background: col }} />
              ))}
            </div>
          </Field>
          <Button variant="link" size="sm" onClick={() => nav(`/voices/design?character=${c.id}&book=${book.id}`)}>没有合适的声音？用 AI 为这个角色设计一个 →</Button>
        </div>
      )}
    </Sheet>
  )
}

function AddCharacter({ book, open, onOpenChange }: { book: any; open: boolean; onOpenChange: (v: boolean) => void }) {
  const [f, setF] = useState({ name: '', gender: 'unknown', ageGroup: 'adult', kind: 'supporting' })
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="添加角色" width={460} footer={<><Button onClick={() => onOpenChange(false)}>取消</Button><Button variant="primary" disabled={!f.name.trim()} onClick={async () => { await api.post(`/books/${book.id}/characters`, f); invalidate(['characters', book.id]); onOpenChange(false); setF({ ...f, name: '' }) }}>添加</Button></>}>
      <div className="grid grid-cols-2 gap-4">
        <Field label="名字" className="col-span-2"><Input autoFocus value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} /></Field>
        <Field label="类型"><Select value={f.kind} onChange={(v) => setF({ ...f, kind: v })} options={Object.entries(kindName).filter(([k]) => k !== 'narrator')} /></Field>
        <Field label="性别"><Select value={f.gender} onChange={(v) => setF({ ...f, gender: v })} options={Object.entries(genderName)} /></Field>
      </div>
    </Dialog>
  )
}

export { Chip }
