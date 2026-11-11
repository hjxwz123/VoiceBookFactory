import { useQuery } from '@tanstack/react-query'
import { motion } from 'framer-motion'
import { Download, ExternalLink, KanbanSquare } from '@/components/icons'
import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { toast } from 'sonner'
import { api, qs } from '@/lib/api'
import { date, fromNow, issueStatusName, num, pct, yuan } from '@/lib/format'
import { canManage, useSession, useStudio } from '@/lib/session'
import { Avatar, Badge, Button, Checkbox, cn, DateInput, Dialog, Empty, Field, PageHeader, Pagination, Panel, Segmented, Select, Stat, Table, Tabs, Textarea, Tip } from '@/components/ui'
import { invalidate, useMembers } from '../hooks'

export default function Production() {
  const [tab, setTab] = useState<'board' | 'issues' | 'stats' | 'payroll'>('board')
  const studio = useStudio()
  return (
    <div>
      <PageHeader title="生产管理" description="团队协作时，每章会按流程拆成任务分配给成员。在这里跟进进度、处理返修、查看产能并结算计件工资。" />
      <Tabs className="mb-6" value={tab} onChange={setTab} items={[{ value: 'board', label: '任务看板' }, { value: 'issues', label: '工单' }, { value: 'stats', label: '数据统计' }, ...(canManage(studio?.role) ? [{ value: 'payroll' as const, label: '计件结算' }] : [])]} />
      {tab === 'board' && <Board />}
      {tab === 'issues' && <Issues />}
      {tab === 'stats' && <Stats />}
      {tab === 'payroll' && <Payroll />}
    </div>
  )
}

const cols = [
  { key: 'pending', label: '未开始' },
  { key: 'in_progress', label: '进行中' },
  { key: 'reviewing', label: '待审核' },
  { key: 'rejected', label: '已打回' },
  { key: 'done', label: '已完成' },
]

function Board() {
  const sid = useSession((s) => s.studioId)
  const me = useSession((s) => s.user)
  const [mine, setMine] = useState('all')
  const [book, setBook] = useState('')
  const { data, isLoading } = useQuery<any[]>({ queryKey: ['tasks', sid, mine, book], queryFn: () => api.get(`/studios/${sid}/tasks${qs({ assigneeId: mine === 'mine' ? me?.id : '', bookId: book, pageSize: 500 })}`) })
  const [act, setAct] = useState<{ t: any; action: string } | null>(null)
  const books = useMemo(() => Array.from(new Map((data ?? []).map((t) => [t.task.bookId, t.bookTitle])).entries()), [data])
  const list = (data ?? []).filter((t) => !book || t.task.bookId === book)
  const run = async (t: any, action: string, extra: any = {}) => {
    await api.post(`/tasks/${t.task.id}/action`, { action, ...extra })
    invalidate(['tasks'])
    toast.success('已更新')
  }
  if (!isLoading && !(data ?? []).length)
    return <Panel padded={false}><Empty icon={<KanbanSquare />} title="还没有生产任务" description="在图书的「设置 → 生产流程」中开启流程后，每章会自动拆分成画本、配音、后期、审听等任务。" /></Panel>
  return (
    <div>
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <Segmented size="sm" value={mine} onChange={setMine} items={[{ value: 'all', label: '全部任务' }, { value: 'mine', label: '分配给我的' }]} />
        <Select size="sm" value={book} onChange={setBook} options={[['', '全部图书'], ...books] as [string, string][]} className="w-44" />
      </div>
      <div className="grid gap-4 overflow-x-auto pb-2 lg:grid-cols-5">
        {cols.map((c) => {
          const items = list.filter((t) => t.task.status === c.key)
          return (
            <div key={c.key} className="min-w-[240px] rounded-panel bg-sunken/60 p-2">
              <div className="flex items-center justify-between px-2 py-1.5 text-[12.5px] font-medium">{c.label}<span className="text-faint tnum">{items.length}</span></div>
              <div className="space-y-2">
                {items.slice(0, 60).map((t) => (
                  <motion.div layout key={t.task.id} className="rounded-[10px] border border-line bg-surface p-3">
                    <div className="flex items-center gap-2">
                      <Badge tone="accent">{t.task.stageName}</Badge>
                      {t.overdue && <Badge tone="danger">已逾期</Badge>}
                      {t.task.reworks > 0 && <Tip label={`返工 ${t.task.reworks} 次`}><Badge tone="warn">返工</Badge></Tip>}
                    </div>
                    <Link to={`/books/${t.task.bookId}/chapters/${t.task.chapterId}`} className="mt-2 block text-[13px] font-medium hover:text-accent">《{t.bookTitle}》第 {t.chapterIndex} 章</Link>
                    {t.characterName && <div className="mt-0.5 flex items-center gap-1 text-xs text-muted"><span className="size-1.5 rounded-full" style={{ background: t.characterColor }} />{t.characterName}</div>}
                    <div className="mt-2 flex items-center gap-2 text-[11.5px] text-faint">
                      <Avatar name={t.assignee} src={t.assigneeAvatar} size={18} />
                      <span className="truncate">{t.assignee || '未分配'}</span>
                      <span className="ml-auto">{t.task.dueAt ? `${date(t.task.dueAt).slice(5)} 截止` : ''}</span>
                    </div>
                    {t.task.status === 'rejected' && t.task.rejectReason && <p className="mt-2 rounded bg-danger-soft px-2 py-1 text-[11.5px] text-danger">{t.task.rejectReason}</p>}
                    <div className="mt-2.5 flex flex-wrap gap-1">
                      {t.task.status === 'pending' && t.task.assigneeId === me?.id && <Button size="xs" onClick={() => run(t, 'start')}>开始</Button>}
                      {(t.task.status === 'in_progress' || t.task.status === 'rejected') && t.task.assigneeId === me?.id && <Button size="xs" variant="primary" onClick={() => run(t, 'submit')}>提交</Button>}
                      {t.task.status === 'reviewing' && <><Button size="xs" onClick={() => setAct({ t, action: 'reject' })}>打回</Button><Button size="xs" variant="primary" onClick={() => run(t, 'approve')}>通过</Button></>}
                      {t.task.status !== 'done' && <Button size="xs" variant="ghost" onClick={() => setAct({ t, action: 'reassign' })}>转派</Button>}
                      {t.task.status === 'done' && !t.task.settled && <Button size="xs" variant="ghost" onClick={() => run(t, 'reopen')}>重开</Button>}
                    </div>
                  </motion.div>
                ))}
              </div>
            </div>
          )
        })}
      </div>
      <TaskActionDialog act={act} onClose={() => setAct(null)} run={run} />
    </div>
  )
}

function TaskActionDialog({ act, onClose, run }: { act: { t: any; action: string } | null; onClose: () => void; run: (t: any, a: string, e?: any) => Promise<void> }) {
  const members = useMembers()
  const [reason, setReason] = useState('')
  const [assignee, setAssignee] = useState('')
  return (
    <Dialog open={!!act} onOpenChange={(o) => !o && onClose()} title={act?.action === 'reject' ? '打回任务' : '转派任务'} width={420} footer={<><Button onClick={onClose}>取消</Button><Button variant="primary" onClick={async () => { await run(act!.t, act!.action, act!.action === 'reject' ? { reason } : { assigneeId: assignee }); onClose() }}>确定</Button></>}>
      {act?.action === 'reject' ? (
        <Field label="打回原因"><Textarea rows={3} value={reason} onChange={(e) => setReason(e.target.value)} /></Field>
      ) : (
        <Field label="转派给"><Select value={assignee} onChange={setAssignee} placeholder="选择成员" options={(members.data ?? []).map((m: any) => [m.userId, m.nickname] as [string, string])} /></Field>
      )}
    </Dialog>
  )
}

function Issues() {
  const sid = useSession((s) => s.studioId)
  const [status, setStatus] = useState('active')
  const [page, setPage] = useState(1)
  const { data } = useQuery({ queryKey: ['issues', sid, status, page], queryFn: () => api.get(`/studios/${sid}/issues${qs({ status, page, pageSize: 30 })}`), placeholderData: (p) => p })
  return (
    <Panel padded={false}>
      <div className="border-b border-line px-5 py-3"><Segmented size="sm" value={status} onChange={(v) => { setStatus(v); setPage(1) }} items={[{ value: 'active', label: '未关闭' }, { value: 'verifying', label: '待复核' }, { value: 'disputed', label: '有异议' }, { value: 'closed', label: '已关闭' }]} /></div>
      {!data?.items?.length ? <Empty title="没有工单" /> : (
        <Table>
          <thead><tr><th>问题</th><th>图书 / 章节</th><th>处理人</th><th>状态</th><th>时间</th><th /></tr></thead>
          <tbody>
            {data.items.map((it: any) => (
              <tr key={it.issue.id}>
                <td className="max-w-xs"><div className="flex items-center gap-2"><Badge>{it.typeName}</Badge><span className="truncate font-medium">{it.issue.title}</span></div><div className="mt-1 truncate text-xs text-muted">「{it.segmentText}」</div></td>
                <td className="whitespace-nowrap text-muted">{it.bookTitle ?? ''} 第 {it.chapterIndex} 章</td>
                <td>{it.assignee || <span className="text-faint">未指派</span>}</td>
                <td><Badge tone={it.issue.status === 'closed' ? 'neutral' : it.issue.status === 'verifying' ? 'warn' : 'danger'}>{issueStatusName[it.issue.status]}</Badge></td>
                <td className="whitespace-nowrap text-xs text-faint">{fromNow(it.issue.createdAt)}</td>
                <td><Link to={`/books/${it.issue.bookId}/chapters/${it.issue.chapterId}?segment=${it.issue.segmentId}`}><Button size="xs" variant="ghost" icon={<ExternalLink className="size-3.5" />}>定位</Button></Link></td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      {data && <div className="px-5 pb-4"><Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} /></div>}
    </Panel>
  )
}

function Stats() {
  const sid = useSession((s) => s.studioId)
  const [days, setDays] = useState('30')
  const from = new Date(Date.now() - Number(days) * 86400000).toISOString().slice(0, 10)
  const { data } = useQuery({ queryKey: ['stats', sid, days], queryFn: () => api.get(`/studios/${sid}/stats?from=${from}`) })
  const series: any[] = data?.series ?? []
  const max = Math.max(1, ...series.map((s) => s.words))
  return (
    <div className="space-y-6">
      <Segmented size="sm" value={days} onChange={setDays} items={[{ value: '7', label: '近 7 天' }, { value: '30', label: '近 30 天' }, { value: '90', label: '近 90 天' }]} />
      <Panel>
        <div className="grid grid-cols-2 gap-6 sm:grid-cols-5">
          <Stat label="完成字数" value={num(data?.words)} />
          <Stat label="完成任务" value={num(data?.tasksDone)} />
          <Stat label="一次通过率" value={pct(data?.firstPassRate)} />
          <Stat label="返工率" value={pct(data?.reworkRate)} />
          <Stat label="消耗积分" value={num(data?.credits)} />
        </div>
        <div className="mt-6">
          <div className="mb-2 text-xs text-muted">每日完成字数</div>
          <div className="flex h-32 items-end gap-[3px]">
            {series.map((s) => (
              <Tip key={s.date} label={`${s.date}：${num(s.words)} 字`}>
                <motion.div className="min-w-[3px] max-w-8 flex-1 rounded-t-[2px] bg-accent/80 hover:bg-accent" initial={{ height: 0 }} animate={{ height: `${Math.max(1.5, (s.words / max) * 100)}%` }} transition={{ type: 'spring', stiffness: 140, damping: 20 }} />
              </Tip>
            ))}
          </div>
          <div className="mt-1 flex justify-between text-[11px] text-faint"><span>{series[0]?.date}</span><span>{series[series.length - 1]?.date}</span></div>
        </div>
      </Panel>
      <div className="grid gap-6 lg:grid-cols-2">
        <Panel title="成员产能" padded={false}>
          <Table>
            <thead><tr><th>成员</th><th className="!text-right">字数</th><th className="!text-right">任务</th><th className="!text-right">通过率</th><th className="!text-right">积分</th></tr></thead>
            <tbody>{(data?.members ?? []).map((m: any) => <tr key={m.userId}><td><span className="flex items-center gap-2"><Avatar name={m.nickname} size={22} />{m.nickname}</span></td><td className="text-right tnum">{num(m.words)}</td><td className="text-right tnum">{m.tasks}</td><td className="text-right tnum">{pct(m.passRate)}</td><td className="text-right tnum">{num(m.credits)}</td></tr>)}</tbody>
          </Table>
        </Panel>
        <Panel title="积分去向" padded={false}>
          <Table>
            <thead><tr><th>功能</th><th className="!text-right">积分</th></tr></thead>
            <tbody>{(data?.byFeature ?? []).map((f: any) => <tr key={f.feature}><td>{f.name}</td><td className="text-right tnum">{num(f.credits)}</td></tr>)}</tbody>
          </Table>
          <div className="border-t border-line px-4 py-2 text-xs text-muted">按图书：{(data?.byBook ?? []).map((b: any) => `《${b.title}》${num(b.credits)}（每万字 ${num(b.per10k)}）`).join('，') || '—'}</div>
        </Panel>
      </div>
    </div>
  )
}

function Payroll() {
  const sid = useSession((s) => s.studioId)
  const [all, setAll] = useState(false)
  const [sel, setSel] = useState<string[]>([])
  const [from, setFrom] = useState(new Date(Date.now() - 30 * 86400000).toISOString().slice(0, 10))
  const [to, setTo] = useState(new Date().toISOString().slice(0, 10))
  const { data, refetch } = useQuery({ queryKey: ['payroll', sid, all, from, to], queryFn: () => api.get(`/studios/${sid}/payroll${qs({ all: all ? 1 : '', from, to })}`) })
  const rows: any[] = data?.rows ?? []
  const unsettled = rows.filter((r) => !r.settled)
  return (
    <Panel padded={false}>
      <div className="flex flex-wrap items-center gap-3 border-b border-line px-5 py-3">
        <DateInput size="sm" value={from} onChange={setFrom} placeholder="开始日期" className="w-40" />
        <span className="text-faint">至</span>
        <DateInput size="sm" value={to} onChange={setTo} placeholder="结束日期" className="w-40" />
        <Checkbox checked={all} onChange={setAll} label="包含已结算" />
        <div className="ml-auto flex gap-2">
          <Button size="sm" icon={<Download className="size-4" />} onClick={async () => { const r = await api.get(`/studios/${sid}/payroll/export${qs({ from, to })}`); window.location.href = r.url }}>导出 Excel</Button>
          <Button size="sm" variant="primary" disabled={!sel.length} onClick={async () => { await api.post(`/studios/${sid}/payroll/settle`, { taskIds: sel }); setSel([]); refetch(); toast.success('已标记为已结算') }}>标记已结算（{sel.length}）</Button>
        </div>
      </div>
      <div className="flex flex-wrap gap-6 border-b border-line px-5 py-3 text-[13px]">
        {Object.entries(data?.totals ?? {}).map(([name, cent]: any) => <span key={name}>{name}：<span className="font-semibold tnum">{yuan(cent)}</span></span>)}
        {!Object.keys(data?.totals ?? {}).length && <span className="text-muted">这段时间没有待结算的任务。计件单价在「工作室设置 → 计件单价」中设置。</span>}
      </div>
      {rows.length > 0 && (
        <Table>
          <thead><tr><th><Checkbox checked={sel.length && sel.length === unsettled.length ? true : sel.length ? 'indeterminate' : false} onChange={(v) => setSel(v ? unsettled.map((r) => r.taskId) : [])} /></th><th>成员</th><th>内容</th><th>环节</th><th className="!text-right">工作量</th><th className="!text-right">单价</th><th className="!text-right">金额</th><th>完成</th></tr></thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.taskId} className={cn(r.settled && 'opacity-50')}>
                <td>{!r.settled && <Checkbox checked={sel.includes(r.taskId)} onChange={(v) => setSel(v ? [...sel, r.taskId] : sel.filter((x) => x !== r.taskId))} />}</td>
                <td>{r.nickname}</td>
                <td className="max-w-xs truncate">《{r.book}》{r.chapter}{r.character && ` · ${r.character}`}</td>
                <td>{r.stage}{r.reworks > 0 && <span className="ml-1 text-xs text-warn">返工{r.reworks}</span>}</td>
                <td className="text-right tnum">{num(r.measure)} {r.unit === 'words' ? '字' : r.unit === 'minutes' ? '分钟' : r.unit}</td>
                <td className="text-right tnum">{yuan(r.rateCent)}</td>
                <td className="text-right font-medium tnum">{yuan(r.amountCent)}</td>
                <td className="whitespace-nowrap text-xs text-faint">{date(r.completedAt)}{r.settled && ' · 已结算'}</td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </Panel>
  )
}
