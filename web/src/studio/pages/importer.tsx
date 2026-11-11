import { FileText, Merge, Scissors, Sparkles } from '@/components/icons'
import { useState } from 'react'
import { toast } from 'sonner'
import { CopyrightNotice } from '@/components/legal'
import { api } from '@/lib/api'
import { num } from '@/lib/format'
import { UploadZone } from '@/components/audio'
import { Badge, Button, Checkbox, cn, Panel, Segmented, Select, Switch, Textarea } from '@/components/ui'
import { useSid } from '../hooks'

/** A chapter of an import preview; `sources` lists the detected chapters merged into it. */
export type ImportCh = { title: string; kind: string; included: boolean; wordCount: number; preview: string; volume?: string; sources: number[] }

export const chaptersFromPreview = (p: any): ImportCh[] => p.chapters.map((c: any, i: number) => ({ ...c, sources: [i] }))
export const chapterRefs = (chs: ImportCh[]) => chs.map((c) => ({ sources: c.sources, title: c.title, included: c.included }))

const detectionText: Record<string, string> = {
  structure: '按文件自带的章节结构识别',
  rules: '按「第 X 章」等标题识别',
  ai: '由 AI 识别章节标题',
  size: '没有找到章节标题，暂时按字数分集',
  single: '内容较短，作为一章处理',
}

/** Upload a file or paste text; resolves to an import preview with detected chapters. */
export function ManuscriptInput({ onLoaded, title = '', compact }: { onLoaded: (preview: any) => void; title?: string; compact?: boolean }) {
  const sid = useSid()
  const [source, setSource] = useState<'file' | 'text'>('file')
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const run = async (fn: () => Promise<any>) => {
    setBusy(true)
    try {
      onLoaded(await fn())
    } catch (e: any) {
      toast.error(e.message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div>
      <Segmented value={source} onChange={setSource} items={[{ value: 'file', label: '上传文件' }, { value: 'text', label: '粘贴文本' }]} />
      <CopyrightNotice className="mt-3">请确认书稿是你的原创、已取得有声改编和发行授权，或已进入公有领域。本平台不审查书稿版权，因此产生的纠纷由你自行承担。</CopyrightNotice>
      <div className="mt-5">
        {source === 'file' ? (
          <UploadZone accept=".txt,.md,.docx,.epub,.pdf" onFiles={(files) => run(() => api.upload(`/studios/${sid}/imports`, files[0]))} hint="支持 txt、docx、epub、pdf、md，最大 200 MB。编码会自动识别（UTF-8 / GBK）。">
            {busy ? <p className="text-[14px] text-muted">正在读取书稿并识别章节…</p> : undefined}
          </UploadZone>
        ) : (
          <div className="space-y-3">
            <Textarea rows={compact ? 9 : 12} value={text} onChange={(e) => setText(e.target.value)} placeholder="把正文粘贴到这里。带有「第一章」「第二章」这样的标题时，会自动拆分章节。" />
            <div className="flex justify-between">
              <span className="text-xs text-faint tnum">{text.length} 字</span>
              <Button variant="primary" disabled={!text.trim()} loading={busy} onClick={() => run(() => api.post(`/studios/${sid}/imports`, { text, title }))}>识别章节</Button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

/** Review detected chapters: rename, merge, include/exclude, re-split by length or let AI detect titles. */
export function ChapterReview({ preview, chs, setChs, onPreview, maxHeight = '52vh' }: { preview: any; chs: ImportCh[]; setChs: (c: ImportCh[]) => void; onPreview: (p: any) => void; maxHeight?: string }) {
  const sid = useSid()
  const [sel, setSel] = useState<number[]>([])
  const [size, setSize] = useState('3000')
  const [busy, setBusy] = useState('')
  const reload = async (kind: string, fn: () => Promise<any>, ok?: string) => {
    setBusy(kind)
    try {
      onPreview(await fn())
      setSel([])
      if (ok) toast.success(ok)
    } catch (e: any) {
      toast.error(e.message)
    } finally {
      setBusy('')
    }
  }
  const merge = () => {
    const idx = [...sel].sort((a, b) => a - b)
    if (idx.length < 2) return
    const first = idx[0]
    const merged: ImportCh = { ...chs[first], sources: idx.flatMap((i) => chs[i].sources), wordCount: idx.reduce((s, i) => s + chs[i].wordCount, 0) }
    setChs(chs.flatMap((c, i) => (i === first ? [merged] : idx.includes(i) ? [] : [c])))
    setSel([])
  }
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3 rounded-panel border border-line bg-surface px-4 py-3 text-[13px]">
        <FileText className="size-4 text-muted" />
        <span className="font-medium">{preview.fileName}</span>
        <span className="text-muted">共 {num(preview.totalWords)} 字 · 识别出 {chs.length} 章</span>
        <Badge tone={preview.detection === 'size' ? 'warn' : 'accent'}>{detectionText[preview.detection]}</Badge>
        <div className="ml-auto">
          <Button size="sm" icon={<Sparkles className="size-3.5" />} loading={busy === 'ai'} onClick={() => reload('ai', () => api.post(`/studios/${sid}/imports/${preview.id}/ai-detect`), 'AI 已重新识别章节')}>让 AI 识别章节</Button>
        </div>
      </div>
      <Panel padded={false}>
        <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2.5 text-[13px]">
          <Checkbox checked={sel.length === chs.length && chs.length > 0 ? true : sel.length ? 'indeterminate' : false} onChange={(v) => setSel(v ? chs.map((_, i) => i) : [])} />
          <span className="text-muted">已选 {sel.length} 章</span>
          <Button size="xs" icon={<Merge className="size-3.5" />} disabled={sel.length < 2} onClick={merge}>合并为一章</Button>
          <Button size="xs" disabled={!sel.length} onClick={() => setChs(chs.map((c, i) => (sel.includes(i) ? { ...c, included: !c.included } : c)))}>切换是否纳入</Button>
          <div className="ml-auto flex items-center gap-2">
            <span className="text-xs text-muted">按字数重新分集</span>
            <Select size="sm" value={size} onChange={setSize} options={[['2000', '每集约 2000 字'], ['3000', '每集约 3000 字'], ['5000', '每集约 5000 字'], ['8000', '每集约 8000 字']]} className="w-36" />
            <Button size="xs" icon={<Scissors className="size-3.5" />} loading={busy === 'split'} onClick={() => reload('split', () => api.post(`/studios/${sid}/imports/${preview.id}/resplit`, { target: Number(size) }))}>重新分集</Button>
          </div>
        </div>
        <ul className="overflow-y-auto" style={{ maxHeight }}>
          {chs.map((c, i) => (
            <li key={i} className={cn('flex items-start gap-3 border-t border-line px-4 py-2.5 first:border-t-0', !c.included && 'opacity-55')}>
              <Checkbox checked={sel.includes(i)} onChange={(v) => setSel(v ? [...sel, i] : sel.filter((x) => x !== i))} className="mt-1.5" />
              <span className="mt-1.5 w-7 shrink-0 text-xs text-faint tnum">{i + 1}</span>
              <div className="min-w-0 flex-1">
                <input value={c.title} onChange={(e) => setChs(chs.map((x, k) => (k === i ? { ...x, title: e.target.value } : x)))} className="w-full rounded-md bg-transparent px-1 py-0.5 text-[13.5px] font-medium hover:bg-sunken focus:bg-sunken focus:outline-none" />
                <p className="mt-0.5 line-clamp-1 px-1 text-xs text-faint">{c.preview}</p>
              </div>
              <span className="mt-1 shrink-0 text-xs text-muted tnum">{num(c.wordCount)} 字</span>
              {c.kind !== 'body' && <Badge className="mt-1">{c.kind === 'front' ? '正文前' : '正文后'}</Badge>}
              <Switch checked={c.included} onChange={(v) => setChs(chs.map((x, k) => (k === i ? { ...x, included: v } : x)))} />
            </li>
          ))}
        </ul>
      </Panel>
    </div>
  )
}
