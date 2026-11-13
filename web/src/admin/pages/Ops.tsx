import { Pencil, Plus, RotateCcw, Trash2, X } from '@/components/icons'
import { useState } from 'react'
import { toast } from 'sonner'
import { adminApi } from '@/lib/api'
import { bytes, dateTime, duration, fromNow, jobStatusName, num } from '@/lib/format'
import { Badge, Button, Checkbox, cn, confirm, Dialog, Empty, Fade, Field, IconButton, Input, PageHeader, Pagination, Panel, Progress, Segmented, Select, Sheet, Stat, Switch, Table } from '@/components/ui'
import { ainv, useAdmin } from '../kit'

const laneName: Record<string, string> = { default: '文本与 AI 分析', audio: '配音与合成', pipeline: '一键成书' }

const tone: Record<string, any> = { queued: 'neutral', running: 'signal', succeeded: 'ok', failed: 'danger', canceled: 'neutral', paused: 'warn' }

export function Jobs() {
  const [tab, setTab] = useState(new URLSearchParams(location.search).get('tab') === 'queues' ? 'queues' : 'jobs')
  return (
    <div>
      <PageHeader title="任务队列" description="后台任务的运行情况，以及队列怎么分配：每个队列处理哪几类任务、给哪些会员用、同时运行几个。" />
      <Segmented value={tab} onChange={(v) => { setTab(v); history.replaceState(null, '', v === 'queues' ? '?tab=queues' : location.pathname) }} items={[{ value: 'jobs', label: '任务列表' }, { value: 'queues', label: '队列设置' }]} className="mb-5" />
      <Fade k={tab}>{tab === 'queues' ? <Queues /> : <JobList />}</Fade>
    </div>
  )
}

function JobList() {
  const [status, setStatus] = useState(new URLSearchParams(location.search).get('status') ?? '')
  const [lane, setLane] = useState('')
  const [type, setType] = useState('')
  const [page, setPage] = useState(1)
  const [open, setOpen] = useState<any>(null)
  const { data } = useAdmin<any>('/jobs', { status, lane, type, page, pageSize: 50 }, { refetch: 5000 })
  return (
    <div>
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <Segmented size="sm" value={status} onChange={(v) => { setStatus(v); setPage(1) }} items={[{ value: '', label: '全部' }, { value: 'running', label: '运行中' }, { value: 'queued', label: '排队' }, { value: 'failed', label: '失败' }, { value: 'succeeded', label: '完成' }]} />
        <Select size="sm" value={lane} onChange={(v) => { setLane(v); setPage(1) }} options={[['', '全部类型'], ['default', laneName.default], ['audio', laneName.audio], ['pipeline', laneName.pipeline]]} className="w-36" />
        <Input value={type} onChange={(e) => { setType(e.target.value); setPage(1) }} placeholder="任务类型，如 tts_segments" className="!h-8 w-52 font-mono text-xs" />
        <span className="text-xs text-faint">每 5 秒刷新</span>
        {status === 'failed' && <Button size="sm" className="ml-auto" onClick={async () => { const r = await adminApi.post('/jobs/retry-failed', { type }); ainv(['/jobs']); toast.success(`已重新排队 ${r?.retried ?? ''} 个任务`) }}>全部重试</Button>}
      </div>
      <Panel padded={false}>
        {!data?.items?.length ? <Empty title="没有任务" /> : (
          <Table>
            <thead><tr><th>任务</th><th>类型</th><th>任务类别</th><th>状态</th><th className="!text-right">尝试</th><th className="!text-right">积分</th><th>创建</th><th>用时</th><th /></tr></thead>
            <tbody>
              {data.items.map((j: any) => (
                <tr key={j.id} className="cursor-pointer" onClick={() => setOpen(j)}>
                  <td className="max-w-xs"><div className="truncate font-medium">{j.title}</div>{j.status === 'running' ? <Progress value={j.progress} tone="signal" height={3} className="mt-1 w-40" /> : j.error && <div className="truncate text-xs text-danger">{j.error}</div>}</td>
                  <td className="font-mono text-xs text-muted">{j.type}</td>
                  <td className="whitespace-nowrap text-muted">{laneName[j.lane] ?? j.lane}</td>
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

// Queues: what runs where. A job can be taken by any enabled queue whose kinds and plans both match it.
function Queues() {
  const { data } = useAdmin<any>('/job-queues', {}, { refetch: 5000 })
  const [edit, setEdit] = useState<any>(null)
  if (!data) return null
  const planName = (code: string) => data.plans.find((p: any) => p.code === code)?.name ?? code
  const save = async (q: any, patch: any = {}) => {
    const body = { name: q.name, lanes: q.lanes, plans: q.plans, concurrency: q.concurrency, enabled: q.enabled, note: q.note, ...patch }
    if (q.id) await adminApi.put(`/job-queues/${q.id}`, body)
    else await adminApi.post('/job-queues', body)
    ainv(['/job-queues'])
  }
  return (
    <div className="space-y-6">
      <p className="max-w-3xl text-[13px] leading-relaxed text-muted">每个队列处理哪几类任务、给哪些会员等级用、每台服务器同时运行几个，都在这里设置。一个任务可以由任何「任务类别」和「会员等级」都对得上的队列来接，所以同一个会员等级可以同时用好几个队列。比如给付费会员单独建一个加速队列，同时让他们也能用公共队列。会员等级之间谁先谁后，由「会员套餐」里的排队优先级决定。</p>
      {data.gaps?.length > 0 && (
        <div className="rounded-panel border border-danger/40 bg-danger-soft px-4 py-3 text-[13px] text-danger">
          有 {data.gaps.length} 类任务没有队列可用，会一直排队：{data.gaps.map((g: any) => `「${planName(g.plan)}」的${laneName[g.lane]}任务`).join('、')}。请编辑某个队列，让它覆盖这些会员等级。
        </div>
      )}
      <Panel title="队列" padded={false} actions={<Button size="sm" variant="primary" icon={<Plus className="size-4" />} onClick={() => setEdit({ name: '', lanes: ['pipeline'], plans: [], concurrency: 2, enabled: true, note: '' })}>新建队列</Button>}>
        <Table>
          <thead><tr><th>队列</th><th>处理的任务</th><th>可用的会员等级</th><th className="!text-right">同时运行</th><th className="!text-right">运行中</th><th className="!text-right">可接的排队任务</th><th>启用</th><th /></tr></thead>
          <tbody>
            {data.queues.map((q: any) => (
              <tr key={q.id} className={cn(!q.enabled && 'opacity-60')}>
                <td><div className="font-medium">{q.name}</div>{q.note && <div className="text-xs text-faint">{q.note}</div>}</td>
                <td><div className="flex flex-wrap gap-1">{q.lanes.map((l: string) => <Badge key={l}>{laneName[l] ?? l}</Badge>)}</div></td>
                <td>{q.plans.length ? <div className="flex flex-wrap gap-1">{q.plans.map((p: string) => <Badge key={p} tone="accent">{planName(p)}</Badge>)}</div> : <span className="text-muted">全部会员</span>}</td>
                <td className="text-right tnum">{q.concurrency}</td>
                <td className="text-right tnum">{q.running}</td>
                <td className="text-right tnum">{q.waiting}</td>
                <td><Switch checked={q.enabled} onChange={(v) => save(q, { enabled: v }).catch((e) => toast.error(e.message))} /></td>
                <td className="whitespace-nowrap text-right">
                  <IconButton label="编辑" size="xs" icon={<Pencil className="size-3.5" />} onClick={() => setEdit(q)} />
                  <IconButton label="删除" size="xs" icon={<Trash2 className="size-3.5" />} onClick={async () => {
                    if (!(await confirm({ title: `删除「${q.name}」？`, description: '正在运行的任务会继续跑完；排队中的任务改由其他能接它们的队列处理。', danger: true, confirmText: '删除' }))) return
                    try { await adminApi.del(`/job-queues/${q.id}`); ainv(['/job-queues']); toast.success('已删除') } catch (e: any) { toast.error(e.message) }
                  }} />
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
        <p className="border-t border-line px-5 py-3 text-xs text-faint">「同时运行」按每台服务器计算；部署了多台时，总数是它乘以服务器台数。「可接的排队任务」是这个队列能接的、正在排队的任务数，同一个任务可能同时算在几个队列里。</p>
      </Panel>
      <Panel title="各会员等级可用的队列" description="每一格是这个会员等级的这类任务可以用的队列，以及合计的同时运行数。" padded={false}>
        <Table>
          <thead><tr><th>会员等级</th>{data.lanes.map((l: any) => <th key={l.key}>{l.name}</th>)}</tr></thead>
          <tbody>
            {data.coverage.map((row: any) => {
              const plan = data.plans.find((p: any) => p.code === row.plan)
              return (
                <tr key={row.plan}>
                  <td><div className="font-medium">{plan?.name}</div><div className="text-xs text-faint">排队优先级 {plan?.queuePriority ?? 0}</div></td>
                  {data.lanes.map((l: any) => {
                    const c = row[l.key]
                    return (
                      <td key={l.key}>
                        {c.queues.length ? <><div className="text-[13px]">{c.queues.join('、')}</div><div className="text-xs text-faint tnum">共 {c.slots} 个同时运行</div></> : <span className="text-[13px] text-danger">没有队列，会一直排队</span>}
                      </td>
                    )
                  })}
                </tr>
              )
            })}
          </tbody>
        </Table>
      </Panel>
      <QueueDialog q={edit} data={data} onClose={() => setEdit(null)} onSave={async (q) => { await save(q); toast.success(q.id ? '已保存，新设置立即生效' : '已新建队列') }} />
    </div>
  )
}

function QueueDialog({ q, data, onClose, onSave }: { q: any; data: any; onClose: () => void; onSave: (q: any) => Promise<void> }) {
  const [f, setF] = useState<any>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  if (q && (!f || f._for !== q)) setTimeout(() => { setF({ ...q, _for: q, some: q.plans.length > 0 }); setErr('') })
  const set = (k: string, v: any) => setF({ ...f, [k]: v })
  const toggle = (k: 'lanes' | 'plans', v: string, on: boolean) => set(k, on ? [...f[k], v] : f[k].filter((x: string) => x !== v))
  const submit = async () => {
    setBusy(true)
    setErr('')
    try {
      await onSave({ ...f, plans: f.some ? f.plans : [] })
      onClose()
    } catch (e: any) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog open={!!q} onOpenChange={(v) => !v && onClose()} title={q?.id ? `编辑「${q.name}」` : '新建队列'} width={560} footer={<><Button onClick={onClose}>取消</Button><Button variant="primary" loading={busy} disabled={!f?.name?.trim() || !f?.lanes?.length || (f?.some && !f?.plans?.length)} onClick={submit}>保存</Button></>}>
      {f && (
        <div className="space-y-5">
          <Field label="名称"><Input value={f.name} onChange={(e) => set('name', e.target.value)} placeholder="如：会员加速" /></Field>
          <div className="space-y-2">
            <div className="text-[13px] font-medium">处理哪些任务</div>
            {data.lanes.map((l: any) => (
              <label key={l.key} className="flex items-start gap-2.5 rounded-[10px] border border-line px-3 py-2.5">
                <Checkbox checked={f.lanes.includes(l.key)} onChange={(v) => toggle('lanes', l.key, v)} className="mt-0.5" />
                <span><span className="block text-[13px]">{l.name}</span><span className="block text-xs text-muted">{l.hint}</span></span>
              </label>
            ))}
          </div>
          <div className="space-y-2">
            <div className="flex items-center justify-between gap-3"><span className="text-[13px] font-medium">给哪些会员等级用</span><Segmented size="sm" value={f.some ? 'some' : 'all'} onChange={(v) => set('some', v === 'some')} items={[{ value: 'all', label: '全部会员' }, { value: 'some', label: '指定会员' }]} /></div>
            {f.some && (
              <div className="flex flex-wrap gap-x-5 gap-y-2 rounded-[10px] bg-sunken/60 px-3 py-2.5">
                {data.plans.map((p: any) => <Checkbox key={p.code} checked={f.plans.includes(p.code)} onChange={(v) => toggle('plans', p.code, v)} label={p.name} />)}
              </div>
            )}
          </div>
          <Field label="同时运行几个任务" hint={`每台服务器上同时运行的数量（1 到 ${data.maxConcurrency}）。一键成书每本书占一个位置，书内会几章同时制作；大模型和语音的调用还受各供应商账号自己的并发上限约束。`}>
            <span className="block w-28"><Input type="number" min={1} max={data.maxConcurrency} value={f.concurrency} onChange={(e) => set('concurrency', Number(e.target.value) || 1)} className="tnum" /></span>
          </Field>
          <Field label="备注（可选）"><Input value={f.note ?? ''} onChange={(e) => set('note', e.target.value)} placeholder="写给其他管理员看的说明" /></Field>
          <Switch checked={f.enabled} onChange={(v) => set('enabled', v)} label="启用这个队列" />
          {err && <p className="rounded-md bg-danger-soft px-3 py-2 text-[13px] text-danger">{err}</p>}
        </div>
      )}
    </Dialog>
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
      <ErrorLogPanel />
    </div>
  )
}

// Errors users were shown only in plain words, with the full message. Users quote the reference
// (编号) from the message they saw. Background jobs keep their full error in 任务队列.
function ErrorLogPanel() {
  const [ref, setRef] = useState('')
  const [page, setPage] = useState(1)
  const [open, setOpen] = useState<any>(null)
  const { data } = useAdmin<any>('/errors', { ref, page, pageSize: 20 }, { refetch: 15000 })
  return (
    <Panel title="最近的报错" description="用户只会看到通俗的提示和一个编号，完整原因记在这里。后台任务的完整报错在「任务队列」里。" padded={false} actions={<Input value={ref} onChange={(e) => { setRef(e.target.value); setPage(1) }} placeholder="按编号查找" className="!h-8 w-40 font-mono text-xs" />}>
      {!data?.items?.length ? <Empty title={ref ? '没有这个编号' : '还没有报错'} /> : (
        <Table>
          <thead><tr><th>编号</th><th>时间</th><th>请求</th><th>用户看到的</th><th>原因</th></tr></thead>
          <tbody>
            {data.items.map((e: any) => (
              <tr key={e.id} className="cursor-pointer" onClick={() => setOpen(e)}>
                <td className="font-mono text-xs">{e.ref}</td>
                <td className="whitespace-nowrap text-xs text-faint">{fromNow(e.createdAt)}</td>
                <td className="max-w-48 truncate font-mono text-xs text-muted">{e.method} {e.path}</td>
                <td className="max-w-56 truncate text-[13px]">{e.message}</td>
                <td className="max-w-72 truncate font-mono text-xs text-danger">{e.detail}</td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      {data && data.total > data.pageSize && <div className="px-5 pb-4"><Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} /></div>}
      <Sheet open={!!open} onOpenChange={(o) => !o && setOpen(null)} title={open ? `报错 ${open.ref}` : ''} description={open && `${open.method} ${open.path} · ${dateTime(open.createdAt)}`} width={600}>
        {open && (
          <div className="space-y-4 text-[13px]">
            <div><div className="mb-1 text-xs text-muted">用户看到的</div><p>{open.message}</p></div>
            <div><div className="mb-1 text-xs text-muted">完整原因</div><pre className="whitespace-pre-wrap rounded-md bg-danger-soft p-3 text-xs text-danger">{open.detail}</pre></div>
            <div className="text-xs text-muted">用户 <span className="font-mono">{open.userId || '未登录'}</span> · 状态码 {open.status}</div>
          </div>
        )}
      </Sheet>
    </Panel>
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
