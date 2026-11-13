import { useQuery } from '@tanstack/react-query'
import { BookOpen, ChevronLeft, LifeBuoy, MessageCircle, Search, Sparkles } from '@/components/icons'
import { useMemo, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { Link, Route, Routes, useParams } from 'react-router-dom'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { date, fromNow } from '@/lib/format'
import { useSession } from '@/lib/session'
import { LegalLinks } from '@/components/legal'
import { Badge, Button, Dialog, Empty, Field, Input, PageHeader, Panel, Segmented, Skeleton, Tabs, Textarea, TERMS } from '@/components/ui'

export default function Help() {
  return (
    <Routes>
      <Route index element={<HelpHome />} />
      <Route path=":id" element={<Article />} />
    </Routes>
  )
}

function HelpHome() {
  const [tab, setTab] = useState<'help' | 'glossary' | 'changelog' | 'feedback'>('help')
  const [q, setQ] = useState('')
  const [fb, setFb] = useState(false)
  const site = useSession((s) => s.site) ?? {}
  const { data, isLoading } = useQuery<any[]>({ queryKey: ['articles', tab], queryFn: () => api.get(`/articles?kind=${tab === 'changelog' ? 'changelog' : 'help'}`), enabled: tab === 'help' || tab === 'changelog' })
  const groups = useMemo(() => {
    const m = new Map<string, any[]>()
    ;(data ?? []).filter((a) => !q || a.title.includes(q) || a.summary?.includes(q) || a.body?.includes(q)).forEach((a) => m.set(a.category || '其他', [...(m.get(a.category || '其他') ?? []), a]))
    return Array.from(m.entries())
  }, [data, q])
  return (
    <div>
      <PageHeader title="帮助中心" description={<>从零开始的使用指南、常见问题和术语解释。<span className="ml-2 text-faint tnum">当前版本 v{__APP_VERSION__}</span></>} actions={<Button icon={<MessageCircle className="size-4" />} onClick={() => setFb(true)}>意见反馈</Button>} />
      <Tabs className="mb-6" value={tab} onChange={setTab} items={[{ value: 'help', label: '使用指南' }, { value: 'glossary', label: '术语表' }, { value: 'changelog', label: '更新日志' }, { value: 'feedback', label: '我的反馈' }]} />
      {tab === 'help' && (
        <div>
          <div className="mb-6 max-w-md"><Input icon={<Search />} placeholder="搜索问题，例如：多音字、换声音、导出" value={q} onChange={(e) => setQ(e.target.value)} /></div>
          {isLoading ? <div className="grid gap-4 sm:grid-cols-2">{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-24" />)}</div> : !groups.length ? <Empty icon={<LifeBuoy />} title="没有找到相关内容" description={<>试试其他关键词，或者 <button className="text-accent" onClick={() => setFb(true)}>直接问我们</button>{site.supportEmail && `（${site.supportEmail}）`}。</>} /> : (
            <div className="space-y-8">
              {groups.map(([cat, list]) => (
                <section key={cat}>
                  <h2 className="mb-3 text-[13px] font-medium text-muted">{cat}</h2>
                  <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                    {list.map((a) => (
                      <Link key={a.id} to={`/help/${a.id}`} className="group rounded-panel border border-line bg-surface p-4 transition-colors hover:border-line-strong">
                        <div className="flex items-center gap-2"><BookOpen className="size-4 text-accent" /><span className="text-[14px] font-medium group-hover:text-accent">{a.title}</span>{a.featured && <Badge tone="accent">推荐</Badge>}</div>
                        {a.summary && <p className="mt-1.5 text-[13px] text-muted">{a.summary}</p>}
                      </Link>
                    ))}
                  </div>
                </section>
              ))}
            </div>
          )}
        </div>
      )}
      {tab === 'glossary' && (
        <Panel padded={false}>
          <dl>
            {Object.entries(TERMS).map(([k, v]) => (
              <div key={k} className="grid gap-1 border-t border-line px-5 py-3.5 first:border-t-0 sm:grid-cols-[120px_1fr]"><dt className="text-[13.5px] font-medium">{k}</dt><dd className="text-[13.5px] text-muted">{v}</dd></div>
            ))}
          </dl>
        </Panel>
      )}
      {tab === 'changelog' && (
        <div className="max-w-2xl space-y-6">
          {(data ?? []).map((a) => (
            <article key={a.id} className="border-l-2 border-line pl-5">
              <div className="flex items-center gap-2 text-xs text-faint">{a.version && <Badge tone="accent">{a.version}</Badge>}{date(a.createdAt)}</div>
              <h3 className="mt-1 text-[15px] font-semibold">{a.title}</h3>
              <div className="prose-help mt-2 text-[13.5px]"><ReactMarkdown remarkPlugins={[remarkGfm]}>{a.body}</ReactMarkdown></div>
            </article>
          ))}
          {!data?.length && <Empty icon={<Sparkles />} title="暂无更新日志" />}
        </div>
      )}
      {tab === 'feedback' && <MyFeedback onNew={() => setFb(true)} />}
      <FeedbackDialog open={fb} onOpenChange={setFb} />
    </div>
  )
}

function Article() {
  const { id } = useParams()
  const { data } = useQuery({ queryKey: ['article', id], queryFn: () => api.get(`/articles/${id}`) })
  return (
    <div className="mx-auto max-w-2xl">
      <Link to="/help" className="mb-4 inline-flex items-center gap-1 text-[13px] text-muted hover:text-ink"><ChevronLeft className="size-4" />帮助中心</Link>
      {!data ? <Skeleton className="h-60" /> : (
        <article>
          <p className="text-xs text-faint">{data.category}</p>
          <h1 className="mt-1 font-serif text-[28px] font-semibold leading-tight tracking-[0.01em]">{data.title}</h1>
          <div className="prose-help mt-6 text-[14.5px] leading-[1.85]"><ReactMarkdown remarkPlugins={[remarkGfm]}>{data.body}</ReactMarkdown></div>
          <div className="mt-10 rounded-panel border border-line bg-surface px-5 py-4 text-[13px] text-muted">没有解决你的问题？在右上角「意见反馈」告诉我们，通常一个工作日内回复。</div>
          <div className="mt-4 text-[12.5px] text-muted">协议与政策：<LegalLinks all /></div>
        </article>
      )}
    </div>
  )
}

function MyFeedback({ onNew }: { onNew: () => void }) {
  const { data } = useQuery<any>({ queryKey: ['my-feedback'], queryFn: () => api.get('/me/feedback') })
  const list = Array.isArray(data) ? data : data?.items ?? []
  if (!list.length) return <Empty icon={<MessageCircle />} title="还没有提交过反馈" action={<Button onClick={onNew}>提交反馈</Button>} />
  return (
    <Panel padded={false}>
      <ul>
        {list.map((f: any) => (
          <li key={f.id} className="border-t border-line px-5 py-4 first:border-t-0">
            <div className="flex items-center gap-2 text-xs text-faint"><Badge>{f.kind === 'bug' ? '问题' : f.kind === 'question' ? '咨询' : '建议'}</Badge>{fromNow(f.createdAt)}<Badge tone={f.status === 'resolved' ? 'ok' : 'neutral'} className="ml-auto">{f.status === 'resolved' ? '已处理' : f.status === 'replied' ? '已回复' : '待处理'}</Badge></div>
            <p className="mt-2 text-[13.5px]">{f.content}</p>
            {f.reply && <p className="mt-2 rounded-md bg-accent-soft px-3 py-2 text-[13px] text-accent">官方回复：{f.reply}</p>}
          </li>
        ))}
      </ul>
    </Panel>
  )
}

function FeedbackDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const [f, setF] = useState({ kind: 'suggestion', content: '', contact: '' })
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="意见反馈" description="遇到问题或者有想法，都可以告诉我们。" width={480} footer={<><Button onClick={() => onOpenChange(false)}>取消</Button><Button variant="primary" disabled={!f.content.trim()} onClick={async () => { await api.post('/feedback', { ...f, page: location.pathname }); toast.success('感谢反馈，我们会尽快处理'); setF({ kind: 'suggestion', content: '', contact: '' }); onOpenChange(false) }}>提交</Button></>}>
      <div className="space-y-4">
        <Segmented size="sm" value={f.kind} onChange={(v) => setF({ ...f, kind: v })} items={[{ value: 'suggestion', label: '建议' }, { value: 'bug', label: '遇到问题' }, { value: 'question', label: '咨询' }]} />
        <Field label="内容"><Textarea rows={5} value={f.content} onChange={(e) => setF({ ...f, content: e.target.value })} /></Field>
        <Field label="联系方式（可选）"><Input value={f.contact} onChange={(e) => setF({ ...f, contact: e.target.value })} placeholder="邮箱、手机或微信" /></Field>
      </div>
    </Dialog>
  )
}
