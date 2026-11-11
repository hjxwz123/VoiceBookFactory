import { useQuery } from '@tanstack/react-query'
import { motion } from 'framer-motion'
import { Download, MessageSquare } from '@/components/icons'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useParams } from 'react-router-dom'
import { toast } from 'sonner'
import { api, ApiError } from '@/lib/api'
import { duration, fromNow, timecode } from '@/lib/format'
import { usePlayer } from '@/lib/player'
import { Button, cn, Input, Spinner, Textarea } from '@/components/ui'
import { BookCover } from './Books'

/** Public listening / review page — no login needed. Lines highlight in sync with playback. */
export default function Share() {
  const { token } = useParams()
  const [pwd, setPwd] = useState('')
  const [tried, setTried] = useState('')
  const { data, error, isLoading, refetch } = useQuery({ queryKey: ['share', token, tried], queryFn: () => api.get(`/share/${token}${tried ? `?password=${encodeURIComponent(tried)}` : ''}`), retry: false })
  const [ci, setCi] = useState(0)
  const { id, time, playing } = usePlayer()
  const [author, setAuthor] = useState(() => localStorage.getItem('vb-share-name') || '')
  const [body, setBody] = useState('')
  const [target, setTarget] = useState<any>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const ch = data?.chapters?.[ci]
  const nowMs = id === 'share:' + ch?.id ? time * 1000 : -1
  const current = useMemo(() => (ch?.timings ?? []).find((t: any) => nowMs >= t.startMs && nowMs < t.endMs), [ch, nowMs])
  useEffect(() => {
    if (!current) return
    listRef.current?.querySelector(`[data-seg="${current.segmentId}"]`)?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  }, [current?.segmentId])

  const needPwd = error instanceof ApiError && error.code === 'password_required'
  if (isLoading) return <div className="grid h-full place-items-center"><Spinner className="size-6" /></div>
  if (needPwd || (error && tried))
    return (
      <div className="grid h-full place-items-center px-6">
        <form onSubmit={(e) => { e.preventDefault(); setTried(pwd) }} className="w-full max-w-xs rounded-sheet border border-line bg-surface p-6 text-center">
          <p className="text-[15px] font-semibold">这个链接需要访问密码</p>
          {tried && <p className="mt-1 text-xs text-danger">密码不正确</p>}
          <Input autoFocus type="password" value={pwd} onChange={(e) => setPwd(e.target.value)} className="mt-4" />
          <Button variant="primary" className="mt-3 w-full" type="submit">进入</Button>
        </form>
      </div>
    )
  if (error) return <div className="grid h-full place-items-center text-muted">{(error as any).message || '链接已失效'}</div>
  const play = (c: any, at = 0) => {
    usePlayer.getState().play('share:' + c.id, c.url, c.title)
    if (at) setTimeout(() => usePlayer.getState().seek(at / 1000), 200)
  }
  const comments = (data.comments ?? []).filter((c: any) => c.chapterId === ch?.id)
  const send = async () => {
    await api.post(`/share/${token}/comments`, { password: tried, chapterId: ch.id, segmentId: target?.segmentId ?? '', atMs: target ? target.startMs : nowMs >= 0 ? Math.round(nowMs) : null, author, body })
    localStorage.setItem('vb-share-name', author)
    setBody('')
    setTarget(null)
    refetch()
    toast.success('意见已发送给制作团队')
  }

  return (
    <div className="min-h-full bg-bg">
      <div className="mx-auto max-w-5xl px-6 py-10">
        <div className="flex items-start gap-6">
          <BookCover book={data.book} className="w-28 shrink-0" />
          <div className="min-w-0">
            <p className="text-xs text-muted">{data.site} · {data.mode === 'review' ? '审听样片' : '试听'}</p>
            <h1 className="mt-1 font-serif text-[28px] font-semibold leading-tight tracking-[0.01em]">{data.book.title}</h1>
            <p className="mt-1 text-[13px] text-muted">{[data.book.author && `作者 ${data.book.author}`, data.book.narrator && `演播 ${data.book.narrator}`].filter(Boolean).join(' · ')}</p>
            {data.book.description && <p className="mt-3 line-clamp-3 max-w-2xl text-[13.5px] leading-relaxed text-muted">{data.book.description}</p>}
          </div>
        </div>
        <div className="mt-8 grid gap-6 lg:grid-cols-[260px_1fr]">
          <div className="space-y-1">
            {data.chapters.map((c: any, i: number) => (
              <button key={c.id} onClick={() => { setCi(i); play(c) }} className={cn('flex w-full items-center gap-3 rounded-[10px] px-3 py-2.5 text-left transition-colors', ci === i ? 'bg-surface shadow-[0_1px_2px_rgb(0_0_0/0.06)]' : 'hover:bg-surface/60')}>
                <span className="w-6 text-xs text-faint tnum">{c.index}</span>
                <span className="min-w-0 flex-1 truncate text-[13.5px]">{c.title}</span>
                {id === 'share:' + c.id && playing ? <span className="eq flex h-3 items-end text-signal"><span /><span /><span /></span> : <span className="text-xs text-faint tnum">{duration(c.durationMs)}</span>}
              </button>
            ))}
          </div>
          {ch && (
            <div className="rounded-panel border border-line bg-surface">
              <div className="flex items-center gap-3 border-b border-line px-5 py-3">
                <Button variant="primary" size="sm" onClick={() => (id === 'share:' + ch.id ? usePlayer.getState().toggle(id, ch.url) : play(ch))}>{id === 'share:' + ch.id && playing ? '暂停' : '播放'}</Button>
                <span className="text-[13.5px] font-medium">{ch.title}</span>
                <span className="text-xs text-muted tnum">{nowMs >= 0 ? timecode(nowMs) : '00:00'} / {timecode(ch.durationMs)}</span>
                {ch.downloadUrl && <a href={ch.downloadUrl} className="ml-auto"><Button size="xs" icon={<Download className="size-3.5" />}>下载</Button></a>}
              </div>
              <div ref={listRef} className="max-h-[56vh] space-y-1 overflow-y-auto px-5 py-4">
                {(ch.timings ?? []).map((t: any) => {
                  const c = data.characters?.[t.characterId]
                  const active = current?.segmentId === t.segmentId
                  const has = comments.some((x: any) => x.segmentId === t.segmentId)
                  return (
                    <motion.div key={t.segmentId} data-seg={t.segmentId} onClick={() => play(ch, t.startMs)} className={cn('group flex cursor-pointer gap-3 rounded-[8px] px-2 py-1.5 transition-colors', active ? 'bg-signal-soft' : 'hover:bg-sunken/60')}>
                      <span className="w-16 shrink-0 pt-0.5 text-right text-xs" style={{ color: c?.color }}>{c?.name}</span>
                      <p className={cn('flex-1 text-[15px] leading-relaxed', active ? 'text-ink' : 'text-ink/80')}>{t.text}</p>
                      {data.allowComment && <button onClick={(e) => { e.stopPropagation(); setTarget(t) }} className={cn('shrink-0 text-faint hover:text-accent', has ? 'text-accent' : 'opacity-0 group-hover:opacity-100')} aria-label="对这句提意见"><MessageSquare className="size-4" /></button>}
                    </motion.div>
                  )
                })}
              </div>
              {data.allowComment && (
                <div className="border-t border-line p-4">
                  {target && <div className="mb-2 flex items-center justify-between text-xs text-muted"><span className="truncate">针对：「{target.text}」</span><button onClick={() => setTarget(null)} className="text-faint hover:text-ink">取消</button></div>}
                  <div className="flex gap-2">
                    <Input value={author} onChange={(e) => setAuthor(e.target.value)} placeholder="你的称呼" className="w-28" />
                    <Textarea rows={1} value={body} onChange={(e) => setBody(e.target.value)} placeholder="点击句子右侧的气泡针对某一句提意见，或直接写下整体感受" className="min-h-9 flex-1" />
                    <Button variant="primary" disabled={!body.trim() || !author.trim()} onClick={send}>发送</Button>
                  </div>
                  {comments.length > 0 && (
                    <ul className="mt-4 space-y-2">
                      {comments.map((c: any) => <li key={c.id} className="text-[13px]"><span className="font-medium">{c.authorName}</span><span className="ml-2 text-xs text-faint">{fromNow(c.createdAt)}</span><p className="text-muted">{c.body}</p></li>)}
                    </ul>
                  )}
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
