import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'
import 'dayjs/locale/zh-cn'

dayjs.extend(relativeTime)
dayjs.locale('zh-cn')

export const fromNow = (t?: string | null) => (t ? dayjs(t).fromNow() : '—')
export const dateTime = (t?: string | null) => (t ? dayjs(t).format('YYYY-MM-DD HH:mm') : '—')
export const date = (t?: string | null) => (t ? dayjs(t).format('YYYY-MM-DD') : '—')

export function duration(ms?: number | null) {
  if (!ms || ms < 0) return '0:00'
  const s = Math.round(ms / 1000)
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const ss = String(s % 60).padStart(2, '0')
  return h ? `${h}:${String(m).padStart(2, '0')}:${ss}` : `${m}:${ss}`
}

/** Length of a short clip such as one line: "2.4 秒" (a plain duration would round it to 0:02 or 0:00). */
export function clipLength(ms?: number | null) {
  if (!ms || ms < 0) return '—'
  return ms < 60000 ? `${(ms / 1000).toFixed(1)} 秒` : duration(ms)
}

export function timecode(ms: number) {
  const s = Math.max(0, ms) / 1000
  const m = Math.floor(s / 60)
  return `${String(m).padStart(2, '0')}:${(s % 60).toFixed(1).padStart(4, '0')}`
}

export function num(n?: number | null) {
  if (n === undefined || n === null) return '0'
  if (Math.abs(n) >= 10000) return (n / 10000).toFixed(n >= 100000 ? 0 : 1).replace(/\.0$/, '') + ' 万'
  return n.toLocaleString('zh-CN')
}

export function bytes(n?: number | null) {
  if (!n) return '0 B'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = n
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${u[i]}`
}

export const pct = (v?: number | null) => `${Math.round((v ?? 0) * 100)}%`

export const yuan = (cent?: number | null) => `¥${((cent ?? 0) / 100).toFixed(2)}`

export const genderName: Record<string, string> = { male: '男', female: '女', unknown: '未知' }
export const ageName: Record<string, string> = { child: '儿童', teen: '少年', young: '青年', adult: '成年', middle: '中年', senior: '老年', unknown: '未知' }
export const kindName: Record<string, string> = { narrator: '旁白', lead: '主角', supporting: '配角', minor: '次要角色', crowd: '群演' }
export const tierName: Record<string, string> = { economy: '经济', standard: '标准', premium: '精品' }
export const modeName: Record<string, string> = { single: '单播有声书', multi: '多播有声书', drama: '广播剧' }
export const bookStatusName: Record<string, string> = { preparing: '筹备中', producing: '制作中', completed: '已完成', published: '已发布', archived: '已归档', moderation: '审核中' }
export const roleName: Record<string, string> = { owner: '所有者', admin: '管理员', producer: '制作人', member: '成员', guest: '外部协作者' }
export const positionName: Record<string, string> = { lead: '负责人', script: '画本', voice: '配音', post: '后期', review: '审听' }
export const segTypeName: Record<string, string> = { narration: '旁白', dialogue: '对白', monologue: '内心独白', sfx: '音效', pause: '停顿' }
export const emotions: [string, string][] = [
  ['neutral', '平静'], ['happy', '开心'], ['sad', '悲伤'], ['angry', '愤怒'], ['fearful', '恐惧'], ['surprised', '惊讶'],
  ['disgusted', '厌恶'], ['gentle', '温柔'], ['nervous', '紧张'], ['tired', '疲惫'], ['whisper', '低语'], ['shout', '喊叫'],
]
export const emotionName: Record<string, string> = Object.fromEntries(emotions)
export const performanceTags = ['笑', '冷笑', '叹气', '哭腔', '哽咽', '喘息', '停顿', '轻声', '气声', '惊呼', '咳嗽']
export const jobStatusName: Record<string, string> = { queued: '排队中', running: '进行中', succeeded: '已完成', failed: '失败', canceled: '已取消', paused: '已暂停' }
export const taskStatusName: Record<string, string> = { pending: '未开始', in_progress: '进行中', reviewing: '待审核', done: '已完成', rejected: '已打回' }
export const issueStatusName: Record<string, string> = { open: '待处理', in_progress: '处理中', verifying: '待复核', closed: '已关闭', disputed: '有异议' }
export const issueTypes: [string, string][] = [
  ['missing', '漏读'], ['wrong', '错读'], ['extra', '多读'], ['pronunciation', '读音'], ['performance', '情绪/演绎'], ['noise', '噪音/音质'], ['edit', '剪辑'], ['script', '画本错误'], ['other', '其他'],
]
export const featureName: Record<string, string> = {
  script: '智能画本', character: '角色分析', lexicon: '发音词条', casting: 'AI 选角', tts: 'AI 配音', preview: '试听', voice_design: 'AI 设计音色',
  voice_clone: '声音克隆', sfx: 'AI 音效', music: 'AI 配乐', image: 'AI 封面', asr_align: '智能对轨', asr_review: '智能审听', moderation: '内容审核', quick_tts: '快速配音',
}

/** Copies text. The clipboard API only exists on HTTPS (and localhost); when the site is opened by plain
 *  http, e.g. http://IP:8080, it falls back to selecting a hidden textarea. Rejects if neither works. */
export async function copy(text: string): Promise<void> {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text)
      return
    } catch {
      // permission denied or document not focused: try the fallback below
    }
  }
  // inside a dialog the textarea must live in the dialog, or its focus trap pulls the selection away
  const host = (document.activeElement?.closest('[role=dialog]') as HTMLElement | null) ?? document.body
  const ta = document.createElement('textarea')
  ta.value = text
  ta.setAttribute('readonly', '')
  Object.assign(ta.style, { position: 'fixed', top: '0', left: '-9999px', opacity: '0' })
  host.appendChild(ta)
  ta.focus({ preventScroll: true })
  ta.select()
  let ok = false
  try {
    ok = document.execCommand('copy')
  } finally {
    ta.remove()
  }
  if (!ok) throw new Error('copy failed')
}
