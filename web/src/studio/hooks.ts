import { useMutation, useQuery } from '@tanstack/react-query'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { queryClient } from '@/lib/live'
import { useSession } from '@/lib/session'

export const useSid = () => useSession((s) => s.studioId)

export function useBook(bid?: string) {
  return useQuery({ queryKey: ['book', bid], queryFn: () => api.get(`/books/${bid}`), enabled: !!bid })
}
export function useChapters(bid?: string) {
  return useQuery<any[]>({ queryKey: ['chapters', bid], queryFn: () => api.get(`/books/${bid}/chapters`), enabled: !!bid })
}
export function useCharacters(bid?: string) {
  return useQuery<any[]>({ queryKey: ['characters', bid], queryFn: () => api.get(`/books/${bid}/characters`), enabled: !!bid })
}
export function useStudioInfo() {
  const sid = useSid()
  return useQuery({ queryKey: ['studio', sid], queryFn: () => api.get(`/studios/${sid}`), enabled: !!sid })
}
export function useMembers() {
  const sid = useSid()
  return useQuery<any[]>({ queryKey: ['members', sid], queryFn: () => api.get(`/studios/${sid}/members`), enabled: !!sid })
}

export const can = (book: any, perm: string) => !!book?.permissions?.includes(perm)

/** Mutation with toast + query invalidation. */
export function useAction<T = any, V = any>(fn: (v: V) => Promise<T>, opts: { success?: string | ((r: T) => string); invalidate?: any[][]; onDone?: (r: T) => void } = {}) {
  return useMutation({
    mutationFn: fn,
    onSuccess: (r) => {
      if (opts.success) toast.success(typeof opts.success === 'function' ? opts.success(r) : opts.success)
      opts.invalidate?.forEach((k) => queryClient.invalidateQueries({ queryKey: k }))
      opts.onDone?.(r)
    },
  })
}

export const invalidate = (...keys: any[][]) => keys.forEach((k) => queryClient.invalidateQueries({ queryKey: k }))
