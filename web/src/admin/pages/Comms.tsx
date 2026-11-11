import { useState } from 'react'
import { toast } from 'sonner'
import { adminApi } from '@/lib/api'
import { fromNow } from '@/lib/format'
import { Badge, Button, Checkbox, Dialog, Empty, Field, Input, PageHeader, Pagination, Panel, Segmented, Textarea } from '@/components/ui'
import { ainv, CrudPage, useAdmin } from '../kit'

export function Announcements() {
  return (
    <CrudPage
      title="公告"
      description="横幅公告显示在用户工作台顶部，可以设置展示时间段。"
      path="/announcements"
      columns={[
        { key: 'title', label: '标题', render: (r: any) => <span className="font-medium">{r.title}</span> },
        { key: 'kind', label: '形式', render: (r: any) => (r.kind === 'banner' ? '横幅' : r.kind === 'modal' ? '弹窗' : r.kind) },
        { key: 'startsAt', label: '开始' },
        { key: 'endsAt', label: '结束' },
        { key: 'published', label: '发布', render: (r: any) => (r.published ? <Badge tone="ok">已发布</Badge> : <Badge>草稿</Badge>) },
      ]}
      fields={[
        { key: 'title', label: '标题', span: 2 },
        { key: 'body', label: '内容', type: 'textarea' },
        { key: 'kind', label: '形式', type: 'select', options: [['banner', '横幅'], ['modal', '弹窗']] },
        { key: 'level', label: '级别', type: 'select', options: [['', '普通'], ['warning', '重要'], ['danger', '紧急']] },
        { key: 'link', label: '链接', span: 2 },
        { key: 'startsAt', label: '开始时间', type: 'date' },
        { key: 'endsAt', label: '结束时间', type: 'date' },
        { key: 'published', label: '发布', type: 'switch' },
      ]}
      defaults={{ kind: 'banner', level: '', published: false, audience: {} }}
    />
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
  const [f, setF] = useState({ title: '', body: '', link: '', planCodes: [] as string[], userIds: '' })
  const [busy, setBusy] = useState(false)
  return (
    <div className="mx-auto max-w-2xl">
      <PageHeader title="站内信" description="向用户发送站内通知。不选择对象时发送给所有用户。" />
      <Panel>
        <div className="space-y-4">
          <Field label="标题"><Input value={f.title} onChange={(e) => setF({ ...f, title: e.target.value })} /></Field>
          <Field label="内容"><Textarea rows={5} value={f.body} onChange={(e) => setF({ ...f, body: e.target.value })} /></Field>
          <Field label="跳转链接（可选）" hint="站内路径，如 /voices"><Input value={f.link} onChange={(e) => setF({ ...f, link: e.target.value })} /></Field>
          <Field label="按套餐发送">
            <div className="flex flex-wrap gap-4">{(plans.data?.items ?? []).map((p: any) => <Checkbox key={p.code} checked={f.planCodes.includes(p.code)} onChange={(v) => setF({ ...f, planCodes: v ? [...f.planCodes, p.code] : f.planCodes.filter((x) => x !== p.code) })} label={p.name} />)}</div>
          </Field>
          <Field label="或指定用户 ID" hint="每行一个"><Textarea rows={3} value={f.userIds} onChange={(e) => setF({ ...f, userIds: e.target.value })} className="font-mono text-xs" /></Field>
          <div className="flex justify-end"><Button variant="primary" loading={busy} disabled={!f.title.trim()} onClick={async () => {
            setBusy(true)
            try {
              const r = await adminApi.post('/messages', { ...f, userIds: f.userIds.split(/\s+/).filter(Boolean) })
              toast.success(`已发送给 ${r?.sent ?? r?.count ?? ''} 位用户`)
              setF({ title: '', body: '', link: '', planCodes: [], userIds: '' })
            } finally {
              setBusy(false)
            }
          }}>发送</Button></div>
        </div>
      </Panel>
    </div>
  )
}
