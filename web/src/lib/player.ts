import { create } from 'zustand'

// One global audio element: starting a clip stops the previous one, and every play button can show state.
type PlayerState = {
  id: string | null
  url: string | null
  playing: boolean
  loading: boolean
  time: number
  duration: number
  title?: string
  queue: { id: string; url: string; title?: string }[]
  /** true while playing through a list (e.g. a chapter read-through), so views can follow along */
  continuous: boolean
  play: (id: string, url: string, title?: string, queue?: { id: string; url: string; title?: string }[], continuous?: boolean) => void
  toggle: (id: string, url: string, title?: string) => void
  stop: () => void
  seek: (t: number) => void
}

const audio = typeof Audio !== 'undefined' ? new Audio() : (null as any as HTMLAudioElement)

export const usePlayer = create<PlayerState>((set, get) => ({
  id: null, url: null, playing: false, loading: false, time: 0, duration: 0, queue: [], continuous: false,
  play: (id, url, title, queue = [], continuous = queue.length > 0) => {
    if (!url) return
    audio.src = url
    audio.currentTime = 0
    set({ id, url, title, playing: true, loading: true, time: 0, queue, continuous })
    audio.play().catch(() => set({ playing: false, loading: false }))
  },
  toggle: (id, url, title) => {
    const s = get()
    if (s.id === id && s.playing) {
      audio.pause()
      set({ playing: false })
    } else if (s.id === id && s.url) {
      audio.play()
      set({ playing: true })
    } else s.play(id, url, title)
  },
  stop: () => {
    audio.pause()
    set({ playing: false, id: null, queue: [], continuous: false })
  },
  seek: (t) => {
    audio.currentTime = t
    set({ time: t })
  },
}))

if (audio) {
  audio.addEventListener('timeupdate', () => usePlayer.setState({ time: audio.currentTime }))
  audio.addEventListener('loadedmetadata', () => usePlayer.setState({ duration: audio.duration, loading: false }))
  audio.addEventListener('playing', () => usePlayer.setState({ loading: false, playing: true }))
  audio.addEventListener('pause', () => usePlayer.setState({ playing: false }))
  audio.addEventListener('ended', () => {
    const { queue } = usePlayer.getState()
    if (queue.length) {
      const [next, ...rest] = queue
      usePlayer.getState().play(next.id, next.url, next.title, rest, true)
    } else usePlayer.setState({ playing: false, id: null, continuous: false })
  })
}
