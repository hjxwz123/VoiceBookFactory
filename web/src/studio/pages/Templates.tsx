import { AlertTriangle, AudioLines, Check, Copy, FolderOpen, Mic2, Music, Pencil, Plus, Sparkles, Trash2, Upload, Wand2, X } from '@/components/icons'
import { useState } from 'react'
import { toast } from 'sonner'
import { CopyrightNotice } from '@/components/legal'
import { api } from '@/lib/api'
import { duration } from '@/lib/format'
import { usePlayer } from '@/lib/player'
import { useSession } from '@/lib/session'
import { AssetPicker } from '@/components/asset'
import { PlayButton, UploadZone } from '@/components/audio'
import { VoicePicker } from '@/components/voice'
import { isFixed, kindLabel, TemplatePreviewButton, type TemplateView, useTemplates } from '@/components/templates'
import { Badge, Button, Chip, cn, Collapse, confirm, Empty, Fade, Field, IconButton, Input, Segmented, Sheet, Skeleton, Slider, Switch, Textarea } from '@/components/ui'
import { invalidate } from '../hooks'

const PLACEHOLDERS = ['书名', '章节名', '章节号', '作者', '主播']
const MAX_JINGLE_MS = 180_000
const placeholdersIn = (s: string) => [...s.matchAll(/\{([^{}\s]{1,8})\}/g)].map((m) => m[1])

type Audio = NonNullable<TemplateView['audio']>
type Draft = TemplateView['template'] & { musicName?: string; voiceName?: string; audio?: Audio | null; books?: number }

const blank = (kind: 'intro' | 'outro'): Draft => ({
  id: '', scope: 'studio', kind, name: '', mode: 'compose', text: kind === 'intro' ? '{书名}，{章节名}。' : '本集到这里就结束了，感谢收听，我们下集再见。',
  voiceId: '', musicAssetId: '', audioAssetId: '', musicVolume: -10, audioGain: 0, leadMs: 2000, tailMs: 1500, disclaimer: false, audio: null,
})

/** Body accepted by the template endpoints (helper fields stripped). */
const body = (f: Draft) => ({ kind: f.kind, name: f.name, mode: f.mode || 'compose', text: f.text, voiceId: f.voiceId, musicAssetId: f.musicAssetId, audioAssetId: f.audio?.id ?? '', musicVolume: f.musicVolume, audioGain: f.audioGain, leadMs: f.leadMs, tailMs: f.tailMs, disclaimer: f.disclaimer })

export default function Templates() {
  const sid = useSession((s) => s.studioId)
  const { data, isLoading } = useTemplates(sid)
  const [edit, setEdit] = useState<Draft | null>(null)
  const [kind, setKind] = useState<'all' | 'intro' | 'outro'>('all')
  const list = (data ?? []).filter((v) => kind === 'all' || v.template.kind === kind)
  const open = (v: TemplateView, copy = false) =>
    setEdit({ ...v.template, mode: v.template.mode || 'compose', musicName: v.musicName, voiceName: v.voiceName, audio: v.audio ?? null, books: v.books, ...(copy ? { id: '', scope: 'studio', name: `${v.template.name} 副本`.slice(0, 30), books: 0 } : {}) })
  return (
    <div>
      <HowItWorks />
      <div className="mb-4 mt-6 flex flex-wrap items-center gap-3">
        <Segmented size="sm" value={kind} onChange={setKind} items={[{ value: 'all', label: '全部' }, { value: 'intro', label: '片头' }, { value: 'outro', label: '片尾' }]} />
        <div className="ml-auto flex gap-2">
          <Button size="sm" icon={<Plus className="size-4" />} onClick={() => setEdit(blank('outro'))}>新建片尾</Button>
          <Button size="sm" variant="primary" icon={<Plus className="size-4" />} onClick={() => setEdit(blank('intro'))}>新建片头</Button>
        </div>
      </div>
      {isLoading ? (
        <div className="grid gap-3 sm:grid-cols-2">{Array.from({ length: 4 }, (_, i) => <Skeleton key={i} className="h-28" />)}</div>
      ) : !list.length ? (
        <Empty icon={<AudioLines />} title="还没有模板" description="新建一个片头或片尾，在图书设置里选用后，每一集都会自动加上。" />
      ) : (
        <div className="grid gap-3 sm:grid-cols-2">
          {list.map((v) => <TemplateCard key={v.template.id} v={v} sid={sid!} onEdit={() => open(v)} onCopy={() => open(v, true)} />)}
        </div>
      )}
      <TemplateEditor draft={edit} onClose={() => setEdit(null)} />
    </div>
  )
}

function HowItWorks() {
  const steps = [
    { icon: <Pencil className="size-4" />, title: '做一个片头或片尾', text: '用配乐加 AI 播报，或者直接用一段固定音频。' },
    { icon: <Check className="size-4" />, title: '在图书设置里选用', text: '也可以在工作室设置里设为新书的默认模板。' },
    { icon: <AudioLines className="size-4" />, title: '每一集自动加上', text: '合成成品时自动加在开头和结尾，个别章节可以在混音里关掉。' },
  ]
  return (
    <div className="grid gap-px overflow-hidden rounded-[12px] border border-line bg-line sm:grid-cols-3">
      {steps.map((s, i) => (
        <div key={i} className="flex gap-3 bg-surface px-4 py-3.5">
          <span className="mt-0.5 grid size-7 shrink-0 place-items-center rounded-full bg-accent/10 text-accent">{s.icon}</span>
          <div>
            <div className="text-[13px] font-medium">{i + 1}. {s.title}</div>
            <div className="mt-0.5 text-xs leading-relaxed text-muted">{s.text}</div>
          </div>
        </div>
      ))}
    </div>
  )
}

function TemplateCard({ v, sid, onEdit, onCopy }: { v: TemplateView; sid: string; onEdit: () => void; onCopy: () => void }) {
  const t = v.template
  const fixed = isFixed(t)
  const official = t.scope === 'system'
  const usage = [v.books ? `${v.books} 本书在用` : '', v.studioDefault ? '工作室默认' : ''].filter(Boolean).join(' · ')
  return (
    <div className="group flex items-start gap-3 rounded-[12px] border border-line bg-surface p-4 transition-colors hover:border-line-strong">
      <TemplatePreviewButton studioId={sid} id={t.id} />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="truncate text-[14px] font-semibold">{t.name}</span>
          <Badge>{kindLabel(t.kind)}</Badge>
          <Badge tone={fixed ? 'signal' : 'neutral'}>{fixed ? '固定音频' : '配乐 + 播报'}</Badge>
          {official && <Badge tone="accent">官方</Badge>}
        </div>
        {fixed ? (
          v.audioMissing ? (
            <p className="mt-1.5 flex items-center gap-1 text-[13px] text-danger"><AlertTriangle className="size-3.5" />音频文件丢失，请重新上传</p>
          ) : (
            <p className="mt-1.5 flex items-center gap-1.5 truncate text-[13px] text-muted"><Music className="size-3.5 shrink-0" /><span className="truncate">{v.audio?.name}</span><span className="tnum text-faint">{duration(v.audio?.durationMs)}</span></p>
          )
        ) : (
          <p className="mt-1.5 line-clamp-2 text-[13px] text-muted">{t.text ? `「${t.text}」` : '只有配乐，没有播报'}</p>
        )}
        <p className="mt-1 truncate text-xs text-faint">
          {fixed ? '每一集完全一样' : [v.musicName && `音乐：${v.musicName}`, `声音：${v.voiceName ?? '图书旁白'}`, t.disclaimer && '含 AI 声明'].filter(Boolean).join(' · ')}
          {usage && <span className="text-muted"> · {usage}</span>}
        </p>
      </div>
      {official ? (
        <Button size="xs" variant="ghost" icon={<Copy className="size-3.5" />} onClick={onCopy}>复制修改</Button>
      ) : (
        <Button size="xs" variant="ghost" onClick={onEdit}>编辑</Button>
      )}
    </div>
  )
}

function TemplateEditor({ draft, onClose }: { draft: Draft | null; onClose: () => void }) {
  const sid = useSession((s) => s.studioId)
  const [f, setF] = useState<Draft | null>(null)
  const [src, setSrc] = useState<Draft | null>(null)
  const [busy, setBusy] = useState(false)
  if (draft !== src) {
    setSrc(draft)
    if (draft) setF({ ...draft })
  }
  const set = (patch: Partial<Draft>) => setF((x) => (x ? { ...x, ...patch } : x))
  const close = () => { if (!busy) onClose() }
  const fixed = f?.mode === 'audio'
  const save = async () => {
    if (!f) return
    setBusy(true)
    try {
      if (f.id) await api.put(`/studios/${sid}/templates/${f.id}`, body(f))
      else await api.post(`/studios/${sid}/templates`, body(f))
      invalidate(['templates'])
      const p = usePlayer.getState()
      if (p.id === 'tpl:' + f.id && p.playing && p.url) p.toggle(p.id, p.url)
      toast.success('已保存', { description: f.books ? `使用它的 ${f.books} 本书需要重新合成，成品才会更新。` : '在图书设置里选用后，每一集都会自动加上。' })
      onClose()
    } finally {
      setBusy(false)
    }
  }
  const remove = async () => {
    if (!f?.id) return
    const ok = await confirm({ title: `删除「${f.name}」？`, description: f.books ? `有 ${f.books} 本书正在使用，删除后这些书合成时不再加这个${kindLabel(f.kind)}。` : '删除后无法恢复。', danger: true, confirmText: '删除' })
    if (!ok) return
    await api.del(`/studios/${sid}/templates/${f.id}`)
    invalidate(['templates'])
    onClose()
  }
  const canSave = !!f && (fixed ? !!f.audio : !!(f.text.trim() || f.musicAssetId))
  return (
    <Sheet open={!!draft} onOpenChange={(o) => { if (!o) close() }} title={f?.id ? '编辑模板' : `新建${kindLabel(f?.kind ?? 'intro')}`} description="片头片尾会自动加在每一集的开头和结尾" width={540} footer={f && <>
      {f.id && <IconButton label="删除" className="mr-auto text-danger" icon={<Trash2 className="size-4" />} onClick={remove} />}
      <Button onClick={close}>取消</Button>
      <Button variant="primary" loading={busy} disabled={!canSave} onClick={save}>保存</Button>
    </>}>
      {f && (
        <div className="space-y-6">
          <div className="grid grid-cols-[auto_1fr] items-end gap-4">
            <Field label="类型"><Segmented size="sm" value={f.kind} onChange={(v) => set({ kind: v })} items={[{ value: 'intro', label: '片头' }, { value: 'outro', label: '片尾' }]} /></Field>
            <Field label="名称"><Input value={f.name} maxLength={30} placeholder={f.kind === 'intro' ? '例如：《长安》片头' : '例如：通用片尾'} onChange={(e) => set({ name: e.target.value })} /></Field>
          </div>
          <Field label="制作方式">
            <div className="grid grid-cols-2 gap-2.5">
              <ModeCard active={!fixed} icon={<Mic2 className="size-4" />} title="配乐 + AI 播报" text="每一集自动念出书名、章节名" onClick={() => set({ mode: 'compose' })} />
              <ModeCard active={fixed} icon={<AudioLines className="size-4" />} title="固定音频" text="上传或生成一段，每集完全一样" onClick={() => set({ mode: 'audio' })} />
            </div>
          </Field>
          <Fade k={fixed ? 'audio' : 'compose'}>
            {fixed ? <FixedAudio f={f} set={set} /> : (
              <div className="space-y-5">
                <Recipe f={f} set={set} placeholders />
                <DraftPreview f={f} />
              </div>
            )}
          </Fade>
        </div>
      )}
    </Sheet>
  )
}

function ModeCard({ active, icon, title, text, onClick }: { active: boolean; icon: React.ReactNode; title: string; text: string; onClick: () => void }) {
  return (
    <button type="button" onClick={onClick} className={cn('relative rounded-[10px] border p-3 text-left transition-all duration-200', active ? 'border-accent bg-accent/[0.06] ring-3 ring-accent/15' : 'border-line hover:border-line-strong hover:bg-sunken/40')}>
      <div className={cn('flex items-center gap-2 text-[13.5px] font-medium transition-colors', active ? 'text-accent' : 'text-ink')}>{icon}{title}</div>
      <div className="mt-1 text-xs text-muted">{text}</div>
      <span className={cn('absolute right-2.5 top-2.5 grid size-4 place-items-center rounded-full bg-accent text-white transition-all duration-200', active ? 'scale-100 opacity-100' : 'scale-50 opacity-0')}><Check className="size-3" strokeWidth={3} /></span>
    </button>
  )
}

/** Announcement + music bed settings: the template itself (compose) or the generator of a fixed audio. */
function Recipe({ f, set, placeholders }: { f: Draft; set: (p: Partial<Draft>) => void; placeholders: boolean }) {
  const [music, setMusic] = useState(false)
  const [voice, setVoice] = useState(false)
  const used = placeholdersIn(f.text)
  return (
    <div className="space-y-5">
      <Field label="播报文字" hint={placeholders ? '点下面的标签插入，合成时会换成每本书、每一章的实际内容' : undefined} error={!placeholders && used.length ? `固定音频每集都一样，不能用 {${used.join('}、{')}}，请直接写成文字，例如“欢迎收听《长安》”` : undefined}>
        <Textarea rows={2} value={f.text} maxLength={300} placeholder={placeholders ? '{书名}，{章节名}。' : '欢迎收听《长安》，本节目由声书工坊出品。'} onChange={(e) => set({ text: e.target.value })} />
      </Field>
      {placeholders && (
        <div className="-mt-3 flex flex-wrap gap-1.5">
          {PLACEHOLDERS.map((p) => <Chip key={p} active={used.includes(p)} onClick={() => set({ text: f.text + `{${p}}` })}>{`{${p}}`}</Chip>)}
        </div>
      )}
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="播报声音">
          <PickButton icon={<Mic2 className="size-3.5" />} label={f.voiceName || (f.voiceId ? '已选择' : placeholders ? '跟随图书旁白' : '平台推荐音色')} set={!!f.voiceId} onClick={() => setVoice(true)} onClear={() => set({ voiceId: '', voiceName: '' })} />
        </Field>
        <Field label="背景音乐">
          <PickButton icon={<Music className="size-3.5" />} label={f.musicName || (f.musicAssetId ? '已选择' : '不用音乐')} set={!!f.musicAssetId} onClick={() => setMusic(true)} onClear={() => set({ musicAssetId: '', musicName: '' })} />
        </Field>
      </div>
      <Collapse open={!!f.musicAssetId}>
        <div className="space-y-4 rounded-[10px] bg-sunken/50 p-4">
          <Field label={`音乐音量 ${f.musicVolume} dB`}><Slider value={f.musicVolume} min={-30} max={0} step={1} onChange={(v) => set({ musicVolume: v })} /></Field>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={`人声前的音乐 ${(f.leadMs / 1000).toFixed(1)} 秒`}><Slider value={f.leadMs} min={0} max={10000} step={250} onChange={(v) => set({ leadMs: v })} /></Field>
            <Field label={`人声后的音乐 ${(f.tailMs / 1000).toFixed(1)} 秒`}><Slider value={f.tailMs} min={0} max={10000} step={250} onChange={(v) => set({ tailMs: v })} /></Field>
          </div>
        </div>
      </Collapse>
      <Switch checked={f.disclaimer} onChange={(v) => set({ disclaimer: v })} label="在播报后追加“AI 生成内容”声明" />
      <AssetPicker open={music} onOpenChange={setMusic} kinds={['bgm']} title="选择背景音乐" onPick={(a) => { set({ musicAssetId: a.id, musicName: a.name }); setMusic(false) }} />
      <VoicePicker open={voice} onOpenChange={setVoice} current={f.voiceId} onPick={(v) => { set({ voiceId: v.id, voiceName: v.name }); setVoice(false) }} />
    </div>
  )
}

function PickButton({ icon, label, set, onClick, onClear }: { icon: React.ReactNode; label: string; set: boolean; onClick: () => void; onClear: () => void }) {
  return (
    <div className="flex items-center gap-1">
      <Button size="sm" className="min-w-0 flex-1 justify-start" icon={icon} onClick={onClick}><span className={cn('truncate', !set && 'text-muted')}>{label}</span></Button>
      {set && <IconButton label="清除" size="sm" icon={<X className="size-3.5" />} onClick={onClear} />}
    </div>
  )
}

function DraftPreview({ f }: { f: Draft }) {
  const sid = useSession((s) => s.studioId)
  const [busy, setBusy] = useState(false)
  const playing = usePlayer((s) => s.id === 'tpl:draft' && s.playing)
  return (
    <div className="flex items-center gap-3 rounded-[10px] border border-dashed border-line px-4 py-3">
      <Button size="sm" loading={busy} disabled={!f.text.trim() && !f.musicAssetId} icon={<AudioLines className="size-4" />} onClick={async () => {
        setBusy(true)
        try {
          const r = await api.post(`/studios/${sid}/templates/preview`, body(f))
          usePlayer.getState().play('tpl:draft', r.url, '模板试听')
        } finally {
          setBusy(false)
        }
      }}>{playing ? '正在播放…' : '试听效果'}</Button>
      <span className="text-xs text-muted">用“示例作品 · 第一章”试听，还没保存也可以听</span>
    </div>
  )
}

function FixedAudio({ f, set }: { f: Draft; set: (p: Partial<Draft>) => void }) {
  const sid = useSession((s) => s.studioId)
  const [way, setWay] = useState<'' | 'upload' | 'library' | 'ai'>(f.audio ? '' : 'upload')
  const [picker, setPicker] = useState(false)
  const [busy, setBusy] = useState(false)
  const use = (a: Audio) => {
    if (a.durationMs > MAX_JINGLE_MS) return void toast.error('这段音频超过 3 分钟', { description: '片头片尾建议控制在 5~30 秒。' })
    set({ audio: a, audioAssetId: a.id })
    setWay('')
  }
  const upload = async (files: File[]) => {
    const file = files[0]
    if (!file) return
    setBusy(true)
    try {
      const a = await api.upload(`/studios/${sid}/assets`, file, { kind: 'jingle', license: 'commercial', name: file.name.replace(/\.[^.]+$/, '') })
      use(a)
    } finally {
      setBusy(false)
    }
  }
  const generate = async () => {
    setBusy(true)
    try {
      const a = await api.post(`/studios/${sid}/templates/generate`, body({ ...f, mode: 'compose' }))
      use(a)
      toast.success('已生成', { description: '先试听一下，满意就保存；不满意可以改文字再生成一次。' })
    } finally {
      setBusy(false)
    }
  }
  const ways = [
    { key: 'upload' as const, icon: <Upload className="size-4" />, label: '上传文件' },
    { key: 'library' as const, icon: <FolderOpen className="size-4" />, label: '从资产库选' },
    { key: 'ai' as const, icon: <Sparkles className="size-4" />, label: 'AI 生成一段' },
  ]
  return (
    <div className="space-y-5">
      <Fade k={f.audio?.id ?? 'none'}>
        {f.audio ? (
          <div className="flex items-center gap-3 rounded-[12px] border border-line bg-sunken/40 p-3.5">
            <PlayButton id={'tplaudio:' + f.audio.id} url={f.audio.url} title={f.audio.name} size="md" />
            <div className="min-w-0 flex-1">
              <div className="truncate text-[13.5px] font-medium">{f.audio.name}</div>
              <div className="text-xs text-muted"><span className="tnum">{duration(f.audio.durationMs)}</span> · 每一集都会原样播放这一段</div>
            </div>
            <Badge tone="ok" dot>已就绪</Badge>
          </div>
        ) : (
          <div className="rounded-[12px] border border-dashed border-line px-4 py-5 text-center text-[13px] text-muted">还没有音频，用下面任意一种方式添加</div>
        )}
      </Fade>
      <div>
        <div className="mb-2 text-[13px] font-medium">{f.audio ? '换一段' : '添加音频'}</div>
        <div className="grid grid-cols-3 gap-2">
          {ways.map((w) => (
            <button key={w.key} type="button" onClick={() => {
              if (w.key === 'library') return setPicker(true)
              // the compose default names the book and chapter, which a fixed audio cannot do
              if (w.key === 'ai' && f.text === blank(f.kind).text && placeholdersIn(f.text).length) set({ text: '' })
              setWay(way === w.key ? '' : w.key)
            }}
              className={cn('flex items-center justify-center gap-1.5 rounded-[9px] border px-2 py-2.5 text-[13px] transition-all duration-200', way === w.key ? 'border-accent bg-accent/[0.06] text-accent' : 'border-line text-ink hover:border-line-strong hover:bg-sunken/40')}>
              {w.icon}{w.label}
            </button>
          ))}
        </div>
        <Collapse open={way === 'upload'}>
          <div className="pt-3">
            <UploadZone accept="audio/*" onFiles={upload} hint="WAV、MP3、M4A、FLAC，最长 3 分钟，建议 5~30 秒">
              {busy ? <p className="text-[13px] text-muted">正在上传…</p> : undefined}
            </UploadZone>
            <CopyrightNotice what="上传的音频" className="mt-2" />
          </div>
        </Collapse>
        <Collapse open={way === 'ai'}>
          <div className="mt-3 space-y-5 rounded-[12px] border border-line p-4">
            <Recipe f={f} set={set} placeholders={false} />
            <div className="flex items-center gap-3">
              <Button variant="primary" icon={<Wand2 className="size-4" />} loading={busy} disabled={(!f.text.trim() && !f.musicAssetId) || placeholdersIn(f.text).length > 0} onClick={generate}>{f.audio ? '重新生成' : '生成'}</Button>
              <span className="text-xs text-muted">只合成这一次，按播报字数扣积分，之后每集直接复用</span>
            </div>
          </div>
        </Collapse>
      </div>
      <Field label={`音量微调 ${f.audioGain > 0 ? '+' : ''}${f.audioGain} dB`} hint="已自动和正文音量对齐，一般不用调">
        <Slider value={f.audioGain} min={-12} max={6} step={1} onChange={(v) => set({ audioGain: v })} />
      </Field>
      <AssetPicker open={picker} onOpenChange={setPicker} kinds={['jingle', 'bgm', 'sfx']} title="选择一段音频" onPick={(a) => { setPicker(false); use(a) }} />
    </div>
  )
}
