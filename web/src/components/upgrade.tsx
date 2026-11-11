import { useQuery } from '@tanstack/react-query'
import { ArrowRight, Crown, Gift } from '@/components/icons'
import { toast } from 'sonner'
import { create } from 'zustand'
import { api, ApiError } from '@/lib/api'
import { useSession } from '@/lib/session'
import { Button, Dialog } from './ui'
import { featureLabel, Plan, wan } from './plans'

// ---------------------------------------------------------------------------
// One place that reacts to membership limits: whatever screen triggered a `plan_limit` or `plan_required`
// error, the user sees what the limit is and which plans lift it, with a way to upgrade or redeem a code.
// ---------------------------------------------------------------------------

type PlanErr = { message: string; kind?: string; limit?: number; used?: number }
const useUpgrade = create<{ err: PlanErr | null }>(() => ({ err: null }))

/** Navigate inside the SPA from code that lives outside the router (the Shell listens for this). */
export function navigateTo(path: string) {
  window.dispatchEvent(new CustomEvent('vb:navigate', { detail: path }))
}

const isPlanError = (e: any) => e instanceof ApiError && (e.code === 'plan_limit' || e.code === 'plan_required')

let lastShown = { msg: '', at: 0 }

/** Shows the right feedback for an API error (upgrade dialog, credits hint or a plain toast). */
export function reportApiError(e: any) {
  if (isPlanError(e)) {
    lastShown = { msg: e.message, at: Date.now() }
    useUpgrade.setState({ err: { message: e.message, ...(e.data ?? {}) } })
    return
  }
  if (e instanceof ApiError && e.code === 'insufficient_credits') {
    toast.error(e.message, { action: { label: '获取积分', onClick: () => navigateTo('/membership') } })
    return
  }
  toast.error(e?.message ?? '操作失败')
}

/**
 * Installs the global handlers: plan errors open the upgrade dialog even when the calling code shows its
 * own toast (the duplicate toast is dropped), and errors nobody caught still reach the user.
 */
export function installErrorReporting() {
  api.onError = (e: ApiError) => {
    if (isPlanError(e)) reportApiError(e)
  }
  const original = toast.error
  toast.error = ((msg: any, opts?: any) => {
    if (typeof msg === 'string' && msg === lastShown.msg && Date.now() - lastShown.at < 3000) return ''
    return original(msg, opts)
  }) as typeof toast.error
  window.addEventListener('unhandledrejection', (ev) => {
    if (ev.reason instanceof ApiError) {
      ev.preventDefault()
      if (!isPlanError(ev.reason)) reportApiError(ev.reason)
    }
  })
}

const kindTitle: Record<string, string> = {
  studios: '工作室数量已达上限', books: '图书数量已达上限', seats: '成员席位已满', storage: '存储空间不足', cloneVoices: '克隆音色已达上限',
  bookWords: '书稿字数超出上限', upload: '文件超过上传上限', premiumVoice: '精品音色为会员专享', stems: '分轨导出为会员专享', exportFormat: '该导出格式为会员专享',
}

/** Does plan p lift the limit described by err? */
function lifts(p: Plan, err: PlanErr): boolean {
  const more = (v: number) => v === 0 || (err.limit != null && v > err.limit)
  switch (err.kind) {
    case 'studios': return more(p.maxStudios)
    case 'books': return more(p.maxBooks)
    case 'seats': return more(p.seats)
    case 'storage': return more(p.storageGb)
    case 'cloneVoices': return p.cloneVoices > (err.limit ?? 0)
    case 'bookWords': return p.maxBookWords === 0 ? false : p.maxBookWords > (err.limit ?? 0)
    case 'upload': return p.maxUploadMb > (err.limit ?? 0)
    case 'premiumVoice': return p.highQuality
    case 'stems': return p.features?.includes('stems')
    case 'exportFormat': return !p.exportFormats?.length
    default: return true
  }
}

function liftText(p: Plan, kind?: string) {
  const n = (v: number, unit: string) => (v ? `${v} ${unit}` : '不限')
  switch (kind) {
    case 'studios': return `可拥有 ${n(p.maxStudios, '个')}工作室`
    case 'books': return `每个工作室 ${n(p.maxBooks, '本')}图书`
    case 'seats': return `每个工作室 ${n(p.seats, '人')}`
    case 'storage': return `${n(p.storageGb, 'GB')} 存储`
    case 'cloneVoices': return `${p.cloneVoices} 个克隆音色`
    case 'bookWords': return `单本 ${wan(p.maxBookWords)} 字`
    case 'upload': return `单个文件 ${p.maxUploadMb} MB`
    default: return `每月 ${wan(p.monthlyCredits)} 积分`
  }
}

export function UpgradeHost() {
  const err = useUpgrade((s) => s.err)
  const level = useSession((s) => s.membership?.level ?? 0)
  const { data: plans } = useQuery<Plan[]>({ queryKey: ['plans'], queryFn: () => api.get('/plans'), enabled: !!err, staleTime: 300_000 })
  const close = () => useUpgrade.setState({ err: null })
  const options = (plans ?? []).filter((p) => p.level > level && !p.contactSales && lifts(p, err ?? { message: '' })).slice(0, 2)
  const go = (path: string) => {
    close()
    navigateTo(path)
  }
  return (
    <Dialog
      open={!!err}
      onOpenChange={(o) => !o && close()}
      title={
        <span className="flex items-center gap-2">
          <span className="grid size-8 place-items-center rounded-full border border-line text-warn"><Crown className="size-4" /></span>
          {(err?.kind && kindTitle[err.kind]) || '升级会员后即可使用'}
        </span>
      }
      width={480}
      footer={
        <>
          <Button variant="ghost" icon={<Gift className="size-4" />} onClick={() => go('/membership#redeem')}>我有兑换码</Button>
          <Button variant="primary" onClick={() => go('/membership#plans')}>查看会员方案</Button>
        </>
      }
    >
      <p className="text-[13.5px] leading-relaxed text-muted">{err?.message}</p>
      {err?.limit != null && err?.used != null && err.limit > 0 && (
        <div className="mt-4">
          <div className="flex justify-between text-[12px] text-faint"><span>已使用</span><span className="tnum">{err.used} / {err.limit}</span></div>
          <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-sunken"><div className="h-full rounded-full bg-signal" style={{ width: `${Math.min((err.used / err.limit) * 100, 100)}%` }} /></div>
        </div>
      )}
      {options.length > 0 && (
        <div className="mt-5 space-y-2">
          <div className="text-[12px] text-faint">升级后可获得</div>
          {options.map((p) => (
            <button key={p.id} onClick={() => go('/membership#plans')} className="group flex w-full items-center gap-3 rounded-[11px] border border-line px-3.5 py-3 text-left transition-colors hover:border-accent/60 hover:bg-accent-soft/40">
              <Crown className="size-4 shrink-0 text-warn" />
              <span className="min-w-0 flex-1">
                <span className="block text-[13.5px] font-medium">{p.name}</span>
                <span className="block text-[12px] text-muted">{err?.kind && featureLabel[err.kind] ? featureLabel[err.kind] : liftText(p, err?.kind)} · 每月 {wan(p.monthlyCredits)} 积分</span>
              </span>
              <ArrowRight className="size-4 text-faint transition-transform group-hover:translate-x-0.5" />
            </button>
          ))}
        </div>
      )}
    </Dialog>
  )
}
