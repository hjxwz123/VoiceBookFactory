import { motion } from 'framer-motion'
import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { useSession } from '@/lib/session'
import { Button, Field, Input, Segmented, cn } from '@/components/ui'
import { AgreeCheckbox, ConsentDialog, LegalLinks } from '@/components/legal'
import { ThemeToggle } from '@/components/theme-toggle'

function useCountdown() {
  const [left, setLeft] = useState(0)
  useEffect(() => {
    if (left <= 0) return
    const t = setTimeout(() => setLeft(left - 1), 1000)
    return () => clearTimeout(t)
  }, [left])
  return [left, setLeft] as const
}

/** The one orchestrated moment of the product: a voice line turning into a waveform. */
function BrandPanel() {
  const bars = useMemo(() => Array.from({ length: 64 }, (_, i) => 0.25 + 0.75 * Math.abs(Math.sin(i * 0.42) * Math.cos(i * 0.13))), [])
  const lines = [
    ['旁白', '夜色渐深，她推开窗。', '#8a93a6'],
    ['林黛玉', '“你来做什么？”', '#ec4899'],
    ['贾宝玉', '“我来看看妹妹。”', '#0ea5e9'],
  ]
  return (
    <div className="relative hidden flex-1 flex-col justify-between overflow-hidden bg-[#171614] p-12 text-white lg:flex">
      <Link to="/welcome" className="flex items-center gap-2.5 self-start" title="了解声书工坊">
        <img src="/favicon.svg" className="size-8" alt="" />
        <span className="text-[16px] font-semibold">声书工坊</span>
      </Link>
      <div className="max-w-md">
        <div className="space-y-3">
          {lines.map(([who, text, color], i) => (
            <motion.div key={i} initial={{ opacity: 0, x: -12 }} animate={{ opacity: 1, x: 0 }} transition={{ delay: 0.25 + i * 0.35, duration: 0.5, ease: [0.22, 1, 0.36, 1] }} className="flex items-baseline gap-3">
              <span className="w-14 shrink-0 text-right text-xs" style={{ color }}>
                {who}
              </span>
              <span className="text-[17px] text-white/90">{text}</span>
            </motion.div>
          ))}
        </div>
        <div className="mt-8 flex h-16 items-center gap-[3px]">
          {bars.map((h, i) => (
            <motion.span
              key={i}
              className="w-[3px] rounded-full"
              style={{ background: i < 26 ? '#E39B2D' : 'rgba(255,255,255,0.22)' }}
              initial={{ height: 2 }}
              animate={{ height: h * 56 }}
              transition={{ delay: 1.4 + i * 0.012, type: 'spring', stiffness: 160, damping: 14 }}
            />
          ))}
        </div>
        <p className="mt-10 font-serif text-[28px] font-semibold leading-snug tracking-[0.01em]">把一本小说，做成一部有角色、有情绪的有声书。</p>
        <p className="mt-3 text-[14px] leading-relaxed text-white/55">导入书稿，AI 识别角色、写好画本、挑选声音；真人配音演员也能在同一个地方录音、对轨与返修。</p>
      </div>
      <p className="text-xs text-white/35">AI 生成的音频请遵守相关法规进行标识</p>
    </div>
  )
}

export function AuthPage({ mode }: { mode: 'login' | 'register' | 'forgot' }) {
  const site = useSession((s) => s.site) ?? {}
  const [params] = useSearchParams()
  const next = params.get('next') || '/'
  const nav = useNavigate()
  const [method, setMethod] = useState<'password' | 'code'>('password')
  const [form, setForm] = useState({ email: '', password: '', nickname: '', code: '', inviteCode: '' })
  const [busy, setBusy] = useState(false)
  const [devCode, setDevCode] = useState('')
  const [left, setLeft] = useCountdown()
  const [agree, setAgree] = useState(false)
  const [consent, setConsent] = useState<null | (() => void)>(null)
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) => setForm({ ...form, [k]: e.target.value })
  const err = params.get('error')
  useEffect(() => {
    if (err) toast.error(err)
  }, [err])

  const sendCode = async () => {
    const target = form.email.trim()
    if (!target) return toast.error(mode === 'login' ? '请先填写邮箱或手机号' : '请先填写邮箱')
    try {
      const isPhone = /^\+?\d{8,15}$/.test(target)
      const r = isPhone
        ? await api.post('/auth/phone/code', { phone: target, purpose: 'login' })
        : await api.post('/auth/code', { email: target, purpose: mode === 'forgot' ? 'reset' : mode === 'login' ? 'login' : 'register' })
      setLeft(60)
      if (r.devCode) {
        setDevCode(r.devCode)
        setForm((f) => ({ ...f, code: r.devCode }))
      }
      toast.success('验证码已发送')
    } catch (e: any) {
      toast.error(e.message)
    }
  }

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    // signing in or up means accepting the agreements; ask in place instead of just refusing
    if (mode !== 'forgot' && !agree) return setConsent(() => () => { setAgree(true); run() })
    run()
  }
  const oauth = (p: string) => {
    // consent travels with the sign-in, so an account created on the way records it
    const go = () => { window.location.href = `/api/v1/auth/oauth/${p}/start?agree=1&redirect=${encodeURIComponent(next)}` }
    if (!agree) return setConsent(() => () => { setAgree(true); go() })
    go()
  }
  const run = async () => {
    setBusy(true)
    try {
      if (mode === 'forgot') {
        await api.post('/auth/reset', { email: form.email, code: form.code, password: form.password })
        toast.success('密码已重置，请用新密码登录')
        nav('/login')
        return
      }
      let r
      if (mode === 'register') r = await api.post('/auth/register', { email: form.email, password: form.password, nickname: form.nickname, code: form.code, inviteCode: form.inviteCode, invitation: params.get('invitation') ?? '', agree: true })
      else if (method === 'code') r = await api.post('/auth/login/code', { target: form.email, code: form.code, agree: true })
      else r = await api.post('/auth/login', { email: form.email, password: form.password, agree: true })
      await useSession.getState().signIn(r.accessToken)
      nav(mode === 'register' ? '/?welcome=1' : next, { replace: true })
    } catch (e: any) {
      toast.error(e.message)
    } finally {
      setBusy(false)
    }
  }

  const title = { login: '欢迎回来', register: '创建账号', forgot: '重置密码' }[mode]
  const needCode = mode === 'forgot' || (mode === 'register' && site.emailVerification) || (mode === 'login' && method === 'code')

  return (
    <div className="flex min-h-full">
      <div className="relative flex w-full flex-col justify-center px-6 py-12 lg:w-[520px] lg:px-16">
        <ThemeToggle className="absolute right-4 top-4" />
        <motion.div initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.35, ease: [0.22, 1, 0.36, 1] }} className="mx-auto w-full max-w-[360px]">
          <Link to="/welcome" className="mb-8 flex w-max items-center gap-2 lg:hidden" title="了解声书工坊">
            <img src="/favicon.svg" className="size-7" alt="" />
            <span className="font-semibold">{site.name ?? '声书工坊'}</span>
          </Link>
          <h1 className="font-serif text-[28px] font-semibold tracking-[0.01em]">{title}</h1>
          <p className="mt-1.5 text-[13.5px] text-muted">
            {mode === 'login' ? '登录后继续你的有声书制作。' : mode === 'register' ? '注册即赠送体验积分，几分钟就能听到第一章。' : '我们会向你的邮箱发送验证码。'}
          </p>
          {mode === 'login' && site.phoneLogin && (
            <Segmented className="mt-6" value={method} onChange={setMethod} items={[{ value: 'password', label: '密码登录' }, { value: 'code', label: '验证码登录' }]} />
          )}
          <form onSubmit={submit} className="mt-6 space-y-4">
            {mode === 'register' && (
              <Field label="昵称">
                <Input value={form.nickname} onChange={set('nickname')} placeholder="大家如何称呼你" autoComplete="nickname" />
              </Field>
            )}
            <Field label={mode === 'login' ? (site.phoneLogin ? '邮箱或手机号' : '邮箱') : '邮箱'}>
              <Input value={form.email} onChange={set('email')} placeholder="name@example.com" autoComplete="username" autoFocus />
            </Field>
            {needCode && (
              <Field label="验证码" hint={devCode ? `开发模式：验证码为 ${devCode}（未配置邮件服务时直接显示）` : undefined}>
                <div className="flex gap-2">
                  <Input value={form.code} onChange={set('code')} placeholder="6 位数字" inputMode="numeric" />
                  <Button type="button" onClick={sendCode} disabled={left > 0} className="w-28 shrink-0">
                    {left > 0 ? `${left} 秒` : '获取验证码'}
                  </Button>
                </div>
              </Field>
            )}
            {(mode !== 'login' || method === 'password') && (
              <Field label={mode === 'forgot' ? '新密码' : '密码'} hint={mode !== 'login' ? '至少 8 位，包含字母和数字' : undefined}>
                <Input type="password" value={form.password} onChange={set('password')} autoComplete={mode === 'login' ? 'current-password' : 'new-password'} />
              </Field>
            )}
            {mode === 'register' && site.inviteOnly && (
              <Field label="邀请码">
                <Input value={form.inviteCode} onChange={set('inviteCode')} />
              </Field>
            )}
            <Button type="submit" variant="primary" size="lg" className="w-full" loading={busy}>
              {mode === 'login' ? '登录' : mode === 'register' ? '注册并开始' : '重置密码'}
            </Button>
            {mode !== 'forgot' && <AgreeCheckbox checked={agree} onChange={setAgree} />}
          </form>
          <ConsentDialog open={!!consent} onOpenChange={(v) => !v && setConsent(null)} onAgree={() => consent?.()} action={mode === 'register' ? '注册' : '登录'} />
          {mode !== 'forgot' && site.oauthProviders?.length > 0 && (
            <div className="mt-6">
              <div className="flex items-center gap-3 text-xs text-faint">
                <span className="h-px flex-1 bg-line" />{mode === 'register' ? '或用以下账号注册' : '其他方式'}<span className="h-px flex-1 bg-line" />
              </div>
              <div className={cn('mt-4 grid gap-2', site.oauthProviders.length > 1 && 'grid-cols-2')}>
                {site.oauthProviders.filter((p: OAuthOption) => mode === 'login' || p.signup).map((p: OAuthOption) => (
                  <Button key={p.id} onClick={() => oauth(p.id)} icon={<ProviderMark preset={p.preset} />}>
                    {p.name}
                  </Button>
                ))}
              </div>
            </div>
          )}
          <div className="mt-6 flex justify-between text-[13px]">
            {mode === 'login' ? (
              <>
                <Link to="/forgot" className="text-muted hover:text-ink">忘记密码</Link>
                {site.registrationOpen !== false && (
                  <span className="text-muted">
                    还没有账号？<Link to={`/register${params.toString() ? '?' + params : ''}`} className="font-medium text-accent">免费注册</Link>
                  </span>
                )}
              </>
            ) : (
              <span className="text-muted">
                已有账号？<Link to="/login" className="font-medium text-accent">去登录</Link>
              </span>
            )}
          </div>
          <div className="mt-10 border-t border-line pt-4 text-[11.5px] leading-relaxed text-faint">
            <LegalLinks all className="[&>a]:font-normal [&>a]:text-faint [&>a:hover]:text-ink" />
            {site.icp && <div className="mt-1">{site.icp}</div>}
          </div>
        </motion.div>
      </div>
      <BrandPanel />
    </div>
  )
}

export function OAuthDone() {
  const nav = useNavigate()
  const [params] = useSearchParams()
  useEffect(() => {
    ;(async () => {
      if (await api.refresh()) {
        await useSession.getState().load()
        nav(params.get('next') || '/', { replace: true })
      } else nav('/login?error=' + encodeURIComponent('登录失败，请重试'), { replace: true })
    })()
  }, [])
  return <div className="grid h-full place-items-center text-muted">正在登录…</div>
}

export type OAuthOption = { id: string; name: string; preset: string; signup: boolean }

/** A small mark for a sign-in button: the provider's initial in its usual colour (no brand artwork). */
export function ProviderMark({ preset }: { preset: string }) {
  const m: Record<string, [string, string]> = { github: ['G', '#24292f'], google: ['G', '#4285f4'], microsoft: ['M', '#2f6fd0'], gitlab: ['G', '#e24329'], gitee: ['G', '#c71d23'], wechat: ['微', '#07c160'] }
  const [ch, color] = m[preset] ?? ['ID', '#5b6170']
  return <span className="grid size-[18px] shrink-0 place-items-center rounded-full text-[10px] font-semibold leading-none text-white" style={{ background: color }} aria-hidden>{ch}</span>
}
