import { Search } from '@/components/icons'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { adminApi } from '@/lib/api'
import { dateTime, fromNow, num, roleName } from '@/lib/format'
import { Avatar, Badge, Button, Checkbox, confirm, Copyable, Dialog, Empty, Field, Input, PageHeader, Pagination, Panel, Segmented, Select, Sheet, Table, Textarea } from '@/components/ui'
import { UserMembership } from './Membership'
import { ainv, ReviewButtons, useAdmin } from '../kit'

const adminRoleNames: Record<string, string> = { super_admin: '超级管理员', operator: '运营', auditor: '审核', finance: '财务', ops: '运维' }

export default function Users() {
  const [q, setQ] = useState('')
  const [status, setStatus] = useState('')
  const [filter, setFilter] = useState('')
  const [page, setPage] = useState(1)
  const [open, setOpen] = useState<string | null>(null)
  const { data } = useAdmin<any>('/users', { q, status, page, pageSize: 30, admins: filter === 'admins' ? 1 : '', realName: filter === 'realname' ? 'pending' : '' })
  return (
    <div>
      <PageHeader title="用户" description="查看和管理所有注册用户，处理实名认证。" />
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <Segmented size="sm" value={filter} onChange={(v) => { setFilter(v); setPage(1) }} items={[{ value: '', label: '全部' }, { value: 'realname', label: '待实名审核' }, { value: 'admins', label: '管理员' }]} />
        <Select size="sm" value={status} onChange={(v) => { setStatus(v); setPage(1) }} options={[['', '全部状态'], ['active', '正常'], ['disabled', '已封禁']]} className="w-28" />
        <div className="ml-auto w-full max-w-xs"><Input icon={<Search />} placeholder="邮箱、手机号或昵称" value={q} onChange={(e) => { setQ(e.target.value); setPage(1) }} /></div>
      </div>
      <Panel padded={false}>
        {!data?.items?.length ? <Empty title="没有用户" /> : (
          <Table>
            <thead><tr><th>用户</th><th>联系方式</th><th className="!text-right">个人积分</th><th>实名</th><th>注册</th><th>最近登录</th><th>状态</th></tr></thead>
            <tbody>
              {data.items.map((u: any) => (
                <tr key={u.id} className="cursor-pointer" onClick={() => setOpen(u.id)}>
                  <td><span className="flex items-center gap-2.5"><Avatar name={u.nickname} src={u.avatarUrl} size={26} /><span className="font-medium">{u.nickname}</span>{u.adminRoles?.length > 0 && <Badge tone="accent">{u.adminRoles.map((r: string) => adminRoleNames[r]).join('、')}</Badge>}</span></td>
                  <td className="text-muted">{u.email || u.phone || '—'}</td>
                  <td className="text-right tnum">{num(u.balance)}</td>
                  <td>{u.realNameStatus === 'approved' ? <Badge tone="ok">已认证</Badge> : u.realNameStatus === 'pending' ? <Badge tone="warn">待审核</Badge> : <span className="text-faint">—</span>}</td>
                  <td className="text-xs text-faint">{dateTime(u.createdAt).slice(0, 10)}</td>
                  <td className="text-xs text-faint">{fromNow(u.lastLoginAt)}</td>
                  <td>{u.status === 'active' ? <Badge tone="ok">正常</Badge> : <Badge tone="danger">已封禁</Badge>}{u.deletionAt && <Badge tone="warn" className="ml-1">注销中</Badge>}</td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Panel>
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
      <UserDetail id={open} onClose={() => setOpen(null)} />
    </div>
  )
}

function UserDetail({ id, onClose }: { id: string | null; onClose: () => void }) {
  const { data, refetch } = useAdmin<any>(`/users/${id}`, {}, { enabled: !!id })
  const [roles, setRoles] = useState<string[] | null>(null)
  const [pw, setPw] = useState('')
  const nav = useNavigate()
  const u = data?.user
  const act = async (body: any, msg: string) => {
    await adminApi.patch(`/users/${id}`, body)
    refetch()
    ainv(['/users'])
    toast.success(msg)
  }
  return (
    <Sheet open={!!id} onOpenChange={(o) => { if (!o) { onClose(); setRoles(null); setPw('') } }} title={u?.nickname} description={u?.email || u?.phone} width={560}>
      {u && (
        <div className="space-y-6">
          <div className="flex flex-wrap gap-2">
            {u.status === 'active' ? (
              <Button variant="danger" size="sm" onClick={async () => { const reason = prompt('封禁原因（会记录到操作日志）'); if (reason) await act({ status: 'disabled', reason }, '已封禁') }}>封禁账号</Button>
            ) : (
              <Button size="sm" onClick={() => act({ status: 'active', reason: '解除封禁' }, '已解封')}>解除封禁</Button>
            )}
            <Button size="sm" onClick={async () => { if (await confirm({ title: '重置密码？', description: '会生成一个临时密码，用户需要用它登录后修改。' })) { const r = await adminApi.post(`/users/${id}/reset-password`); setPw(r.password) } }}>重置密码</Button>
            <Button size="sm" onClick={async () => { await adminApi.post(`/users/${id}/logout`); toast.success('已强制下线所有设备'); refetch() }}>强制下线</Button>
            <Button size="sm" onClick={() => nav(`/messages?to=${encodeURIComponent(u.email || u.phone || u.id)}`)}>发站内信</Button>
          </div>
          {pw && <div className="space-y-1"><p className="text-xs text-muted">临时密码（只显示一次）</p><Copyable text={pw} /></div>}
          {u.realNameStatus === 'pending' && (
            <Panel title="实名认证待审核">
              <p className="text-[13px]">姓名：{u.realName} · 证件号：{u.idNumberMasked}</p>
              <div className="mt-3"><ReviewButtons onDecide={async (approve, note) => { await adminApi.post(`/users/${id}/realname`, { approve, note }); refetch(); ainv(['/users']) }} /></div>
            </Panel>
          )}
          <Panel title="管理员权限" description="仅超级管理员可以修改。">
            <div className="grid grid-cols-2 gap-2.5">
              {Object.entries(adminRoleNames).map(([k, l]) => {
                const cur = roles ?? u.adminRoles ?? []
                return <Checkbox key={k} checked={cur.includes(k)} onChange={(v) => setRoles(v ? [...cur, k] : cur.filter((x: string) => x !== k))} label={l} />
              })}
            </div>
            {roles && <Button size="sm" variant="primary" className="mt-3" onClick={() => act({ adminRoles: roles, reason: '调整管理员权限' }, '权限已更新').then(() => setRoles(null))}>保存权限</Button>}
          </Panel>
          <UserMembership userId={u.id} />
          <Panel title="所属工作室" padded={false}>
            <Table>
              <tbody>{(data.studios ?? []).map((s: any) => <tr key={s.id}><td>{s.name}</td><td className="text-muted">{s.type === 'personal' ? '个人' : '团队'}</td><td>{roleName[s.role]}</td><td className="text-right tnum">{num(s.balance)} 积分</td></tr>)}</tbody>
            </Table>
          </Panel>
          {(data.voices ?? []).length > 0 && <Panel title="个人音色"><div className="flex flex-wrap gap-1.5">{data.voices.map((v: any) => <Badge key={v.id}>{v.name}</Badge>)}</div></Panel>}
          <Panel title="登录记录" padded={false}>
            <Table>
              <tbody>{(data.sessions ?? []).slice(0, 10).map((s: any) => <tr key={s.id}><td className="max-w-56 truncate text-xs">{s.userAgent}</td><td className="text-xs text-muted">{s.ip}</td><td className="text-xs text-faint">{fromNow(s.lastUsedAt ?? s.createdAt)}</td><td>{s.revokedAt ? <Badge>已失效</Badge> : <Badge tone="ok">有效</Badge>}</td></tr>)}</tbody>
            </Table>
          </Panel>
          <div className="text-xs text-faint">注册于 {dateTime(u.createdAt)} · ID {u.id}</div>
        </div>
      )}
    </Sheet>
  )
}

export { Dialog, Field, Textarea }
