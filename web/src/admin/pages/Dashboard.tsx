import { motion } from 'framer-motion'
import { AlertTriangle, CheckCircle2, ChevronDown, Circle } from '@/components/icons'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { duration, num, pct } from '@/lib/format'
import { Badge, Button, cn, PageHeader, Panel, Skeleton, Stat, Table, Tip } from '@/components/ui'
import { useAdmin } from '../kit'

function Bars({ data, value, label }: { data: any[]; value: (d: any) => number; label: (d: any) => string }) {
  const max = Math.max(1, ...data.map(value))
  return (
    <div>
      <div className="flex h-28 items-end gap-1">
        {data.map((d, i) => (
          <Tip key={i} label={label(d)}>
            <motion.div className="min-w-1 max-w-8 flex-1 rounded-t-[2px] bg-accent/80 hover:bg-accent" initial={{ height: 0 }} animate={{ height: `${Math.max(2, (value(d) / max) * 100)}%` }} transition={{ type: 'spring', stiffness: 140, damping: 20, delay: i * 0.01 }} />
          </Tip>
        ))}
      </div>
      <div className="mt-1 flex justify-between text-[11px] text-faint"><span>{data[0]?.Day}</span><span>{data[data.length - 1]?.Day}</span></div>
    </div>
  )
}

export default function Dashboard() {
  const { data: d } = useAdmin<any>('/dashboard', {}, { refetch: 30000 })
  if (!d) return <div className="space-y-4"><Skeleton className="h-24" /><Skeleton className="h-60" /></div>
  return (
    <div className="space-y-6">
      <PageHeader title="数据看板" description="平台整体运行情况，每 30 秒刷新。" />
      <LaunchChecklist />
      {d.alerts?.length > 0 && (
        <div className="space-y-2">
          {d.alerts.map((a: any, i: number) => (
            <Link key={i} to={a.link?.replace(/^\/admin/, '') || '/'} className={`flex items-center gap-3 rounded-panel border px-4 py-3 text-[13px] ${a.level === 'danger' ? 'border-danger/40 bg-danger-soft text-danger' : a.level === 'warning' ? 'border-warn/40 bg-warn-soft text-warn' : 'border-accent/30 bg-accent-soft text-accent'}`}>
              <AlertTriangle className="size-4 shrink-0" /><span className="font-medium">{a.title}</span><span className="opacity-80">{a.detail}</span>
            </Link>
          ))}
        </div>
      )}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Panel><Stat label="注册用户" value={num(d.users.total)} hint={`7 天新增 ${num(d.users.new7d)}`} /></Panel>
        <Panel><Stat label="日活 / 月活" value={`${num(d.users.dau)} / ${num(d.users.mau)}`} hint={`当前在线 ${num(d.online)}`} /></Panel>
        <Panel><Stat label="工作室" value={num(d.studios.total)} hint={`团队 ${d.studios.team} · 付费 ${d.studios.paying}`} /></Panel>
        <Panel><Stat label="30 天消耗积分" value={num(d.credits.consumed30d)} hint={`发放 ${num(d.credits.granted30d)} · 成本 ¥${(d.credits.cost30d ?? 0).toFixed(2)}`} /></Panel>
      </div>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Panel><Stat label="图书" value={num(d.content.books)} hint={`${num(d.content.chapters)} 章`} /></Panel>
        <Panel><Stat label="成品总时长" value={duration(d.content.audioMs)} /></Panel>
        <Panel><Stat label="24 小时合成字数" value={num(d.content.chars24h)} /></Panel>
        <Panel><Stat label="24 小时成品" value={num(d.content.renders24h)} hint="章" /></Panel>
      </div>
      <div className="grid gap-6 lg:grid-cols-2">
        <Panel title="每日注册"><Bars data={d.signupSeries ?? []} value={(x) => x.N} label={(x) => `${x.Day}：${x.N} 人`} /></Panel>
        <Panel title="每日积分消耗"><Bars data={d.usageSeries ?? []} value={(x) => x.Credits} label={(x) => `${x.Day}：${num(x.Credits)} 积分，${num(x.Chars)} 字，成本 ¥${(x.Cost ?? 0).toFixed(2)}`} /></Panel>
      </div>
      <div className="grid gap-6 lg:grid-cols-[1fr_340px]">
        <Panel title="供应商健康度" padded={false}>
          <Table>
            <thead><tr><th>名称</th><th>类型</th><th className="!text-right">调用</th><th className="!text-right">成功率</th><th className="!text-right">平均延迟</th><th>状态</th></tr></thead>
            <tbody>{(d.providers ?? []).map((p: any) => <tr key={p.id}><td className="font-medium">{p.name}</td><td className="text-muted">{p.kind}</td><td className="text-right tnum">{num(p.calls)}</td><td className="text-right tnum">{pct(p.successRate)}</td><td className="text-right tnum">{p.avgLatencyMs ? `${num(p.avgLatencyMs)} ms` : '—'}</td><td>{!p.enabled ? <Badge>已停用</Badge> : p.health === 'ok' ? <Badge tone="ok">正常</Badge> : p.health === 'error' ? <Badge tone="danger">异常</Badge> : <Badge>未检测</Badge>}</td></tr>)}</tbody>
          </Table>
        </Panel>
        <Panel title="任务队列" padded={false}>
          {!(d.lanes ?? []).length ? <p className="px-5 py-8 text-center text-[13px] text-muted">队列空闲</p> : (
            <Table>
              <thead><tr><th>通道</th><th className="!text-right">排队</th><th className="!text-right">运行</th></tr></thead>
              <tbody>{Array.from(new Set<string>(d.lanes.map((l: any) => l.Lane))).map((lane) => {
                const n = (st: string) => d.lanes.find((l: any) => l.Lane === lane && l.Status === st)?.N ?? 0
                return <tr key={lane}><td>{({ default: '常规', audio: '音频', pipeline: '一键成书' } as any)[lane] ?? lane}</td><td className="text-right tnum">{n('queued')}</td><td className="text-right tnum">{n('running')}</td></tr>
              })}</tbody>
            </Table>
          )}
        </Panel>
      </div>
    </div>
  )
}

type LaunchItem = { key: string; title: string; ok: boolean; level: 'required' | 'recommended'; detail: string; fixAt?: string }

/** What still needs doing before real users arrive. Collapses to one line once everything required is done. */
function LaunchChecklist() {
  const { data } = useAdmin<LaunchItem[]>('/launch-check', {}, { refetch: 60000 })
  const items = data ?? []
  const todo = items.filter((i) => !i.ok)
  const required = todo.filter((i) => i.level === 'required')
  const [open, setOpen] = useState<boolean | null>(null)
  if (!items.length) return null
  const expanded = open ?? required.length > 0
  return (
    <Panel padded={false}>
      <button onClick={() => setOpen(!expanded)} className="flex w-full items-center gap-3 px-5 py-3.5 text-left">
        {required.length ? <AlertTriangle className="size-5 shrink-0 text-warn" /> : <CheckCircle2 className="size-5 shrink-0 text-ok" />}
        <div className="min-w-0 flex-1">
          <div className="text-[14px] font-semibold">上线前检查</div>
          <div className="text-[12.5px] text-muted">
            {required.length ? `还有 ${required.length} 项必须处理` : '必须项已全部完成'}
            {todo.length - required.length > 0 && `，${todo.length - required.length} 项建议处理`}
            {!todo.length && '，所有项目都已就绪'}
          </div>
        </div>
        <ChevronDown className={cn('size-4 text-faint transition-transform', expanded && 'rotate-180')} />
      </button>
      {expanded && (
        <ul className="border-t border-line">
          {items.map((i) => (
            <li key={i.key} className="flex items-start gap-3 border-t border-line px-5 py-3 first:border-t-0">
              {i.ok ? <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-ok" /> : i.level === 'required' ? <AlertTriangle className="mt-0.5 size-4 shrink-0 text-danger" /> : <Circle className="mt-0.5 size-4 shrink-0 text-warn" />}
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2 text-[13.5px] font-medium">{i.title}{!i.ok && <Badge tone={i.level === 'required' ? 'danger' : 'warn'}>{i.level === 'required' ? '必须' : '建议'}</Badge>}</div>
                <div className="mt-0.5 text-[12.5px] leading-relaxed text-muted">{i.detail}</div>
              </div>
              {!i.ok && i.fixAt && <Link to={i.fixAt}><Button size="xs">去处理</Button></Link>}
              {!i.ok && i.key === 'admin_password' && <Button size="xs" onClick={() => window.dispatchEvent(new Event('vb:admin-password'))}>修改密码</Button>}
            </li>
          ))}
        </ul>
      )}
    </Panel>
  )
}
