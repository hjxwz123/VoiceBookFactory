import { useQuery } from '@tanstack/react-query'
import { Plus, Search, Trash2 } from '@/components/icons'
import React, { useState } from 'react'
import { toast } from 'sonner'
import { adminApi, qs } from '@/lib/api'
import { queryClient } from '@/lib/live'
import { Button, Checkbox, cn, confirm, DateInput, Empty, Field, Input, PageHeader, Pagination, Panel, Segmented, Select, Sheet, Skeleton, Switch, Table, Textarea } from '@/components/ui'

export const ainv = (...keys: any[][]) => keys.forEach((k) => queryClient.invalidateQueries({ queryKey: ['admin', ...k] }))

export function useAdmin<T = any>(path: string, params: Record<string, any> = {}, opts: { enabled?: boolean; refetch?: number } = {}) {
  return useQuery<T>({ queryKey: ['admin', path, params], queryFn: () => adminApi.get(path + qs(params)), placeholderData: (p) => p, enabled: opts.enabled ?? true, refetchInterval: opts.refetch })
}

export type Col<T> = { key: string; label: React.ReactNode; render?: (row: T) => React.ReactNode; className?: string; align?: 'right' }
export type FieldDef = {
  key: string
  label: string
  type?: 'text' | 'textarea' | 'number' | 'switch' | 'select' | 'tags' | 'date' | 'json' | 'markdown'
  options?: [string, string][]
  hint?: string
  span?: 2
  placeholder?: string
}

/** Paginated list + filters + create/edit sheet driven by a field schema. Used for the many simple CRUD resources. */
export function CrudPage<T extends { id: string }>({
  title, description, path, columns, fields, filters, searchable = true, defaults = {}, canCreate = true, rowActions, keyName = (r: any) => r.name ?? r.title ?? r.code ?? r.term ?? r.word, headerActions,
}: {
  title: string; description?: React.ReactNode; path: string; columns: Col<T>[]; fields?: FieldDef[]; filters?: { key: string; label: string; options: [string, string][] }[]
  searchable?: boolean; defaults?: Record<string, any>; canCreate?: boolean; rowActions?: (row: T) => React.ReactNode; keyName?: (r: T) => string; headerActions?: React.ReactNode
}) {
  const [page, setPage] = useState(1)
  const [q, setQ] = useState('')
  const [f, setF] = useState<Record<string, string>>({})
  const [edit, setEdit] = useState<any>(null)
  const { data, isLoading } = useAdmin<any>(path, { page, pageSize: 30, q, ...f })
  const items: T[] = Array.isArray(data) ? data : data?.items ?? []
  return (
    <div>
      <PageHeader title={title} description={description} actions={<>{headerActions}{fields && canCreate && <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEdit({ ...defaults })}>新建</Button>}</>} />
      {(searchable || filters) && (
        <div className="mb-4 flex flex-wrap items-center gap-3">
          {filters?.map((fl) => <Select key={fl.key} size="sm" value={f[fl.key] ?? ''} onChange={(v) => { setF({ ...f, [fl.key]: v }); setPage(1) }} options={[['', `全部${fl.label}`], ...fl.options]} className="w-36" />)}
          {searchable && <div className="ml-auto w-full max-w-xs"><Input icon={<Search />} placeholder="搜索" value={q} onChange={(e) => { setQ(e.target.value); setPage(1) }} /></div>}
        </div>
      )}
      <Panel padded={false}>
        {isLoading ? <div className="space-y-2 p-4">{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-9" />)}</div> : !items.length ? <Empty title="暂无数据" /> : (
          <Table>
            <thead><tr>{columns.map((c) => <th key={c.key} className={cn(c.align === 'right' && '!text-right', c.className)}>{c.label}</th>)}<th /></tr></thead>
            <tbody>
              {items.map((row: any) => (
                <tr key={row.id}>
                  {columns.map((c) => <td key={c.key} className={cn(c.align === 'right' && 'text-right tnum', c.className)}>{c.render ? c.render(row) : fmt(row[c.key])}</td>)}
                  <td className="whitespace-nowrap text-right">
                    {rowActions?.(row)}
                    {fields && <Button size="xs" variant="ghost" onClick={() => setEdit(row)}>编辑</Button>}
                    {fields && <Button size="xs" variant="ghost" className="text-danger" icon={<Trash2 className="size-3.5" />} onClick={async () => { if (await confirm({ title: `删除「${keyName(row) ?? ''}」？`, danger: true, confirmText: '删除' })) { await adminApi.del(`${path}/${row.id}`); ainv([path]); toast.success('已删除') } }} />}
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Panel>
      {data && !Array.isArray(data) && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
      {fields && <EditSheet path={path} fields={fields} value={edit} onClose={() => setEdit(null)} title={edit?.id ? `编辑${title}` : `新建${title}`} />}
    </div>
  )
}

export function fmt(v: any): React.ReactNode {
  if (v === null || v === undefined || v === '') return <span className="text-faint">—</span>
  if (typeof v === 'boolean') return v ? '是' : '否'
  if (Array.isArray(v)) return v.join('、') || <span className="text-faint">—</span>
  if (typeof v === 'object') return <span className="font-mono text-xs text-muted">{JSON.stringify(v).slice(0, 60)}</span>
  if (typeof v === 'string' && /^\d{4}-\d{2}-\d{2}T/.test(v)) return v.slice(0, 16).replace('T', ' ')
  return String(v)
}

export function EditSheet({ path, fields, value, onClose, title }: { path: string; fields: FieldDef[]; value: any; onClose: () => void; title: string }) {
  const [f, setF] = useState<any>(null)
  const [busy, setBusy] = useState(false)
  if (value && (!f || f.__src !== value)) {
    const init: any = { ...value, __src: value }
    fields.forEach((d) => { if (d.type === 'json') init[d.key] = JSON.stringify(value[d.key] ?? (d.key.endsWith('s') ? [] : {}), null, 2); if (d.type === 'tags') init[d.key] = (value[d.key] ?? []).join('、') })
    setTimeout(() => setF(init))
  }
  const save = async () => {
    setBusy(true)
    try {
      const { __src, ...body } = f
      for (const d of fields) {
        if (d.type === 'json') { try { body[d.key] = JSON.parse(body[d.key] || 'null') } catch { toast.error(`${d.label} 不是合法的 JSON`); return } }
        if (d.type === 'tags') body[d.key] = String(body[d.key] ?? '').split(/[、,，\n]+/).map((s) => s.trim()).filter(Boolean)
        if (d.type === 'number') body[d.key] = Number(body[d.key] ?? 0)
        if (d.type === 'date') body[d.key] = body[d.key] ? new Date(body[d.key]).toISOString() : null
      }
      if (value.id) await adminApi.put(`${path}/${value.id}`, body)
      else await adminApi.post(path, body)
      ainv([path])
      toast.success('已保存')
      onClose()
      setF(null)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Sheet open={!!value} onOpenChange={(o) => { if (!o) { onClose(); setF(null) } }} title={title} width={540} footer={<><Button onClick={() => { onClose(); setF(null) }}>取消</Button><Button variant="primary" loading={busy} onClick={save}>保存</Button></>}>
      {f && (
        <div className="grid grid-cols-2 gap-4">
          {fields.map((d) => (
            <Field key={d.key} label={d.type === 'switch' ? undefined : d.label} hint={d.hint} className={cn(d.span === 2 || ['textarea', 'json', 'markdown', 'tags'].includes(d.type ?? '') ? 'col-span-2' : '')}>
              {d.type === 'switch' ? <Switch checked={!!f[d.key]} onChange={(v) => setF({ ...f, [d.key]: v })} label={d.label} />
                : d.type === 'select' ? <Select value={String(f[d.key] ?? '')} onChange={(v) => setF({ ...f, [d.key]: v })} options={d.options!} />
                : d.type === 'textarea' || d.type === 'markdown' ? <Textarea rows={d.type === 'markdown' ? 12 : 3} value={f[d.key] ?? ''} onChange={(e) => setF({ ...f, [d.key]: e.target.value })} placeholder={d.placeholder} className={d.type === 'markdown' ? 'font-mono text-[12.5px]' : ''} />
                : d.type === 'json' ? <Textarea rows={8} value={f[d.key]} onChange={(e) => setF({ ...f, [d.key]: e.target.value })} className="font-mono text-[12px]" />
                : d.type === 'date' ? <DateInput time value={f[d.key] ? String(f[d.key]).slice(0, 16) : ''} onChange={(v) => setF({ ...f, [d.key]: v })} />
                : <Input type={d.type === 'number' ? 'number' : 'text'} value={f[d.key] ?? ''} onChange={(e) => setF({ ...f, [d.key]: e.target.value })} placeholder={d.placeholder} />}
            </Field>
          ))}
        </div>
      )}
    </Sheet>
  )
}

export function ReviewButtons({ onDecide, approveText = '通过', rejectText = '驳回' }: { onDecide: (approve: boolean, note: string) => Promise<void>; approveText?: string; rejectText?: string }) {
  return (
    <span className="inline-flex gap-1">
      <Button size="xs" onClick={async () => { const note = prompt('驳回原因（会通知用户）') ?? ''; if (note !== null) await onDecide(false, note) }}>{rejectText}</Button>
      <Button size="xs" variant="primary" onClick={() => onDecide(true, '')}>{approveText}</Button>
    </span>
  )
}

export { Checkbox, Segmented }
