import { useState } from 'react'
import ReactMarkdown from 'react-markdown'
import { toast } from 'sonner'
import { adminApi } from '@/lib/api'
import { dateTime } from '@/lib/format'
import { Badge, Button, Dialog, Empty, Field, Input, PageHeader, Panel, Segmented, Switch, Table, Textarea } from '@/components/ui'
import { ainv, useAdmin } from '../kit'

const required = ['terms', 'privacy', 'copyright']

/** User agreement, privacy policy, copyright statement and AI rules shown at sign-up and in the footer. */
export default function Legal() {
  const { data } = useAdmin<any>('/articles', { kind: 'legal', pageSize: 50 })
  const [edit, setEdit] = useState<any>(null)
  const items: any[] = data?.items ?? []
  return (
    <div>
      <PageHeader
        title="协议与政策"
        description={<>注册和登录页要求用户勾选同意。正文支持 Markdown，可以用 <code className="rounded bg-sunken px-1">{'{平台名称}'}</code>、<code className="rounded bg-sunken px-1">{'{运营主体}'}</code>、<code className="rounded bg-sunken px-1">{'{联系邮箱}'}</code>，会替换成「系统设置 → 站点」里填写的内容。默认文本仅供参考，正式上线前请让法务审核并补充运营主体信息。</>}
      />
      <Panel padded={false}>
        {!items.length ? <Empty title="暂无文档" /> : (
          <Table>
            <thead><tr><th>文档</th><th>版本</th><th>注册时须同意</th><th>状态</th><th>更新时间</th><th /></tr></thead>
            <tbody>
              {items.sort((a, b) => a.sortOrder - b.sortOrder).map((d) => (
                <tr key={d.id}>
                  <td><div className="font-medium">{d.title}</div><div className="text-xs text-faint">{d.summary}</div></td>
                  <td className="tnum text-muted">{d.version}</td>
                  <td>{required.includes(d.slug) ? <Badge tone="accent">须同意</Badge> : <span className="text-faint">—</span>}</td>
                  <td>{d.published ? <Badge tone="ok">已发布</Badge> : <Badge>未发布</Badge>}</td>
                  <td className="text-xs text-faint">{dateTime(d.updatedAt)}</td>
                  <td className="whitespace-nowrap text-right">
                    <a href={`/legal/${d.slug}`} target="_blank" rel="noreferrer"><Button size="xs" variant="ghost">查看</Button></a>
                    <Button size="xs" variant="ghost" onClick={() => setEdit(d)}>编辑</Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Panel>
      {edit && <EditDoc doc={edit} onClose={() => setEdit(null)} />}
    </div>
  )
}

function EditDoc({ doc, onClose }: { doc: any; onClose: () => void }) {
  const [f, setF] = useState({ title: doc.title, summary: doc.summary, body: doc.body, published: doc.published })
  const [view, setView] = useState<'edit' | 'preview'>('edit')
  const [bump, setBump] = useState(false)
  const [busy, setBusy] = useState(false)
  const isRequired = required.includes(doc.slug)
  const save = async () => {
    setBusy(true)
    try {
      const d = new Date()
      const version = bump ? `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}` : doc.version
      await adminApi.put(`/articles/${doc.id}`, { ...f, version })
      ainv(['/articles'])
      toast.success(bump && isRequired ? '已发布新版本，用户下次打开平台时需要重新同意' : '已保存')
      onClose()
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()} title={`编辑《${doc.title}》`} width={820}
      footer={<><Button onClick={onClose}>取消</Button><Button variant="primary" loading={busy} disabled={!f.title.trim() || !f.body.trim()} onClick={save}>{bump ? '发布新版本' : '保存'}</Button></>}>
      <div className="space-y-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="标题"><Input value={f.title} onChange={(e) => setF({ ...f, title: e.target.value })} /></Field>
          <Field label="摘要"><Input value={f.summary} onChange={(e) => setF({ ...f, summary: e.target.value })} /></Field>
        </div>
        <div className="flex items-center justify-between">
          <Segmented size="sm" value={view} onChange={setView} items={[{ value: 'edit', label: '编辑' }, { value: 'preview', label: '预览' }]} />
          <Switch checked={f.published} onChange={(v) => setF({ ...f, published: v })} label="发布" />
        </div>
        {view === 'edit' ? (
          <Textarea rows={18} value={f.body} onChange={(e) => setF({ ...f, body: e.target.value })} className="font-mono text-[12.5px]" />
        ) : (
          <div className="prose-help max-h-[52vh] overflow-y-auto rounded-[10px] border border-line px-5 py-4 text-[13.5px]"><ReactMarkdown>{f.body}</ReactMarkdown></div>
        )}
        {isRequired && (
          <div className="flex items-center justify-between rounded-[10px] bg-sunken/60 px-4 py-3">
            <div>
              <div className="text-[13.5px] font-medium">作为新版本发布</div>
              <div className="text-xs text-muted">实质性修改（如权利义务、收集的信息变化）请打开：所有用户下次打开平台时都要重新阅读并同意。只改错别字可以不开。</div>
            </div>
            <Switch checked={bump} onChange={setBump} />
          </div>
        )}
      </div>
    </Dialog>
  )
}
