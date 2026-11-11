import { useQuery } from '@tanstack/react-query'
import { ArrowRight, Check, Image as ImageIcon, ImagePlus, Sparkles, Wand2 } from '@/components/icons'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { duration, fromNow, num } from '@/lib/format'
import { useRunningJobs } from '@/lib/live'
import { FLOW, FlowSteps } from '@/components/flow'
import { Avatar, Badge, Button, cn, Dialog, Field, Panel, Progress, Segmented, Stat, Switch, Textarea } from '@/components/ui'
import { CopyrightNotice } from '@/components/legal'
import { can, invalidate } from '../../hooks'

const nextAction: Record<string, { text: string; desc: string; to: string }> = {
  import: { text: '导入书稿', desc: '这本书还没有正文。导入整本书稿会自动识别章节，也可以一章一章手动添加。', to: 'chapters?import=1' },
  characters: { text: '分析角色', desc: 'AI 会读一遍书稿，找出会说话的角色并写好人物小传。', to: 'characters' },
  casting: { text: '去选角', desc: '为每个角色挑一个声音。可以一键让 AI 推荐，再逐个试听调整。', to: 'characters' },
  script: { text: '生成画本', desc: '把章节拆成一句句台词，标好说话人和情绪。', to: 'chapters' },
  voice: { text: '生成配音', desc: '按角色声音给每一句配音。配音前建议先在「读音」里定好人名、多音字的读音，能少返工；真人配音演员可以在「我的台词」里录制。', to: 'chapters' },
  review: { text: '开始审听', desc: '对照原文从头听一遍：在制作台点「连续播放」，听到问题按 M 标记，再统一重新生成；也可以先用智能审听自动找漏读错读。', to: 'review' },
  render: { text: '合成章节', desc: '把句子拼接成完整章节，叠加背景音乐并统一响度。', to: 'chapters' },
  export: { text: '导出成品', desc: '导出 MP3/M4B，或者发布到已绑定的平台。', to: 'deliver' },
}

export default function Overview({ book }: { book: any }) {
  const nav = useNavigate()
  const p = book.progress ?? {}
  const act = nextAction[p.next]
  const activity = useQuery({ queryKey: ['activity', book.id], queryFn: () => api.get(`/books/${book.id}/activity?pageSize=12`) })
  const pipeline = useQuery({ queryKey: ['pipeline', book.id], queryFn: () => api.get(`/books/${book.id}/pipeline`) })
  const running = useRunningJobs((j) => j.bookId === book.id)
  const [intro, setIntro] = useState(false)
  const [desc, setDesc] = useState(book.description ?? '')
  const pl = pipeline.data
  const editable = can(book, 'edit_info')

  const genIntro = async () => {
    setIntro(true)
    try {
      const r = await api.post(`/books/${book.id}/intro`)
      setDesc(r.description)
      await api.patch(`/books/${book.id}`, { description: r.description, tags: r.tags, genre: book.genre || r.genre })
      invalidate(['book', book.id])
      toast.success('简介已生成')
    } finally {
      setIntro(false)
    }
  }

  return (
    <div className="space-y-6">
      {pl && (pl.status === 'running' || pl.status === 'queued' || pl.status === 'paused') && (
        <div className="flex items-center gap-4 rounded-panel border border-signal/40 bg-signal-soft px-5 py-4">
          <Wand2 className="size-5 text-warn" />
          <div className="min-w-0 flex-1">
            <div className="text-[13.5px] font-medium">一键成书进行中 · {pl.stages?.find((s: any) => s.key === pl.stage)?.name}</div>
            <Progress value={pl.progress} tone="signal" className="mt-2" height={4} />
          </div>
          <Button size="sm" onClick={() => nav(`/autobook?run=${pl.id}`)}>查看进度</Button>
        </div>
      )}

      <FlowSteps bookId={book.id} steps={p.steps} next={p.next} />

      {act && (
        <div className="flex flex-wrap items-center gap-4 rounded-panel border border-line bg-surface px-5 py-4">
          <div className="grid size-10 place-items-center rounded-full border border-line text-accent">
            <ArrowRight className="size-5" />
          </div>
          <div className="min-w-0 flex-1">
            <div className="text-[14px] font-semibold">下一步：{FLOW.find((f) => f.key === p.next)?.label}</div>
            <div className="mt-0.5 text-[13px] text-muted">{act.desc}</div>
          </div>
          <Button variant="primary" onClick={() => nav(`/books/${book.id}/${act.to}`)}>{act.text}</Button>
        </div>
      )}

      <div className="grid gap-6 lg:grid-cols-[1fr_340px]">
        <div className="space-y-6">
          <Panel>
            <div className="grid grid-cols-2 gap-6 sm:grid-cols-4">
              <Stat label="章节" value={`${p.scripted ?? 0}/${p.included ?? 0}`} hint="已生成画本" />
              <Stat label="配音进度" value={`${p.segments ? Math.round((p.readySegments / p.segments) * 100) : 0}%`} hint={`${num(p.readySegments)}/${num(p.segments)} 句`} />
              <Stat label="成品时长" value={duration(p.durationMs)} hint={p.estimatedMs ? `预计全书 ${duration(p.estimatedMs)}` : undefined} />
              <Stat label="已用积分" value={num(p.credits)} />
            </div>
            {running.length > 0 && (
              <div className="mt-5 space-y-3 border-t border-line pt-4">
                {running.map((j) => (
                  <div key={j.id}>
                    <div className="flex justify-between text-[13px]"><span>{j.title}</span><span className="text-faint tnum">{Math.round(j.progress * 100)}%</span></div>
                    <Progress value={j.progress} tone="signal" className="mt-1.5" height={4} />
                  </div>
                ))}
              </div>
            )}
          </Panel>
          <Panel
            title="简介"
            actions={editable && <Button size="xs" icon={<Sparkles className="size-3.5" />} loading={intro} onClick={genIntro}>AI 撰写</Button>}
          >
            {editable ? (
              <Textarea rows={4} value={desc} onChange={(e) => setDesc(e.target.value)} onBlur={() => desc !== book.description && api.patch(`/books/${book.id}`, { description: desc }).then(() => invalidate(['book', book.id]))} placeholder="写几句话介绍这本书，发布到平台时会用到。" />
            ) : (
              <p className="text-[13.5px] leading-relaxed text-muted">{book.description || '暂无简介'}</p>
            )}
            {book.tags?.length > 0 && <div className="mt-3 flex flex-wrap gap-1.5">{book.tags.map((t: string) => <Badge key={t}>{t}</Badge>)}</div>}
          </Panel>
        </div>
        <div className="space-y-6">
          {editable && <CoverPanel book={book} />}
          <Panel title="最近动态" padded={false}>
            <ul className="max-h-[420px] overflow-y-auto">
              {(activity.data?.items ?? []).map((a: any) => (
                <li key={a.id} className="flex gap-3 border-t border-line px-5 py-3 first:border-t-0">
                  <Avatar name={a.actor} src={a.avatarUrl} size={24} />
                  <div className="min-w-0 text-[13px]">
                    <span className="font-medium">{a.actor || '系统'}</span> <span className="text-muted">{a.summary}</span>
                    <div className="mt-0.5 text-[11.5px] text-faint">{fromNow(a.createdAt)}</div>
                  </div>
                </li>
              ))}
              {!activity.data?.items?.length && <li className="px-5 py-8 text-center text-[13px] text-faint">暂无动态</li>}
            </ul>
          </Panel>
        </div>
      </div>
    </div>
  )
}

type CoverOptions = { items: { id: string; url: string; model: string; current: boolean }[]; engine: { available: boolean; lettering: boolean; name: string }; price: number }

/** Cover: upload one, or have the image model draw it; every AI cover stays here as a candidate. */
function CoverPanel({ book }: { book: any }) {
  const q = useQuery({ queryKey: ['covers', book.id], queryFn: () => api.get<CoverOptions>(`/books/${book.id}/covers`) })
  const running = useRunningJobs((j) => j.bookId === book.id && j.type === 'image_generate')
  const [open, setOpen] = useState(false)
  const items = q.data?.items ?? []
  const pick = async (id: string) => {
    await api.patch(`/books/${book.id}`, { coverAssetId: id })
    invalidate(['book', book.id], ['books'], ['covers', book.id])
    toast.success('已设为封面')
  }
  return (
    <Panel title="封面">
      <div className="flex gap-4">
        <div className="size-[88px] shrink-0 overflow-hidden rounded-[8px] border border-line bg-sunken">
          {book.cover ? <img src={book.cover} alt="当前封面" className="size-full object-cover" /> : <div className="grid size-full place-items-center text-faint"><ImageIcon className="size-6" /></div>}
        </div>
        <div className="min-w-0 flex-1">
          <p className="text-[13px] leading-relaxed text-muted">有声书平台通用正方形封面（1:1），建议不小于 1400×1400。可以上传，也可以让 AI 按书名和简介画一张。</p>
          <div className="mt-3 flex flex-wrap gap-2">
            <label className="inline-flex">
              <input type="file" accept="image/*" className="hidden" onChange={async (e) => {
                const f = e.target.files?.[0]
                if (!f) return
                await api.upload(`/books/${book.id}/cover`, f)
                invalidate(['book', book.id], ['books'], ['covers', book.id])
                toast.success('封面已更新')
              }} />
              <span className="inline-flex h-8 cursor-pointer items-center gap-1.5 rounded-control border border-line bg-surface px-3 text-[13px] font-medium hover:border-line-strong"><ImagePlus className="size-4" />上传</span>
            </label>
            <Button size="sm" icon={<Sparkles className="size-3.5" />} disabled={running.length > 0} onClick={() => setOpen(true)}>AI 生成</Button>
          </div>
        </div>
      </div>
      {running.map((j) => (
        <div key={j.id} className="mt-4">
          <div className="flex justify-between gap-3 text-[12.5px] text-muted"><span className="min-w-0 truncate">{j.progressText || '排队中'}</span><span className="tnum">{Math.round(j.progress * 100)}%</span></div>
          <Progress value={j.progress} tone="signal" className="mt-1.5" height={4} />
        </div>
      ))}
      {items.length > 0 && (
        <div className="mt-4 border-t border-line pt-3">
          <div className="mb-2 text-[12px] text-muted">AI 生成过的封面，点一张设为封面</div>
          <div className="grid grid-cols-4 gap-2">
            {items.map((c) => (
              <button key={c.id} type="button" title={c.current ? '当前封面' : '设为封面'} onClick={() => !c.current && pick(c.id)}
                className={cn('relative aspect-square overflow-hidden rounded-[6px] border transition', c.current ? 'border-accent ring-2 ring-accent/30' : 'border-line hover:border-line-strong hover:opacity-90')}>
                <img src={c.url} alt="" className="size-full object-cover" />
                {c.current && <span className="absolute right-1 top-1 grid size-4 place-items-center rounded-full bg-accent text-white"><Check className="size-3" /></span>}
              </button>
            ))}
          </div>
        </div>
      )}
      <CoverDialog book={book} options={q.data} open={open} onOpenChange={setOpen} />
    </Panel>
  )
}

function CoverDialog({ book, options, open, onOpenChange }: { book: any; options?: CoverOptions; open: boolean; onOpenChange: (v: boolean) => void }) {
  const [scene, setScene] = useState('')
  const [lettering, setLettering] = useState(true)
  const [count, setCount] = useState('2')
  const [busy, setBusy] = useState(false)
  const engine = options?.engine
  const canLetter = !!engine?.lettering
  const cost = (options?.price ?? 0) * Number(count)
  const go = async () => {
    setBusy(true)
    try {
      // the first picture becomes the cover only when there is none yet; otherwise the user picks
      await api.post(`/studios/${book.studioId}/generate`, { kind: 'image', bookId: book.id, prompt: scene.trim(), count: Number(count), setCover: !book.cover, noTitle: !lettering })
      toast.success(book.cover ? '正在生成，完成后在「封面」里挑一张' : '正在生成，第一张会自动设为封面')
      onOpenChange(false)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="AI 生成封面" description={engine?.available ? `由「${engine.name}」绘制，通常需要 10–60 秒。` : undefined} width={520}
      footer={<><Button onClick={() => onOpenChange(false)}>取消</Button><Button variant="primary" icon={<Wand2 className="size-4" />} loading={busy} disabled={engine?.available === false} onClick={go}>生成{cost > 0 ? ` · ${cost} 积分` : ''}</Button></>}>
      <div className="space-y-5">
        <Field label="画面描述（可选）" hint="留空时 AI 会根据书名、题材和简介构思画面">
          <Textarea rows={3} value={scene} onChange={(e) => setScene(e.target.value)} placeholder="例如：雨夜的江南古镇，一盏灯笼照亮青石板路，冷色调，电影感" />
        </Field>
        {canLetter ? (
          <Switch checked={lettering} onChange={setLettering} label={`在封面上写书名${book.author ? '和作者' : ''}`} />
        ) : (
          <p className="rounded-md bg-sunken px-3 py-2 text-[12.5px] leading-relaxed text-muted">平台目前使用内置生成器，只能画抽象图案，不能写书名。管理员接入 GPT Image 或 Nano Banana 后可以生成带书名的封面。</p>
        )}
        <Field label="生成几张" hint="多生成几张方便挑选，每张单独计费">
          <Segmented size="sm" value={count} onChange={setCount} items={['1', '2', '4'].map((v) => ({ value: v, label: `${v} 张` }))} />
        </Field>
        <CopyrightNotice what="图片">AI 生成的图片可能与已有作品相似，本平台不保证其不侵犯他人权利，请自行判断后使用。</CopyrightNotice>
      </div>
    </Dialog>
  )
}

