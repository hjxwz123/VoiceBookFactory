import { useQuery } from '@tanstack/react-query'
import { CheckCircle2, Download, Link2, Package, Send, XCircle } from '@/components/icons'
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { bytes, dateTime, fromNow } from '@/lib/format'
import { Badge, Button, Checkbox, cn, Copyable, DateInput, Dialog, Empty, Field, Input, Panel, Segmented, Select, Switch, Term } from '@/components/ui'
import { can, invalidate, useChapters } from '../../hooks'
import { CopyrightNotice } from '@/components/legal'

const epStatus: Record<string, [string, any]> = { scheduled: ['待发布', 'neutral'], publishing: ['发布中', 'accent'], published: ['已发布', 'ok'], failed: ['失败', 'danger'], canceled: ['已取消', 'neutral'], paused: ['已暂停', 'warn'] }

export default function Deliver({ book }: { book: any }) {
  const chapters = useChapters(book.id)
  const exports = useQuery<any[]>({ queryKey: ['exports', book.id], queryFn: () => api.get(`/books/${book.id}/exports`), refetchInterval: (q) => ((q.state.data ?? []).some((e: any) => e.export.status === 'running' || e.export.status === 'queued') ? 3000 : false) })
  const pubs = useQuery<any[]>({ queryKey: ['publications', book.id], queryFn: () => api.get(`/books/${book.id}/publications`) })
  const shares = useQuery<any[]>({ queryKey: ['shares', book.id], queryFn: () => api.get(`/books/${book.id}/shares`) })
  const [exp, setExp] = useState(false)
  const [pub, setPub] = useState(false)
  const [share, setShare] = useState(false)
  const rendered = (chapters.data ?? []).filter((c) => c.included && c.renderId)

  return (
    <div className="space-y-6">
      <DeliveryCheck book={book} />
      <Panel
        title="导出成品"
        description="打包下载已合成的章节，可以附带字幕、分轨、画本和台词表。"
        padded={false}
        actions={can(book, 'export') && <Button size="sm" variant="primary" icon={<Package className="size-4" />} disabled={!rendered.length} onClick={() => setExp(true)}>新建导出</Button>}
      >
        {!rendered.length && <p className="border-b border-line px-5 py-3 text-[13px] text-warn">还没有合成好的章节。先在「章节」里生成配音并合成。</p>}
        {!(exports.data ?? []).length ? (
          <Empty title="还没有导出记录" className="py-10" />
        ) : (
          <ul>
            {exports.data!.map(({ export: e, downloadUrl }) => (
              <li key={e.id} className="flex items-center gap-3 border-t border-line px-5 py-3 first:border-t-0">
                <Package className="size-4 text-muted" />
                <div className="min-w-0 flex-1">
                  <div className="truncate text-[13.5px] font-medium">{e.fileName || `${book.title}.${e.format}`}</div>
                  <div className="text-xs text-faint">{e.chapterIds?.length} 章 · {e.format.toUpperCase()} · {bytes(e.size)} · {fromNow(e.createdAt)}</div>
                </div>
                {e.status === 'ready' ? (
                  <a href={downloadUrl}><Button size="sm" icon={<Download className="size-4" />}>下载</Button></a>
                ) : e.status === 'failed' ? (
                  <Badge tone="danger">失败：{e.error}</Badge>
                ) : (
                  <Badge tone="signal" dot>打包中</Badge>
                )}
              </li>
            ))}
          </ul>
        )}
      </Panel>

      <Panel title="发布到平台" description="把章节按计划推送到已绑定的平台（RSS 播客、Webhook 或平台开放接口）。" padded={false} actions={can(book, 'manage') && <Button size="sm" icon={<Send className="size-4" />} onClick={() => setPub(true)}>新建发布</Button>}>
        {!(pubs.data ?? []).length ? (
          <Empty title="还没有发布计划" description={<>先在 <Link to="/studio/channels" className="text-accent">工作室设置 → 发布渠道</Link> 绑定平台。</>} className="py-10" />
        ) : (
          pubs.data!.map((p) => (
            <div key={p.publication.id} className="border-t border-line px-5 py-4 first:border-t-0">
              <div className="flex items-center gap-2">
                <span className="text-[13.5px] font-medium">{p.channel?.name}</span>
                <Badge tone={epStatus[p.publication.status]?.[1]}>{epStatus[p.publication.status]?.[0] ?? p.publication.status}</Badge>
                <span className="text-xs text-faint">开始于 {dateTime(p.publication.startAt)}{p.publication.intervalHours ? ` · 每 ${p.publication.intervalHours} 小时 ${p.publication.perBatch} 集` : ''}</span>
                <div className="ml-auto flex gap-1">
                  {p.publication.status === 'failed' && <Button size="xs" onClick={async () => { await api.post(`/publications/${p.publication.id}/action`, { action: 'retry' }); invalidate(['publications', book.id]) }}>重试</Button>}
                  {['scheduled', 'publishing'].includes(p.publication.status) && <Button size="xs" onClick={async () => { await api.post(`/publications/${p.publication.id}/action`, { action: 'pause' }); invalidate(['publications', book.id]) }}>暂停</Button>}
                  {p.publication.status === 'paused' && <Button size="xs" onClick={async () => { await api.post(`/publications/${p.publication.id}/action`, { action: 'resume' }); invalidate(['publications', book.id]) }}>继续</Button>}
                </div>
              </div>
              <ul className="mt-2 space-y-1">
                {p.episodes.map((ep: any) => (
                  <li key={ep.id} className="flex items-center gap-3 text-[12.5px]">
                    <span className="w-6 text-faint tnum">{ep.index}</span>
                    <span className="min-w-0 flex-1 truncate">{ep.title}</span>
                    {ep.error && <span className="max-w-[50%] truncate text-xs text-danger" title={ep.error}>{ep.error}</span>}
                    {ep.externalUrl && <a href={ep.externalUrl} target="_blank" className="text-xs text-accent">查看</a>}
                    <Badge tone={epStatus[ep.status]?.[1]}>{epStatus[ep.status]?.[0] ?? ep.status}</Badge>
                  </li>
                ))}
              </ul>
            </div>
          ))
        )}
      </Panel>

      <Panel title="分享试听" description="生成一个链接发给作者或客户试听，对方无需注册，还可以直接在句子上留言。" padded={false} actions={<Button size="sm" icon={<Link2 className="size-4" />} disabled={!rendered.length} onClick={() => setShare(true)}>创建分享链接</Button>}>
        {!(shares.data ?? []).length ? (
          <Empty title="还没有分享链接" className="py-10" />
        ) : (
          <ul>
            {shares.data!.map(({ link: l, url }) => (
              <li key={l.id} className="flex flex-wrap items-center gap-3 border-t border-line px-5 py-3 first:border-t-0">
                <div className="min-w-0 flex-1">
                  <div className="text-[13.5px] font-medium">{l.name || (l.mode === 'review' ? '审听链接' : '试听链接')}</div>
                  <div className="text-xs text-faint">{l.chapterIds?.length || '全部'} 章 · {l.views} 次查看 · {l.expiresAt ? `${dateTime(l.expiresAt)} 过期` : '永久有效'}{l.hasPassword && ' · 有密码'}</div>
                </div>
                <Copyable text={url} className="max-w-xs" />
                <Button size="xs" variant="ghost" className="text-danger" onClick={async () => { await api.del(`/shares/${l.id}`); invalidate(['shares', book.id]) }}>撤销</Button>
              </li>
            ))}
          </ul>
        )}
      </Panel>

      <ExportDialog open={exp} onOpenChange={setExp} book={book} chapters={rendered} />
      <PublishDialog open={pub} onOpenChange={setPub} book={book} chapters={rendered} />
      <ShareDialog open={share} onOpenChange={setShare} book={book} chapters={rendered} />
    </div>
  )
}

const fixAt: Record<string, [string, string]> = {
  script: ['chapters', '去生成'], audio: ['chapters?generate=missing', '去配音'], renders: ['chapters', '去合成'], tech: ['chapters', '查看章节'],
  review: ['review', '去审听'], issues: ['review', '查看工单'], title: ['settings', '去填写'], copyright: ['settings', '去填写'], ai_label: ['settings', '去开启'],
  description: ['', '去撰写'], cover: ['', '去设置'],
}

/** One place to see whether the book is ready to hand over, following a professional QC sign-off. */
function DeliveryCheck({ book }: { book: any }) {
  const q = useQuery<any[]>({ queryKey: ['delivery-check', book.id], queryFn: () => api.post(`/books/${book.id}/publish/check`, {}) })
  const checks = q.data ?? []
  const todo = checks.filter((c) => !c.ok)
  if (!checks.length) return null
  return (
    <Panel
      title={<span className="flex items-center gap-2">交付前检查 {todo.length ? <Badge tone="warn">{todo.length} 项待处理</Badge> : <Badge tone="ok">全部就绪</Badge>}</span>}
      description={todo.length ? '按专业交付标准逐项核对。可以先导出试听，正式交付或发布前建议全部处理完。' : '画本、配音、成品、审听和上架信息都已就绪，可以导出或发布了。'}
      actions={<Button size="xs" variant="ghost" loading={q.isFetching} onClick={() => q.refetch()}>重新检查</Button>}
    >
      <ul className="grid gap-x-8 gap-y-2.5 sm:grid-cols-2">
        {checks.map((c) => (
          <li key={c.key} className="flex items-start gap-2.5">
            {c.ok ? <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-ok" /> : <XCircle className="mt-0.5 size-4 shrink-0 text-warn" />}
            <div className="min-w-0 flex-1">
              <div className={cn('text-[13px]', c.ok ? 'text-muted' : 'font-medium')}>{c.name}</div>
              {!c.ok && <div className="text-xs text-muted">{c.tip}</div>}
            </div>
            {!c.ok && fixAt[c.key] && <Link to={`/books/${book.id}${fixAt[c.key][0] ? '/' + fixAt[c.key][0] : ''}`} className="shrink-0 text-xs font-medium text-accent hover:underline">{fixAt[c.key][1]}</Link>}
          </li>
        ))}
      </ul>
    </Panel>
  )
}

function ChapterChooser({ chapters, value, onChange }: { chapters: any[]; value: string[]; onChange: (v: string[]) => void }) {
  return (
    <div className="max-h-48 overflow-y-auto rounded-[10px] border border-line">
      <div className="sticky top-0 border-b border-line bg-surface px-3 py-2">
        <Checkbox checked={value.length === chapters.length ? true : value.length ? 'indeterminate' : false} onChange={(v) => onChange(v ? chapters.map((c) => c.id) : [])} label={`全部 ${chapters.length} 章`} />
      </div>
      {chapters.map((c) => (
        <div key={c.id} className="px-3 py-1.5">
          <Checkbox checked={value.includes(c.id)} onChange={(v) => onChange(v ? [...value, c.id] : value.filter((x) => x !== c.id))} label={<span className="text-[13px]">{c.index}. {c.title}{c.renderStale && <span className="ml-1 text-xs text-warn">（有改动未重新合成）</span>}</span>} />
        </div>
      ))}
    </div>
  )
}

function ExportDialog({ open, onOpenChange, book, chapters }: { open: boolean; onOpenChange: (v: boolean) => void; book: any; chapters: any[] }) {
  const presets = useQuery<any[]>({ queryKey: ['presets'], queryFn: () => api.get('/presets') })
  const [ids, setIds] = useState<string[]>([])
  const [f, setF] = useState({ format: book.settings?.exportFormat || 'mp3', packaging: 'zip', bitrate: book.settings?.exportBitrate || 192, preset: book.settings?.loudnessPreset || 'generic', subtitles: [] as string[], stems: false, script: false, lines: false, timeline: false, naming: book.settings?.namingRule || '{index}_{title}' })
  const [busy, setBusy] = useState(false)
  useEffect(() => { if (open) setIds(chapters.map((c) => c.id)) }, [open])
  const toggleSub = (s: string) => setF({ ...f, subtitles: f.subtitles.includes(s) ? f.subtitles.filter((x) => x !== s) : [...f.subtitles, s] })
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="导出成品" width={600} footer={<><Button onClick={() => onOpenChange(false)}>取消</Button><Button variant="primary" loading={busy} disabled={!ids.length} onClick={async () => {
      setBusy(true)
      try {
        await api.post(`/books/${book.id}/exports`, { chapterIds: ids, ...f, packaging: f.format === 'm4b' ? 'single' : f.packaging })
        invalidate(['exports', book.id])
        toast.success('正在打包，完成后可以在列表中下载')
        onOpenChange(false)
      } finally {
        setBusy(false)
      }
    }}>开始导出</Button></>}>
      <div className="space-y-5">
        <Field label="章节"><ChapterChooser chapters={chapters} value={ids} onChange={setIds} /></Field>
        <div className="grid grid-cols-2 gap-4">
          <Field label="格式" hint={f.format === 'm4b' ? 'M4B 是带章节标记的有声书单文件' : undefined}>
            <Select value={f.format} onChange={(v) => setF({ ...f, format: v })} options={[['mp3', 'MP3'], ['m4a', 'M4A (AAC)'], ['m4b', 'M4B 有声书'], ['wav', 'WAV 无损']]} />
          </Field>
          {f.format !== 'wav' && <Field label="码率"><Select value={String(f.bitrate)} onChange={(v) => setF({ ...f, bitrate: Number(v) })} options={[['64', '64 kbps'], ['128', '128 kbps'], ['192', '192 kbps（推荐）'], ['320', '320 kbps']]} /></Field>}
          <Field label="平台标准" term="响度"><Select value={f.preset} onChange={(v) => setF({ ...f, preset: v })} options={(presets.data ?? []).map((p) => [p.key, p.name] as [string, string])} /></Field>
          <Field label="文件命名" hint="可用 {index} {title} {book}"><Input value={f.naming} onChange={(e) => setF({ ...f, naming: e.target.value })} /></Field>
        </div>
        <Field label="附带内容">
          <div className="grid grid-cols-2 gap-2.5">
            <Checkbox checked={f.subtitles.includes('srt')} onChange={() => toggleSub('srt')} label="SRT 字幕" />
            <Checkbox checked={f.subtitles.includes('lrc')} onChange={() => toggleSub('lrc')} label="LRC 歌词字幕" />
            <Checkbox checked={f.stems} onChange={(v) => setF({ ...f, stems: v })} label="分轨（人声 / 音乐 / 音效）" />
            <Checkbox checked={f.script} onChange={(v) => setF({ ...f, script: v })} label="画本文档（Word）" />
            <Checkbox checked={f.lines} onChange={(v) => setF({ ...f, lines: v })} label="分角色台词表（Excel）" />
            <Checkbox checked={f.timeline} onChange={(v) => setF({ ...f, timeline: v })} label="工程文件（OTIO / JSON）" />
          </div>
        </Field>
        <CopyrightNotice>对外发行前，请确认书稿、配音所用的声音、配乐和音效都已取得授权。本平台不对成品的版权负责，因版权产生的纠纷由你自行承担。</CopyrightNotice>
      </div>
    </Dialog>
  )
}

function PublishDialog({ open, onOpenChange, book, chapters }: { open: boolean; onOpenChange: (v: boolean) => void; book: any; chapters: any[] }) {
  const sid = book.studioId
  const channels = useQuery({ queryKey: ['channels', sid], queryFn: () => api.get(`/studios/${sid}/channels`), enabled: open })
  const [ch, setCh] = useState('')
  const [ids, setIds] = useState<string[]>([])
  const [mode, setMode] = useState('now')
  const [startAt, setStartAt] = useState('')
  const [interval, setIntervalH] = useState(24)
  const [per, setPer] = useState(1)
  const [check, setCheck] = useState<any>(null)
  const [busy, setBusy] = useState(false)
  useEffect(() => { if (open) setIds(chapters.map((c) => c.id)) }, [open])
  useEffect(() => {
    if (!ch || !ids.length) return setCheck(null)
    api.post(`/books/${book.id}/publish/check`, { channelId: ch, chapterIds: ids }).then(setCheck).catch(() => {})
  }, [ch, ids.join()])
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="发布到平台" width={600} footer={<><Button onClick={() => onOpenChange(false)}>取消</Button><Button variant="primary" loading={busy} disabled={!ch || !ids.length} onClick={async () => {
      setBusy(true)
      try {
        await api.post(`/books/${book.id}/publications`, { channelId: ch, chapterIds: ids, startAt: mode === 'now' || !startAt ? new Date().toISOString() : new Date(startAt).toISOString(), intervalHours: mode === 'drip' ? interval : 0, perBatch: mode === 'drip' ? per : ids.length })
        invalidate(['publications', book.id])
        toast.success('发布计划已创建')
        onOpenChange(false)
      } finally {
        setBusy(false)
      }
    }}>创建发布</Button></>}>
      <div className="space-y-5">
        <Field label="发布渠道">
          <Select value={ch} onChange={setCh} placeholder="选择渠道" options={(channels.data?.items ?? []).filter((c: any) => c.enabled).map((c: any) => [c.id, c.name] as [string, string])} />
        </Field>
        <Field label="章节"><ChapterChooser chapters={chapters} value={ids} onChange={setIds} /></Field>
        <Field label="发布节奏">
          <Segmented size="sm" value={mode} onChange={setMode} items={[{ value: 'now', label: '立即全部发布' }, { value: 'at', label: '定时发布' }, { value: 'drip', label: '定时连载' }]} />
        </Field>
        {mode !== 'now' && (
          <div className="grid grid-cols-3 gap-3">
            <Field label="开始时间"><DateInput time value={startAt} onChange={setStartAt} /></Field>
            {mode === 'drip' && <Field label="间隔（小时）"><Input type="number" min={1} value={interval} onChange={(e) => setIntervalH(Number(e.target.value))} /></Field>}
            {mode === 'drip' && <Field label="每次集数"><Input type="number" min={1} value={per} onChange={(e) => setPer(Number(e.target.value))} /></Field>}
          </div>
        )}
        {check && (
          <div className="rounded-[10px] border border-line px-4 py-3">
            <div className="mb-2 text-[13px] font-medium">发布前检查</div>
            <ul className="space-y-1.5">
              {check.items.map((it: any) => (
                <li key={it.key} className="flex items-start gap-2 text-[12.5px]">
                  {it.ok ? <CheckCircle2 className="mt-0.5 size-3.5 shrink-0 text-ok" /> : <XCircle className="mt-0.5 size-3.5 shrink-0 text-warn" />}
                  <span className={cn(!it.ok && 'text-ink')}>{it.name}{!it.ok && <span className="ml-1.5 text-muted">— {it.tip}</span>}</span>
                </li>
              ))}
            </ul>
          </div>
        )}
        <CopyrightNotice>发布即表示你确认书稿、声音、配乐和音效均已取得在该平台发行的授权，并会按规定标识 AI 生成内容。本平台不承担版权责任。</CopyrightNotice>
      </div>
    </Dialog>
  )
}

function ShareDialog({ open, onOpenChange, book, chapters }: { open: boolean; onOpenChange: (v: boolean) => void; book: any; chapters: any[] }) {
  const [ids, setIds] = useState<string[]>([])
  const [f, setF] = useState({ name: '', mode: 'review', password: '', expiresDays: 7, allowComment: true, allowDownload: false })
  const [url, setUrl] = useState('')
  useEffect(() => { if (open) { setIds(chapters.map((c) => c.id)); setUrl('') } }, [open])
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="创建分享链接" width={560} footer={url ? <Button variant="primary" onClick={() => onOpenChange(false)}>完成</Button> : <><Button onClick={() => onOpenChange(false)}>取消</Button><Button variant="primary" disabled={!ids.length} onClick={async () => {
      const r = await api.post(`/books/${book.id}/shares`, { chapterIds: ids, ...f })
      setUrl(r.url)
      invalidate(['shares', book.id])
    }}>创建</Button></>}>
      {url ? (
        <div className="space-y-3 text-center">
          <CheckCircle2 className="mx-auto size-10 text-ok" />
          <p className="text-[14px] font-medium">链接已创建</p>
          <Copyable text={url} className="mx-auto" />
          {f.password && <p className="text-xs text-muted">访问密码：{f.password}</p>}
        </div>
      ) : (
        <div className="space-y-5">
          <Field label="名称（仅自己可见）"><Input value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} placeholder="例如：给作者的第一版试听" /></Field>
          <Field label="章节"><ChapterChooser chapters={chapters} value={ids} onChange={setIds} /></Field>
          <div className="grid grid-cols-2 gap-4">
            <Field label="访问密码" hint="留空则不需要密码"><Input value={f.password} onChange={(e) => setF({ ...f, password: e.target.value })} /></Field>
            <Field label="有效期"><Select value={String(f.expiresDays)} onChange={(v) => setF({ ...f, expiresDays: Number(v) })} options={[['1', '1 天'], ['7', '7 天'], ['30', '30 天'], ['0', '永久']]} /></Field>
          </div>
          <div className="space-y-2.5">
            <Switch checked={f.allowComment} onChange={(v) => setF({ ...f, allowComment: v, mode: v ? 'review' : 'listen' })} label="允许在句子上留言（审听反馈）" />
            <Switch checked={f.allowDownload} onChange={(v) => setF({ ...f, allowDownload: v })} label="允许下载音频" />
          </div>
        </div>
      )}
    </Dialog>
  )
}

export { Term }
