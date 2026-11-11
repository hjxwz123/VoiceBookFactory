import type React from 'react'
import { useQuery } from '@tanstack/react-query'
import { ArrowUpRight, BookOpen, CheckCircle2, Circle, Megaphone, Plus, Upload, Wand2, X } from '@/components/icons'
import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api } from '@/lib/api'
import { duration, fromNow, num, taskStatusName } from '@/lib/format'
import { useLive } from '@/lib/live'
import { useSession, useStudio } from '@/lib/session'
import { useShallow } from 'zustand/react/shallow'
import { Badge, Button, cn, Empty, Panel, Progress, Skeleton } from '@/components/ui'
import { FLOW } from '@/components/flow'
import { BookCover } from './Books'
import { creditReach, useCreditGuide } from '@/components/plans'

export default function Dashboard() {
  const user = useSession((s) => s.user)
  const studio = useStudio()
  const nav = useNavigate()
  const sid = studio?.id
  const books = useQuery({ queryKey: ['books', sid, 'recent'], queryFn: () => api.get(`/studios/${sid}/books?pageSize=6`), enabled: !!sid })
  const credits = useQuery({ queryKey: ['credits', sid], queryFn: () => api.get(`/studios/${sid}/credits`), enabled: !!sid })
  const tasks = useQuery({ queryKey: ['tasks', 'mine'], queryFn: () => api.get('/me/tasks') })
  const issues = useQuery({ queryKey: ['issues', 'mine'], queryFn: () => api.get('/me/issues') })
  const ann = useQuery({ queryKey: ['announcements'], queryFn: () => api.get('/announcements') })
  const jobs = useLive(useShallow((s) => Object.values(s.jobs).filter((j) => j.status === 'running' || j.status === 'queued')))
  const [dismissed, setDismissed] = useState<string[]>(() => JSON.parse(localStorage.getItem('vb-ann') || '[]'))
  const guide = useCreditGuide()
  const reach = creditReach(credits.data?.balance?.available, guide.data)
  const hour = new Date().getHours()
  const greet = hour < 6 ? '夜深了' : hour < 12 ? '早上好' : hour < 18 ? '下午好' : '晚上好'
  const list = books.data?.items ?? []
  const first = list[0]
  const onboarding = [
    { label: '导入第一本书稿', done: list.length > 0, to: '/books/new' },
    { label: '分析角色并选角', done: !!first?.progress?.steps?.casting?.done, to: first ? `/books/${first.id}/characters` : '/books/new' },
    { label: '生成第一章的配音', done: (first?.progress?.readySegments ?? 0) > 0, to: first ? `/books/${first.id}/chapters` : '/books/new' },
    { label: '合成并导出成品', done: !!first?.progress?.steps?.export?.done, to: first ? `/books/${first.id}/deliver` : '/books/new' },
  ]
  const showOnboarding = !books.isLoading && onboarding.some((o) => !o.done) && !localStorage.getItem('vb-onboarded')

  return (
    <div className="space-y-8">
      {(ann.data ?? []).filter((a: any) => !dismissed.includes(a.id)).slice(0, 1).map((a: any) => (
        <div key={a.id} className="flex items-start gap-3 rounded-panel border border-line bg-surface px-4 py-3">
          <Megaphone className="mt-0.5 size-4 shrink-0 text-accent" />
          <div className="min-w-0 flex-1 text-[13px]">
            <span className="font-medium">{a.title}</span>
            <span className="ml-2 text-muted">{a.body}</span>
          </div>
          <button className="text-faint hover:text-ink" onClick={() => {
            const d = [...dismissed, a.id]
            setDismissed(d)
            localStorage.setItem('vb-ann', JSON.stringify(d))
          }} aria-label="关闭"><X className="size-4" /></button>
        </div>
      ))}

      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="font-serif text-[28px] font-semibold leading-tight tracking-[0.01em]">{greet}，{user?.nickname}</h1>
          <p className="mt-2 text-[13.5px] text-muted">
            {studio?.name} · 可用积分 <span className="font-medium text-ink tnum">{num(credits.data?.balance?.available)}</span>
            {credits.data?.balance?.frozen > 0 && <span>（{num(credits.data.balance.frozen)} 正被任务占用）</span>}
            {reach && <span className="text-faint"> · 够做{reach.replace('约 ', '约 ')}有声书</span>}
          </p>
        </div>
        <div className="flex gap-2">
          <Button icon={<Wand2 className="size-4" />} onClick={() => nav('/autobook')}>一键成书</Button>
          <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => nav('/books/new')}>新建图书</Button>
        </div>
      </div>

      {showOnboarding && (
        <Panel title="四步做出第一本有声书" description="跟着清单走一遍，之后你就熟悉整个流程了。" actions={<Button size="xs" variant="ghost" onClick={() => { localStorage.setItem('vb-onboarded', '1'); nav(0) }}>不再显示</Button>}>
          <ol className="grid gap-2 sm:grid-cols-4">
            {onboarding.map((o, i) => {
              const next = !o.done && onboarding.slice(0, i).every((x) => x.done)
              return (
                <li key={i} className="rise-in" style={{ '--i': i } as React.CSSProperties}>
                  <Link to={o.to} className={cn('group flex h-full items-start gap-2.5 rounded-[10px] border px-3 py-3 transition-[border-color,background-color,box-shadow] duration-200', o.done ? 'border-line bg-sunken/40' : next ? 'border-accent/50 bg-accent-soft/50 hover:border-accent' : 'border-line hover:border-line-strong')}>
                    {o.done ? <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-accent" weight="fill" /> : <Circle className={cn('mt-0.5 size-4 shrink-0', next ? 'text-accent' : 'text-faint')} />}
                    <span className="min-w-0">
                      <span className={cn('block text-[11.5px] tnum', next ? 'text-accent' : 'text-faint')}>{o.done ? '已完成' : next ? '下一步' : `第 ${i + 1} 步`}</span>
                      <span className={cn('mt-0.5 block text-[13px]', o.done ? 'text-muted' : 'font-medium text-ink')}>{o.label}</span>
                    </span>
                  </Link>
                </li>
              )
            })}
          </ol>
        </Panel>
      )}

      <section>
        <div className="mb-3 flex items-center justify-between">
          <h2 className="font-serif text-[17px] font-semibold">继续制作</h2>
          <Link to="/books" className="text-[13px] text-muted hover:text-ink">全部图书</Link>
        </div>
        {books.isLoading ? (
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-28" />)}</div>
        ) : !list.length ? (
          <Panel padded={false}>
            <Empty icon={<BookOpen />} title="还没有图书" description="上传一份 txt、docx、epub 或 pdf 书稿，系统会自动识别章节。" action={<Button variant="primary" icon={<Upload className="size-4" />} onClick={() => nav('/books/new')}>上传书稿</Button>} />
          </Panel>
        ) : (
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {list.map((b: any, i: number) => {
              const p = b.progress ?? {}
              const nextStep = FLOW.find((f) => f.key === p.next)
              const audio = p.segments ? p.readySegments / p.segments : 0
              return (
                <Link key={b.id} to={`/books/${b.id}`} style={{ '--i': i } as React.CSSProperties} className="lift rise-in group flex gap-4 rounded-panel border border-line bg-surface p-4 shadow-[var(--shadow-card)]">
                  <BookCover book={b} className="w-16 shrink-0" />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-start justify-between gap-2">
                      <h3 className="truncate text-[14px] font-semibold">{b.title}</h3>
                      <ArrowUpRight className="size-4 shrink-0 text-faint opacity-0 transition-opacity group-hover:opacity-100" />
                    </div>
                    <p className="mt-0.5 text-xs text-muted">{p.chapters ?? 0} 章 · 成品 {duration(p.durationMs)} · {fromNow(b.updatedAt)}</p>
                    <Progress value={audio} className="mt-3" height={4} />
                    <p className="mt-2 truncate text-xs text-faint">{nextStep ? `下一步：${nextStep.label}` : '已全部完成'}</p>
                  </div>
                </Link>
              )
            })}
          </div>
        )}
      </section>

      <div className="grid gap-6 lg:grid-cols-2">
        <Panel title="我的待办" padded={false}>
          {!(tasks.data ?? []).length && !(issues.data ?? []).length ? (
            <Empty title="没有待办事项" description="被分配生产任务或返修工单时，会出现在这里。" className="py-10" />
          ) : (
            <ul>
              {(issues.data ?? []).slice(0, 4).map((i: any) => (
                <li key={i.issue.id} className="border-t border-line first:border-t-0">
                  <Link to={`/books/${i.issue.bookId}/chapters/${i.issue.chapterId}?segment=${i.issue.segmentId}`} className="flex items-center gap-3 px-5 py-3 hover:bg-sunken/50">
                    <Badge tone="danger">工单</Badge>
                    <span className="min-w-0 flex-1 truncate text-[13px]">{i.issue.title} · {i.segmentText}</span>
                    <span className="shrink-0 text-xs text-faint">{i.chapterTitle}</span>
                  </Link>
                </li>
              ))}
              {(tasks.data ?? []).slice(0, 6).map((t: any) => (
                <li key={t.task.id} className="border-t border-line first:border-t-0">
                  <Link to="/production" className="flex items-center gap-3 px-5 py-3 hover:bg-sunken/50">
                    <Badge tone={t.overdue ? 'danger' : 'accent'}>{t.task.stageName}</Badge>
                    <span className="min-w-0 flex-1 truncate text-[13px]">《{t.bookTitle}》{t.chapterTitle}{t.characterName && ` · ${t.characterName}`}</span>
                    <span className="shrink-0 text-xs text-faint">{taskStatusName[t.task.status]}</span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </Panel>
        <Panel title="正在进行的 AI 任务" padded={false} actions={<Link to="/jobs" className="text-[13px] text-muted hover:text-ink">任务中心</Link>}>
          {!jobs.length ? (
            <Empty title="当前没有运行中的任务" description="生成画本、配音和合成都在后台进行，你可以随时离开页面。" className="py-10" />
          ) : (
            <ul>
              {jobs.map((j) => (
                <li key={j.id} className="border-t border-line px-5 py-3 first:border-t-0">
                  <div className="flex justify-between text-[13px]">
                    <span className="truncate font-medium">{j.title}</span>
                    <span className="text-faint tnum">{Math.round(j.progress * 100)}%</span>
                  </div>
                  <Progress value={j.progress} tone="signal" className="mt-2" height={4} />
                  <p className="mt-1 truncate text-xs text-muted">{j.progressText}</p>
                </li>
              ))}
            </ul>
          )}
        </Panel>
      </div>
    </div>
  )
}
