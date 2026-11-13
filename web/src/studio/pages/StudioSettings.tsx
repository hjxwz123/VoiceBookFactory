import { useQuery } from '@tanstack/react-query'
import { motion } from 'framer-motion'
import { ArrowLeftRight, Crown, KeyRound, Link2, Plus, Send, Ticket, Trash2, UserPlus } from '@/components/icons'
import { useEffect, useState } from 'react'
import { NavLink, Navigate, Route, Routes, useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { api, qs } from '@/lib/api'
import { dateTime, featureName, fromNow, num, roleName, yuan } from '@/lib/format'
import { canManage, useSession, useStudio } from '@/lib/session'
import { Avatar, Badge, Button, Checkbox, cn, confirm, Copyable, Dialog, Empty, Field, IconButton, Input, Menu, PageHeader, Pagination, Panel, Progress, Segmented, Select, Stat, SuggestInput, Switch, Table, Term, Textarea } from '@/components/ui'
import { ManageTemplatesLink, templateOptions, useTemplates } from '@/components/templates'
import { invalidate, useMembers, useStudioInfo } from '../hooks'

const nav = [
  ['', '基本信息'],
  ['members', '成员'],
  ['billing', '积分'],
  ['workflows', '生产流程'],
  ['lexicon', '读音词典'],
  ['pay-rates', '计件单价'],
  ['byok', '自带密钥'],
  ['channels', '发布渠道'],
  ['developer', '开放接口'],
]

export default function StudioSettings() {
  const studio = useStudio()
  const manage = canManage(studio?.role)
  return (
    <div>
      <PageHeader title="工作室设置" description={studio?.name} />
      <div className="grid gap-8 lg:grid-cols-[180px_1fr]">
        <nav className="no-scrollbar flex gap-1 overflow-x-auto lg:flex-col">
          {nav.filter(([k]) => manage || ['', 'members', 'billing', 'lexicon'].includes(k)).map(([k, l]) => (
            <NavLink key={k} to={`/studio${k ? '/' + k : ''}`} end className={({ isActive }) => cn('relative shrink-0 rounded-[8px] px-3 py-2 text-[13.5px] transition-colors', isActive ? 'font-medium text-ink' : 'text-muted hover:bg-sunken hover:text-ink')}>
              {({ isActive }) => <>{isActive && <motion.span layoutId="studio-nav" className="absolute inset-0 rounded-[8px] bg-sunken" />}<span className="relative">{l}</span></>}
            </NavLink>
          ))}
        </nav>
        <div className="min-w-0">
          <Routes>
            <Route index element={<General />} />
            <Route path="members" element={<Members />} />
            <Route path="billing" element={<Billing />} />
            <Route path="workflows" element={<Workflows />} />
            <Route path="lexicon" element={<Lexicon />} />
            <Route path="pay-rates" element={<PayRates />} />
            <Route path="byok" element={<Byok />} />
            <Route path="channels" element={<Channels />} />
            <Route path="developer" element={<Developer />} />
            <Route path="*" element={<Navigate to="/studio" replace />} />
          </Routes>
        </div>
      </div>
    </div>
  )
}

function General() {
  const { data } = useStudioInfo()
  const studio = useStudio()
  const [f, setF] = useState<any>(null)
  const templates = useTemplates(data?.id)
  useEffect(() => { if (data && !f) setF({ name: data.name, description: data.description, settings: { ...data.settings } }) }, [data])
  if (!f) return null
  const manage = canManage(studio?.role)
  const s = f.settings
  const setS = (k: string, v: any) => setF({ ...f, settings: { ...s, [k]: v } })
  const tpl = (kind: 'intro' | 'outro') => templateOptions(templates.data, kind)
  return (
    <div className="space-y-6">
      <Panel title="工作室">
        <fieldset disabled={!manage} className="space-y-4">
          <div className="flex items-center gap-4">
            <Avatar name={data.name} src={data.logo} size={52} />
            {manage && (
              <label className="cursor-pointer text-[13px] text-accent">
                <input type="file" accept="image/*" className="hidden" onChange={async (e) => { const file = e.target.files?.[0]; if (!file) return; await api.upload(`/studios/${data.id}/logo`, file); invalidate(['studio']); useSession.getState().load() }} />
                更换标志
              </label>
            )}
          </div>
          <Field label="名称"><Input value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} /></Field>
          <Field label="介绍"><Textarea rows={2} value={f.description} onChange={(e) => setF({ ...f, description: e.target.value })} /></Field>
        </fieldset>
      </Panel>
      <Panel title="新建图书的默认设置">
        <fieldset disabled={!manage} className="grid gap-4 sm:grid-cols-2">
          <Field label="默认片头" hint={<>新建的图书自动使用 · <ManageTemplatesLink /></>}><Select value={s.introTemplateId ?? ''} onChange={(v) => setS('introTemplateId', v)} options={tpl('intro')} /></Field>
          <Field label="默认片尾"><Select value={s.outroTemplateId ?? ''} onChange={(v) => setS('outroTemplateId', v)} options={tpl('outro')} /></Field>
          <Field label="默认导出格式"><Select value={s.defaultFormat || 'mp3'} onChange={(v) => setS('defaultFormat', v)} options={[['mp3', 'MP3'], ['m4a', 'M4A'], ['wav', 'WAV']]} /></Field>
          <Field label="预估语速（字/分钟）"><Input type="number" value={s.speechRate || 220} onChange={(e) => setS('speechRate', Number(e.target.value))} /></Field>
          <div className="space-y-3 sm:col-span-2">
            <Switch checked={!!s.autoQc} onChange={(v) => setS('autoQc', v)} label="生成后自动质检" />
            <Switch checked={!!s.joinApproval} onChange={(v) => setS('joinApproval', v)} label="通过邀请链接加入需要管理员审批" />
            <Switch checked={!!s.preferByok} onChange={(v) => setS('preferByok', v)} label={<span className="inline-flex items-center gap-1">优先使用工作室自己的平台密钥 <Term id="BYOK" /></span>} />
          </div>
        </fieldset>
      </Panel>
      {manage && <div className="flex justify-end"><Button variant="primary" onClick={async () => { await api.patch(`/studios/${data.id}`, f); invalidate(['studio']); useSession.getState().load(); toast.success('已保存') }}>保存</Button></div>}
      {studio?.role === 'owner' && data.type !== 'personal' && (
        <Panel title="危险操作">
          <div className="flex flex-wrap gap-2">
            <TransferOwner studioId={data.id} />
            <Button variant="ghost" className="text-danger" onClick={async () => {
              if (await confirm({ title: `解散「${data.name}」？`, description: '所有图书、音色和剩余积分都会被删除，无法恢复。', danger: true, confirmText: '解散工作室' })) {
                await api.del(`/studios/${data.id}`)
                await useSession.getState().load()
                window.location.href = '/'
              }
            }}>解散工作室</Button>
          </div>
        </Panel>
      )}
    </div>
  )
}

function TransferOwner({ studioId }: { studioId: string }) {
  const members = useMembers()
  const [open, setOpen] = useState(false)
  const [uid, setUid] = useState('')
  return (
    <>
      <Button onClick={() => setOpen(true)}>转让所有权</Button>
      <Dialog open={open} onOpenChange={setOpen} title="转让工作室" description="转让后你会成为管理员。" width={420} footer={<><Button onClick={() => setOpen(false)}>取消</Button><Button variant="danger" disabled={!uid} onClick={async () => { await api.post(`/studios/${studioId}/transfer`, { userId: uid }); await useSession.getState().load(); setOpen(false); toast.success('已转让') }}>转让</Button></>}>
        <Select value={uid} onChange={setUid} placeholder="选择新的所有者" options={(members.data ?? []).filter((m: any) => m.role !== 'owner').map((m: any) => [m.userId, m.nickname] as [string, string])} />
      </Dialog>
    </>
  )
}

function Members() {
  const studio = useStudio()
  const sid = studio?.id
  const me = useSession((s) => s.user)
  const { data: info } = useStudioInfo()
  const members = useMembers()
  const invites = useQuery<any[]>({ queryKey: ['invitations', sid], queryFn: () => api.get(`/studios/${sid}/invitations`), enabled: canManage(studio?.role) })
  const [inv, setInv] = useState(false)
  const [f, setF] = useState({ target: '', role: 'member', days: 7, maxUses: 0 })
  const [link, setLink] = useState('')
  const manage = canManage(studio?.role)
  if (studio?.type === 'personal')
    return <Panel><Empty icon={<UserPlus />} title="个人工作室不能邀请成员" description="在左上角切换菜单里创建一个团队工作室，就可以邀请画本、配音、后期、审听一起协作。" /></Panel>
  return (
    <div className="space-y-6">
      <Panel title="成员" description={info?.seats ? `已使用 ${info.seats.used}/${info.seats.limit || '不限'} 个席位` : undefined} padded={false} actions={manage && <Button size="sm" variant="primary" icon={<UserPlus className="size-4" />} onClick={() => { setLink(''); setInv(true) }}>邀请成员</Button>}>
        <Table>
          <thead><tr><th>成员</th><th>角色</th><th className="!text-right">本月积分</th><th>最近登录</th><th /></tr></thead>
          <tbody>
            {(members.data ?? []).map((m: any) => (
              <tr key={m.userId}>
                <td><span className="flex items-center gap-2.5"><Avatar name={m.nickname} src={m.avatarUrl} size={28} /><span><span className="block font-medium">{m.nickname}{m.status === 'pending' && <Badge tone="warn" className="ml-2">待审批</Badge>}</span><span className="block text-xs text-faint">{m.email}</span></span></span></td>
                <td>
                  {manage && m.role !== 'owner' && m.userId !== me?.id ? (
                    <Select size="sm" value={m.role} onChange={async (v) => { await api.patch(`/studios/${sid}/members/${m.userId}`, { role: v }); invalidate(['members']) }} options={['admin', 'producer', 'member', 'guest'].map((r) => [r, roleName[r]] as [string, string])} className="w-28" />
                  ) : roleName[m.role]}
                </td>
                <td className="text-right tnum">{num(m.creditsThisMonth)}{m.creditCap ? <span className="text-faint"> / {num(m.creditCap)}</span> : ''}</td>
                <td className="text-xs text-faint">{fromNow(m.lastLoginAt)}</td>
                <td className="text-right">
                  {manage && m.status === 'pending' && <Button size="xs" variant="primary" onClick={async () => { await api.patch(`/studios/${sid}/members/${m.userId}`, { approve: true }); invalidate(['members']) }}>通过</Button>}
                  {manage && m.role !== 'owner' && (
                    <Menu trigger={<Button size="xs" variant="ghost">管理</Button>} items={[
                      { label: '设置每月积分上限', onSelect: async () => { const v = prompt('每月最多可用积分（0 表示不限）', String(m.creditCap || 0)); if (v !== null) { await api.patch(`/studios/${sid}/members/${m.userId}`, { creditCap: Number(v) }); invalidate(['members']) } } },
                      'sep',
                      { label: '移出工作室', danger: true, icon: <Trash2 />, onSelect: async () => { if (await confirm({ title: `移出 ${m.nickname}？`, description: '对方将无法访问工作室的任何内容。', danger: true, confirmText: '移出' })) { await api.del(`/studios/${sid}/members/${m.userId}`); invalidate(['members']) } } },
                    ]} />
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      </Panel>
      {manage && !!invites.data?.length && (
        <Panel title="有效的邀请" padded={false}>
          <ul>
            {invites.data.filter((i) => !i.revokedAt).map((i) => (
              <li key={i.id} className="flex items-center gap-3 border-t border-line px-5 py-3 text-[13px] first:border-t-0">
                <Link2 className="size-4 text-muted" />
                <span className="flex-1">{i.target || '邀请链接'} · {roleName[i.role]} · 已使用 {i.uses}{i.maxUses ? `/${i.maxUses}` : ''} 次 · {dateTime(i.expiresAt)} 过期</span>
                <Copyable text={`${location.origin}/invite/${i.token}`} className="max-w-56" />
                <IconButton label="撤销" size="xs" icon={<Trash2 className="size-3.5" />} onClick={async () => { await api.del(`/studios/${sid}/invitations/${i.id}`); invalidate(['invitations']) }} />
              </li>
            ))}
          </ul>
        </Panel>
      )}
      <Panel title="角色说明">
        <dl className="grid gap-3 text-[13px] sm:grid-cols-2">
          {[['所有者 / 管理员', '管理成员、积分、密钥和所有图书。'], ['制作人', '创建图书、组建团队、分配任务，只能看到自己参与的书。'], ['成员', '只能看到被加入的图书，按图书里的职位（画本 / 配音 / 后期 / 审听）工作。'], ['外部协作者', '临时合作的配音演员或审听，权限同成员，可设置不占席位。']].map(([k, v]) => <div key={k}><dt className="font-medium">{k}</dt><dd className="mt-0.5 text-muted">{v}</dd></div>)}
        </dl>
      </Panel>
      <Dialog open={inv} onOpenChange={setInv} title="邀请成员" width={480} footer={link ? <Button variant="primary" onClick={() => setInv(false)}>完成</Button> : <><Button onClick={() => setInv(false)}>取消</Button><Button variant="primary" onClick={async () => {
        const r = await api.post(`/studios/${sid}/invitations`, f)
        setLink(`${location.origin}/invite/${r.token ?? r.invitation?.token}`)
        invalidate(['invitations'])
        if (f.target) toast.success('邀请已发送')
      }}>生成邀请</Button></>}>
        {link ? (
          <div className="space-y-3 text-center"><p className="text-[13.5px]">把链接发给对方，登录或注册后即可加入。</p><Copyable text={link} className="mx-auto" /></div>
        ) : (
          <div className="space-y-4">
            <Field label="邮箱或手机号" hint="留空则生成一个可多人使用的邀请链接"><Input value={f.target} onChange={(e) => setF({ ...f, target: e.target.value })} /></Field>
            <div className="grid grid-cols-3 gap-3">
              <Field label="角色"><Select value={f.role} onChange={(v) => setF({ ...f, role: v })} options={['admin', 'producer', 'member', 'guest'].map((r) => [r, roleName[r]] as [string, string])} /></Field>
              <Field label="有效期"><Select value={String(f.days)} onChange={(v) => setF({ ...f, days: Number(v) })} options={[['1', '1 天'], ['7', '7 天'], ['30', '30 天']]} /></Field>
              <Field label="可用次数"><Select value={String(f.maxUses)} onChange={(v) => setF({ ...f, maxUses: Number(v) })} options={[['1', '1 次'], ['10', '10 次'], ['0', '不限']]} /></Field>
            </div>
          </div>
        )}
      </Dialog>
    </div>
  )
}

function Billing() {
  const sid = useSession((s) => s.studioId)
  const studios = useSession((s) => s.studios)
  const { data: info } = useStudioInfo()
  const go = useNavigate()
  const credits = useQuery({ queryKey: ['credits', sid], queryFn: () => api.get(`/studios/${sid}/credits`) })
  const [page, setPage] = useState(1)
  const ledger = useQuery({ queryKey: ['ledger', sid, page], queryFn: () => api.get(`/studios/${sid}/ledger${qs({ page, pageSize: 20 })}`), placeholderData: (p) => p })
  const [transfer, setTransfer] = useState(false)
  const b = credits.data?.balance
  const cur = studios.find((s) => s.id === sid)
  const managedOthers = studios.filter((s) => s.id !== sid && (s.role === 'owner' || s.role === 'admin'))
  const manager = cur?.role === 'owner' || cur?.role === 'admin'
  return (
    <div className="space-y-6">
      <Panel>
        <div className="grid grid-cols-2 gap-6 sm:grid-cols-4">
          <Stat label="可用积分" value={num(b?.available)} />
          <Stat label="任务占用中" value={num(b?.frozen)} hint="任务结束后多退少补" />
          <Stat label="30 天内过期" value={num(b?.expiring)} />
          <Stat label="会员方案" value={info?.plan?.name ?? '免费版'} hint={info?.planOverrideId ? '平台签约方案' : '跟随所有者的会员'} />
        </div>
        <div className="mt-6 flex flex-wrap items-center gap-2 border-t border-line pt-5">
          <Button variant="primary" icon={<Crown className="size-4" />} onClick={() => go('/membership')}>会员中心</Button>
          <Button icon={<Ticket className="size-4" />} onClick={() => go('/membership#redeem')}>使用兑换码</Button>
          {manager && managedOthers.length > 0 && <Button icon={<ArrowLeftRight className="size-4" />} onClick={() => setTransfer(true)}>划转积分</Button>}
          <p className="ml-auto text-xs text-faint">积分随会员按月赠送，也可通过兑换码获得。</p>
        </div>
      </Panel>
      <TransferDialog open={transfer} onOpenChange={setTransfer} available={b?.available ?? 0} targets={managedOthers} />
      <Panel title="积分价目" description="不同质量档的音色价格不同；部分功能每天有免费额度。" padded={false}>
        <Table>
          <thead><tr><th>功能</th><th>档位</th><th className="!text-right">积分</th><th>计量单位</th><th>每日免费</th></tr></thead>
          <tbody>{(credits.data?.pricing ?? []).map((p: any) => <tr key={p.id}><td>{p.name || featureName[p.feature]}</td><td className="text-muted">{p.tier || '—'}</td><td className="text-right tnum">{num(p.credits)}</td><td className="text-muted">{p.unit}</td><td className="text-muted">{p.freeDaily || '—'}</td></tr>)}</tbody>
        </Table>
      </Panel>
      <Panel title="积分流水" padded={false}>
        <Table>
          <thead><tr><th>时间</th><th>说明</th><th className="!text-right">变动</th><th className="!text-right">余额</th></tr></thead>
          <tbody>{(ledger.data?.items ?? []).map((l: any) => <tr key={l.id}><td className="whitespace-nowrap text-xs text-faint">{dateTime(l.createdAt)}</td><td>{l.reason || featureName[l.feature] || l.kind}</td><td className={cn('text-right tnum', l.delta > 0 ? 'text-ok' : '')}>{l.delta > 0 ? '+' : ''}{num(l.delta)}</td><td className="text-right text-muted tnum">{num(l.balanceAfter)}</td></tr>)}</tbody>
        </Table>
        {ledger.data && <div className="px-5 pb-4"><Pagination page={ledger.data.page} pageSize={ledger.data.pageSize} total={ledger.data.total} onChange={setPage} /></div>}
      </Panel>
    </div>
  )
}

function TransferDialog({ open, onOpenChange, available, targets }: { open: boolean; onOpenChange: (v: boolean) => void; available: number; targets: { id: string; name: string }[] }) {
  const sid = useSession((s) => s.studioId)
  const [to, setTo] = useState('')
  const [amount, setAmount] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (open) {
      setTo(targets[0]?.id ?? '')
      setAmount('')
    }
  }, [open])
  const n = Number(amount)
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title="划转积分"
      description="把这个工作室的积分转到你管理的另一个工作室，积分保持原来的有效期。"
      width={440}
      footer={<><Button onClick={() => onOpenChange(false)}>取消</Button><Button variant="primary" loading={busy} disabled={!to || !(n > 0) || n > available} onClick={async () => {
        setBusy(true)
        try {
          await api.post(`/studios/${sid}/credits/transfer`, { toStudioId: to, amount: n })
          toast.success(`已划转 ${num(n)} 积分`)
          invalidate(['credits'], ['ledger'])
          onOpenChange(false)
        } finally {
          setBusy(false)
        }
      }}>确认划转</Button></>}
    >
      <div className="space-y-4">
        <Field label="转到"><Select value={to} onChange={setTo} options={targets.map((t) => [t.id, t.name])} /></Field>
        <Field label="积分数量" hint={`当前可用 ${num(available)} 积分`} error={n > available ? '超过可用积分' : undefined}>
          <div className="flex gap-2">
            <Input type="number" min={1} value={amount} onChange={(e) => setAmount(e.target.value)} placeholder="例如 50000" />
            <Button onClick={() => setAmount(String(available))}>全部</Button>
          </div>
        </Field>
      </div>
    </Dialog>
  )
}

function Workflows() {
  const sid = useSession((s) => s.studioId)
  const { data } = useQuery<any[]>({ queryKey: ['workflows', sid], queryFn: () => api.get(`/studios/${sid}/workflows`) })
  const [edit, setEdit] = useState<any>(null)
  const kinds = [['script', '画本'], ['tts', 'AI 配音'], ['record', '真人录音'], ['align', '对轨'], ['post', '后期'], ['review', '审听'], ['custom', '自定义']]
  return (
    <div className="space-y-4">
      <div className="flex justify-between"><p className="text-[13px] text-muted">流程模板定义每章要经过哪些环节、谁来做、要不要审核。在图书设置里选择模板开启。</p><Button size="sm" variant="primary" icon={<Plus className="size-4" />} onClick={() => setEdit({ name: '', stages: [{ key: 'script', name: '画本', kind: 'script', needsApproval: false, defaultDays: 2 }] })}>新建模板</Button></div>
      {(data ?? []).map((w) => (
        <Panel key={w.id}>
          <div className="flex items-center gap-3">
            <span className="text-[14px] font-semibold">{w.name}</span>
            {w.scope === 'system' && <Badge tone="accent">内置</Badge>}
            <div className="ml-auto">{w.scope !== 'system' && <Button size="xs" variant="ghost" onClick={() => setEdit(w)}>编辑</Button>}</div>
          </div>
          <div className="mt-3 flex flex-wrap items-center gap-2 text-[13px]">
            {w.stages.map((s: any, i: number) => <span key={i} className="flex items-center gap-2">{i > 0 && <span className="text-faint">→</span>}<span className="rounded-md bg-sunken px-2 py-1">{s.name}{s.needsApproval && <span className="ml-1 text-xs text-warn">需审核</span>}</span></span>)}
          </div>
        </Panel>
      ))}
      <Dialog open={!!edit} onOpenChange={(o) => !o && setEdit(null)} title={edit?.id ? '编辑流程' : '新建流程'} width={620} footer={edit && <>
        {edit.id && <Button variant="ghost" className="mr-auto text-danger" onClick={async () => { await api.del(`/studios/${sid}/workflows/${edit.id}`); invalidate(['workflows']); setEdit(null) }}>删除</Button>}
        <Button onClick={() => setEdit(null)}>取消</Button>
        <Button variant="primary" disabled={!edit.name.trim() || !edit.stages.length} onClick={async () => { if (edit.id) await api.put(`/studios/${sid}/workflows/${edit.id}`, edit); else await api.post(`/studios/${sid}/workflows`, edit); invalidate(['workflows']); setEdit(null) }}>保存</Button>
      </>}>
        {edit && (
          <div className="space-y-4">
            <Field label="名称"><Input value={edit.name} onChange={(e) => setEdit({ ...edit, name: e.target.value })} /></Field>
            <div className="space-y-2">
              {edit.stages.map((s: any, i: number) => (
                <div key={i} className="flex items-center gap-2">
                  <span className="w-5 text-xs text-faint tnum">{i + 1}</span>
                  <Input value={s.name} onChange={(e) => setEdit({ ...edit, stages: edit.stages.map((x: any, k: number) => (k === i ? { ...x, name: e.target.value } : x)) })} className="flex-1" />
                  <Select value={s.kind} onChange={(v) => setEdit({ ...edit, stages: edit.stages.map((x: any, k: number) => (k === i ? { ...x, kind: v, key: v + (k || '') } : x)) })} options={kinds as [string, string][]} className="w-28" />
                  <Input type="number" value={s.defaultDays} onChange={(e) => setEdit({ ...edit, stages: edit.stages.map((x: any, k: number) => (k === i ? { ...x, defaultDays: Number(e.target.value) } : x)) })} className="w-16" title="默认天数" />
                  <Checkbox checked={s.needsApproval} onChange={(v) => setEdit({ ...edit, stages: edit.stages.map((x: any, k: number) => (k === i ? { ...x, needsApproval: v } : x)) })} label="审核" />
                  <IconButton label="删除" size="xs" icon={<Trash2 className="size-3.5" />} onClick={() => setEdit({ ...edit, stages: edit.stages.filter((_: any, k: number) => k !== i) })} />
                </div>
              ))}
              <Button size="xs" variant="ghost" icon={<Plus className="size-3.5" />} onClick={() => setEdit({ ...edit, stages: [...edit.stages, { key: 'custom' + edit.stages.length, name: '新环节', kind: 'custom', needsApproval: false, defaultDays: 2 }] })}>添加环节</Button>
            </div>
          </div>
        )}
      </Dialog>
    </div>
  )
}

function Lexicon() {
  const sid = useSession((s) => s.studioId)
  const studio = useStudio()
  const [q, setQ] = useState('')
  const { data } = useQuery<any[]>({ queryKey: ['studio-lexicon', sid, q], queryFn: () => api.get(`/studios/${sid}/lexicon${qs({ q })}`) })
  const [f, setF] = useState({ term: '', pinyin: '', replacement: '', note: '' })
  return (
    <div className="space-y-6">
      <Panel title="添加词条" description="工作室词典对所有图书生效。比如人名「乐正」读 yuè zhèng，或把「Dr.」读作「博士」。">
        <div className="grid gap-3 sm:grid-cols-[1fr_1fr_1fr_auto]">
          <Input value={f.term} onChange={(e) => setF({ ...f, term: e.target.value })} placeholder="词语" />
          <Input value={f.pinyin} onChange={(e) => setF({ ...f, pinyin: e.target.value })} placeholder="拼音，如 yuè zhèng" />
          <Input value={f.replacement} onChange={(e) => setF({ ...f, replacement: e.target.value })} placeholder="或替换为（可选）" />
          <Button variant="primary" disabled={!f.term || (!f.pinyin && !f.replacement)} onClick={async () => { await api.post(`/studios/${sid}/lexicon`, f); setF({ term: '', pinyin: '', replacement: '', note: '' }); invalidate(['studio-lexicon']) }}>添加</Button>
        </div>
      </Panel>
      <Panel padded={false} title="词条" actions={<Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索" className="!h-8 w-44" />}>
        {!(data ?? []).length ? <Empty title="还没有词条" className="py-10" /> : (
          <Table>
            <thead><tr><th>词语</th><th>读音 / 替换</th><th>来源</th><th>命中</th><th /></tr></thead>
            <tbody>{data!.map((e) => <tr key={e.id}><td className="font-medium">{e.term}</td><td>{e.pinyin || `→ ${e.replacement}`}</td><td className="text-muted">{e.scope === 'platform' ? '平台' : e.scope === 'book' ? '图书' : '工作室'}{e.status === 'pending' && <Badge tone="warn" className="ml-1">待审核</Badge>}</td><td className="tnum text-muted">{e.hits}</td><td className="text-right">{e.scope === 'studio' && canManage(studio?.role) && <>
              <Button size="xs" variant="ghost" onClick={async () => { await api.post(`/lexicon/${e.id}/submit`); toast.success('已提交到平台词典审核') }}>贡献到平台</Button>
              <IconButton label="删除" size="xs" icon={<Trash2 className="size-3.5" />} onClick={async () => { await api.del(`/lexicon/${e.id}`); invalidate(['studio-lexicon']) }} />
            </>}</td></tr>)}</tbody>
          </Table>
        )}
      </Panel>
    </div>
  )
}

function PayRates() {
  const sid = useSession((s) => s.studioId)
  const members = useMembers()
  const { data } = useQuery<any[]>({ queryKey: ['pay-rates', sid], queryFn: () => api.get(`/studios/${sid}/pay-rates`) })
  const [f, setF] = useState({ stageKind: 'record', unit: 'per_1k_words', price: 20, userId: '', note: '' })
  const unitName: Record<string, string> = { per_1k_words: '每千字', per_minute: '每分钟成品', per_episode: '每集' }
  const kindName: Record<string, string> = { script: '画本', tts: 'AI 配音', record: '真人录音', align: '对轨', post: '后期', review: '审听', custom: '自定义' }
  return (
    <div className="space-y-6">
      <Panel title="添加单价" description="任务完成后按单价自动计算计件工资，在「生产管理 → 计件结算」中导出和结算。可以为个别成员设置单独的价格。">
        <div className="grid gap-3 sm:grid-cols-5">
          <Select value={f.stageKind} onChange={(v) => setF({ ...f, stageKind: v })} options={Object.entries(kindName)} />
          <Select value={f.unit} onChange={(v) => setF({ ...f, unit: v })} options={Object.entries(unitName)} />
          <Input type="number" value={f.price} onChange={(e) => setF({ ...f, price: Number(e.target.value) })} placeholder="元" />
          <Select value={f.userId} onChange={(v) => setF({ ...f, userId: v })} options={[['', '所有成员'], ...(members.data ?? []).map((m: any) => [m.userId, m.nickname] as [string, string])]} />
          <Button variant="primary" onClick={async () => { await api.post(`/studios/${sid}/pay-rates`, { stageKey: f.stageKind, stageKind: f.stageKind, unit: f.unit, priceCent: Math.round(f.price * 100), userId: f.userId, note: f.note }); invalidate(['pay-rates']) }}>添加</Button>
        </div>
      </Panel>
      <Panel padded={false}>
        {!(data ?? []).length ? <Empty title="还没有设置单价" className="py-10" /> : (
          <Table>
            <thead><tr><th>环节</th><th>计价方式</th><th className="!text-right">单价</th><th>适用</th><th /></tr></thead>
            <tbody>{data!.map((r) => <tr key={r.id}><td>{kindName[r.stageKind] ?? r.stageKey}</td><td>{unitName[r.unit]}</td><td className="text-right tnum">{yuan(r.priceCent)}</td><td className="text-muted">{r.userId ? (members.data ?? []).find((m: any) => m.userId === r.userId)?.nickname : '所有成员'}</td><td className="text-right"><IconButton label="删除" size="xs" icon={<Trash2 className="size-3.5" />} onClick={async () => { await api.del(`/studios/${sid}/pay-rates/${r.id}`); invalidate(['pay-rates']) }} /></td></tr>)}</tbody>
          </Table>
        )}
      </Panel>
    </div>
  )
}

const kindLabel: Record<string, string> = { llm: '大语言模型', tts: '语音合成', asr: '语音识别', image: '图像生成', music: '音乐生成', sfx: '音效生成' }

function Byok() {
  const sid = useSession((s) => s.studioId)
  const { data } = useQuery<any[]>({ queryKey: ['providers', sid], queryFn: () => api.get(`/studios/${sid}/providers`) })
  const catalog = useQuery<any[]>({ queryKey: ['ai-catalog'], queryFn: () => api.get('/ai-catalog') })
  const [add, setAdd] = useState<any>(null)
  const [testing, setTesting] = useState('')
  return (
    <div className="space-y-6">
      <Panel>
        <div className="flex items-start gap-4">
          <KeyRound className="mt-0.5 size-5 text-accent" />
          <div className="flex-1 text-[13px] text-muted">
            <div className="flex items-center gap-1 text-[14px] font-semibold text-ink">自带平台密钥 <Term id="BYOK" /></div>
            <p className="mt-1">接入工作室自己在 OpenAI、Anthropic、阿里云等平台的账号后，对应功能直接使用你的密钥调用，费用由你和平台结算，不再扣积分。密钥加密保存，只显示最后 4 位。</p>
          </div>
          <Button variant="primary" size="sm" icon={<Plus className="size-4" />} onClick={() => setAdd({ preset: '', name: '', baseUrl: '', apiKey: '', model: '' })}>添加密钥</Button>
        </div>
      </Panel>
      <Panel padded={false}>
        {!(data ?? []).length ? <Empty title="还没有接入自己的密钥" description="不接入也完全可以使用，所有 AI 功能默认走平台通道并按积分计费。" /> : (
          <Table>
            <thead><tr><th>名称</th><th>类型</th><th>模型</th><th>密钥</th><th>状态</th><th /></tr></thead>
            <tbody>{data!.map((p) => (
              <tr key={p.id}>
                <td className="font-medium">{p.name}</td>
                <td>{kindLabel[p.kind]}</td>
                <td className="text-muted">{p.model || '—'}</td>
                <td className="text-muted">••••{p.keyLast4}</td>
                <td>{p.healthStatus === 'ok' ? <Badge tone="ok">正常</Badge> : p.healthStatus === 'error' ? <Badge tone="danger" className="max-w-40 truncate">{p.healthNote || '异常'}</Badge> : <Badge>未检测</Badge>}</td>
                <td className="whitespace-nowrap text-right">
                  <Switch checked={p.enabled} onChange={async (v) => { await api.patch(`/studios/${sid}/providers/${p.id}`, { enabled: v }); invalidate(['providers']) }} />
                  <Button size="xs" variant="ghost" className="ml-2" loading={testing === p.id} onClick={async () => { setTesting(p.id); try { const r = await api.post(`/studios/${sid}/providers/${p.id}/test`); toast.success(r?.message || '连接正常') } catch (e: any) { toast.error(e.message) } finally { setTesting(''); invalidate(['providers']) } }}>测试</Button>
                  <IconButton label="删除" size="xs" icon={<Trash2 className="size-3.5" />} onClick={async () => { if (await confirm({ title: '删除这个密钥？', danger: true, confirmText: '删除' })) { await api.del(`/studios/${sid}/providers/${p.id}`); invalidate(['providers']) } }} />
                </td>
              </tr>
            ))}</tbody>
          </Table>
        )}
      </Panel>
      <Dialog open={!!add} onOpenChange={(o) => !o && setAdd(null)} title="添加平台密钥" width={520} footer={add && <><Button onClick={() => setAdd(null)}>取消</Button><Button variant="primary" disabled={!add.preset || !add.apiKey} onClick={async () => {
        const p = catalog.data!.find((x) => x.key === add.preset)
        await api.post(`/studios/${sid}/providers`, { kind: p.kind, provider: p.provider, name: add.name || p.name, baseUrl: add.baseUrl || p.baseUrl, apiKey: add.apiKey, model: add.model || p.models?.[0] || '', config: p.protocol ? { protocol: p.protocol } : {} })
        invalidate(['providers'])
        setAdd(null)
        toast.success('已添加，建议点「测试」确认连接')
      }}>保存</Button></>}>
        {add && (
          <div className="space-y-4">
            <Field label="平台">
              <Select value={add.preset} onChange={(v) => { const p = catalog.data?.find((x) => x.key === v); setAdd({ ...add, preset: v, baseUrl: p?.baseUrl ?? '', model: p?.models?.[0] ?? '' }) }} placeholder="选择平台" options={(catalog.data ?? []).map((p) => [p.key, `${kindLabel[p.kind]} · ${p.name}${p.implemented ? '' : '（预留）'}`] as [string, string])} />
            </Field>
            {add.preset && <p className="text-xs text-muted">{catalog.data?.find((x) => x.key === add.preset)?.note}</p>}
            <Field label="API Key"><Input type="password" value={add.apiKey} onChange={(e) => setAdd({ ...add, apiKey: e.target.value })} autoComplete="off" /></Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label="接口地址"><Input value={add.baseUrl} onChange={(e) => setAdd({ ...add, baseUrl: e.target.value })} /></Field>
              <Field label="模型"><SuggestInput value={add.model} onChange={(v) => setAdd({ ...add, model: v })} suggestions={catalog.data?.find((x) => x.key === add.preset)?.models ?? []} placeholder="选择或输入模型名" /></Field>
            </div>
          </div>
        )}
      </Dialog>
    </div>
  )
}

function Channels() {
  const sid = useSession((s) => s.studioId)
  const { data } = useQuery({ queryKey: ['channels', sid], queryFn: () => api.get(`/studios/${sid}/channels`) })
  const [add, setAdd] = useState<any>(null)
  const kinds: [string, string][] = [['rss', 'RSS 播客订阅'], ['webhook', '自定义推送（Webhook）'], ['ximalaya', '喜马拉雅'], ['qingting', '蜻蜓 FM'], ['lizhi', '荔枝'], ['spotify', 'Spotify'], ['apple', 'Apple Podcasts']]
  return (
    <div className="space-y-6">
      <div className="flex justify-between"><p className="text-[13px] text-muted">绑定发布渠道后，可以在图书的「导出与发布」里一键或定时连载发布。RSS 地址可以直接提交到小宇宙、Apple Podcasts 等播客平台。</p><Button size="sm" variant="primary" icon={<Plus className="size-4" />} onClick={() => setAdd({ kind: 'rss', name: '', url: '' })}>添加渠道</Button></div>
      <Panel padded={false}>
        {!data?.items?.length ? <Empty icon={<Send />} title="还没有发布渠道" /> : (
          <ul>{data.items.map((c: any) => (
            <li key={c.id} className="flex flex-wrap items-center gap-3 border-t border-line px-5 py-3.5 first:border-t-0">
              <div className="min-w-0 flex-1"><div className="text-[13.5px] font-medium">{c.name}</div><div className="text-xs text-faint">{kinds.find(([k]) => k === c.kind)?.[1]}{!['rss', 'webhook'].includes(c.kind) && ' · 需要平台开放授权，接口已预留'}</div></div>
              {c.kind === 'rss' && <Copyable text={`${location.origin}/feeds/${c.token}.xml`} className="max-w-64" />}
              <Switch checked={c.enabled} onChange={async (v) => { await api.patch(`/studios/${sid}/channels/${c.id}`, { enabled: v }); invalidate(['channels']) }} />
              <IconButton label="删除" size="xs" icon={<Trash2 className="size-3.5" />} onClick={async () => { await api.del(`/studios/${sid}/channels/${c.id}`); invalidate(['channels']) }} />
            </li>
          ))}</ul>
        )}
      </Panel>
      <Dialog open={!!add} onOpenChange={(o) => !o && setAdd(null)} title="添加发布渠道" width={460} footer={add && <><Button onClick={() => setAdd(null)}>取消</Button><Button variant="primary" onClick={async () => { await api.post(`/studios/${sid}/channels`, { kind: add.kind, name: add.name, config: add.kind === 'webhook' ? { url: add.url } : {} }); invalidate(['channels']); setAdd(null) }}>添加</Button></>}>
        {add && (
          <div className="space-y-4">
            <Field label="类型"><Select value={add.kind} onChange={(v) => setAdd({ ...add, kind: v })} options={kinds} /></Field>
            <Field label="名称"><Input value={add.name} onChange={(e) => setAdd({ ...add, name: e.target.value })} placeholder={kinds.find(([k]) => k === add.kind)?.[1]} /></Field>
            {add.kind === 'webhook' && <Field label="推送地址" hint="每集发布时会 POST 音频地址和元数据"><Input value={add.url} onChange={(e) => setAdd({ ...add, url: e.target.value })} placeholder="https://" /></Field>}
          </div>
        )}
      </Dialog>
    </div>
  )
}

// API key scopes, as the open API checks them
const SCOPES: [string, string, string][] = [
  ['read', '查询与下载', '查音色、查图书和任务进度、获取成品下载地址'],
  ['produce', '导入书稿与一键成书', '会消耗工作室积分'],
  ['tts', '单段配音', '会消耗工作室积分'],
]
// keys made before 0.1.4 may carry the older names "write" and "generate"
const LEGACY_SCOPES: Record<string, string[]> = { write: ['produce'], generate: ['produce', 'tts'] }
function scopeLabels(scopes: string[] = []) {
  const set = new Set(scopes.flatMap((s) => LEGACY_SCOPES[s] ?? [s]))
  return SCOPES.filter(([k]) => set.has(k)).map(([, l]) => l).join('、') || '—'
}

function Developer() {
  const sid = useSession((s) => s.studioId)
  const keys = useQuery<any[]>({ queryKey: ['api-keys', sid], queryFn: () => api.get(`/studios/${sid}/api-keys`) })
  const hooks = useQuery({ queryKey: ['webhooks', sid], queryFn: () => api.get(`/studios/${sid}/webhooks`) })
  const [newKey, setNewKey] = useState<any>(null)
  const [secret, setSecret] = useState('')
  const [hook, setHook] = useState<any>(null)
  return (
    <div className="space-y-6">
      <Panel title="API 密钥" description={<>用程序批量导入书稿、发起一键成书和下载成品。<a href="/openapi/v1/docs" target="_blank" rel="noreferrer" className="text-accent underline underline-offset-2">接口文档</a> · <a href="/openapi/v1/openapi.yaml" target="_blank" rel="noreferrer" className="text-accent underline underline-offset-2">OpenAPI 描述文件</a>（可导入 Postman、Apifox）</>} padded={false} actions={<Button size="sm" variant="primary" icon={<Plus className="size-4" />} onClick={() => { setSecret(''); setNewKey({ name: '', scopes: ['read', 'produce'], rateLimit: 60, ips: '' }) }}>创建密钥</Button>}>
        {!(keys.data ?? []).length ? <Empty title="还没有 API 密钥" className="py-10" /> : (
          <Table>
            <thead><tr><th>名称</th><th>前缀</th><th>权限</th><th className="!text-right">调用</th><th>最近使用</th><th /></tr></thead>
            <tbody>{keys.data!.map((k) => <tr key={k.id} className={cn(k.disabled && 'opacity-50')}><td className="font-medium">{k.name}</td><td className="font-mono text-xs">{k.prefix}…</td><td>{scopeLabels(k.scopes)}</td><td className="text-right tnum">{num(k.calls)}</td><td className="text-xs text-faint">{fromNow(k.lastUsedAt)}</td><td className="whitespace-nowrap text-right"><Switch checked={!k.disabled} onChange={async (v) => { await api.patch(`/studios/${sid}/api-keys/${k.id}`, { disabled: !v }); invalidate(['api-keys']) }} /><IconButton label="删除" size="xs" className="ml-2" icon={<Trash2 className="size-3.5" />} onClick={async () => { if (await confirm({ title: '删除这个密钥？', description: '使用它的程序会立即无法访问。', danger: true, confirmText: '删除' })) { await api.del(`/studios/${sid}/api-keys/${k.id}`); invalidate(['api-keys']) } }} /></td></tr>)}</tbody>
          </Table>
        )}
      </Panel>
      <Panel title="Webhook 通知" description="任务完成、章节合成、音色就绪时向你的服务器发送通知，请求头带 HMAC 签名。" padded={false} actions={<Button size="sm" icon={<Plus className="size-4" />} onClick={() => setHook({ url: '', events: hooks.data?.events ?? [], note: '' })}>添加</Button>}>
        {!hooks.data?.items?.length ? <Empty title="还没有 Webhook" className="py-10" /> : (
          <ul>{hooks.data.items.map((h: any) => (
            <li key={h.id} className="flex flex-wrap items-center gap-3 border-t border-line px-5 py-3 first:border-t-0">
              <div className="min-w-0 flex-1"><div className="truncate font-mono text-[12.5px]">{h.url}</div><div className="mt-0.5 text-xs text-faint">{(h.events ?? []).join('、')}</div></div>
              <Button size="xs" variant="ghost" onClick={async () => { await api.post(`/studios/${sid}/webhooks/${h.id}/test`); toast.success('已发送测试事件') }}>发送测试</Button>
              <Switch checked={h.enabled} onChange={async (v) => { await api.patch(`/studios/${sid}/webhooks/${h.id}`, { enabled: v }); invalidate(['webhooks']) }} />
              <IconButton label="删除" size="xs" icon={<Trash2 className="size-3.5" />} onClick={async () => { await api.del(`/studios/${sid}/webhooks/${h.id}`); invalidate(['webhooks']) }} />
            </li>
          ))}</ul>
        )}
      </Panel>
      <Dialog open={!!newKey} onOpenChange={(o) => !o && setNewKey(null)} title="创建 API 密钥" width={480} footer={secret ? <Button variant="primary" onClick={() => setNewKey(null)}>我已保存</Button> : newKey && <><Button onClick={() => setNewKey(null)}>取消</Button><Button variant="primary" disabled={!newKey.name.trim()} onClick={async () => { const r = await api.post(`/studios/${sid}/api-keys`, { name: newKey.name, scopes: newKey.scopes, rateLimit: newKey.rateLimit, ipAllowlist: newKey.ips.split(/[\s,]+/).filter(Boolean) }); setSecret(r.secret); invalidate(['api-keys']) }}>创建</Button></>}>
        {secret ? (
          <div className="space-y-3">
            <p className="text-[13px] text-warn">密钥只显示这一次，请立即复制保存。</p>
            <Copyable text={secret} />
          </div>
        ) : newKey && (
          <div className="space-y-4">
            <Field label="名称"><Input value={newKey.name} onChange={(e) => setNewKey({ ...newKey, name: e.target.value })} placeholder="例如：内容管理系统" /></Field>
            <Field label="权限">
              <div className="space-y-2">
                {SCOPES.map(([k, l, hint]) => (
                  <div key={k}>
                    <Checkbox checked={newKey.scopes.includes(k)} onChange={(v) => setNewKey({ ...newKey, scopes: v ? [...newKey.scopes, k] : newKey.scopes.filter((x: string) => x !== k) })} label={<>{l} <span className="font-mono text-xs text-faint">{k}</span></>} />
                    <p className="pl-6 text-xs text-muted">{hint}</p>
                  </div>
                ))}
              </div>
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label="每分钟请求上限"><Input type="number" value={newKey.rateLimit} onChange={(e) => setNewKey({ ...newKey, rateLimit: Number(e.target.value) })} /></Field>
              <Field label="IP 白名单（可选）"><Input value={newKey.ips} onChange={(e) => setNewKey({ ...newKey, ips: e.target.value })} placeholder="逗号分隔" /></Field>
            </div>
          </div>
        )}
      </Dialog>
      <Dialog open={!!hook} onOpenChange={(o) => !o && setHook(null)} title="添加 Webhook" width={480} footer={hook && <><Button onClick={() => setHook(null)}>取消</Button><Button variant="primary" disabled={!hook.url.startsWith('http')} onClick={async () => { const r = await api.post(`/studios/${sid}/webhooks`, hook); invalidate(['webhooks']); setHook(null); if (r?.secret) toast.success('已添加', { description: `签名密钥：${r.secret}` }) }}>添加</Button></>}>
        {hook && (
          <div className="space-y-4">
            <Field label="地址"><Input value={hook.url} onChange={(e) => setHook({ ...hook, url: e.target.value })} placeholder="https://example.com/hooks/voicebook" /></Field>
            <Field label="订阅事件"><div className="grid grid-cols-2 gap-2">{(hooks.data?.events ?? []).filter((e: string) => e !== 'ping').map((e: string) => <Checkbox key={e} checked={hook.events.includes(e)} onChange={(v) => setHook({ ...hook, events: v ? [...hook.events, e] : hook.events.filter((x: string) => x !== e) })} label={<span className="font-mono text-xs">{e}</span>} />)}</div></Field>
          </div>
        )}
      </Dialog>
    </div>
  )
}
