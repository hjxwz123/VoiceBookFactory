import { useQuery } from '@tanstack/react-query'
import { ClipboardCheck, Download, ExternalLink } from '@/components/icons'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { toast } from 'sonner'
import { api, qs } from '@/lib/api'
import { dateTime, duration, fromNow, issueStatusName, issueTypes } from '@/lib/format'
import { Badge, Button, Empty, Panel, Pagination, Segmented, Select, Table, Term } from '@/components/ui'
import { can, invalidate, useChapters } from '../../hooks'

const typeName = Object.fromEntries(issueTypes)
const statusTone: Record<string, any> = { open: 'danger', in_progress: 'accent', verifying: 'warn', closed: 'neutral', disputed: 'signal' }

export default function Review({ book }: { book: any }) {
  const chapters = useChapters(book.id)
  const [status, setStatus] = useState('open')
  const [chapter, setChapter] = useState('')
  const [page, setPage] = useState(1)
  const { data } = useQuery({ queryKey: ['issues', book.id, status, chapter, page], queryFn: () => api.get(`/books/${book.id}/issues${qs({ status: status === 'open' ? 'active' : status, chapterId: chapter, page, pageSize: 30 })}`), placeholderData: (p) => p })
  const act = async (id: string, action: string) => {
    await api.post(`/issues/${id}/action`, { action })
    invalidate(['issues'])
    toast.success('已更新')
  }
  const reviewed = (chapters.data ?? []).filter((c) => c.reviewStatus === 'passed').length
  const total = (chapters.data ?? []).filter((c) => c.included).length

  return (
    <div className="space-y-6">
      <Panel padded={false}>
        <div className="flex flex-wrap items-start gap-x-6 gap-y-3 px-5 py-4">
          <div className="flex items-center gap-3">
            <div className="grid size-10 place-items-center rounded-full border border-line text-accent"><ClipboardCheck className="size-5" /></div>
            <div>
              <div className="flex items-center gap-1 text-[14px] font-semibold">审听进度 <Term id="审听" /></div>
              <div className="text-[13px] text-muted">{reviewed}/{total} 章已通过审听</div>
            </div>
          </div>
          <p className="min-w-[16rem] max-w-2xl flex-1 basis-full text-[13px] leading-relaxed text-muted sm:basis-0">逐章进入制作台审听：「连续播放」对照文字边听边按 <kbd className="rounded border border-line px-1 text-[11px]">M</kbd> 标记问题句，也可以用「智能审听」自动比对原文找出漏读、错读。返工的句子重新生成后变为「待复核」，复听没问题再关闭，整章没问题就点「审听通过」。</p>
        </div>
        <ChapterReviewList book={book} chapters={(chapters.data ?? []).filter((c) => c.included)} onIssues={(id) => { setChapter(id); setPage(1); document.getElementById('issues')?.scrollIntoView({ behavior: 'smooth', block: 'start' }) }} />
      </Panel>

      <Panel id="issues" padded={false} title="返修工单" description="工单定位到具体句子，处理人修复后提交复核，审听人确认后关闭。" actions={<Button size="sm" icon={<Download className="size-4" />} onClick={() => exportSheet(book, status, chapter)}>导出工单表</Button>}>
        <div className="flex flex-wrap items-center gap-3 border-b border-line px-5 py-3">
          <Segmented size="sm" value={status} onChange={(v) => { setStatus(v); setPage(1) }} items={[{ value: 'open', label: '未关闭' }, { value: 'verifying', label: '待复核' }, { value: 'disputed', label: '有异议' }, { value: 'closed', label: '已关闭' }]} />
          <Select size="sm" className="ml-auto w-48" value={chapter} onChange={(v) => { setChapter(v); setPage(1) }} options={[['', '全部章节'], ...(chapters.data ?? []).map((c) => [c.id, c.title] as [string, string])]} />
        </div>
        {!data?.items?.length ? (
          <Empty title="没有工单" description={status === 'open' ? '一切正常。审听时发现问题可以随时提工单。' : undefined} />
        ) : (
          <Table>
            <thead><tr><th>问题</th><th>位置</th><th>处理人</th><th>状态</th><th>时间</th><th /></tr></thead>
            <tbody>
              {data.items.map((it: any) => {
                const i = it.issue
                return (
                  <tr key={i.id}>
                    <td className="max-w-[320px]">
                      <div className="flex items-center gap-2"><Badge>{typeName[i.type] ?? i.type}</Badge><span className="truncate font-medium">{i.title}</span></div>
                      {it.segmentText && <div className="mt-1 truncate text-xs text-muted">「{it.segmentText}」</div>}
                    </td>
                    <td className="whitespace-nowrap text-muted">第 {it.chapterIndex} 章</td>
                    <td className="whitespace-nowrap">{it.assignee || <span className="text-faint">未指派</span>}</td>
                    <td><Badge tone={statusTone[i.status]}>{issueStatusName[i.status]}</Badge></td>
                    <td className="whitespace-nowrap text-xs text-faint">{fromNow(i.createdAt)}</td>
                    <td className="whitespace-nowrap text-right">
                      <div className="flex justify-end gap-1">
                        {(i.status === 'open' || i.status === 'in_progress') && <Button size="xs" onClick={() => act(i.id, 'submit')}>已修复</Button>}
                        {i.status === 'verifying' && can(book, 'review') && <><Button size="xs" onClick={() => act(i.id, 'reopen')}>没修好</Button><Button size="xs" variant="primary" onClick={() => act(i.id, 'close')}>确认关闭</Button></>}
                        {i.status === 'closed' && <Button size="xs" variant="ghost" onClick={() => act(i.id, 'reopen')}>重新打开</Button>}
                        <Link to={`/books/${book.id}/chapters/${i.chapterId}?segment=${i.segmentId}`}><Button size="xs" variant="ghost" icon={<ExternalLink className="size-3.5" />}>定位</Button></Link>
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </Table>
        )}
        {data && <div className="px-5 pb-4"><Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} /></div>}
      </Panel>
    </div>
  )
}

/** QC sheet as CSV (opens in Excel/WPS): every problem with chapter, line, type, note and status, to hand to
 *  narrators or editors the way proofers usually do. */
async function exportSheet(book: any, status: string, chapter: string) {
  const rows: any[] = []
  for (let page = 1; page < 50; page++) {
    const r = await api.get(`/books/${book.id}/issues${qs({ status: status === 'open' ? 'active' : status, chapterId: chapter, page, pageSize: 200 })}`)
    rows.push(...(r.items ?? []))
    if (rows.length >= r.total || !r.items?.length) break
  }
  if (!rows.length) return toast('当前筛选下没有工单')
  const cell = (v: any) => `"${String(v ?? '').replace(/"/g, '""')}"`
  const head = ['章节', '章节标题', '句子', '问题类型', '问题', '说明', '状态', '处理人', '提出人', '提出时间']
  const lines = rows.map((it) => [it.chapterIndex, it.chapterTitle, it.segmentText, typeName[it.issue.type] ?? it.issue.type, it.issue.title, it.issue.description, issueStatusName[it.issue.status], it.assignee, it.reporter, dateTime(it.issue.createdAt)].map(cell).join(','))
  const blob = new Blob(['\ufeff' + [head.map(cell).join(','), ...lines].join('\r\n')], { type: 'text/csv;charset=utf-8' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = `${book.title}-审听工单表.csv`
  a.click()
  setTimeout(() => URL.revokeObjectURL(a.href), 2000)
  toast.success(`已导出 ${rows.length} 条工单`)
}

/** One row per chapter: where the finished audio stands, whether it passed review, and what is left to fix. */
function ChapterReviewList({ book, chapters, onIssues }: { book: any; chapters: any[]; onIssues: (chapterId: string) => void }) {
  const [all, setAll] = useState(false)
  if (!chapters.length) return null
  const shown = all ? chapters : chapters.slice(0, 8)
  const state = (c: any): [string, any] => {
    if (c.reviewStatus === 'passed') return ['已通过', 'ok']
    if (c.reviewStatus === 'failed') return ['未通过', 'danger']
    if (!c.renderId) return ['还没合成', 'neutral']
    if (c.renderStale) return ['有改动待重新合成', 'warn']
    return ['待审听', 'accent']
  }
  return (
    <div className="border-t border-line">
      <Table>
        <thead><tr><th className="w-14">序号</th><th>章节</th><th>成品</th><th>审听</th><th>待处理工单</th><th /></tr></thead>
        <tbody>
          {shown.map((c) => {
            const [label, tone] = state(c)
            return (
              <tr key={c.id}>
                <td className="tnum text-faint">{c.index}</td>
                <td className="min-w-32 font-medium">{c.title}</td>
                <td className="whitespace-nowrap tnum text-muted">{c.renderId ? duration(c.durationMs) : '—'}</td>
                <td className="whitespace-nowrap"><Badge tone={tone}>{label}</Badge></td>
                <td className="whitespace-nowrap">
                  {c.openIssues > 0 || c.verifyingIssues > 0 ? (
                    <button className="text-[13px] hover:underline" onClick={() => onIssues(c.id)}>
                      {c.openIssues > 0 && <span className="text-danger">{c.openIssues} 个待返工</span>}
                      {c.openIssues > 0 && c.verifyingIssues > 0 && <span className="text-faint"> · </span>}
                      {c.verifyingIssues > 0 && <span className="text-warn">{c.verifyingIssues} 个待复核</span>}
                    </button>
                  ) : <span className="text-faint">—</span>}
                </td>
                <td className="text-right">
                  <Link to={`/books/${book.id}/chapters/${c.id}?panel=review`}><Button size="xs" variant={c.reviewStatus !== 'passed' && c.renderId ? 'primary' : 'ghost'}>{c.reviewStatus === 'passed' ? '再听一遍' : '去审听'}</Button></Link>
                </td>
              </tr>
            )
          })}
        </tbody>
      </Table>
      {chapters.length > 8 && (
        <div className="border-t border-line px-5 py-2.5 text-center">
          <button className="text-[13px] text-muted hover:text-ink" onClick={() => setAll(!all)}>{all ? '收起' : `显示全部 ${chapters.length} 章`}</button>
        </div>
      )}
    </div>
  )
}
