import { useQuery } from '@tanstack/react-query'
import { Search } from '@/components/icons'
import { useState } from 'react'
import { CopyrightNotice } from './legal'
import { api, qs } from '@/lib/api'
import { duration } from '@/lib/format'
import { useSession } from '@/lib/session'
import { PlayButton } from './audio'
import { Badge, Chip, cn, Empty, Input, Pagination, Segmented, Sheet, Skeleton } from './ui'

export const assetKindName: Record<string, string> = { jingle: '片头片尾音频', bgm: '背景音乐', sfx: '音效', ambience: '环境音', image: '图片', document: '文档', recording: '录音', voice_sample: '声音样本' }

/** Categories of the official libraries (only those with imported items); value is the category name,
 *  which every library item carries as a tag. bgm = music styles; sfx / ambience = sound-effect scenes. */
export function LibraryCategoryChips({ kind = 'bgm', value, onChange, className }: { kind?: string; value: string; onChange: (v: string) => void; className?: string }) {
  const music = kind === 'bgm'
  const { data } = useQuery<{ key: string; name: string; count: number }[]>({
    queryKey: music ? ['music-categories'] : ['sfx-categories', kind],
    queryFn: () => api.get(music ? '/music/categories' : `/sfx/categories?kind=${kind}`),
    staleTime: 300_000,
    enabled: ['bgm', 'sfx', 'ambience'].includes(kind),
  })
  if (!data?.length) return null
  const chip = (v: string, label: string, count?: number) => <Chip key={v || 'all'} active={value === v} count={count} onClick={() => onChange(v)}>{label}</Chip>
  return (
    <div className={cn('fade-x -mx-1 flex gap-1.5 overflow-x-auto px-1 pb-1 pr-10 [scrollbar-width:none]', className)}>
      {chip('', music ? '全部风格' : '全部场景')}
      {data.map((c) => chip(c.name, c.name, c.count))}
    </div>
  )
}

export const MusicCategoryChips = (p: { value: string; onChange: (v: string) => void; className?: string }) => <LibraryCategoryChips kind="bgm" {...p} />

/** Source and license of an official library track (shown in asset details). */
export function LibraryCredit({ a }: { a: any }) {
  const m = a?.meta
  if (!m?.libraryId) return null
  return (
    <div className="rounded-[10px] border border-line px-4 py-3 text-[12.5px] leading-relaxed">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <Badge>{m.license === 'CC0' ? 'CC0' : '公有领域'}</Badge>
        <span>原网站标注的许可</span>
      </div>
      <div className="mt-2 text-muted">作者：{m.artist || '佚名'}</div>
      <div className="text-muted">来源：{m.source}{m.page && <> · <a href={m.page} target="_blank" rel="noreferrer" className="text-accent hover:underline">查看原始页面</a></>}</div>
      <p className="mt-2 text-[12px] text-warn">许可信息来自原网站，仅供参考，本平台不保证其准确。使用前请打开原始页面自行核实，版权责任由使用者承担。</p>
    </div>
  )
}

export function AssetRow({ a, onClick, extra }: { a: any; onClick?: () => void; extra?: React.ReactNode }) {
  return (
    <div onClick={onClick} className="flex cursor-pointer items-center gap-3 rounded-[10px] border border-line bg-surface px-3 py-2.5 transition-colors hover:border-line-strong">
      {a.kind === 'image' ? <img src={a.url} className="size-8 rounded object-cover" alt="" /> : <PlayButton id={'asset:' + a.id} url={a.url} title={a.name} />}
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-[13.5px] font-medium">{a.name}</span>
          {a.scope === 'system' && <Badge tone="accent">官方</Badge>}
        </div>
        <div className="truncate text-xs text-muted">{[assetKindName[a.kind], a.durationMs ? duration(a.durationMs) : '', ...(a.tags ?? []).slice(0, 4)].filter(Boolean).join(' · ')}</div>
      </div>
      {extra}
    </div>
  )
}

export function AssetPicker({ open, onOpenChange, kinds = ['bgm', 'sfx', 'ambience'], onPick, title = '选择素材' }: { open: boolean; onOpenChange: (v: boolean) => void; kinds?: string[]; onPick: (a: any) => void; title?: string }) {
  const sid = useSession((s) => s.studioId)
  const [kind, setKind] = useState(kinds[0])
  const [q, setQ] = useState('')
  const [tag, setTag] = useState('')
  const [page, setPage] = useState(1)
  const { data, isLoading } = useQuery({ queryKey: ['assets', sid, kind, q, tag, page, 'picker'], queryFn: () => api.get(`/studios/${sid}/assets${qs({ kind, q, tag, page, pageSize: 30 })}`), enabled: open, placeholderData: (p) => p })
  return (
    <Sheet open={open} onOpenChange={onOpenChange} title={title} width={520}>
      {kinds.length > 1 && <Segmented size="sm" value={kind} onChange={(v) => { setKind(v); setTag(''); setPage(1) }} items={kinds.map((k) => ({ value: k, label: assetKindName[k] }))} />}
      <div className="mt-3"><Input icon={<Search />} placeholder="搜索名称或标签，如：紧张、雨声、古风" value={q} onChange={(e) => { setQ(e.target.value); setPage(1) }} /></div>
      <LibraryCategoryChips key={kind} kind={kind} className="mt-3" value={tag} onChange={(v) => { setTag(v); setPage(1) }} />
      <CopyrightNotice what="配乐和音效" className="mt-3 rounded-md bg-warn-soft px-3 py-2" />
      <div className="mt-4 space-y-2">
        {isLoading ? Array.from({ length: 5 }, (_, i) => <Skeleton key={i} className="h-14" />) : !data?.items?.length ? <Empty title="没有找到素材" description="可以在「资产库」上传，或用 AI 生成。" /> : data.items.map((a: any) => <AssetRow key={a.id} a={a} onClick={() => onPick(a)} />)}
      </div>
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
    </Sheet>
  )
}
