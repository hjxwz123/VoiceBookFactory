import { useQuery } from '@tanstack/react-query'
import { motion } from 'framer-motion'
import { BookOpen, Plus, RotateCcw, Search, Trash2 } from '@/components/icons'
import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api, qs } from '@/lib/api'
import { bookStatusName, duration, fromNow, modeName } from '@/lib/format'
import { canCreateBooks, useStudio } from '@/lib/session'
import { Badge, Button, cn, confirm, Empty, Input, PageHeader, Pagination, Segmented, Sheet, Skeleton, stringColor } from '@/components/ui'
import { invalidate, useSid } from '../hooks'
import { toast } from 'sonner'

export function BookCover({ book, className }: { book: { title: string; cover?: string; author?: string }; className?: string }) {
  if (book.cover) return <img src={book.cover} alt={book.title} className={cn('aspect-[3/4] rounded-[8px] object-cover shadow-[0_6px_16px_-8px_rgb(0_0_0/0.35)]', className)} />
  const c = stringColor(book.title)
  return (
    <div className={cn('relative aspect-[3/4] overflow-hidden rounded-[8px] shadow-[0_6px_16px_-8px_rgb(0_0_0/0.35)] [container-type:inline-size]', className)} style={{ background: `linear-gradient(160deg, ${c}, color-mix(in oklab, ${c} 55%, #0d1014))` }}>
      <div className="absolute inset-y-0 left-[7%] w-px bg-white/15" />
      {/* sizes follow the cover width (container units), so long titles never run into the author line */}
      <div className="flex h-full flex-col justify-between gap-1 p-[12%] pl-[16%]">
        <div className="line-clamp-3 font-serif font-semibold leading-[1.3] tracking-[0.02em] text-white" style={{ fontSize: 'clamp(9px, 17cqw, 15px)' }}>{book.title}</div>
        {book.author && <div className="truncate text-white/70" style={{ fontSize: 'clamp(8px, 11cqw, 11px)' }}>{book.author}</div>}
      </div>
    </div>
  )
}

export default function Books() {
  const sid = useSid()
  const studio = useStudio()
  const nav = useNavigate()
  const [status, setStatus] = useState('')
  const [q, setQ] = useState('')
  const [page, setPage] = useState(1)
  const [trash, setTrash] = useState(false)
  const { data, isLoading } = useQuery({ queryKey: ['books', sid, status, q, page], queryFn: () => api.get(`/studios/${sid}/books${qs({ status, q, page, pageSize: 24 })}`), enabled: !!sid, placeholderData: (p) => p })
  const items = data?.items ?? []
  return (
    <div>
      <PageHeader
        title="图书"
        description="每本书是一个制作项目：从导入书稿到导出成品，进度都在这里。"
        actions={
          <>
            <Button variant="ghost" icon={<Trash2 className="size-4" />} onClick={() => setTrash(true)}>回收站</Button>
            {canCreateBooks(studio?.role) && <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => nav('/books/new')}>新建图书</Button>}
          </>
        }
      />
      <div className="mb-5 flex flex-wrap items-center gap-3">
        <Segmented value={status} onChange={(v) => { setStatus(v); setPage(1) }} items={[{ value: '', label: '全部' }, { value: 'preparing', label: '筹备中' }, { value: 'producing', label: '制作中' }, { value: 'completed', label: '已完成' }, { value: 'published', label: '已发布' }, { value: 'archived', label: '已归档' }]} />
        <div className="ml-auto w-full max-w-xs">
          <Input icon={<Search />} placeholder="搜索书名或作者" value={q} onChange={(e) => { setQ(e.target.value); setPage(1) }} />
        </div>
      </div>
      {isLoading ? (
        <div className="grid grid-cols-2 gap-6 sm:grid-cols-3 lg:grid-cols-5">{Array.from({ length: 10 }, (_, i) => <Skeleton key={i} className="aspect-[3/4] h-auto" />)}</div>
      ) : !items.length ? (
        <Empty icon={<BookOpen />} title={q || status ? '没有符合条件的图书' : '还没有图书'} description={q || status ? '换个筛选条件试试。' : '新建图书时上传书稿，系统会自动识别章节，之后一步步引导你完成制作。'} action={!q && !status && canCreateBooks(studio?.role) && <Button variant="primary" onClick={() => nav('/books/new')}>新建图书</Button>} />
      ) : (
        <motion.div layout className="grid grid-cols-2 gap-x-6 gap-y-8 sm:grid-cols-3 lg:grid-cols-5">
          {items.map((b: any, i: number) => {
            const p = b.progress ?? {}
            const pct = p.segments ? Math.round((p.readySegments / p.segments) * 100) : 0
            return (
              <Link key={b.id} to={`/books/${b.id}`} className="rise-in group block" style={{ '--i': i } as React.CSSProperties}>
                <div className="rounded-[8px] transition-[transform,box-shadow] duration-300 ease-[var(--ease-out)] group-hover:-translate-y-1.5 group-hover:shadow-[0_22px_40px_-18px_rgb(0_0_0/0.45)]">
                  <BookCover book={b} />
                </div>
                <div className="mt-3 flex items-start justify-between gap-2">
                  <h3 className="line-clamp-2 text-[13.5px] font-semibold leading-snug">{b.title}</h3>
                  <Badge tone={b.status === 'completed' || b.status === 'published' ? 'ok' : b.status === 'producing' ? 'accent' : b.status === 'moderation' ? 'warn' : 'neutral'}>{bookStatusName[b.status]}</Badge>
                </div>
                <p className="mt-1 text-xs text-muted">{modeName[b.mode]} · {p.chapters ?? 0} 章</p>
                <div className="mt-2 flex items-center gap-2 text-[11.5px] text-faint">
                  <div className="h-1 flex-1 overflow-hidden rounded-full bg-sunken">
                    <div className="h-full bg-accent" style={{ width: `${pct}%` }} />
                  </div>
                  <span className="tnum">{pct}%</span>
                </div>
                <p className="mt-1 text-[11.5px] text-faint">成品 {duration(p.durationMs)} · {fromNow(b.updatedAt)}</p>
              </Link>
            )
          })}
        </motion.div>
      )}
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
      <Trash open={trash} onOpenChange={setTrash} />
    </div>
  )
}

function Trash({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const sid = useSid()
  const { data, refetch } = useQuery({ queryKey: ['trash', sid], queryFn: () => api.get(`/studios/${sid}/trash`), enabled: open })
  return (
    <Sheet open={open} onOpenChange={onOpenChange} title="回收站" description="删除的图书会保留 30 天，之后自动清除。">
      {!(data ?? []).length ? (
        <Empty icon={<Trash2 />} title="回收站是空的" />
      ) : (
        <ul className="space-y-2">
          {data.map((b: any) => (
            <li key={b.id} className="flex items-center gap-3 rounded-[10px] border border-line px-3 py-2.5">
              <BookCover book={b} className="w-9" />
              <div className="min-w-0 flex-1">
                <div className="truncate text-[13px] font-medium">{b.title}</div>
                <div className="text-xs text-faint">删除于 {fromNow(b.deletedAt)}</div>
              </div>
              <Button size="xs" icon={<RotateCcw className="size-3.5" />} onClick={async () => { await api.post(`/studios/${sid}/trash/${b.id}/restore`); toast.success('已恢复'); refetch(); invalidate(['books']) }}>恢复</Button>
              <Button size="xs" variant="ghost" className="text-danger" onClick={async () => {
                if (await confirm({ title: '永久删除这本书？', description: '画本、音频和成品都会被删除，无法恢复。', confirmText: '永久删除', danger: true })) {
                  await api.del(`/studios/${sid}/trash/${b.id}`)
                  refetch()
                }
              }}>彻底删除</Button>
            </li>
          ))}
        </ul>
      )}
    </Sheet>
  )
}
