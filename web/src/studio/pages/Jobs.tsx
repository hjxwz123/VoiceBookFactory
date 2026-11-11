import { useQuery } from '@tanstack/react-query'
import { ListChecks, RotateCcw, X } from '@/components/icons'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { toast } from 'sonner'
import { api, qs } from '@/lib/api'
import { dateTime, duration, fromNow, jobStatusName, num } from '@/lib/format'
import { useLive } from '@/lib/live'
import { useSession } from '@/lib/session'
import { Badge, Button, Empty, PageHeader, Pagination, Panel, Progress, Segmented, Tip } from '@/components/ui'
import { invalidate } from '../hooks'

const tone: Record<string, any> = { queued: 'neutral', running: 'signal', succeeded: 'ok', failed: 'danger', canceled: 'neutral', paused: 'warn' }

export default function Jobs() {
  const sid = useSession((s) => s.studioId)
  const [status, setStatus] = useState('')
  const [page, setPage] = useState(1)
  const live = useLive((s) => s.jobs)
  const { data } = useQuery({ queryKey: ['jobs', sid, status, page], queryFn: () => api.get(`/studios/${sid}/jobs${qs({ status, page, pageSize: 30 })}`), placeholderData: (p) => p, refetchInterval: 10000 })
  return (
    <div>
      <PageHeader title="任务中心" description="所有在后台运行的 AI 任务：画本、配音、合成、审听、导出……关掉页面也会继续运行，完成后会通知你。" />
      <Segmented className="mb-4" value={status} onChange={(v) => { setStatus(v); setPage(1) }} items={[{ value: '', label: '全部' }, { value: 'running', label: '进行中' }, { value: 'queued', label: '排队中' }, { value: 'failed', label: '失败' }, { value: 'succeeded', label: '已完成' }]} />
      <Panel padded={false}>
        {!data?.items?.length ? (
          <Empty icon={<ListChecks />} title={status ? '没有符合条件的任务' : '还没有任务'} description={status ? undefined : '生成画本、配音、合成或导出时，任务会出现在这里，可以随时查看进度和失败原因。'} />
        ) : (
          <ul>
            {data.items.map((j0: any) => {
              const j = { ...j0, ...(live[j0.id] ?? {}) }
              return (
                <li key={j.id} className="flex flex-wrap items-center gap-4 border-t border-line px-5 py-3.5 first:border-t-0">
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="truncate text-[13.5px] font-medium">{j.title}</span>
                      <Badge tone={tone[j.status]} dot={j.status === 'running'}>{jobStatusName[j.status]}</Badge>
                    </div>
                    <div className="mt-0.5 truncate text-xs text-faint">
                      {fromNow(j.createdAt)}
                      {j.finishedAt && j.startedAt && ` · 用时 ${duration(new Date(j.finishedAt).getTime() - new Date(j.startedAt).getTime())}`}
                      {j.creditsUsed > 0 && ` · ${num(j.creditsUsed)} 积分`}
                      {j.status === 'failed' && j.error && <span className="text-danger"> · {j.error}</span>}
                      {j.status !== 'failed' && j.progressText && ` · ${j.progressText}`}
                    </div>
                  </div>
                  {(j.status === 'running' || j.status === 'queued') && <div className="w-40"><Progress value={j.progress} tone="signal" height={4} /></div>}
                  <div className="flex gap-1">
                    {j.bookId && <Link to={j.chapterId ? `/books/${j.bookId}/chapters/${j.chapterId}` : `/books/${j.bookId}`}><Button size="xs" variant="ghost">查看</Button></Link>}
                    {(j.status === 'running' || j.status === 'queued') && <Button size="xs" variant="ghost" icon={<X className="size-3.5" />} onClick={async () => { await api.post(`/jobs/${j.id}/cancel`); invalidate(['jobs']); toast.success('已请求取消，未使用的积分会退回') }}>取消</Button>}
                    {j.status === 'failed' && <Tip label={dateTime(j.finishedAt)}><Button size="xs" icon={<RotateCcw className="size-3.5" />} onClick={async () => { await api.post(`/jobs/${j.id}/retry`); invalidate(['jobs']) }}>重试</Button></Tip>}
                  </div>
                </li>
              )
            })}
          </ul>
        )}
      </Panel>
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
    </div>
  )
}
