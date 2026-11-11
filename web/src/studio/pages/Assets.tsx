import { useQuery } from '@tanstack/react-query'
import { Heart, Image, Music, Search, Sparkles, Trash2, Upload, Wand2 } from '@/components/icons'
import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { toast } from 'sonner'
import { api, qs } from '@/lib/api'
import { bytes, duration, fromNow } from '@/lib/format'
import { useSession } from '@/lib/session'
import { assetKindName, LibraryCategoryChips, LibraryCredit } from '@/components/asset'
import { CopyrightNotice } from '@/components/legal'
import { PlayButton, UploadZone } from '@/components/audio'
import { Badge, Button, Checkbox, Chip, cn, confirm, Dialog, Empty, Fade, Field, Input, PageHeader, Pagination, Segmented, Select, Sheet, Skeleton, Slider, Tabs, Textarea } from '@/components/ui'
import { invalidate } from '../hooks'
import Templates from './Templates'

export default function Assets() {
  const [params, setParams] = useSearchParams()
  const tab = params.get('tab') === 'templates' ? 'templates' : 'library'
  const setTab = (v: string) => setParams(v === 'templates' ? { tab: v } : {}, { replace: true })
  const [gen, setGen] = useState(false)
  const [up, setUp] = useState(false)
  return (
    <div>
      <PageHeader
        title="资产库"
        description={tab === "library" ? "背景音乐、音效、环境音和图片。可以使用平台收录的素材，也可以上传自己的或用 AI 生成。" : "每一集开头和结尾自动加上的固定段落。"}
        actions={tab === 'library' && <><Button icon={<Upload className="size-4" />} onClick={() => setUp(true)}>上传</Button><Button variant="primary" icon={<Sparkles className="size-4" />} onClick={() => setGen(true)}>AI 生成</Button></>}
      />
      <CopyrightNotice variant="banner" className="mb-5" what={tab === 'library' ? '素材' : '片头片尾里的配乐和素材'} />
      <Tabs className="mb-6" value={tab} onChange={setTab} items={[{ value: 'library', label: '素材' }, { value: 'templates', label: '片头片尾' }]} />
      <Fade k={tab}>{tab === 'library' ? <Library /> : <Templates />}</Fade>
      <GenerateDialog open={gen} onOpenChange={setGen} />
      <UploadDialog open={up} onOpenChange={setUp} />
    </div>
  )
}

function Library() {
  const sid = useSession((s) => s.studioId)
  const [kind, setKind] = useState('bgm')
  const [scope, setScope] = useState('')
  const [q, setQ] = useState('')
  const [tag, setTag] = useState('')
  const [page, setPage] = useState(1)
  const [open, setOpen] = useState<any>(null)
  const { data, isLoading } = useQuery({ queryKey: ['assets', sid, kind, scope, q, tag, page], queryFn: () => api.get(`/studios/${sid}/assets${qs({ kind, scope, q, tag: kind === 'image' ? '' : tag, page, pageSize: 30 })}`), placeholderData: (p) => p })
  return (
    <div>
      <div className="mb-5 flex flex-wrap items-center gap-3">
        <Segmented value={kind} onChange={(v) => { setKind(v); setTag(''); setPage(1) }} items={['bgm', 'sfx', 'ambience', 'image'].map((k) => ({ value: k, label: assetKindName[k] }))} />
        <Select size="sm" value={scope} onChange={(v) => { setScope(v); setPage(1) }} options={[['', '全部来源'], ['official', '平台官方'], ['studio', '工作室'], ['mine', '我上传的'], ['favorites', '收藏']]} className="w-32" />
        <div className="ml-auto w-full max-w-xs"><Input icon={<Search />} placeholder="搜索：紧张、雨声、古风……" value={q} onChange={(e) => { setQ(e.target.value); setPage(1) }} /></div>
      </div>
      {kind !== 'image' && <LibraryCategoryChips key={kind} kind={kind} className="-mt-1 mb-5" value={tag} onChange={(v) => { setTag(v); setPage(1) }} />}
      {isLoading ? (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">{Array.from({ length: 9 }, (_, i) => <Skeleton key={i} className="h-16" />)}</div>
      ) : !data?.items?.length ? (
        <Empty icon={kind === 'image' ? <Image /> : <Music />} title="没有素材" description="上传文件，或者用 AI 描述你想要的声音来生成。" />
      ) : kind === 'image' ? (
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4 lg:grid-cols-6">
          {data.items.map((a: any, i: number) => (
            <button key={a.id} onClick={() => setOpen(a)} className="rise-in group text-left" style={{ '--i': i } as React.CSSProperties}>
              <img src={a.url} alt={a.name} className="aspect-square w-full rounded-[10px] border border-line object-cover transition-[transform,box-shadow] duration-300 ease-[var(--ease-out)] group-hover:-translate-y-1 group-hover:shadow-[var(--shadow-lift)]" />
              <div className="mt-1.5 truncate text-[12.5px]">{a.name}</div>
            </button>
          ))}
        </div>
      ) : (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {data.items.map((a: any, i: number) => (
            <div key={a.id} onClick={() => setOpen(a)} style={{ '--i': i } as React.CSSProperties} className="rise-in flex cursor-pointer items-center gap-3 rounded-[10px] border border-line bg-surface px-3 py-2.5 shadow-[var(--shadow-card)] transition-[border-color,box-shadow] duration-200 hover:border-line-strong hover:shadow-[var(--shadow-lift)]">
              <PlayButton id={'asset:' + a.id} url={a.url} title={a.name} />
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-1.5"><span className="truncate text-[13.5px] font-medium">{a.name}</span>{a.scope === 'system' && <Badge tone="accent">官方</Badge>}{a.origin === 'ai' && <Badge tone="signal">AI</Badge>}</div>
                <div className="truncate text-xs text-muted">{[duration(a.durationMs), ...(a.tags ?? []).slice(0, 3)].join(' · ')}</div>
              </div>
              {a.favorite && <Heart className="size-3.5 fill-danger text-danger" />}
            </div>
          ))}
        </div>
      )}
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
      <AssetDetail a={open} onClose={() => setOpen(null)} />
    </div>
  )
}

function AssetDetail({ a, onClose }: { a: any; onClose: () => void }) {
  const me = useSession((s) => s.user)
  const sid = useSession((s) => s.studioId)
  const usage = useQuery({ queryKey: ['asset-usage', a?.id], queryFn: () => api.get(`/assets/${a.id}/usage`), enabled: !!a })
  const [name, setName] = useState('')
  const [tags, setTags] = useState('')
  const owned = a && a.scope !== 'system' && (a.ownerUserId === me?.id || a.studioId === sid)
  if (a && name === '' && tags === '') setTimeout(() => { setName(a.name); setTags((a.tags ?? []).join('、')) })
  return (
    <Sheet open={!!a} onOpenChange={(o) => { if (!o) { onClose(); setName(''); setTags('') } }} title={a?.name} description={a && [assetKindName[a.kind], a.durationMs ? duration(a.durationMs) : '', bytes(a.size)].filter(Boolean).join(' · ')} width={460}
      footer={a && <>
        <Button variant="ghost" icon={<Heart className={cn('size-4', a.favorite && 'fill-danger text-danger')} />} onClick={async () => { await api.post(`/assets/${a.id}/favorite`); invalidate(['assets']); onClose() }}>{a.favorite ? '取消收藏' : '收藏'}</Button>
        {owned && <Button variant="ghost" className="text-danger" icon={<Trash2 className="size-4" />} onClick={async () => {
          if (await confirm({ title: '删除这个素材？', description: usage.data?.count ? `有 ${usage.data.count} 处在使用它，删除后重新合成时会被跳过。` : undefined, danger: true, confirmText: '删除' })) {
            await api.del(`/assets/${a.id}`)
            invalidate(['assets'])
            onClose()
          }
        }}>删除</Button>}
        {owned && <Button variant="primary" onClick={async () => { await api.patch(`/assets/${a.id}`, { name, tags: tags.split(/[、,，\s]+/).filter(Boolean) }); invalidate(['assets']); toast.success('已保存'); onClose() }}>保存</Button>}
      </>}
    >
      {a && (
        <div className="space-y-5">
          {a.kind === 'image' ? <img src={a.url} className="w-full rounded-[10px] border border-line" alt="" /> : (
            <div className="flex items-center gap-3 rounded-[10px] bg-sunken/60 p-4"><PlayButton id={'asset:' + a.id} url={a.url} title={a.name} size="md" /><span className="text-[13px] text-muted">{a.description || '点击试听'}</span></div>
          )}
          {owned ? (
            <>
              <Field label="名称"><Input value={name} onChange={(e) => setName(e.target.value)} /></Field>
              <Field label="标签" hint="用顿号分隔，方便搜索"><Input value={tags} onChange={(e) => setTags(e.target.value)} /></Field>
            </>
          ) : (
            <div className="flex flex-wrap gap-1">{(a.tags ?? []).map((t: string) => <Badge key={t}>{t}</Badge>)}</div>
          )}
          {a.meta?.libraryId ? <LibraryCredit a={a} /> : <div className="text-[13px] text-muted">授权：{a.license === 'commercial' ? '可商用' : a.license === 'personal' ? '仅限个人使用' : a.license === 'provider_terms' ? `以生成服务（${a.meta?.provider === 'suno' ? 'Suno' : '第三方'}）的条款为准，请自行确认能否商用` : a.license === 'unknown' ? '不确定' : a.license}{a.meta?.prompt && <div className="mt-2">AI 提示词：{a.meta.prompt}</div>}{a.meta?.lyrics && a.meta.lyrics !== '[Instrumental]' && <div className="mt-2 whitespace-pre-line">歌词：{a.meta.lyrics}</div>}</div>}
          {usage.data?.count > 0 && <div className="text-[13px] text-muted">在 {usage.data.count} 个章节中使用</div>}
          {a.kind === 'image' && <a href={a.url} download className="text-[13px] text-accent">下载原图</a>}
          <div className="text-[11.5px] text-faint">添加于 {fromNow(a.createdAt)}</div>
        </div>
      )}
    </Sheet>
  )
}

const genExamples: Record<string, string[]> = {
  music: ['舒缓的古风钢琴，带一点忧伤，适合回忆场景', '紧张悬疑的弦乐，节奏逐渐加快', '轻快温馨的吉他，阳光午后'],
  sfx: ['老旧木门缓缓推开的吱呀声', '夜晚窗外淅淅沥沥的雨声', '远处传来的钟声，空旷回响'],
  image: ['水墨风格的江南庭院，竹影摇曳，书籍封面', '赛博朋克城市夜景，霓虹灯，悬疑小说封面', '温暖的手绘插画，少女在窗边读书'],
}

type MusicFeatures = { vocals: boolean; lyrics: boolean; style: boolean; duration: boolean; perRequest: number }
type GenOptions = Record<string, { available: boolean; provider?: string; name?: string; builtin?: boolean; features?: MusicFeatures }>

function GenerateDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const sid = useSession((s) => s.studioId)
  const [kind, setKind] = useState('music')
  const [prompt, setPrompt] = useState('')
  const [dur, setDur] = useState(30)
  const [count, setCount] = useState(2)
  const [vocal, setVocal] = useState(false)
  const [style, setStyle] = useState('')
  const [lyrics, setLyrics] = useState('')
  const [title, setTitle] = useState('')
  const [busy, setBusy] = useState(false)
  // what the configured services can do: Suno takes style, lyrics and sung songs but picks its own length
  const opts = useQuery({ queryKey: ['gen-options', sid], queryFn: () => api.get<GenOptions>(`/studios/${sid}/generate/options`), enabled: open && !!sid, staleTime: 60_000 })
  const music = opts.data?.music
  const mf = kind === 'music' ? music?.features : undefined
  const per = mf?.perRequest ?? 1
  const counts = per > 1 ? [per, per * 2] : [1, 2, 4]
  const showDuration = kind !== 'image' && (kind !== 'music' || !mf || mf.duration)
  const examples = kind === 'music' && vocal ? vocalExamples : genExamples[kind]
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="AI 生成素材" description="描述你想要的声音或画面，生成后会自动保存到资产库。" width={580} footer={<><Button onClick={() => onOpenChange(false)}>取消</Button><Button variant="primary" icon={<Wand2 className="size-4" />} loading={busy} disabled={!prompt.trim()} onClick={async () => {
      setBusy(true)
      try {
        const songs = kind === 'music' && mf?.vocals
        await api.post(`/studios/${sid}/generate`, {
          kind, prompt, durationMs: showDuration ? dur * 1000 : 0, count,
          ...(songs ? { vocal, style: mf?.style ? style : '', lyrics: vocal && mf?.lyrics ? lyrics : '', title: vocal ? title : '' } : {}),
        })
        toast.success(songs ? `已交给 ${music?.name ?? '音乐服务'} 作曲，通常 1–3 分钟完成，完成后会出现在资产库并通知你` : '正在生成，完成后会出现在资产库并通知你')
        onOpenChange(false)
        setPrompt('')
        setLyrics('')
        setTitle('')
      } finally {
        setBusy(false)
      }
    }}>生成</Button></>}>
      <div className="space-y-5">
        <Segmented value={kind} onChange={(v) => { setKind(v); setCount(v === 'music' ? (music?.features?.perRequest ?? 1) > 1 ? music!.features!.perRequest : 2 : 4); setDur(v === 'sfx' ? 5 : 30) }} items={[{ value: 'music', label: '背景音乐' }, { value: 'sfx', label: '音效' }, { value: 'image', label: '图片 / 封面' }]} />
        {kind === 'music' && mf?.vocals && (
          <Field label="类型" hint={vocal ? '带人声的歌曲适合片头曲、片尾曲和插曲' : '纯音乐适合垫在旁白下面做背景'}>
            <Segmented value={vocal ? 'song' : 'bgm'} onChange={(v) => setVocal(v === 'song')} items={[{ value: 'bgm', label: '纯音乐' }, { value: 'song', label: '带人声的歌曲' }]} />
          </Field>
        )}
        <Field label="描述"><Textarea rows={3} value={prompt} onChange={(e) => setPrompt(e.target.value)} placeholder={kind === 'music' && vocal ? '这首歌讲什么、什么情绪，例如：为《雾港来信》写一首温柔的片头曲' : undefined} /></Field>
        <div className="flex flex-wrap gap-1.5">{examples.map((e) => <Chip key={e} onClick={() => setPrompt(e)}>{e.slice(0, 16)}…</Chip>)}</div>
        {kind === 'music' && mf?.style && (
          <Field label="风格（可选）" hint="用逗号分隔，例如乐器、曲风、速度、人声"><Input value={style} onChange={(e) => setStyle(e.target.value)} placeholder={vocal ? '民谣, 女声, 木吉他, 慢节奏' : '古风, 钢琴, 弦乐, 舒缓'} maxLength={200} /></Field>
        )}
        {kind === 'music' && vocal && mf?.lyrics && (
          <div className="grid gap-4 sm:grid-cols-[1fr_2fr]">
            <Field label="歌名（可选）"><Input value={title} onChange={(e) => setTitle(e.target.value)} maxLength={80} /></Field>
            <Field label="歌词（可选）" hint="留空由 AI 根据描述写词；可以用 [Verse] [Chorus] 标出段落"><Textarea rows={4} value={lyrics} onChange={(e) => setLyrics(e.target.value)} maxLength={3000} /></Field>
          </div>
        )}
        {showDuration && <Field label={`时长 ${dur} 秒`}><Slider value={dur} min={kind === 'sfx' ? 1 : 10} max={kind === 'sfx' ? 22 : 180} step={1} onChange={setDur} /></Field>}
        <Field label="生成数量" hint={per > 1 ? `${music?.name ?? '音乐服务'}每次生成 ${per} 首，时长由它决定，通常 2–4 分钟` : undefined}>
          <Segmented size="sm" value={String(count)} onChange={(v) => setCount(Number(v))} items={counts.map((v) => ({ value: String(v), label: kind === 'music' && per > 1 ? `${v} 首` : `${v} 个` }))} />
        </Field>
        <p className="text-[12px] leading-relaxed text-muted">
          AI 生成的内容可能与已有作品相似，本平台不保证其不侵犯他人权利，请自行判断后使用。
          {kind === 'music' && music?.name && <>音乐由第三方服务（{music.name}）生成，能否商用以该服务的条款为准。</>}
          <a href="/legal/copyright" target="_blank" rel="noreferrer" className="text-accent hover:underline">版权声明</a>
        </p>
      </div>
    </Dialog>
  )
}

const vocalExamples = ['为《雾港来信》写一首温柔的片头曲，讲一封迟到二十年的信', '热血少年冒险故事的主题曲，节奏明快，充满希望', '古风言情的片尾曲，缠绵惆怅，女声']

function UploadDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const sid = useSession((s) => s.studioId)
  const [kind, setKind] = useState('bgm')
  const [license, setLicense] = useState('commercial')
  const [tags, setTags] = useState('')
  const [busy, setBusy] = useState(false)
  const [owned, setOwned] = useState(false)
  useEffect(() => { if (open) setOwned(false) }, [open])
  const upload = async (files: File[]) => {
    if (!owned) return toast.error('请先勾选确认你拥有这些文件的使用权')
    setBusy(true)
    try {
      for (const f of files) await api.upload(`/studios/${sid}/assets`, f, { kind, license, tags, name: f.name.replace(/\.[^.]+$/, '') })
      invalidate(['assets'])
      toast.success(`已上传 ${files.length} 个文件`)
      onOpenChange(false)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="上传素材" width={520}>
      <div className="space-y-4">
        <div className="grid grid-cols-2 gap-3">
          <Field label="类型"><Select value={kind} onChange={setKind} options={['bgm', 'sfx', 'ambience', 'image'].map((k) => [k, assetKindName[k]] as [string, string])} /></Field>
          <Field label="授权"><Select value={license} onChange={setLicense} options={[['commercial', '可商用'], ['personal', '仅个人使用'], ['unknown', '不确定']]} /></Field>
        </div>
        <Field label="标签（可选）"><Input value={tags} onChange={(e) => setTags(e.target.value)} placeholder="用逗号分隔" /></Field>
        <label className={cn('flex cursor-pointer items-start gap-2.5 rounded-[10px] border px-3.5 py-3 text-[12.5px] leading-relaxed transition-colors', owned ? 'border-line bg-sunken/40' : 'border-warn/50 bg-warn-soft')}>
          <Checkbox checked={owned} onChange={setOwned} className="mt-[3px]" />
          <span>我确认这些文件是我自己创作的，或我已取得在作品中使用、发行的授权。<b>本平台不审查也不保证上传内容的版权，因此产生的纠纷由我自行承担。</b><a href="/legal/copyright" target="_blank" rel="noreferrer" className="ml-1 text-accent hover:underline">版权声明</a></span>
        </label>
        <div className={cn('transition-opacity', !owned && 'pointer-events-none opacity-45')} aria-disabled={!owned}>
          <UploadZone accept={kind === 'image' ? 'image/*' : 'audio/*'} multiple onFiles={upload} hint={owned ? (kind === 'image' ? 'PNG、JPG、WebP' : 'WAV、MP3、M4A、FLAC、OGG') : '勾选上面的确认后才能上传'}>
            {busy ? <p className="text-[13px] text-muted">正在上传…</p> : undefined}
          </UploadZone>
        </div>
      </div>
    </Dialog>
  )
}
