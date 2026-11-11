import { useQuery } from '@tanstack/react-query'
import {
  animate, AnimatePresence, motion, useInView, useMotionValue, useMotionValueEvent, useReducedMotion, useScroll, useSpring, useTransform,
  type MotionValue,
} from 'framer-motion'
import Lenis from 'lenis'
import 'lenis/dist/lenis.css'
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { ThemeToggle } from '@/components/theme-toggle'
import { api } from '@/lib/api'
import { useSession } from '@/lib/session'
import { useTheme } from '@/lib/theme'
import { annotate, SAMPLES, type Line } from './annotate'
import './landing.css'
import cover from './shots/cover.png'
import dCharacters from './shots/dark/characters.webp'
import dDeliver from './shots/dark/deliver.webp'
import dLexicon from './shots/dark/lexicon.webp'
import dPoster from './shots/dark/playback-poster.jpg'
import dPlayback from './shots/dark/playback.mp4'
import dReview from './shots/dark/review.webp'
import dWorkspace from './shots/dark/workspace.webp'
import lCharacters from './shots/light/characters.webp'
import lDeliver from './shots/light/deliver.webp'
import lLexicon from './shots/light/lexicon.webp'
import lPoster from './shots/light/playback-poster.jpg'
import lPlayback from './shots/light/playback.mp4'
import lReview from './shots/light/review.webp'
import lWorkspace from './shots/light/workspace.webp'

// Every picture is a screenshot of the product (2880×1800 at 2×) made while producing the sample book 《雾港来信》,
// once in the day theme and once in the night theme; the page shows the one that matches its own theme.
const SHOTS = {
  characters: [lCharacters, dCharacters], lexicon: [lLexicon, dLexicon], review: [lReview, dReview],
  deliver: [lDeliver, dDeliver], workspace: [lWorkspace, dWorkspace],
} as const
type ShotName = keyof typeof SHOTS
const SHOT_W = 2880
const SHOT_H = 1800
const ease = [0.22, 1, 0.36, 1] as const
const easeInOut = [0.76, 0, 0.24, 1] as const
type Region = [x: number, y: number, w: number, h: number]

/** Fetches the other theme's pictures once, so switching day and night shows them without a gap. */
let warmed = false
function warmOtherTheme(dark: boolean) {
  if (warmed) return
  warmed = true
  const i = dark ? 0 : 1
  for (const src of [...Object.values(SHOTS).map((s) => s[i]), i ? dPoster : lPoster]) {
    const img = new Image()
    img.decoding = 'async'
    img.src = src
  }
}

function useMedia(q: string) {
  const [on, setOn] = useState(() => typeof matchMedia !== 'undefined' && matchMedia(q).matches)
  useEffect(() => {
    const m = matchMedia(q)
    const f = () => setOn(m.matches)
    m.addEventListener('change', f)
    return () => m.removeEventListener('change', f)
  }, [q])
  return on
}

/** One region of a product screenshot, scaled to the width of its box, in the page's theme. */
function Shot({ name, r, alt }: { name: ShotName; r: Region; alt: string }) {
  const dark = useTheme((s) => s.dark)
  const [x, y, w, h] = r
  return (
    <div className="crop" style={{ aspectRatio: `${w} / ${h}` }}>
      <img src={SHOTS[name][dark ? 1 : 0]} alt={alt} loading="lazy" decoding="async" width={SHOT_W} height={SHOT_H}
        style={{ width: `${(SHOT_W / w) * 100}%`, left: `${(-x / w) * 100}%`, top: `${(-y / h) * 100}%` }} />
    </div>
  )
}

/** Fades up once when it scrolls into view. */
function Reveal({ children, delay = 0, className, as = 'div' }: { children: ReactNode; delay?: number; className?: string; as?: 'div' | 'li' | 'header' }) {
  const reduce = useReducedMotion()
  const M = motion[as] as typeof motion.div
  return (
    <M className={className} initial={{ opacity: 0, y: reduce ? 0 : 18 }} whileInView={{ opacity: 1, y: 0 }} viewport={{ once: true, amount: 0.2 }}
      transition={{ duration: reduce ? 0.2 : 0.8, delay: reduce ? 0 : delay, ease }}>
      {children}
    </M>
  )
}

/** A large panel that opens from a narrower, rounder frame as it scrolls into view, like a page lifted onto the desk. */
function Curtain({ children, className }: { children: ReactNode; className?: string }) {
  const reduce = useReducedMotion()
  return (
    <motion.div className={className}
      initial={reduce ? { opacity: 0 } : { opacity: 0, y: 48, clipPath: 'inset(6% 5% 0% 5% round 22px)' }}
      whileInView={reduce ? { opacity: 1 } : { opacity: 1, y: 0, clipPath: 'inset(0% 0% 0% 0% round 12px)' }}
      viewport={{ once: true, amount: 0.15 }} transition={{ duration: reduce ? 0.2 : 1.1, ease }}>
      {children}
    </motion.div>
  )
}

/** Section heading: the number and label, then the title rising out of its line, then the rule drawn across. */
function Head({ n, label, title, sub }: { n: string; label: string; title: ReactNode; sub?: ReactNode }) {
  const ref = useRef<HTMLElement>(null)
  const seen = useInView(ref, { once: true, amount: 0.5 })
  return (
    <header ref={ref} className={'head' + (seen ? ' in' : '')}>
      <p className="label"><span>{n}</span>{label}</p>
      <h2><span className="mask"><span>{title}</span></span></h2>
      {sub && <p className="sub">{sub}</p>}
      <i className="rule" aria-hidden="true" />
    </header>
  )
}

/** Characters that rise one after another out of their phrase, like type set by hand. */
function Chars({ text, delay = 0, step = 0.045 }: { text: string; delay?: number; step?: number }) {
  const reduce = useReducedMotion()
  return (
    <>
      {[...text].map((c, i) => (
        <motion.span key={i} className="ch" initial={reduce ? { opacity: 0 } : { y: '115%' }} animate={reduce ? { opacity: 1 } : { y: '0%' }}
          transition={{ duration: reduce ? 0.3 : 1, delay: reduce ? 0 : delay + i * step, ease: [0.16, 1, 0.3, 1] }}>{c}</motion.span>
      ))}
    </>
  )
}

/** A button label that rolls up to a fresh copy of itself on hover. */
const Roll = ({ children }: { children: string }) => <span className="roll" data-t={children}><span>{children}</span></span>

/** Leans gently toward the pointer (mouse and pen only). */
function Magnetic({ children, strength = 0.28 }: { children: ReactNode; strength?: number }) {
  const fine = useMedia('(pointer: fine)')
  const reduce = useReducedMotion()
  const x = useSpring(0, { stiffness: 220, damping: 18, mass: 0.4 })
  const y = useSpring(0, { stiffness: 220, damping: 18, mass: 0.4 })
  if (!fine || reduce) return <>{children}</>
  return (
    <motion.span className="mag" style={{ x, y }}
      onPointerMove={(e) => {
        const b = e.currentTarget.getBoundingClientRect()
        x.set(Math.max(-10, Math.min(10, (e.clientX - b.left - b.width / 2) * strength)))
        y.set(Math.max(-8, Math.min(8, (e.clientY - b.top - b.height / 2) * strength)))
      }}
      onPointerLeave={() => { x.set(0); y.set(0) }}>
      {children}
    </motion.span>
  )
}

/** Text whose characters go from pale to full ink as `progress` runs from 0 to 1. One CSS variable drives them all.
    It is hidden from screen readers; the heading around it carries the text as its label. */
function InkText({ text, progress, soft = 4 }: { text: string; progress: MotionValue<number>; soft?: number }) {
  const chars = [...text]
  const p = useTransform(progress, (v) => v * (chars.length + soft))
  return (
    <motion.span className="ink" style={{ '--p': p, '--soft': soft } as any} aria-hidden="true">
      {chars.map((c, i) => <span key={i} className="c" style={{ '--i': i } as CSSProperties}>{c}</span>)}
    </motion.span>
  )
}

const Arrow = () => (
  <svg className="arr" width="15" height="15" viewBox="0 0 16 16" aria-hidden="true"><path d="M3 8h9.5M8.5 4l4 4-4 4" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" /></svg>
)
const Tick = () => (
  <svg width="15" height="15" viewBox="0 0 16 16" aria-hidden="true"><path d="m3.5 8.5 3 3 6-7" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" /></svg>
)
const Logo = () => (
  <span className="logo" aria-hidden="true">
    <svg width="16" height="16" viewBox="0 0 18 18">{[2, 5.5, 9, 12.5, 16].map((x, i) => <rect key={x} x={x - 1} y={9 - [3, 5.5, 7.5, 5, 2.5][i]} width="2" height={[3, 5.5, 7.5, 5, 2.5][i] * 2} rx="1" fill={i === 3 ? '#e8a53c' : 'currentColor'} />)}</svg>
  </span>
)

// ---------- live data: plans and what credits buy ----------
type Plan = { code: string; name: string; tagline: string; priceMonthly: number; monthlyCredits: number; bonusCredits: number; creditRate: number; seats: number; maxBooks: number; storageGb: number; cloneVoices: number; highQuality: boolean; commercial: boolean; exportFormats: string[]; highlight: boolean; contactSales: boolean }
type Guide = { per10kChars: { economy: number; standard: number; premium: number }; speechRate: number }

const FALLBACK_PLANS: Plan[] = [
  { code: 'free', name: '免费版', tagline: '体验 AI 多播有声书', priceMonthly: 0, monthlyCredits: 10000, bonusCredits: 0, creditRate: 0, seats: 1, maxBooks: 3, storageGb: 2, cloneVoices: 1, highQuality: false, commercial: false, exportFormats: ['mp3'], highlight: false, contactSales: false },
  { code: 'personal', name: '个人版', tagline: '个人创作者与配音演员', priceMonthly: 6900, monthlyCredits: 150000, bonusCredits: 50000, creditRate: 95, seats: 1, maxBooks: 0, storageGb: 30, cloneVoices: 5, highQuality: true, commercial: true, exportFormats: [], highlight: true, contactSales: false },
  { code: 'team', name: '团队版', tagline: '有声工作室与剧组', priceMonthly: 29900, monthlyCredits: 800000, bonusCredits: 0, creditRate: 90, seats: 10, maxBooks: 0, storageGb: 200, cloneVoices: 30, highQuality: true, commercial: true, exportFormats: [], highlight: false, contactSales: false },
  { code: 'enterprise', name: '企业版', tagline: '版权方与平台批量有声化', priceMonthly: 0, monthlyCredits: 5000000, bonusCredits: 0, creditRate: 85, seats: 100, maxBooks: 0, storageGb: 2000, cloneVoices: 200, highQuality: true, commercial: true, exportFormats: [], highlight: false, contactSales: true },
]
const FALLBACK_GUIDE: Guide = { per10kChars: { economy: 95, standard: 130, premium: 230 }, speechRate: 220 }

function wan(n: number) {
  if (n >= 1e8) return `${+(n / 1e8).toFixed(1)} 亿`
  if (n >= 1e4) return `${Math.round(n / 1e4).toLocaleString('zh-CN')} 万`
  return n.toLocaleString('zh-CN')
}
/** What a monthly allowance makes at the standard tier: characters and listening hours. */
function reach(credits: number, rate: number, g: Guide) {
  const cost = g.per10kChars.standard * (rate > 0 && rate < 100 ? rate / 100 : 1)
  const chars = (credits / cost) * 10000
  const hours = chars / (g.speechRate || 220) / 60
  return { chars: `约 ${wan(chars)}字`, hours: `约 ${Math.round(hours).toLocaleString('zh-CN')} 小时` }
}

// ---------- navigation ----------
const LINKS: [string, string][] = [['#try', '原文变画本'], ['#features', '功能'], ['#how', '流程'], ['#pricing', '价格'], ['#faq', '常见问题']]

/** Which section is under the middle of the screen, for the navigation's marker. */
function useSpy(ids: string[]) {
  const [on, setOn] = useState('')
  useEffect(() => {
    const seen = new Map<string, boolean>()
    const io = new IntersectionObserver((es) => {
      for (const e of es) seen.set(e.target.id, e.isIntersecting)
      setOn(ids.find((id) => seen.get(id)) ?? '')
    }, { rootMargin: '-45% 0px -50% 0px' })
    for (const id of ids) {
      const el = document.getElementById(id)
      if (el) io.observe(el)
    }
    return () => io.disconnect()
  }, [ids])
  return on
}

function Nav({ action, authed, ink }: { action: { to: string; label: string }; authed: boolean; ink: boolean }) {
  const { scrollY, scrollYProgress } = useScroll()
  const [solid, setSolid] = useState(false)
  const [open, setOpen] = useState(false)
  const dark = useTheme((s) => s.dark)
  const ids = useMemo(() => LINKS.map(([h]) => h.slice(1)), [])
  const on = useSpy(ids)
  useMotionValueEvent(scrollY, 'change', (v) => setSolid(v > 8))
  return (
    <motion.header className={'nav' + (solid || open ? ' solid' : '') + (ink && !open ? ' ink' : '')}
      initial={{ y: -64, opacity: 0 }} animate={{ y: 0, opacity: 1 }} transition={{ duration: 0.8, delay: 0.1, ease }}>
      <div className="nav-in">
        <Link to={authed ? '/' : '/welcome'} className="brand" aria-label="声书工坊首页"><Logo /><span>声书工坊</span></Link>
        <nav className="nav-links" aria-label="页面导航">
          {LINKS.map(([href, label]) => (
            <a key={href} href={href} className={on === href.slice(1) ? 'on' : ''} aria-current={on === href.slice(1) ? 'location' : undefined}>
              {label}
              {on === href.slice(1) && <motion.i className="nav-ul" layoutId="nav-ul" transition={{ type: 'spring', stiffness: 420, damping: 36 }} />}
            </a>
          ))}
        </nav>
        <div className="nav-act" onPointerEnter={() => warmOtherTheme(dark)}>
          <ThemeToggle className="tt" />
          {!authed && <Link to="/login" viewTransition className="nav-login">登录</Link>}
          <Link to={action.to} viewTransition className="btn btn-primary btn-sm"><Roll>{action.label}</Roll></Link>
          <button type="button" className="nav-burger" aria-expanded={open} aria-label={open ? '关闭菜单' : '打开菜单'} onClick={() => setOpen(!open)}>
            <span /><span />
          </button>
        </div>
      </div>
      {/* how far down the page you are, like the edge of a book's pages */}
      <motion.i className="nav-prog" style={{ scaleX: scrollYProgress }} aria-hidden="true" />
      <AnimatePresence>
        {open && (
          <motion.nav className="nav-sheet" aria-label="页面导航" initial={{ opacity: 0, height: 0 }} animate={{ opacity: 1, height: 'auto' }} exit={{ opacity: 0, height: 0 }} transition={{ duration: 0.3, ease }}>
            {LINKS.map(([href, label], i) => (
              <motion.a key={href} href={href} onClick={() => setOpen(false)} initial={{ opacity: 0, x: -12 }} animate={{ opacity: 1, x: 0 }} transition={{ duration: 0.35, delay: 0.05 + i * 0.04, ease }}>{label}</motion.a>
            ))}
            {!authed && <Link to="/login" viewTransition>登录</Link>}
          </motion.nav>
        )}
      </AnimatePresence>
    </motion.header>
  )
}

// ---------- hero ----------
const LEGEND: [string, string][] = [['旁白', '#8b8a84'], ['林晚', '#d64575'], ['周伯', '#7b57d1'], ['陆沉舟', '#e08a1e'], ['阿满', '#1f8f7a']]

function Hero({ action, g, freeCredits, authed }: { action: { to: string; label: string }; g: Guide; freeCredits: number; authed: boolean }) {
  const reduce = useReducedMotion()
  const small = useMedia('(max-width: 760px)')
  const dark = useTheme((s) => s.dark)
  const r = reach(freeCredits, 0, g)
  // the screen starts leaning back and rises upright as you scroll; the words drift up and give way to it
  const { scrollY } = useScroll()
  const tilt = reduce ? 0 : small ? 8 : 14
  const rotateX = useTransform(scrollY, [0, 560], [tilt, 0])
  const scale = useTransform(scrollY, [0, 560], [reduce ? 1 : 0.94, 1])
  const textY = useTransform(scrollY, [0, 700], [0, reduce ? 0 : -56])
  const textO = useTransform(scrollY, [0, 560], [1, reduce ? 1 : 0.25])
  const fade = (d: number) => ({ initial: { opacity: 0, y: reduce ? 0 : 14 }, animate: { opacity: 1, y: 0 }, transition: { duration: 0.9, delay: reduce ? 0 : d, ease } })
  return (
    <section className="hero">
      <motion.div className="hero-top" style={{ y: textY, opacity: textO }}>
        <div className="hero-title">
          <motion.p className="kicker" {...fade(0.1)}><span className="eq" aria-hidden="true"><i /><i /><i /><i /></span>AI 有声书工作台</motion.p>
          <h1 aria-label="把一本小说，做成有角色、有情绪的有声书。">
            <span className="ln" aria-hidden="true"><span className="ph"><Chars text="把一本小说，" delay={0.2} /></span></span>
            <span className="ln" aria-hidden="true"><span className="ph"><Chars text="做成有角色、" delay={0.5} /></span><span className="ph"><Chars text="有情绪的有声书。" delay={0.77} /></span></span>
          </h1>
        </div>
        <motion.div className="hero-side" {...fade(0.95)}>
          <p className="lede">导入书稿，AI 分出角色、标好情绪、挑选声音，几分钟就能听到第一章。想精修的地方逐句调，也可以请真人配音演员一起录。</p>
          <div className="cta">
            <Magnetic><Link to={action.to} viewTransition className="btn btn-primary"><Roll>{action.label}</Roll><Arrow /></Link></Magnetic>
            <a href="#try" className="btn btn-quiet">先试试原文变画本</a>
          </div>
          <p className="fine">{authed ? '欢迎回来，你的图书和进度都在工作台里。' : `免费版每月 ${wan(freeCredits)}积分，${r.chars.replace('约 ', '约够做 ')}，不用安装。`}</p>
        </motion.div>
      </motion.div>

      <motion.figure className="hero-fig" initial={{ opacity: 0, y: reduce ? 0 : 80 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 1.4, delay: reduce ? 0 : 0.9, ease }}>
        <motion.div className="window" style={{ rotateX, scale }}>
          <div className="window-bar" aria-hidden="true">
            <span className="dots"><i /><i /><i /></span>
            <span className="window-t">雾港来信 · 第一章 无人认领的信</span>
            <span className="window-live"><i />连续播放</span>
          </div>
          {/* a real recording: chapter one of the sample book playing, the current line highlighted and followed */}
          <video key={dark ? 'd' : 'l'} className="window-video" src={dark ? dPlayback : lPlayback} poster={dark ? dPoster : lPoster} muted loop playsInline autoPlay={!reduce} preload="auto" width={1376} height={768}
            aria-label="制作台连续播放《雾港来信》第一章：正在播放的句子高亮，列表自动跟随；每句标着说话人、情绪和表演指导" />
        </motion.div>
        <figcaption>
          <span>《雾港来信》第一章在制作台连续播放的实录。每句标着说话人、情绪和表演指导。</span>
          <span className="legend">{LEGEND.map(([n, c]) => <span key={n}><i style={{ background: c }} />{n}</span>)}</span>
        </figcaption>
      </motion.figure>
    </section>
  )
}

// ---------- interlude: a page being read aloud ----------
// Chapter three of the sample book, annotated the way the demo below does it. As you scroll, the passage is
// "read": each character inks in, the marker follows the line being spoken and the voice card says who is
// speaking and how, while the track below fills. The lights go down for it: the section is a field of ink.
const PASSAGE = annotate(SAMPLES[2])
const BARS = 96
const fmt = (s: number) => `${String(Math.floor(s / 60)).padStart(2, '0')}:${String(Math.floor(s % 60)).padStart(2, '0')}`

function Listen({ onInk }: { onInk: (on: boolean) => void }) {
  const reduce = useReducedMotion()
  const ref = useRef<HTMLElement>(null)
  const textRef = useRef<HTMLDivElement>(null)
  const stage = useRef<HTMLDivElement>(null)
  const lineRefs = useRef<(HTMLSpanElement | null)[]>([])
  const { paras, starts, total } = useMemo(() => {
    const starts: number[] = []
    let n = 0
    for (const l of PASSAGE) { starts.push(n); n += [...l.text].length }
    const paras: number[][] = []
    PASSAGE.forEach((l, i) => (paras[l.para] ??= []).push(i))
    return { paras, starts, total: n }
  }, [])
  const seconds = Math.round((total / 220) * 60)
  const cast = new Set(PASSAGE.filter((l) => l.kind === 'dialogue').map((l) => l.speaker)).size
  // the waveform: one bar per slice of the passage, quieter at the pauses between lines
  const bars = useMemo(() => Array.from({ length: BARS }, (_, j) => {
    const at = (j / BARS) * total
    const li = Math.max(0, starts.findLastIndex((s) => s <= at))
    const edge = Math.min(at - starts[li], (starts[li + 1] ?? total) - at)
    const loud = PASSAGE[li].kind === 'dialogue' ? (PASSAGE[li].emotion === '喊叫' ? 1 : 0.8) : 0.6
    const wobble = 0.35 + 0.65 * Math.abs(Math.sin(j * 1.93) * Math.cos(j * 0.61 + 0.4))
    return edge < 0.8 ? 0.08 : Math.max(0.12, wobble * loud)
  }), [starts, total])

  const { scrollY } = useScroll()
  const { scrollYProgress: enter } = useScroll({ target: ref, offset: ['start end', 'start start'] })
  const { scrollYProgress: leave } = useScroll({ target: ref, offset: ['end end', 'end start'] })
  const { scrollYProgress: run } = useScroll({ target: ref, offset: ['start start', 'end end'] })
  const still = useMotionValue(1)
  const read = useTransform(reduce ? still : run, (v) => Math.min(1, Math.max(0, (v - 0.06) / 0.86)))
  const clipPath = useTransform([enter, leave], ([e, l]: number[]) => {
    if (reduce) return 'none'
    const k = Math.max(1 - e, l)
    return `inset(${(k * 9).toFixed(2)}% ${(k * 6).toFixed(2)}% round ${Math.min(28, k * 90).toFixed(1)}px)`
  })
  const p = useTransform(read, (v) => v * (total + 4))
  const time = useTransform(read, (v) => fmt(v * seconds))
  const head = useTransform(read, (v) => `${(v * 100).toFixed(2)}%`)
  const lit = useTransform(read, (v) => `inset(0 ${((1 - v) * 100).toFixed(2)}% 0 0)`)

  const [cur, setCur] = useState(reduce ? PASSAGE.length - 1 : 0)
  useMotionValueEvent(p, 'change', (v) => {
    const at = v - 2
    setCur(Math.max(0, starts.findLastIndex((s) => s <= at)))
  })
  // the navigation turns dark while the visible part of the field is under it
  // (worked out from the boxes, not the progress values, which may not have caught up with a jump yet)
  const checkInk = () => {
    const sec = ref.current?.getBoundingClientRect()
    const b = stage.current?.getBoundingClientRect()
    if (!sec || !b) return
    const vh = innerHeight
    const k = reduce ? 0 : Math.min(1, Math.max(0, Math.max(sec.top / vh, (vh - sec.bottom) / vh)))
    const inset = k * 0.09 * b.height
    onInk(b.top + inset <= 32 && b.bottom - inset >= 64)
  }
  useMotionValueEvent(scrollY, 'change', checkInk)
  useEffect(() => () => onInk(false), [onInk])

  // the marker sits beside the line being spoken
  const [mark, setMark] = useState({ y: 0, h: 0 })
  useLayoutEffect(() => {
    const place = () => {
      const el = lineRefs.current[cur]
      const box = textRef.current
      if (!el || !box) return
      const a = el.getBoundingClientRect()
      const b = box.getBoundingClientRect()
      setMark({ y: a.top - b.top, h: a.height })
    }
    place()
    addEventListener('resize', place)
    return () => removeEventListener('resize', place)
  }, [cur])

  const l = PASSAGE[cur]
  return (
    <section ref={ref} className={'listen' + (reduce ? ' still' : '')} aria-labelledby="listen-h">
      <motion.div className="listen-stage" ref={stage} style={{ clipPath }}>
        <div className="listen-in">
          <div className="listen-top">
            <p className="listen-k"><i aria-hidden="true" />试听 · 《雾港来信》第三章</p>
            <h2 id="listen-h">一页书，读出来是这样的</h2>
            <p className="listen-cast">旁白和 {cast} 个角色，{cast + 1} 种声音</p>
          </div>

          <div className="listen-body">
            <div className="voice" aria-live="polite">
              <span className="voice-k">正在朗读</span>
              <AnimatePresence mode="popLayout" initial={false}>
                <motion.div key={cur} className="voice-card" initial={{ opacity: 0, y: reduce ? 0 : 22 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, y: reduce ? 0 : -22 }} transition={{ duration: 0.5, ease }}>
                  <p className="voice-who"><i style={{ background: l.kind === 'narration' ? 'var(--f-muted)' : l.color }} />{l.speaker}</p>
                  <p className="voice-how">{l.kind === 'narration' ? '叙述，平稳' : l.emotion + (l.direction ? `，${l.direction}` : '')}</p>
                  <p className="voice-meta">{l.tag && <span className="voice-tag">{l.tag}</span>}音色 · {l.voice}</p>
                </motion.div>
              </AnimatePresence>
            </div>

            <div className="passage" ref={textRef}>
              <motion.span className="passage-mark" aria-hidden="true" style={{ '--mc': l.kind === 'narration' ? 'var(--f-faint)' : l.color } as any} animate={{ y: mark.y, height: mark.h }} transition={{ duration: reduce ? 0 : 0.55, ease }} />
              <motion.div className="ink-p" style={{ '--p': p } as any}>
                {paras.map((idx, pi) => (
                  <p key={pi}>
                    {idx.map((li) => {
                      const s = starts[li]
                      return (
                        <span key={li} ref={(el) => { lineRefs.current[li] = el }} className={'pl' + (li === cur ? ' now' : '')}>
                          {[...PASSAGE[li].text].map((c, k) => <span key={k} className="c" style={{ '--i': s + k } as CSSProperties}>{c}</span>)}
                        </span>
                      )
                    })}
                  </p>
                ))}
              </motion.div>
            </div>
          </div>

          <div className="track" aria-hidden="true">
            <motion.span className="track-t">{time}</motion.span>
            <div className="wave">
              <div className="bars">{bars.map((h, j) => <i key={j} style={{ height: `${h * 100}%` }} />)}</div>
              <motion.div className="bars lit" style={{ clipPath: lit }}>{bars.map((h, j) => <i key={j} style={{ height: `${h * 100}%` }} />)}</motion.div>
              <motion.span className="head" style={{ left: head }} />
            </div>
            <span className="track-t">{fmt(seconds)}</span>
          </div>
        </div>
      </motion.div>
    </section>
  )
}

// ---------- 01 the live demo: manuscript in, script out ----------
function Demo() {
  const reduce = useReducedMotion()
  const [sample, setSample] = useState(0)
  const [ver, setVer] = useState(0)
  const [text, setText] = useState(SAMPLES[0])
  const [lines, setLines] = useState(() => annotate(SAMPLES[0]))
  const box = useRef<HTMLTextAreaElement>(null)
  const sheet = useRef<HTMLDivElement>(null)
  // the first time the sheet comes into view, the script is written out line by line
  const seen = useInView(sheet, { once: true, amount: 0.35 })
  const first = useRef(true)
  useEffect(() => {
    if (!seen) return
    const t = setTimeout(() => { first.current = false }, 2400)
    return () => clearTimeout(t)
  }, [seen])
  useEffect(() => {
    const t = setTimeout(() => setLines(annotate(text)), 220)
    return () => clearTimeout(t)
  }, [text])
  // the manuscript grows with its text, so nothing hides behind a scrollbar
  useEffect(() => {
    const el = box.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = el.scrollHeight + 2 + 'px'
  }, [text, ver])
  const cast = useMemo(() => {
    const m = new Map<string, Line>()
    for (const l of lines) if (l.kind === 'dialogue' && !m.has(l.speaker)) m.set(l.speaker, l)
    return [...m.values()]
  }, [lines])
  const said = lines.filter((l) => l.kind === 'dialogue').length
  const seconds = Math.round((text.replace(/\s/g, '').length / 220) * 60)
  return (
    <section className="sec" id="try" aria-labelledby="try-h">
      <Head n="01" label="原文变画本" title={<span id="try-h">贴进一段原文，看它变成画本</span>} sub="画本是有声书的演播脚本：每句话由谁来说、带什么情绪、用哪个声音。左边这段可以直接改。" />
      <Curtain className="sheet">
        <div className="sheet-in" ref={sheet}>
          <div className="sheet-head">
            <span className="sheet-t">书稿</span>
            <div className="tabs" role="group" aria-label="换一段示例">
              {['第一章', '第二章', '第三章'].map((label, i) => (
                <button key={label} type="button" aria-pressed={sample === i} onClick={() => { setSample(i); setText(SAMPLES[i]); setVer((v) => v + 1) }}>
                  {label}
                  {sample === i && <motion.i className="tab-ul" layoutId="demo-tab" transition={{ type: 'spring', stiffness: 420, damping: 36 }} />}
                </button>
              ))}
            </div>
          </div>
          <motion.textarea key={ver} id="manuscript" ref={box} aria-label="书稿原文，可以直接改" value={text} spellCheck={false}
            initial={ver ? { opacity: 0, y: reduce ? 0 : 8 } : false} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.45, ease }}
            onChange={(e) => { setText(e.target.value); setSample(-1) }} />
        </div>
        <div className="sheet-out" aria-live="polite" aria-label="画本">
          <div className="sheet-head">
            <span className="sheet-t">画本</span>
            <span className="stats">{cast.length} 个角色 · {said} 句对白 · {lines.length - said} 句旁白 · 成品约 {seconds >= 60 ? `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒` : `${seconds} 秒`}</span>
          </div>
          <ol className="script" data-lenis-prevent>
            <AnimatePresence initial={false} mode="popLayout">
              {lines.map((l, i) => {
                const narr = l.kind === 'narration'
                const d = reduce ? 0 : first.current ? 0.35 + i * 0.11 : Math.min(i, 10) * 0.025
                return (
                  <motion.li key={l.key + l.speaker + l.text} className={narr ? 'narr' : ''} layout={!reduce}
                    initial={{ opacity: 0, x: reduce ? 0 : -10 }} animate={seen ? { opacity: 1, x: 0 } : { opacity: 0, x: reduce ? 0 : -10 }} exit={{ opacity: 0 }}
                    transition={{ duration: 0.5, delay: d, ease }}>
                    <span className="no">{i + 1}</span>
                    <span className="who" style={{ '--c': l.color } as CSSProperties}>{l.speaker}</span>
                    <span className="said">
                      {l.tag && <span className="tag">{l.tag}</span>}{l.text}
                      {!narr && <span className="how">{l.emotion}{l.direction ? `，${l.direction}` : ''} · {l.voice}</span>}
                    </span>
                  </motion.li>
                )
              })}
            </AnimatePresence>
          </ol>
        </div>
      </Curtain>
      <p className="note">这里用几条简单规则在浏览器里演示，示例音色与成书时的选角一致。正式制作时由大模型逐句标注说话人、情绪、表演标签和读音，不改动原文。《雾港来信》是为演示写的原创短篇。</p>
    </section>
  )
}

// ---------- 02 features: a list on the left, the screen it is about on the right ----------
type Feature = { title: string; text: string; points: string[]; shot: ShotName; r: Region; alt: string }
const FEATURES: Feature[] = [
  { title: '选角与音色库', text: 'AI 读完全书，找出会说话的角色、写好人物小传，再按性别、年龄和性格推荐声音。上千个平台音色可以挑，也能一句话设计新音色，或者克隆自己的声音。', points: ['一键选角', '用角色自己的台词试听', '设计与克隆声音'], shot: 'characters', r: [520, 456, 2240, 1344], alt: '角色与选角：旁白、林晚、陆沉舟、周伯、阿满各自绑定了平台音色，可以用台词试听或更换' },
  { title: '读音全书统一', text: '人名、地名、多音字和生僻字，AI 先挑出来。每个字的读音从下拉框里选，定一次，全书所有章节、所有角色都照着读。', points: ['多音字与生僻字', '改了读音自动标出要重配的句子'], shot: 'lexicon', r: [520, 600, 2000, 1200], alt: '读音表：重新、扎着、还给、一行脚印、朝雾等词条，每个字上方标注拼音' },
  { title: '真人和 AI 同台', text: '给角色指派真人配音演员，在线逐句录音，或者整章录完上传、自动对轨到每一句。没有指派的角色继续用 AI，混在同一章里。', points: ['逐句录音与提词器', '整章上传自动对轨'], shot: 'workspace', r: [1120, 120, 1760, 1056], alt: '制作台：选中周伯的一句台词，右侧是音频版本、录音和上传按钮、情绪选择' },
  { title: '审听与返工', text: '连续播放，听到问题按 M 标记，不打断收听。智能审听检查音量、底噪和爆音；返工单指派到人，修好后复听关闭。', points: ['边听边标记', '返工单导出 Excel'], shot: 'review', r: [520, 600, 2000, 1200], alt: '审听与工单：第一章已通过，第二章有 2 个待返工的工单' },
  { title: '交付与导出', text: '交付前逐项检查画本、成品、审听、封面和 AI 生成内容标识。导出 MP3、WAV、M4A，或带章节和封面的整本 M4B，附带字幕和分角色分轨。', points: ['交付前检查', '整本 M4B、字幕与分轨'], shot: 'deliver', r: [520, 600, 2000, 1200], alt: '交付前检查：画本、成品音频、审听、书名作者、封面和 AI 生成内容标识逐项核对' },
]
const CYCLE = 8 // seconds each feature stays before the next one, while the section is on screen

function Features() {
  const reduce = useReducedMotion()
  const [active, setActive] = useState(0)
  const [paused, setPaused] = useState(false)
  const [moved, setMoved] = useState(false) // the turning edge only runs once the first screen has been replaced
  const ref = useRef<HTMLDivElement>(null)
  const seen = useInView(ref, { amount: 0.4 })
  const go = (i: number) => { setActive(i); setMoved(true) }
  const running = seen && !paused && !reduce
  const f = FEATURES[active]
  return (
    <section className="sec" id="features" aria-labelledby="feat-h">
      <Head n="02" label="功能" title={<span id="feat-h">一个人，也是一个剧组</span>} sub="选角、读音、录音、审听、交付，有声书制作里每个岗位的活，都有对应的工具。" />
      <Reveal>
        <div className="fx" ref={ref} onPointerEnter={() => setPaused(true)} onPointerLeave={() => setPaused(false)}>
          <div className="fx-list" role="tablist" aria-label="功能">
            {FEATURES.map((x, i) => {
              const on = i === active
              return (
                <div key={x.title} className={'fx-item' + (on ? ' on' : '')}>
                  <button type="button" role="tab" aria-selected={on} id={`fx-${i}`} aria-controls="fx-panel" onClick={() => go(i)}>
                    <span className="fx-n">{String(i + 1).padStart(2, '0')}</span><span className="fx-t">{x.title}</span>
                  </button>
                  <AnimatePresence initial={false}>
                    {on && (
                      <motion.div className="fx-body" initial={{ height: 0, opacity: 0 }} animate={{ height: 'auto', opacity: 1 }} exit={{ height: 0, opacity: 0 }} transition={{ duration: reduce ? 0 : 0.5, ease }}>
                        <p>{x.text}</p>
                        <ul>{x.points.map((p, k) => <motion.li key={p} initial={{ opacity: 0, x: reduce ? 0 : -8 }} animate={{ opacity: 1, x: 0 }} transition={{ duration: 0.4, delay: reduce ? 0 : 0.15 + k * 0.07, ease }}><Tick />{p}</motion.li>)}</ul>
                        <div className="fx-shot-m"><Shot name={x.shot} r={x.r} alt={x.alt} /></div>
                      </motion.div>
                    )}
                  </AnimatePresence>
                  {on && <span className="fx-bar" aria-hidden="true"><i key={active} style={{ animationDuration: `${CYCLE}s`, animationPlayState: running ? 'running' : 'paused' }} onAnimationEnd={() => go((active + 1) % FEATURES.length)} /></span>}
                </div>
              )
            })}
          </div>
          <div className="fx-stage" id="fx-panel" role="tabpanel" aria-labelledby={`fx-${active}`}>
            {/* the next screen is laid over the last one like a turned page, a thin edge running ahead of it */}
            <AnimatePresence initial={false}>
              <motion.div key={active} className="fx-pic" style={{ zIndex: 2 }}
                initial={reduce ? { opacity: 0 } : { clipPath: 'inset(0% 0% 0% 100%)' }} animate={reduce ? { opacity: 1 } : { clipPath: 'inset(0% 0% 0% 0%)' }}
                exit={reduce ? { zIndex: 1, opacity: 0 } : { zIndex: 1, scale: 0.96, opacity: 0.35, transition: { duration: 0.95, ease: easeInOut } }} transition={{ duration: reduce ? 0.3 : 0.95, ease: easeInOut }}>
                <motion.div className="fx-zoom" initial={reduce ? false : { scale: 1.1, x: '4%' }} animate={{ scale: reduce ? 1 : [1.1, 1, 1.035], x: reduce ? 0 : ['4%', '0%', '0%'] }}
                  transition={{ duration: CYCLE + 1, times: [0, 0.14, 1], ease: 'easeOut' }}>
                  <Shot name={f.shot} r={f.r} alt={f.alt} />
                </motion.div>
              </motion.div>
            </AnimatePresence>
            {!reduce && moved && <motion.i key={'e' + active} className="fx-edge" initial={{ left: '100%', opacity: 1 }} animate={{ left: '0%', opacity: [1, 1, 0] }} transition={{ duration: 0.95, ease: easeInOut, opacity: { times: [0, 0.85, 1], duration: 0.95 } }} aria-hidden="true" />}
          </div>
        </div>
      </Reveal>
    </section>
  )
}

// ---------- 03 how ----------
const STEPS: { n: string; title: string; text: string; detail: string }[] = [
  { n: '第一步', title: '上传书稿', text: 'txt、docx、epub、pdf 都可以。章节自动拆好，每章的配音进度和审听状态一目了然。', detail: '导入时确认版权，平台不审查也不保证内容的版权' },
  { n: '第二步', title: '选一种演播方式', text: '单播、多播有声书或广播剧，选好质量档，交给一键成书：分析角色、选角、画本、配音、配乐、合成按顺序完成。', detail: '开始前显示预计消耗，进度在任务中心查看' },
  { n: '第三步', title: '进制作台精修', text: '点任意一句，换说话人、改情绪、插表演标签、试听不同版本，或者直接录音。数字键换说话人，M 标记返工。', detail: '每句话有 2 次免费重新生成' },
]
const AUDIENCE: [string, string][] = [
  ['个人创作者', '把自己写的小说做成多人演播的有声书，不用学配音和剪辑。'],
  ['有声工作室', '画本、配音、后期、审听按职位分工，派活、进度和计薪都在一处。'],
  ['配音演员', '在「我的台词」里逐句录音，或整章录完上传，自动对轨。'],
  ['版权方与平台', '批量把书库有声化，导出带章节的 M4B，绑定渠道直接发布。'],
]
function How() {
  const reduce = useReducedMotion()
  const ref = useRef<HTMLOListElement>(null)
  const dots = useRef<(HTMLSpanElement | null)[]>([])
  // a line is drawn through the three steps as you read down; each step lights up when it reaches it
  const { scrollYProgress } = useScroll({ target: ref, offset: ['start 0.8', 'end 0.55'] })
  const still = useMotionValue(1)
  const prog = reduce ? still : scrollYProgress
  const [reached, setReached] = useState(reduce ? STEPS.length : 0)
  useMotionValueEvent(prog, 'change', (v) => {
    const box = ref.current
    if (!box) return
    const b = box.getBoundingClientRect()
    const vertical = b.height > b.width * 0.8
    const len = vertical ? b.height : b.width
    setReached(dots.current.filter((d) => {
      if (!d) return false
      const r = d.getBoundingClientRect()
      return v * len >= (vertical ? r.top - b.top : r.left - b.left) - 2
    }).length)
  })
  return (
    <section className="sec" id="how" aria-labelledby="how-h">
      <Head n="03" label="流程" title={<span id="how-h">三步做完一本书</span>} sub="从书稿到成品，大部分步骤都可以交给 AI；你只在想精修的地方动手。" />
      <motion.ol className="steps" ref={ref} style={{ '--s': prog } as any}>
        {STEPS.map((s, i) => (
          <Reveal as="li" key={s.n} delay={i * 0.1} className={i < reached ? 'lit' : ''}>
            <span className="step-dot" ref={(el) => { dots.current[i] = el }} aria-hidden="true" />
            <span className="step-n">{s.n}</span>
            <h3>{s.title}</h3>
            <p>{s.text}</p>
            <p className="step-d">{s.detail}</p>
          </Reveal>
        ))}
      </motion.ol>
      <div className="for">
        <Reveal className="for-t">适合谁</Reveal>
        <ul>{AUDIENCE.map(([t, d], i) => <Reveal as="li" key={t} delay={0.08 + i * 0.08}><b>{t}</b><span>{d}</span></Reveal>)}</ul>
      </div>
    </section>
  )
}

// ---------- 04 pricing ----------
/** A number that counts up from zero the first time it is seen. */
function CountUp({ value, prefix = '' }: { value: number; prefix?: string }) {
  const ref = useRef<HTMLSpanElement>(null)
  const seen = useInView(ref, { once: true, amount: 0.8 })
  const reduce = useReducedMotion()
  useEffect(() => {
    const el = ref.current
    if (!el) return
    if (!seen || reduce || !value) { el.textContent = prefix + (seen || reduce ? value : 0); return }
    const c = animate(0, value, { duration: 1.3, ease: [0.16, 1, 0.3, 1], onUpdate: (v) => { el.textContent = prefix + Math.round(v) } })
    return () => c.stop()
  }, [seen, value, prefix, reduce])
  return <span ref={ref}>{prefix}{value}</span>
}

function Pricing({ plans, g, authed, contact }: { plans: Plan[]; g: Guide; authed: boolean; contact: string }) {
  const reduce = useReducedMotion()
  const rows = (p: Plan): [string, boolean][] => {
    const r = reach(p.monthlyCredits, p.creditRate, g)
    return [
      [`每月 ${wan(p.monthlyCredits)}积分${p.bonusCredits ? `，开通再送 ${wan(p.bonusCredits)}` : ''}`, true],
      [`${r.chars}，${r.hours}`, true],
      [p.maxBooks ? `最多 ${p.maxBooks} 本书` : '图书数量不限', true],
      [`${p.seats} 名成员，${p.storageGb.toLocaleString('zh-CN')} GB 存储`, true],
      [`${p.cloneVoices} 个声音克隆`, true],
      ['精品音色与商用授权', p.highQuality && p.commercial],
      [p.exportFormats?.length ? `导出 ${p.exportFormats.map((x) => x.toUpperCase()).join('、')}` : '导出全部格式，含整本 M4B', true],
      [p.creditRate > 0 && p.creditRate < 100 ? `积分 ${p.creditRate % 10 ? p.creditRate : p.creditRate / 10} 折` : '积分原价', p.creditRate > 0 && p.creditRate < 100],
    ]
  }
  const cta = (p: Plan) => {
    if (p.contactSales) return contact ? { href: `mailto:${contact}?subject=${encodeURIComponent('咨询声书工坊企业版')}`, label: '联系我们' } : { to: authed ? '/membership' : '/register', label: authed ? '查看会员' : '先免费注册' }
    if (authed) return { to: '/membership', label: p.priceMonthly ? `开通${p.name}` : '进入工作台' }
    return { to: '/register', label: p.priceMonthly ? `选择${p.name}` : '免费开始' }
  }
  return (
    <section className="sec" id="pricing" aria-labelledby="price-h">
      <Head n="04" label="价格" title={<span id="price-h">从免费开始</span>} sub="积分用于画本、配音、设计音色等 AI 功能，生成前都会显示预计消耗。" />
      <Curtain className="plans">
        {plans.map((p, i) => {
          const c = cta(p)
          const cls = 'btn btn-block ' + (p.highlight ? 'btn-primary' : 'btn-line')
          return (
            <motion.div key={p.code} className={'plan' + (p.highlight ? ' hl' : '')} initial={{ opacity: 0, y: reduce ? 0 : 24 }} whileInView={{ opacity: 1, y: 0 }} viewport={{ once: true, amount: 0.3 }} transition={{ duration: 0.8, delay: reduce ? 0 : 0.25 + i * 0.09, ease }}>
              <div className="plan-top">
                <h3>{p.name}{p.highlight && <em>推荐</em>}</h3>
                <p className="plan-tag">{p.tagline}</p>
              </div>
              <p className="plan-price">{p.contactSales ? '按需报价' : <><CountUp value={Math.round(p.priceMonthly / 100)} prefix="¥" /><small>/月</small></>}</p>
              {'href' in c && c.href ? <a className={cls} href={c.href}><Roll>{c.label}</Roll></a> : <Link className={cls} to={(c as any).to} viewTransition><Roll>{c.label}</Roll></Link>}
              <ul>{rows(p).map(([t, on]) => <li key={t} className={on ? '' : 'off'}>{on ? <Tick /> : <span className="dash" />}{t}</li>)}</ul>
            </motion.div>
          )
        })}
      </Curtain>
      <p className="note">「大约能做」按标准档音色估算，精品音色消耗更多、经济音色更少。</p>
    </section>
  )
}

// ---------- 05 faq ----------
const FAQ: [string, string][] = [
  ['需要会配音或剪辑吗？', '不需要。上传书稿后，拆章、分析角色、选角、标注画本、配音和合成都可以交给 AI；想精修时再进制作台逐句调整。'],
  ['书稿的版权谁负责？', '平台只提供制作工具，不审查也不保证任何内容的版权。书稿须是你的原创、已取得有声改编授权，或已进入公有领域；因版权产生的纠纷由使用者自行承担。'],
  ['用的是哪些 AI 模型？', '大语言模型支持 OpenAI、Claude 和兼容协议的服务（如 DeepSeek、通义千问）；语音合成接入了千问和 MiniMax。具体使用哪些由平台管理员配置。'],
  ['做出来的声音可以商用吗？', '个人版及以上包含精品音色和商用授权；免费版用于体验，不含商用授权。发布到平台时请按平台要求标注 AI 生成。'],
  ['积分是怎么算的？', '画本和配音按字数计算，设计音色、克隆按次数，对轨和审听按音频时长。开始生成前都会显示预计消耗；相同内容重复生成命中缓存，不扣积分。'],
  ['人名、多音字读错了怎么办？', '在「读音」里定好读法，全书统一生效；已经配好的相关句子会标为有改动，一键重新生成。每句话还有 2 次免费重新生成。'],
  ['能克隆自己的声音吗？', '可以。克隆本人需要朗读一段验证句；克隆他人需要上传声音主人的书面授权。可克隆的数量按会员方案不同。'],
  ['能和真人配音演员一起做吗？', '可以。把角色设为真人配音并指派给演员，演员在「我的台词」里逐句录音，或整章录完上传后智能对轨。'],
  ['导出成品是什么格式？', 'MP3、WAV、M4A，或整本书一个带章节标记和封面的 M4B；还可以附带字幕、分角色分轨、演播稿和台词本。免费版只能导出 MP3。'],
  ['一本书大概要多久？', '取决于字数和排队情况。一键成书会自动走完全部步骤并在任务中心显示进度，你可以随时进制作台精修已经完成的章节。'],
]
function Faq() {
  const [open, setOpen] = useState<number | null>(null)
  const reduce = useReducedMotion()
  return (
    <section className="sec" id="faq" aria-labelledby="faq-h">
      <Head n="05" label="常见问题" title={<span id="faq-h">还想知道的</span>} />
      <div className="faq">
        {FAQ.map(([q, a], i) => {
          const on = open === i
          return (
            <Reveal key={q} delay={(i % 2) * 0.06 + Math.floor(i / 2) * 0.05} className={'qa' + (on ? ' on' : '')}>
              <button type="button" aria-expanded={on} aria-controls={`qa-${i}`} onClick={() => setOpen(on ? null : i)}>
                <span>{q}</span><i aria-hidden="true" />
              </button>
              <AnimatePresence initial={false}>
                {on && (
                  <motion.div id={`qa-${i}`} className="qa-a" initial={{ height: 0, opacity: 0 }} animate={{ height: 'auto', opacity: 1 }} exit={{ height: 0, opacity: 0 }} transition={{ duration: reduce ? 0 : 0.4, ease }}>
                    <motion.p initial={{ y: reduce ? 0 : -6 }} animate={{ y: 0 }} transition={{ duration: 0.4, ease }}>{a}</motion.p>
                  </motion.div>
                )}
              </AnimatePresence>
            </Reveal>
          )
        })}
      </div>
    </section>
  )
}

// ---------- closing ----------
/** The sample book as a small hardback: it turns to face you as it scrolls in and leans after the pointer. */
function Book() {
  const reduce = useReducedMotion()
  const fine = useMedia('(pointer: fine)')
  const ref = useRef<HTMLDivElement>(null)
  const { scrollYProgress } = useScroll({ target: ref, offset: ['start end', 'center 0.55'] })
  const px = useSpring(0, { stiffness: 120, damping: 16 })
  const py = useSpring(0, { stiffness: 120, damping: 16 })
  const turn = useTransform(scrollYProgress, [0, 1], [reduce ? 26 : 62, 26])
  const rotateY = useTransform([turn, px], ([t, x]: number[]) => t + x * 18)
  const rotateX = useTransform(py, (y) => -7 - y * 10)
  const shadowX = useTransform(rotateY, (r) => (r - 26) * -0.6)
  useEffect(() => {
    if (!fine || reduce) return
    const sec = ref.current?.closest('section')
    if (!sec) return
    const move = (e: PointerEvent) => {
      const b = sec.getBoundingClientRect()
      px.set(((e.clientX - b.left) / b.width - 0.5) * 2)
      py.set(((e.clientY - b.top) / b.height - 0.5) * 2)
    }
    const out = () => { px.set(0); py.set(0) }
    sec.addEventListener('pointermove', move)
    sec.addEventListener('pointerleave', out)
    return () => { sec.removeEventListener('pointermove', move); sec.removeEventListener('pointerleave', out) }
  }, [fine, reduce, px, py])
  return (
    <div className="book3d" ref={ref}>
      <motion.span className="bk-shadow" style={{ x: shadowX }} aria-hidden="true" />
      <motion.div className="bk" style={{ rotateY, rotateX }}>
        <img className="bk-front" src={cover} alt="示例作品《雾港来信》的封面" width={240} height={240} loading="lazy" />
        <span className="bk-spine" style={{ backgroundImage: `url(${cover})` }} aria-hidden="true"><b>雾港来信</b></span>
        <span className="bk-top" aria-hidden="true" />
        <span className="bk-edge" aria-hidden="true" />
        <span className="bk-back" aria-hidden="true" />
      </motion.div>
    </div>
  )
}

function Closing({ action, slogan }: { action: { to: string; label: string }; slogan: string }) {
  const ref = useRef<HTMLHeadingElement>(null)
  const reduce = useReducedMotion()
  const { scrollYProgress } = useScroll({ target: ref, offset: ['start 0.92', 'start 0.42'] })
  const still = useMotionValue(1)
  return (
    <section className="end" aria-labelledby="end-h">
      <div className="end-in">
        <div>
          <h2 id="end-h" ref={ref} aria-label={slogan}><InkText text={slogan} progress={reduce ? still : scrollYProgress} /></h2>
          <Reveal delay={0.1}><p>下一本有声书，可以从今晚开始。不用装软件，打开浏览器，把书稿拖进来。</p></Reveal>
          <Reveal delay={0.2} className="cta">
            <Magnetic><Link to={action.to} viewTransition className="btn btn-primary"><Roll>{action.label}</Roll><Arrow /></Link></Magnetic>
            <a href="#pricing" className="btn btn-quiet">看看会员方案</a>
          </Reveal>
        </div>
        <figure className="book">
          <Book />
          <figcaption>本页截图里的示例作品《雾港来信》：3 章，旁白和 4 个角色，从书稿做到成品。</figcaption>
        </figure>
      </div>
    </section>
  )
}

function Footer({ site }: { site: any }) {
  const year = new Date().getFullYear()
  return (
    <footer className="foot">
      <div className="foot-in">
        <div className="foot-brand">
          <span className="brand"><Logo /><span>{site?.name || '声书工坊'}</span></span>
          <p>AI 有声书工作台</p>
        </div>
        <div className="foot-cols">
          <div><span>产品</span><a href="#try">原文变画本</a><a href="#features">功能</a><a href="#how">流程</a><a href="#pricing">价格</a></div>
          <div><span>开始使用</span><Link to="/register" viewTransition>注册</Link><Link to="/login" viewTransition>登录</Link><a href="#faq">常见问题</a></div>
          <div><span>协议与政策</span><Link to="/legal/terms">用户协议</Link><Link to="/legal/privacy">隐私政策</Link><Link to="/legal/copyright">版权与素材使用声明</Link><Link to="/legal/ai">AI 生成内容规范</Link></div>
          <div>
            <span>联系</span>
            {site?.supportEmail ? <a href={`mailto:${site.supportEmail}`}>{site.supportEmail}</a> : <em>注册后可在帮助中心联系我们</em>}
            {site?.operator && <em>{site.operator}</em>}
          </div>
        </div>
      </div>
      <div className="foot-legal">
        <span>© {year} {site?.operator || site?.name || '声书工坊'}</span>
        {site?.icp && <a href="https://beian.miit.gov.cn/" target="_blank" rel="noreferrer">{site.icp}</a>}
        <span>AI 生成的声音发布时请按平台要求标注</span>
      </div>
    </footer>
  )
}

/** Eased, weighted scrolling for wheel and trackpad (touch keeps the phone's own), including the page's # links. */
function useSmoothScroll() {
  useEffect(() => {
    if (matchMedia('(prefers-reduced-motion: reduce)').matches) return
    const lenis = new Lenis({ autoRaf: true, lerp: 0.1, wheelMultiplier: 0.95, anchors: { offset: -24 } })
    return () => lenis.destroy()
  }, [])
}

export default function Landing() {
  const user = useSession((s) => s.user)
  const site = useSession((s) => s.site) as any
  const plansQ = useQuery({ queryKey: ['lp-plans'], queryFn: () => api.get<Plan[]>('/plans'), staleTime: 300_000 })
  const guideQ = useQuery({ queryKey: ['lp-guide'], queryFn: () => api.get<Guide>('/credits/guide'), staleTime: 300_000 })
  const plans = plansQ.data?.length ? plansQ.data : FALLBACK_PLANS
  const g = guideQ.data?.per10kChars?.standard ? guideQ.data : FALLBACK_GUIDE
  const free = plans.find((p) => p.code === 'free') ?? plans[0]
  const canRegister = site?.registrationOpen !== false && !site?.inviteOnly
  const authed = !!user
  const action = authed ? { to: '/', label: '进入工作台' } : canRegister ? { to: '/register', label: '免费开始' } : { to: '/login', label: '登录' }
  const [ink, setInk] = useState(false)
  useSmoothScroll()
  useEffect(() => {
    const prev = document.title
    document.title = `${site?.name || '声书工坊'} · AI 有声书工作台`
    return () => { document.title = prev }
  }, [site?.name])
  return (
    <div className="lp">
      <Nav action={action} authed={authed} ink={ink} />
      <main>
        <Hero action={action} g={g} freeCredits={free?.monthlyCredits ?? 10000} authed={authed} />
        <Listen onInk={setInk} />
        <Demo />
        <Features />
        <How />
        <Pricing plans={plans} g={g} authed={authed} contact={site?.supportEmail ?? ''} />
        <Faq />
        <Closing action={action} slogan={(site?.slogan || '让每一本书，都被好好听见').replace(/^让每一本书都/, '让每一本书，都')} />
      </main>
      <Footer site={site} />
    </div>
  )
}
