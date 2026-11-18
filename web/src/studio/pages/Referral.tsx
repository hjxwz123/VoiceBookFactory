import { useQuery, useQueryClient } from '@tanstack/react-query'
import qrcode from 'qrcode-generator'
import { useEffect, useMemo, useRef, useState } from 'react'
import { toast } from 'sonner'
import { Copy, Download, Gift, Link2, Pencil, Users } from '@/components/icons'
import { api } from '@/lib/api'
import { copy, date } from '@/lib/format'
import { Badge, Button, Dialog, Empty, Field, Input, PageHeader, Pagination, Panel, Skeleton, Stat, Table, Tabs } from '@/components/ui'

type Rules = {
  inviterCredits: number; inviteeCredits: number; inviterAfter: 'signup' | 'payment'; creditDays: number; dailyLimit: number; totalLimit: number
  commissionRate: number; creditsPerYuan: number; commissionOn: 'first' | 'all'; commissionDays: number; signupCredits: number
}
type Me = {
  enabled: boolean; code: string; link: string; disabled: boolean; renamesLeft: number; rules: Rules
  stats: { visits: number; signups: number; paying: number; pending: number; earned: number }
  poster: { title: string; text: string; siteName: string; slogan: string; nickname: string }
}

const n = (v: number) => v.toLocaleString('zh-CN')

/** The rules in plain sentences, in the order a user cares about them. */
function ruleLines(r: Rules): string[] {
  const out: string[] = []
  if (r.inviterCredits > 0) out.push(r.inviterAfter === 'payment' ? `好友通过你的邀请注册并完成首次充值后，你得 ${n(r.inviterCredits)} 积分。` : `好友通过你的邀请注册，你立即得 ${n(r.inviterCredits)} 积分。`)
  if (r.inviteeCredits > 0) out.push(`好友注册时，在新用户赠送之外再得 ${n(r.inviteeCredits)} 积分。`)
  if (r.commissionRate > 0 && r.creditsPerYuan > 0) {
    const per = (100 * r.commissionRate / 100) * r.creditsPerYuan
    out.push(`好友${r.commissionOn === 'first' ? '首次' : '每次'}充值，你获得充值金额 ${r.commissionRate}% 的返利，按 1 元 = ${n(r.creditsPerYuan)} 积分发放（好友充值 100 元，你得 ${n(Math.floor(per))} 积分）${r.commissionDays > 0 ? `，限好友注册后 ${r.commissionDays} 天内的充值` : ''}。`)
  }
  if (r.dailyLimit > 0 || r.totalLimit > 0) out.push(`注册奖励${r.dailyLimit > 0 ? `每 24 小时最多 ${r.dailyLimit} 次` : ''}${r.dailyLimit > 0 && r.totalLimit > 0 ? '，' : ''}${r.totalLimit > 0 ? `累计最多 ${r.totalLimit} 次` : ''}；超出后好友照常注册，只是不再发放这项奖励。`)
  out.push(r.creditDays > 0 ? `奖励积分自到账起 ${r.creditDays} 天内有效。` : '奖励积分长期有效。')
  out.push('每位好友只能被邀请一次，只对新注册的账号有效；用不正当方式刷奖励的，平台会收回奖励并停用邀请码。')
  return out
}

function headline(r: Rules): string {
  const parts: string[] = []
  if (r.inviterCredits > 0) parts.push(`每邀请一位好友${r.inviterAfter === 'payment' ? '并完成首充' : '注册'}，你得 ${n(r.inviterCredits)} 积分`)
  if (r.commissionRate > 0 && r.creditsPerYuan > 0) parts.push(`好友充值再返 ${r.commissionRate}%`)
  if (r.inviteeCredits > 0) parts.push(`好友也能多得 ${n(r.inviteeCredits)} 积分`)
  return parts.length ? parts.join('，') + '。' : '把声书工坊分享给朋友。'
}

export default function Referral() {
  const qc = useQueryClient()
  const { data: me, isLoading } = useQuery<Me>({ queryKey: ['referral'], queryFn: () => api.get('/referral') })
  const [tab, setTab] = useState<'invitees' | 'rewards'>('invitees')
  const [rename, setRename] = useState(false)
  if (isLoading) return <div className="space-y-4"><Skeleton className="h-16" /><Skeleton className="h-72" /></div>
  if (!me?.enabled) return <Empty icon={<Gift />} title="邀请活动暂未开放" description="平台开放后，你可以在这里获取邀请链接和海报。" />
  const copyIt = (text: string, what: string) => copy(text).then(() => toast.success(`${what}已复制`), () => toast.error('复制失败，请手动选择复制'))
  const shareText = `${me.poster.nickname} 邀请你用${me.poster.siteName}把小说做成有声书${me.rules.inviteeCredits > 0 ? `，注册额外得 ${n(me.rules.inviteeCredits)} 积分` : ''}：${me.link}`
  return (
    <div>
      <PageHeader title="邀请好友" description={headline(me.rules)} />
      {me.disabled && <div className="mb-5 rounded-panel border border-[color:var(--danger,#b4372f)]/30 bg-surface px-4 py-3 text-[13px] text-muted">你的邀请码已被平台停用，好友暂时不能用它注册。如有疑问请联系客服。</div>}
      <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_360px]">
        <div className="space-y-5">
          <Panel title="你的邀请码">
            <div className="flex flex-wrap items-end justify-between gap-4">
              <div>
                <div className="font-mono text-[34px] font-semibold leading-none tracking-[0.12em]">{me.code}</div>
                <div className="mt-2 text-xs text-faint">好友注册时填写邀请码，或者直接打开你的邀请链接</div>
              </div>
              <div className="flex gap-2">
                <Button size="sm" icon={<Copy className="size-4" />} onClick={() => copyIt(me.code, '邀请码')}>复制邀请码</Button>
                <Button size="sm" icon={<Pencil className="size-4" />} onClick={() => setRename(true)} disabled={me.renamesLeft <= 0}>修改</Button>
              </div>
            </div>
            <div className="mt-5 flex flex-col gap-2 rounded-[10px] border border-line bg-sunken/60 p-3 sm:flex-row sm:items-center">
              <Link2 className="hidden size-4 shrink-0 text-faint sm:block" />
              <span className="min-w-0 flex-1 truncate font-mono text-[13px]">{me.link}</span>
              <div className="flex shrink-0 gap-2">
                <Button size="sm" variant="primary" onClick={() => copyIt(me.link, '邀请链接')}>复制链接</Button>
                <Button size="sm" onClick={() => copyIt(shareText, '邀请文案')}>复制邀请文案</Button>
              </div>
            </div>
          </Panel>
          <Panel padded={false}>
            <div className="grid grid-cols-2 gap-px overflow-hidden rounded-panel bg-line sm:grid-cols-4">
              {[
                ['链接打开', n(me.stats.visits), '同一人一分钟内重复打开只算一次'],
                ['注册好友', n(me.stats.signups), me.stats.pending > 0 ? `${me.stats.pending} 位等待首充` : undefined],
                ['已充值好友', n(me.stats.paying), undefined],
                ['累计获得积分', n(me.stats.earned), undefined],
              ].map(([label, value, hint]) => (
                <div key={label as string} className="bg-surface px-5 py-4"><Stat label={label as string} value={value} hint={hint} /></div>
              ))}
            </div>
          </Panel>
          <Panel title="活动规则">
            <ol className="list-decimal space-y-2 pl-5 text-[13.5px] leading-relaxed text-muted marker:text-faint">
              {ruleLines(me.rules).map((l) => <li key={l}>{l}</li>)}
            </ol>
          </Panel>
        </div>
        <Poster me={me} />
      </div>
      <div className="mt-8">
        <Tabs className="mb-4" value={tab} onChange={setTab} items={[{ value: 'invitees', label: '我邀请的好友' }, { value: 'rewards', label: '奖励记录' }]} />
        {tab === 'invitees' ? <Invitees /> : <Rewards />}
      </div>
      <RenameDialog open={rename} onOpenChange={setRename} me={me} onDone={(v) => qc.setQueryData(['referral'], v)} />
    </div>
  )
}

// ---------------- poster ----------------

const PAPER = '#f6f2ea', INK = '#1d1d1b', MUTED = '#6b675f', ACCENT = '#2e4c80', SIGNAL = '#d9922a', LINE = '#e2ddd2'
const SERIF = '"Noto Serif SC", "Source Han Serif SC", "Songti SC", "STSong", serif'
const SANS = '-apple-system, "PingFang SC", "Noto Sans SC", "Microsoft YaHei", sans-serif'

/** Breaks text into lines that fit width (CJK text breaks anywhere). */
function wrap(ctx: CanvasRenderingContext2D, text: string, width: number, maxLines: number): string[] {
  const lines: string[] = []
  let cur = ''
  for (const ch of Array.from(text)) {
    if (ctx.measureText(cur + ch).width > width && cur) {
      lines.push(cur)
      cur = ch.trim() ? ch : ''
      if (lines.length === maxLines) break
    } else cur += ch
  }
  if (cur && lines.length < maxLines) lines.push(cur)
  return lines
}

function drawPoster(canvas: HTMLCanvasElement, me: Me) {
  const W = 720, H = 1080
  canvas.width = W
  canvas.height = H
  const ctx = canvas.getContext('2d')!
  ctx.fillStyle = PAPER
  ctx.fillRect(0, 0, W, H)
  // a hairline frame, like a printed card
  ctx.strokeStyle = LINE
  ctx.lineWidth = 2
  ctx.strokeRect(28, 28, W - 56, H - 56)

  const x = 72
  ctx.textBaseline = 'alphabetic'
  ctx.fillStyle = INK
  ctx.font = `600 30px ${SERIF}`
  ctx.fillText(me.poster.siteName || '声书工坊', x, 118)
  if (me.poster.slogan) {
    ctx.fillStyle = MUTED
    ctx.font = `22px ${SANS}`
    ctx.fillText(me.poster.slogan, x, 156)
  }

  // the title, large, in serif
  ctx.fillStyle = INK
  ctx.font = `700 68px ${SERIF}`
  let y = 300
  for (const l of wrap(ctx, me.poster.title || '把小说做成有声书', W - 2 * x, 3)) {
    ctx.fillText(l, x, y)
    y += 88
  }
  ctx.fillStyle = MUTED
  ctx.font = `26px ${SANS}`
  y += 4
  for (const l of wrap(ctx, me.poster.text || '', W - 2 * x, 2)) {
    ctx.fillText(l, x, y)
    y += 40
  }

  // the product's motif: a line of voice turning into a waveform
  const baseY = Math.max(y + 90, 640)
  const bars = 46
  for (let i = 0; i < bars; i++) {
    const h = 10 + 74 * Math.abs(Math.sin(i * 0.42) * Math.cos(i * 0.13))
    ctx.fillStyle = i < 18 ? SIGNAL : 'rgba(29,29,27,0.16)'
    const bx = x + i * ((W - 2 * x) / bars)
    ctx.beginPath()
    ctx.roundRect(bx, baseY - h / 2, 5, h, 3)
    ctx.fill()
  }

  // the card at the bottom: QR code and who invites
  const cardY = H - 300, cardH = 220
  ctx.fillStyle = '#ffffff'
  ctx.beginPath()
  ctx.roundRect(x - 16, cardY, W - 2 * x + 32, cardH, 18)
  ctx.fill()
  ctx.strokeStyle = LINE
  ctx.stroke()
  const qr = qrcode(0, 'M')
  qr.addData(me.link)
  qr.make()
  const count = qr.getModuleCount()
  const size = 172, cell = size / count, qx = x + 8, qy = cardY + (cardH - size) / 2
  ctx.fillStyle = INK
  for (let r = 0; r < count; r++) for (let c = 0; c < count; c++) if (qr.isDark(r, c)) ctx.fillRect(qx + c * cell, qy + r * cell, Math.ceil(cell), Math.ceil(cell))
  const tx = qx + size + 36
  ctx.fillStyle = INK
  ctx.font = `600 30px ${SANS}`
  ctx.fillText(wrap(ctx, `${me.poster.nickname} 邀请你`, W - tx - x, 1)[0] ?? '', tx, cardY + 68)
  ctx.fillStyle = MUTED
  ctx.font = `22px ${SANS}`
  ctx.fillText(me.rules.inviteeCredits > 0 ? `扫码注册，额外得 ${n(me.rules.inviteeCredits)} 积分` : '扫码注册，免费体验', tx, cardY + 110)
  ctx.fillStyle = ACCENT
  ctx.font = `600 28px ui-monospace, Menlo, monospace`
  ctx.fillText(`邀请码 ${me.code}`, tx, cardY + 162)
}

function Poster({ me }: { me: Me }) {
  const ref = useRef<HTMLCanvasElement>(null)
  const [url, setUrl] = useState('')
  useEffect(() => {
    let live = true
    ;(document.fonts?.ready ?? Promise.resolve()).then(() => {
      if (!live || !ref.current) return
      drawPoster(ref.current, me)
      setUrl(ref.current.toDataURL('image/png'))
    })
    return () => { live = false }
  }, [me.code, me.link, me.poster.title, me.poster.text, me.rules.inviteeCredits])
  const download = () => {
    const a = document.createElement('a')
    a.href = url
    a.download = `邀请海报-${me.code}.png`
    a.click()
  }
  return (
    <Panel title="邀请海报" description="保存到手机后发给朋友或发到朋友圈，扫码就能注册。">
      <canvas ref={ref} className="hidden" />
      <div className="overflow-hidden rounded-[10px] border border-line bg-sunken">
        {url ? <img src={url} alt="邀请海报" className="block w-full" /> : <Skeleton className="aspect-[2/3] w-full" />}
      </div>
      <Button className="mt-4 w-full" variant="primary" icon={<Download className="size-4" />} disabled={!url} onClick={download}>下载海报</Button>
      <p className="mt-2 text-center text-xs text-faint">在手机上也可以长按图片保存</p>
    </Panel>
  )
}

// ---------------- lists ----------------

const statusText: Record<string, [string, 'neutral' | 'accent' | 'ok' | 'warn']> = {
  signed_up: ['已注册', 'neutral'],
  pending: ['等待首充', 'warn'],
  paid: ['已充值', 'ok'],
}

function Invitees() {
  const [page, setPage] = useState(1)
  const { data } = useQuery<any>({ queryKey: ['referral-invitees', page], queryFn: () => api.get(`/referral/invitees?page=${page}&pageSize=20`) })
  if (!data) return <Skeleton className="h-40" />
  if (!data.items.length) return <Empty icon={<Users />} title="还没有好友通过你的邀请注册" description="复制邀请链接或下载海报发给朋友吧。" />
  return (
    <Panel padded={false}>
      <Table>
        <thead><tr><th>好友</th><th>注册时间</th><th>状态</th><th className="text-right">你从 Ta 获得的积分</th></tr></thead>
        <tbody>
          {data.items.map((it: any) => (
            <tr key={it.id}>
              <td>{it.name}</td>
              <td className="text-muted tnum">{date(it.joinedAt)}</td>
              <td><Badge tone={statusText[it.status]?.[1]}>{statusText[it.status]?.[0] ?? it.status}</Badge></td>
              <td className="text-right tnum">{it.earned > 0 ? `+${n(it.earned)}` : '—'}</td>
            </tr>
          ))}
        </tbody>
      </Table>
      <div className="px-4 pb-3"><Pagination page={page} pageSize={20} total={data.total} onChange={setPage} /></div>
    </Panel>
  )
}

const rewardText: Record<string, string> = { signup_inviter: '邀请注册奖励', signup_invitee: '受邀注册奖励', commission: '好友充值返利' }

function Rewards() {
  const [page, setPage] = useState(1)
  const { data } = useQuery<any>({ queryKey: ['referral-rewards', page], queryFn: () => api.get(`/referral/rewards?page=${page}&pageSize=20`) })
  if (!data) return <Skeleton className="h-40" />
  if (!data.items.length) return <Empty icon={<Gift />} title="还没有奖励" description="好友注册或充值后，奖励会自动发到你的个人工作室。" />
  return (
    <Panel padded={false}>
      <Table>
        <thead><tr><th>时间</th><th>类型</th><th>好友</th><th className="text-right">积分</th></tr></thead>
        <tbody>
          {data.items.map((it: any) => (
            <tr key={it.id}>
              <td className="text-muted tnum">{date(it.createdAt)}</td>
              <td>{rewardText[it.kind] ?? it.kind}</td>
              <td>{it.friend}</td>
              <td className="text-right font-medium text-warn tnum">+{n(it.credits)}</td>
            </tr>
          ))}
        </tbody>
      </Table>
      <div className="px-4 pb-3"><Pagination page={page} pageSize={20} total={data.total} onChange={setPage} /></div>
    </Panel>
  )
}

function RenameDialog({ open, onOpenChange, me, onDone }: { open: boolean; onOpenChange: (v: boolean) => void; me: Me; onDone: (v: Me) => void }) {
  const [code, setCode] = useState(me.code)
  const [busy, setBusy] = useState(false)
  useEffect(() => { if (open) setCode(me.code) }, [open])
  const valid = useMemo(() => /^[A-Za-z0-9]{4,16}$/.test(code), [code])
  const save = async () => {
    setBusy(true)
    try {
      onDone(await api.put('/referral/code', { code }))
      toast.success('邀请码已修改')
      onOpenChange(false)
    } catch (e: any) {
      toast.error(e.message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="修改邀请码" description={`还可以修改 ${me.renamesLeft} 次。修改后，旧的邀请链接和海报就不能用了。`} width={420}
      footer={<><Button onClick={() => onOpenChange(false)}>取消</Button><Button variant="primary" loading={busy} disabled={!valid || code.toUpperCase() === me.code} onClick={save}>保存</Button></>}>
      <Field label="新的邀请码" hint="4~16 个英文字母或数字，不区分大小写">
        <Input value={code} onChange={(e) => setCode(e.target.value.replace(/[^A-Za-z0-9]/g, '').slice(0, 16))} className="font-mono uppercase" autoFocus />
      </Field>
    </Dialog>
  )
}
