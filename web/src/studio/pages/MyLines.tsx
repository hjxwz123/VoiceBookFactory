import { useQuery } from '@tanstack/react-query'
import { AnimatePresence, motion } from 'framer-motion'
import { ChevronLeft, ChevronRight, Maximize2, Mic2, Upload, X } from '@/components/icons'
import { useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import { api, qs } from '@/lib/api'
import { emotionName } from '@/lib/format'
import { useSession } from '@/lib/session'
import { PlayButton, Recorder, UploadZone } from '@/components/audio'
import { Badge, Button, cn, Dialog, Empty, Field, PageHeader, Panel, Segmented, Select, Skeleton } from '@/components/ui'
import { invalidate } from '../hooks'
import { CueText, segmentCues } from '@/components/cues'

const statusTone: Record<string, any> = { todo: 'neutral', recorded: 'ok', rework: 'danger' }
const statusName: Record<string, string> = { todo: '待录', recorded: '已录', rework: '需返修' }

export default function MyLines() {
  const [book, setBook] = useState('')
  const [char, setChar] = useState('')
  const [status, setStatus] = useState('')
  const [prompter, setPrompter] = useState<number | null>(null)
  const [bulk, setBulk] = useState(false)
  const { data, isLoading } = useQuery({ queryKey: ['lines', book, status], queryFn: () => api.get(`/me/lines${qs({ bookId: book, status })}`), placeholderData: (p) => p })
  const lines: any[] = useMemo(() => (data?.lines ?? []).filter((l: any) => !char || l.characterId === char), [data, char])
  const counts = useMemo(() => {
    const all = data?.lines ?? []
    return { todo: all.filter((l: any) => l.status === 'todo').length, rework: all.filter((l: any) => l.status === 'rework').length }
  }, [data])

  return (
    <div>
      <PageHeader
        title="我的台词"
        description="被指派给你的角色台词都在这里。可以一句句录，也可以整段录完上传，系统会自动切开对到每一句。"
        actions={
          <>
            <Button icon={<Upload className="size-4" />} onClick={() => setBulk(true)} disabled={!data?.characters?.length}>上传整段录音</Button>
            <Button variant="primary" icon={<Maximize2 className="size-4" />} disabled={!lines.length} onClick={() => setPrompter(Math.max(0, lines.findIndex((l) => l.status !== 'recorded')))}>提词器模式</Button>
          </>
        }
      />
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <Segmented value={status} onChange={setStatus} size="sm" items={[{ value: '', label: '全部' }, { value: 'todo', label: `待录 ${counts.todo}` }, { value: 'rework', label: `需返修 ${counts.rework}` }, { value: 'recorded', label: '已录' }]} />
        <Select size="sm" value={book} onChange={(v) => { setBook(v); setChar('') }} options={[['', '全部图书'], ...(data?.books ?? []).map((b: any) => [b.id, b.title] as [string, string])]} className="w-44" />
        <Select size="sm" value={char} onChange={setChar} options={[['', '全部角色'], ...(data?.characters ?? []).filter((c: any) => !book || c.bookId === book).map((c: any) => [c.id, c.name] as [string, string])]} className="w-36" />
      </div>
      {isLoading ? (
        <div className="space-y-2">{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-20" />)}</div>
      ) : !lines.length ? (
        <Panel padded={false}>
          <Empty icon={<Mic2 />} title={data?.characters?.length ? '这里没有台词' : '还没有被指派角色'} description={data?.characters?.length ? '换个筛选条件看看。' : '当制作人把某个角色指派给你，或者你在图书的「角色」页认领角色后，台词会出现在这里。'} />
        </Panel>
      ) : (
        <Panel padded={false}>
          <ul>
            {lines.map((l, i) => (
              <li key={l.id} className="group flex items-start gap-4 border-t border-line px-5 py-3.5 first:border-t-0 hover:bg-sunken/40">
                <div className="w-28 shrink-0 pt-0.5">
                  <div className="flex items-center gap-1.5 text-[12.5px] font-medium"><span className="size-2 rounded-full" style={{ background: l.characterColor }} />{l.characterName}</div>
                  <div className="mt-0.5 truncate text-[11px] text-faint">{l.bookTitle} · 第 {l.chapterIndex} 章</div>
                </div>
                <div className="min-w-0 flex-1">
                  <p className="text-[15px] leading-relaxed"><CueText text={l.text} cues={segmentCues(l)} small /></p>
                  <div className="mt-1 flex flex-wrap gap-x-3 text-[11.5px] text-muted">
                    {l.emotion && l.emotion !== 'neutral' && <span>情绪：{emotionName[l.emotion]}</span>}
                    {l.instruction && <span>指导：{l.instruction}</span>}
                    {l.note && <span>备注：{l.note}</span>}
                  </div>
                </div>
                <div className="flex shrink-0 items-center gap-2">
                  <Badge tone={statusTone[l.status]}>{statusName[l.status]}</Badge>
                  <PlayButton id={'seg:' + l.id} url={l.take?.url} title={l.text} size="xs" />
                  <Button size="xs" onClick={() => setPrompter(i)}>录音</Button>
                </div>
              </li>
            ))}
          </ul>
        </Panel>
      )}
      <AnimatePresence>{prompter !== null && lines[prompter] && <Prompter lines={lines} index={prompter} setIndex={setPrompter} />}</AnimatePresence>
      <BulkUpload open={bulk} onOpenChange={setBulk} data={data} />
    </div>
  )
}

/** Full-screen teleprompter: context lines dimmed, current line large, records and auto-advances. */
function Prompter({ lines, index, setIndex }: { lines: any[]; index: number; setIndex: (i: number | null) => void }) {
  const l = lines[index]
  const [saved, setSaved] = useState<Record<string, boolean>>({})
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setIndex(null)
      if (e.key === 'ArrowRight') setIndex(Math.min(lines.length - 1, index + 1))
      if (e.key === 'ArrowLeft') setIndex(Math.max(0, index - 1))
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [index])
  const done = async (blob: Blob) => {
    try {
      const r = await api.upload(`/segments/${l.id}/record`, blob, { origin: 'record' }, 'take.webm')
      setSaved({ ...saved, [l.id]: true })
      const tips: string[] = r.hints?.tips ?? []
      if (tips.length) toast.warning('已保存', { description: tips.join('；') })
      invalidate(['lines'])
      if (index < lines.length - 1) setTimeout(() => setIndex(index + 1), 500)
      else toast.success('全部录完了')
    } catch (e: any) {
      toast.error(e.message)
    }
  }
  return (
    <motion.div className="fixed inset-0 z-50 flex flex-col bg-[#0f1216] text-white" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}>
      <div className="flex h-14 items-center justify-between px-6">
        <div className="text-[13px] text-white/60">{l.bookTitle} · {l.chapterTitle} · <span style={{ color: l.characterColor }}>{l.characterName}</span></div>
        <div className="flex items-center gap-4 text-[13px] text-white/60">
          <span className="tnum">{index + 1} / {lines.length}</span>
          <button onClick={() => setIndex(null)} className="grid size-8 place-items-center rounded-full hover:bg-white/10" aria-label="退出"><X className="size-5" /></button>
        </div>
      </div>
      <div className="flex flex-1 flex-col items-center justify-center px-8">
        <div className="w-full max-w-3xl">
          {(l.before ?? []).map((t: string, i: number) => <p key={i} className="mb-2 text-[17px] leading-relaxed text-white/30">{t}</p>)}
          <AnimatePresence mode="wait">
            <motion.p key={l.id} initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, y: -12 }} transition={{ duration: 0.25 }} className="my-6 text-[34px] font-medium leading-[1.6]">
              <CueText text={l.text} cues={segmentCues(l)} />
            </motion.p>
          </AnimatePresence>
          {(l.after ?? []).map((t: string, i: number) => <p key={i} className="mb-2 text-[17px] leading-relaxed text-white/30">{t}</p>)}
          <div className="mt-6 flex flex-wrap gap-3 text-[13px] text-white/60">
            {l.emotion && l.emotion !== 'neutral' && <span className="rounded-full bg-white/10 px-3 py-1">情绪：{emotionName[l.emotion]}</span>}
            {l.instruction && <span className="rounded-full bg-white/10 px-3 py-1">{l.instruction}</span>}
            {l.note && <span className="rounded-full bg-white/10 px-3 py-1">{l.note}</span>}
          </div>
        </div>
      </div>
      <div className="flex items-center justify-center gap-4 pb-10">
        <button onClick={() => setIndex(Math.max(0, index - 1))} disabled={index === 0} className="grid size-11 place-items-center rounded-full bg-white/10 hover:bg-white/15 disabled:opacity-30"><ChevronLeft className="size-5" /></button>
        <div className="rounded-full bg-white/5 px-4 py-2 [&_button]:!h-11">
          <Recorder key={l.id} onDone={done} />
        </div>
        <PlayButton id={'seg:' + l.id} url={l.take?.url} title={l.text} size="md" className="!bg-white/10 !text-white" />
        <button onClick={() => setIndex(Math.min(lines.length - 1, index + 1))} disabled={index >= lines.length - 1} className="grid size-11 place-items-center rounded-full bg-white/10 hover:bg-white/15 disabled:opacity-30"><ChevronRight className="size-5" /></button>
      </div>
      <p className={cn('pb-4 text-center text-xs text-white/40', saved[l.id] && 'text-ok')}>{saved[l.id] ? '这句已保存' : '倒数 3 秒后开始录音 · ← → 切换 · Esc 退出'}</p>
    </motion.div>
  )
}

function BulkUpload({ open, onOpenChange, data }: { open: boolean; onOpenChange: (v: boolean) => void; data: any }) {
  const sid = useSession((s) => s.studioId)
  const [char, setChar] = useState('')
  const [chapter, setChapter] = useState('')
  const [files, setFiles] = useState<File[]>([])
  const [busy, setBusy] = useState(false)
  const c = (data?.characters ?? []).find((x: any) => x.id === char)
  const chapters = useMemo(() => {
    const m = new Map<string, string>()
    ;(data?.lines ?? []).filter((l: any) => l.characterId === char).forEach((l: any) => m.set(l.chapterId, `第 ${l.chapterIndex} 章 ${l.chapterTitle}`))
    return Array.from(m.entries())
  }, [data, char])
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="上传整段录音" description="把一章里这个角色的台词连续录完再上传，系统会识别语音并自动切成一句一句，对应到画本里。" width={560} footer={<><Button onClick={() => onOpenChange(false)}>取消</Button><Button variant="primary" loading={busy} disabled={!char || !chapter || !files.length} onClick={async () => {
      setBusy(true)
      try {
        const ids: string[] = []
        for (const f of files) ids.push((await api.upload(`/studios/${sid}/uploads`, f, { kind: 'recording' })).id)
        await api.post(`/chapters/${chapter}/recordings`, { assetIds: ids, mode: 'character', characterId: char })
        toast.success('已上传，正在自动对轨，完成后会通知你')
        onOpenChange(false)
        setFiles([])
      } finally {
        setBusy(false)
      }
    }}>上传并对轨</Button></>}>
      <div className="space-y-4">
        <div className="grid grid-cols-2 gap-3">
          <Field label="角色"><Select value={char} onChange={(v) => { setChar(v); setChapter('') }} placeholder="选择角色" options={(data?.characters ?? []).map((x: any) => [x.id, x.name] as [string, string])} /></Field>
          <Field label="章节"><Select value={chapter} onChange={setChapter} placeholder="选择章节" options={chapters} /></Field>
        </div>
        <UploadZone accept="audio/*" multiple onFiles={setFiles} hint="支持 wav、mp3、m4a，可以一次上传多个分段文件">
          {files.length ? <p className="text-[13px]">{files.map((f) => f.name).join('、')}</p> : undefined}
        </UploadZone>
        {c && <p className="text-xs text-muted">录音时按画本顺序读「{c.name}」的台词，句子之间稍作停顿，读错了直接重读一遍即可，系统会保留最后一遍。</p>}
      </div>
    </Dialog>
  )
}
