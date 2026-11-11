import { useQuery } from '@tanstack/react-query'
import { AnimatePresence, motion } from 'framer-motion'
import { CalendarClock, CheckCircle2, ChevronDown, Crown, Gift, Info, Sparkles, Ticket } from '@/components/icons'
import { useEffect, useRef, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { bytes, date, dateTime } from '@/lib/format'
import { useSession, useStudio } from '@/lib/session'
import { CompareTable, Cycle, Meter, Plan, PlanCard, priceFor, rateText, wan } from '@/components/plans'
import { Badge, Button, cn, Collapse, Dialog, Empty, Input, PageHeader, Panel, Segmented, Select, Skeleton, Tabs } from '@/components/ui'
import { invalidate } from '../hooks'

type View = {
  plan: Plan; isDefault: boolean; defaultPlan: { id: string; name: string }; expiresAt: string | null; nextCreditAt: string | null
  creditStudioId: string; ownedStudios: { id: string; name: string; type: string }[]; periods: any[]; everPaid: boolean
  studio?: { id: string; name: string; plan: Plan; own: boolean; contract: boolean }
  usage: { studios: [number, number]; books: [number, number]; seats: [number, number]; storage: [number, number]; cloneVoices: [number, number] }
}

const sourceName: Record<string, string> = { redeem: '兑换码', admin: '客服开通', trial: '新用户试用', order: '购买', migrate: '系统迁移' }
const stateBadge: Record<string, [string, any]> = {
  active: ['生效中', 'accent'], paused: ['已顺延', 'warn'], queued: ['待生效', 'neutral'], ended: ['已结束', 'neutral'], revoked: ['已撤销', 'danger'],
}

export default function Membership() {
  const studio = useStudio()
  const loc = useLocation()
  const site = useSession((s) => s.site)
  const view = useQuery<View>({ queryKey: ['membership', studio?.id], queryFn: () => api.get(`/membership?studioId=${studio?.id ?? ''}`), enabled: !!studio })
  const plans = useQuery<Plan[]>({ queryKey: ['plans'], queryFn: () => api.get('/plans') })
  const [cycle, setCycle] = useState<Cycle>('year')
  const [compare, setCompare] = useState(false)
  const [buy, setBuy] = useState<Plan | null>(null)
  const plansRef = useRef<HTMLDivElement>(null)
  const redeemRef = useRef<HTMLDivElement>(null)
  const v = view.data

  // deep links from the upgrade dialog: #plans / #redeem
  useEffect(() => {
    if (!v) return
    const target = loc.hash === '#plans' ? plansRef.current : loc.hash === '#redeem' ? redeemRef.current : null
    if (target) setTimeout(() => target.scrollIntoView({ behavior: 'smooth', block: 'start' }), 120)
    if (loc.hash === '#redeem') setTimeout(() => redeemRef.current?.querySelector('input')?.focus(), 500)
  }, [loc.hash, !!v])

  const listed = plans.data ?? []
  const maxSave = Math.max(0, ...listed.map((p) => priceFor(p, 'year').save))
  const refresh = () => {
    invalidate(['membership'], ['credits'], ['ledger'], ['redeem-history'])
    useSession.getState().load()
  }

  return (
    <div className="space-y-8">
      <PageHeader title="会员中心" description="会员决定可用的资源和功能，并且每月赠送积分。到期后已有的图书和音频都会保留。" />

      {!v ? <Skeleton className="h-48 rounded-panel" /> : <Hero v={v} onUpgrade={() => plansRef.current?.scrollIntoView({ behavior: 'smooth' })} onChanged={refresh} />}

      {v && <Usage v={v} />}

      <div ref={redeemRef} className="scroll-mt-6">
        <Redeem onDone={refresh} />
      </div>

      <section ref={plansRef} className="scroll-mt-6">
        <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
          <div>
            <h2 className="font-serif text-[18px] font-semibold">会员方案</h2>
            <p className="mt-1 text-[13px] text-muted">所有方案的积分都可用于 AI 配音、画本、选角、配乐等全部功能。</p>
          </div>
          <Segmented value={cycle} onChange={setCycle} items={[{ value: 'month', label: '按月' }, { value: 'year', label: maxSave > 0 ? `按年 · 省 ${maxSave}%` : '按年' }]} />
        </div>
        {plans.isLoading ? (
          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-96 rounded-panel" />)}</div>
        ) : (
          <div className={cn('grid gap-4 pt-2 md:grid-cols-2', listed.length >= 4 ? 'xl:grid-cols-4' : 'xl:grid-cols-3')}>
            {listed.map((p, i) => {
              const current = v?.plan.id === p.id
              const higher = (v?.plan.level ?? 0) < p.level
              return (
                <PlanCard
                  key={p.id}
                  plan={p}
                  cycle={cycle}
                  current={current}
                  index={i}
                  action={
                    current ? (
                      v?.isDefault ? <Button className="w-full" disabled>当前方案</Button> : <Button className="w-full" onClick={() => setBuy(p)}>续费</Button>
                    ) : p.contactSales ? (
                      <Button className="w-full" onClick={() => setBuy(p)}>联系我们</Button>
                    ) : higher ? (
                      <Button variant={p.highlight ? 'primary' : 'secondary'} className="w-full" onClick={() => setBuy(p)}>{v?.isDefault ? '开通' : '升级'}{p.name}</Button>
                    ) : p.priceMonthly > 0 ? (
                      <Button variant="ghost" className="w-full" onClick={() => setBuy(p)}>开通</Button>
                    ) : null
                  }
                />
              )
            })}
          </div>
        )}
        <button onClick={() => setCompare(!compare)} className="mx-auto mt-5 flex items-center gap-1 text-[13px] text-muted hover:text-ink">
          {compare ? '收起完整对比' : '查看完整对比'} <ChevronDown className={cn('size-4 transition-transform duration-200', compare && 'rotate-180')} />
        </button>
        <Collapse open={compare}>
          <div className="pt-4"><CompareTable plans={listed} currentId={v?.plan.id} site={site} /></div>
        </Collapse>
      </section>

      {v && <History v={v} />}

      <BuyDialog plan={buy} cycle={cycle} onClose={() => setBuy(null)} onRedeem={() => { setBuy(null); redeemRef.current?.scrollIntoView({ behavior: 'smooth' }); setTimeout(() => redeemRef.current?.querySelector('input')?.focus(), 400) }} />
    </div>
  )
}

function Hero({ v, onUpgrade, onChanged }: { v: View; onUpgrade: () => void; onChanged: () => void }) {
  const left = v.expiresAt ? Math.max(0, Math.ceil((new Date(v.expiresAt).getTime() - Date.now()) / 86400000)) : null
  const soon = left != null && left <= 7
  const creditStudio = v.ownedStudios.find((s) => s.id === v.creditStudioId)
  return (
    <motion.div initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.35, ease: [0.22, 1, 0.36, 1] }}
      className="relative overflow-hidden rounded-panel border border-line bg-surface p-6 shadow-[var(--shadow-card)]">
      {!v.isDefault && <span aria-hidden className="absolute inset-y-0 left-0 w-[3px] bg-accent" />}
      <div className="relative flex flex-wrap items-start gap-6">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 text-[12.5px] text-muted">
            <Crown className={cn('size-4', v.isDefault ? 'text-faint' : 'text-warn')} /> 我的会员
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-2.5">
            <span className="font-serif text-[28px] font-semibold tracking-[0.01em]">{v.plan.name}</span>
            {v.isDefault ? <Badge>免费</Badge> : <Badge tone="accent" dot>生效中</Badge>}
            {rateText(v.plan.creditRate) && <Badge tone="warn">积分消耗 {rateText(v.plan.creditRate)}</Badge>}
          </div>
          <div className={cn('mt-2 flex items-center gap-1.5 text-[13px]', soon ? 'text-warn' : 'text-muted')}>
            <CalendarClock className="size-4" />
            {v.isDefault ? '长期有效，可随时升级' : v.expiresAt ? <>有效期至 {date(v.expiresAt)}<span className="tnum">（还剩 {left} 天）</span>{soon && '，到期后恢复为' + v.defaultPlan.name}</> : '永久有效'}
          </div>
        </div>
        <div className="flex shrink-0 gap-2">
          {v.isDefault ? <Button variant="primary" icon={<Crown className="size-4" />} onClick={onUpgrade}>升级会员</Button> : <Button onClick={onUpgrade}>续费 / 升级</Button>}
        </div>
      </div>
      <div className="relative mt-5 grid gap-3 border-t border-line/70 pt-4 sm:grid-cols-2">
        <div className="flex items-start gap-2.5">
          <Sparkles className="mt-0.5 size-4 shrink-0 text-accent" />
          <div className="text-[13px]">
            <div>每月赠送 <b className="font-semibold tnum">{wan(v.plan.monthlyCredits)}</b> 积分{v.plan.monthlyCredits > 0 && v.nextCreditAt && <span className="text-muted">，下次 {date(v.nextCreditAt)} 到账</span>}</div>
            <div className="mt-0.5 text-[12px] text-faint">月度积分 {v.plan.creditMonths || 2} 个月内有效，优先扣除快到期的积分</div>
          </div>
        </div>
        <div className="flex items-start gap-2.5">
          <Gift className="mt-0.5 size-4 shrink-0 text-muted" />
          <div className="min-w-0 flex-1 text-[13px]">
            <div className="flex flex-wrap items-center gap-1.5">
              积分发放到
              {v.ownedStudios.length > 1 ? (
                <Select size="sm" className="w-48" value={v.creditStudioId} onChange={async (id) => { await api.patch('/membership', { creditStudioId: id }); toast.success('以后的会员积分会发放到这个工作室'); onChanged() }} options={v.ownedStudios.map((s) => [s.id, s.name])} />
              ) : <b className="font-medium">「{creditStudio?.name ?? '个人工作室'}」</b>}
            </div>
            <div className="mt-0.5 text-[12px] text-faint">会员属于你的账号，你拥有的所有工作室都享有会员权益</div>
          </div>
        </div>
      </div>
    </motion.div>
  )
}

function Usage({ v }: { v: View }) {
  const u = v.usage
  const st = v.studio
  return (
    <section>
      <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="font-serif text-[17px] font-semibold">用量</h2>
        {st && <span className="text-[12px] text-faint">图书、成员、存储和克隆音色按工作室计算，当前为「{st.name}」</span>}
      </div>
      {st && (!st.own || st.contract) && (
        <div className="mb-3 flex items-start gap-2 rounded-[10px] bg-sunken/70 px-3.5 py-2.5 text-[12.5px] text-muted">
          <Info className="mt-0.5 size-3.5 shrink-0" />
          {st.contract ? <>这个工作室由平台签约为「{st.plan.name}」，额度按合同方案计算。</> : <>这个工作室不属于你，额度按所有者的会员「{st.plan.name}」计算。</>}
        </div>
      )}
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
        <Meter label="我的工作室" used={u.studios[0]} max={u.studios[1]} hint="含个人工作室" />
        {st && <Meter label="图书" used={u.books[0]} max={u.books[1]} hint="回收站里的也计入" />}
        {st && <Meter label="成员席位" used={u.seats[0]} max={u.seats[1]} />}
        {st && <Meter label="存储空间" used={u.storage[0]} max={u.storage[1]} format={(n) => bytes(n)} />}
        {st && <Meter label="克隆音色" used={u.cloneVoices[0]} max={u.cloneVoices[1]} zeroMeans="未包含" />}
      </div>
    </section>
  )
}

function Redeem({ onDone }: { onDone: () => void }) {
  const studios = useSession((s) => s.studios)
  const user = useSession((s) => s.user)
  const managed = studios.filter((s) => s.role === 'owner' || s.role === 'admin')
  const [code, setCode] = useState('')
  const [offer, setOffer] = useState<any>(null)
  const [target, setTarget] = useState('')
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState<any>(null)
  const check = async () => {
    setBusy(true)
    try {
      const o = await api.post('/redeem/preview', { code })
      setOffer(o)
      setTarget(user?.personalStudioId ?? managed[0]?.id ?? '')
    } finally {
      setBusy(false)
    }
  }
  const confirmRedeem = async () => {
    setBusy(true)
    try {
      const r = await api.post('/redeem', { code, studioId: offer.kind === 'credits' ? target : undefined })
      setOffer(null)
      setCode('')
      setDone(r)
      onDone()
    } finally {
      setBusy(false)
    }
  }
  return (
    <Panel>
      <div className="flex flex-wrap items-center gap-4">
        <span className="grid size-11 shrink-0 place-items-center rounded-full border border-line text-warn"><Ticket className="size-5" /></span>
        <div className="min-w-48 flex-1">
          <div className="text-[14.5px] font-semibold">兑换码</div>
          <div className="mt-0.5 text-[12.5px] text-muted">可兑换会员或积分。大小写、空格和横线都不影响。</div>
        </div>
        <form className="flex w-full gap-2 sm:w-auto" onSubmit={(e) => { e.preventDefault(); code.trim() && check() }}>
          <Input value={code} onChange={(e) => setCode(e.target.value.toUpperCase())} placeholder="例如 VIP-7KQ2-MX9P-4TJD" className="font-mono tracking-wide sm:w-72" maxLength={48} />
          <Button type="submit" variant="primary" loading={busy && !offer} disabled={!code.trim()}>兑换</Button>
        </form>
      </div>
      <AnimatePresence>
        {done && (
          <motion.div initial={{ opacity: 0, height: 0 }} animate={{ opacity: 1, height: 'auto' }} exit={{ opacity: 0, height: 0 }} transition={{ duration: 0.25 }} className="overflow-hidden">
            <div className="mt-4 flex items-center gap-2.5 rounded-[10px] bg-accent-soft px-4 py-3 text-[13.5px]">
              <motion.span initial={{ scale: 0.4, rotate: -30 }} animate={{ scale: 1, rotate: 0 }} transition={{ type: 'spring', stiffness: 420, damping: 16 }}><CheckCircle2 className="size-5 text-accent" /></motion.span>
              <span className="flex-1">兑换成功：{done.summary}</span>
              <button className="text-[12px] text-muted hover:text-ink" onClick={() => setDone(null)}>知道了</button>
            </div>
          </motion.div>
        )}
      </AnimatePresence>
      <Dialog
        open={!!offer}
        onOpenChange={(o) => !o && setOffer(null)}
        title="确认兑换"
        width={440}
        footer={<><Button onClick={() => setOffer(null)}>取消</Button><Button variant="primary" loading={busy} onClick={confirmRedeem}>确认兑换</Button></>}
      >
        {offer && (
          <div className="space-y-4">
            <div className="flex items-center gap-3 rounded-[12px] border border-line bg-sunken/40 px-4 py-4">
              <span className={cn('grid size-10 place-items-center rounded-full border border-line', offer.kind === 'membership' ? 'text-warn' : 'text-accent')}>
                {offer.kind === 'membership' ? <Crown className="size-5" /> : <Sparkles className="size-5" />}
              </span>
              <div>
                <div className="text-[16px] font-semibold">{offer.summary}</div>
                <div className="text-[12px] text-faint">{offer.batch}</div>
              </div>
            </div>
            {offer.kind === 'membership' && <p className="text-[12.5px] leading-relaxed text-muted">已有同等会员会自动延长；已有更高等级会员时，这段会员会在其到期后接着生效，不会浪费。</p>}
            {offer.kind === 'credits' && managed.length > 1 && (
              <div>
                <div className="mb-1.5 text-[12.5px] text-muted">积分发放到</div>
                <Select value={target} onChange={setTarget} options={managed.map((s) => [s.id, s.name])} />
              </div>
            )}
          </div>
        )}
      </Dialog>
    </Panel>
  )
}

function History({ v }: { v: View }) {
  const [tab, setTab] = useState<'periods' | 'redeem'>('periods')
  const uses = useQuery<any[]>({ queryKey: ['redeem-history'], queryFn: () => api.get('/redeem/history'), enabled: tab === 'redeem' })
  return (
    <Panel padded={false}>
      <div className="flex items-center justify-between border-b border-line px-5">
        <Tabs value={tab} onChange={setTab} items={[{ value: 'periods', label: '会员记录' }, { value: 'redeem', label: '兑换记录' }]} />
      </div>
      {tab === 'periods' ? (
        !v.periods.length ? <Empty icon={<Crown />} title="还没有会员记录" description="开通或兑换会员后，每一段有效期都会记在这里。" /> : (
          <ul className="divide-y divide-line">
            {v.periods.map((p) => (
              <li key={p.id} className="flex flex-wrap items-center gap-3 px-5 py-3.5 text-[13px]">
                <Crown className="size-4 text-warn" />
                <span className="font-medium">{p.planName}</span>
                <Badge tone={stateBadge[p.state]?.[1]}>{stateBadge[p.state]?.[0] ?? p.state}</Badge>
                <span className="text-muted">{p.days ? `${p.days} 天` : '永久'} · {sourceName[p.source] ?? p.source}</span>
                <span className="ml-auto text-[12px] text-faint tnum">{date(p.startsAt)} — {p.expiresAt ? date(p.expiresAt) : '永久'}</span>
              </li>
            ))}
          </ul>
        )
      ) : !uses.data?.length ? <Empty icon={<Ticket />} title="还没有兑换记录" /> : (
        <ul className="divide-y divide-line">
          {uses.data.map((u) => (
            <li key={u.id} className="flex items-center gap-3 px-5 py-3.5 text-[13px]">
              {u.kind === 'membership' ? <Crown className="size-4 text-warn" /> : <Sparkles className="size-4 text-accent" />}
              <span className="flex-1">{u.summary}</span>
              <span className="text-[12px] text-faint">{dateTime(u.createdAt)}</span>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  )
}

function BuyDialog({ plan, cycle, onClose, onRedeem }: { plan: Plan | null; cycle: Cycle; onClose: () => void; onRedeem: () => void }) {
  const p = plan
  const pr = p ? priceFor(p, cycle) : null
  return (
    <Dialog open={!!p} onOpenChange={(o) => !o && onClose()} title={p ? (p.contactSales ? `咨询「${p.name}」` : `开通「${p.name}」`) : ''} width={460}
      footer={<><Button onClick={onClose}>稍后再说</Button><Button variant="primary" icon={<Ticket className="size-4" />} onClick={onRedeem}>使用兑换码</Button></>}>
      {p && pr && (
        <div className="space-y-4 text-[13.5px]">
          {!p.contactSales && (
            <div className="rounded-[12px] border border-line bg-sunken/40 px-4 py-3.5">
              <div className="flex items-baseline justify-between">
                <span className="text-muted">{cycle === 'year' && p.priceYearly ? '按年付' : '按月付'}</span>
                <span className="text-[20px] font-semibold tnum">¥{(pr.total / 100).toFixed(2)}</span>
              </div>
              <div className="mt-1 text-[12px] text-faint">含每月 {wan(p.monthlyCredits)} 积分{p.bonusCredits ? `，开通再送 ${wan(p.bonusCredits)}` : ''}</div>
            </div>
          )}
          <p className="leading-relaxed text-muted">
            {p.contactSales ? '企业方案按团队规模和用量定制，可签订合同并开具发票。请通过帮助中心的「联系我们」留下需求，我们会在一个工作日内联系你。' : '在线支付即将开放。现在可以使用兑换码开通会员，兑换码可在官方活动、合作渠道或联系客服获得。'}
          </p>
        </div>
      )}
    </Dialog>
  )
}
