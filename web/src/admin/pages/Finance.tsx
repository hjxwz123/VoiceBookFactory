import { useState } from 'react'
import { toast } from 'sonner'
import { adminApi } from '@/lib/api'
import { dateTime, featureName, num, yuan } from '@/lib/format'
import { PlayButton } from '@/components/audio'
import { Badge, Button, DateInput, Empty, Input, PageHeader, Pagination, Panel, Segmented, Select, Stat, Table } from '@/components/ui'
import { ainv, CrudPage, ReviewButtons, useAdmin } from '../kit'

export function Ledger() {
  const [kind, setKind] = useState('')
  const [feature, setFeature] = useState('')
  const [studioId, setStudioId] = useState('')
  const [page, setPage] = useState(1)
  const { data } = useAdmin<any>('/ledger', { kind, feature, studioId, page, pageSize: 50 })
  return (
    <div>
      <PageHeader title="积分流水" description="所有工作室的积分变动记录。" />
      <div className="mb-4 flex flex-wrap gap-3">
        <Select size="sm" value={kind} onChange={(v) => { setKind(v); setPage(1) }} options={[['', '全部类型'], ['grant', '发放'], ['consume', '消耗'], ['refund', '退回'], ['expire', '过期'], ['admin', '人工调整'], ['promo', '兑换码'], ['market_income', '市场收入']]} className="w-32" />
        <Select size="sm" value={feature} onChange={(v) => { setFeature(v); setPage(1) }} options={[['', '全部功能'], ...Object.entries(featureName)]} className="w-32" />
        <Input value={studioId} onChange={(e) => { setStudioId(e.target.value); setPage(1) }} placeholder="工作室 ID" className="!h-8 w-72 font-mono text-xs" />
      </div>
      <Panel padded={false}>
        {!data?.items?.length ? <Empty title="没有记录" /> : (
          <Table>
            <thead><tr><th>时间</th><th>工作室</th><th>类型</th><th>功能</th><th>说明</th><th className="!text-right">变动</th><th className="!text-right">余额</th></tr></thead>
            <tbody>{data.items.map((l: any) => <tr key={l.id}><td className="whitespace-nowrap text-xs text-faint">{dateTime(l.createdAt)}</td><td className="font-mono text-[11px] text-muted">{l.studioId.slice(0, 8)}</td><td>{l.kind}</td><td className="text-muted">{featureName[l.feature] ?? l.feature}</td><td className="max-w-xs truncate">{l.reason}</td><td className={`text-right tnum ${l.delta > 0 ? 'text-ok' : ''}`}>{l.delta > 0 ? '+' : ''}{num(l.delta)}</td><td className="text-right text-muted tnum">{num(l.balanceAfter)}</td></tr>)}</tbody>
          </Table>
        )}
      </Panel>
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
    </div>
  )
}

export function FinanceReport() {
  const [from, setFrom] = useState(new Date(Date.now() - 30 * 86400000).toISOString().slice(0, 10))
  const [to, setTo] = useState(new Date().toISOString().slice(0, 10))
  const { data } = useAdmin<any>('/finance', { from, to })
  const names = data?.featureNames ?? {}
  const totalCredits = (data?.byFeature ?? []).reduce((s: number, x: any) => s + x.Credits, 0)
  const totalCost = (data?.byFeature ?? []).reduce((s: number, x: any) => s + x.Cost, 0)
  return (
    <div className="space-y-6">
      <PageHeader title="成本报表" description="积分消耗与供应商实际成本对比，用于核算毛利和调整定价。" actions={<div className="flex items-center gap-2"><DateInput size="sm" value={from} onChange={setFrom} placeholder="开始日期" className="w-40" /><span className="text-faint">至</span><DateInput size="sm" value={to} onChange={setTo} placeholder="结束日期" className="w-40" /></div>} />
      <Panel>
        <div className="grid grid-cols-2 gap-6 sm:grid-cols-4">
          <Stat label="发放积分" value={num(data?.granted)} />
          <Stat label="消耗积分" value={num(totalCredits)} />
          <Stat label="供应商成本" value={`¥${totalCost.toFixed(2)}`} />
          <Stat label="每万积分成本" value={totalCredits ? `¥${((totalCost / totalCredits) * 10000).toFixed(2)}` : '—'} />
        </div>
      </Panel>
      <div className="grid gap-6 lg:grid-cols-2">
        <Panel title="按功能" padded={false}>
          <Table>
            <thead><tr><th>功能</th><th className="!text-right">用量</th><th className="!text-right">积分</th><th className="!text-right">成本</th></tr></thead>
            <tbody>{(data?.byFeature ?? []).map((x: any) => <tr key={x.Feature}><td>{names[x.Feature] ?? x.Feature}</td><td className="text-right tnum">{num(x.Qty)}</td><td className="text-right tnum">{num(x.Credits)}</td><td className="text-right tnum">¥{x.Cost.toFixed(2)}</td></tr>)}</tbody>
          </Table>
        </Panel>
        <Panel title="按供应商" padded={false}>
          <Table>
            <thead><tr><th>供应商</th><th className="!text-right">用量</th><th className="!text-right">成本</th></tr></thead>
            <tbody>{(data?.byProvider ?? []).map((x: any) => <tr key={x.Provider}><td>{x.Provider}</td><td className="text-right tnum">{num(x.Qty)}</td><td className="text-right tnum">¥{x.Cost.toFixed(2)}</td></tr>)}</tbody>
          </Table>
        </Panel>
      </div>
      <Panel title="消耗最多的工作室" padded={false}>
        <Table>
          <thead><tr><th>工作室</th><th className="!text-right">积分</th><th className="!text-right">成本</th></tr></thead>
          <tbody>{(data?.byStudio ?? []).map((x: any) => <tr key={x.studioId}><td>{x.name}</td><td className="text-right tnum">{num(x.credits)}</td><td className="text-right tnum">¥{x.cost.toFixed(2)}</td></tr>)}</tbody>
        </Table>
      </Panel>
    </div>
  )
}

export function Withdrawals() {
  const [status, setStatus] = useState('pending')
  const [page, setPage] = useState(1)
  const { data } = useAdmin<any>('/withdrawals', { status, page, pageSize: 30 })
  return (
    <div>
      <PageHeader title="提现审核" description="音色市场创作者的收益提现申请。确认打款后点击通过。" />
      <Segmented className="mb-4" size="sm" value={status} onChange={(v) => { setStatus(v); setPage(1) }} items={[{ value: 'pending', label: '待处理' }, { value: 'done', label: '已打款' }, { value: 'rejected', label: '已驳回' }, { value: '', label: '全部' }]} />
      <Panel padded={false}>
        {!data?.items?.length ? <Empty title="没有提现申请" /> : (
          <Table>
            <thead><tr><th>时间</th><th>用户</th><th className="!text-right">积分</th><th>收款账户</th><th>状态</th><th /></tr></thead>
            <tbody>{data.items.map((w: any) => <tr key={w.id}><td className="text-xs text-faint">{dateTime(w.createdAt)}</td><td className="font-mono text-xs">{w.userId.slice(0, 8)}</td><td className="text-right tnum">{num(w.amount)}</td><td>{w.account}</td><td><Badge tone={w.status === 'done' ? 'ok' : w.status === 'rejected' ? 'danger' : 'warn'}>{w.status === 'done' ? '已打款' : w.status === 'rejected' ? '已驳回' : '待处理'}</Badge>{w.note && <span className="ml-2 text-xs text-muted">{w.note}</span>}</td><td className="text-right">{w.status === 'pending' && <ReviewButtons approveText="已打款" onDecide={async (approve, note) => { await adminApi.post(`/withdrawals/${w.id}`, { approve, note }); ainv(['/withdrawals']); toast.success('已处理') }} />}</td></tr>)}</tbody>
          </Table>
        )}
      </Panel>
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
    </div>
  )
}

export function Listings() {
  const [status, setStatus] = useState('pending')
  const [page, setPage] = useState(1)
  const { data } = useAdmin<any>('/listings', { status, page, pageSize: 30 })
  return (
    <div>
      <PageHeader title="音色市场" description="用户申请上架到市场的音色。审核时请确认声音授权完整、音质达标。" />
      <Segmented className="mb-4" size="sm" value={status} onChange={(v) => { setStatus(v); setPage(1) }} items={[{ value: 'pending', label: '待审核' }, { value: 'listed', label: '已上架' }, { value: 'rejected', label: '已驳回' }, { value: 'delisted', label: '已下架' }]} />
      <Panel padded={false}>
        {!data?.items?.length ? <Empty title="没有记录" /> : (
          <Table>
            <thead><tr><th /><th>音色</th><th>创作者</th><th className="!text-right">千字价格</th><th className="!text-right">使用</th><th className="!text-right">收入</th><th>介绍</th><th /></tr></thead>
            <tbody>{data.items.map(({ listing: l, voice: v, owner }: any) => <tr key={l.id}><td className="w-10"><PlayButton id={'voice:' + v?.id} url={v?.sampleUrl} size="xs" /></td><td className="font-medium">{v?.name}{v?.cloneTier && <Badge className="ml-1.5">{v.cloneTier === 'pro' ? '专业克隆' : '快速克隆'}</Badge>}</td><td>{owner}</td><td className="text-right tnum">{num(l.pricePer1k)}</td><td className="text-right tnum">{num(l.uses)}</td><td className="text-right tnum">{num(l.earned)}</td><td className="max-w-xs truncate text-muted">{l.intro}</td><td className="whitespace-nowrap text-right">{l.status === 'pending' ? <ReviewButtons approveText="上架" onDecide={async (approve, note) => { await adminApi.post(`/listings/${l.id}`, { approve, note }); ainv(['/listings']) }} /> : l.status === 'listed' && <Button size="xs" variant="ghost" className="text-danger" onClick={async () => { await adminApi.post(`/listings/${l.id}`, { approve: false, note: '平台下架' }); ainv(['/listings']) }}>下架</Button>}</td></tr>)}</tbody>
          </Table>
        )}
      </Panel>
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
    </div>
  )
}
