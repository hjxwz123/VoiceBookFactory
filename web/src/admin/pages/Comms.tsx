import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { adminApi } from '@/lib/api'
import dayjs from 'dayjs'
import { dateTime, fromNow } from '@/lib/format'
import { Badge, Button, Checkbox, cn, confirm, DateInput, Dialog, Empty, Field, Input, PageHeader, Pagination, Panel, Segmented, Sheet, Skeleton, Switch, Table, Textarea } from '@/components/ui'
import { Eye, Megaphone, Plus, Trash2 } from '@/components/icons'
import { AnnouncementBody, AnnouncementDialog } from '@/components/announcement'
import { ainv, CrudPage, useAdmin } from '../kit'

const kindName: Record<string, string> = { banner: '横幅', modal: '弹窗' }
const formatName: Record<string, string> = { text: '纯文本', markdown: 'Markdown', html: 'HTML' }
const levelName: Record<string, [string, any]> = { '': ['普通', 'neutral'], warning: ['重要', 'warn'], danger: ['紧急', 'danger'] }
// ISO time → the "YYYY-MM-DDTHH:mm" the date picker uses, in local time
const localInput = (t?: string | null) => (t ? dayjs(t).format('YYYY-MM-DDTHH:mm') : '')

function annState(a: any): [string, any] {
  const now = Date.now()
  if (!a.published) return ['草稿', 'neutral']
  if (a.endsAt && new Date(a.endsAt).getTime() < now) return ['已结束', 'neutral']
  if (a.startsAt && new Date(a.startsAt).getTime() > now) return ['未开始', 'accent']
  return ['展示中', 'ok']
}

export function Announcements() {
  const [page, setPage] = useState(1)
  const [edit, setEdit] = useState<any>(null)
  const { data, refetch } = useAdmin<any>('/announcements', { page, pageSize: 30 })
  const items: any[] = Array.isArray(data) ? data : data?.items ?? []
  return (
    <div className="space-y-5">
      <PageHeader title="公告" description="横幅显示在用户工作台顶部；弹窗在用户打开任意页面时居中弹出，用户关闭后就不再出现（换设备也不会再弹）。内容可以用纯文本、Markdown 或 HTML。"
        actions={<Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEdit({ kind: 'modal', format: 'markdown', level: '', published: false, audience: {}, title: '', body: '', link: '', linkText: '', width: 0 })}>新建公告</Button>} />
      <Panel padded={false}>
        {!data ? <div className="p-4"><Skeleton className="h-32" /></div> : !items.length ? <Empty icon={<Megaphone />} title="还没有公告" /> : (
          <Table>
            <thead><tr><th>标题</th><th>形式</th><th>格式</th><th>级别</th><th>时间</th><th>状态</th><th /></tr></thead>
            <tbody>
              {items.map((a) => {
                const [st, tone] = annState(a)
                return (
                  <tr key={a.id}>
                    <td className="font-medium">{a.title}</td>
                    <td>{kindName[a.kind] ?? a.kind}</td>
                    <td className="text-muted">{formatName[a.format || 'text']}</td>
                    <td><Badge tone={levelName[a.level ?? '']?.[1]}>{levelName[a.level ?? '']?.[0] ?? a.level}</Badge></td>
                    <td className="text-[12px] text-muted">{a.startsAt ? dateTime(a.startsAt) : '立即'} — {a.endsAt ? dateTime(a.endsAt) : '长期'}</td>
                    <td><Badge tone={tone}>{st}</Badge></td>
                    <td className="whitespace-nowrap text-right">
                      <Button size="xs" variant="ghost" onClick={() => setEdit({ ...a })}>编辑</Button>
                      <Button size="xs" variant="ghost" icon={<Trash2 className="size-3.5" />} aria-label="删除" onClick={async () => {
                        if (!(await confirm({ title: `删除「${a.title}」？`, danger: true, confirmText: '删除' }))) return
                        await adminApi.del(`/announcements/${a.id}`)
                        refetch()
                        toast.success('已删除')
                      }} />
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </Table>
        )}
      </Panel>
      {data && !Array.isArray(data) && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
      <AnnouncementEditor value={edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); refetch() }} />
    </div>
  )
}

const bodyPlaceholder: Record<string, string> = {
  text: '直接输入要说的话',
  markdown: '支持 **加粗**、[链接](https://…)、列表、图片 ![](https://…) 等',
  html: '<p>支持常用 HTML，例如 <b>加粗</b>、<a href="https://…">链接</a>、<img src="https://…">。脚本、表单和内嵌网页会被去掉。</p>',
}

function AnnouncementEditor({ value, onClose, onSaved }: { value: any; onClose: () => void; onSaved: () => void }) {
  const [f, setF] = useState<any>(null)
  const [busy, setBusy] = useState(false)
  const [preview, setPreview] = useState(false)
  const plans = useAdmin<any>('/plans', { pageSize: 100 }, { enabled: !!value })
  useEffect(() => {
    if (value) setF({ ...value, format: value.format || 'text', startsAt: localInput(value.startsAt), endsAt: localInput(value.endsAt), planCodes: (value.audience?.planCodes ?? []) as string[] })
  }, [value])
  if (!value || !f) return null
  const set = (k: string, v: any) => setF((x: any) => ({ ...x, [k]: v }))
  const save = async () => {
    setBusy(true)
    try {
      const { planCodes, ...rest } = f
      const body = { ...rest, startsAt: f.startsAt ? new Date(f.startsAt).toISOString() : null, endsAt: f.endsAt ? new Date(f.endsAt).toISOString() : null, audience: { ...(value.audience ?? {}), planCodes } }
      if (value.id) await adminApi.put(`/announcements/${value.id}`, body)
      else await adminApi.post('/announcements', body)
      ainv(['/announcements'])
      toast.success(f.published ? '已保存并发布' : '已保存为草稿')
      onSaved()
    } catch (e: any) {
      toast.error(e.message)
    } finally {
      setBusy(false)
    }
  }
  const planList: any[] = plans.data?.items ?? []
  return (
    <Sheet open onOpenChange={(o) => !o && onClose()} title={value.id ? '编辑公告' : '新建公告'} width={720}
      footer={<><Button onClick={onClose}>取消</Button>{f.kind === 'modal' && <Button icon={<Eye className="size-4" />} onClick={() => setPreview(true)}>预览弹窗</Button>}<Button variant="primary" loading={busy} disabled={!f.title.trim()} onClick={save}>{f.published ? '保存并发布' : '保存草稿'}</Button></>}>
      <div className="space-y-5">
        <Field label="标题"><Input value={f.title} onChange={(e) => set('title', e.target.value)} maxLength={200} placeholder="例如 新音色上线、系统维护通知" /></Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="形式" hint={f.kind === 'modal' ? '打开任意页面时居中弹出，关闭后不再出现' : '显示在工作台顶部，可以关闭'}>
            <Segmented value={f.kind} onChange={(v) => set('kind', v)} items={[{ value: 'modal', label: '弹窗' }, { value: 'banner', label: '横幅' }]} />
          </Field>
          <Field label="级别"><Segmented value={f.level ?? ''} onChange={(v) => set('level', v)} items={[{ value: '', label: '普通' }, { value: 'warning', label: '重要' }, { value: 'danger', label: '紧急' }]} /></Field>
        </div>
        <Field label="内容">
          <Segmented size="sm" className="mb-2" value={f.format} onChange={(v) => set('format', v)} items={[{ value: 'markdown', label: 'Markdown' }, { value: 'html', label: 'HTML' }, { value: 'text', label: '纯文本' }]} />
          <Textarea rows={f.kind === 'modal' ? 9 : 4} value={f.body} onChange={(e) => set('body', e.target.value)} placeholder={bodyPlaceholder[f.format]} className={cn(f.format !== 'text' && 'font-mono text-[12.5px]')} />
        </Field>
        {f.body.trim() && (
          <div>
            <div className="mb-1.5 text-xs text-muted">预览</div>
            <div className="rounded-[12px] border border-line bg-surface px-5 py-4">
              {f.kind === 'banner' ? <div className="text-[13px]"><span className="font-medium">{f.title}</span><AnnouncementBody a={f} inline className="ml-2 text-muted" /></div> : <AnnouncementBody a={f} className="text-[14px] leading-relaxed" />}
            </div>
          </div>
        )}
        <div className="grid gap-4 sm:grid-cols-[1fr_160px]">
          <Field label="链接（选填）" hint="站内页面写路径，如 /membership；站外写完整网址"><Input value={f.link ?? ''} onChange={(e) => set('link', e.target.value)} placeholder="/membership 或 https://…" /></Field>
          <Field label="按钮文字"><Input value={f.linkText ?? ''} maxLength={12} onChange={(e) => set('linkText', e.target.value)} placeholder="查看详情" /></Field>
        </div>
        {f.kind === 'modal' && (
          <Field label="弹窗宽度"><Segmented size="sm" value={String(f.width || 0)} onChange={(v) => set('width', Number(v))} items={[{ value: '0', label: '标准' }, { value: '440', label: '窄' }, { value: '720', label: '宽' }]} /></Field>
        )}
        <Field label="面向哪些会员" hint="不选表示所有用户">
          <div className="flex flex-wrap gap-1.5">
            {planList.map((p) => {
              const on = f.planCodes.includes(p.code)
              return <button key={p.id} onClick={() => set('planCodes', on ? f.planCodes.filter((c: string) => c !== p.code) : [...f.planCodes, p.code])} className={cn('h-8 rounded-control border px-2.5 text-[12.5px] transition-colors', on ? 'border-accent bg-accent-soft text-accent' : 'border-line hover:border-line-strong')}>{p.name}</button>
            })}
          </div>
        </Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="开始时间"><DateInput time value={f.startsAt} onChange={(v) => set('startsAt', v)} placeholder="立即开始" /></Field>
          <Field label="结束时间"><DateInput time value={f.endsAt} onChange={(v) => set('endsAt', v)} placeholder="一直展示" /></Field>
        </div>
        <Switch checked={!!f.published} onChange={(v) => set('published', v)} label="发布（关闭则保存为草稿，用户看不到）" />
      </div>
      <AnnouncementDialog a={{ ...f, id: 'preview' }} open={preview} onClose={() => setPreview(false)} preview />
    </Sheet>
  )
}

export function Articles() {
  return (
    <CrudPage
      title="帮助文章"
      description="显示在用户端「帮助中心」。正文支持 Markdown。"
      path="/articles"
      filters={[{ key: 'kind', label: '类型', options: [['help', '使用指南'], ['changelog', '更新日志'], ['legal', '协议与政策']] }]}
      columns={[
        { key: 'title', label: '标题', render: (r: any) => <span className="font-medium">{r.title}</span> },
        { key: 'kind', label: '类型', render: (r: any) => (r.kind === 'help' ? '使用指南' : r.kind === 'legal' ? '协议与政策' : '更新日志') },
        { key: 'category', label: '分类' },
        { key: 'sortOrder', label: '排序', align: 'right' },
        { key: 'published', label: '状态', render: (r: any) => (r.published ? <Badge tone="ok">已发布</Badge> : <Badge>草稿</Badge>) },
      ]}
      fields={[
        { key: 'title', label: '标题', span: 2 },
        { key: 'kind', label: '类型', type: 'select', options: [['help', '使用指南'], ['changelog', '更新日志']] },
        { key: 'category', label: '分类', placeholder: '入门、画本、配音……' },
        { key: 'version', label: '版本号（更新日志）' },
        { key: 'sortOrder', label: '排序', type: 'number' },
        { key: 'summary', label: '摘要', type: 'textarea' },
        { key: 'body', label: '正文（Markdown）', type: 'markdown' },
        { key: 'featured', label: '推荐', type: 'switch' },
        { key: 'published', label: '发布', type: 'switch' },
      ]}
      defaults={{ kind: 'help', sortOrder: 0, published: true, featured: false }}
    />
  )
}

export function Feedback() {
  const [status, setStatus] = useState('open')
  const [page, setPage] = useState(1)
  const [reply, setReply] = useState<any>(null)
  const [text, setText] = useState('')
  const { data } = useAdmin<any>('/feedback', { status, page, pageSize: 30 })
  return (
    <div>
      <PageHeader title="用户反馈" description="回复会以站内通知发送给用户。" />
      <Segmented className="mb-4" size="sm" value={status} onChange={(v) => { setStatus(v); setPage(1) }} items={[{ value: 'open', label: '待处理' }, { value: 'replied', label: '已回复' }, { value: '', label: '全部' }]} />
      <Panel padded={false}>
        {!data?.items?.length ? <Empty title="没有反馈" /> : (
          <ul>
            {data.items.map((f: any) => (
              <li key={f.id} className="border-t border-line px-5 py-4 first:border-t-0">
                <div className="flex items-center gap-2 text-xs text-faint"><Badge>{f.kind === 'bug' ? '问题' : f.kind === 'question' ? '咨询' : '建议'}</Badge>{fromNow(f.createdAt)}{f.page && <span className="font-mono">{f.page}</span>}{f.contact && <span>联系方式：{f.contact}</span>}</div>
                <p className="mt-2 text-[13.5px]">{f.content}</p>
                {f.reply ? <p className="mt-2 rounded-md bg-accent-soft px-3 py-2 text-[13px] text-accent">已回复：{f.reply}</p> : <Button size="xs" className="mt-2" onClick={() => { setReply(f); setText('') }}>回复</Button>}
              </li>
            ))}
          </ul>
        )}
      </Panel>
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
      <Dialog open={!!reply} onOpenChange={(o) => !o && setReply(null)} title="回复反馈" width={460} footer={<><Button onClick={() => setReply(null)}>取消</Button><Button variant="primary" disabled={!text.trim()} onClick={async () => { await adminApi.post(`/feedback/${reply.id}/reply`, { reply: text }); ainv(['/feedback']); setReply(null); toast.success('已回复') }}>发送</Button></>}>
        <p className="mb-3 rounded-md bg-sunken px-3 py-2 text-[13px] text-muted">{reply?.content}</p>
        <Textarea rows={4} value={text} onChange={(e) => setText(e.target.value)} />
      </Dialog>
    </div>
  )
}

export function Messages() {
  const plans = useAdmin<any>('/plans', { pageSize: 50 })
  const initial = () => ({ title: '', body: '', link: '', to: (new URLSearchParams(location.search).get('to') ? 'users' : 'all') as 'all' | 'plans' | 'users', planCodes: [] as string[], recipients: new URLSearchParams(location.search).get('to') ?? '', email: false })
  const [f, setF] = useState(initial)
  const [busy, setBusy] = useState(false)
  const [check, setCheck] = useState<any>(null)
  const body = () => ({ title: f.title, body: f.body, link: f.link, email: f.email, planCodes: f.to === 'plans' ? f.planCodes : [], recipients: f.to === 'users' ? [f.recipients] : [] })
  // who would get it, checked as the administrator types
  useEffect(() => {
    if (f.to === 'users' && !f.recipients.trim()) return setCheck(null)
    if (f.to === 'plans' && !f.planCodes.length) return setCheck(null)
    const t = setTimeout(() => adminApi.post('/messages', { ...body(), dryRun: true }).then(setCheck).catch(() => setCheck(null)), 400)
    return () => clearTimeout(t)
  }, [f.to, f.recipients, f.planCodes.join(',')])
  const ready = !!f.title.trim() && !!check && check.recipients > 0 && !(check.unknown?.length)
  const send = async () => {
    const who = f.to === 'all' ? `全部 ${check.recipients} 位用户` : `${check.recipients} 位用户`
    if (!(await confirm({ title: `发送给${who}？`, description: f.email && check.smtp ? `站内信之外，还会给其中有邮箱的 ${check.withEmail} 位发邮件。发出后不能撤回。` : '发出后不能撤回。', confirmText: '发送', danger: f.to === 'all' }))) return
    setBusy(true)
    try {
      const r = await adminApi.post('/messages', body())
      toast.success(`已发送给 ${r.recipients} 位用户`, { description: f.email && r.smtp ? `邮件正在后台陆续发出（${r.withEmail} 封）。` : undefined })
      setF({ ...initial(), to: f.to })
      setCheck(null)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="mx-auto max-w-2xl">
      <PageHeader title="站内信" description="向用户发送站内通知，也可以同时发邮件。" />
      <Panel>
        <div className="space-y-5">
          <Field label="标题"><Input value={f.title} onChange={(e) => setF({ ...f, title: e.target.value })} /></Field>
          <Field label="内容"><Textarea rows={5} value={f.body} onChange={(e) => setF({ ...f, body: e.target.value })} /></Field>
          <Field label="跳转链接（可选）" hint="站内路径，如 /voices"><Input value={f.link} onChange={(e) => setF({ ...f, link: e.target.value })} /></Field>
          <div className="space-y-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="text-[13px] font-medium">发送给</span>
              <Segmented size="sm" value={f.to} onChange={(v) => setF({ ...f, to: v })} items={[{ value: 'users', label: '指定用户' }, { value: 'plans', label: '按会员套餐' }, { value: 'all', label: '全部用户' }]} />
            </div>
            {f.to === 'users' && (
              <Field hint="填邮箱、手机号或用户 ID 都可以，每行一个，也可以用逗号分隔">
                <Textarea rows={4} value={f.recipients} onChange={(e) => setF({ ...f, recipients: e.target.value })} placeholder={'zhang@example.com\n13800138000'} className="font-mono text-xs" />
              </Field>
            )}
            {f.to === 'plans' && (
              <div className="flex flex-wrap gap-x-5 gap-y-2 rounded-[10px] bg-sunken/60 px-3 py-2.5">{(plans.data?.items ?? []).map((p: any) => <Checkbox key={p.code} checked={f.planCodes.includes(p.code)} onChange={(v) => setF({ ...f, planCodes: v ? [...f.planCodes, p.code] : f.planCodes.filter((x) => x !== p.code) })} label={p.name} />)}</div>
            )}
            {check && (
              <div className="space-y-2 rounded-[10px] border border-line px-3 py-2.5 text-[13px]">
                <div>{f.to === 'all' ? '将发送给全部用户：' : '将发送给：'}<span className="font-medium tnum">{check.recipients}</span> 位{check.smtp && <span className="text-muted">，其中 {check.withEmail} 位有邮箱</span>}</div>
                {f.to === 'users' && check.found?.length > 0 && (
                  <div className="flex flex-wrap gap-1.5">{check.found.slice(0, 30).map((u: any) => <Badge key={u.id}>{u.nickname || '未命名'}{(u.email || u.phone) && <span className="ml-1 font-normal text-faint">{u.email || u.phone}</span>}</Badge>)}{check.found.length > 30 && <span className="text-xs text-faint">等 {check.found.length} 位</span>}</div>
                )}
                {check.unknown?.length > 0 && <p className="text-danger">找不到这些用户，请改正或删掉：{check.unknown.join('、')}</p>}
              </div>
            )}
          </div>
          <div>
            <Switch checked={f.email} onChange={(v) => setF({ ...f, email: v })} disabled={check ? !check.smtp : false} label="同时发送邮件" />
            <p className="mt-1 pl-11 text-xs text-muted">{check && !check.smtp ? '邮件服务还没有配置，请先在「系统设置」里填写 SMTP。' : '给有邮箱的用户再发一封邮件，内容和站内信相同。不选时，只有在个人设置里开启了邮件提醒的用户会收到邮件。'}</p>
          </div>
          <div className="flex justify-end"><Button variant="primary" loading={busy} disabled={!ready} onClick={send}>发送</Button></div>
        </div>
      </Panel>
    </div>
  )
}
