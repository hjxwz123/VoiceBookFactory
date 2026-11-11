import { useState } from 'react'
import { toast } from 'sonner'
import { adminApi } from '@/lib/api'
import { dateTime, fromNow } from '@/lib/format'
import { PlayButton } from '@/components/audio'
import { Badge, Button, Dialog, Empty, Field, PageHeader, Pagination, Panel, Segmented, Select, Table, Textarea } from '@/components/ui'
import { ainv, CrudPage, ReviewButtons, useAdmin } from '../kit'

const statusTone = (s: string) => (s === 'approved' || s === 'pass' || s === 'resolved' ? 'ok' : s === 'rejected' || s === 'block' ? 'danger' : 'warn')

export function Moderation() {
  const [status, setStatus] = useState('pending')
  const [page, setPage] = useState(1)
  const { data } = useAdmin<any>('/moderation', { status, page, pageSize: 30 })
  return (
    <div>
      <PageHeader title="内容审核" description="书稿导入、简介、评论等内容会经过敏感词和（可选的）大模型审核，命中的内容在这里人工复核。" />
      <Segmented className="mb-4" size="sm" value={status} onChange={(v) => { setStatus(v); setPage(1) }} items={[{ value: 'pending', label: '待复核' }, { value: 'approved', label: '已通过' }, { value: 'rejected', label: '已驳回' }, { value: '', label: '全部' }]} />
      <Panel padded={false}>
        {!data?.items?.length ? <Empty title="没有需要复核的内容" /> : (
          <ul>
            {data.items.map(({ record: m, bookTitle, bookStatus }: any) => (
              <li key={m.id} className="border-t border-line px-5 py-4 first:border-t-0">
                <div className="flex flex-wrap items-center gap-2 text-[13px]">
                  <Badge tone={m.result === 'block' ? 'danger' : 'warn'}>{m.result === 'block' ? '拦截' : m.result === 'review' ? '疑似' : m.result}</Badge>
                  <span className="font-medium">{bookTitle ? `《${bookTitle}》` : m.targetType}</span>
                  <span className="text-xs text-faint">{m.source === 'llm' ? 'AI 审核' : '敏感词'} · {fromNow(m.createdAt)}</span>
                  <Badge tone={statusTone(m.status)} className="ml-auto">{m.status === 'pending' ? '待复核' : m.status === 'approved' ? '已通过' : '已驳回'}</Badge>
                </div>
                {m.hits && <div className="mt-2 flex flex-wrap gap-1">{Object.entries(m.hits as Record<string, string[]>).flatMap(([lv, ws]) => (ws ?? []).map((w) => <Badge key={lv + w} tone={lv === 'block' ? 'danger' : 'warn'}>{w}</Badge>))}</div>}
                {m.excerpt && <p className="mt-2 rounded-md bg-sunken/60 px-3 py-2 text-[13px] leading-relaxed text-muted">{m.excerpt}</p>}
                {m.note && <p className="mt-1 text-xs text-muted">备注：{m.note}</p>}
                {m.status === 'pending' && <div className="mt-3"><ReviewButtons onDecide={async (approve, note) => { await adminApi.post(`/moderation/${m.id}`, { approve, note }); ainv(['/moderation']); toast.success(approve ? '已放行' : '已驳回，图书已下架') }} approveText="放行" rejectText="驳回" /></div>}
                {bookStatus === 'moderation' && <p className="mt-2 text-xs text-warn">图书当前处于审核中状态，放行后恢复</p>}
              </li>
            ))}
          </ul>
        )}
      </Panel>
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
    </div>
  )
}

export function Consents() {
  const [status, setStatus] = useState('pending')
  const [page, setPage] = useState(1)
  const { data } = useAdmin<any>('/consents', { status, page, pageSize: 30 })
  return (
    <div>
      <PageHeader title="声音授权" description="克隆他人声音需要提交授权书，审核通过后才开始训练。本人克隆由验证句和声纹自动校验，也会记录在这里备查。" />
      <Segmented className="mb-4" size="sm" value={status} onChange={(v) => { setStatus(v); setPage(1) }} items={[{ value: 'pending', label: '待审核' }, { value: 'approved', label: '已通过' }, { value: 'rejected', label: '已驳回' }, { value: '', label: '全部' }]} />
      <Panel padded={false}>
        {!data?.items?.length ? <Empty title="没有记录" /> : (
          <Table>
            <thead><tr><th>音色</th><th>申请人</th><th>方式</th><th>声音主人</th><th>材料</th><th>状态</th><th>时间</th><th /></tr></thead>
            <tbody>
              {data.items.map(({ consent: c, user, voiceName, sampleUrl, evidenceUrl, documentUrl }: any) => (
                <tr key={c.id}>
                  <td className="font-medium">{voiceName}</td>
                  <td>{user}</td>
                  <td>{c.type === 'self' ? '本人' : '他人授权'}</td>
                  <td>{c.speakerName}{c.identityInfo && <div className="text-xs text-faint">{c.identityInfo}</div>}</td>
                  <td className="whitespace-nowrap">
                    <span className="inline-flex items-center gap-1.5">
                      {sampleUrl && <PlayButton id={'consent-s:' + c.id} url={sampleUrl} size="xs" label="试听样本" />}
                      {evidenceUrl && <PlayButton id={'consent-e:' + c.id} url={evidenceUrl} size="xs" label={`验证句：${c.challenge}`} />}
                      {documentUrl && <a href={documentUrl} target="_blank" className="text-xs text-accent">授权书</a>}
                    </span>
                  </td>
                  <td><Badge tone={statusTone(c.status)}>{c.status === 'pending' ? '待审核' : c.status === 'approved' ? '已通过' : '已驳回'}</Badge>{c.reviewNote && <div className="max-w-40 truncate text-[11px] text-faint" title={c.reviewNote}>{c.reviewNote}</div>}</td>
                  <td className="text-xs text-faint">{dateTime(c.createdAt)}</td>
                  <td className="text-right">{c.status === 'pending' && <ReviewButtons onDecide={async (approve, note) => { await adminApi.post(`/consents/${c.id}`, { approve, note }); ainv(['/consents']); toast.success('已处理') }} />}</td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Panel>
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
    </div>
  )
}

export function Reports() {
  const [status, setStatus] = useState('open')
  const [page, setPage] = useState(1)
  const [act, setAct] = useState<any>(null)
  const [f, setF] = useState({ action: '', resolution: '' })
  const { data } = useAdmin<any>('/reports', { status, page, pageSize: 30 })
  return (
    <div>
      <PageHeader title="举报" description="用户对音色、作品、评论的侵权或违规举报。" />
      <Segmented className="mb-4" size="sm" value={status} onChange={(v) => { setStatus(v); setPage(1) }} items={[{ value: 'open', label: '待处理' }, { value: 'resolved', label: '已处理' }, { value: 'dismissed', label: '已驳回' }, { value: '', label: '全部' }]} />
      <Panel padded={false}>
        {!data?.items?.length ? <Empty title="没有举报" /> : (
          <Table>
            <thead><tr><th>对象</th><th>原因</th><th>举报人</th><th>时间</th><th>状态</th><th /></tr></thead>
            <tbody>{data.items.map(({ report: r, reporter, targetName }: any) => <tr key={r.id}><td><div className="font-medium">{targetName}</div><div className="text-xs text-faint">{r.targetType}</div></td><td className="max-w-xs"><div>{r.reason}</div>{r.detail && <div className="truncate text-xs text-muted">{r.detail}</div>}</td><td>{reporter}</td><td className="text-xs text-faint">{fromNow(r.createdAt)}</td><td><Badge tone={statusTone(r.status)}>{r.status === 'open' ? '待处理' : r.status === 'resolved' ? '已处理' : '已驳回'}</Badge>{r.resolution && <div className="max-w-40 truncate text-[11px] text-faint">{r.resolution}</div>}</td><td className="text-right">{r.status === 'open' && <Button size="xs" onClick={() => { setAct(r); setF({ action: r.targetType === 'voice' ? 'disable_voice' : '', resolution: '' }) }}>处理</Button>}</td></tr>)}</tbody>
          </Table>
        )}
      </Panel>
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
      <Dialog open={!!act} onOpenChange={(o) => !o && setAct(null)} title="处理举报" width={460} footer={<><Button onClick={async () => { await adminApi.post(`/reports/${act.id}`, { status: 'dismissed', resolution: f.resolution || '经核实不构成违规' }); ainv(['/reports']); setAct(null) }}>驳回举报</Button><Button variant="primary" onClick={async () => { await adminApi.post(`/reports/${act.id}`, { status: 'resolved', action: f.action, resolution: f.resolution }); ainv(['/reports']); setAct(null); toast.success('已处理') }}>确认处理</Button></>}>
        <div className="space-y-4">
          <Field label="处置措施"><Select value={f.action} onChange={(v) => setF({ ...f, action: v })} options={[['', '仅记录，不做处置'], ['disable_voice', '停用该音色并下架'], ['archive_book', '下架（归档）该作品'], ['disable_user', '封禁该用户']]} /></Field>
          <Field label="处理说明（会通知举报人）"><Textarea rows={3} value={f.resolution} onChange={(e) => setF({ ...f, resolution: e.target.value })} /></Field>
        </div>
      </Dialog>
    </div>
  )
}

export function Words() {
  const [imp, setImp] = useState(false)
  const [f, setF] = useState({ words: '', level: 'block', category: '' })
  return (
    <>
      <CrudPage
        title="敏感词"
        description="「拦截」级别的词会阻止导入并进入人工复核；「提示」级别只做标记。"
        path="/sensitive-words"
        filters={[{ key: 'level', label: '级别', options: [['block', '拦截'], ['review', '提示']] }]}
        columns={[{ key: 'word', label: '词' }, { key: 'level', label: '级别', render: (r: any) => <Badge tone={r.level === 'block' ? 'danger' : 'warn'}>{r.level === 'block' ? '拦截' : '提示'}</Badge> }, { key: 'category', label: '分类' }]}
        fields={[{ key: 'word', label: '词' }, { key: 'level', label: '级别', type: 'select', options: [['block', '拦截'], ['review', '提示']] }, { key: 'category', label: '分类' }]}
        defaults={{ level: 'block' }}
        headerActions={<Button onClick={() => setImp(true)}>批量导入</Button>}
      />
      <Dialog open={imp} onOpenChange={setImp} title="批量导入敏感词" width={480} footer={<><Button onClick={() => setImp(false)}>取消</Button><Button variant="primary" disabled={!f.words.trim()} onClick={async () => { const r = await adminApi.post('/sensitive-words/import', f); ainv(['/sensitive-words']); setImp(false); toast.success(`已导入 ${r?.imported ?? ''} 个`) }}>导入</Button></>}>
        <div className="space-y-4">
          <Field label="词表" hint="每行一个"><Textarea rows={10} value={f.words} onChange={(e) => setF({ ...f, words: e.target.value })} /></Field>
          <div className="grid grid-cols-2 gap-3">
            <Field label="级别"><Select value={f.level} onChange={(v) => setF({ ...f, level: v })} options={[['block', '拦截'], ['review', '提示']]} /></Field>
            <Field label="分类"><Textarea rows={1} value={f.category} onChange={(e) => setF({ ...f, category: e.target.value })} /></Field>
          </div>
        </div>
      </Dialog>
    </>
  )
}
