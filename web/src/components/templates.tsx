import { useQuery } from '@tanstack/react-query'
import { Pause, Play } from '@/components/icons'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, qs } from '@/lib/api'
import { usePlayer } from '@/lib/player'
import { Button, Tip } from '@/components/ui'

/** One row of GET /studios/{sid}/templates. */
export type TemplateView = {
  template: { id: string; scope: string; kind: 'intro' | 'outro'; name: string; mode: 'compose' | 'audio' | ''; text: string; voiceId: string; musicAssetId: string; audioAssetId: string; musicVolume: number; audioGain: number; leadMs: number; tailMs: number; disclaimer: boolean }
  musicName?: string
  voiceName?: string
  audio?: { id: string; name: string; url: string; durationMs: number }
  audioMissing?: boolean
  books?: number
  studioDefault?: boolean
  placeholders?: string[]
}

export const kindLabel = (k: string) => (k === 'outro' ? '片尾' : '片头')
export const isFixed = (t: { mode?: string }) => t.mode === 'audio'

export function useTemplates(studioId?: string) {
  return useQuery<TemplateView[]>({ queryKey: ['templates', studioId], queryFn: () => api.get(`/studios/${studioId}/templates`), enabled: !!studioId })
}

/** Select options for a template kind; fixed-audio templates are labelled as such. */
export function templateOptions(list: TemplateView[] | undefined, kind: 'intro' | 'outro', none = '不使用'): [string, string][] {
  return [['', none], ...(list ?? []).filter((v) => v.template.kind === kind).map((v) => [v.template.id, v.template.name + (isFixed(v.template) ? '（固定音频）' : '')] as [string, string])]
}

/** Plays a saved template (rendered with the book's title when bookId is given). */
export function TemplatePreviewButton({ studioId, id, bookId, size = 'md' }: { studioId: string; id: string; bookId?: string; size?: 'sm' | 'md' }) {
  const [busy, setBusy] = useState(false)
  const playing = usePlayer((s) => s.id === 'tpl:' + id && s.playing)
  if (!id) return null
  const dim = size === 'sm' ? '!size-7' : '!size-8'
  return (
    <Tip label={playing ? '暂停' : '试听'}>
      <Button size="sm" variant="secondary" className={`${dim} shrink-0 !rounded-full !px-0`} loading={busy} onClick={async () => {
        const p = usePlayer.getState()
        if (p.id === 'tpl:' + id && p.playing && p.url) return p.toggle(p.id, p.url)
        setBusy(true)
        try {
          const r = await api.post(`/studios/${studioId}/templates/${id}/preview${qs({ bookId })}`)
          p.play('tpl:' + id, r.url, '片头片尾试听')
        } finally {
          setBusy(false)
        }
      }}>{!busy && (playing ? <Pause className="size-3.5" fill="currentColor" /> : <Play className="size-3.5 translate-x-px" fill="currentColor" />)}</Button>
    </Tip>
  )
}

export function ManageTemplatesLink({ className }: { className?: string }) {
  return <Link to="/assets?tab=templates" className={className ?? 'text-[12.5px] text-accent hover:underline'}>管理片头片尾</Link>
}
