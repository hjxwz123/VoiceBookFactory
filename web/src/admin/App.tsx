import { AnimatePresence, motion } from 'framer-motion'
import {
  AudioLines, BadgeCheck, Bot, Boxes, BrainCircuit, ChevronDown, CircleDollarSign, FileText, Gauge, LayoutTemplate, ListChecks, LogOut, Megaphone, MessageSquare, Monitor, Moon, KeyRound,
  Route as RouteIcon, ScrollText, Settings, ShieldAlert, Store, Sun, Tag, Users, Building2, Languages, FlaskConical, Wallet, Ban, Flag, Receipt, Mail, ServerCog, Crown, Ticket,
} from '@/components/icons'
import { lazy, Suspense, useEffect, useState } from 'react'
import { Navigate, NavLink, Route, Routes, useLocation } from 'react-router-dom'
import { toast } from 'sonner'
import { create } from 'zustand'
import { adminApi } from '@/lib/api'
import { useTheme } from '@/lib/theme'
import { ThemeToggle } from '@/components/theme-toggle'
import { Avatar, Button, cn, Dialog, Fade, Field, Input, Menu, Spinner } from '@/components/ui'
import { ainv } from './kit'

type AdminState = { ready: boolean; me: any; load: () => Promise<void> }
export const useAdminSession = create<AdminState>((set) => ({
  ready: false,
  me: null,
  load: async () => {
    try {
      if (adminApi.token || (await adminApi.refresh())) set({ me: await adminApi.get('/me') })
    } catch {
      set({ me: null })
    }
    set({ ready: true })
  },
}))
export const hasRole = (me: any, ...roles: string[]) => me?.adminRoles?.includes('super_admin') || roles.some((r) => me?.adminRoles?.includes(r))

const P = {
  Dashboard: lazy(() => import('./pages/Dashboard')),
  Users: lazy(() => import('./pages/Users')),
  Studios: lazy(() => import('./pages/Studios')),
  Voices: lazy(() => import('./pages/Content').then((m) => ({ default: m.Voices }))),
  Assets: lazy(() => import('./pages/Content').then((m) => ({ default: m.Assets }))),
  Templates: lazy(() => import('./pages/Content').then((m) => ({ default: m.Templates }))),
  Lexicon: lazy(() => import('./pages/Content').then((m) => ({ default: m.Lexicon }))),
  Providers: lazy(() => import('./pages/AI').then((m) => ({ default: m.Providers }))),
  Routing: lazy(() => import('./pages/AI').then((m) => ({ default: m.Routing }))),
  Pricing: lazy(() => import('./pages/AI').then((m) => ({ default: m.Pricing }))),
  Prompts: lazy(() => import('./pages/AI').then((m) => ({ default: m.Prompts }))),
  Evals: lazy(() => import('./pages/AI').then((m) => ({ default: m.Evals }))),
  Plans: lazy(() => import('./pages/Membership').then((m) => ({ default: m.Plans }))),
  Redeem: lazy(() => import('./pages/Membership').then((m) => ({ default: m.Redeem }))),
  Ledger: lazy(() => import('./pages/Finance').then((m) => ({ default: m.Ledger }))),
  FinanceReport: lazy(() => import('./pages/Finance').then((m) => ({ default: m.FinanceReport }))),
  Withdrawals: lazy(() => import('./pages/Finance').then((m) => ({ default: m.Withdrawals }))),
  Listings: lazy(() => import('./pages/Finance').then((m) => ({ default: m.Listings }))),
  Moderation: lazy(() => import('./pages/Trust').then((m) => ({ default: m.Moderation }))),
  Consents: lazy(() => import('./pages/Trust').then((m) => ({ default: m.Consents }))),
  Reports: lazy(() => import('./pages/Trust').then((m) => ({ default: m.Reports }))),
  Words: lazy(() => import('./pages/Trust').then((m) => ({ default: m.Words }))),
  Jobs: lazy(() => import('./pages/Ops').then((m) => ({ default: m.Jobs }))),
  System: lazy(() => import('./pages/Ops').then((m) => ({ default: m.System }))),
  Audit: lazy(() => import('./pages/Ops').then((m) => ({ default: m.Audit }))),
  Announcements: lazy(() => import('./pages/Comms').then((m) => ({ default: m.Announcements }))),
  Articles: lazy(() => import('./pages/Comms').then((m) => ({ default: m.Articles }))),
  Legal: lazy(() => import('./pages/Legal')),
  Feedback: lazy(() => import('./pages/Comms').then((m) => ({ default: m.Feedback }))),
  Messages: lazy(() => import('./pages/Comms').then((m) => ({ default: m.Messages }))),
  Settings: lazy(() => import('./pages/SettingsPage')),
}

const groups: { title: string; items: [string, string, any, any][] }[] = [
  { title: '概览', items: [['/', '数据看板', Gauge, P.Dashboard]] },
  { title: '用户', items: [['/users', '用户', Users, P.Users], ['/studios', '工作室', Building2, P.Studios]] },
  { title: '内容', items: [['/voices', '音色库', AudioLines, P.Voices], ['/assets', '素材库', Boxes, P.Assets], ['/templates', '片头片尾', LayoutTemplate, P.Templates], ['/lexicon', '平台词典', Languages, P.Lexicon]] },
  { title: 'AI 服务', items: [['/providers', '模型与供应商', Bot, P.Providers], ['/routing', '路由规则', RouteIcon, P.Routing], ['/pricing', '积分定价', Tag, P.Pricing], ['/prompts', '提示词', BrainCircuit, P.Prompts], ['/evals', '效果评测', FlaskConical, P.Evals]] },
  { title: '商业', items: [['/plans', '会员套餐', Crown, P.Plans], ['/redeem', '兑换码', Ticket, P.Redeem], ['/ledger', '积分流水', ScrollText, P.Ledger], ['/finance', '成本报表', CircleDollarSign, P.FinanceReport], ['/listings', '音色市场', Store, P.Listings], ['/withdrawals', '提现审核', Wallet, P.Withdrawals]] },
  { title: '安全与合规', items: [['/moderation', '内容审核', ShieldAlert, P.Moderation], ['/consents', '声音授权', FileText, P.Consents], ['/legal', '协议与政策', ScrollText, P.Legal], ['/reports', '举报', Flag, P.Reports], ['/words', '敏感词', Ban, P.Words]] },
  { title: '运营', items: [['/announcements', '公告', Megaphone, P.Announcements], ['/articles', '帮助文章', FileText, P.Articles], ['/feedback', '用户反馈', MessageSquare, P.Feedback], ['/messages', '站内信', Mail, P.Messages]] },
  { title: '系统', items: [['/jobs', '任务队列', ListChecks, P.Jobs], ['/system', '运行状态', ServerCog, P.System], ['/audit', '操作日志', ScrollText, P.Audit], ['/settings', '系统设置', Settings, P.Settings]] },
]

export default function AdminApp() {
  const { ready, me, load } = useAdminSession()
  useEffect(() => { load() }, [])
  if (!ready) return <div className="grid h-full place-items-center"><Spinner className="size-5" /></div>
  if (!me) return <Login />
  return <Shell />
}

function Login() {
  const [f, setF] = useState({ email: '', password: '' })
  const [busy, setBusy] = useState(false)
  return (
    <div className="grid min-h-full place-items-center bg-[#10161a] px-6">
      <motion.form initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} onSubmit={async (e) => {
        e.preventDefault()
        setBusy(true)
        try {
          const r = await adminApi.post('/auth/login', f)
          adminApi.setToken(r.accessToken)
          await useAdminSession.getState().load()
        } catch (err: any) {
          toast.error(err.message)
        } finally {
          setBusy(false)
        }
      }} className="w-full max-w-sm rounded-sheet border border-line bg-surface p-8 shadow-[var(--shadow-pop)]">
        <div className="mb-6 flex items-center gap-2.5"><img src="/favicon.svg" className="size-8" alt="" /><div><div className="text-[15px] font-semibold">声书工坊</div><div className="text-xs text-muted">管理后台</div></div></div>
        <div className="space-y-4">
          <Field label="管理员邮箱"><Input autoFocus value={f.email} onChange={(e) => setF({ ...f, email: e.target.value })} /></Field>
          <Field label="密码"><Input type="password" value={f.password} onChange={(e) => setF({ ...f, password: e.target.value })} /></Field>
          <Button type="submit" variant="primary" size="lg" className="w-full" loading={busy}>登录</Button>
        </div>
        <p className="mt-4 text-center text-xs text-faint">仅限平台管理员，所有操作都会记录日志</p>
      </motion.form>
    </div>
  )
}

function Shell() {
  const me = useAdminSession((s) => s.me)
  const { theme, setTheme } = useTheme()
  const loc = useLocation()
  const [collapsed, setCollapsed] = useState<string[]>([])
  const [pwdOpen, setPwdOpen] = useState(false)
  useEffect(() => {
    const h = () => setPwdOpen(true)
    window.addEventListener('vb:admin-password', h)
    return () => window.removeEventListener('vb:admin-password', h)
  }, [])
  const routes = groups.flatMap((g) => g.items)
  return (
    <div className="flex h-full">
      <aside className="hidden w-[232px] shrink-0 flex-col border-r border-line bg-surface lg:flex">
        <div className="flex h-14 items-center gap-2.5 px-5"><img src="/favicon.svg" className="size-7" alt="" /><span className="font-serif text-[16px] font-semibold tracking-[0.02em]">管理后台</span></div>
        <nav className="flex-1 overflow-y-auto px-3 pb-4">
          {groups.map((g) => {
            const closed = collapsed.includes(g.title)
            return (
              <div key={g.title} className="mt-3">
                <button onClick={() => setCollapsed(closed ? collapsed.filter((x) => x !== g.title) : [...collapsed, g.title])} className="flex w-full items-center justify-between px-3 py-1 text-[11px] font-medium uppercase tracking-wide text-faint hover:text-muted">
                  {g.title}<ChevronDown className={cn('size-3 transition-transform', closed && '-rotate-90')} />
                </button>
                <AnimatePresence initial={false}>
                  {!closed && (
                    <motion.div initial={{ height: 0, opacity: 0 }} animate={{ height: 'auto', opacity: 1 }} exit={{ height: 0, opacity: 0 }} className="overflow-hidden">
                      {g.items.map(([to, label, Icon]) => (
                        <NavLink key={to} to={to} end={to === '/'} className={({ isActive }) => cn('relative mt-0.5 flex h-8 items-center gap-2.5 rounded-[7px] px-3 text-[13px] transition-colors', isActive ? 'font-medium text-ink' : 'text-muted hover:bg-sunken hover:text-ink')}>
                          {({ isActive }) => <>{isActive && <motion.span layoutId="admin-nav" className="absolute inset-0 rounded-[7px] bg-sunken" transition={{ type: 'spring', stiffness: 500, damping: 40 }} />}<Icon className={cn('relative size-[17px] transition-colors', isActive && 'text-accent')} weight={isActive ? 'duotone' : 'regular'} /><span className="relative">{label}</span></>}
                        </NavLink>
                      ))}
                    </motion.div>
                  )}
                </AnimatePresence>
              </div>
            )
          })}
        </nav>
        <div className="border-t border-line px-5 py-3 text-[11.5px] text-faint tnum" title="当前版本">声书工坊 v{__APP_VERSION__}</div>
      </aside>
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 shrink-0 items-center gap-3 border-b border-line bg-bg/80 px-6 backdrop-blur">
          <div className="text-[13px] text-muted">{routes.find(([to]) => to !== '/' && loc.pathname.startsWith(to))?.[1] ?? '数据看板'}</div>
          <div className="ml-auto flex items-center gap-2">
            <ThemeToggle />
            <a href="/" className="text-[13px] text-muted hover:text-ink">返回工作台</a>
            <Menu trigger={<button className="ml-2 rounded-full"><Avatar name={me.nickname} src={me.avatarUrl} size={30} /></button>} items={[
              { label: <span className="block w-44"><span className="block font-medium">{me.nickname}</span><span className="block text-xs text-faint">{me.adminRoles.join('、')}</span></span>, onSelect: () => {} },
              'sep',
              { label: theme === 'dark' ? '浅色模式' : theme === 'light' ? '跟随系统' : '深色模式', icon: theme === 'dark' ? <Sun /> : theme === 'light' ? <Monitor /> : <Moon />, onSelect: () => setTheme(theme === 'dark' ? 'light' : theme === 'light' ? 'system' : 'dark') },
              { label: '修改密码', icon: <KeyRound />, onSelect: () => setPwdOpen(true) },
              { label: '退出登录', icon: <LogOut />, onSelect: async () => { await adminApi.post('/auth/logout').catch(() => {}); adminApi.setToken(null); useAdminSession.setState({ me: null }) } },
            ]} />
          </div>
        </header>
        <main className="min-h-0 flex-1 overflow-y-auto">
          <Fade k={loc.pathname.split('/')[1]} className="page-enter mx-auto w-full max-w-[1320px] px-6 py-7">
              <Suspense fallback={<div className="grid h-40 place-items-center"><Spinner /></div>}>
                <Routes>
                  {routes.map(([to, , , C]) => <Route key={to} path={to === '/' ? '/' : to + '/*'} element={<C />} />)}
                  <Route path="*" element={<Navigate to="/" replace />} />
                </Routes>
              </Suspense>
          </Fade>
        </main>
      </div>
      <ChangePassword open={pwdOpen} onOpenChange={setPwdOpen} />
    </div>
  )
}

function ChangePassword({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const [f, setF] = useState({ old: '', next: '', again: '' })
  const mismatch = !!f.again && f.next !== f.again
  const save = async () => {
    await adminApi.post('/me/password', { old: f.old, new: f.next })
    toast.success('密码已修改，其他设备上的登录已退出')
    setF({ old: '', next: '', again: '' })
    onOpenChange(false)
    ainv(['/launch-check'])
  }
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="修改管理员密码" width={400}
      footer={<><Button onClick={() => onOpenChange(false)}>取消</Button><Button variant="primary" disabled={!f.old || f.next.length < 8 || f.next !== f.again} onClick={save}>保存</Button></>}>
      <div className="space-y-4">
        <Field label="当前密码"><Input type="password" value={f.old} onChange={(e) => setF({ ...f, old: e.target.value })} autoComplete="current-password" /></Field>
        <Field label="新密码" hint="至少 8 位，包含字母和数字"><Input type="password" value={f.next} onChange={(e) => setF({ ...f, next: e.target.value })} autoComplete="new-password" /></Field>
        <Field label="再输入一次" error={mismatch ? '两次输入的密码不一致' : undefined}><Input type="password" value={f.again} onChange={(e) => setF({ ...f, again: e.target.value })} autoComplete="new-password" /></Field>
      </div>
    </Dialog>
  )
}
