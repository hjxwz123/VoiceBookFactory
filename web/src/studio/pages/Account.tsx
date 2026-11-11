import { useQuery } from '@tanstack/react-query'
import { Monitor, Moon, Sun, Trash2 } from '@/components/icons'
import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { dateTime, fromNow } from '@/lib/format'
import { useSession } from '@/lib/session'
import { useTheme } from '@/lib/theme'
import { LegalLinks } from '@/components/legal'
import { ProviderMark, type OAuthOption } from './Auth'
import { Avatar, Badge, Button, confirm, Dialog, Field, IconButton, Input, PageHeader, Panel, Segmented, Switch, Textarea } from '@/components/ui'

const notifyEvents: [string, string][] = [['job_done', 'AI 任务完成'], ['task_assigned', '被分配生产任务'], ['issue', '收到返修工单'], ['comment', '评论中提到我'], ['role_assigned', '被指派配音角色'], ['credits_low', '积分余额不足']]

export default function Account() {
  const { user, load } = useSession()
  const { theme, setTheme } = useTheme()
  const site = useSession((s) => s.site) ?? {}
  const [f, setF] = useState({ nickname: user?.nickname ?? '', bio: user?.bio ?? '' })
  const [cv, setCv] = useState<any>(user?.cv ?? { enabled: false, tags: [], intro: '', works: [] })
  const [pwd, setPwd] = useState({ old: '', next: '' })
  const [phone, setPhone] = useState({ phone: '', code: '', sent: false })
  const [del, setDel] = useState(false)
  const [delPwd, setDelPwd] = useState('')
  const sessions = useQuery<any[]>({ queryKey: ['sessions'], queryFn: () => api.get('/me/sessions') })
  const identities = useQuery<any[]>({ queryKey: ['identities'], queryFn: () => api.get('/me/identities') })
  useEffect(() => { if (user) { setF({ nickname: user.nickname, bio: user.bio }); setCv(user.cv ?? cv) } }, [user?.id])
  // coming back from linking a third-party account
  const [params, setParams] = useSearchParams()
  useEffect(() => {
    const err = params.get('oauth_error')
    if (params.get('oauth') === 'bound') toast.success('绑定成功，以后可以用它直接登录')
    else if (err) toast.error(err, { duration: 8000 })
    else return
    identities.refetch()
    setParams({}, { replace: true })
  }, [])
  const notify = user?.preferences?.notify ?? {}
  const savePrefs = async (p: any) => { await api.patch('/me', { preferences: { ...user!.preferences, ...p } }); load() }

  return (
    <div className="mx-auto max-w-3xl">
      <PageHeader title="账户设置" />
      <div className="space-y-6">
        <Panel title="个人资料">
          <div className="flex items-center gap-4">
            <Avatar name={user?.nickname} src={user?.avatarUrl} size={56} />
            <label className="cursor-pointer text-[13px] text-accent">
              <input type="file" accept="image/*" className="hidden" onChange={async (e) => { const file = e.target.files?.[0]; if (!file) return; await api.upload('/me/avatar', file); load(); toast.success('头像已更新') }} />
              更换头像
            </label>
          </div>
          <div className="mt-5 grid gap-4">
            <Field label="昵称"><Input value={f.nickname} onChange={(e) => setF({ ...f, nickname: e.target.value })} /></Field>
            <Field label="简介"><Textarea rows={2} value={f.bio} onChange={(e) => setF({ ...f, bio: e.target.value })} /></Field>
            <div className="flex justify-end"><Button variant="primary" onClick={async () => { await api.patch('/me', f); load(); toast.success('已保存') }}>保存</Button></div>
          </div>
        </Panel>

        <Panel title="配音演员名片" description="打开后，制作人在为角色指派真人配音时可以看到你的声线标签和代表作。">
          <Switch checked={cv.enabled} onChange={(v) => setCv({ ...cv, enabled: v })} label="公开我的配音演员名片" />
          {cv.enabled && (
            <div className="mt-4 grid gap-4">
              <Field label="声线标签" hint="用顿号分隔，如：少年音、温柔、御姐"><Input value={(cv.tags ?? []).join('、')} onChange={(e) => setCv({ ...cv, tags: e.target.value.split(/[、,，]+/).filter(Boolean) })} /></Field>
              <Field label="自我介绍"><Textarea rows={3} value={cv.intro} onChange={(e) => setCv({ ...cv, intro: e.target.value })} /></Field>
              <Field label="代表作品" hint="每行一个"><Textarea rows={3} value={(cv.works ?? []).join('\n')} onChange={(e) => setCv({ ...cv, works: e.target.value.split('\n').filter(Boolean) })} /></Field>
            </div>
          )}
          <div className="mt-4 flex justify-end"><Button onClick={async () => { await api.patch('/me', { cv }); load(); toast.success('已保存') }}>保存名片</Button></div>
        </Panel>

        <Panel title="外观">
          <Segmented value={theme} onChange={setTheme} items={[{ value: 'light', label: <><Sun className="size-3.5" />浅色</> }, { value: 'dark', label: <><Moon className="size-3.5" />深色</> }, { value: 'system', label: <><Monitor className="size-3.5" />跟随系统</> }]} />
        </Panel>

        <Panel title="邮件通知" description="站内通知始终开启，这里设置哪些事件同时发送邮件。">
          <div className="grid gap-3 sm:grid-cols-2">
            {notifyEvents.map(([k, l]) => <Switch key={k} checked={notify[k] !== false} onChange={(v) => savePrefs({ notify: { ...notify, [k]: v } })} label={l} />)}
          </div>
        </Panel>

        <Panel title="登录与安全">
          <div className="space-y-5">
            <div className="flex items-center justify-between text-[13.5px]"><span>邮箱</span><span className="text-muted">{user?.email || '未绑定'}{user?.emailVerified && <Badge tone="ok" className="ml-2">已验证</Badge>}</span></div>
            {site.phoneLogin && (
              <div>
                <div className="flex items-center justify-between text-[13.5px]"><span>手机号</span><span className="text-muted">{user?.phone || '未绑定'}</span></div>
                <div className="mt-2 flex gap-2">
                  <Input value={phone.phone} onChange={(e) => setPhone({ ...phone, phone: e.target.value })} placeholder="新手机号" />
                  {phone.sent && <Input value={phone.code} onChange={(e) => setPhone({ ...phone, code: e.target.value })} placeholder="验证码" className="w-28" />}
                  {!phone.sent ? (
                    <Button disabled={!phone.phone} onClick={async () => { const r = await api.post('/auth/phone/code', { phone: phone.phone, purpose: 'bind' }); setPhone({ ...phone, sent: true, code: r.devCode ?? '' }); toast.success('验证码已发送') }}>获取验证码</Button>
                  ) : (
                    <Button variant="primary" onClick={async () => { await api.post('/me/phone', { phone: phone.phone, code: phone.code }); setPhone({ phone: '', code: '', sent: false }); load(); toast.success('手机号已绑定') }}>绑定</Button>
                  )}
                </div>
              </div>
            )}
            <div className="border-t border-line pt-5">
              <div className="mb-2 text-[13.5px]">{user?.hasPassword ? '修改密码' : '设置登录密码'}</div>
              <div className="flex flex-wrap gap-2">
                {user?.hasPassword && <Input type="password" placeholder="当前密码" value={pwd.old} onChange={(e) => setPwd({ ...pwd, old: e.target.value })} className="w-48" />}
                <Input type="password" placeholder="新密码（至少 8 位，含字母和数字）" value={pwd.next} onChange={(e) => setPwd({ ...pwd, next: e.target.value })} className="w-72" />
                <Button disabled={!pwd.next} onClick={async () => {
                  if (user?.hasPassword) await api.post('/me/password', { old: pwd.old, new: pwd.next })
                  else await api.post('/me/password/set', { new: pwd.next })
                  setPwd({ old: '', next: '' })
                  load()
                  toast.success('密码已更新')
                }}>保存</Button>
              </div>
            </div>
            {((identities.data ?? []).length > 0 || (site.oauthProviders ?? []).length > 0) && (
              <div className="border-t border-line pt-5">
                <div className="mb-1 text-[13.5px]">第三方账号</div>
                <p className="mb-3 text-xs text-muted">绑定后可以直接用这些账号登录。</p>
                <ul className="divide-y divide-line rounded-md border border-line">
                  {(identities.data ?? []).map((i) => (
                    <li key={i.id} className="flex items-center gap-3 px-3 py-2.5 text-[13px]">
                      <ProviderMark preset={(site.oauthProviders ?? []).find((p: OAuthOption) => p.id === i.provider)?.preset ?? i.provider} />
                      <span className="min-w-0 flex-1 truncate">{i.providerName}<span className="text-muted"> · {i.name || i.email || '已绑定'}</span></span>
                      <span className="hidden text-xs text-faint sm:inline">{i.lastLoginAt ? `最近登录 ${fromNow(i.lastLoginAt)}` : `绑定于 ${dateTime(i.createdAt)}`}</span>
                      <Button size="xs" variant="ghost" onClick={async () => {
                        if (!(await confirm({ title: `解除绑定 ${i.providerName}？`, description: '解除后不能再用它登录这个账号，之后可以重新绑定。' }))) return
                        await api.del(`/me/identities/${i.id}`)
                        identities.refetch()
                        toast.success('已解除绑定')
                      }}>解除绑定</Button>
                    </li>
                  ))}
                  {(site.oauthProviders ?? []).filter((p: OAuthOption) => !(identities.data ?? []).some((i) => i.provider === p.id)).map((p: OAuthOption) => (
                    <li key={p.id} className="flex items-center gap-3 px-3 py-2.5 text-[13px]">
                      <ProviderMark preset={p.preset} />
                      <span className="min-w-0 flex-1 truncate">{p.name}<span className="text-faint"> · 未绑定</span></span>
                      <Button size="xs" onClick={() => { window.location.href = `/api/v1/auth/oauth/${p.id}/start?mode=bind&redirect=${encodeURIComponent('/settings/account')}` }}>绑定</Button>
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </div>
        </Panel>

        <Panel title="登录设备" padded={false}>
          <ul>
            {(sessions.data ?? []).map((s) => (
              <li key={s.id} className="flex items-center gap-3 border-t border-line px-5 py-3 text-[13px] first:border-t-0">
                <div className="min-w-0 flex-1"><div className="truncate">{s.userAgent || '未知设备'}{s.current && <Badge tone="accent" className="ml-2">当前</Badge>}</div><div className="text-xs text-faint">{s.ip} · 最近活动 {fromNow(s.lastSeenAt ?? s.updatedAt)}</div></div>
                {!s.current && <IconButton label="退出这台设备" size="xs" icon={<Trash2 className="size-3.5" />} onClick={async () => { await api.del(`/me/sessions/${s.id}`); sessions.refetch() }} />}
              </li>
            ))}
          </ul>
        </Panel>

        <Panel title="协议与政策">
          <div className="flex flex-wrap items-center justify-between gap-3 text-[13px] text-muted">
            <span>{user?.legalAgreedAt ? `你于 ${dateTime(user.legalAgreedAt)} 同意了当前版本的协议。` : '平台协议与政策：'}</span>
            <LegalLinks all />
          </div>
        </Panel>

        <Panel title="注销账号">
          {user?.deletionAt ? (
            <div className="flex items-center justify-between text-[13px]"><span className="text-warn">账号将在 {dateTime(user.deletionAt)} 注销。</span><Button onClick={async () => { await api.post('/me/delete/cancel'); load(); toast.success('已撤销注销') }}>撤销注销</Button></div>
          ) : (
            <div className="flex items-center justify-between gap-4 text-[13px] text-muted"><span>注销后有 7 天冷静期，期间重新登录或点「撤销注销」即可恢复。之后个人工作室的图书、音色和积分会被永久删除。</span><Button variant="ghost" className="shrink-0 text-danger" onClick={() => setDel(true)}>申请注销</Button></div>
          )}
        </Panel>
      </div>
      <Dialog open={del} onOpenChange={setDel} title="确认注销账号？" width={420} footer={<><Button onClick={() => setDel(false)}>取消</Button><Button variant="danger" onClick={async () => {
        if (!(await confirm({ title: '最后确认', description: '7 天后数据会被永久删除。', danger: true, confirmText: '确认注销' }))) return
        await api.post('/me/delete', { password: delPwd })
        setDel(false)
        load()
      }}>注销</Button></>}>
        <Field label="输入登录密码确认"><Input type="password" value={delPwd} onChange={(e) => setDelPwd(e.target.value)} /></Field>
      </Dialog>
    </div>
  )
}
