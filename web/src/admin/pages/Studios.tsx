import { Search } from '@/components/icons'
import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { adminApi } from '@/lib/api'
import { bookStatusName, bytes, dateTime, num, roleName } from '@/lib/format'
import { Avatar, Badge, Button, Checkbox, Dialog, Empty, Field, Input, PageHeader, Pagination, Panel, Select, Sheet, Stat, Table } from '@/components/ui'
import { ainv, useAdmin } from '../kit'

const features: [string, string][] = [['alignment', '智能对轨'], ['review', '智能审听'], ['production', '生产流程'], ['payroll', '计件结算'], ['byok', '自带密钥'], ['openapi', '开放接口'], ['batch', '批量导入'], ['private', '私有化'], ['pro_clone', '专业克隆'], ['distribute', '平台分发']]

export default function Studios() {
  const [q, setQ] = useState('')
  const [type, setType] = useState('')
  const [planId, setPlanId] = useState('')
  const [page, setPage] = useState(1)
  const [open, setOpen] = useState<string | null>(null)
  const plans = useAdmin<any>('/plans', { pageSize: 50 })
  const { data } = useAdmin<any>('/studios', { q, type, planId, page, pageSize: 30 })
  return (
    <div>
      <PageHeader title="工作室" description="调整套餐、功能开关、席位和存储，给工作室发放积分。" />
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <Select size="sm" value={type} onChange={(v) => { setType(v); setPage(1) }} options={[['', '全部类型'], ['personal', '个人'], ['team', '团队']]} className="w-28" />
        <Select size="sm" value={planId} onChange={(v) => { setPlanId(v); setPage(1) }} options={[['', '全部套餐'], ...(plans.data?.items ?? []).map((p: any) => [p.id, p.name] as [string, string])]} className="w-32" />
        <div className="ml-auto w-full max-w-xs"><Input icon={<Search />} placeholder="名称" value={q} onChange={(e) => { setQ(e.target.value); setPage(1) }} /></div>
      </div>
      <Panel padded={false}>
        {!data?.items?.length ? <Empty title="没有工作室" /> : (
          <Table>
            <thead><tr><th>工作室</th><th>所有者</th><th>套餐</th><th className="!text-right">可用积分</th><th className="!text-right">图书</th><th className="!text-right">席位</th><th className="!text-right">存储</th><th>状态</th></tr></thead>
            <tbody>
              {data.items.map((s: any) => (
                <tr key={s.id} className="cursor-pointer" onClick={() => setOpen(s.id)}>
                  <td><span className="flex items-center gap-2.5"><Avatar name={s.name} src={s.logo} size={24} /><span className="font-medium">{s.name}</span>{s.type === 'team' && <Badge>团队</Badge>}</span></td>
                  <td className="text-muted">{s.owner}</td>
                  <td>{s.plan?.name}</td>
                  <td className="text-right tnum">{num(s.credits?.available)}</td>
                  <td className="text-right tnum">{s.books}</td>
                  <td className="text-right tnum">{s.seats?.used}/{s.seats?.limit || '∞'}</td>
                  <td className="text-right tnum text-muted">{bytes(s.storage?.used)}</td>
                  <td>{s.status === 'active' ? <Badge tone="ok">正常</Badge> : <Badge tone="danger">{s.status === 'frozen' ? '已冻结' : s.status}</Badge>}</td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Panel>
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
      <StudioDetail id={open} plans={plans.data?.items ?? []} onClose={() => setOpen(null)} />
    </div>
  )
}

function StudioDetail({ id, plans, onClose }: { id: string | null; plans: any[]; onClose: () => void }) {
  const { data, refetch } = useAdmin<any>(`/studios/${id}`, {}, { enabled: !!id })
  const s = data?.studio
  const [f, setF] = useState<any>(null)
  const [grant, setGrant] = useState(false)
  const [g, setG] = useState({ amount: 10000, reason: '', expiresDays: 0 })
  useEffect(() => { if (s) setF({ planId: s.planOverrideId ?? '', status: s.status, seatLimit: s.seats?.limit ?? 0, storageLimit: Math.round((s.storage?.limit ?? 0) / 1024 / 1024 / 1024), features: s.features ?? [] }) }, [s?.id, data])
  return (
    <Sheet open={!!id} onOpenChange={(o) => !o && onClose()} title={s?.name} description={s && `${s.type === 'personal' ? '个人' : '团队'}工作室 · 所有者 ${s.owner}`} width={620}>
      {s && f && (
        <div className="space-y-6">
          <div className="grid grid-cols-3 gap-4">
            <Stat label="可用积分" value={num(s.credits?.available)} hint={s.credits?.frozen ? `占用 ${num(s.credits.frozen)}` : undefined} />
            <Stat label="图书" value={s.books} />
            <Stat label="存储" value={bytes(s.storage?.used)} />
          </div>
          <div className="flex gap-2">
            <Button variant="primary" size="sm" onClick={() => setGrant(true)}>发放 / 扣减积分</Button>
          </div>
          <Panel title="方案与配额" description={`当前生效：${s.plan?.name ?? '—'}${s.planOverrideId ? '（签约方案）' : '（跟随所有者的会员）'}`}>
            <div className="grid grid-cols-2 gap-4">
              <Field label="签约方案" hint="企业客户签约后指定；指定后不再跟随所有者的会员，并按该方案每月给本工作室发放积分"><Select value={f.planId} onChange={(v) => setF({ ...f, planId: v })} options={[['', '跟随所有者的会员'], ...plans.map((p) => [p.id, p.name] as [string, string])]} /></Field>
              <Field label="状态"><Select value={f.status} onChange={(v) => setF({ ...f, status: v })} options={[['active', '正常'], ['frozen', '冻结']]} /></Field>
              <Field label="席位上限" hint="0 表示使用套餐默认值"><Input type="number" value={f.seatLimit} onChange={(e) => setF({ ...f, seatLimit: Number(e.target.value) })} /></Field>
              <Field label="存储上限（GB）" hint="0 表示使用套餐默认值"><Input type="number" value={f.storageLimit} onChange={(e) => setF({ ...f, storageLimit: Number(e.target.value) })} /></Field>
            </div>
            <Field label="额外开通的功能" className="mt-4">
              <div className="grid grid-cols-3 gap-2">{features.map(([k, l]) => <Checkbox key={k} checked={f.features.includes(k)} onChange={(v) => setF({ ...f, features: v ? [...f.features, k] : f.features.filter((x: string) => x !== k) })} label={l} />)}</div>
            </Field>
            <Button className="mt-4" variant="primary" size="sm" onClick={async () => {
              const reason = prompt('调整原因（记录到操作日志）') ?? ''
              await adminApi.patch(`/studios/${id}`, { ...f, storageLimit: f.storageLimit * 1024 * 1024 * 1024, reason })
              refetch()
              ainv(['/studios'])
              toast.success('已保存')
            }}>保存</Button>
          </Panel>
          <Panel title="成员" padded={false}>
            <Table><tbody>{(data.members ?? []).map((m: any) => <tr key={m.userId}><td>{m.nickname}</td><td className="text-muted">{m.email}</td><td>{roleName[m.role]}</td><td>{m.status !== 'active' && <Badge tone="warn">{m.status}</Badge>}</td></tr>)}</tbody></Table>
          </Panel>
          <Panel title="图书" padded={false}>
            {!(data.books ?? []).length ? <p className="px-5 py-4 text-[13px] text-faint">没有图书</p> : <Table><tbody>{data.books.map((b: any) => <tr key={b.id}><td>{b.title}</td><td className="text-muted">{bookStatusName[b.status]}</td><td className="text-right tnum">{num(b.wordCount)} 字</td></tr>)}</tbody></Table>}
          </Panel>
          <Panel title="最近积分流水" padded={false}>
            <Table><tbody>{(data.ledger ?? []).map((l: any) => <tr key={l.id}><td className="text-xs text-faint">{dateTime(l.createdAt)}</td><td>{l.reason || l.feature || l.kind}</td><td className="text-right tnum">{l.delta > 0 ? '+' : ''}{num(l.delta)}</td></tr>)}</tbody></Table>
          </Panel>
        </div>
      )}
      <Dialog open={grant} onOpenChange={setGrant} title="调整积分" description="正数为发放，负数为扣减。操作会记录到流水和审计日志。" width={420} footer={<><Button onClick={() => setGrant(false)}>取消</Button><Button variant="primary" disabled={!g.amount || !g.reason.trim()} onClick={async () => { await adminApi.post(`/studios/${id}/credits`, g); setGrant(false); refetch(); ainv(['/studios']); toast.success('已调整') }}>确定</Button></>}>
        <div className="space-y-4">
          <Field label="积分"><Input type="number" value={g.amount} onChange={(e) => setG({ ...g, amount: Number(e.target.value) })} /></Field>
          <Field label="原因"><Input value={g.reason} onChange={(e) => setG({ ...g, reason: e.target.value })} placeholder="例如：活动奖励、客诉补偿" /></Field>
          {g.amount > 0 && <Field label="有效期（天）" hint="0 表示永久有效"><Input type="number" value={g.expiresDays} onChange={(e) => setG({ ...g, expiresDays: Number(e.target.value) })} /></Field>}
        </div>
      </Dialog>
    </Sheet>
  )
}
