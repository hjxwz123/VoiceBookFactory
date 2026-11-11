import { create } from 'zustand'
import { api } from '@/lib/api'
import { queryClient, scriptBusy } from '@/lib/live'
import { toast } from 'sonner'

/** Where a new line is being typed: right after `afterId` (null = at the very top). */
export type Draft = { afterId: string | null; key: number }

type WS = {
  bookId: string
  chapterId: string
  focus: string | null
  selected: string[]
  anchor: string | null
  panel: string
  editing: string | null
  draft: Draft | null
  splitting: string | null
  setFocus: (id: string | null) => void
  click: (id: string, e: { shiftKey?: boolean; metaKey?: boolean; ctrlKey?: boolean }, order: string[]) => void
  clear: () => void
  setPanel: (p: string) => void
  setEditing: (id: string | null) => void
  startDraft: (afterId: string | null) => void
  cancelDraft: () => void
}

export const useWS = create<WS>((set, get) => ({
  bookId: '',
  chapterId: '',
  focus: null,
  selected: [],
  anchor: null,
  panel: 'segment',
  editing: null,
  draft: null,
  splitting: null,
  setFocus: (id) => set({ focus: id, selected: id ? [id] : [], anchor: id }),
  click: (id, e, order) => {
    const s = get()
    if (e.shiftKey && s.anchor) {
      const a = order.indexOf(s.anchor)
      const b = order.indexOf(id)
      const [lo, hi] = a < b ? [a, b] : [b, a]
      set({ selected: order.slice(lo, hi + 1), focus: id })
    } else if (e.metaKey || e.ctrlKey) {
      set({ selected: s.selected.includes(id) ? s.selected.filter((x) => x !== id) : [...s.selected, id], focus: id, anchor: id })
    } else set({ selected: [id], focus: id, anchor: id })
  },
  clear: () => set({ selected: [], focus: null, anchor: null }),
  setPanel: (p) => set({ panel: p }),
  setEditing: (id) => set({ editing: id }),
  startDraft: (afterId) => set({ draft: { afterId, key: Date.now() }, editing: null }),
  cancelDraft: () => set({ draft: null }),
}))

// ---------------------------------------------------------------------------
// Save status for the header ("保存中… / 已保存 / 保存失败").
// ---------------------------------------------------------------------------
type SaveState = { pending: number; savedAt: number; failed: string }
export const useSave = create<SaveState>(() => ({ pending: 0, savedAt: 0, failed: '' }))

async function tracked<T>(p: Promise<T>): Promise<T> {
  useSave.setState((s) => ({ pending: s.pending + 1, failed: '' }))
  try {
    const r = await p
    useSave.setState({ savedAt: Date.now() })
    return r
  } catch (e: any) {
    useSave.setState({ failed: e?.message ?? '保存失败' })
    throw e
  } finally {
    useSave.setState((s) => ({ pending: Math.max(0, s.pending - 1) }))
  }
}

// ---------------------------------------------------------------------------
// Undo (Ctrl+Z / the header button). Each edit records how to put things back; undoing never records
// a new entry. Split and merge record a notice instead, so Ctrl+Z never silently skips past them.
// ---------------------------------------------------------------------------
type UndoEntry = { label: string; undo: () => unknown }
const stacks = new Map<string, UndoEntry[]>()
export const useUndo = create<{ chapterId: string; count: number; label: string }>(() => ({ chapterId: '', count: 0, label: '' }))
let undoing = false
function syncUndo(chapterId: string) {
  const st = stacks.get(chapterId) ?? []
  useUndo.setState({ chapterId, count: st.length, label: st[st.length - 1]?.label ?? '' })
}
function pushUndo(chapterId: string, e: UndoEntry) {
  if (undoing) return e
  const st = stacks.get(chapterId) ?? []
  st.push(e)
  if (st.length > 50) st.shift()
  stacks.set(chapterId, st)
  syncUndo(chapterId)
  return e
}
function dropUndo(chapterId: string, e: UndoEntry) {
  const st = stacks.get(chapterId)
  if (st) stacks.set(chapterId, st.filter((x) => x !== e))
  syncUndo(chapterId)
}
export function undoStatus(chapterId: string) {
  syncUndo(chapterId)
}
export async function undoLast(chapterId: string) {
  const st = stacks.get(chapterId) ?? []
  const e = st.pop()
  syncUndo(chapterId)
  if (!e) return toast('没有可以撤销的操作')
  undoing = true
  let r: unknown
  try {
    r = e.undo()
  } finally {
    undoing = false
  }
  if (r === 'notice') return
  await r
  toast.success(`已撤销：${e.label}`, { duration: 2000 })
}

// ---------------------------------------------------------------------------
// Structural edits (insert / move / split / merge) change the order of lines. They are applied to the
// screen at once and sent to the server strictly one after another, so quick repeated clicks or drags on
// a slow network all land, in the order made. New lines get a temporary id until the server answers.
// ---------------------------------------------------------------------------
const tempIds = new Map<string, string>()
export const realId = (id: string) => tempIds.get(id) ?? id
export const isTemp = (id: string) => id.startsWith('tmp-') && !tempIds.has(id)
let chain: Promise<unknown> = Promise.resolve()
let batchFailed = false

function remapIds() {
  const map = (id: string | null) => (id ? realId(id) : id)
  useWS.setState((s) => ({
    focus: map(s.focus),
    anchor: map(s.anchor),
    editing: map(s.editing),
    selected: s.selected.map((id) => realId(id)),
    draft: s.draft && { ...s.draft, afterId: map(s.draft.afterId) },
  }))
}

function enqueue(chapterId: string, task: () => Promise<any[] | null | undefined>): Promise<any[] | null | undefined> {
  scriptBusy.set(chapterId, (scriptBusy.get(chapterId) ?? 0) + 1)
  const run = chain.then(task)
  chain = run.catch(() => {})
  const settle = () => {
    const left = (scriptBusy.get(chapterId) ?? 1) - 1
    if (left > 0) scriptBusy.set(chapterId, left)
    else scriptBusy.delete(chapterId)
    return left
  }
  return tracked(run).then(
    (list) => {
      if (settle() === 0) {
        if (batchFailed || !Array.isArray(list)) refetchScript(chapterId)
        else setSegments(chapterId, list)
        batchFailed = false
        remapIds()
      }
      return list
    },
    (e: any) => {
      batchFailed = true
      toast.error(e?.message ?? '保存失败', { description: '已恢复为服务器上的内容，请再试一次。' })
      if (settle() === 0) {
        batchFailed = false
        refetchScript(chapterId)
        remapIds()
      }
      return null
    },
  )
}

const key = (chapterId: string) => ['script', chapterId]
function setSegments(chapterId: string, segments: any[]) {
  queryClient.setQueryData(key(chapterId), (old: any) => old && { ...old, segments })
}
function localSegments(chapterId: string): any[] {
  return queryClient.getQueryData<any>(key(chapterId))?.segments ?? []
}

/** Waits for queued structural edits when ids include lines the server has not created yet. */
async function resolveIds(ids: string[]) {
  if (ids.some(isTemp)) await chain
  return ids.map(realId)
}

/** Moves lines (kept in their current order) to right after `afterId` (null = top). */
export function moveSegments(chapterId: string, ids: string[], afterId: string | null) {
  const all = localSegments(chapterId)
  const moving = all.filter((s) => ids.includes(s.id))
  if (!moving.length || (afterId && ids.includes(afterId))) return Promise.resolve(null)
  const rest = all.filter((s) => !ids.includes(s.id))
  const at = afterId ? rest.findIndex((s) => s.id === afterId) + 1 : 0
  if (afterId && at === 0) return Promise.resolve(null)
  const para = at > 0 ? rest[at - 1].para : rest[0]?.para ?? 0
  const next = [...rest.slice(0, at), ...moving.map((s) => ({ ...s, para })), ...rest.slice(at)]
  if (next.every((s, i) => s.id === all[i]?.id)) return Promise.resolve(null)
  const back = moving.map((s) => ({ id: s.id, after: all[all.findIndex((x) => x.id === s.id) - 1]?.id ?? null }))
  pushUndo(chapterId, { label: moving.length > 1 ? `移动 ${moving.length} 句` : '移动句子', undo: () => Promise.all(back.map((b) => moveSegments(chapterId, [b.id], b.after))) })
  setSegments(chapterId, next)
  return enqueue(chapterId, async () => {
    let prev = afterId
    let list: any[] | null = null
    for (const s of moving) {
      list = await api.post(`/segments/${realId(s.id)}/move`, { afterId: prev ? realId(prev) : '' })
      prev = s.id
    }
    return list
  })
}

/** Moves the given lines one visible step up or down (Alt+↑/↓, the ↑↓ buttons). */
export function moveBy(chapterId: string, visible: any[], ids: string[], dir: -1 | 1) {
  const order = visible.map((s) => s.id)
  const idx = ids.map((id) => order.indexOf(id)).filter((i) => i >= 0).sort((a, b) => a - b)
  if (!idx.length) return Promise.resolve(null)
  if (dir < 0) {
    if (idx[0] === 0) return Promise.resolve(null)
    return moveSegments(chapterId, idx.map((i) => order[i]), idx[0] >= 2 ? order[idx[0] - 2] : null)
  }
  const last = idx[idx.length - 1]
  if (last >= order.length - 1) return Promise.resolve(null)
  return moveSegments(chapterId, idx.map((i) => order[i]), order[last + 1])
}

/** Inserts a line after `afterId` (null = top). Returns the temporary id shown until the server answers. */
export function insertSegment(chapterId: string, afterId: string | null, line: { text: string; characterId: string; type: string }) {
  const tmp = `tmp-${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`
  const all = localSegments(chapterId)
  const at = afterId ? all.findIndex((s) => s.id === afterId) + 1 : 0
  const prev = at > 0 ? all[at - 1] : all[0]
  const row = {
    id: tmp, ...line, para: prev?.para ?? 0, emotion: 'neutral', intensity: 1, audioStatus: 'none', modified: true, prons: [], cues: [], tags: [], effects: [], pending: true,
  }
  setSegments(chapterId, [...all.slice(0, at), row, ...all.slice(at)])
  pushUndo(chapterId, { label: '添加句子', undo: () => removeQuietly(chapterId, [tmp]) })
  enqueue(chapterId, async () => {
    const before = new Set(localSegments(chapterId).map((s) => realId(s.id)))
    const list: any[] = await api.post(`/chapters/${chapterId}/segments`, { afterId: afterId ? realId(afterId) : '', ...line })
    const anchor = afterId ? list.findIndex((s) => s.id === realId(afterId)) : -1
    const created = list[anchor + 1]?.text === line.text && !before.has(list[anchor + 1]?.id) ? list[anchor + 1] : list.find((s) => !before.has(s.id) && s.text === line.text)
    if (created) tempIds.set(tmp, created.id)
    return list
  })
  return tmp
}

export function splitSegment(chapterId: string, id: string, at: number) {
  pushUndo(chapterId, { label: '拆分', undo: () => (toast('拆分不能自动撤销', { description: '选中拆开的两句，点「合并」即可恢复。' }), 'notice') })
  return enqueue(chapterId, async () => api.post(`/segments/${realId(id)}/split`, { at }))
}

export function mergeSegments(bookId: string, chapterId: string, ids: string[]) {
  pushUndo(chapterId, { label: '合并', undo: () => (toast('合并不能自动撤销', { description: '可以用「拆分」把它重新分开。' }), 'notice') })
  return enqueue(chapterId, async () => api.post(`/books/${bookId}/segments/merge`, { ids: ids.map(realId) }))
}

/** Removes lines without an undo toast (used to undo "添加句子"). */
function removeQuietly(chapterId: string, ids: string[]) {
  const { bookId } = useWS.getState()
  queryClient.setQueryData(key(chapterId), (old: any) => old && { ...old, segments: old.segments.filter((s: any) => !ids.includes(s.id)) })
  useWS.setState((st) => ({ selected: st.selected.filter((id) => !ids.includes(id)), focus: st.focus && ids.includes(st.focus) ? null : st.focus }))
  return enqueue(chapterId, async () => {
    await api.post(`/books/${bookId}/segments/delete`, { ids: ids.map(realId) })
    return api.get(`/chapters/${chapterId}/script`).then((r: any) => r.segments)
  })
}

const UNDOABLE = ['text', 'characterId', 'type', 'emotion', 'intensity', 'locked', 'instruction', 'note', 'cues', 'tags', 'prons', 'needsReview', 'speed', 'pitch', 'volume', 'pauseBeforeMs', 'pauseAfterMs']
function patchLabel(patch: Record<string, any>) {
  if ('text' in patch) return '修改文字'
  if ('characterId' in patch) return '更换说话人'
  if ('emotion' in patch || 'intensity' in patch) return '修改情绪'
  if ('locked' in patch) return patch.locked ? '锁定' : '解除锁定'
  if ('cues' in patch || 'tags' in patch) return '修改表演标签'
  if ('prons' in patch) return '修改读音'
  if ('speed' in patch || 'pitch' in patch || 'volume' in patch) return '调整语速音高'
  if ('pauseBeforeMs' in patch || 'pauseAfterMs' in patch) return '调整停顿'
  return '修改'
}

// ---------------------------------------------------------------------------
// Field edits
// ---------------------------------------------------------------------------

/** Optimistic batch patch of segments; merges server response into the script cache. */
export async function patchSegments(bookId: string, chapterId: string, ids: string[], patch: Record<string, any>) {
  const prev = queryClient.getQueryData<any>(key(chapterId))
  const entry = recordPatchUndo(bookId, chapterId, ids, patch, prev?.segments ?? [])
  queryClient.setQueryData(key(chapterId), (old: any) => old && { ...old, segments: old.segments.map((s: any) => (ids.includes(s.id) ? { ...s, ...patch, modified: true } : s)) })
  try {
    const real = await resolveIds(ids)
    const res = await tracked(api.patch(`/books/${bookId}/segments`, { ids: real, patch }))
    if (Array.isArray(res)) mergeSegments_(chapterId, res)
    return res
  } catch (e: any) {
    queryClient.setQueryData(key(chapterId), prev)
    if (entry) dropUndo(chapterId, entry)
    toast.error(e.message)
    throw e
  }
}

/** Records how to restore the patched fields; lines with the same old values are restored together.
 *  Fields that were unset ("自动") cannot be put back through the API, so such patches are not undoable. */
function recordPatchUndo(bookId: string, chapterId: string, ids: string[], patch: Record<string, any>, segments: any[]): UndoEntry | null {
  const keys = Object.keys(patch)
  if (!keys.length || !keys.every((k) => UNDOABLE.includes(k))) return null
  const empty: Record<string, any> = { cues: [], tags: [], prons: [], text: '', instruction: '', note: '' }
  const groups = new Map<string, { ids: string[]; patch: Record<string, any> }>()
  for (const s of segments.filter((x) => ids.includes(x.id))) {
    const old: Record<string, any> = {}
    for (const k of keys) old[k] = s[k] ?? empty[k]
    if (keys.some((k) => old[k] === undefined || old[k] === null)) return null
    const g = JSON.stringify(old)
    groups.set(g, { ids: [...(groups.get(g)?.ids ?? []), s.id], patch: old })
  }
  if (!groups.size) return null
  return pushUndo(chapterId, { label: patchLabel(patch), undo: () => Promise.all([...groups.values()].map((g) => patchSegments(bookId, chapterId, g.ids, g.patch))) })
}

function mergeSegments_(chapterId: string, list: any[]) {
  queryClient.setQueryData(key(chapterId), (old: any) => {
    if (!old?.segments) return old
    const map = new Map(list.map((s) => [s.id, s]))
    return { ...old, segments: old.segments.map((s: any) => (map.has(realId(s.id)) ? { ...s, ...map.get(realId(s.id)) } : s)) }
  })
}
export { mergeSegments_ as mergeSegmentData }

export const refetchScript = (chapterId: string) => queryClient.invalidateQueries({ queryKey: key(chapterId) })

/** Delete lines with an optimistic update and an undo toast (lines and their audio come back on undo). */
export async function deleteSegments(bookId: string, chapterId: string, ids: string[]) {
  const prev = queryClient.getQueryData<any>(key(chapterId))
  const order: string[] = (prev?.segments ?? []).filter((s: any) => !s.removed).map((s: any) => s.id)
  const { focus } = useWS.getState()
  // move focus to the next surviving line so keyboard editing can continue
  if (focus && ids.includes(focus)) {
    const i = order.indexOf(focus)
    const next = order.slice(i + 1).find((id) => !ids.includes(id)) ?? order.slice(0, i).reverse().find((id) => !ids.includes(id)) ?? null
    useWS.setState({ focus: next, selected: next ? [next] : [], anchor: next })
  } else {
    useWS.setState((st) => ({ selected: st.selected.filter((id) => !ids.includes(id)) }))
  }
  queryClient.setQueryData(key(chapterId), (old: any) => old && { ...old, segments: old.segments.filter((s: any) => !ids.includes(s.id)) })
  try {
    const real = await resolveIds(ids)
    const res = await tracked(api.post(`/books/${bookId}/segments/delete`, { ids: real }))
    const deleted = res?.deleted ?? []
    if (res?.locked) toast.warning(`${res.locked} 句已锁定，未删除`)
    if (deleted.length) {
      const restore = async () => {
        await tracked(api.post(`/books/${bookId}/segments/restore`, { segments: deleted }))
        refetchScript(chapterId)
      }
      const entry = pushUndo(chapterId, { label: deleted.length > 1 ? `删除 ${deleted.length} 句` : '删除句子', undo: restore })
      toast.success(deleted.length === 1 ? '已删除 1 句' : `已删除 ${deleted.length} 句`, {
        description: `可以点「撤销」或按 ${typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘' : 'Ctrl'}+Z 恢复`,
        duration: 8000,
        action: {
          label: '撤销',
          onClick: async () => {
            dropUndo(chapterId, entry)
            await restore()
            toast.success('已恢复')
          },
        },
      })
    }
  } catch (e: any) {
    queryClient.setQueryData(key(chapterId), prev)
    toast.error(e.message)
  } finally {
    if (!scriptBusy.get(chapterId)) refetchScript(chapterId)
  }
}

// ---------------------------------------------------------------------------
// Proof-listening marks
// ---------------------------------------------------------------------------

const bumpIssues = (chapterId: string, segId: string, d: number, title?: string) =>
  queryClient.setQueryData(key(chapterId), (old: any) => old && {
    ...old,
    segments: old.segments.map((s: any) => (s.id === segId ? { ...s, openIssues: Math.max(0, (s.openIssues ?? 0) + d), issueTitle: title ?? s.issueTitle } : s)),
  })

/** Marks a line for rework without stopping playback (the "M" key while proof-listening). Shows at once,
 *  saves in the background, and can be taken back from the toast. */
export async function markForRework(bookId: string, chapterId: string, seg: any, lineNo: number) {
  if (isTemp(seg.id)) return
  bumpIssues(chapterId, seg.id, 1, '需要返工')
  try {
    const r = await tracked(api.post(`/books/${bookId}/issues`, { chapterId, segmentId: realId(seg.id), type: 'other', title: '需要返工', atMs: 0 }))
    queryClient.invalidateQueries({ queryKey: ['issues'] })
    toast.success(`已标记第 ${lineNo} 句需要返工`, {
      id: 'rework-mark',
      description: '播放不会停。听完后在右侧「审听」里统一重新生成或补充说明。',
      action: {
        label: '撤回',
        onClick: async () => {
          bumpIssues(chapterId, seg.id, -1)
          try {
            await api.post(`/issues/${r.issue.id}/action`, { action: 'withdraw' })
            queryClient.invalidateQueries({ queryKey: ['issues'] })
            toast.success('已撤回标记', { id: 'rework-mark' })
          } catch (e: any) {
            bumpIssues(chapterId, seg.id, 1)
            toast.error(e.message)
          }
        },
      },
    })
  } catch (e: any) {
    bumpIssues(chapterId, seg.id, -1)
    toast.error(e.message)
  }
}
