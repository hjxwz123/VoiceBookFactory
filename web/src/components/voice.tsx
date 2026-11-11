import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, ChevronDown, Heart, Search } from '@/components/icons'
import { useState } from 'react'
import { api, qs } from '@/lib/api'
import { ageName, genderName, tierName } from '@/lib/format'
import { useSession } from '@/lib/session'
import { PlayButton } from './audio'
import { Badge, Button, Chip, cn, Empty, Input, Menu, Pagination, Segmented, Select, Sheet, Skeleton } from './ui'

export type Voice = {
  id: string; name: string; gender: string; ageGroup: string; priceTier: string; provider: string; tags: string[]; description: string
  sampleUrl: string; status: string; scope: string; favorite?: boolean; recommended?: boolean; origin?: string; usageCount?: number; emotions?: string[]
  expiresAt?: string | null; statusNote?: string; shared?: boolean; listed?: boolean; cloneTier?: string; ownerUserId?: string; studioId?: string; designPrompt?: string
  model?: string; modelLabel?: string; capabilities?: any; language?: string
  /** other models offering the same platform voice (only when listed with group=1) */
  versions?: Voice[]
}

/** The version of a grouped voice that should be shown: the one currently chosen, else the default. */
export function shownVersion(v: Voice, current?: string): Voice {
  const hit = current && v.versions?.find((x) => x.id === current)
  return hit ? { ...hit, versions: v.versions, favorite: v.favorite } : v
}

/** Synthesis model families — every voice belongs to exactly one model. */
export const voiceModels: [string, string][] = [
  ['', '全部模型'],
  ['qwen-audio-3.1-tts-plus', 'Qwen-Audio 3.1 Plus'],
  ['qwen-audio-3.1-tts-flash', 'Qwen-Audio 3.1 Flash'],
  ['qwen-audio-3.0-tts-plus', 'Qwen-Audio 3.0 Plus'],
  ['qwen-audio-3.0-tts-flash', 'Qwen-Audio 3.0 Flash'],
  ['cosyvoice-v3-plus', 'CosyVoice v3 Plus'],
  ['cosyvoice-v3-flash', 'CosyVoice v3 Flash'],
  ['cosyvoice-v2', 'CosyVoice v2'],
  ['speech-2.8-hd', 'MiniMax 2.8 HD'],
  ['speech-2.8-turbo', 'MiniMax 2.8 Turbo'],
  ['placeholder', '内置演示引擎'],
]

/**
 * Plays a voice's preview. Platform voices without an official preview get one generated on first play
 * (free, then shared by everyone).
 */
export function VoicePlay({ v, size = 'sm', className }: { v: Pick<Voice, 'id' | 'name' | 'sampleUrl'> & { scope?: string }; size?: 'xs' | 'sm' | 'md'; className?: string }) {
  const sid = useSession((s) => s.studioId)
  const qc = useQueryClient()
  const resolve = !v.sampleUrl && sid ? async () => {
    const r = await api.post(`/studios/${sid}/voices/${v.id}/preview`, {})
    qc.invalidateQueries({ queryKey: ['voices'] })
    return r.url as string
  } : undefined
  return <PlayButton id={'voice:' + v.id} url={v.sampleUrl} title={v.name} size={size} className={className} resolve={resolve} resolveLabel="试听（首次播放时生成）" />
}

export function ModelBadge({ v }: { v: { model?: string; modelLabel?: string; provider?: string } }) {
  if (v.provider === 'placeholder') return <Badge>演示</Badge>
  if (!v.model) return null
  return <span className="shrink-0 rounded-md border border-line px-1.5 py-px text-[10.5px] text-muted">{v.modelLabel || v.model}</span>
}

/** Switch between the models a platform voice is offered on (same speaker, different quality and price). */
function VersionMenu({ v, onVersion }: { v: Voice; onVersion: (x: Voice) => void }) {
  return (
    <span onClick={(e) => e.stopPropagation()} className="shrink-0">
      <Menu
        align="start"
        trigger={
          <button className="inline-flex items-center gap-0.5 rounded-md border border-line px-1.5 py-px text-[10.5px] text-muted transition-colors hover:border-line-strong hover:text-ink" title="同一个声音在不同合成模型上的版本，音质和价格不同">
            {v.modelLabel || v.model}<span className="text-faint">· {v.versions!.length} 个版本</span><ChevronDown className="size-3" />
          </button>
        }
        items={v.versions!.map((x) => ({
          label: <span className="flex w-56 items-center justify-between gap-3"><span>{x.modelLabel || x.model}</span><span className="text-[11px] text-faint">{tierName[x.priceTier]}</span></span>,
          icon: x.id === v.id ? <Check className="text-accent" /> : <span className="size-4" />,
          onSelect: () => onVersion({ ...x, versions: v.versions, favorite: v.favorite }),
        }))}
      />
    </span>
  )
}

const known = (x?: string) => (x && x !== 'unknown' ? x : '')

export function VoiceRow({ v, selected, onSelect, onVersion, extra, reason }: { v: Voice; selected?: boolean; onSelect?: () => void; onVersion?: (x: Voice) => void; extra?: React.ReactNode; reason?: string }) {
  return (
    <div onClick={onSelect} className={cn('flex items-center gap-3 rounded-[10px] border px-3 py-2.5 shadow-[var(--shadow-card)] transition-[border-color,background-color,box-shadow] duration-200', onSelect && 'cursor-pointer', selected ? 'border-accent bg-accent-soft' : 'border-line bg-surface hover:border-line-strong hover:shadow-[var(--shadow-lift)]')}>
      <VoicePlay v={v} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-[13.5px] font-medium">{v.name}</span>
          {v.priceTier && <Badge tone={v.priceTier === 'premium' ? 'signal' : v.priceTier === 'economy' ? 'neutral' : 'accent'}>{tierName[v.priceTier]}</Badge>}
          {onVersion && (v.versions?.length ?? 0) > 1 ? <VersionMenu v={v} onVersion={onVersion} /> : <ModelBadge v={v} />}
          {v.status !== 'ready' && <Badge tone="warn">{v.status === 'expired' ? '已过期' : v.status === 'pending' ? '审核中' : v.status}</Badge>}
        </div>
        <div className="mt-0.5 truncate text-xs text-muted">
          {[genderName[known(v.gender)], ageName[known(v.ageGroup)], ...(v.tags ?? []).slice(0, 4)].filter(Boolean).join(' · ')}
        </div>
        {reason && <div className="mt-0.5 truncate text-xs text-accent">{reason}</div>}
      </div>
      {extra}
      {selected && <Check className="size-4 shrink-0 text-accent" />}
    </div>
  )
}

const tabs = [
  { value: 'system', label: '平台音色' },
  { value: 'studio', label: '工作室' },
  { value: 'mine', label: '我的' },
  { value: 'favorites', label: '收藏' },
]

/** Sheet for choosing a voice; `suggestions` (from AI casting) show on top. */
export function VoicePicker({ open, onOpenChange, onPick, current, title = '选择音色', suggestions, gender: g0 = '' }: { open: boolean; onOpenChange: (v: boolean) => void; onPick: (v: Voice) => void; current?: string; title?: string; suggestions?: { voice?: Voice; voiceId: string; reason: string; score: number }[]; gender?: string }) {
  const sid = useSession((s) => s.studioId)
  const [tab, setTab] = useState('system')
  const [q, setQ] = useState('')
  const [gender, setGender] = useState(g0 === 'unknown' ? '' : g0)
  const [age, setAge] = useState('')
  const [tier, setTier] = useState('')
  const [vmodel, setVModel] = useState('')
  const [series, setSeries] = useState('')
  const [page, setPage] = useState(1)
  const { data, isLoading } = useQuery({
    queryKey: ['voices', sid, tab, q, gender, age, tier, vmodel, series, page],
    queryFn: () => api.get(`/studios/${sid}/voices${qs({ tab, q, gender, age, tier, model: vmodel, series, page, pageSize: 30, group: 1 })}`),
    enabled: open && !!sid,
    placeholderData: (p) => p,
  })
  const sugg = (suggestions ?? []).filter((s) => s.voice)
  return (
    <Sheet open={open} onOpenChange={onOpenChange} title={title} width={560}>
      {sugg.length > 0 && (
        <div className="mb-6">
          <div className="mb-2 text-xs font-medium text-muted">AI 推荐</div>
          <div className="space-y-2">
            {sugg.map((s) => (
              <VoiceRow key={s.voiceId} v={s.voice!} reason={s.reason} selected={current === s.voiceId} onSelect={() => onPick(s.voice!)} />
            ))}
          </div>
        </div>
      )}
      <Segmented value={tab} onChange={(v) => { setTab(v); setPage(1) }} items={tabs} size="sm" />
      <div className="mt-3 flex gap-2">
        <Input icon={<Search />} placeholder="搜索名称、标签或描述" value={q} onChange={(e) => { setQ(e.target.value); setPage(1) }} />
      </div>
      <div className="mt-3 flex flex-wrap gap-1.5">
        {[['', '不限'], ['female', '女声'], ['male', '男声']].map(([v, l]) => <Chip key={v} active={gender === v} onClick={() => { setGender(v); setPage(1) }}>{l}</Chip>)}
        <span className="mx-1 w-px bg-line" />
        <Select size="sm" value={age} onChange={(v) => { setAge(v); setPage(1) }} options={[['', '全部年龄'], ...Object.entries(ageName).filter(([k]) => k !== 'unknown')]} className="w-28" />
        <Select size="sm" value={tier} onChange={(v) => { setTier(v); setPage(1) }} options={[['', '全部价位'], ...Object.entries(tierName)]} className="w-28" />
        <Select size="sm" value={vmodel} onChange={(v) => { setVModel(v); setPage(1) }} options={voiceModels} className="w-40" />
        {tab === 'system' && <Select size="sm" value={series} onChange={(v) => { setSeries(v); setPage(1) }} options={[['', '全部系列'], ['system', '精选系统音色'], ['base', '基础音色库']]} className="w-32" />}
      </div>
      <div className="mt-4 space-y-2">
        {isLoading ? (
          Array.from({ length: 6 }, (_, i) => <Skeleton key={i} className="h-14" />)
        ) : !data?.items?.length ? (
          <Empty title="没有找到音色" description={tab === 'mine' ? '可以在「音色库」里设计或克隆一个专属音色。' : '换个筛选条件试试。'} />
        ) : (
          data.items.map((g: Voice) => {
            const v = shownVersion(g, current)
            return <VoiceRow key={g.id} v={v} selected={current === v.id} onSelect={() => onPick(v)} onVersion={onPick} extra={v.favorite ? <Heart className="size-3.5 fill-danger text-danger" /> : null} />
          })
        )}
      </div>
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
      {current && (
        <div className="mt-4 border-t border-line pt-4">
          <Button variant="ghost" size="sm" onClick={() => onPick({ id: '' } as Voice)}>清除选择</Button>
        </div>
      )}
    </Sheet>
  )
}
