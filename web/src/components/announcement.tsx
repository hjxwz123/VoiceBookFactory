import DOMPurify from 'dompurify'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '@/lib/api'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { ExternalLink, Megaphone } from '@/components/icons'
import { Badge, Button, cn, Dialog } from '@/components/ui'

export type Announcement = {
  id: string; title: string; body: string; kind: 'banner' | 'modal'; level: '' | 'warning' | 'danger'; format: '' | 'text' | 'markdown' | 'html'
  link?: string; linkText?: string; width?: number
}

// links in announcements open in a new tab and never get the opener
DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node.tagName === 'A' && node.getAttribute('href')) {
    node.setAttribute('target', '_blank')
    node.setAttribute('rel', 'noopener noreferrer')
  }
})

/** HTML written by an administrator, without scripts, frames, forms or event handlers. */
export function safeHTML(html: string) {
  return DOMPurify.sanitize(html, { USE_PROFILES: { html: true }, FORBID_TAGS: ['style', 'form', 'input', 'button', 'textarea', 'select', 'iframe', 'object', 'embed'], FORBID_ATTR: ['srcset'] })
}

const mdLink = { a: (p: any) => <a {...p} target="_blank" rel="noopener noreferrer" /> }

export function AnnouncementBody({ a, className, inline }: { a: Pick<Announcement, 'body' | 'format'>; className?: string; inline?: boolean }) {
  if (!a.body?.trim()) return null
  if (a.format === 'html') return <div className={cn('prose-help prose-ann', inline && 'prose-ann-inline', className)} dangerouslySetInnerHTML={{ __html: safeHTML(a.body) }} />
  if (a.format === 'markdown') return <div className={cn('prose-help prose-ann', inline && 'prose-ann-inline', className)}><ReactMarkdown remarkPlugins={[remarkGfm]} components={mdLink}>{a.body}</ReactMarkdown></div>
  return <div className={cn('whitespace-pre-wrap', className)}>{a.body}</div>
}

const levelBadge: Record<string, [string, any]> = { warning: ['重要', 'warn'], danger: ['紧急', 'danger'] }

/** A real popup: centred, over the page, closed only by the reader. */
export function AnnouncementDialog({ a, open, onClose, preview }: { a: Announcement | null; open: boolean; onClose: () => void; preview?: boolean }) {
  const nav = useNavigate()
  if (!a) return null
  const lv = levelBadge[a.level]
  const external = a.link && /^https?:\/\//.test(a.link)
  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()} width={a.width || 560}
      title={<span className="flex items-center gap-2"><Megaphone className="size-[18px] shrink-0 text-accent" /><span className="min-w-0">{a.title}</span>{lv && <Badge tone={lv[1]}>{lv[0]}</Badge>}</span>}
      footer={<>
        <Button onClick={onClose}>我知道了</Button>
        {a.link && <Button variant="primary" icon={external ? <ExternalLink className="size-4" /> : undefined} onClick={() => { onClose(); if (preview) return; external ? window.open(a.link, '_blank', 'noopener') : nav(a.link!) }}>{a.linkText || '查看详情'}</Button>}
      </>}>
      <AnnouncementBody a={a} className="text-[14px] leading-relaxed" />
    </Dialog>
  )
}

/** Shows popup announcements one after another, on whichever page the user is. Closing one records it on
 * the server, so it does not come back on another device. */
export function AnnouncementHost() {
  const qc = useQueryClient()
  const { data } = useQuery<Announcement[]>({ queryKey: ['announcements'], queryFn: () => api.get('/announcements'), staleTime: 5 * 60_000 })
  const [closed, setClosed] = useState<string[]>([])
  const cur = (data ?? []).find((a) => a.kind === 'modal' && !closed.includes(a.id)) ?? null
  const close = () => {
    if (!cur) return
    setClosed((c) => [...c, cur.id])
    api.post(`/announcements/${cur.id}/dismiss`).then(() => qc.invalidateQueries({ queryKey: ['announcements'] })).catch(() => {})
  }
  return <AnnouncementDialog key={cur?.id} a={cur} open={!!cur} onClose={close} />
}

/** Dismisses an announcement for this user everywhere. */
export function dismissAnnouncement(id: string) {
  return api.post(`/announcements/${id}/dismiss`).catch(() => {})
}
