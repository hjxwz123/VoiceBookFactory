export class ApiError extends Error {
  status: number
  code: string
  data: any
  constructor(status: number, code: string, message: string, data?: any) {
    super(message)
    this.status = status
    this.code = code
    this.data = data
  }
}

export type Page<T> = { items: T[]; total: number; page: number; pageSize: number }

/** Requests that change something (POST/PUT/PATCH/DELETE) still waiting for the server; drives the
 *  global activity bar so a slow network never looks like a click that did nothing. */
type NetListener = (pending: number) => void
let pendingWrites = 0
const netListeners = new Set<NetListener>()
export const net = {
  get pending() {
    return pendingWrites
  },
  subscribe(l: NetListener) {
    netListeners.add(l)
    return () => {
      netListeners.delete(l)
    }
  },
}
function trackWrite(delta: number) {
  pendingWrites = Math.max(0, pendingWrites + delta)
  netListeners.forEach((l) => l(pendingWrites))
}

type Listener = (token: string | null) => void

export function createClient(prefix: string) {
  let token: string | null = null
  let refreshing: Promise<boolean> | null = null
  let errorHook: ((e: ApiError) => void) | null = null
  const listeners = new Set<Listener>()

  const setToken = (t: string | null) => {
    token = t
    listeners.forEach((l) => l(t))
  }

  async function refresh(): Promise<boolean> {
    if (!refreshing) {
      refreshing = fetch(prefix + '/auth/refresh', { method: 'POST', credentials: 'include' })
        .then(async (r) => {
          if (!r.ok) {
            setToken(null)
            return false
          }
          const js = await r.json()
          setToken(js.accessToken)
          return true
        })
        .catch(() => false)
        .finally(() => setTimeout(() => (refreshing = null), 0))
    }
    return refreshing
  }

  async function request<T = any>(method: string, path: string, body?: any, retry = true): Promise<T> {
    const headers: Record<string, string> = {}
    let payload: BodyInit | undefined
    if (body instanceof FormData) payload = body
    else if (body !== undefined) {
      headers['Content-Type'] = 'application/json'
      payload = JSON.stringify(body)
    }
    if (token) headers.Authorization = 'Bearer ' + token
    const write = method !== 'GET'
    let res: Response
    if (write) trackWrite(1)
    try {
      res = await fetch(prefix + path, { method, headers, body: payload, credentials: 'include' })
    } catch {
      throw new ApiError(0, 'network', navigator.onLine === false ? '网络已断开，请联网后重试' : '网络连接失败，请检查网络后重试')
    } finally {
      if (write) trackWrite(-1)
    }
    if (res.status === 401 && retry && !path.startsWith('/auth/')) {
      if (await refresh()) return request<T>(method, path, body, false)
    }
    const text = await res.text()
    let js: any = null
    try {
      js = text ? JSON.parse(text) : null
    } catch {
      js = null
    }
    if (!res.ok) {
      const e = js?.error
      const err = new ApiError(res.status, e?.code ?? 'error', e?.message ?? `请求失败（${res.status}）`, e?.data)
      errorHook?.(err)
      throw err
    }
    return js as T
  }

  return {
    get token() {
      return token
    },
    setToken,
    refresh,
    /** Called for every failed request before the error is thrown (global reactions such as the upgrade dialog). */
    set onError(fn: ((e: ApiError) => void) | null) {
      errorHook = fn
    },
    onToken(l: Listener) {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    get: <T = any>(p: string) => request<T>('GET', p),
    post: <T = any>(p: string, b?: any) => request<T>('POST', p, b ?? {}),
    put: <T = any>(p: string, b?: any) => request<T>('PUT', p, b ?? {}),
    patch: <T = any>(p: string, b?: any) => request<T>('PATCH', p, b ?? {}),
    del: <T = any>(p: string) => request<T>('DELETE', p),
    upload: <T = any>(p: string, file: Blob, fields: Record<string, string> = {}, name = 'upload') => {
      const fd = new FormData()
      Object.entries(fields).forEach(([k, v]) => fd.append(k, v))
      fd.append('file', file, (file as File).name ?? name)
      return request<T>('POST', p, fd)
    },
  }
}

export type Client = ReturnType<typeof createClient>

export const api = createClient('/api/v1')
export const adminApi = createClient('/api/admin/v1')

export function qs(params: Record<string, any>) {
  const s = new URLSearchParams()
  Object.entries(params).forEach(([k, v]) => {
    if (v !== undefined && v !== null && v !== '') s.set(k, String(v))
  })
  const out = s.toString()
  return out ? '?' + out : ''
}

/** Fetch an authenticated endpoint and save the response as a file. */
export async function downloadAuthed(path: string, filename: string) {
  const res = await fetch('/api/v1' + path, { headers: api.token ? { Authorization: 'Bearer ' + api.token } : {}, credentials: 'include' })
  if (!res.ok) throw new ApiError(res.status, 'download', '下载失败')
  const url = URL.createObjectURL(await res.blob())
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
