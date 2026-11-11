import { AnimatePresence, motion } from 'framer-motion'
import { CheckCircle2, Copy, Crown, Download, Eye, EyeOff, Plus, Search, Sparkles, Ticket, Trash2, Wand2 } from '@/components/icons'
import React, { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { adminApi } from '@/lib/api'
import { copy, date, dateTime, num } from '@/lib/format'
import { autoBenefits, Cycle, Plan, planFeatures, PlanCard, rateText, wan } from '@/components/plans'
import { Badge, Button, Checkbox, cn, confirm, DateInput, Dialog, Empty, Field, IconButton, Input, PageHeader, Pagination, Panel, Segmented, Select, Sheet, Slider, Stat, Switch, Table, Tabs, Textarea, Tip } from '@/components/ui'
import { ainv, useAdmin } from '../kit'

// ======================================================================================
// 会员套餐
// ======================================================================================

const emptyPlan: Partial<Plan> = {
  code: '', name: '', tagline: '', level: 10, priceMonthly: 0, priceYearly: 0, originalMonthly: 0, monthlyCredits: 100000, bonusCredits: 0, creditMonths: 2, creditRate: 100,
  maxStudios: 2, seats: 1, maxBooks: 0, storageGb: 20, cloneVoices: 3, maxBookWords: 0, maxUploadMb: 0, queuePriority: 0, exportFormats: [], highQuality: false, commercial: false,
  features: [], benefits: [], badge: '', highlight: false, contactSales: false, listed: true, sortOrder: 10,
}

export function Plans() {
  const { data } = useAdmin<any>('/plans', { pageSize: 100 })
  const overview = useAdmin<any>('/membership/overview')
  const [edit, setEdit] = useState<Partial<Plan> | null>(null)
  const plans: Plan[] = data?.items ?? []
  const o = overview.data
  const totalMembers = (o?.byPlan ?? []).reduce((s: number, x: any) => s + x.members, 0) || 1
  return (
    <div className="space-y-6">
      <PageHeader
        title="会员套餐"
        description="会员属于用户账号，决定可用资源和功能，并按月赠送积分。同时拥有多个会员时，等级最高的生效，其余顺延。"
        actions={<Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEdit({ ...emptyPlan })}>新建会员</Button>}
      />
      <Panel>
        <div className="grid grid-cols-2 gap-6 sm:grid-cols-4">
          <Stat label="付费会员" value={num(o?.paid)} />
          <Stat label="7 天内到期" value={num(o?.expiring7d)} hint="会自动发送续费提醒" />
          <Stat label="试用中" value={num(o?.trials)} />
          <Stat label="近 30 天兑换" value={num(o?.redeemed30d)} />
        </div>
        {o && (
          <div className="mt-6">
            <div className="flex h-2.5 overflow-hidden rounded-full bg-sunken">
              {o.byPlan.filter((x: any) => x.members).map((x: any, i: number) => (
                <Tip key={x.planId} label={`${x.name}：${num(x.members)} 人`}>
                  <motion.div initial={{ width: 0 }} animate={{ width: `${(x.members / totalMembers) * 100}%` }} transition={{ duration: 0.6, delay: i * 0.05 }} className={cn('h-full', ['bg-line-strong', 'bg-accent', 'bg-signal', 'bg-ink/70', 'bg-accent/50'][i % 5])} />
                </Tip>
              ))}
            </div>
            <div className="mt-2.5 flex flex-wrap gap-x-5 gap-y-1 text-[12px] text-muted">
              {o.byPlan.map((x: any, i: number) => <span key={x.planId} className="flex items-center gap-1.5"><span className={cn('size-2 rounded-full', ['bg-line-strong', 'bg-accent', 'bg-signal', 'bg-ink/70', 'bg-accent/50'][i % 5])} />{x.name} <span className="tnum text-faint">{num(x.members)}</span></span>)}
            </div>
          </div>
        )}
      </Panel>

      <Panel padded={false}>
        {!plans.length ? <Empty icon={<Crown />} title="还没有会员套餐" action={<Button variant="primary" onClick={() => setEdit({ ...emptyPlan })}>新建会员</Button>} /> : (
          <Table>
            <thead><tr><th>会员</th><th className="!text-right">等级</th><th className="!text-right">月付 / 年付</th><th className="!text-right">每月积分</th><th>主要限制</th><th>公开</th><th /></tr></thead>
            <tbody>
              {plans.map((p) => (
                <tr key={p.id} className="cursor-pointer" onClick={() => setEdit(p)}>
                  <td>
                    <div className="flex items-center gap-2">
                      {p.level > 0 && <Crown className="size-3.5 text-warn" />}
                      <span className="font-medium">{p.name}</span>
                      <span className="font-mono text-[11px] text-faint">{p.code}</span>
                      {p.badge && <Badge tone="accent">{p.badge}</Badge>}
                    </div>
                    <div className="mt-0.5 max-w-xs truncate text-[12px] text-muted">{p.tagline}</div>
                  </td>
                  <td className="text-right tnum">{p.level}</td>
                  <td className="text-right tnum">{p.contactSales ? '定制' : p.priceMonthly ? `¥${p.priceMonthly / 100} / ¥${(p.priceYearly || 0) / 100}` : '免费'}</td>
                  <td className="text-right tnum">{wan(p.monthlyCredits)}{rateText(p.creditRate) && <div className="text-[11px] text-warn">消耗 {rateText(p.creditRate)}</div>}</td>
                  <td className="text-[12px] text-muted">{[`工作室 ${p.maxStudios || '不限'}`, `成员 ${p.seats || '不限'}`, `图书 ${p.maxBooks || '不限'}`, `${p.storageGb || '不限'} GB`].join(' · ')}</td>
                  <td onClick={(e) => e.stopPropagation()}>
                    <Switch checked={p.listed} onChange={async (v) => { await adminApi.put(`/plans/${p.id}`, { ...p, listed: v }); ainv(['/plans']); toast.success(v ? '已在会员页公开' : '已从会员页隐藏') }} />
                  </td>
                  <td onClick={(e) => e.stopPropagation()} className="text-right">
                    <div className="flex justify-end gap-1">
                      <IconButton label="复制为新会员" icon={<Copy className="size-4" />} onClick={() => setEdit({ ...p, id: undefined as any, code: p.code + '_copy', name: p.name + '（副本）', listed: false })} />
                      <IconButton label="删除" icon={<Trash2 className="size-4" />} onClick={async () => {
                        if (!(await confirm({ title: `删除「${p.name}」？`, description: '仍有用户使用或兑换码引用的会员不能删除，可以改为不公开。', danger: true, confirmText: '删除' }))) return
                        await adminApi.del(`/plans/${p.id}`)
                        ainv(['/plans'])
                        toast.success('已删除')
                      }} />
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Panel>
      <PlanEditor value={edit} onClose={() => setEdit(null)} />
    </div>
  )
}

const exportFormats: [string, string][] = [['mp3', 'MP3'], ['m4a', 'M4A'], ['m4b', 'M4B 有声书'], ['wav', 'WAV 无损']]

function PlanEditor({ value, onClose }: { value: Partial<Plan> | null; onClose: () => void }) {
  const [f, setF] = useState<any>(null)
  const [cycle, setCycle] = useState<Cycle>('month')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    // older plans may carry null lists; the editor always works on arrays
    if (value) setF({ ...emptyPlan, ...value, features: value.features ?? [], benefits: value.benefits ?? [], exportFormats: value.exportFormats ?? [], benefitsText: (value.benefits ?? []).join('\n') })
  }, [value])
  const open = !!value
  if (!f) return <Sheet open={false} onOpenChange={() => {}} title="" />
  const set = (k: string, v: any) => setF((x: any) => ({ ...x, [k]: v }))
  const numIn = (k: string, opts: { unit?: string; yuan?: boolean; min?: number; max?: number } = {}) => (
    <div className="relative">
      <Input type="number" min={opts.min ?? 0} max={opts.max} step={opts.yuan ? '0.01' : '1'} value={opts.yuan ? (f[k] ?? 0) / 100 : f[k] ?? 0} onChange={(e) => set(k, opts.yuan ? Math.round(Number(e.target.value) * 100) : Number(e.target.value))} className={opts.unit ? 'pr-10' : ''} />
      {opts.unit && <span className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-[12px] text-faint">{opts.unit}</span>}
    </div>
  )
  const preview: Plan = { ...f, benefits: f.benefitsText.split('\n').map((s: string) => s.trim()).filter(Boolean) }
  const save = async () => {
    setBusy(true)
    try {
      const { benefitsText, ...body } = f
      body.benefits = preview.benefits
      if (f.id) await adminApi.put(`/plans/${f.id}`, body)
      else await adminApi.post('/plans', body)
      ainv(['/plans'], ['/membership/overview'])
      toast.success('会员套餐已保存')
      onClose()
    } finally {
      setBusy(false)
    }
  }
  return (
    <Sheet open={open} onOpenChange={(o) => !o && onClose()} title={f.id ? `编辑「${f.name}」` : '新建会员'} description="0 表示不限（克隆音色为 0 表示不包含）。修改后立即对所有该会员用户生效。" width={980}
      footer={<><Button onClick={onClose}>取消</Button><Button variant="primary" loading={busy} disabled={!f.name.trim() || !f.code.trim()} onClick={save}>保存</Button></>}>
      <div className="grid gap-6 lg:grid-cols-[1fr_300px]">
        <div className="space-y-6">
          <Section title="基本信息">
            <Field label="名称"><Input value={f.name} onChange={(e) => set('name', e.target.value)} placeholder="例如 专业版" /></Field>
            <Field label="编码" hint="唯一标识，兑换码与配置里使用"><Input value={f.code} onChange={(e) => set('code', e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, ''))} className="font-mono" placeholder="pro" /></Field>
            <Field label="一句话介绍" className="col-span-2"><Input value={f.tagline} onChange={(e) => set('tagline', e.target.value)} placeholder="适合谁、解决什么问题" /></Field>
            <Field label="等级" hint="同时拥有多个会员时等级高的生效">{numIn('level')}</Field>
            <Field label="排序" hint="会员页从小到大展示">{numIn('sortOrder')}</Field>
          </Section>
          <Section title="价格" hint="用于展示；在线支付接入后按此价格收费">
            <Field label="月付">{numIn('priceMonthly', { unit: '元', yuan: true })}</Field>
            <Field label="年付" hint={f.priceMonthly && f.priceYearly ? `相当于月付 ${Math.round((f.priceYearly / (f.priceMonthly * 12)) * 100) / 10} 折` : '留 0 则不提供年付'}>{numIn('priceYearly', { unit: '元', yuan: true })}</Field>
            <Field label="划线原价（月）" hint="促销时展示，0 表示不展示">{numIn('originalMonthly', { unit: '元', yuan: true })}</Field>
            <div className="flex flex-col justify-end gap-2.5 pb-1">
              <Switch checked={f.contactSales} onChange={(v) => set('contactSales', v)} label="显示「联系我们」而不是价格" />
            </div>
          </Section>
          <Section title="积分" hint="积分随会员赠送，用于所有 AI 功能">
            <Field label="每月赠送">{numIn('monthlyCredits', { unit: '积分' })}</Field>
            <Field label="开通赠送" hint="每段会员首次生效时赠送一次">{numIn('bonusCredits', { unit: '积分' })}</Field>
            <Field label="月度积分有效期">{numIn('creditMonths', { unit: '个月', min: 1 })}</Field>
            <Field label="积分消耗" hint={rateText(f.creditRate) ? `会员使用 AI 功能时按 ${rateText(f.creditRate)} 扣积分` : '100 表示原价，90 表示 9 折'}>{numIn('creditRate', { unit: '%', min: 1, max: 100 })}</Field>
          </Section>
          <Section title="资源限制" hint="按账号或按工作室计算，已有内容超出时不删除，只是不能新增">
            <Field label="可拥有的工作室" hint="含个人工作室">{numIn('maxStudios', { unit: '个' })}</Field>
            <Field label="每个工作室成员">{numIn('seats', { unit: '人' })}</Field>
            <Field label="每个工作室图书">{numIn('maxBooks', { unit: '本' })}</Field>
            <Field label="每个工作室存储">{numIn('storageGb', { unit: 'GB' })}</Field>
            <Field label="克隆音色" hint="0 表示不包含克隆">{numIn('cloneVoices', { unit: '个' })}</Field>
            <Field label="单本字数上限" hint="0 表示按站点设置">{numIn('maxBookWords', { unit: '字' })}</Field>
            <Field label="单个文件上传" hint="0 表示按站点设置">{numIn('maxUploadMb', { unit: 'MB' })}</Field>
            <Field label={`排队优先级：${f.queuePriority}`} hint="平台繁忙时，数值高的任务先处理">
              <Slider value={f.queuePriority} min={0} max={10} onChange={(v) => set('queuePriority', v)} className="mt-2.5" />
            </Field>
          </Section>
          <Section title="权益">
            <div className="col-span-2 flex flex-wrap gap-x-6 gap-y-2.5">
              <Switch checked={f.highQuality} onChange={(v) => set('highQuality', v)} label="精品音色" />
              <Switch checked={f.commercial} onChange={(v) => set('commercial', v)} label="商用授权" />
            </div>
            <Field label="导出格式" hint="都不勾选表示全部格式" className="col-span-2">
              <div className="flex flex-wrap gap-x-5 gap-y-2">{exportFormats.map(([k, l]) => <Checkbox key={k} checked={f.exportFormats.includes(k)} onChange={(v) => set('exportFormats', v ? [...f.exportFormats, k] : f.exportFormats.filter((x: string) => x !== k))} label={l} />)}</div>
            </Field>
            <Field label="功能" className="col-span-2">
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">{planFeatures.map(([k, l]) => <Checkbox key={k} checked={f.features.includes(k)} onChange={(v) => set('features', v ? [...f.features, k] : f.features.filter((x: string) => x !== k))} label={l} />)}</div>
            </Field>
          </Section>
          <Section title="会员页展示">
            <Field label="角标" hint="例如 最受欢迎、限时特惠"><Input value={f.badge} maxLength={8} onChange={(e) => set('badge', e.target.value)} /></Field>
            <div className="flex flex-col justify-end gap-2.5 pb-1">
              <Switch checked={f.highlight} onChange={(v) => set('highlight', v)} label="重点推荐（卡片高亮）" />
              <Switch checked={f.listed} onChange={(v) => set('listed', v)} label="在会员页公开" />
            </div>
            <Field label="卖点" hint="每行一条，显示在会员卡片上；留空会根据配置自动生成" className="col-span-2">
              <Textarea rows={5} value={f.benefitsText} onChange={(e) => set('benefitsText', e.target.value)} />
              <button className="mt-1.5 flex items-center gap-1 text-[12px] text-accent hover:underline" onClick={() => set('benefitsText', [`每月 ${wan(f.monthlyCredits)} 积分${f.bonusCredits ? `，开通再送 ${wan(f.bonusCredits)}` : ''}`, ...autoBenefits(preview)].join('\n'))}>
                <Wand2 className="size-3" /> 根据配置生成
              </button>
            </Field>
          </Section>
        </div>
        <div className="lg:sticky lg:top-0 lg:self-start">
          <div className="mb-3 flex items-center justify-between">
            <span className="text-[12px] text-faint">实时预览</span>
            <Segmented size="sm" value={cycle} onChange={setCycle} items={[{ value: 'month', label: '月' }, { value: 'year', label: '年' }]} />
          </div>
          <div className="pt-2"><PlanCard plan={preview} cycle={cycle} action={<Button variant={preview.highlight ? 'primary' : 'secondary'} className="w-full">{preview.contactSales ? '联系我们' : `开通${preview.name || ''}`}</Button>} /></div>
        </div>
      </div>
    </Sheet>
  )
}

function Section({ title, hint, children }: { title: string; hint?: string; children: React.ReactNode }) {
  return (
    <section>
      <div className="mb-3 flex items-baseline gap-2 border-b border-line pb-2">
        <h3 className="text-[13.5px] font-semibold">{title}</h3>
        {hint && <span className="text-[12px] text-faint">{hint}</span>}
      </div>
      <div className="grid grid-cols-2 gap-4">{children}</div>
    </section>
  )
}

// ======================================================================================
// 兑换码
// ======================================================================================

const stateTone: Record<string, [string, any]> = { active: ['进行中', 'ok'], disabled: ['已停用', 'neutral'], expired: ['已过期', 'warn'] }
const codeState: Record<string, [string, any]> = { unused: ['未使用', 'neutral'], used: ['已使用', 'accent'], disabled: ['已停用', 'warn'] }

function batchContent(b: any) {
  if (b.kind === 'credits') return `${num(b.credits)} 积分${b.creditDays ? ` · ${b.creditDays} 天有效` : ''}`
  return `${b.planName || '会员'} · ${b.days ? `${b.days} 天` : '永久'}`
}

async function exportBatch(b: any, status = '') {
  const res = await fetch(`/api/admin/v1/redeem-batches/${b.id}/export${status ? `?status=${status}` : ''}`, { headers: adminApi.token ? { Authorization: 'Bearer ' + adminApi.token } : {}, credentials: 'include' })
  if (!res.ok) {
    toast.error('导出失败')
    return
  }
  const url = URL.createObjectURL(await res.blob())
  const a = document.createElement('a')
  a.href = url
  a.download = `兑换码-${b.name}${status === 'unused' ? '-未使用' : ''}.csv`
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
  ainv(['/redeem-batches'])
  toast.success('已导出 CSV，可直接用 Excel 打开')
}

export function Redeem() {
  const [page, setPage] = useState(1)
  const [kind, setKind] = useState('')
  const [status, setStatus] = useState('')
  const [q, setQ] = useState('')
  const [create, setCreate] = useState(false)
  const [detail, setDetail] = useState<string | null>(null)
  const [lookup, setLookup] = useState(false)
  const { data } = useAdmin<any>('/redeem-batches', { page, pageSize: 20, kind, status, q })
  const items: any[] = data?.items ?? []
  return (
    <div className="space-y-5">
      <PageHeader
        title="兑换码"
        description="批量生成可兑换会员或积分的兑换码，导出后通过活动、渠道或客服发放；也可以设一个通用口令给活动用。"
        actions={<><Button icon={<Search className="size-4" />} onClick={() => setLookup(true)}>查询兑换码</Button><Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setCreate(true)}>生成兑换码</Button></>}
      />
      <div className="flex flex-wrap gap-3">
        <Segmented size="sm" value={kind} onChange={(v) => { setKind(v); setPage(1) }} items={[{ value: '', label: '全部' }, { value: 'membership', label: '会员' }, { value: 'credits', label: '积分' }]} />
        <Select size="sm" value={status} onChange={(v) => { setStatus(v); setPage(1) }} options={[['', '全部状态'], ['active', '启用中'], ['disabled', '已停用']]} className="w-28" />
        <Input value={q} onChange={(e) => { setQ(e.target.value); setPage(1) }} placeholder="搜索批次名称、渠道" icon={<Search className="size-4" />} className="!h-8 w-60" />
      </div>
      <Panel padded={false}>
        {!items.length ? <Empty icon={<Ticket />} title="还没有兑换码" description="生成一批兑换码，导出后就可以发放给用户。" action={<Button variant="primary" onClick={() => setCreate(true)}>生成兑换码</Button>} /> : (
          <Table>
            <thead><tr><th>批次</th><th>内容</th><th>方式</th><th className="w-44">使用进度</th><th>截止</th><th>状态</th></tr></thead>
            <tbody>
              {items.map((b) => {
                const total = b.mode === 'unique' ? b.count : b.maxUses
                return (
                  <tr key={b.id} className="cursor-pointer" onClick={() => setDetail(b.id)}>
                    <td>
                      <div className="font-medium">{b.name}</div>
                      <div className="text-[11.5px] text-faint">{b.channel ? `${b.channel} · ` : ''}{date(b.createdAt)}{b.createdBy ? ` · ${b.createdBy}` : ''}</div>
                    </td>
                    <td><span className="flex items-center gap-1.5">{b.kind === 'membership' ? <Crown className="size-3.5 text-warn" /> : <Sparkles className="size-3.5 text-accent" />}{batchContent(b)}</span></td>
                    <td className="text-muted">{b.mode === 'unique' ? `一码一用 · ${num(b.count)} 个` : <span>通用码 <span className="font-mono text-ink">{b.sharedCode}</span></span>}</td>
                    <td>
                      <div className="flex justify-between text-[12px]"><span className="tnum">{num(b.used)}</span><span className="text-faint tnum">{total ? num(total) : '不限'}</span></div>
                      <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-sunken"><div className="h-full rounded-full bg-accent" style={{ width: `${total ? Math.min((b.used / total) * 100, 100) : b.used ? 100 : 0}%`, opacity: total ? 1 : 0.35 }} /></div>
                    </td>
                    <td className="text-[12px] text-muted">{b.expiresAt ? dateTime(b.expiresAt) : '长期'}</td>
                    <td><Badge tone={stateTone[b.state]?.[1]}>{stateTone[b.state]?.[0] ?? b.state}</Badge></td>
                  </tr>
                )
              })}
            </tbody>
          </Table>
        )}
      </Panel>
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
      <CreateBatch open={create} onClose={() => setCreate(false)} onCreated={(id) => setDetail(id)} />
      <BatchDetail id={detail} onClose={() => setDetail(null)} />
      <Lookup open={lookup} onOpenChange={setLookup} onOpenBatch={(id) => { setLookup(false); setDetail(id) }} />
    </div>
  )
}

const dayPresets: [number, string][] = [[7, '7 天'], [30, '30 天'], [90, '90 天'], [365, '1 年'], [0, '永久']]

function CreateBatch({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: (id: string) => void }) {
  const plans = useAdmin<any>('/plans', { pageSize: 100 }, { enabled: open })
  const paid: Plan[] = (plans.data?.items ?? []).filter((p: Plan) => p.level > 0)
  const init = { name: '', kind: 'membership', planId: '', days: 30, credits: 10000, creditDays: 365, mode: 'unique', count: 100, code: '', maxUses: 0, perUserLimit: 1, newUsersOnly: false, prefix: '', startsAt: '', expiresAt: '', channel: '', note: '' }
  const [f, setF] = useState<any>(init)
  const [busy, setBusy] = useState(false)
  const [created, setCreated] = useState<any>(null)
  useEffect(() => {
    if (open) {
      setF({ ...init, planId: paid[0]?.id ?? '' })
      setCreated(null)
    }
  }, [open])
  useEffect(() => {
    if (open && !f.planId && paid[0]) setF((x: any) => ({ ...x, planId: paid[0].id }))
  }, [paid.length])
  const set = (k: string, v: any) => setF((x: any) => ({ ...x, [k]: v }))
  const plan = paid.find((p) => p.id === f.planId)
  const content = f.kind === 'membership' ? `「${plan?.name ?? '会员'}」${f.days ? `${f.days} 天` : '永久'}` : `${num(f.credits)} 积分${f.creditDays ? `（${f.creditDays} 天内有效）` : ''}`
  const summary = f.mode === 'unique'
    ? `将生成 ${num(f.count)} 个兑换码，每个可兑换 ${content}`
    : `通用码 ${f.code ? f.code.toUpperCase() : '……'} 可兑换 ${content}${f.maxUses ? `，共 ${num(f.maxUses)} 份` : '，不限份数'}`
  const rules = [f.perUserLimit ? `每人限 ${f.perUserLimit} 次` : '每人不限次数', f.newUsersOnly && '仅限未开通过会员的用户', f.startsAt && `${f.startsAt.replace('T', ' ')} 开始`, f.expiresAt ? `${f.expiresAt.replace('T', ' ')} 截止` : '长期有效'].filter(Boolean).join('，')
  const submit = async () => {
    setBusy(true)
    try {
      const body = { ...f, startsAt: f.startsAt ? new Date(f.startsAt).toISOString() : null, expiresAt: f.expiresAt ? new Date(f.expiresAt).toISOString() : null }
      if (!body.name.trim()) body.name = f.kind === 'membership' ? `${plan?.name ?? '会员'} ${f.days ? f.days + ' 天' : '永久'}` : `${num(f.credits)} 积分`
      const b = await adminApi.post('/redeem-batches', body)
      ainv(['/redeem-batches'])
      setCreated(b)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Sheet open={open} onOpenChange={(o) => !o && onClose()} title="生成兑换码" width={560}
      footer={created ? <><Button onClick={onClose}>关闭</Button><Button onClick={() => { onClose(); onCreated(created.id) }}>查看批次</Button>{created.mode === 'unique' && <Button variant="primary" icon={<Download className="size-4" />} onClick={() => exportBatch(created)}>导出 CSV</Button>}</>
        : <><Button onClick={onClose}>取消</Button><Button variant="primary" loading={busy} disabled={(f.kind === 'membership' && !f.planId) || (f.mode === 'shared' && f.code.trim().length < 4)} onClick={submit}>生成</Button></>}>
      <AnimatePresence mode="wait">
        {created ? (
          <motion.div key="done" initial={{ opacity: 0, scale: 0.97 }} animate={{ opacity: 1, scale: 1 }} className="flex flex-col items-center py-10 text-center">
            <motion.span initial={{ scale: 0.3, rotate: -20 }} animate={{ scale: 1, rotate: 0 }} transition={{ type: 'spring', stiffness: 400, damping: 15 }} className="grid size-14 place-items-center rounded-full bg-accent-soft text-accent"><CheckCircle2 className="size-7" /></motion.span>
            <div className="mt-4 text-[17px] font-semibold">已生成「{created.name}」</div>
            <p className="mt-1.5 text-[13px] text-muted">{created.mode === 'unique' ? `${num(created.count)} 个兑换码，导出后即可发放。` : <>通用码 <span className="font-mono font-medium text-ink">{created.sharedCode}</span> 已生效</>}</p>
            {created.mode === 'shared' && <Button className="mt-4" icon={<Copy className="size-4" />} onClick={() => { copy(created.sharedCode); toast.success('已复制') }}>复制兑换码</Button>}
          </motion.div>
        ) : (
          <motion.div key="form" initial={{ opacity: 0 }} animate={{ opacity: 1 }} className="space-y-5">
            <Field label="兑换内容">
              <Segmented value={f.kind} onChange={(v) => set('kind', v)} items={[{ value: 'membership', label: '会员' }, { value: 'credits', label: '积分' }]} />
            </Field>
            {f.kind === 'membership' ? (
              <div className="grid grid-cols-2 gap-4">
                <Field label="会员">
                  {paid.length ? <Select value={f.planId} onChange={(v) => set('planId', v)} options={paid.map((p) => [p.id, p.name])} /> : <p className="text-[12.5px] text-faint">请先新建付费会员</p>}
                </Field>
                <Field label="时长">
                  <div className="flex flex-wrap gap-1.5">
                    {dayPresets.map(([d, l]) => <button key={d} onClick={() => set('days', d)} className={cn('h-8 rounded-control border px-2.5 text-[12.5px] transition-colors', f.days === d ? 'border-accent bg-accent-soft text-accent' : 'border-line hover:border-line-strong')}>{l}</button>)}
                  </div>
                  <div className="relative mt-2"><Input type="number" min={0} max={3650} value={f.days} onChange={(e) => set('days', Number(e.target.value))} className="pr-8" /><span className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-[12px] text-faint">天</span></div>
                </Field>
              </div>
            ) : (
              <div className="grid grid-cols-2 gap-4">
                <Field label="积分数量"><Input type="number" min={1} value={f.credits} onChange={(e) => set('credits', Number(e.target.value))} /></Field>
                <Field label="积分有效期" hint="兑换后多少天内有效，0 表示永久"><div className="relative"><Input type="number" min={0} value={f.creditDays} onChange={(e) => set('creditDays', Number(e.target.value))} className="pr-8" /><span className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-[12px] text-faint">天</span></div></Field>
              </div>
            )}
            <Field label="生成方式">
              <div className="grid grid-cols-2 gap-2">
                {[['unique', '一码一用', '批量生成，导出后逐个发放'], ['shared', '通用口令', '一个码多人领，适合活动']].map(([k, t, d]) => (
                  <button key={k} onClick={() => set('mode', k)} className={cn('rounded-[11px] border px-3.5 py-3 text-left transition-colors', f.mode === k ? 'border-accent bg-accent-soft/60' : 'border-line hover:border-line-strong')}>
                    <div className="text-[13.5px] font-medium">{t}</div>
                    <div className="mt-0.5 text-[12px] text-muted">{d}</div>
                  </button>
                ))}
              </div>
            </Field>
            {f.mode === 'unique' ? (
              <div className="grid grid-cols-3 gap-4">
                <Field label="数量" hint="最多 20000"><Input type="number" min={1} max={20000} value={f.count} onChange={(e) => set('count', Number(e.target.value))} /></Field>
                <Field label="前缀" hint="可选，如 VIP"><Input value={f.prefix} maxLength={6} onChange={(e) => set('prefix', e.target.value.toUpperCase().replace(/[^A-Z0-9]/g, ''))} className="font-mono" /></Field>
                <Field label="每人可兑换" hint="0 表示不限"><Input type="number" min={0} value={f.perUserLimit} onChange={(e) => set('perUserLimit', Number(e.target.value))} /></Field>
              </div>
            ) : (
              <div className="grid grid-cols-3 gap-4">
                <Field label="兑换口令" className="col-span-1"><Input value={f.code} maxLength={32} onChange={(e) => set('code', e.target.value.toUpperCase().replace(/[^A-Z0-9]/g, ''))} className="font-mono" placeholder="WELCOME2026" /></Field>
                <Field label="总份数" hint="0 表示不限"><Input type="number" min={0} value={f.maxUses} onChange={(e) => set('maxUses', Number(e.target.value))} /></Field>
                <Field label="每人可兑换" hint="0 表示不限"><Input type="number" min={0} value={f.perUserLimit} onChange={(e) => set('perUserLimit', Number(e.target.value))} /></Field>
              </div>
            )}
            <div className="grid grid-cols-2 gap-4">
              <Field label="开始时间" hint="可选"><DateInput time value={f.startsAt} onChange={(v) => set('startsAt', v)} placeholder="立即生效" /></Field>
              <Field label="截止时间" hint="过期后不能再兑换"><DateInput time value={f.expiresAt} onChange={(v) => set('expiresAt', v)} placeholder="长期有效" /></Field>
            </div>
            <Switch checked={f.newUsersOnly} onChange={(v) => set('newUsersOnly', v)} label="仅限从未开通过会员的用户（拉新活动）" />
            <div className="grid grid-cols-2 gap-4">
              <Field label="批次名称" hint="留空自动生成"><Input value={f.name} onChange={(e) => set('name', e.target.value)} placeholder="例如 双十一直播间" /></Field>
              <Field label="渠道" hint="用于统计各渠道的兑换效果"><Input value={f.channel} onChange={(e) => set('channel', e.target.value)} placeholder="例如 小红书、合作书城" /></Field>
            </div>
            <Field label="备注"><Textarea rows={2} value={f.note} onChange={(e) => set('note', e.target.value)} /></Field>
            <div className="rounded-[11px] bg-sunken/70 px-4 py-3 text-[13px]">
              <div className="font-medium">{summary}</div>
              <div className="mt-0.5 text-[12px] text-muted">{rules}</div>
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </Sheet>
  )
}

function BatchDetail({ id, onClose }: { id: string | null; onClose: () => void }) {
  const { data: b, refetch } = useAdmin<any>(`/redeem-batches/${id}`, {}, { enabled: !!id })
  const [tab, setTab] = useState<'codes' | 'uses'>('codes')
  const [status, setStatus] = useState('')
  const [q, setQ] = useState('')
  const [page, setPage] = useState(1)
  const [reveal, setReveal] = useState(false)
  const codes = useAdmin<any>(`/redeem-batches/${id}/codes`, { page, pageSize: 50, status, q }, { enabled: !!id && tab === 'codes' })
  const uses = useAdmin<any>(`/redeem-batches/${id}/uses`, { page, pageSize: 50 }, { enabled: !!id && tab === 'uses' })
  useEffect(() => { setPage(1); setReveal(false) }, [id, tab, status, q])
  const patch = async (body: any, msg: string) => {
    await adminApi.patch(`/redeem-batches/${id}`, body)
    refetch()
    ainv(['/redeem-batches'])
    toast.success(msg)
  }
  const mask = (c: string) => (reveal || b?.mode === 'shared' ? c : c.replace(/[A-Z0-9]{4}(?=-[A-Z0-9]{4}$)/, '••••'))
  return (
    <Sheet open={!!id} onOpenChange={(o) => !o && onClose()} title={b?.name} description={b && `${batchContent(b)} · ${b.mode === 'unique' ? '一码一用' : '通用口令'}${b.channel ? ` · ${b.channel}` : ''}`} width={720}>
      {b && (
        <div className="space-y-5">
          <div className="grid grid-cols-4 gap-4">
            <Stat label="已兑换" value={num(b.used)} />
            <Stat label="未使用" value={b.mode === 'unique' ? num(b.codes.unused) : b.maxUses ? num(Math.max(b.maxUses - b.used, 0)) : '不限'} />
            <Stat label="已停用" value={num(b.codes.disabled)} />
            <Stat label="导出次数" value={num(b.exported)} />
          </div>
          <div className="flex flex-wrap gap-2">
            {b.mode === 'unique' && <Button variant="primary" size="sm" icon={<Download className="size-4" />} onClick={() => exportBatch(b, 'unused')}>导出未使用</Button>}
            <Button size="sm" icon={<Download className="size-4" />} onClick={() => exportBatch(b)}>导出全部</Button>
            {b.status === 'active'
              ? <Button size="sm" variant="danger" onClick={async () => { if (await confirm({ title: '停用整个批次？', description: '停用后这个批次里所有未使用的兑换码都不能再兑换，已兑换的不受影响。可以随时重新启用。', danger: true, confirmText: '停用' })) patch({ status: 'disabled' }, '批次已停用') }}>停用批次</Button>
              : <Button size="sm" onClick={() => patch({ status: 'active' }, '批次已启用')}>重新启用</Button>}
            <Button size="sm" variant="ghost" onClick={async () => { const v = prompt('新的截止时间（例如 2026-12-31 23:59，留空表示长期有效）', b.expiresAt ? dateTime(b.expiresAt) : ''); if (v === null) return; if (!v.trim()) patch({ clearExpiresAt: true }, '已改为长期有效'); else { const d = new Date(v.replace(' ', 'T')); if (isNaN(d.getTime())) { toast.error('时间格式不正确'); return } patch({ expiresAt: d.toISOString() }, '截止时间已更新') } }}>修改截止时间</Button>
          </div>
          <dl className="grid grid-cols-2 gap-x-6 gap-y-2 rounded-[11px] bg-sunken/50 px-4 py-3 text-[12.5px]">
            <Info k="每人可兑换" v={b.perUserLimit ? `${b.perUserLimit} 次` : '不限'} />
            <Info k="仅限新用户" v={b.newUsersOnly ? '是' : '否'} />
            <Info k="开始" v={b.startsAt ? dateTime(b.startsAt) : '立即'} />
            <Info k="截止" v={b.expiresAt ? dateTime(b.expiresAt) : '长期有效'} />
            <Info k="创建" v={`${dateTime(b.createdAt)}${b.createdBy ? ` · ${b.createdBy}` : ''}`} />
            {b.note && <Info k="备注" v={b.note} />}
          </dl>
          <div className="flex items-center justify-between border-b border-line">
            <Tabs value={tab} onChange={setTab} items={[{ value: 'codes', label: '兑换码' }, { value: 'uses', label: `兑换记录 ${b.used ? `(${b.used})` : ''}` }]} />
            {tab === 'codes' && b.mode === 'unique' && <button className="flex items-center gap-1 pb-2 text-[12px] text-muted hover:text-ink" onClick={() => setReveal(!reveal)}>{reveal ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}{reveal ? '隐藏' : '显示完整'}</button>}
          </div>
          {tab === 'codes' ? (
            <>
              <div className="flex gap-2">
                <Select size="sm" value={status} onChange={setStatus} options={[['', '全部'], ['unused', '未使用'], ['used', '已使用'], ['disabled', '已停用']]} className="w-28" />
                <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索兑换码" icon={<Search className="size-4" />} className="!h-8 w-56 font-mono" />
              </div>
              <Table>
                <thead><tr><th>兑换码</th><th>状态</th><th>使用者</th><th>时间</th><th /></tr></thead>
                <tbody>
                  {(codes.data?.items ?? []).map((c: any) => (
                    <tr key={c.id}>
                      <td className="font-mono text-[12.5px]">{mask(c.code)}</td>
                      <td><Badge tone={codeState[c.status]?.[1]}>{b.mode === 'shared' && c.status !== 'disabled' ? `已用 ${c.uses} 次` : codeState[c.status]?.[0]}</Badge></td>
                      <td className="text-muted">{c.user || '—'}</td>
                      <td className="text-[12px] text-faint">{c.usedAt ? dateTime(c.usedAt) : ''}</td>
                      <td className="text-right">
                        <div className="flex justify-end gap-1">
                          <IconButton label="复制" icon={<Copy className="size-3.5" />} onClick={() => { copy(c.code); toast.success('已复制') }} />
                          {c.status === 'unused' && <Button size="xs" variant="ghost" onClick={async () => { await adminApi.post(`/redeem-codes/${c.id}/status`, { status: 'disabled' }); codes.refetch(); refetch(); toast.success('已停用') }}>停用</Button>}
                          {c.status === 'disabled' && <Button size="xs" variant="ghost" onClick={async () => { await adminApi.post(`/redeem-codes/${c.id}/status`, { status: 'unused' }); codes.refetch(); refetch(); toast.success('已恢复') }}>恢复</Button>}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </Table>
              {codes.data && <Pagination page={codes.data.page} pageSize={codes.data.pageSize} total={codes.data.total} onChange={setPage} />}
            </>
          ) : !uses.data?.items?.length ? <Empty title="还没有人兑换" /> : (
            <>
              <Table>
                <thead><tr><th>用户</th><th>兑换码</th><th>获得</th><th>时间</th></tr></thead>
                <tbody>{uses.data.items.map((u: any) => <tr key={u.id}><td>{u.user}</td><td className="font-mono text-[12px] text-muted">{u.code}</td><td className="max-w-56 truncate">{u.summary}</td><td className="text-[12px] text-faint">{dateTime(u.createdAt)}</td></tr>)}</tbody>
              </Table>
              <Pagination page={uses.data.page} pageSize={uses.data.pageSize} total={uses.data.total} onChange={setPage} />
            </>
          )}
        </div>
      )}
    </Sheet>
  )
}

function Info({ k, v }: { k: string; v: React.ReactNode }) {
  return <div className="flex gap-2"><dt className="w-20 shrink-0 text-faint">{k}</dt><dd className="min-w-0">{v}</dd></div>
}

function Lookup({ open, onOpenChange, onOpenBatch }: { open: boolean; onOpenChange: (v: boolean) => void; onOpenBatch: (id: string) => void }) {
  const [code, setCode] = useState('')
  const [res, setRes] = useState<any>(null)
  const [busy, setBusy] = useState(false)
  useEffect(() => { if (!open) { setCode(''); setRes(null) } }, [open])
  const run = async () => {
    setBusy(true)
    try {
      setRes(await adminApi.get(`/redeem-codes/lookup?code=${encodeURIComponent(code)}`))
    } catch (e: any) {
      setRes({ error: e.message })
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="查询兑换码" description="用户反馈兑换问题时，查一下这个码的状态和使用记录。" width={520}>
      <form className="flex gap-2" onSubmit={(e) => { e.preventDefault(); code.trim() && run() }}>
        <Input value={code} onChange={(e) => setCode(e.target.value.toUpperCase())} placeholder="输入兑换码" className="font-mono" />
        <Button type="submit" variant="primary" loading={busy} disabled={!code.trim()}>查询</Button>
      </form>
      {res?.error && <p className="mt-4 text-[13px] text-danger">{res.error}</p>}
      {res?.code && (
        <div className="mt-4 space-y-3 text-[13px]">
          <div className="flex items-center gap-2">
            <span className="font-mono font-medium">{res.code.code}</span>
            <Badge tone={codeState[res.code.status]?.[1]}>{codeState[res.code.status]?.[0]}</Badge>
            <Badge tone={stateTone[res.batch.state]?.[1]}>批次{stateTone[res.batch.state]?.[0]}</Badge>
          </div>
          <div className="text-muted">{batchContent(res.batch)} · 批次「{res.batch.name}」{res.batch.expiresAt ? ` · ${dateTime(res.batch.expiresAt)} 截止` : ''}</div>
          {res.uses.length > 0 && <ul className="space-y-1 rounded-[10px] bg-sunken/60 px-3 py-2">{res.uses.map((u: any) => <li key={u.id} className="flex justify-between gap-3"><span>{u.user}</span><span className="text-faint">{dateTime(u.createdAt)}</span></li>)}</ul>}
          <Button size="sm" onClick={() => onOpenBatch(res.batch.id)}>打开批次</Button>
        </div>
      )}
    </Dialog>
  )
}

// ======================================================================================
// 用户详情里的会员面板
// ======================================================================================

const sourceName: Record<string, string> = { redeem: '兑换码', admin: '管理员开通', trial: '新用户试用', order: '购买', migrate: '系统迁移' }
const periodState: Record<string, [string, any]> = { active: ['生效中', 'accent'], paused: ['已顺延', 'warn'], queued: ['待生效', 'neutral'], ended: ['已结束', 'neutral'], revoked: ['已撤销', 'danger'] }

export function UserMembership({ userId }: { userId: string }) {
  const { data: v, refetch } = useAdmin<any>(`/users/${userId}/membership`)
  const plans = useAdmin<any>('/plans', { pageSize: 100 })
  const paid: Plan[] = (plans.data?.items ?? []).filter((p: Plan) => p.level > 0)
  const [grant, setGrant] = useState(false)
  const [g, setG] = useState({ planId: '', days: 30, note: '' })
  if (!v) return null
  return (
    <Panel title="会员" actions={<Button size="xs" variant="primary" icon={<Crown className="size-3.5" />} onClick={() => { setG({ planId: paid[0]?.id ?? '', days: 30, note: '' }); setGrant(true) }}>开通 / 续期</Button>}>
      <div className="flex flex-wrap items-center gap-2 text-[13px]">
        <Crown className={cn('size-4', v.isDefault ? 'text-faint' : 'text-warn')} />
        <span className="font-medium">{v.plan.name}</span>
        <span className="text-muted">{v.isDefault ? '（默认）' : v.expiresAt ? `有效期至 ${dateTime(v.expiresAt)}` : '永久'}</span>
        <span className="ml-auto text-[12px] text-faint">拥有 {v.usage.studios[0]} 个工作室</span>
      </div>
      {v.periods.length > 0 && (
        <ul className="mt-3 divide-y divide-line rounded-[10px] border border-line text-[12.5px]">
          {v.periods.map((p: any) => (
            <li key={p.id} className="flex items-center gap-2 px-3 py-2">
              <span className="font-medium">{p.planName}</span>
              <Badge tone={periodState[p.state]?.[1]}>{periodState[p.state]?.[0]}</Badge>
              <span className="text-muted">{p.days ? `${p.days} 天` : '永久'} · {sourceName[p.source] ?? p.source}{p.note ? ` · ${p.note}` : ''}</span>
              <span className="ml-auto text-faint tnum">{date(p.startsAt)} — {p.expiresAt ? date(p.expiresAt) : '永久'}</span>
              {['active', 'paused', 'queued'].includes(p.state) && (
                <Button size="xs" variant="ghost" onClick={async () => {
                  if (!(await confirm({ title: '撤销这段会员？', description: '撤销后立即失效，已发放的积分不会收回。', danger: true, confirmText: '撤销' }))) return
                  await adminApi.del(`/membership-periods/${p.id}`)
                  refetch()
                  toast.success('已撤销')
                }}>撤销</Button>
              )}
            </li>
          ))}
        </ul>
      )}
      <Dialog open={grant} onOpenChange={setGrant} title="开通 / 续期会员" description="已有同等会员会顺延；开通更高等级时，原会员暂停并在之后恢复。用户会收到通知。" width={460}
        footer={<><Button onClick={() => setGrant(false)}>取消</Button><Button variant="primary" disabled={!g.planId} onClick={async () => {
          const r = await adminApi.post(`/users/${userId}/membership`, g)
          setGrant(false)
          refetch()
          toast.success(r.summary)
        }}>确认开通</Button></>}>
        <div className="space-y-4">
          <Field label="会员"><Select value={g.planId} onChange={(v) => setG({ ...g, planId: v })} options={paid.map((p) => [p.id, p.name])} /></Field>
          <Field label="时长">
            <div className="flex flex-wrap gap-1.5">{dayPresets.map(([d, l]) => <button key={d} onClick={() => setG({ ...g, days: d })} className={cn('h-8 rounded-control border px-2.5 text-[12.5px]', g.days === d ? 'border-accent bg-accent-soft text-accent' : 'border-line hover:border-line-strong')}>{l}</button>)}</div>
            <Input type="number" min={0} className="mt-2" value={g.days} onChange={(e) => setG({ ...g, days: Number(e.target.value) })} />
          </Field>
          <Field label="原因" hint="记录到操作日志，例如 客诉补偿、合作赠送"><Input value={g.note} onChange={(e) => setG({ ...g, note: e.target.value })} /></Field>
        </div>
      </Dialog>
    </Panel>
  )
}
