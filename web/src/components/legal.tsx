import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, ShieldCheck } from '@/components/icons'
import { useState } from 'react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { useSession } from '@/lib/session'
import { Button, Checkbox, cn, Dialog } from './ui'

export type LegalDoc = { slug: string; title: string; summary: string; version: string; updatedAt: string; required: boolean }

export function useLegalDocs() {
  return useQuery<LegalDoc[]>({ queryKey: ['legal'], queryFn: () => api.get('/legal'), staleTime: 10 * 60_000 })
}

const fallback: LegalDoc[] = [
  { slug: 'terms', title: '用户服务协议', summary: '', version: '', updatedAt: '', required: true },
  { slug: 'privacy', title: '隐私政策', summary: '', version: '', updatedAt: '', required: true },
  { slug: 'copyright', title: '版权与素材使用声明', summary: '', version: '', updatedAt: '', required: true },
]

/** 《用户服务协议》《隐私政策》… as links that open in a new tab, so reading never loses what was typed. */
export function LegalLinks({ all, className }: { all?: boolean; className?: string }) {
  const { data } = useLegalDocs()
  const docs = (data ?? fallback).filter((d) => all || d.required)
  return (
    <span className={className}>
      {docs.map((d) => (
        <a key={d.slug} href={`/legal/${d.slug}`} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()} className="font-medium text-accent hover:underline">《{d.title}》</a>
      ))}
    </span>
  )
}

/** The consent checkbox under login / sign-up forms. */
export function AgreeCheckbox({ checked, onChange, attention }: { checked: boolean; onChange: (v: boolean) => void; attention?: number }) {
  return (
    <div key={attention} className={cn('flex items-start gap-2 text-[12.5px] leading-relaxed text-muted', attention && 'animate-[shake_0.4s_ease-in-out]')}>
      <Checkbox checked={checked} onChange={(v) => onChange(!!v)} className="mt-[3px]" />
      <span onClick={() => onChange(!checked)} className="cursor-pointer select-none">我已阅读并同意<LegalLinks /></span>
    </div>
  )
}

/** Asked when someone presses "登录 / 注册" without ticking the box: agree and carry on in one click. */
export function ConsentDialog({ open, onOpenChange, onAgree, action = '继续' }: { open: boolean; onOpenChange: (v: boolean) => void; onAgree: () => void; action?: string }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="请阅读并同意以下协议" width={420}
      footer={<><Button onClick={() => onOpenChange(false)}>不同意</Button><Button variant="primary" onClick={() => { onOpenChange(false); onAgree() }}>同意并{action}</Button></>}>
      <p className="text-[13.5px] leading-relaxed text-muted">为了保障你的权益，请先阅读<LegalLinks />。其中说明了账号规则、个人信息的处理方式，以及<b className="text-ink">书稿、配乐、音效、声音的版权须由你自行确认，本平台不承担版权责任</b>。</p>
    </Dialog>
  )
}

/** Shown once after sign-in when the user has not accepted the current agreements (new or updated ones). */
export function LegalGate() {
  const user = useSession((s) => s.user)
  const site = useSession((s) => s.site)
  const [busy, setBusy] = useState(false)
  const need = !!user && !!site?.legalVersion && user.legalVersion !== site.legalVersion
  if (!need) return null
  const first = !user.legalVersion
  const agree = async () => {
    setBusy(true)
    try {
      const r = await api.post('/me/legal/agree')
      useSession.setState({ user: { ...user, legalVersion: r.legalVersion } as any })
      toast.success('感谢你的确认')
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog open onOpenChange={() => {}} dismissible={false} title={first ? '请阅读并同意平台协议' : '平台协议已更新'} width={460}
      footer={<><Button onClick={() => useSession.getState().signOut()}>不同意并退出</Button><Button variant="primary" loading={busy} onClick={agree}>同意并继续</Button></>}>
      <div className="space-y-3 text-[13.5px] leading-relaxed text-muted">
        <p>{first ? '继续使用前，请阅读' : '我们更新了'}<LegalLinks />{first ? '。' : '，请阅读更新后的内容。'}</p>
        <p className="rounded-[10px] bg-warn-soft px-3 py-2.5 text-[12.5px] text-warn">特别提示：本平台只提供制作工具，不对书稿、配乐、音效、图片和声音的版权作任何保证。使用任何内容前请自行确认拥有合法权利，因版权产生的纠纷由使用者承担。</p>
      </div>
    </Dialog>
  )
}

/**
 * Copyright reminder for pages where people pick or upload material.
 * - banner: prominent block at the top of a page
 * - inline: one short line inside dialogs and pickers
 */
export function CopyrightNotice({ variant = 'inline', what = '素材', className, children }: { variant?: 'banner' | 'inline'; what?: string; className?: string; children?: React.ReactNode }) {
  if (variant === 'banner') {
    return (
      <div role="note" className={cn('flex items-start gap-3 rounded-panel border border-warn/40 bg-warn-soft px-4 py-3', className)}>
        <AlertTriangle className="mt-0.5 size-[18px] shrink-0 text-warn" />
        <div className="min-w-0 flex-1 text-[13px] leading-relaxed">
          <div className="font-semibold text-warn">版权提示：使用前请自行确认版权</div>
          <p className="mt-0.5 text-ink/80">
            平台{what}标注的来源和许可信息（如 CC0、公有领域）来自原网站，<b>仅供参考，本平台不保证其准确，也不对任何{what}的版权负责</b>。
            你上传或在作品中使用的任何{what}，都须由你自行确认拥有合法使用权；因版权产生的纠纷和损失由使用者自行承担。
            商业发行或签约作品建议使用自行购买正版授权的{what}。<a href="/legal/copyright" target="_blank" rel="noreferrer" className="ml-1 font-medium text-accent hover:underline">查看《版权与素材使用声明》</a>
          </p>
        </div>
      </div>
    )
  }
  return (
    <p role="note" className={cn('flex items-start gap-1.5 text-[12px] leading-relaxed text-warn', className)}>
      <ShieldCheck className="mt-0.5 size-3.5 shrink-0" />
      <span>{children ?? <>版权须自行确认：本平台不对{what}的版权作保证，因使用产生的纠纷由使用者承担。</>}<a href="/legal/copyright" target="_blank" rel="noreferrer" className="font-medium underline-offset-2 hover:underline">了解详情</a></span>
    </p>
  )
}
