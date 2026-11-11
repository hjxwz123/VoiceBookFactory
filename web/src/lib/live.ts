import { QueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'
import { toast } from 'sonner'
import { create } from 'zustand'
import { useShallow } from 'zustand/react/shallow'
import { api } from './api'
import { useSession } from './session'
import { ApiError } from './api'

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 15_000, refetchOnWindowFocus: false, retry: (n, e: any) => n < 1 && !(e instanceof ApiError && e.status < 500) },
    mutations: { onError: (e: any) => toast.error(e?.message ?? '操作失败') }, // plan limits are handled globally (components/upgrade)
  },
})

export type LiveJob = { id: string; type: string; status: string; progress: number; progressText: string; title: string; bookId: string; chapterId: string; error: string; refId: string }

type LiveState = {
  jobs: Record<string, LiveJob>
  presence: Record<string, any[]>
  connected: boolean
}
export const useLive = create<LiveState>(() => ({ jobs: {}, presence: {}, connected: false }))

const inv = (key: any[]) => queryClient.invalidateQueries({ queryKey: key })

/** Chapters whose script has local changes still being saved in order (workspace/store.ts). While busy,
 *  "script changed" events are ignored: refetching then would briefly undo the user's own moves, and the
 *  editor loads the server's final order itself once the queue is empty. */
export const scriptBusy = new Map<string, number>()
const invScript = (chapterId: string) => {
  if (!scriptBusy.get(chapterId)) inv(['script', chapterId])
}

function onJob(j: LiveJob) {
  useLive.setState((s) => ({ jobs: { ...s.jobs, [j.id]: j } }))
  if (j.status === 'succeeded' || j.status === 'failed' || j.status === 'canceled') {
    inv(['jobs'])
    if (j.bookId) {
      inv(['book', j.bookId])
      inv(['chapters', j.bookId])
      inv(['characters', j.bookId])
      inv(['exports', j.bookId])
      inv(['pipeline', j.bookId])
    }
    if (j.chapterId) {
      ;['timeline', 'renders', 'reviews', 'recordings', 'chapter'].forEach((k) => inv([k, j.chapterId]))
      invScript(j.chapterId)
    }
    if (j.type === 'voice_design' || j.type === 'voice_clone') inv(['voices'])
    if (j.type.endsWith('_generate')) inv(['assets'])
    if (j.type === 'image_generate' && j.bookId) {
      inv(['covers', j.bookId])
      inv(['books'])
    }
    if (j.type === 'quick_tts') inv(['quick'])
    inv(['credits'])
    setTimeout(() => useLive.setState((s) => {
      const { [j.id]: _, ...rest } = s.jobs
      return { jobs: rest }
    }), 4000)
  }
}

function onSegments(chapterId: string, list: any[]) {
  queryClient.setQueryData(['script', chapterId], (old: any) => {
    if (!old?.segments) return old
    const map = new Map(list.map((s) => [s.id, s]))
    return { ...old, segments: old.segments.map((s: any) => (map.has(s.id) ? { ...s, ...map.get(s.id) } : s)) }
  })
  inv(['timeline', chapterId])
}

export function useLiveEvents() {
  const user = useSession((s) => s.user)
  useEffect(() => {
    if (!user) return
    let es: EventSource | null = null
    let closed = false
    let retry: any
    const connect = () => {
      const token = api.token
      if (!token || closed) return
      es = new EventSource(`/api/v1/events?access_token=${encodeURIComponent(token)}`)
      es.onopen = () => useLive.setState({ connected: true })
      es.onerror = () => {
        useLive.setState({ connected: false })
        es?.close()
        if (!closed) retry = setTimeout(async () => {
          await api.refresh()
          connect()
        }, 3000)
      }
      const on = (type: string, fn: (ev: any) => void) => es!.addEventListener(type, (m: MessageEvent) => {
        try {
          fn(JSON.parse(m.data))
        } catch {}
      })
      on('job', (ev) => onJob(ev.data))
      on('segments', (ev) => onSegments(ev.chapterId, ev.data ?? []))
      on('chapter', (ev) => {
        ;['timeline', 'renders', 'reviews', 'comments', 'chapter'].forEach((k) => inv([k, ev.chapterId]))
        invScript(ev.chapterId)
        inv(['chapters', ev.bookId])
        inv(['delivery-check', ev.bookId])
        if (ev.data?.scope === 'issues') inv(['issues'])
      })
      on('book', (ev) => {
        const scope = ev.data?.scope
        if (scope === 'characters') inv(['characters', ev.bookId])
        else if (scope === 'tasks') inv(['tasks'])
        else if (scope === 'issues') inv(['issues'])
        else if (scope === 'pipeline') inv(['pipeline', ev.bookId])
        inv(['book', ev.bookId])
        inv(['delivery-check', ev.bookId])
      })
      on('presence', (ev) => useLive.setState((s) => ({ presence: { ...s.presence, [ev.chapterId]: ev.data ?? [] } })))
      on('notification', (ev) => {
        useSession.setState((s) => ({ unread: s.unread + 1 }))
        inv(['notifications'])
        const n = ev.data
        if (n?.type !== 'job_done') toast(n?.title, { description: n?.body })
      })
    }
    connect()
    return () => {
      closed = true
      clearTimeout(retry)
      es?.close()
    }
  }, [user?.id])
}

/** Live job (if any) for a target, e.g. the running script parse of a chapter. */
export function useRunningJobs(filter: (j: LiveJob) => boolean) {
  return useLive(useShallow((s) => Object.values(s.jobs).filter((j) => (j.status === 'queued' || j.status === 'running') && filter(j))))
}
