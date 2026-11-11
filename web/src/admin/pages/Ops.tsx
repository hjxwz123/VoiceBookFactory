import { RotateCcw, X } from '@/components/icons'
import { useState } from 'react'
import { toast } from 'sonner'
import { adminApi } from '@/lib/api'
import { bytes, dateTime, duration, fromNow, jobStatusName, num } from '@/lib/format'
import { Badge, Button, Empty, Input, PageHeader, Pagination, Panel, Progress, Segmented, Select, Sheet, Stat, Table } from '@/components/ui'
import { ainv, useAdmin } from '../kit'

const tone: Record<string, any> = { queued: 'neutral', running: 'signal', succeeded: 'ok', failed: 'danger', canceled: 'neutral', paused: 'warn' }

export function Jobs() {
  const [status, setStatus] = useState(new URLSearchParams(location.search).get('status') ?? '')
  const [lane, setLane] = useState('')
  const [type, setType] = useState('')
  const [page, setPage] = useState(1)
  const [open, setOpen] = useState<any>(null)
  const { data } = useAdmin<any>('/jobs', { status, lane, type, page, pageSize: 50 }, { refetch: 5000 })
  return (
    <div>
      <PageHeader title="任务队列" description="所有后台任务，每 5 秒刷新。失败的任务可以单个或批量重试。" actions={status === 'failed' && <Button onClick={async () => { const r = await adminApi.post('/jobs/retry-failed', { type }); ainv(['/jobs']); toast.success(`已重新排队 ${r?.retried ?? ''} 个任务`) }}>全部重试</Button>} />
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <Segmented size="sm" value={status} onChange={(v) => { setStatus(v); setPage(1) }} items={[{ value: '', label: '全部' }, { value: 'running', label: '运行中' }, { value: 'queued', label: '排队' }, { value: 'failed', label: '失败' }, { value: 'succeeded', label: '完成' }]} />
        <Select size="sm" value={lane} onChange={(v) => { setLane(v); setPage(1) }} options={[['', '全部通道'], ['default', '常规'], ['audio', '音频'], ['pipeline', '一键成书']]} className="w-28" />
        <Input value={type} onChange={(e) => { setType(e.target.value); setPage(1) }} placeholder="任务类型，如 tts_segments" className="!h-8 w-52 font-mono text-xs" />
      </div>
      <Panel padded={false}>
        {!data?.items?.length ? <Empty title="没有任务" /> : (
          <Table>
            <thead><tr><th>任务</th><th>类型</th><th>通道</th><th>状态</th><th className="!text-right">尝试</th><th className="!text-right">积分</th><th>创建</th><th>用时</th><th /></tr></thead>
            <tbody>
              {data.items.map((j: any) => (
                <tr key={j.id} className="cursor-pointer" onClick={() => setOpen(j)}>
                  <td className="max-w-xs"><div className="truncate font-medium">{j.title}</div>{j.status === 'running' ? <Progress value={j.progress} tone="signal" height={3} className="mt-1 w-40" /> : j.error && <div className="truncate text-xs text-danger">{j.error}</div>}</td>
                  <td className="font-mono text-xs text-muted">{j.type}</td>
                  <td className="text-muted">{j.lane}</td>
                  <td><Badge tone={tone[j.status]} dot={j.status === 'running'}>{jobStatusName[j.status]}</Badge></td>
                  <td className="text-right tnum">{j.attempts}/{j.maxAttempts}</td>
                  <td className="text-right tnum">{num(j.creditsUsed)}</td>
                  <td className="whitespace-nowrap text-xs text-faint">{fromNow(j.createdAt)}</td>
                  <td className="text-xs text-muted tnum">{j.startedAt && j.finishedAt ? duration(new Date(j.finishedAt).getTime() - new Date(j.startedAt).getTime()) : '—'}</td>
                  <td className="whitespace-nowrap text-right" onClick={(e) => e.stopPropagation()}>
                    {(j.status === 'running' || j.status === 'queued') && <Button size="xs" variant="ghost" icon={<X className="size-3.5" />} onClick={async () => { await adminApi.post(`/jobs/${j.id}/cancel`); ainv(['/jobs']) }}>取消</Button>}
                    {(j.status === 'failed' || j.status === 'canceled') && <Button size="xs" variant="ghost" icon={<RotateCcw className="size-3.5" />} onClick={async () => { await adminApi.post(`/jobs/${j.id}/retry`); ainv(['/jobs']) }}>重试</Button>}
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Panel>
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
      <Sheet open={!!open} onOpenChange={(o) => !o && setOpen(null)} title={open?.title} description={open && `${open.type} · ${open.id}`} width={600}>
        {open && (
          <div className="space-y-4 text-[13px]">
            <div className="grid grid-cols-2 gap-3">
              <div>状态：{jobStatusName[open.status]}</div><div>进度：{Math.round(open.progress * 100)}% {open.progressText}</div>
              <div>创建：{dateTime(open.createdAt)}</div><div>完成：{dateTime(open.finishedAt)}</div>
              <div>工作室：<span className="font-mono text-xs">{open.studioId}</span></div><div>冻结 / 使用积分：{num(open.creditsFrozen)} / {num(open.creditsUsed)}</div>
            </div>
            {open.error && <pre className="whitespace-pre-wrap rounded-md bg-danger-soft p-3 text-xs text-danger">{open.error}</pre>}
            <div><div className="mb-1 text-xs text-muted">参数</div><pre className="max-h-60 overflow-auto rounded-md bg-sunken p-3 font-mono text-[11.5px]">{JSON.stringify(open.payload, null, 2)}</pre></div>
            <div><div className="mb-1 text-xs text-muted">结果</div><pre className="max-h-60 overflow-auto rounded-md bg-sunken p-3 font-mono text-[11.5px]">{JSON.stringify(open.result, null, 2)}</pre></div>
          </div>
        )}
      </Sheet>
    </div>
  )
}

export function System() {
  const { data: s } = useAdmin<any>('/system', {}, { refetch: 10000 })
  if (!s) return null
  return (
    <div className="space-y-6">
      <PageHeader title="运行状态" description="服务进程与依赖的实时状态。" />
      <Panel>
        <div className="grid grid-cols-2 gap-6 sm:grid-cols-4">
          <Stat label="版本" value={`v${s.version}`} hint={[s.commit && s.commit.slice(0, 7), s.buildTime && dateTime(s.buildTime), s.go].filter(Boolean).join(' · ')} />
          <Stat label="运行时长" value={duration(Date.now() - new Date(s.startedAt).getTime())} hint={`启动于 ${dateTime(s.startedAt)}`} />
          <Stat label="内存" value={`${num(s.memoryMb)} MB`} hint={`${num(s.goroutines)} 个协程`} />
          <Stat label="在线连接" value={num(s.online)} />
        </div>
      </Panel>
      <Panel title="依赖" padded={false}>
        <Table>
          <tbody>
            <tr><td>数据库</td><td>{s.dbDriver === 'sqlite' ? 'SQLite' : 'PostgreSQL'}</td><td className="text-right text-muted">{s.dbBytes ? bytes(s.dbBytes) : ''}</td></tr>
            <tr><td>多实例协同</td><td>{s.bus === 'redis' ? <Badge tone="ok">Redis</Badge> : <Badge>单实例（未配置 Redis）</Badge>}</td><td className="text-right text-xs text-muted">{s.bus === 'redis' ? '实时消息、任务唤醒、限流和定时任务锁在各实例间共享' : '只运行一个实例时不需要 Redis'}</td></tr>
            <tr><td>文件存储</td><td>本地磁盘</td><td className="text-right text-muted">{bytes(s.storageBytes)}</td></tr>
            <tr><td>ffmpeg（音频处理）</td><td>{s.ffmpeg ? <Badge tone="ok">可用</Badge> : <Badge tone="danger">未安装</Badge>}</td><td className="text-right text-xs text-muted">{!s.ffmpeg && '合成、导出和响度标准化需要 ffmpeg'}</td></tr>
            <tr><td>邮件服务</td><td>{s.smtp ? <Badge tone="ok">已配置</Badge> : <Badge tone="warn">未配置</Badge>}</td><td className="text-right text-xs text-muted">{!s.smtp && '验证码会直接显示在页面上（开发模式）'}</td></tr>
            <tr><td>运行模式</td><td>{s.devMode ? <Badge tone="warn">开发模式</Badge> : <Badge tone="ok">生产模式</Badge>}</td><td className="text-right font-mono text-xs text-muted">{s.publicUrl}</td></tr>
          </tbody>
        </Table>
      </Panel>
    </div>
  )
}

export function Audit() {
  const [action, setAction] = useState('')
  const [page, setPage] = useState(1)
  const [open, setOpen] = useState<any>(null)
  const { data } = useAdmin<any>('/audit-logs', { action, page, pageSize: 50 })
  return (
    <div>
      <PageHeader title="操作日志" description="管理员的所有敏感操作都会记录在这里，不可删除。" />
      <div className="mb-4"><Input value={action} onChange={(e) => { setAction(e.target.value); setPage(1) }} placeholder="按操作筛选，如 user.update、studio.credits" className="w-80 font-mono text-xs" /></div>
      <Panel padded={false}>
        {!data?.items?.length ? <Empty title="没有记录" /> : (
          <Table>
            <thead><tr><th>时间</th><th>操作人</th><th>操作</th><th>对象</th><th>原因</th><th>IP</th></tr></thead>
            <tbody>{data.items.map(({ log: l, actor }: any) => <tr key={l.id} className="cursor-pointer" onClick={() => setOpen(l)}><td className="whitespace-nowrap text-xs text-faint">{dateTime(l.createdAt)}</td><td>{actor}</td><td className="font-mono text-xs">{l.action}</td><td className="text-xs text-muted">{l.targetType} <span className="font-mono">{l.targetId?.slice(0, 8)}</span></td><td className="max-w-xs truncate">{l.reason}</td><td className="font-mono text-xs text-muted">{l.ip}</td></tr>)}</tbody>
          </Table>
        )}
      </Panel>
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
      <Sheet open={!!open} onOpenChange={(o) => !o && setOpen(null)} title={open?.action} width={520}>
        {open && <pre className="whitespace-pre-wrap rounded-md bg-sunken p-3 font-mono text-[12px]">{JSON.stringify(open, null, 2)}</pre>}
      </Sheet>
    </div>
  )
}
