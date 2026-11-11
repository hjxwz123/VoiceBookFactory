// A small rule-based annotator for the landing page's "原文 → 画本" demo. It runs in the browser on
// whatever the visitor types. The product itself asks a language model; the page says so next to the demo.

export type Line = {
  key: string
  para: number // which paragraph of the passage the line belongs to
  speaker: string // '旁白' or a character name
  kind: 'narration' | 'dialogue'
  text: string
  emotion: string
  direction: string
  tag?: string
  voice: string
  color: string
}

const PALETTE = ['#d64575', '#7b57d1', '#e08a1e', '#1f8f7a', '#2f6fd0', '#b5483b']
// real platform voices: a narrator, then voices handed out to characters by a rough gender guess
const NARRATOR_VOICE = '谢舒柔'
const FEMALE = ['叶清禾', '叶知晴', '许玉远', '安小岚']
const MALE = ['龙岚珺蕊', '龙岭芷杏', '龙修', '安明远']
// the sample book's real casting (and the colours the product gave them), so the demo matches the screenshots
const KNOWN: Record<string, { voice: string; color: string }> = {
  林晚: { voice: '叶清禾', color: '#d64575' },
  周伯: { voice: '龙岭芷杏', color: '#7b57d1' },
  陆沉舟: { voice: '龙岚珺蕊', color: '#e08a1e' },
  阿满: { voice: '叶知晴', color: '#1f8f7a' },
}
const MALE_HINT = /[伯叔爷翁哥兄舟虎龙刚强军父爹郎公生]/
const FEMALE_HINT = /[姐妹娘婆姑嫂晚满婉玉莲芳娟婷雪梅兰丫妈]/

const STOP = new Set(['他', '她', '它', '我', '你', '他们', '她们', '男人', '女人', '老人', '孩子', '小姑娘', '小声', '轻声', '低声', '大声', '笑着', '冷冷', '忽然', '终于', '那天', '晚上', '清晨', '门外', '灯塔', '邮局', '院子', '外面', '有些', '眼眶', '声音', '一个', '这里'])
const SUBJECT_NEXT = '(?=的|把|将|站|举|踮|放|摸|望|盯|接|躲|蹲|推|握|愣|笑|轻|冷|大|小|叹|终|忽|又|说|问|道|喊|答|低|抬|看|走|回|点|摇|拍|拉|转|停|想)'

/** Names that act in the passage: subjects of clauses, speakers of 说/问/道, and self-introductions. */
function scanNames(text: string): string[] {
  const found = new Map<string, number>()
  const add = (n: string) => !STOP.has(n) && found.set(n, (found.get(n) ?? 0) + 1)
  for (const m of text.matchAll(new RegExp('(?:^|[。，！？：；\\n“”「」])\\s*([\\u4e00-\\u9fa5]{2,3}?)' + SUBJECT_NEXT, 'g'))) add(m[1])
  for (const m of text.matchAll(/我(?:是|叫)[^，。！？”]*?([\u4e00-\u9fa5]{2,3})[，。！？”]/g)) add(/^[员的是叫人长师生手者]/.test(m[1]) || (m[1].length === 3 && found.has(m[1].slice(1))) ? m[1].slice(1) : m[1])
  // keep the longest spelling of each name (陆沉舟 over 沉舟)
  const names = [...found.keys()]
  return names.filter((n) => !names.some((o) => o !== n && o.includes(n) && (found.get(o) ?? 0) >= 1 && n.length < o.length))
}

function emotionOf(context: string, text: string): { emotion: string; direction: string; tag?: string } {
  if (/叹(了口)?气/.test(context)) return { emotion: '悲伤', direction: '叹一口气再开口', tag: '叹气' }
  if (/(大声|喊|叫道)/.test(context) || /！.*！/.test(text)) return { emotion: '喊叫', direction: '放开声音，带着兴奋', tag: '大喊' }
  if (/(小声|低声|轻声|压得很低|耳语)/.test(context)) return { emotion: '低语', direction: '压低声音', tag: '耳语' }
  if (/(冷冷|冷笑|不屑)/.test(context)) return { emotion: '冷淡', direction: '冷淡，带着距离感', tag: '轻蔑' }
  if (/笑/.test(context)) return { emotion: '开心', direction: '笑着说', tag: '轻笑' }
  if (/(颤|怕|躲|慌)/.test(context)) return { emotion: '紧张', direction: '声音发紧，有点害怕', tag: '颤抖' }
  if (/(愣|惊|竟然)/.test(context) || /[？?]$/.test(text) && /(您|早就|怎么)/.test(text)) return { emotion: '惊讶', direction: '意外，脱口而出', tag: '倒吸气' }
  if (/[？?]/.test(text)) return { emotion: '疑问', direction: '带着疑问', tag: '好奇' }
  if (/！/.test(text)) return { emotion: '激动', direction: '语气上扬' }
  return { emotion: '平静', direction: '' }
}

/** Splits a passage into narration and quoted lines and guesses who says each quote. */
export function annotate(raw: string): Line[] {
  const paras = raw.split(/\n+/).map((p) => p.trim()).filter(Boolean).slice(0, 14)
  const known = scanNames(paras.join('\n'))
  const voices = new Map<string, { voice: string; color: string }>()
  const male = (n: string) => MALE_HINT.test(n) && !FEMALE_HINT.test(n)
  const cast = (name: string) => {
    if (!voices.has(name) && KNOWN[name]) voices.set(name, KNOWN[name])
    if (!voices.has(name)) {
      const pool = male(name) ? MALE : FEMALE
      const same = [...voices.keys()].filter((k) => male(k) === male(name)).length
      voices.set(name, { voice: pool[same % pool.length], color: PALETTE[voices.size % PALETTE.length] })
    }
    return voices.get(name)!
  }
  const mentioned: string[] = [] // names in reading order, for 他/她
  const resolve = (pronoun: string, not?: string | null) => {
    for (let i = mentioned.length - 1; i >= 0; i--) {
      const n = mentioned[i]
      if (n === not) continue
      if (pronoun === '他' && !male(n)) continue
      if (pronoun === '她' && male(n)) continue
      return n
    }
    return null
  }
  // the subject of a stretch of narration: its last clause that names someone (or says 他/她)
  const subjectOf = (narr: string, fromEnd: boolean, not?: string | null): string | null => {
    const clauses = narr.split(/[。，！？；：]/).map((c) => c.trim()).filter(Boolean)
    const order = fromEnd ? [...clauses].reverse() : clauses
    for (const c of order) {
      const named = known.map((n) => [n, c.indexOf(n)] as const).filter(([, i]) => i >= 0).sort((a, b) => a[1] - b[1])[0]
      if (named && named[1] <= 1) return named[0]
      const pr = c.match(/^(他|她)/)
      if (pr) return resolve(pr[1], not)
      if (named) return named[0]
    }
    return null
  }
  const note = (s: string) => known.forEach((n) => { let i = s.indexOf(n); while (i >= 0) { mentioned.push(n); i = s.indexOf(n, i + n.length) } })

  const out: Line[] = []
  let last: string | null = null
  let n = 0
  paras.forEach((para, pi) => {
    const parts = para.split(/(“[^”]*”|「[^」]*」|"[^"]*")/).filter((p) => p.trim())
    parts.forEach((part, i) => {
      const quoted = /^[“「"]/.test(part)
      if (!quoted) {
        const text = part.replace(/^[，。：:]+/, '').trim()
        if (!text || /^[，。：:！？\s]+$/.test(text)) return
        if (i === parts.length - 1 || !/^[“「"]/.test(parts[i + 1] ?? '')) note(text) // trailing narration names the next speaker's world
        else if (i === 0) note(text)
        out.push({ key: `n${n++}`, para: pi, speaker: '旁白', kind: 'narration', text, emotion: '', direction: '', voice: NARRATOR_VOICE, color: '#8b909c' })
        return
      }
      const before = parts.slice(0, i).filter((p) => !/^[“「"]/.test(p)).join('，')
      const after = parts.slice(i + 1).find((p) => !/^[“「"]/.test(p)) ?? ''
      const self = part.match(/我(?:是|叫)[^，。！？”]*?([\u4e00-\u9fa5]{2,3})[，。！？”]/)
      let who: string | null = self ? known.find((k) => self[1].endsWith(k)) ?? null : null
      if (!who && before) { note(before); who = subjectOf(before, true) }
      if (!who && after) who = subjectOf(after, false, last)
      if (!who) who = known.find((k) => k !== last && mentioned.includes(k)) ?? last ?? '角色'
      note(after)
      mentioned.push(who)
      last = who
      const v = cast(who)
      const mood = emotionOf(before.slice(-14) + after.slice(0, 12), part)
      out.push({ key: `d${n++}`, para: pi, speaker: who, kind: 'dialogue', text: part, ...mood, voice: v.voice, color: v.color })
    })
  })
  return out
}

export const SAMPLES = [
  `周伯的手停了一下。他接过信，对着窗口的光看了很久。
“灯塔那边，已经二十年没人收过信了。”他把信还给她，声音压得很低，“你要是怕，就放回去。”
“我不怕。”林晚把信塞进外套内袋，“送信是我的工作。”
“晚姐姐！”阿满举着一只纸折的小船，“你能带我去灯塔看看吗？”
林晚笑着摸了摸她的头：“去灯塔可不是玩的，路上全是礁石。”`,
  `灯塔的铁门没有锁。一个高瘦的男人站在楼梯的阴影里。
“你们找谁？”陆沉舟冷冷地问。
“我是雾港邮局的投递员林晚。”她把信递过去。
阿满躲到林晚身后，小声问：“晚姐姐，他是谁呀？”
陆沉舟蹲下来，轻声说：“别怕，小姑娘。”`,
  `那天晚上，鹭屿的灯塔亮了。
“亮了！真的亮了！”阿满大声喊着，用力挥着那只纸船。
周伯叹了口气：“老灯塔的灯，是我和你母亲一起点的第一盏。”
林晚愣住了：“周伯，您早就知道？”`,
]
