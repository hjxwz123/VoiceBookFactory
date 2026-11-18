import { useState } from 'react'
import { Link } from 'react-router-dom'
import { duration, num, pct } from '@/lib/format'
import { Panel, Segmented, Skeleton, Table, Tabs } from '@/components/ui'
import { useAdmin } from '../kit'
import { BarList, compact, Funnel, Kpi, TrendChart, type Part } from '../charts'

type Pair = { cur: number; prev: number }
const yuan = (fen: number) => `¥${compact(Math.round(fen) / 100)}`
const cny = (v: number) => `¥${v >= 100 ? compact(Math.round(v)) : v.toFixed(2)}`
const sumParts = (p: Part[] = []) => p.reduce((a, x) => a + x.value, 0)
const ratio = (a: number, b: number) => (b ? a / b : 0)

function Tiles({ children }: { children: React.ReactNode }) {
  return (
    <Panel padded={false}>
      <div className="grid grid-cols-2 gap-px overflow-hidden rounded-panel bg-line lg:grid-cols-4 [&>*]:bg-surface [&>*]:px-5 [&>*]:py-4">{children}</div>
    </Panel>
  )
}

/** The detailed part of the dashboard: one period, compared with the period before it. */
export function Analytics() {
  const [days, setDays] = useState('30')
  const [tab, setTab] = useState<'growth' | 'revenue' | 'ai' | 'production'>('growth')
  const { data: a, isFetching } = useAdmin<any>('/dashboard/analytics', { days }, { refetch: 120000 })
  return (
    <section className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3 pt-2">
        <div>
          <h2 className="font-serif text-[18px] font-semibold">经营分析</h2>
          <p className="mt-0.5 text-[12.5px] text-muted">最近 {days} 天，和之前 {days} 天对比{isFetching && a ? ' · 更新中…' : ''}</p>
        </div>
        <Segmented size="sm" value={days} onChange={setDays} items={[{ value: '7', label: '7 天' }, { value: '30', label: '30 天' }, { value: '90', label: '90 天' }]} />
      </div>
      <Tabs value={tab} onChange={setTab} items={[{ value: 'growth', label: '用户与增长' }, { value: 'revenue', label: '收入与积分' }, { value: 'ai', label: 'AI 成本' }, { value: 'production', label: '制作与任务' }]} />
      {!a ? <div className="space-y-4"><Skeleton className="h-24" /><Skeleton className="h-64" /></div> : (
        <>
          {tab === 'growth' && <Growth a={a} />}
          {tab === 'revenue' && <Revenue a={a} />}
          {tab === 'ai' && <AICost a={a} />}
          {tab === 'production' && <Production a={a} />}
        </>
      )}
    </section>
  )
}

function Growth({ a }: { a: any }) {
  const g = a.growth
  const signup = g.funnel[0]?.value ?? 0
  const paid = g.funnel[4]?.value ?? 0
  return (
    <div className="space-y-4">
      <Tiles>
        <Kpi label="新注册用户" value={num(g.newUsers.cur)} cur={g.newUsers.cur} prev={g.newUsers.prev} />
        <Kpi label="活跃用户" value={num(g.activeUsers)} hint="这段时间登录过的人" />
        <Kpi label="创作用户" value={num(g.creators)} hint="用过 AI 生成功能的人" />
        <Kpi label="新用户付费转化" value={pct(ratio(paid, signup))} hint={`${num(paid)} / ${num(signup)} 人`} />
      </Tiles>
      <Panel title="每日新注册与创作用户">
        <TrendChart days={a.dayList} series={[{ key: 'new', label: '新注册', values: g.series.newUsers }, { key: 'creators', label: '创作用户', values: g.series.creators }]} />
      </Panel>
      <div className="grid gap-4 lg:grid-cols-3">
        <Panel title="新用户转化" description="这段时间注册的人，走到了哪一步"><Funnel steps={g.funnel} /></Panel>
        <Panel title="注册方式"><BarList items={g.methods} format={num} empty="这段时间没有新用户" /></Panel>
        <Panel title="注册渠道" description="同一人可能既用了注册码也来自邀请"><BarList items={g.channels} format={num} empty="这段时间没有新用户" /></Panel>
      </div>
    </div>
  )
}

function Revenue({ a }: { a: any }) {
  const r = a.revenue
  const arppu = ratio(r.amount.cur, r.payers.cur)
  const arppuPrev = ratio(r.amount.prev, r.payers.prev)
  return (
    <div className="space-y-4">
      <Tiles>
        <Kpi label="充值金额" value={yuan(r.amount.cur)} cur={r.amount.cur} prev={r.amount.prev} hint={`${num(r.payments.cur)} 笔`} />
        <Kpi label="付费用户" value={num(r.payers.cur)} cur={r.payers.cur} prev={r.payers.prev} />
        <Kpi label="客单价" value={yuan(arppu)} cur={arppu} prev={arppuPrev} hint="每位付费用户" />
        <Kpi label="新开通会员" value={num(sumParts(r.newMembers))} hint={`试用 ${num(r.trials.cur)} · 7 天内到期 ${num(r.expiring7d)}`} />
      </Tiles>
      <div className="grid gap-4 lg:grid-cols-2">
        <Panel title="每日充值金额"><TrendChart days={a.dayList} series={[{ key: 'amount', label: '充值金额', values: r.series.amount.map((v: number) => v / 100) }]} format={(v) => `¥${compact(v)}`} /></Panel>
        <Panel title="每日积分发放与消耗"><TrendChart days={a.dayList} series={[{ key: 'consumed', label: '消耗', values: r.series.consumed }, { key: 'granted', label: '发放', values: r.series.granted }]} /></Panel>
      </div>
      <Tiles>
        <Kpi label="消耗积分" value={compact(r.consumed.cur)} cur={r.consumed.cur} prev={r.consumed.prev} />
        <Kpi label="发放积分" value={compact(r.grantedSum.cur)} cur={r.grantedSum.cur} prev={r.grantedSum.prev} goodUp={false} />
        <Kpi label="用户手里未用的积分" value={compact(r.outstanding)} hint="未过期的余额合计" />
        <Kpi label="兑换码兑换" value={num(r.redeemed.cur)} cur={r.redeemed.cur} prev={r.redeemed.prev} />
      </Tiles>
      <div className="grid gap-4 lg:grid-cols-3">
        <Panel title="当前会员分布"><BarList items={r.members} format={num} /></Panel>
        <Panel title="积分从哪里发出"><BarList items={r.granted} /></Panel>
        <Panel title="充值来源"><BarList items={r.bySource} format={(v) => yuan(v)} empty="这段时间没有充值" /></Panel>
      </div>
    </div>
  )
}

function AICost({ a }: { a: any }) {
  const ai = a.ai
  const consumed = a.revenue.consumed
  const per = ratio(ai.cost.cur, consumed.cur) * 10000
  const perPrev = ratio(ai.cost.prev, consumed.prev) * 10000
  const totalCost = ai.byFeature.reduce((x: number, f: any) => x + f.cost, 0)
  return (
    <div className="space-y-4">
      <Tiles>
        <Kpi label="AI 成本" value={cny(ai.cost.cur)} cur={ai.cost.cur} prev={ai.cost.prev} goodUp={false} />
        <Kpi label="每万积分成本" value={cny(per)} cur={per} prev={perPrev} goodUp={false} hint="越低毛利越高" />
        <Kpi label="AI 调用" value={compact(ai.calls.cur)} cur={ai.calls.cur} prev={ai.calls.prev} />
        <Kpi label="配音字数" value={compact(ai.ttsChars.cur)} cur={ai.ttsChars.cur} prev={ai.ttsChars.prev} hint={ai.byok ? `自带密钥 ${compact(ai.byok)} 积分` : undefined} />
      </Tiles>
      <Panel title="每日 AI 成本"><TrendChart days={a.dayList} series={[{ key: 'cost', label: 'AI 成本', values: ai.series.cost }]} format={(v) => `¥${compact(v)}`} /></Panel>
      <Panel title="按功能" padded={false}>
        <Table>
          <thead><tr><th>功能</th><th className="!text-right">调用</th><th className="!text-right">用量</th><th className="!text-right">消耗积分</th><th className="!text-right">成本</th><th className="w-48">成本占比</th></tr></thead>
          <tbody>
            {ai.byFeature.map((f: any) => (
              <tr key={f.key}>
                <td>{f.label}</td>
                <td className="text-right tnum">{num(f.calls)}</td>
                <td className="text-right tnum text-muted">{compact(f.quantity)}</td>
                <td className="text-right tnum">{compact(f.credits)}</td>
                <td className="text-right font-medium tnum">{cny(f.cost)}</td>
                <td><div className="flex items-center gap-2"><div className="h-2 flex-1 rounded-r-[4px]"><div className="h-full rounded-r-[4px] bg-[var(--chart-1)]" style={{ width: `${ratio(f.cost, totalCost) * 100}%` }} /></div><span className="w-10 text-right text-[11.5px] text-faint tnum">{(ratio(f.cost, totalCost) * 100).toFixed(0)}%</span></div></td>
              </tr>
            ))}
            {!ai.byFeature.length && <tr><td colSpan={6} className="py-6 text-center text-faint">这段时间没有 AI 调用</td></tr>}
          </tbody>
        </Table>
      </Panel>
      <div className="grid gap-4 lg:grid-cols-3">
        <Panel title="按供应商账号的成本"><BarList items={ai.byAccount} format={cny} sub={(p) => (p.extra ? `${compact(p.extra)} 次` : undefined)} /></Panel>
        <Panel title="配音字数（按音色价位）"><BarList items={ai.byTier} /></Panel>
        <Panel title="消耗最多的工作室" padded={false}>
          <Table>
            <tbody>
              {ai.topStudios.map((t: any) => <tr key={t.id}><td className="max-w-40 truncate"><Link to={`/studios?id=${t.id}`} className="hover:text-accent">{t.name}</Link></td><td className="text-right tnum">{compact(t.credits)} 积分</td><td className="text-right text-muted tnum">{cny(t.cost)}</td></tr>)}
              {!ai.topStudios.length && <tr><td className="py-6 text-center text-faint">暂无数据</td></tr>}
            </tbody>
          </Table>
        </Panel>
      </div>
    </div>
  )
}

function Production({ a }: { a: any }) {
  const p = a.production
  const rel = a.reliability
  const pipeDone = p.pipelines.find((x: Part) => x.key === 'completed')?.value ?? 0
  const pipeAll = sumParts(p.pipelines)
  return (
    <div className="space-y-4">
      <Tiles>
        <Kpi label="新建图书" value={num(p.books.cur)} cur={p.books.cur} prev={p.books.prev} />
        <Kpi label="合成章节" value={num(p.renders.cur)} cur={p.renders.cur} prev={p.renders.prev} />
        <Kpi label="成品时长" value={duration(p.audioMs.cur)} cur={p.audioMs.cur} prev={p.audioMs.prev} />
        <Kpi label="导出" value={num(p.exports.cur)} cur={p.exports.cur} prev={p.exports.prev} hint={`克隆 ${num(p.clones.cur)} · 设计音色 ${num(p.designs.cur)}`} />
      </Tiles>
      <div className="grid gap-4 lg:grid-cols-[2fr_1fr]">
        <Panel title="每日合成的成品时长（分钟）"><TrendChart days={a.dayList} series={[{ key: 'audio', label: '成品时长', values: p.series.audioMs.map((v: number) => Math.round(v / 60000)) }]} format={(v) => compact(v)} /></Panel>
        <Panel title="一键成书" description={pipeAll ? `完成率 ${pct(ratio(pipeDone, pipeAll))}${p.pipelineMedianMs ? ` · 一本书中位耗时 ${duration(p.pipelineMedianMs)}` : ''}` : undefined}><BarList items={p.pipelines} format={num} empty="这段时间没有一键成书" /></Panel>
      </div>
      <div className="grid gap-4 lg:grid-cols-3">
        <Panel title="图书类型"><BarList items={p.modes} format={num} empty="这段时间没有新图书" /></Panel>
        <Panel title="失败任务与用户看到的报错" className="lg:col-span-2">
          <div className="mb-3 flex gap-8">
            <Kpi label="失败任务" value={num(rel.failedJobs.cur)} cur={rel.failedJobs.cur} prev={rel.failedJobs.prev} goodUp={false} />
            <Kpi label="用户看到的报错" value={num(rel.errors.cur)} cur={rel.errors.cur} prev={rel.errors.prev} goodUp={false} />
          </div>
          <TrendChart days={a.dayList} height={140} series={[{ key: 'failed', label: '失败任务', values: rel.series.failed }, { key: 'errors', label: '报错', values: rel.series.errors }]} format={num} />
        </Panel>
      </div>
      <div className="grid gap-4 lg:grid-cols-[2fr_1fr]">
        <Panel title="后台任务" padded={false}>
          <Table>
            <thead><tr><th>任务</th><th className="!text-right">总数</th><th className="!text-right">成功率</th><th className="!text-right">失败</th><th className="!text-right">中位耗时</th></tr></thead>
            <tbody>
              {rel.jobs.map((j: any) => {
                const finished = j.done + j.failed
                return (
                  <tr key={j.type}>
                    <td>{j.label}</td>
                    <td className="text-right tnum">{num(j.total)}</td>
                    <td className="text-right tnum">{finished ? pct(j.done / finished) : '—'}</td>
                    <td className={`text-right tnum ${j.failed ? 'text-danger' : 'text-faint'}`}>{num(j.failed)}</td>
                    <td className="text-right tnum text-muted">{j.medianMs ? duration(j.medianMs) : '—'}</td>
                  </tr>
                )
              })}
              {!rel.jobs.length && <tr><td colSpan={5} className="py-6 text-center text-faint">这段时间没有后台任务</td></tr>}
            </tbody>
          </Table>
        </Panel>
        <Panel title="报错最多的接口" description={<Link to="/system" className="text-accent">在「运行状态」里按编号查详情</Link>}><BarList items={rel.errorPaths} format={num} empty="这段时间没有报错" /></Panel>
      </div>
    </div>
  )
}
