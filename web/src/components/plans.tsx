import { motion } from 'framer-motion'
import { Check, Crown, Minus, Sparkles } from '@/components/icons'
import React from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { cn } from './ui'

export type Plan = {
  id: string; code: string; name: string; tagline: string; level: number
  priceMonthly: number; priceYearly: number; originalMonthly: number
  monthlyCredits: number; bonusCredits: number; creditMonths: number; creditRate: number
  maxStudios: number; seats: number; maxBooks: number; storageGb: number; cloneVoices: number; maxBookWords: number; maxUploadMb: number
  queuePriority: number; exportFormats: string[]; highQuality: boolean; commercial: boolean; features: string[]
  benefits: string[]; badge: string; highlight: boolean; contactSales: boolean; listed: boolean; sortOrder: number
}

export type Cycle = 'month' | 'year'

export const planFeatures: [string, string, string][] = [
  ['alignment', '智能对轨', '真人录音自动对齐画本'],
  ['review', '智能审听', '自动发现漏读、错读和音质问题'],
  ['production', '生产管理', '任务看板、工序流转与审核'],
  ['payroll', '统计与计薪', '按工作量统计并结算'],
  ['pro_clone', '专业克隆', '更高还原度的声音克隆'],
  ['stems', '分轨导出', '按角色和音乐音效分轨'],
  ['distribute', '多平台分发', '一键发布到已绑定的平台'],
  ['byok', '自带密钥', '接入自己的 AI 平台账号'],
  ['openapi', '开放 API', '通过接口批量生产'],
  ['batch', '批量生产', '整批图书自动成书'],
  ['private', '私有化部署', '部署在自有服务器'],
]
export const featureLabel = Object.fromEntries(planFeatures.map(([k, l]) => [k, l]))

/** 150000 -> "15 万" */
export function wan(n?: number | null) {
  const v = n ?? 0
  if (v >= 10000) return `${+(v / 10000).toFixed(v % 10000 ? 1 : 0)} 万`
  return v.toLocaleString('zh-CN')
}

export type CreditGuide = { per10kChars: Record<string, number>; speechRate: number }

/** How far credits go: typical multi-voice audiobook cost per 10,000 characters, set by the platform's prices. */
export function useCreditGuide() {
  return useQuery<CreditGuide>({ queryKey: ['credit-guide'], queryFn: () => api.get('/credits/guide'), staleTime: 30 * 60_000 })
}

/** "约 77 万字 · 58 小时" — what a number of credits buys at the standard tier (rate: plan discount, 95 = 9.5 折). */
export function creditReach(credits: number | undefined, g: CreditGuide | undefined, rate = 100, tier = 'standard') {
  const per = g?.per10kChars?.[tier]
  if (!credits || credits <= 0 || !per) return ''
  const chars = (credits / ((per * (rate || 100)) / 100)) * 10000
  const minutes = chars / (g!.speechRate || 220)
  const len = chars >= 10000 ? `${chars >= 1_000_000 ? Math.round(chars / 10000) : +(chars / 10000).toFixed(1)} 万字` : `${Math.round(chars / 100) * 100} 字`
  const time = minutes >= 60 ? `${minutes >= 6000 ? Math.round(minutes / 60) : +(minutes / 60).toFixed(1)} 小时` : `${Math.max(1, Math.round(minutes))} 分钟`
  return `约 ${len} · ${time}`
}

export const yuanShort = (cent: number) => {
  const v = cent / 100
  return Number.isInteger(v) ? String(v) : v.toFixed(2)
}

/** "9.5 折" for a credit rate of 95; "" when full price. */
export function rateText(rate?: number) {
  if (!rate || rate >= 100) return ''
  return `${+(rate / 10).toFixed(1)} 折`
}

export function queueText(p: number) {
  if (p >= 9) return '最高'
  if (p >= 5) return '高'
  if (p >= 1) return '优先'
  return '普通'
}

/** Effective monthly price for a billing cycle (cents) and the saving of paying yearly. */
export function priceFor(p: Plan, cycle: Cycle) {
  if (cycle === 'year' && p.priceYearly > 0) {
    const monthly = Math.round(p.priceYearly / 12)
    const save = p.priceMonthly > 0 ? Math.round((1 - p.priceYearly / (p.priceMonthly * 12)) * 100) : 0
    return { monthly, total: p.priceYearly, save: Math.max(save, 0) }
  }
  return { monthly: p.priceMonthly, total: p.priceMonthly, save: 0 }
}

const limit = (v: number, unit = '', none = '不限') => (v > 0 ? `${v.toLocaleString('zh-CN')}${unit}` : none)

/** Rows of the comparison table: label, value renderer, optional hint. */
export const compareRows: { group: string; rows: [string, (p: Plan, site?: any) => React.ReactNode, string?][] }[] = [
  {
    group: '积分',
    rows: [
      ['每月赠送积分', (p) => wan(p.monthlyCredits)],
      ['开通赠送', (p) => (p.bonusCredits ? wan(p.bonusCredits) : <No />)],
      ['积分消耗', (p) => rateText(p.creditRate) || '原价', '每次使用 AI 功能时按折扣扣积分'],
      ['月度积分有效期', (p) => `${p.creditMonths || 2} 个月`],
    ],
  },
  {
    group: '资源',
    rows: [
      ['可拥有的工作室', (p) => limit(p.maxStudios, ' 个'), '含个人工作室'],
      ['每个工作室成员', (p) => limit(p.seats, ' 人')],
      ['每个工作室图书', (p) => limit(p.maxBooks, ' 本')],
      ['每个工作室存储', (p) => limit(p.storageGb, ' GB')],
      ['克隆音色', (p) => (p.cloneVoices > 0 ? `${p.cloneVoices} 个` : <No />)],
      ['单本字数上限', (p, site) => (p.maxBookWords > 0 ? wan(p.maxBookWords) + ' 字' : site?.maxBookChars ? wan(site.maxBookChars) + ' 字' : '标准')],
      ['单个文件上传', (p, site) => `${p.maxUploadMb || site?.maxUploadMb || 200} MB`],
      ['排队优先级', (p) => queueText(p.queuePriority), '平台繁忙时优先处理'],
    ],
  },
  {
    group: '权益',
    rows: [
      ['精品音色', (p) => <Yes on={p.highQuality} />],
      ['商用授权', (p) => <Yes on={p.commercial} />],
      ['导出格式', (p) => (p.exportFormats?.length ? p.exportFormats.map((f) => f.toUpperCase()).join(' / ') : '全部格式')],
      ...planFeatures.map(([k, l, hint]) => [l, (p: Plan) => <Yes on={p.features?.includes(k)} />, hint] as [string, (p: Plan) => React.ReactNode, string]),
    ],
  },
]

function Yes({ on }: { on?: boolean }) {
  return on ? <Check className="mx-auto size-4 text-accent" strokeWidth={2.5} /> : <No />
}
function No() {
  return <Minus className="mx-auto size-4 text-faint/60" />
}

export function PlanPrice({ plan, cycle, size = 'md' }: { plan: Plan; cycle: Cycle; size?: 'md' | 'sm' }) {
  if (plan.contactSales) return <div className={cn('font-semibold tracking-[-0.02em]', size === 'md' ? 'text-[30px]' : 'text-[20px]')}>定制</div>
  const pr = priceFor(plan, cycle)
  if (!pr.monthly) return <div className={cn('font-semibold tracking-[-0.02em]', size === 'md' ? 'text-[30px]' : 'text-[20px]')}>免费</div>
  return (
    <div>
      <div className="flex items-baseline gap-1.5">
        <span className={cn('font-semibold tracking-[-0.02em] tnum', size === 'md' ? 'text-[30px]' : 'text-[20px]')}>¥{yuanShort(pr.monthly)}</span>
        <span className="text-[13px] text-muted">/月</span>
        {cycle === 'month' && plan.originalMonthly > plan.priceMonthly && <span className="text-[13px] text-faint line-through tnum">¥{yuanShort(plan.originalMonthly)}</span>}
      </div>
      {cycle === 'year' && plan.priceYearly > 0 && (
        <div className="mt-0.5 text-[12px] text-muted">按年付 ¥{yuanShort(pr.total)}{pr.save > 0 && <span className="ml-1.5 rounded bg-signal-soft px-1 py-px text-warn">省 {pr.save}%</span>}</div>
      )}
    </div>
  )
}

export function PlanCard({ plan, cycle, current, action, index = 0, compact }: { plan: Plan; cycle: Cycle; current?: boolean; action?: React.ReactNode; index?: number; compact?: boolean }) {
  return (
    <div
      style={{ '--i': index } as React.CSSProperties}
      className={cn(
        'lift rise-in relative flex flex-col overflow-hidden rounded-panel border bg-surface p-5 shadow-[var(--shadow-card)]',
        plan.highlight ? 'border-accent/50' : 'border-line',
        current && 'bg-accent-soft/40',
      )}
    >
      {plan.highlight && <span aria-hidden className="absolute inset-x-0 top-0 h-[3px] rounded-t-panel bg-accent" />}
      <div className="flex items-center gap-1.5">
        {plan.level > 0 && <Crown className={cn('size-4', plan.highlight ? 'text-accent' : 'text-warn')} />}
        <span className="font-serif text-[16px] font-semibold">{plan.name}</span>
        {(plan.badge || current) && <span className={cn('ml-auto text-[11.5px] font-medium', current ? 'text-muted' : 'text-accent')}>{current ? '当前方案' : plan.badge}</span>}
      </div>
      <p className="mt-1 min-h-[2.4em] text-[12.5px] leading-relaxed text-muted">{plan.tagline}</p>
      <div className="mt-3"><PlanPrice plan={plan} cycle={cycle} /></div>
      <div className="mt-3 flex items-center gap-1.5 rounded-[9px] bg-sunken/70 px-3 py-2 text-[12.5px]">
        <Sparkles className="size-3.5 shrink-0 text-accent" />
        <span>每月 <b className="font-semibold tnum">{wan(plan.monthlyCredits)}</b> 积分</span>
        {plan.bonusCredits > 0 && <span className="ml-auto text-warn">开通送 {wan(plan.bonusCredits)}</span>}
      </div>
      <PlanReach plan={plan} />
      {!compact && (
        <ul className="mt-4 flex-1 space-y-2 text-[13px]">
          {(plan.benefits?.length ? plan.benefits : autoBenefits(plan)).map((b, i) => (
            <li key={i} className="flex gap-2"><Check className="mt-0.5 size-3.5 shrink-0 text-accent" strokeWidth={2.5} /><span>{b}</span></li>
          ))}
        </ul>
      )}
      {action && <div className="mt-5">{action}</div>}
    </div>
  )
}

function PlanReach({ plan }: { plan: Plan }) {
  const { data } = useCreditGuide()
  const r = creditReach(plan.monthlyCredits, data, (plan as any).creditRate)
  if (!r) return null
  return <p className="mt-1.5 px-1 text-[11.5px] text-faint" title="按标准档多播有声书估算（角色分析 + 画本 + 配音），实际以内容和所选音色为准">每月约可制作 {r.replace('约 ', '')} 有声书</p>
}

/** Selling points derived from limits when an administrator did not write any. */
export function autoBenefits(p: Plan): string[] {
  const out: string[] = []
  out.push(p.maxBooks > 0 ? `每个工作室 ${p.maxBooks} 本图书` : '图书数量不限')
  if (p.seats > 1) out.push(`${p.seats} 人协作`)
  if (p.storageGb) out.push(`${p.storageGb} GB 存储`)
  if (p.highQuality) out.push('精品音色')
  if (p.commercial) out.push('商用授权')
  if (rateText(p.creditRate)) out.push(`积分消耗 ${rateText(p.creditRate)}`)
  ;(p.features ?? []).slice(0, 3).forEach((f) => featureLabel[f] && out.push(featureLabel[f]))
  return out.slice(0, 6)
}

export function CompareTable({ plans, currentId, site }: { plans: Plan[]; currentId?: string; site?: any }) {
  return (
    <div className="overflow-x-auto rounded-panel border border-line bg-surface">
      <table className="w-full min-w-[640px] text-[13px]">
        <thead>
          <tr className="border-b border-line">
            <th className="w-48 px-4 py-3 text-left font-medium text-muted">对比项</th>
            {plans.map((p) => (
              <th key={p.id} className={cn('px-3 py-3 text-center font-semibold', p.id === currentId && 'bg-accent-soft/50')}>
                {p.name}
                {p.id === currentId && <span className="ml-1 text-[11px] font-normal text-accent">当前</span>}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {compareRows.map((g) => (
            <React.Fragment key={g.group}>
              <tr><td colSpan={plans.length + 1} className="bg-sunken/50 px-4 py-1.5 text-[11.5px] font-medium tracking-wide text-faint">{g.group}</td></tr>
              {g.rows.map(([label, render, hint]) => (
                <tr key={label} className="border-t border-line/70">
                  <td className="px-4 py-2.5">
                    <span>{label}</span>
                    {hint && <span className="block text-[11px] text-faint">{hint}</span>}
                  </td>
                  {plans.map((p) => <td key={p.id} className={cn('px-3 py-2.5 text-center tnum', p.id === currentId && 'bg-accent-soft/30')}>{render(p, site)}</td>)}
                </tr>
              ))}
            </React.Fragment>
          ))}
        </tbody>
      </table>
    </div>
  )
}

/** A labelled usage meter; limit 0 = unlimited (or "not included" when `zeroMeans` says so). */
export function Meter({ label, used, max, format = (v: number) => v.toLocaleString('zh-CN'), zeroMeans = '不限', hint }: { label: string; used: number; max: number; format?: (v: number) => string; zeroMeans?: string; hint?: string }) {
  const ratio = max > 0 ? Math.min(used / max, 1) : 0
  // amber when the allowance is used up (normal on small plans), red only when over it
  const tone = max > 0 && used > max ? 'bg-danger' : ratio >= 0.8 ? 'bg-signal' : 'bg-accent'
  return (
    <div className="rounded-[11px] border border-line bg-surface px-4 py-3.5">
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-[12.5px] text-muted">{label}</span>
        <span className="text-[12px] text-faint tnum">{max > 0 ? `${format(used)} / ${format(max)}` : zeroMeans === '不限' ? `${format(used)} · 不限` : zeroMeans}</span>
      </div>
      <div className="mt-2.5 h-1.5 overflow-hidden rounded-full bg-sunken">
        <motion.div className={cn('h-full rounded-full', max > 0 ? tone : 'bg-accent/30')} initial={{ width: 0 }} animate={{ width: max > 0 ? `${Math.max(ratio * 100, used > 0 ? 3 : 0)}%` : '100%' }} transition={{ duration: 0.7, ease: [0.22, 1, 0.36, 1] }} />
      </div>
      {hint && <div className="mt-1.5 text-[11px] text-faint">{hint}</div>}
    </div>
  )
}
