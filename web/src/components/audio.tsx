import { Loader2, Mic, Pause, Play, Square, Upload } from '@/components/icons'
import React, { useEffect, useRef, useState } from 'react'
import { usePlayer } from '@/lib/player'
import { duration } from '@/lib/format'
import { Button, cn, Tip } from './ui'

/**
 * Round play/pause button. Without a `url`, `resolve` can produce one on first click (e.g. a voice preview
 * generated on demand); the button shows a spinner meanwhile.
 */
export function PlayButton({ id, url, title, size = 'sm', className, label, resolve, resolveLabel }: { id: string; url?: string | null; title?: string; size?: 'xs' | 'sm' | 'md'; className?: string; label?: string; resolve?: () => Promise<string>; resolveLabel?: string }) {
  const { id: cur, playing, loading, toggle } = usePlayer()
  const [made, setMade] = useState<string>()
  const [busy, setBusy] = useState(false)
  const src = url || made
  const active = cur === id
  const sz = { xs: 'size-6', sm: 'size-8', md: 'size-10' }[size]
  const icon = { xs: 'size-3', sm: 'size-3.5', md: 'size-4' }[size]
  const tip = src ? label ?? (active && playing ? '暂停' : '播放') : resolve ? resolveLabel ?? '生成试听' : '还没有音频'
  return (
    <Tip label={busy ? '正在生成…' : tip}>
      <button
        disabled={(!src && !resolve) || busy}
        aria-busy={busy}
        onClick={async (e) => {
          e.stopPropagation()
          if (src) return toggle(id, src, title)
          if (!resolve) return
          setBusy(true)
          try {
            const u = await resolve()
            setMade(u)
            usePlayer.getState().play(id, u, title)
          } finally {
            setBusy(false)
          }
        }}
        className={cn('grid shrink-0 place-items-center rounded-full transition-all duration-150 disabled:opacity-35 aria-busy:!opacity-100', sz, active && playing ? 'bg-signal text-[#2a1a00] shadow-[0_0_0_4px_var(--signal-soft)]' : 'bg-sunken text-ink hover:bg-line', className)}
      >
        {busy ? <Loader2 className={cn(icon, 'animate-spin text-muted')} /> : active && playing ? loading ? <span className="eq flex items-end h-3"><span /><span /><span /></span> : <Pause className={icon} fill="currentColor" /> : <Play className={cn(icon, 'translate-x-[1px]')} fill="currentColor" />}
      </button>
    </Tip>
  )
}

export function LiveBars({ className }: { className?: string }) {
  return (
    <span className={cn('eq inline-flex h-3 items-end', className)}>
      <span />
      <span />
      <span />
    </span>
  )
}

/** Peak waveform drawn to canvas; `progress` (0..1) tints the played part in signal amber. */
export function Waveform({ peaks, progress = 0, height = 48, color = 'var(--line-strong)', className, onSeek }: { peaks: number[]; progress?: number; height?: number; color?: string; className?: string; onSeek?: (ratio: number) => void }) {
  const ref = useRef<HTMLCanvasElement>(null)
  useEffect(() => {
    const c = ref.current
    if (!c) return
    const dpr = window.devicePixelRatio || 1
    const w = c.clientWidth
    c.width = w * dpr
    c.height = height * dpr
    const ctx = c.getContext('2d')!
    ctx.scale(dpr, dpr)
    ctx.clearRect(0, 0, w, height)
    const styles = getComputedStyle(c)
    const base = color.startsWith('var(') ? styles.getPropertyValue(color.slice(4, -1)) : color
    const played = styles.getPropertyValue('--signal')
    const bars = Math.max(1, Math.floor(w / 3))
    const step = peaks.length / bars
    for (let i = 0; i < bars; i++) {
      let m = 0
      for (let k = Math.floor(i * step); k < Math.floor((i + 1) * step) && k < peaks.length; k++) m = Math.max(m, peaks[k])
      const h = Math.max(1.5, Math.min(1, m * 1.6) * (height - 4))
      ctx.fillStyle = i / bars < progress ? played : base
      ctx.fillRect(i * 3, (height - h) / 2, 2, h)
    }
  }, [peaks, progress, height, color])
  return (
    <canvas
      ref={ref}
      className={cn('w-full', onSeek && 'cursor-pointer', className)}
      style={{ height }}
      onClick={(e) => {
        if (!onSeek) return
        const r = (e.target as HTMLCanvasElement).getBoundingClientRect()
        onSeek((e.clientX - r.left) / r.width)
      }}
    />
  )
}

/** Browser microphone recorder with input level meter. */
export function Recorder({ onDone, countdown = true, compact }: { onDone: (blob: Blob) => void; countdown?: boolean; compact?: boolean }) {
  const [state, setState] = useState<'idle' | 'counting' | 'recording'>('idle')
  const [level, setLevel] = useState(0)
  const [elapsed, setElapsed] = useState(0)
  const [count, setCount] = useState(3)
  const rec = useRef<MediaRecorder | null>(null)
  const chunks = useRef<Blob[]>([])
  const raf = useRef(0)
  const started = useRef(0)
  const stream = useRef<MediaStream | null>(null)

  const begin = async () => {
    try {
      const s = await navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: false, noiseSuppression: false, autoGainControl: false } })
      stream.current = s
      const ac = new AudioContext()
      const an = ac.createAnalyser()
      an.fftSize = 512
      ac.createMediaStreamSource(s).connect(an)
      const data = new Uint8Array(an.fftSize)
      const tick = () => {
        an.getByteTimeDomainData(data)
        let m = 0
        for (const v of data) m = Math.max(m, Math.abs(v - 128) / 128)
        setLevel(m)
        if (started.current) setElapsed(Date.now() - started.current)
        raf.current = requestAnimationFrame(tick)
      }
      tick()
      const start = () => {
        const mr = new MediaRecorder(s)
        chunks.current = []
        mr.ondataavailable = (e) => e.data.size && chunks.current.push(e.data)
        mr.onstop = () => {
          cancelAnimationFrame(raf.current)
          s.getTracks().forEach((t) => t.stop())
          ac.close()
          started.current = 0
          setElapsed(0)
          onDone(new Blob(chunks.current, { type: mr.mimeType || 'audio/webm' }))
        }
        mr.start()
        rec.current = mr
        started.current = Date.now()
        setState('recording')
      }
      if (countdown) {
        setState('counting')
        let n = 3
        setCount(n)
        const t = setInterval(() => {
          n--
          setCount(n)
          if (n <= 0) {
            clearInterval(t)
            start()
          }
        }, 700)
      } else start()
    } catch {
      alert('无法使用麦克风：请在浏览器地址栏允许麦克风权限（需要 HTTPS 或 localhost）')
    }
  }
  const stop = () => {
    rec.current?.stop()
    setState('idle')
  }
  useEffect(() => () => {
    cancelAnimationFrame(raf.current)
    stream.current?.getTracks().forEach((t) => t.stop())
  }, [])

  return (
    <div className={cn('flex items-center gap-3', compact && 'gap-2')}>
      {state === 'recording' ? (
        <Button variant="danger" size={compact ? 'sm' : 'md'} icon={<Square className="size-3.5" fill="currentColor" />} onClick={stop}>
          停止 {duration(elapsed)}
        </Button>
      ) : state === 'counting' ? (
        <Button variant="signal" size={compact ? 'sm' : 'md'} disabled>
          {count} 秒后开始
        </Button>
      ) : (
        <Button variant="signal" size={compact ? 'sm' : 'md'} icon={<Mic className="size-4" />} onClick={begin}>
          录音
        </Button>
      )}
      {state !== 'idle' && (
        <div className="h-1.5 w-28 overflow-hidden rounded-full bg-sunken">
          <div className={cn('h-full rounded-full transition-[width] duration-75', level > 0.95 ? 'bg-danger' : 'bg-signal')} style={{ width: `${Math.min(1, level * 1.4) * 100}%` }} />
        </div>
      )}
    </div>
  )
}

export function UploadZone({ accept, onFiles, hint, multiple, children, className }: { accept?: string; onFiles: (files: File[]) => void; hint?: React.ReactNode; multiple?: boolean; children?: React.ReactNode; className?: string }) {
  const [over, setOver] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  return (
    <div
      onDragOver={(e) => {
        e.preventDefault()
        setOver(true)
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        e.preventDefault()
        setOver(false)
        const files = Array.from(e.dataTransfer.files)
        if (files.length) onFiles(multiple ? files : files.slice(0, 1))
      }}
      onClick={() => input.current?.click()}
      className={cn('flex cursor-pointer flex-col items-center justify-center rounded-panel border border-dashed px-6 py-10 text-center transition-colors', over ? 'border-accent bg-accent-soft' : 'border-line-strong bg-sunken/40 hover:border-accent/60', className)}
    >
      <input ref={input} type="file" accept={accept} multiple={multiple} className="hidden" onChange={(e) => e.target.files && onFiles(Array.from(e.target.files))} />
      {children ?? (
        <>
          <div className="mb-3 grid size-10 place-items-center rounded-full bg-surface text-muted shadow-sm">
            <Upload className="size-5" />
          </div>
          <p className="text-[14px] font-medium">拖拽文件到这里，或点击选择</p>
          {hint && <p className="mt-1 text-xs text-muted">{hint}</p>}
        </>
      )}
    </div>
  )
}

export function MiniPlayerBar() {
  const { id, playing, time, duration: d, title, stop, seek } = usePlayer()
  if (!id) return null
  return (
    <div className="fixed bottom-4 left-1/2 z-40 flex w-[min(560px,calc(100vw-32px))] -translate-x-1/2 items-center gap-3 rounded-full border border-line bg-surface/95 py-2 pl-2 pr-4 shadow-[var(--shadow-pop)] backdrop-blur">
      <PlayButton id={id} url={usePlayer.getState().url} size="sm" />
      <div className="min-w-0 flex-1">
        <div className="truncate text-[12.5px] font-medium">{title || '正在播放'}</div>
        <div className="mt-1 h-1 cursor-pointer overflow-hidden rounded-full bg-sunken" onClick={(e) => {
          const r = (e.currentTarget as HTMLDivElement).getBoundingClientRect()
          seek(((e.clientX - r.left) / r.width) * d)
        }}>
          <div className="h-full bg-signal" style={{ width: `${d ? (time / d) * 100 : 0}%` }} />
        </div>
      </div>
      <span className="text-xs text-muted tnum">{duration(time * 1000)} / {duration(d * 1000)}</span>
      <button onClick={stop} className="text-xs text-muted hover:text-ink">关闭</button>
    </div>
  )
}
