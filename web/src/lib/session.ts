import { create } from 'zustand'
import { api } from './api'

export type User = {
  id: string; email: string; phone?: string; nickname: string; avatarUrl: string; bio: string; adminRoles: string[]; preferences: any; cv: any
  realNameStatus: string; personalStudioId: string; emailVerified: boolean; hasPassword?: boolean; deletionAt?: string | null
  legalVersion?: string; legalAgreedAt?: string | null
}
export type StudioRef = { id: string; name: string; type: string; logo: string; role: string; status: string; plan: string; planCode: string }

type State = {
  ready: boolean
  user: User | null
  studios: StudioRef[]
  studioId: string
  unread: number
  membership: { planName: string; planCode: string; level: number; expiresAt: string | null; isDefault: boolean } | null
  site: any
  setStudio: (id: string) => void
  load: () => Promise<void>
  signIn: (token: string) => Promise<void>
  signOut: () => Promise<void>
}

export const useSession = create<State>((set, get) => ({
  ready: false,
  user: null,
  studios: [],
  studioId: localStorage.getItem('vb-studio') || '',
  unread: 0,
  membership: null,
  site: null,
  setStudio: (id) => {
    localStorage.setItem('vb-studio', id)
    set({ studioId: id })
  },
  load: async () => {
    const me = await api.get('/me')
    const studios: StudioRef[] = (me.studios ?? []).filter((s: StudioRef) => s.status === 'active')
    let sid = get().studioId
    if (!studios.find((s) => s.id === sid)) sid = me.user.personalStudioId || studios[0]?.id || ''
    localStorage.setItem('vb-studio', sid)
    set({ user: me.user, studios, studioId: sid, unread: me.unread, membership: me.membership ?? null })
  },
  signIn: async (token) => {
    api.setToken(token)
    await get().load()
  },
  signOut: async () => {
    await api.post('/auth/logout').catch(() => {})
    api.setToken(null)
    set({ user: null, studios: [] })
  },
}))

export const useStudio = () => {
  const { studios, studioId } = useSession()
  return studios.find((s) => s.id === studioId) ?? studios[0]
}

export const sid = () => useSession.getState().studioId

export function canManage(role?: string) {
  return role === 'owner' || role === 'admin'
}
export function canCreateBooks(role?: string) {
  return role === 'owner' || role === 'admin' || role === 'producer'
}
