import { useQuery } from '@tanstack/react-query'
import { AnimatePresence, motion } from 'framer-motion'
import {
  AudioLines, Bell, BookOpen, Boxes, Check, ChevronsUpDown, CircleHelp, Command, Crown, FileText, Gauge, KanbanSquare, ListChecks, LogOut, Menu as MenuIcon, Mic2, Monitor, Moon, Plus,
  Search, Settings, Sparkles, Sun, User, UserRound, Wand2, Zap,
} from '@/components/icons'
import { Suspense, useEffect, useState } from 'react'
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { api } from '@/lib/api'
import { fromNow, num } from '@/lib/format'
import { useLive, useLiveEvents } from '@/lib/live'
import { useSession, useStudio } from '@/lib/session'
import { useTheme } from '@/lib/theme'
import { ThemeToggle } from '@/components/theme-toggle'
import { MiniPlayerBar } from '@/components/audio'
import { creditReach, useCreditGuide } from '@/components/plans'
import { Avatar, Button, cn, Dialog, Empty, Fade, Field, Input, Kbd, Menu, Popover, Progress, Sheet, Spinner, Tip } from '@/components/ui'
import { toast } from 'sonner'
import { useShallow } from 'zustand/react/shallow'

/** Main navigation, grouped the way people work: make books → pick voices and material → work with a team. */
const navGroups: { label?: string; items: { to: string; label: string; icon: any; end?: boolean }[] }[] = [
  {
    items: [
      { to: '/', label: '工作台', icon: Gauge, end: true },
      { to: '/books', label: '图书', icon: BookOpen },
      { to: '/autobook', label: '一键成书', icon: Wand2 },
      { to: '/quick', label: '快速配音', icon: Zap },
    ],
  },
  {
    label: '声音与素材',
    items: [
      { to: '/voices', label: '音色库', icon: AudioLines },
      { to: '/assets', label: '资产库', icon: Boxes },
    ],
  },
  {
    label: '团队协作',
    items: [
      { to: '/my-lines', label: '我的台词', icon: Mic2 },
      { to: '/production', label: '生产管理', icon: KanbanSquare },
    ],
  },
  { items: [{ to: '/jobs', label: '任务中心', icon: ListChecks }] },
]

export default function Shell() {
  useLiveEvents()
  const [mobileNav, setMobileNav] = useState(false)
  const [palette, setPalette] = useState(false)
  const loc = useLocation()
  const go = useNavigate()
  useEffect(() => {
    // navigation requests from outside the router (e.g. the upgrade dialog)
    const h = (e: Event) => go((e as CustomEvent<string>).detail)
    window.addEventListener('vb:navigate', h)
    return () => window.removeEventListener('vb:navigate', h)
  }, [go])
  useEffect(() => setMobileNav(false), [loc.pathname])
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setPalette((v) => !v)
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [])
  const section = '/' + (loc.pathname.split('/')[1] || '')

  return (
    <div className="flex h-full">
      <aside className="hidden w-[228px] shrink-0 flex-col border-r border-line bg-surface lg:flex">
        <Sidebar />
      </aside>
      <AnimatePresence>
        {mobileNav && (
          <>
            <motion.div className="fixed inset-0 z-40 bg-[var(--scrim)] lg:hidden" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} onClick={() => setMobileNav(false)} />
            <motion.aside className="fixed inset-y-0 left-0 z-50 flex w-[260px] flex-col border-r border-line bg-surface lg:hidden" initial={{ x: -280 }} animate={{ x: 0 }} exit={{ x: -280 }} transition={{ type: 'spring', stiffness: 380, damping: 38 }}>
              <Sidebar />
            </motion.aside>
          </>
        )}
      </AnimatePresence>
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 shrink-0 items-center gap-3 border-b border-line bg-bg/80 px-4 backdrop-blur lg:px-8">
          <button className="grid size-8 place-items-center rounded-md hover:bg-sunken lg:hidden" onClick={() => setMobileNav(true)} aria-label="打开导航">
            <MenuIcon className="size-5" />
          </button>
          <button onClick={() => setPalette(true)} className="flex h-9 w-full max-w-[360px] items-center gap-2 rounded-control border border-line bg-surface px-3 text-[13px] text-faint transition-colors hover:border-line-strong">
            <Search className="size-4" />
            <span className="min-w-0 flex-1 truncate whitespace-nowrap text-left"><span className="max-sm:hidden">搜索图书、章节、角色、音色</span><span className="sm:hidden">搜索</span></span>
            <span className="hidden items-center gap-0.5 text-[11px] sm:flex">
              <Command className="size-3" />K
            </span>
          </button>
          <div className="ml-auto flex items-center gap-1.5">
            <ThemeToggle />
            <JobsIndicator />
            <MemberBadge />
            <Credits />
            <Notifications />
            <UserMenu />
          </div>
        </header>
        <main className="min-h-0 flex-1 overflow-y-auto">
          {/* a new section fades in; the page loads inside it, so the frame stays put while it does */}
          <Fade k={section} className="page-enter mx-auto w-full max-w-[1280px] px-4 py-7 lg:px-8">
            <Suspense fallback={<div className="grid min-h-60 place-items-center"><Spinner className="size-5" /></div>}>
              <Outlet />
            </Suspense>
          </Fade>
        </main>
      </div>
      <SearchPalette open={palette} onOpenChange={setPalette} />
      <MiniPlayerBar />
    </div>
  )
}

function Sidebar() {
  return (
    <>
      <div className="flex h-14 items-center gap-2.5 px-5">
        <img src="/favicon.svg" className="size-7" alt="" />
        <span className="font-serif text-[16px] font-semibold tracking-[0.02em]">{useSession.getState().site?.name ?? '声书工坊'}</span>
      </div>
      <StudioSwitcher />
      <nav className="mt-2 flex-1 overflow-y-auto px-3">
        {navGroups.map((g, gi) => (
          <div key={gi} className={cn('space-y-0.5', gi > 0 && (g.label ? 'mt-4' : 'mt-3 border-t border-line pt-3'))}>
            {g.label && <div className="px-3 pb-1 text-[11px] font-medium tracking-[0.04em] text-faint">{g.label}</div>}
            {g.items.map((n) => (
              <NavLink key={n.to} to={n.to} end={n.end} className={({ isActive }) => cn('group relative flex h-9 items-center gap-2.5 rounded-[8px] px-3 text-[13.5px] transition-colors', isActive ? 'font-medium text-ink' : 'text-muted hover:bg-sunken hover:text-ink')}>
                {({ isActive }) => (
                  <>
                    {isActive && <motion.span layoutId="nav-active" className="absolute inset-0 rounded-[8px] bg-sunken" transition={{ type: 'spring', stiffness: 500, damping: 40 }} />}
                    <n.icon className={cn('relative size-[18px] transition-colors', isActive ? 'text-accent' : '')} weight={isActive ? 'duotone' : 'regular'} />
                    <span className="relative">{n.label}</span>
                  </>
                )}
              </NavLink>
            ))}
          </div>
        ))}
      </nav>
      <div className="space-y-0.5 border-t border-line p-3">
        <NavLink to="/membership" className={({ isActive }) => cn('flex h-9 items-center gap-2.5 rounded-[8px] px-3 text-[13.5px]', isActive ? 'bg-sunken text-ink' : 'text-muted hover:bg-sunken hover:text-ink')}>
          <Crown className="size-[17px] text-warn" strokeWidth={1.8} /> 会员中心
        </NavLink>
        <NavLink to="/studio" className={({ isActive }) => cn('flex h-9 items-center gap-2.5 rounded-[8px] px-3 text-[13.5px]', isActive ? 'bg-sunken text-ink' : 'text-muted hover:bg-sunken hover:text-ink')}>
          <Settings className="size-[17px]" strokeWidth={1.8} /> 工作室设置
        </NavLink>
        <NavLink to="/help" className={({ isActive }) => cn('flex h-9 items-center gap-2.5 rounded-[8px] px-3 text-[13.5px]', isActive ? 'bg-sunken text-ink' : 'text-muted hover:bg-sunken hover:text-ink')}>
          <CircleHelp className="size-[17px]" strokeWidth={1.8} /> 帮助中心
        </NavLink>
      </div>
    </>
  )
}

function StudioSwitcher() {
  const { studios, setStudio } = useSession()
  const cur = useStudio()
  const [create, setCreate] = useState(false)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const nav = useNavigate()
  return (
    <div className="px-3">
      <Menu
        align="start"
        trigger={
          <button className="flex w-full items-center gap-2.5 rounded-[9px] border border-line px-2.5 py-2 text-left transition-colors hover:border-line-strong">
            <Avatar name={cur?.name} src={cur?.logo} size={26} />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-[13px] font-medium">{cur?.name}</span>
              <span className="block text-[11px] text-faint">{cur?.plan || '免费版'} · {cur?.type === 'personal' ? '个人' : '团队'}</span>
            </span>
            <ChevronsUpDown className="size-4 text-faint" />
          </button>
        }
        items={[
          ...studios.map((s) => ({
            label: (
              <span className="flex w-52 items-center justify-between gap-2">
                <span className="truncate">{s.name}</span>
                {s.id === cur?.id && <Check className="size-4 text-accent" />}
              </span>
            ),
            icon: <Avatar name={s.name} src={s.logo} size={18} />,
            onSelect: () => {
              setStudio(s.id)
              nav('/')
            },
          })),
          'sep' as const,
          { label: '创建团队工作室', icon: <Plus />, onSelect: () => setCreate(true) },
        ]}
      />
      <Dialog
        open={create}
        onOpenChange={setCreate}
        title="创建团队工作室"
        description="团队工作室可以邀请成员一起制作，图书、音色和积分都归工作室所有。"
        footer={
          <>
            <Button onClick={() => setCreate(false)}>取消</Button>
            <Button
              variant="primary"
              loading={busy}
              disabled={!name.trim()}
              onClick={async () => {
                setBusy(true)
                try {
                  const st = await api.post('/studios', { name })
                  await useSession.getState().load()
                  setStudio(st.id)
                  setCreate(false)
                  toast.success('工作室已创建')
                  nav('/studio/members')
                } catch (e: any) {
                  if (e?.code === 'plan_limit' || e?.code === 'plan_required') setCreate(false) // the upgrade dialog takes over
                  else toast.error(e.message)
                } finally {
                  setBusy(false)
                }
              }}
            >
              创建
            </Button>
          </>
        }
      >
        <Field label="工作室名称">
          <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="例如：星河有声工作室" />
        </Field>
      </Dialog>
    </div>
  )
}

function JobsIndicator() {
  const jobs = useLive(useShallow((s) => Object.values(s.jobs).filter((j) => j.status === 'running' || j.status === 'queued')))
  const nav = useNavigate()
  if (!jobs.length) return null
  return (
    <Popover
      align="end"
      width={340}
      trigger={
        <button className="flex h-8 items-center gap-2 rounded-full bg-signal-soft px-3 text-[12.5px] font-medium text-warn">
          <span className="eq flex h-3 items-end">
            <span />
            <span />
            <span />
          </span>
          {jobs.length} 个任务进行中
        </button>
      }
    >
      <div className="space-y-3">
        {jobs.slice(0, 6).map((j) => (
          <div key={j.id}>
            <div className="flex justify-between gap-2 text-[13px]">
              <span className="truncate font-medium">{j.title}</span>
              <span className="text-faint tnum">{Math.round(j.progress * 100)}%</span>
            </div>
            <Progress value={j.progress} tone="signal" className="mt-1.5" height={4} />
            {j.progressText && <div className="mt-1 truncate text-xs text-muted">{j.progressText}</div>}
          </div>
        ))}
        <Button size="sm" className="w-full" onClick={() => nav('/jobs')}>
          打开任务中心
        </Button>
      </div>
    </Popover>
  )
}

function MemberBadge() {
  const m = useSession((s) => s.membership)
  const nav = useNavigate()
  if (!m) return null
  const left = m.expiresAt ? Math.ceil((new Date(m.expiresAt).getTime() - Date.now()) / 86400000) : null
  return (
    <Tip label={m.isDefault ? '升级会员，解锁更多图书、成员和每月积分' : left != null ? `有效期还剩 ${left} 天` : '永久会员'}>
      <button onClick={() => nav('/membership')} className={cn('hidden h-8 items-center gap-1.5 rounded-full px-3 text-[12.5px] font-medium transition-colors sm:flex', m.isDefault ? 'text-warn hover:bg-signal-soft' : 'bg-signal-soft text-warn hover:brightness-95')}>
        <Crown className="size-3.5" />
        {m.isDefault ? '升级会员' : m.planName}
        {left != null && left <= 7 && <span className="rounded-full bg-warn/15 px-1.5 text-[11px] tnum">{left} 天</span>}
      </button>
    </Tip>
  )
}

function Credits() {
  const studio = useStudio()
  const { data } = useQuery({ queryKey: ['credits', studio?.id], queryFn: () => api.get(`/studios/${studio!.id}/credits`), enabled: !!studio, refetchInterval: 60_000 })
  const guide = useCreditGuide()
  const reach = creditReach(data?.balance?.available, guide.data)
  const nav = useNavigate()
  return (
    <Tip label={<>可用积分（AI 功能按积分计量）{reach && <><br />{reach.replace('约 ', '约可制作 ')} 标准档多播有声书</>}</>}>
      <button onClick={() => nav('/studio/billing')} className="hidden h-8 items-center gap-1.5 rounded-full px-3 text-[12.5px] text-muted hover:bg-sunken hover:text-ink sm:flex">
        <Sparkles className="size-3.5 text-accent" />
        <span className="font-medium text-ink tnum">{num(data?.balance?.available)}</span> 积分
      </button>
    </Tip>
  )
}

function Notifications() {
  const unread = useSession((s) => s.unread)
  const [open, setOpen] = useState(false)
  const nav = useNavigate()
  const { data, refetch } = useQuery({ queryKey: ['notifications'], queryFn: () => api.get('/notifications?pageSize=40'), enabled: open })
  const markAll = async () => {
    await api.post('/notifications/read', { all: true })
    useSession.setState({ unread: 0 })
    refetch()
  }
  return (
    <>
      <button onClick={() => setOpen(true)} className="relative grid size-8 place-items-center rounded-full text-muted hover:bg-sunken hover:text-ink" aria-label="通知">
        <Bell className="size-[18px]" />
        {unread > 0 && <span className="absolute right-1 top-1 grid min-w-4 place-items-center rounded-full bg-danger px-1 text-[10px] font-semibold leading-4 text-white">{unread > 99 ? '99+' : unread}</span>}
      </button>
      <Sheet open={open} onOpenChange={setOpen} title="通知" footer={unread > 0 ? <Button size="sm" onClick={markAll}>全部标为已读</Button> : undefined}>
        {!data?.items?.length ? (
          <Empty icon={<Bell />} title="暂时没有通知" description="任务完成、被分配工作、收到评论时会在这里提醒你。" />
        ) : (
          <div className="-mx-2 space-y-0.5">
            {data.items.map((n: any) => (
              <button
                key={n.id}
                onClick={() => {
                  // open the target right away; marking as read happens in the background
                  if (!n.readAt) {
                    useSession.setState((s) => ({ unread: Math.max(0, s.unread - 1) }))
                    api.post('/notifications/read', { ids: [n.id] }).then(() => refetch(), () => {})
                  }
                  if (n.link) {
                    setOpen(false)
                    nav(n.link)
                  }
                }}
                className="flex w-full gap-3 rounded-[10px] px-2 py-2.5 text-left hover:bg-sunken"
              >
                <span className={cn('mt-1.5 size-2 shrink-0 rounded-full', n.readAt ? 'bg-transparent' : 'bg-accent')} />
                <span className="min-w-0 flex-1">
                  <span className="block text-[13px] font-medium">{n.title}</span>
                  {n.body && <span className="mt-0.5 line-clamp-2 block text-xs text-muted">{n.body}</span>}
                  <span className="mt-1 block text-[11px] text-faint">{fromNow(n.createdAt)}</span>
                </span>
              </button>
            ))}
          </div>
        )}
      </Sheet>
    </>
  )
}

function UserMenu() {
  const { user, signOut } = useSession()
  const { theme, setTheme } = useTheme()
  const nav = useNavigate()
  return (
    <Menu
      trigger={
        <button className="ml-1 rounded-full" aria-label="账户">
          <Avatar name={user?.nickname} src={user?.avatarUrl} size={30} />
        </button>
      }
      items={[
        { label: <span className="block w-48"><span className="block font-medium">{user?.nickname}</span><span className="block truncate text-xs text-faint">{user?.email || user?.phone}</span></span>, onSelect: () => nav('/settings/account') },
        'sep',
        { label: '账户设置', icon: <User />, onSelect: () => nav('/settings/account') },
        { label: theme === 'dark' ? '浅色模式' : theme === 'light' ? '跟随系统' : '深色模式', icon: theme === 'dark' ? <Sun /> : theme === 'light' ? <Monitor /> : <Moon />, onSelect: () => setTheme(theme === 'dark' ? 'light' : theme === 'light' ? 'system' : 'dark') },
        user?.adminRoles?.length ? { label: '管理后台', icon: <Settings />, onSelect: () => (window.location.href = '/admin') } : null,
        'sep',
        {
          label: '退出登录',
          icon: <LogOut />,
          onSelect: async () => {
            await signOut()
            nav('/login')
          },
        },
      ]}
    />
  )
}

function SearchPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const studio = useStudio()
  const [q, setQ] = useState('')
  const [hi, setHi] = useState(0)
  const nav = useNavigate()
  const { data, isFetching } = useQuery({ queryKey: ['search', studio?.id, q], queryFn: () => api.get(`/studios/${studio!.id}/search?q=${encodeURIComponent(q)}`), enabled: open && q.trim().length > 0 })
  const go = (to: string) => {
    onOpenChange(false)
    setQ('')
    nav(to)
  }
  const actions: [string, string][] = [
    ['新建图书', '/books/new'],
    ['一键成书', '/autobook'],
    ['AI 设计音色', '/voices/design'],
    ['克隆我的声音', '/voices/clone'],
    ['快速配音', '/quick'],
    ['邀请成员', '/studio/members'],
  ]
  type Hit = { label: string; to: string; sub?: string }
  const groups: [string, any, Hit[]][] = q
    ? [
        ['图书', BookOpen, (data?.books ?? []).map((b: any) => ({ label: b.title, to: `/books/${b.id}`, sub: b.author }))],
        ['章节', FileText, (data?.chapters ?? []).map((c: any) => ({ label: c.title, to: `/books/${c.bookId}/chapters/${c.id}`, sub: c.bookTitle }))],
        ['角色', UserRound, (data?.characters ?? []).map((c: any) => ({ label: c.name, to: `/books/${c.bookId}/characters` }))],
        ['音色', AudioLines, (data?.voices ?? []).map((v: any) => ({ label: v.name, to: `/voices?voice=${v.id}`, sub: (v.tags ?? []).join('、') }))],
      ]
    : [['快捷操作', Zap, actions.map(([label, to]) => ({ label, to }))]]
  const flat = groups.flatMap(([, , items]) => items)
  const empty = q && !isFetching && !flat.length
  useEffect(() => setHi(0), [q, data])
  useEffect(() => {
    document.querySelector(`[data-hit="${hi}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [hi])
  const onKey = (e: React.KeyboardEvent) => {
    if (!flat.length) return
    if (e.key === 'ArrowDown') { e.preventDefault(); setHi((h) => (h + 1) % flat.length) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setHi((h) => (h - 1 + flat.length) % flat.length) }
    else if (e.key === 'Enter' && !e.nativeEvent.isComposing) { e.preventDefault(); go(flat[hi].to) }
  }
  let n = -1
  return (
    <Dialog open={open} onOpenChange={onOpenChange} width={600} className="h-[min(440px,calc(100dvh-48px))]"
      footer={
        <div className="-mx-6 -mb-5 -mt-2 flex w-[calc(100%+48px)] items-center gap-4 border-t border-line px-6 py-2.5 text-[11.5px] text-faint">
          <span className="flex items-center gap-1"><Kbd>↑</Kbd><Kbd>↓</Kbd>选择</span>
          <span className="flex items-center gap-1"><Kbd>Enter</Kbd>打开</span>
          <span className="flex items-center gap-1"><Kbd>Esc</Kbd>关闭</span>
          <span className="ml-auto flex items-center gap-1 max-sm:hidden"><Kbd>Ctrl</Kbd><Kbd>K</Kbd>随时呼出</span>
        </div>
      }>
      <div className="-mx-6 -mt-6 border-b border-line py-3 pl-5 pr-12">
        <Input autoFocus icon={<Search />} placeholder="搜索图书、章节、角色、音色" value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={onKey} className="!h-10 !border-0 !shadow-none !ring-0 text-[15px]" />
      </div>
      <div className="-mx-3 pt-2">
        <div>
          {groups.map(([title, Icon, items]) =>
            items.length ? (
              <div key={title} className="mb-2">
                <div className="px-3 pb-1 pt-1.5 text-[11.5px] font-medium tracking-[0.04em] text-faint">{title}</div>
                {items.map((it) => {
                  const k = ++n
                  const on = k === hi
                  return (
                    <button key={it.to + it.label} data-hit={k} onMouseMove={() => setHi(k)} onClick={() => go(it.to)} className={cn('relative flex w-full items-center gap-3 rounded-[9px] px-3 py-2 text-left text-[13.5px] transition-colors', on ? 'text-ink' : 'text-ink/85')}>
                      {on && <motion.span layoutId="palette-hi" className="absolute inset-0 rounded-[9px] bg-sunken" transition={{ type: 'spring', stiffness: 600, damping: 42 }} />}
                      <Icon className={cn('relative size-4 shrink-0', on ? 'text-accent' : 'text-faint')} />
                      <span className="relative min-w-0 flex-1 truncate">{it.label}</span>
                      {it.sub && <span className="relative max-w-[40%] truncate text-xs text-faint">{it.sub}</span>}
                      {on && <span className="relative text-[11px] text-faint max-sm:hidden">回车打开</span>}
                    </button>
                  )
                })}
              </div>
            ) : null,
          )}
          {q && isFetching && !flat.length && <p className="px-3 py-6 text-center text-[13px] text-faint">正在搜索…</p>}
          {empty && <p className="px-3 py-8 text-center text-[13px] text-muted">没有找到「{q}」相关的内容</p>}
        </div>
      </div>
    </Dialog>
  )
}
