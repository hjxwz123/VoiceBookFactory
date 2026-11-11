import { AnimatePresence, motion } from 'framer-motion'
import { ArrowLeft, BookPlus, Check, FileUp } from '@/components/icons'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { num } from '@/lib/format'
import { Button, cn, Field, Input, Panel, Segmented, Select, Switch, Textarea, Term } from '@/components/ui'
import { useSid } from '../hooks'
import { chapterRefs, chaptersFromPreview, ChapterReview, ImportCh, ManuscriptInput } from './importer'

export default function NewBook() {
  const sid = useSid()
  const nav = useNavigate()
  const [step, setStep] = useState(0)
  const [start, setStart] = useState<'manuscript' | 'empty'>('manuscript')
  const [busy, setBusy] = useState(false)
  const [preview, setPreview] = useState<any>(null)
  const [chs, setChs] = useState<ImportCh[]>([])
  const [info, setInfo] = useState({ title: '', author: '', mode: 'multi', tier: 'standard', genre: '', description: '', autoAnalyze: true })
  const empty = start === 'empty'

  const load = (p: any) => {
    setPreview(p)
    setChs(chaptersFromPreview(p))
    setInfo((x) => ({ ...x, title: p.title || x.title, author: p.author || x.author }))
    setStep(1)
  }
  const create = async () => {
    setBusy(true)
    try {
      const body: any = { ...info }
      if (!empty && preview) Object.assign(body, { importId: preview.id, chapters: chapterRefs(chs) })
      else body.autoAnalyze = false
      const b = await api.post(`/studios/${sid}/books`, body)
      toast.success(empty ? '图书已创建，接下来添加章节' : '图书已创建' + (info.autoAnalyze ? '，AI 正在分析角色' : ''))
      nav(empty ? `/books/${b.id}/chapters?import=1` : `/books/${b.id}`, { replace: true })
    } catch (e: any) {
      toast.error(e.message)
    } finally {
      setBusy(false)
    }
  }
  const included = chs.filter((c) => c.included)
  const steps = empty ? ['选择方式', '填写信息'] : ['准备书稿', '确认章节', '填写信息']
  const visual = empty ? (step === 2 ? 1 : 0) : step
  const back = () => (step === 0 ? nav(-1) : setStep(empty && step === 2 ? 0 : step - 1))

  return (
    <div className="mx-auto max-w-4xl">
      <button onClick={back} className="mb-4 inline-flex items-center gap-1 text-[13px] text-muted hover:text-ink">
        <ArrowLeft className="size-4" /> {step ? '上一步' : '返回'}
      </button>
      <h1 className="font-serif text-[26px] font-semibold tracking-[0.01em]">新建图书</h1>
      <ol className="mt-5 flex items-center gap-2 text-[13px]">
        {steps.map((s, i) => (
          <li key={s} className="flex items-center gap-2">
            <span className={cn('grid size-6 place-items-center rounded-full text-xs font-semibold transition-colors', i < visual ? 'bg-accent text-accent-ink' : i === visual ? 'bg-ink text-bg' : 'bg-sunken text-faint')}>{i < visual ? <Check className="size-3.5" strokeWidth={3} /> : i + 1}</span>
            <span className={i === visual ? 'font-medium' : 'text-muted'}>{s}</span>
            {i < steps.length - 1 && <span className="mx-1 h-px w-10 bg-line" />}
          </li>
        ))}
      </ol>

      <AnimatePresence mode="wait">
        <motion.div key={step + start} initial={{ opacity: 0, x: 12 }} animate={{ opacity: 1, x: 0 }} exit={{ opacity: 0, x: -12 }} transition={{ duration: 0.2, ease: [0.22, 1, 0.36, 1] }} className="mt-6">
          {step === 0 && (
            <div className="space-y-4">
              <div className="grid gap-3 sm:grid-cols-2">
                {[
                  { k: 'manuscript' as const, icon: <FileUp className="size-5" />, t: '从书稿开始', d: '上传或粘贴正文，自动识别章节，最快几分钟就能听到第一章。' },
                  { k: 'empty' as const, icon: <BookPlus className="size-5" />, t: '先创建空白图书', d: '书稿还没准备好？先建好项目、组建团队，之后随时导入章节或逐章添加。' },
                ].map((o) => (
                  <button key={o.k} onClick={() => setStart(o.k)} className={cn('flex gap-3 rounded-panel border p-4 text-left transition-colors', start === o.k ? 'border-accent bg-accent-soft' : 'border-line bg-surface hover:border-line-strong')}>
                    <span className={cn('grid size-10 shrink-0 place-items-center rounded-full', start === o.k ? 'bg-accent text-accent-ink' : 'bg-sunken text-muted')}>{o.icon}</span>
                    <span>
                      <span className="block text-[14px] font-semibold">{o.t}</span>
                      <span className="mt-1 block text-[12.5px] leading-relaxed text-muted">{o.d}</span>
                    </span>
                  </button>
                ))}
              </div>
              {empty ? (
                <div className="flex justify-end"><Button variant="primary" onClick={() => setStep(2)}>下一步</Button></div>
              ) : (
                <Panel><ManuscriptInput onLoaded={load} title={info.title} /></Panel>
              )}
            </div>
          )}

          {step === 1 && preview && (
            <div className="space-y-4">
              <ChapterReview preview={preview} chs={chs} setChs={setChs} onPreview={load} />
              <div className="flex items-center justify-between">
                <p className="text-[13px] text-muted">将制作 {included.length} 章，共 {num(included.reduce((s, c) => s + c.wordCount, 0))} 字。版权页、目录等默认不纳入，需要时打开开关。</p>
                <Button variant="primary" disabled={!included.length} onClick={() => setStep(2)}>下一步</Button>
              </div>
            </div>
          )}

          {step === 2 && (
            <Panel>
              <div className="grid gap-5 sm:grid-cols-2">
                <Field label="书名" hint={!empty && !info.title.trim() ? '没能从书稿里读到书名，请填写一下' : undefined}><Input autoFocus={empty || !info.title.trim()} value={info.title} onChange={(e) => setInfo({ ...info, title: e.target.value })} placeholder="给这本书起个名字" /></Field>
                <Field label="作者"><Input value={info.author} onChange={(e) => setInfo({ ...info, author: e.target.value })} /></Field>
                <Field label="制作类型" hint="多播：旁白之外每个角色有自己的声音；广播剧：还会去掉“他说道”等提示语并加入音效。" className="sm:col-span-2">
                  <Segmented value={info.mode} onChange={(v) => setInfo({ ...info, mode: v })} items={[{ value: 'single', label: '单播（一个声音读全书）' }, { value: 'multi', label: '多播有声书' }, { value: 'drama', label: '广播剧' }]} />
                </Field>
                <Field label="质量档" hint="决定默认选用的音色价位：经济档成本最低，精品档主要角色使用高表现力音色。">
                  <Select value={info.tier} onChange={(v) => setInfo({ ...info, tier: v })} options={[['economy', '经济'], ['standard', '标准（推荐）'], ['premium', '精品']]} />
                </Field>
                <Field label="题材"><Input value={info.genre} onChange={(e) => setInfo({ ...info, genre: e.target.value })} placeholder="言情、悬疑、玄幻、历史……" /></Field>
                <Field label="简介" className="sm:col-span-2" hint={empty ? '可以先空着，导入书稿后可让 AI 帮你写。' : '可以先空着，创建后可让 AI 帮你写。'}><Textarea rows={3} value={info.description} onChange={(e) => setInfo({ ...info, description: e.target.value })} /></Field>
                {!empty && (
                  <div className="flex items-center justify-between rounded-[10px] bg-sunken/60 px-4 py-3 sm:col-span-2">
                    <div>
                      <div className="flex items-center gap-1 text-[13.5px] font-medium">创建后立即分析角色 <Term id="选角" /></div>
                      <div className="text-xs text-muted">AI 会读一遍书稿，找出会说话的角色并写好人物小传，大约需要 1 分钟。</div>
                    </div>
                    <Switch checked={info.autoAnalyze} onChange={(v) => setInfo({ ...info, autoAnalyze: v })} />
                  </div>
                )}
              </div>
              <div className="mt-6 flex items-center justify-end gap-3">
                {empty && <span className="text-xs text-faint">创建后可以在「章节」里导入书稿或逐章添加</span>}
                <Button variant="primary" size="lg" loading={busy} disabled={!info.title.trim()} onClick={create}>创建图书</Button>
              </div>
            </Panel>
          )}
        </motion.div>
      </AnimatePresence>
    </div>
  )
}
