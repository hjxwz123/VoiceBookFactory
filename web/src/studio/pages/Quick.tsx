import { useQuery } from '@tanstack/react-query'
import { AnimatePresence, motion } from 'framer-motion'
import { BookPlus, ChevronDown, Download, Plus, Trash2, X, Zap } from '@/components/icons'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { duration, emotions, fromNow, num } from '@/lib/format'
import { useSession } from '@/lib/session'
import { PlayButton } from '@/components/audio'
import { Voice, VoicePicker, VoicePlay } from '@/components/voice'
import { Badge, Button, Empty, IconButton, Input, PageHeader, Panel, Select, Spinner } from '@/components/ui'
import { invalidate } from '../hooks'

type Line = { key: string; speaker: string; voice: Voice | null; text: string; emotion: string }
const k = () => Math.random().toString(36).slice(2)

export default function Quick() {
  const sid = useSession((s) => s.studioId)
  const nav = useNavigate()
  const [title, setTitle] = useState('')
  const [lines, setLines] = useState<Line[]>([{ key: k(), speaker: '旁白', voice: null, text: '', emotion: 'neutral' }])
  const [pick, setPick] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const history = useQuery<any[]>({ queryKey: ['quick', sid], queryFn: () => api.get(`/studios/${sid}/quick`), refetchInterval: (q) => ((q.state.data ?? []).some((x: any) => x.item.status !== 'ready' && x.item.status !== 'failed') ? 2000 : false) })
  const upd = (key: string, p: Partial<Line>) => setLines(lines.map((l) => (l.key === key ? { ...l, ...p } : l)))
  const chars = lines.reduce((s, l) => s + Array.from(l.text).length, 0)
  const go = async () => {
    const missing = lines.find((l) => l.text.trim() && !l.voice)
    if (missing) return toast.error(`请为「${missing.speaker}」选择声音`)
    setBusy(true)
    try {
      await api.post(`/studios/${sid}/quick`, { title: title || lines[0].text.slice(0, 20), lines: lines.filter((l) => l.text.trim()).map((l) => ({ speaker: l.speaker, voiceId: l.voice!.id, text: l.text, emotion: l.emotion })) })
      invalidate(['quick', sid])
      toast.success('正在生成')
    } finally {
      setBusy(false)
    }
  }
  return (
    <div>
      <PageHeader title="快速配音" description="不用建书，直接输入文字生成音频。适合试音、短视频配音和多人对话小样。" />
      <div className="grid gap-6 lg:grid-cols-[1fr_340px]">
        <Panel padded={false}>
          <div className="border-b border-line px-5 py-3"><Input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="标题（可选）" className="!border-0 !px-0 text-[15px] font-medium !ring-0" /></div>
          <ul>
            <AnimatePresence initial={false}>
              {lines.map((l, i) => (
                <motion.li key={l.key} layout initial={{ opacity: 0, height: 0 }} animate={{ opacity: 1, height: 'auto' }} exit={{ opacity: 0, height: 0 }} className="border-b border-line">
                  <div className="flex gap-3 px-5 py-3">
                    <div className="w-36 shrink-0 space-y-1.5">
                      <input value={l.speaker} onChange={(e) => upd(l.key, { speaker: e.target.value })} className="w-full rounded-md bg-transparent px-1 text-[13px] font-medium hover:bg-sunken focus:bg-sunken focus:outline-none" />
                      {/* the same field as the emotion select under it, so the two read as a pair */}
                      <button type="button" onClick={() => setPick(l.key)} className="group flex h-8 w-full items-center gap-1.5 rounded-control border border-line bg-surface pl-1.5 pr-2.5 text-left text-[13px] transition-[border-color,box-shadow] duration-150 hover:border-line-strong focus:border-accent focus:outline-none focus:ring-3 focus:ring-accent/15">
                        {l.voice ? <><VoicePlay v={l.voice} size="xs" className="!size-5" /><span className="min-w-0 flex-1 truncate">{l.voice.name}</span></> : <><span className="ml-1 size-1.5 shrink-0 rounded-full bg-signal" aria-hidden /><span className="min-w-0 flex-1 truncate text-faint">选择声音</span></>}
                        <ChevronDown className="size-3.5 shrink-0 text-faint" weight="bold" />
                      </button>
                      <Select size="sm" value={l.emotion} onChange={(v) => upd(l.key, { emotion: v })} options={emotions} />
                    </div>
                    <textarea value={l.text} onChange={(e) => upd(l.key, { text: e.target.value })} rows={3} placeholder="输入要朗读的文字" className="min-h-20 flex-1 resize-y rounded-control border border-transparent bg-sunken/50 px-3 py-2 text-[14px] leading-relaxed focus:border-accent focus:bg-surface focus:outline-none" />
                    {lines.length > 1 && <IconButton label="删除这一行" size="xs" icon={<X className="size-3.5" />} onClick={() => setLines(lines.filter((x) => x.key !== l.key))} />}
                  </div>
                </motion.li>
              ))}
            </AnimatePresence>
          </ul>
          <div className="flex flex-wrap items-center gap-3 px-5 py-3">
            <Button size="sm" variant="ghost" icon={<Plus className="size-4" />} onClick={() => setLines([...lines, { key: k(), speaker: `角色 ${lines.length}`, voice: lines.length > 1 ? lines[lines.length - 2].voice : null, text: '', emotion: 'neutral' }])}>添加一句</Button>
            <span className="ml-auto text-xs text-muted tnum">{num(chars)} 字</span>
            <Button variant="primary" icon={<Zap className="size-4" />} loading={busy} disabled={!chars} onClick={go}>生成音频</Button>
          </div>
        </Panel>
        <Panel title="最近生成" description="保留 30 天" padded={false}>
          {!(history.data ?? []).length ? (
            <Empty title="还没有生成过" description="在左边写好文字、选好声音，点「生成音频」。生成的音频在这里保留 30 天，可以试听和下载。" className="py-10" />
          ) : (
            <ul>
              {history.data!.map(({ item: q, url }) => (
                <li key={q.id} className="flex items-center gap-3 border-t border-line px-5 py-3 first:border-t-0">
                  {q.status === 'ready' ? <PlayButton id={'quick:' + q.id} url={url} title={q.title} /> : q.status === 'failed' ? <Badge tone="danger">失败</Badge> : <Spinner />}
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-[13px] font-medium">{q.title}</div>
                    <div className="text-[11px] text-faint">{q.status === 'failed' ? q.error : `${duration(q.durationMs)} · ${num(q.credits)} 积分 · ${fromNow(q.createdAt)}`}</div>
                  </div>
                  {q.status === 'ready' && (
                    <>
                      <a href={url} download><IconButton label="下载" size="xs" icon={<Download className="size-3.5" />} /></a>
                      <IconButton label="转为图书继续制作" size="xs" icon={<BookPlus className="size-3.5" />} onClick={async () => { const r = await api.post(`/quick/${q.id}/to-book`); nav(`/books/${r.bookId}`) }} />
                    </>
                  )}
                  <IconButton label="删除" size="xs" icon={<Trash2 className="size-3.5" />} onClick={async () => { await api.del(`/quick/${q.id}`); invalidate(['quick', sid]) }} />
                </li>
              ))}
            </ul>
          )}
        </Panel>
      </div>
      <VoicePicker open={!!pick} onOpenChange={(o) => !o && setPick(null)} current={lines.find((l) => l.key === pick)?.voice?.id} onPick={(v) => { upd(pick!, { voice: v.id ? v : null }); setPick(null) }} />
    </div>
  )
}
