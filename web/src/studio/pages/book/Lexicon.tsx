import { useQuery } from '@tanstack/react-query'
import { AnimatePresence, motion } from 'framer-motion'
import { Check, Languages, Plus, Sparkles, Trash2 } from '@/components/icons'
import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { num } from '@/lib/format'
import { Badge, Button, cn, Dialog, Empty, Field, IconButton, Input, Panel, Segmented, Skeleton, Tip } from '@/components/ui'
import { can, invalidate } from '../../hooks'

type Entry = { id: string; scope: 'book' | 'studio' | 'platform'; term: string; pinyin: string; replacement: string; note: string; status: string; occurrences: number; staleSegments?: number }
type CharReading = { char: string; readings: string[]; pinyin: string }

const scopeName: Record<string, string> = { studio: '工作室', platform: '平台' }

/** After a reading changes, voiced lines using the word are marked "changed"; offer the next step right away. */
export function useStaleToast(bookId: string) {
  const nav = useNavigate()
  return (msg: string, stale = 0) => {
    if (stale > 0) toast.success(msg, { description: `用到这个词的 ${num(stale)} 句已配音，需要重新生成才会按新读音读。`, action: { label: '去重新生成', onClick: () => nav(`/books/${bookId}/chapters?generate=stale`) }, duration: 8000 })
    else toast.success(msg)
  }
}

/** Pick each character's tone from a list (no need to type tone marks). Value is "zhēn huán". */
function ReadingPicker({ term, value, onChange, disabled, autofill }: { term: string; value: string; onChange: (v: string) => void; disabled?: boolean; autofill?: boolean }) {
  const q = useQuery<CharReading[]>({ queryKey: ['word-readings', term], queryFn: () => api.get(`/pinyin/word?text=${encodeURIComponent(term)}`), enabled: !!term.trim(), staleTime: Infinity })
  const chars = q.data ?? []
  const han = chars.filter((c) => c.readings.length > 0)
  const given = value.trim().split(/\s+/).filter(Boolean)
  const current = han.map((c, i) => (given.length === han.length ? given[i] : c.pinyin))
  // a fresh word starts with the engine's default reading so saving without changes is still meaningful
  useEffect(() => {
    if (autofill && han.length && given.length !== han.length) onChange(current.join(' '))
  }, [q.data, autofill])
  if (!term.trim()) return <span className="text-xs text-faint">先填写词语</span>
  if (q.isLoading) return <Skeleton className="h-11 w-28" />
  let k = -1
  return (
    <div className="flex flex-wrap items-end gap-1">
      {chars.map((c, i) => {
        if (!c.readings.length) return <span key={i} className="px-0.5 pb-1 text-[15px]">{c.char}</span>
        k++
        const idx = k
        const multi = c.readings.length > 1
        return (
          <label key={i} className={cn('flex min-w-9 flex-col items-center rounded-md px-1 py-0.5', multi && !disabled && 'bg-sunken/70 hover:bg-sunken')}>
            {multi && !disabled ? (
              <select
                aria-label={`「${c.char}」的读音`}
                value={current[idx]}
                onChange={(e) => onChange(current.map((p, j) => (j === idx ? e.target.value : p)).join(' '))}
                className="cursor-pointer appearance-none bg-transparent text-center text-[12px] font-medium text-accent focus:outline-none"
              >
                {(c.readings.includes(current[idx]) ? c.readings : [current[idx], ...c.readings]).map((r) => <option key={r} value={r}>{r}</option>)}
              </select>
            ) : (
              <span className={cn('text-[12px]', multi ? 'font-medium text-accent' : 'text-faint')}>{current[idx]}</span>
            )}
            <span className="text-[15px] leading-tight">{c.char}</span>
          </label>
        )
      })}
    </div>
  )
}

export default function Lexicon({ book }: { book: any }) {
  const { data, isLoading } = useQuery<Entry[]>({ queryKey: ['lexicon', book.id], queryFn: () => api.get(`/books/${book.id}/lexicon`) })
  const editable = can(book, 'script')
  const staleToast = useStaleToast(book.id)
  const [add, setAdd] = useState(false)
  const [extracting, setExtracting] = useState(false)
  const all = data ?? []
  const candidates = all.filter((e) => e.scope === 'book' && e.status === 'candidate')
  const active = all.filter((e) => e.scope === 'book' && e.status === 'active')
  const inherited = all.filter((e) => e.scope !== 'book')
  const refresh = () => invalidate(['lexicon', book.id])

  const extract = async () => {
    setExtracting(true)
    try {
      const found: Entry[] = await api.post(`/books/${book.id}/lexicon/extract`)
      refresh()
      if (found?.length) toast.success(`AI 找到 ${found.length} 个容易读错的词`, { description: '请逐个确认读音，点「采用」后才会生效。' })
      else toast('没有发现新的易错词', { description: '已有的词条不会重复列出。' })
    } finally {
      setExtracting(false)
    }
  }
  const activate = async (ids: string[]) => {
    const r = await api.post(`/books/${book.id}/lexicon/activate`, { ids })
    refresh()
    staleToast(ids.length > 1 ? `已采用 ${r.activated} 个词条` : '已采用，全书配音都会按这个读音', r.staleSegments)
  }
  const patch = async (e: Entry, body: Partial<Entry>, msg = '读音已更新') => {
    const r: Entry = await api.patch(`/lexicon/${e.id}`, body)
    refresh()
    if (e.status === 'active') staleToast(msg, r.staleSegments)
  }
  const remove = async (e: Entry) => {
    const r = await api.del(`/lexicon/${e.id}`)
    refresh()
    if (e.status === 'candidate') toast.success(`已忽略「${e.term}」`)
    else staleToast(`已删除「${e.term}」`, r?.staleSegments)
  }

  if (isLoading) return <div className="space-y-3"><Skeleton className="h-24" /><Skeleton className="h-40" /></div>
  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start gap-4 rounded-panel border border-line bg-surface px-5 py-4">
        <span className="grid size-10 shrink-0 place-items-center rounded-full border border-line text-accent"><Languages className="size-5" /></span>
        <div className="min-w-0 flex-1">
          <h2 className="text-[14px] font-semibold">统一全书读音</h2>
          <p className="mt-1 max-w-2xl text-[13px] leading-relaxed text-muted">
            人名、地名、多音字和生僻字在这里定好读音，所有章节、所有角色都会照着读，不用一句句改。
            建议在生成配音前先让 AI 过一遍书稿，挑出容易读错的词。
          </p>
        </div>
        {editable && (
          <div className="flex shrink-0 gap-2">
            <Button icon={<Sparkles className="size-4" />} loading={extracting} onClick={extract}>{extracting ? 'AI 正在读书稿…' : 'AI 找出易读错的词'}</Button>
            <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setAdd(true)}>添加词条</Button>
          </div>
        )}
      </div>

      <AnimatePresence initial={false}>
        {candidates.length > 0 && (
          <motion.div initial={{ opacity: 0, y: 6 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0 }}>
            <Panel
              title={<span className="flex items-center gap-2">待确认 <Badge tone="warn">{candidates.length}</Badge></span>}
              description="AI 建议的读音，确认无误后点「采用」才会生效；点读音可以改成别的音。"
              padded={false}
              actions={editable && candidates.length > 1 && <Button size="sm" icon={<Check className="size-4" />} onClick={() => activate(candidates.map((c) => c.id))}>全部采用</Button>}
            >
              <EntryTable entries={candidates} editable={editable} candidate onPatch={patch} onRemove={remove} onActivate={(e) => activate([e.id])} />
            </Panel>
          </motion.div>
        )}
      </AnimatePresence>

      <Panel title={`本书词条${active.length ? `（${active.length}）` : ''}`} padded={false}>
        {!active.length ? (
          <Empty
            icon={<Languages />}
            title="还没有词条"
            description={<>比如人名「甄嬛」读 zhēn huán、「乐正」读 yuè zhèng，或者把「Dr.」读成「博士」。<br />改了读音之后，已经配好的句子会标记为「有改动」，在章节列表里选择「有改动的」重新生成即可。</>}
            action={editable && <div className="flex gap-2"><Button icon={<Sparkles className="size-4" />} loading={extracting} onClick={extract}>AI 找出易读错的词</Button><Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setAdd(true)}>添加词条</Button></div>}
            className="py-12"
          />
        ) : (
          <EntryTable entries={active} editable={editable} onPatch={patch} onRemove={remove} />
        )}
      </Panel>

      {inherited.length > 0 && (
        <Panel title="同时生效的通用词条" description={<>来自工作室词典和平台词典，这本书里也出现了。本书词条优先。{editable && <> 修改工作室词条请到 <Link to="/studio/lexicon" className="text-accent">工作室设置 → 读音词典</Link>。</>}</>} padded={false}>
          <EntryTable entries={inherited} editable={false} />
        </Panel>
      )}

      <AddEntry open={add} onOpenChange={setAdd} book={book} existing={active.concat(candidates)} onAdded={(e) => { refresh(); staleToast(`已添加「${e.term}」`, e.staleSegments) }} />
    </div>
  )
}

function EntryTable({ entries, editable, candidate, onPatch, onRemove, onActivate }: { entries: Entry[]; editable: boolean; candidate?: boolean; onPatch?: (e: Entry, body: Partial<Entry>, msg?: string) => Promise<void>; onRemove?: (e: Entry) => Promise<void>; onActivate?: (e: Entry) => Promise<void> }) {
  return (
    <ul>
      <li className="hidden grid-cols-[minmax(7rem,1fr)_minmax(9rem,1.4fr)_minmax(0,1.6fr)_4.5rem_auto] gap-4 border-b border-line px-5 py-2 text-xs text-faint sm:grid">
        <span>词语</span><span>读法</span><span>说明</span><span className="text-right">出现</span><span className="w-[7.5rem]" />
      </li>
      <AnimatePresence initial={false}>
        {entries.map((e) => (
          <motion.li key={e.id} layout initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0, height: 0 }} className="grid grid-cols-[1fr_auto] items-center gap-x-4 gap-y-2 border-t border-line px-5 py-3 first:border-t-0 sm:grid-cols-[minmax(7rem,1fr)_minmax(9rem,1.4fr)_minmax(0,1.6fr)_4.5rem_auto]">
            <div className="flex min-w-0 items-center gap-2">
              <span className="truncate text-[14px] font-medium">{e.term}</span>
              {e.scope !== 'book' && <Badge>{scopeName[e.scope]}</Badge>}
            </div>
            <div className="col-span-2 row-start-2 sm:col-span-1 sm:row-start-auto">
              {e.replacement ? (
                <span className="text-[13px]">读作「{e.replacement}」</span>
              ) : (
                <ReadingPicker term={e.term} value={e.pinyin} disabled={!editable || !onPatch} onChange={(v) => v !== e.pinyin && onPatch?.(e, { pinyin: v })} />
              )}
            </div>
            <span className="col-span-2 row-start-3 truncate text-[12.5px] text-muted sm:col-span-1 sm:row-start-auto">{e.note || '—'}</span>
            <span className="hidden text-right text-xs text-muted tnum sm:block">{e.occurrences ? `${num(e.occurrences)} 次` : <Tip label="正文里没有找到这个词"><span className="text-faint">0 次</span></Tip>}</span>
            <div className="col-start-2 row-start-1 flex w-[7.5rem] justify-end gap-1 sm:col-start-auto sm:row-start-auto">
              {candidate && editable && onActivate && <Button size="xs" variant="primary" onClick={() => onActivate(e)}>采用</Button>}
              {editable && onRemove && (candidate ? <Button size="xs" variant="ghost" onClick={() => onRemove(e)}>忽略</Button> : <IconButton label="删除词条" size="xs" icon={<Trash2 className="size-3.5" />} onClick={() => onRemove(e)} />)}
            </div>
          </motion.li>
        ))}
      </AnimatePresence>
    </ul>
  )
}

function AddEntry({ open, onOpenChange, book, existing, onAdded }: { open: boolean; onOpenChange: (v: boolean) => void; book: any; existing: Entry[]; onAdded: (e: Entry) => void }) {
  const [term, setTerm] = useState('')
  const [kind, setKind] = useState<'pinyin' | 'replace'>('pinyin')
  const [pinyin, setPinyin] = useState('')
  const [replacement, setReplacement] = useState('')
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  const [debounced, setDebounced] = useState('')
  useEffect(() => {
    const t = setTimeout(() => setDebounced(term.trim()), 300)
    return () => clearTimeout(t)
  }, [term])
  useEffect(() => {
    if (open) {
      setTerm(''); setPinyin(''); setReplacement(''); setNote(''); setKind('pinyin')
    }
  }, [open])
  useEffect(() => setPinyin(''), [debounced])
  const dup = useMemo(() => existing.find((e) => e.term === term.trim()), [existing, term])
  const hasHan = /\p{Script=Han}/u.test(term)
  const ok = term.trim() && !dup && (kind === 'replace' ? replacement.trim() : hasHan && pinyin)
  const save = async () => {
    if (!ok) return
    setBusy(true)
    try {
      const e: Entry = await api.post(`/books/${book.id}/lexicon`, { term: term.trim(), pinyin: kind === 'pinyin' ? pinyin : '', replacement: kind === 'replace' ? replacement.trim() : '', note: note.trim() })
      onAdded(e)
      onOpenChange(false)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="添加词条" description="对这本书的所有章节、所有角色生效。" width={480}
      footer={<><Button onClick={() => onOpenChange(false)}>取消</Button><Button variant="primary" loading={busy} disabled={!ok} onClick={save}>添加</Button></>}>
      <div className="space-y-5">
        <Field label="词语" error={dup ? `「${dup.term}」已经在列表里了` : undefined}>
          <Input autoFocus value={term} onChange={(e) => setTerm(e.target.value)} placeholder="人名、地名、多音字词语……" maxLength={20} onKeyDown={(e) => e.key === 'Enter' && save()} />
        </Field>
        <Field label="怎么读">
          <Segmented value={kind} onChange={setKind} size="sm" items={[{ value: 'pinyin', label: '指定读音' }, { value: 'replace', label: '读成别的文字' }]} />
        </Field>
        {kind === 'pinyin' ? (
          <div className="rounded-[10px] bg-sunken/50 px-4 py-3">
            {debounced && !hasHan ? <p className="text-[13px] text-muted">英文、数字和符号请选「读成别的文字」，例如把「Dr.」读成「博士」。</p> : <ReadingPicker term={debounced} value={pinyin} onChange={setPinyin} autofill />}
            {debounced && hasHan && <p className="mt-2 text-[11.5px] text-faint">点高亮的拼音可以换成这个字的其他读音。</p>}
          </div>
        ) : (
          <Field label="读作" hint="合成时会把词语换成这里的文字来读，原文不变。">
            <Input value={replacement} onChange={(e) => setReplacement(e.target.value)} placeholder="例如：博士" onKeyDown={(e) => e.key === 'Enter' && save()} />
          </Field>
        )}
        <Field label="说明（可选）"><Input value={note} onChange={(e) => setNote(e.target.value)} placeholder="例如：男主角，复姓" onKeyDown={(e) => e.key === 'Enter' && save()} /></Field>
      </div>
    </Dialog>
  )
}
