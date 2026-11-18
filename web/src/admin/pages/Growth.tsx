import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { Ban, CheckCircle2, Copy, Download, Gift, KeyRound, Plus, Search, Trash2, Users } from '@/components/icons'
import { adminApi } from '@/lib/api'
import { copy, date, dateTime, num } from '@/lib/format'
import { Badge, Button, cn, confirm, Dialog, Empty, Field, Input, PageHeader, Pagination, Panel, Segmented, Select, Skeleton, Stat, Switch, Table, Tabs, Textarea } from '@/components/ui'
import { ainv, useAdmin } from '../kit'

const yuan = (fen: number) => `¥${(fen / 100).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
const who = (u?: any) => (u?.id ? <span><span className="text-ink">{u.nickname || '未命名'}</span><span className="ml-1.5 text-[11.5px] text-faint">{u.email || u.phone || u.id.slice(0, 8)}</span></span> : <span className="text-faint">已注销</span>)

/** A setting switch that saves the whole setting object it belongs to. */
function useSetting(key: string) {
  const { data, refetch } = useAdmin<any>(`/settings/${key}`)
  const value = data?.value
  const save = async (patch: Record<string, any>) => {
    await adminApi.put(`/settings/${key}`, { ...value, ...patch })
    await refetch()
    ainv([`/settings/${key}`])
  }
  return { value, save }
}

// ======================================================================================
// 注册码
// ======================================================================================

function codeState(c: any): [string, any] {
  if (c.status === 'disabled') return ['已停用', 'neutral']
  if (c.expiresAt && new Date(c.expiresAt) < new Date()) return ['已过期', 'warn']
  if (c.maxUses > 0 && c.uses >= c.maxUses) return ['已用完', 'accent']
  return ['可用', 'ok']
}

export function RegCodes() {
  const site = useSetting('site')
  const [page, setPage] = useState(1)
  const [status, setStatus] = useState('')
  const [q, setQ] = useState('')
  const [create, setCreate] = useState(false)
  const [uses, setUses] = useState<any>(null)
  const { data, refetch } = useAdmin<any>('/registration-codes', { page, pageSize: 30, status, q })
  const items: any[] = data?.items ?? []
  const on = !!site.value?.regCodeRequired
  return (
    <div className="space-y-5">
      <PageHeader title="注册码" description="开启「必须使用注册码注册」后，新用户注册时要填写这里的注册码。可以批量生成一人一码，也可以设一个多人共用的码。" actions={<Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setCreate(true)}>生成注册码</Button>} />
      <Panel>
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div>
            <div className="text-[14px] font-medium">{on ? '现在必须使用注册码才能注册' : '现在不需要注册码，任何人都能注册'}</div>
            <p className="mt-1 text-[12.5px] text-muted">适用于邮箱注册、手机号首次登录和第三方账号注册。被邀请加入工作室的人不需要注册码；好友邀请链接是否也能注册，在「邀请返利 → 规则设置」里设置。</p>
          </div>
          {site.value && <Switch checked={on} onChange={(v) => site.save({ regCodeRequired: v }).then(() => toast.success(v ? '已开启：新用户需要注册码' : '已关闭：新用户可以直接注册'))} label="必须使用注册码" />}
        </div>
        {site.value && !site.value.registrationOpen && <p className="mt-3 text-[12.5px] text-warn">「系统设置 → 站点」里的「开放注册」是关闭的，目前谁都不能注册。</p>}
      </Panel>
      <div className="flex flex-wrap gap-3">
        <Segmented size="sm" value={status} onChange={(v) => { setStatus(v); setPage(1) }} items={[{ value: '', label: '全部' }, { value: 'usable', label: '可用' }, { value: 'used', label: '用过的' }, { value: 'disabled', label: '已停用' }]} />
        <Input value={q} onChange={(e) => { setQ(e.target.value); setPage(1) }} placeholder="搜索注册码或备注" icon={<Search className="size-4" />} className="!h-8 w-60" />
      </div>
      <Panel padded={false}>
        {!data ? <Skeleton className="m-4 h-40" /> : !items.length ? <Empty icon={<KeyRound />} title={q || status ? '没有符合条件的注册码' : '还没有注册码'} description="生成后发给要邀请的人，他们注册时填写即可。" action={<Button variant="primary" onClick={() => setCreate(true)}>生成注册码</Button>} /> : (
          <Table>
            <thead><tr><th>注册码</th><th>已用 / 可用次数</th><th>有效期</th><th>备注</th><th>状态</th><th /></tr></thead>
            <tbody>
              {items.map((c) => {
                const [label, tone] = codeState(c)
                return (
                  <tr key={c.id}>
                    <td><button className="inline-flex items-center gap-1.5 font-mono text-[13px] hover:text-accent" onClick={() => copy(c.code).then(() => toast.success('已复制'))}>{c.code}<Copy className="size-3.5 text-faint" /></button></td>
                    <td className="tnum">{c.uses > 0 ? <button className="text-accent hover:underline" onClick={() => setUses(c)}>{num(c.uses)}</button> : 0} <span className="text-faint">/ {c.maxUses ? num(c.maxUses) : '不限'}</span></td>
                    <td className="text-[12.5px] text-muted">{c.expiresAt ? `${dateTime(c.expiresAt)} 前` : '长期'}</td>
                    <td className="max-w-56 truncate text-muted">{c.note || '—'}</td>
                    <td><Badge tone={tone}>{label}</Badge></td>
                    <td className="whitespace-nowrap text-right">
                      <Button size="xs" variant="ghost" onClick={async () => { await adminApi.patch(`/registration-codes/${c.id}`, { status: c.status === 'disabled' ? 'active' : 'disabled' }); refetch(); toast.success(c.status === 'disabled' ? '已启用' : '已停用') }}>{c.status === 'disabled' ? '启用' : '停用'}</Button>
                      {c.uses === 0 && <Button size="xs" variant="ghost" icon={<Trash2 className="size-3.5" />} onClick={async () => {
                        if (!(await confirm({ title: `删除注册码 ${c.code}？`, danger: true, confirmText: '删除' }))) return
                        await adminApi.del(`/registration-codes/${c.id}`)
                        refetch()
                        toast.success('已删除')
                      }} aria-label="删除" />}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </Table>
        )}
      </Panel>
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
      <CreateRegCodes open={create} onOpenChange={setCreate} onCreated={() => { setPage(1); refetch() }} />
      <RegCodeUses code={uses} onClose={() => setUses(null)} />
    </div>
  )
}

function CreateRegCodes({ open, onOpenChange, onCreated }: { open: boolean; onOpenChange: (v: boolean) => void; onCreated: () => void }) {
  const init = { mode: 'batch', count: 20, code: '', maxUses: 1, expiresDays: 0, prefix: '', note: '' }
  const [f, setF] = useState<any>(init)
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState<any[] | null>(null)
  useEffect(() => { if (open) { setF(init); setDone(null) } }, [open])
  const set = (k: string, v: any) => setF((x: any) => ({ ...x, [k]: v }))
  const submit = async () => {
    setBusy(true)
    try {
      const body = f.mode === 'batch' ? { count: f.count, maxUses: f.maxUses, expiresDays: f.expiresDays, prefix: f.prefix, note: f.note } : { code: f.code, maxUses: f.maxUses, expiresDays: f.expiresDays, note: f.note }
      const r = await adminApi.post('/registration-codes', body)
      setDone(r.codes)
      onCreated()
    } catch (e: any) {
      toast.error(e.message)
    } finally {
      setBusy(false)
    }
  }
  const text = (done ?? []).map((c) => c.code).join('\n')
  const download = () => {
    const a = document.createElement('a')
    a.href = URL.createObjectURL(new Blob([text + '\n'], { type: 'text/plain;charset=utf-8' }))
    a.download = `注册码-${new Date().toISOString().slice(0, 10)}.txt`
    a.click()
  }
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title={done ? `已生成 ${done.length} 个注册码` : '生成注册码'} width={500}
      footer={done ? <><Button onClick={() => onOpenChange(false)}>关闭</Button><Button icon={<Download className="size-4" />} onClick={download}>下载 TXT</Button><Button variant="primary" icon={<Copy className="size-4" />} onClick={() => copy(text).then(() => toast.success('已复制全部注册码'))}>复制全部</Button></>
        : <><Button onClick={() => onOpenChange(false)}>取消</Button><Button variant="primary" loading={busy} disabled={f.mode === 'custom' ? f.code.length < 4 : f.count < 1} onClick={submit}>生成</Button></>}>
      {done ? (
        <div>
          <div className="mb-3 flex items-center gap-2 text-[13px] text-muted"><CheckCircle2 className="size-4 text-accent" />发给要邀请的人，注册时填写即可。</div>
          <pre className="max-h-72 overflow-auto rounded-[10px] border border-line bg-sunken px-4 py-3 font-mono text-[13px] leading-7">{text}</pre>
        </div>
      ) : (
        <div className="space-y-4">
          <Segmented value={f.mode} onChange={(v) => setF({ ...f, mode: v, maxUses: v === 'batch' ? 1 : 0 })} items={[{ value: 'batch', label: '批量生成' }, { value: 'custom', label: '自定义一个码' }]} />
          {f.mode === 'batch' ? (
            <div className="grid grid-cols-2 gap-4">
              <Field label="数量" hint="一次最多 1000 个"><Input type="number" min={1} max={1000} value={f.count} onChange={(e) => set('count', Number(e.target.value))} /></Field>
              <Field label="前缀" hint="可选，如 VB"><Input value={f.prefix} maxLength={6} onChange={(e) => set('prefix', e.target.value.toUpperCase().replace(/[^A-Z0-9]/g, ''))} className="font-mono" /></Field>
            </div>
          ) : (
            <Field label="注册码" hint="4~32 个字母或数字，例如 BETA2026；不区分大小写"><Input value={f.code} maxLength={32} onChange={(e) => set('code', e.target.value.toUpperCase().replace(/[^A-Z0-9-]/g, ''))} className="font-mono" placeholder="BETA2026" /></Field>
          )}
          <div className="grid grid-cols-2 gap-4">
            <Field label="每个码可以注册几个账号" hint="0 表示不限"><Input type="number" min={0} value={f.maxUses} onChange={(e) => set('maxUses', Number(e.target.value))} /></Field>
            <Field label="有效天数" hint="0 表示长期有效"><Input type="number" min={0} value={f.expiresDays} onChange={(e) => set('expiresDays', Number(e.target.value))} /></Field>
          </div>
          <Field label="备注" hint="例如 发给哪个渠道、哪一批内测用户"><Textarea rows={2} value={f.note} onChange={(e) => set('note', e.target.value)} /></Field>
          <div className="rounded-[10px] bg-sunken/70 px-4 py-3 text-[13px]">
            {f.mode === 'batch' ? `生成 ${num(f.count)} 个注册码，` : `注册码 ${f.code || '……'}，`}{f.maxUses ? `每个可注册 ${f.maxUses} 个账号` : '不限注册人数'}，{f.expiresDays ? `${f.expiresDays} 天内有效` : '长期有效'}。
          </div>
        </div>
      )}
    </Dialog>
  )
}

function RegCodeUses({ code, onClose }: { code: any; onClose: () => void }) {
  const { data } = useAdmin<any[]>(`/registration-codes/${code?.id}/uses`, {}, { enabled: !!code })
  return (
    <Dialog open={!!code} onOpenChange={(v) => !v && onClose()} title={`用 ${code?.code ?? ''} 注册的账号`} width={520}>
      {!data ? <Skeleton className="h-24" /> : (
        <ul className="divide-y divide-line rounded-[10px] border border-line text-[13px]">
          {data.map((u: any, i: number) => <li key={i} className="flex items-center justify-between gap-3 px-3 py-2">{who(u.user)}<span className="text-[12px] text-faint tnum">{dateTime(u.createdAt)}</span></li>)}
        </ul>
      )}
    </Dialog>
  )
}

// ======================================================================================
// 邀请返利
// ======================================================================================

export function Referrals() {
  const ref = useSetting('referral')
  const [tab, setTab] = useState<'overview' | 'records' | 'payments' | 'rules'>('overview')
  const on = !!ref.value?.enabled
  return (
    <div className="space-y-5">
      <PageHeader title="邀请返利" description="用户分享自己的邀请码、链接或海报；好友注册后双方可得积分，好友充值时邀请人按比例获得返利。" />
      <Panel>
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div>
            <div className="text-[14px] font-medium">{on ? '邀请活动进行中' : '邀请活动已关闭'}</div>
            <p className="mt-1 text-[12.5px] text-muted">{on ? '用户在侧边栏「邀请好友」里拿到自己的邀请码、链接和海报。' : '关闭时用户看不到「邀请好友」，邀请码不能用于注册，也不会发放任何奖励；已有的邀请关系保留。'}</p>
          </div>
          {ref.value && <Switch checked={on} onChange={(v) => ref.save({ enabled: v }).then(() => toast.success(v ? '邀请活动已开启' : '邀请活动已关闭'))} label="开启邀请活动" />}
        </div>
      </Panel>
      <Tabs value={tab} onChange={setTab} items={[{ value: 'overview', label: '概览' }, { value: 'records', label: '邀请记录' }, { value: 'payments', label: '充值记录' }, { value: 'rules', label: '规则设置' }]} />
      {tab === 'overview' && <ReferralOverview />}
      {tab === 'records' && <ReferralRecords />}
      {tab === 'payments' && <Payments />}
      {tab === 'rules' && ref.value && <ReferralRules value={ref.value} onSave={ref.save} />}
    </div>
  )
}

function toggleInviter(userId: string, disabled: boolean) {
  return (async () => {
    const ok = await confirm(disabled
      ? { title: '停用这个用户的邀请码？', description: '停用后，这个邀请码不能再用于注册，已邀请的好友充值也不再给 Ta 返利。已发放的积分不会收回，可以在「工作室」里手动扣减。', danger: true, confirmText: '停用' }
      : { title: '恢复这个用户的邀请码？', confirmText: '恢复' })
    if (!ok) return
    await adminApi.patch(`/invite-codes/${userId}`, { disabled })
    ainv(['/referrals'], ['/referrals/summary'])
    toast.success(disabled ? '已停用' : '已恢复')
  })()
}

function ReferralOverview() {
  const { data } = useAdmin<any>('/referrals/summary')
  if (!data) return <Skeleton className="h-60" />
  const stats: [string, string, string?][] = [
    ['邀请注册', num(data.total), `近 30 天 ${num(data.last30)}`],
    ['已充值好友', num(data.paying), data.total ? `转化 ${((data.paying / data.total) * 100).toFixed(1)}%` : undefined],
    ['好友充值总额', yuan(data.paidCents)],
    ['发出的邀请奖励', num(data.rewards.signup_inviter + data.rewards.signup_invitee), `邀请人 ${num(data.rewards.signup_inviter)} · 好友 ${num(data.rewards.signup_invitee)}`],
    ['发出的充值返利', num(data.rewards.commission), '积分'],
  ]
  return (
    <div className="space-y-5">
      <Panel padded={false}>
        <div className="grid grid-cols-2 gap-px overflow-hidden rounded-panel bg-line md:grid-cols-5">
          {stats.map(([l, v, h]) => <div key={l} className="bg-surface px-5 py-4"><Stat label={l} value={v} hint={h} /></div>)}
        </div>
      </Panel>
      <Panel title="邀请最多的用户" padded={false}>
        {!data.top.length ? <Empty icon={<Gift />} title="还没有人通过邀请注册" /> : (
          <Table>
            <thead><tr><th>用户</th><th>邀请码</th><th className="text-right">链接打开</th><th className="text-right">邀请注册</th><th className="text-right">已充值</th><th className="text-right">获得积分</th><th /></tr></thead>
            <tbody>
              {data.top.map((t: any) => (
                <tr key={t.user?.id}>
                  <td>{who(t.user)}</td>
                  <td className="font-mono">{t.code}{t.disabled && <Badge className="ml-2" tone="warn">已停用</Badge>}</td>
                  <td className="text-right tnum">{num(t.visits)}</td>
                  <td className="text-right tnum">{num(t.signups)}</td>
                  <td className="text-right tnum">{num(t.paying)}</td>
                  <td className="text-right tnum">{num(t.earned)}</td>
                  <td className="text-right"><Button size="xs" variant="ghost" icon={t.disabled ? <CheckCircle2 className="size-3.5" /> : <Ban className="size-3.5" />} onClick={() => toggleInviter(t.user.id, !t.disabled)}>{t.disabled ? '恢复' : '停用'}</Button></td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Panel>
    </div>
  )
}

function ReferralRecords() {
  const [page, setPage] = useState(1)
  const [q, setQ] = useState('')
  const { data } = useAdmin<any>('/referrals', { page, pageSize: 30, q })
  const items: any[] = data?.items ?? []
  return (
    <div className="space-y-4">
      <Input value={q} onChange={(e) => { setQ(e.target.value); setPage(1) }} placeholder="搜索邀请人、被邀请人（昵称、邮箱、手机）或邀请码" icon={<Search className="size-4" />} className="!h-8 max-w-sm" />
      <Panel padded={false}>
        {!data ? <Skeleton className="m-4 h-40" /> : !items.length ? <Empty icon={<Users />} title="没有邀请记录" /> : (
          <Table>
            <thead><tr><th>时间</th><th>邀请人</th><th>被邀请人</th><th>邀请人注册奖励</th><th className="text-right">好友充值</th><th className="text-right">邀请人获得积分</th><th /></tr></thead>
            <tbody>
              {items.map((x) => (
                <tr key={x.id}>
                  <td className="text-[12px] text-muted tnum">{dateTime(x.createdAt)}</td>
                  <td>{who(x.inviter)}{x.inviterDisabled && <Badge className="ml-2" tone="warn">已停用</Badge>}</td>
                  <td>{who(x.invitee)}<div className="text-[11px] text-faint">{x.ip}</div></td>
                  <td>{x.signupPaid ? <Badge tone="ok">已发放</Badge> : x.pending ? <Badge tone="warn">等待首充</Badge> : <Badge>未发放</Badge>}</td>
                  <td className="text-right tnum">{x.payments ? <>{yuan(x.paidCents)}<div className="text-[11px] text-faint">{x.payments} 笔</div></> : '—'}</td>
                  <td className="text-right tnum">{x.earned ? num(x.earned) : '—'}</td>
                  <td className="text-right">{x.inviter?.id && <Button size="xs" variant="ghost" onClick={() => toggleInviter(x.inviter.id, !x.inviterDisabled)}>{x.inviterDisabled ? '恢复邀请人' : '停用邀请人'}</Button>}</td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Panel>
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
      <p className="text-[12px] text-faint">「未发放」表示达到了每日或累计上限，或者当时没有设置邀请人奖励。</p>
    </div>
  )
}

const sourceName: Record<string, string> = { admin: '后台录入', redeem: '兑换码', order: '在线支付' }

function Payments() {
  const [page, setPage] = useState(1)
  const [q, setQ] = useState('')
  const [source, setSource] = useState('')
  const { data } = useAdmin<any>('/payments', { page, pageSize: 30, q, source })
  const items: any[] = data?.items ?? []
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap gap-3">
        <Segmented size="sm" value={source} onChange={(v) => { setSource(v); setPage(1) }} items={[{ value: '', label: '全部' }, { value: 'admin', label: '后台录入' }, { value: 'redeem', label: '兑换码' }, { value: 'order', label: '在线支付' }]} />
        <Input value={q} onChange={(e) => { setQ(e.target.value); setPage(1) }} placeholder="搜索用户" icon={<Search className="size-4" />} className="!h-8 w-60" />
      </div>
      <Panel padded={false}>
        {!data ? <Skeleton className="m-4 h-40" /> : !items.length ? <Empty icon={<Gift />} title="还没有充值记录" description="后台给工作室发积分或开通会员时填写「实收金额」、用户兑换有售价的兑换码，都会记在这里。" /> : (
          <Table>
            <thead><tr><th>时间</th><th>用户</th><th className="text-right">金额</th><th>来源</th><th>内容</th><th className="text-right">产生返利</th><th>操作人</th></tr></thead>
            <tbody>
              {items.map((p) => (
                <tr key={p.id}>
                  <td className="text-[12px] text-muted tnum">{dateTime(p.createdAt)}</td>
                  <td>{who(p.user)}</td>
                  <td className="text-right font-medium tnum">{yuan(p.amountCents)}</td>
                  <td><Badge>{sourceName[p.source] ?? p.source}</Badge></td>
                  <td className="max-w-72 truncate text-muted">{p.summary || '—'}</td>
                  <td className="text-right tnum">{p.commission ? `${num(p.commission)} 积分` : '—'}</td>
                  <td className="text-muted">{p.operator || '—'}</td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Panel>
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
    </div>
  )
}

function NumberField({ label, hint, value, onChange, unit, step }: { label: string; hint?: string; value: number; onChange: (v: number) => void; unit?: string; step?: string }) {
  return (
    <Field label={label} hint={hint}>
      <div className="relative"><Input type="number" min={0} step={step} value={value} onChange={(e) => onChange(Number(e.target.value))} className={cn(unit && 'pr-12')} />{unit && <span className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-[12px] text-faint">{unit}</span>}</div>
    </Field>
  )
}

function ReferralRules({ value, onSave }: { value: any; onSave: (v: any) => Promise<void> }) {
  const [f, setF] = useState<any>(value)
  const [busy, setBusy] = useState(false)
  useEffect(() => setF(value), [value])
  const set = (k: string) => (v: any) => setF((x: any) => ({ ...x, [k]: v }))
  const example = Math.floor(100 * (f.commissionRate / 100) * f.creditsPerYuan)
  const save = async () => {
    setBusy(true)
    try {
      await onSave(f)
      toast.success('规则已保存，立即生效')
    } catch (e: any) {
      toast.error(e.message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="space-y-5">
      <Panel title="注册奖励" description="好友用邀请码注册成功后发放到双方的个人工作室。">
        <div className="grid gap-4 sm:grid-cols-2">
          <NumberField label="邀请人得" value={f.inviterCredits} onChange={set('inviterCredits')} unit="积分" hint="0 表示不奖励邀请人" />
          <NumberField label="被邀请的好友得" value={f.inviteeCredits} onChange={set('inviteeCredits')} unit="积分" hint="在普通的新用户赠送之外另加，0 表示不奖励" />
          <Field label="邀请人奖励什么时候发" hint="选「首次充值后」可以防止有人批量注册小号刷奖励">
            <Select value={f.inviterAfter} onChange={set('inviterAfter')} options={[['signup', '好友注册后立即发放'], ['payment', '好友首次充值后发放']]} />
          </Field>
          <NumberField label="奖励积分有效期" value={f.creditDays} onChange={set('creditDays')} unit="天" hint="0 表示长期有效" />
          <NumberField label="每位邀请人 24 小时内最多奖励" value={f.dailyLimit} onChange={set('dailyLimit')} unit="次" hint="超出后好友照常注册，只是邀请人不再得奖励；0 表示不限" />
          <NumberField label="每位邀请人累计最多奖励" value={f.totalLimit} onChange={set('totalLimit')} unit="次" hint="0 表示不限" />
        </div>
      </Panel>
      <Panel title="充值返利" description="被邀请的好友充值后，邀请人按充值金额的比例获得积分。">
        <div className="grid gap-4 sm:grid-cols-2">
          <NumberField label="返利比例" value={f.commissionRate} onChange={set('commissionRate')} unit="%" step="0.1" hint="0 表示不返利" />
          <NumberField label="1 元折合积分" value={f.creditsPerYuan} onChange={set('creditsPerYuan')} unit="积分" hint="返利按这个比例换算成积分" />
          <Field label="哪些充值返利"><Select value={f.commissionOn} onChange={set('commissionOn')} options={[['all', '好友的每一次充值'], ['first', '只有好友的首次充值']]} /></Field>
          <NumberField label="只返好友注册后多少天内的充值" value={f.commissionDays} onChange={set('commissionDays')} unit="天" hint="0 表示一直返" />
        </div>
        <div className="mt-4 rounded-[10px] bg-sunken/70 px-4 py-3 text-[13px] text-muted">
          {f.commissionRate > 0 && f.creditsPerYuan > 0 ? <>例如好友充值 100 元，邀请人得 <span className="font-medium text-ink">{num(example)}</span> 积分。</> : '现在不返利。'}
          <span className="ml-1">充值包括：后台发积分或开通会员时填写的「实收金额」、用户兑换有售价的兑换码，以及接入在线支付后的订单。</span>
        </div>
      </Panel>
      <Panel title="注册码与海报">
        <div className="space-y-4">
          <Switch checked={!!f.bypassRegCode} onChange={set('bypassRegCode')} label="开启「必须使用注册码注册」时，好友邀请链接也能直接注册" />
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="海报标题" hint="最多 24 个字"><Input value={f.posterTitle} maxLength={24} onChange={(e) => set('posterTitle')(e.target.value)} /></Field>
            <Field label="海报副标题" hint="最多 60 个字"><Input value={f.posterText} maxLength={60} onChange={(e) => set('posterText')(e.target.value)} /></Field>
          </div>
        </div>
      </Panel>
      <div className="flex justify-end gap-2">
        <Button onClick={() => setF(value)}>还原</Button>
        <Button variant="primary" loading={busy} onClick={save}>保存规则</Button>
      </div>
    </div>
  )
}
