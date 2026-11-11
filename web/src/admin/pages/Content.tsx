import { AudioLines, Download, Music, Plus, Search, Upload } from '@/components/icons'
import { useState } from 'react'
import { toast } from 'sonner'
import { adminApi } from '@/lib/api'
import { ageName, duration, genderName, num, tierName } from '@/lib/format'
import { PlayButton, UploadZone } from '@/components/audio'
import { assetKindName } from '@/components/asset'
import { voiceModels } from '@/components/voice'
import { Badge, Button, confirm, Dialog, Empty, Field, Input, PageHeader, Pagination, Panel, Progress, Segmented, Select, Sheet, Switch, Table, Textarea } from '@/components/ui'
import { ainv, CrudPage, useAdmin } from '../kit'

// ---------------- Voices ----------------
export function Voices() {
  const [q, setQ] = useState('')
  const [scope, setScope] = useState('system')
  const [vmodel, setVModel] = useState('')
  const [page, setPage] = useState(1)
  const [edit, setEdit] = useState<any>(null)
  const { data } = useAdmin<any>('/voices', vmodel === 'placeholder' ? { q, scope, page, pageSize: 30, provider: 'placeholder' } : { q, scope, page, pageSize: 30, model: vmodel })
  return (
    <div>
      <PageHeader title="音色库" description="平台预置音色由运营维护：每个音色对应某个语音平台的音色 ID。用户克隆和设计的音色也可以在这里查看与停用。" actions={<Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEdit({ scope: 'system', name: '', provider: 'placeholder', providerVoiceId: '', gender: 'female', ageGroup: 'young', priceTier: 'standard', language: 'zh-CN', tags: [], description: '', listed: true, recommended: false, crowdPool: false, sampleText: '你好，很高兴认识你。这是我的声音，希望能为你的故事增添色彩。', sortOrder: 0, status: 'ready' })}>新建音色</Button>} />
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <Segmented size="sm" value={scope} onChange={(v) => { setScope(v); setPage(1) }} items={[{ value: 'system', label: '平台音色' }, { value: 'studio', label: '工作室音色' }, { value: 'user', label: '个人音色' }]} />
        <Select size="sm" value={vmodel} onChange={(v) => { setVModel(v); setPage(1) }} options={voiceModels} className="w-44" />
        <div className="ml-auto w-full max-w-xs"><Input icon={<Search />} placeholder="名称或标签" value={q} onChange={(e) => { setQ(e.target.value); setPage(1) }} /></div>
      </div>
      <Panel padded={false}>
        {!data?.items?.length ? <Empty title="没有音色" /> : (
          <Table>
            <thead><tr><th /><th>名称</th><th>平台</th><th>属性</th><th>价位</th><th className="!text-right">使用次数</th><th>状态</th><th /></tr></thead>
            <tbody>
              {data.items.map((v: any) => (
                <tr key={v.id}>
                  <td className="w-10"><PlayButton id={'voice:' + v.id} url={v.sampleUrl} size="xs" /></td>
                  <td><div className="font-medium">{v.name}{v.recommended && <Badge tone="accent" className="ml-1.5">推荐</Badge>}</div><div className="max-w-56 truncate text-xs text-faint">{(v.tags ?? []).join('、')}</div></td>
                  <td className="text-muted">{v.provider === 'placeholder' ? '演示引擎' : v.modelLabel || v.provider}<div className="font-mono text-[11px] text-faint">{v.providerVoiceId}</div></td>
                  <td className="text-muted">{genderName[v.gender]} · {ageName[v.ageGroup]}</td>
                  <td>{tierName[v.priceTier]}</td>
                  <td className="text-right tnum">{num(v.usageCount)}</td>
                  <td>{v.status === 'ready' ? (v.listed || v.scope !== 'system' ? <Badge tone="ok">可用</Badge> : <Badge>未上架</Badge>) : <Badge tone={v.status === 'disabled' ? 'danger' : 'warn'}>{v.status}</Badge>}</td>
                  <td className="whitespace-nowrap text-right"><Button size="xs" variant="ghost" onClick={() => setEdit(v)}>编辑</Button></td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Panel>
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
      <VoiceEditor v={edit} onClose={() => setEdit(null)} />
    </div>
  )
}

function VoiceEditor({ v, onClose }: { v: any; onClose: () => void }) {
  const [f, setF] = useState<any>(null)
  const [busy, setBusy] = useState('')
  if (v && (!f || f.__src !== v)) setTimeout(() => setF({ ...v, __src: v, tagsText: (v.tags ?? []).join('、') }))
  const set = (k: string, x: any) => setF({ ...f, [k]: x })
  const close = () => { onClose(); setF(null) }
  const save = async () => {
    setBusy('save')
    try {
      const { __src, tagsText, capabilities, sampleUrl, favorite, ...body } = f
      body.tags = tagsText.split(/[、,，]+/).map((s: string) => s.trim()).filter(Boolean)
      const r = v.id ? await adminApi.put(`/voices/${v.id}`, body) : await adminApi.post('/voices', body)
      ainv(['/voices'])
      toast.success('已保存')
      if (!v.id) setF({ ...r, __src: r, tagsText: (r.tags ?? []).join('、') })
      else close()
    } finally {
      setBusy('')
    }
  }
  return (
    <Sheet open={!!v} onOpenChange={(o) => !o && close()} title={v?.id ? `编辑「${v.name}」` : '新建平台音色'} width={560} footer={f && <>
      {v?.id && <Button variant="ghost" className="mr-auto text-danger" onClick={async () => { if (await confirm({ title: '删除这个音色？', description: '已选用它的角色会变成未选角。', danger: true, confirmText: '删除' })) { await adminApi.del(`/voices/${v.id}`); ainv(['/voices']); close() } }}>删除</Button>}
      <Button onClick={close}>取消</Button><Button variant="primary" loading={busy === 'save'} onClick={save}>保存</Button>
    </>}>
      {f && (
        <div className="space-y-5">
          <div className="grid grid-cols-2 gap-4">
            <Field label="名称" className="col-span-2"><Input value={f.name} onChange={(e) => set('name', e.target.value)} /></Field>
            <Field label="语音平台" hint="对应「模型与供应商」中的 TTS 适配器"><Input value={f.provider} onChange={(e) => set('provider', e.target.value)} placeholder="aliyun / minimax / elevenlabs …" /></Field>
            <Field label="平台音色 ID"><Input value={f.providerVoiceId} onChange={(e) => set('providerVoiceId', e.target.value)} /></Field>
            <Field label="模型（可选）"><Input value={f.model ?? ''} onChange={(e) => set('model', e.target.value)} /></Field>
            <Field label="语言"><Select value={f.language} onChange={(x) => set('language', x)} options={[['zh-CN', '中文'], ['en-US', '英语'], ['ja-JP', '日语'], ['yue', '粤语']]} /></Field>
            <Field label="性别"><Select value={f.gender} onChange={(x) => set('gender', x)} options={Object.entries(genderName)} /></Field>
            <Field label="年龄段"><Select value={f.ageGroup} onChange={(x) => set('ageGroup', x)} options={Object.entries(ageName)} /></Field>
            <Field label="价位"><Select value={f.priceTier} onChange={(x) => set('priceTier', x)} options={Object.entries(tierName)} /></Field>
            <Field label="排序"><Input type="number" value={f.sortOrder ?? 0} onChange={(e) => set('sortOrder', Number(e.target.value))} /></Field>
            <Field label="标签" className="col-span-2" hint="用顿号分隔，AI 选角会参考"><Input value={f.tagsText} onChange={(e) => set('tagsText', e.target.value)} /></Field>
            <Field label="介绍" className="col-span-2"><Textarea rows={2} value={f.description} onChange={(e) => set('description', e.target.value)} /></Field>
            <Field label="试听文本" className="col-span-2"><Textarea rows={2} value={f.sampleText} onChange={(e) => set('sampleText', e.target.value)} /></Field>
          </div>
          <div className="space-y-2.5">
            <Switch checked={f.listed} onChange={(x) => set('listed', x)} label="上架（用户可见）" />
            <Switch checked={f.recommended} onChange={(x) => set('recommended', x)} label="推荐" />
            <Switch checked={f.crowdPool} onChange={(x) => set('crowdPool', x)} label="加入群演音色池" />
            <Switch checked={f.status === 'ready'} onChange={(x) => set('status', x ? 'ready' : 'disabled')} label="启用" />
          </div>
          {v?.id && (
            <Panel title="试听样本">
              <div className="flex flex-wrap items-center gap-3">
                <PlayButton id={'voice:' + v.id} url={f.sampleUrl ?? v.sampleUrl} size="md" />
                <Button size="sm" loading={busy === 'sample'} onClick={async () => { setBusy('sample'); try { const r = await adminApi.post(`/voices/${v.id}/sample`); set('sampleUrl', r.sampleUrl); ainv(['/voices']); toast.success('已用试听文本重新生成') } finally { setBusy('') } }}>重新生成</Button>
                <label className="cursor-pointer text-[13px] text-accent"><input type="file" accept="audio/*" className="hidden" onChange={async (e) => { const file = e.target.files?.[0]; if (!file) return; const r = await adminApi.upload(`/voices/${v.id}/sample-upload`, file); set('sampleUrl', r.url); ainv(['/voices']) }} />上传样本</label>
              </div>
            </Panel>
          )}
        </div>
      )}
    </Sheet>
  )
}

// ---------------- Assets ----------------
/** One-click import of an official library (music or sound effects) from its CC0 / public-domain sources. */
function LibraryImport({ endpoint, title, icon, description, unit }: { endpoint: string; title: string; icon: React.ReactNode; description: (d: any) => string; unit: string }) {
  const { data, refetch } = useAdmin<any>(endpoint, {}, { refetch: 4000 })
  const [busy, setBusy] = useState(false)
  if (!data) return null
  const job = data.job
  const running = job && (job.status === 'running' || job.status === 'queued')
  const complete = data.total > 0 && data.imported >= data.total
  return (
    <Panel className="mb-5" title={<span className="flex items-center gap-2">{icon}{title}</span>} description={description(data)}
      actions={!complete && data.total > 0 && <Button size="sm" variant="primary" loading={busy || running} icon={<Download className="size-4" />} onClick={async () => { setBusy(true); try { await adminApi.post(endpoint + '/import'); toast.success('开始导入，可以离开此页面，完成后会自动更新'); refetch() } finally { setBusy(false) } }}>{running ? '导入中' : data.imported ? '补全导入' : '一键导入'}</Button>}>
      <div className="flex items-baseline justify-between text-[13px]">
        <span>已导入 <b className="tnum">{data.imported}</b> / {data.total} {unit}</span>
        {running ? <span className="text-[12px] text-muted">{job.progressText}</span> : job?.status === 'succeeded' ? <span className="text-[12px] text-faint">{job.progressText}</span> : job?.status === 'failed' ? <span className="text-[12px] text-danger">{job.error}</span> : null}
      </div>
      <Progress value={running ? job.progress : data.total ? data.imported / data.total : 0} className="mt-2" height={5} />
      {!data.contactSet && !complete && (
        <p className="mt-3 rounded-md bg-warn-soft px-3 py-2 text-[12px] leading-relaxed text-warn">
          导入会从 Wikimedia Commons 和 OpenGameArt 下载文件，这些网站要求下载方留下联系方式。请先在服务器环境变量 <code>VB_BOT_CONTACT</code> 中填写运营联系邮箱或网址并重启服务，否则下载可能被拒绝。
        </p>
      )}
      <p className="mt-3 text-[12px] leading-relaxed text-muted">每条素材都保留了原始页面、作者和许可（CC0 / 公有领域）。许可信息来自原网站，平台不作保证；用户端在素材库、配乐音效选择和导出处都会提示用户自行确认版权，且《版权与素材使用声明》写明平台不承担版权责任。</p>
      <div className="mt-4 flex flex-wrap gap-1.5">
        {data.categories.map((c: any) => (
          <span key={c.key} className="rounded-full bg-sunken px-2.5 py-1 text-[12px] text-muted">{c.name} <span className="tnum text-faint">{c.imported}/{c.total}</span></span>
        ))}
      </div>
    </Panel>
  )
}

function MusicLibrary() {
  return <LibraryImport endpoint="/music-library" unit="首" title="官方配乐库" icon={<Music className="size-4 text-accent" />}
    description={(d) => `精选 ${d.total} 首可免费商用的纯音乐（约 ${Math.round(d.minutes / 60)} 小时），全部为 CC0 或公有领域，来自 Free Music Archive、Musopen 与 OpenGameArt，逐首核对过授权。`} />
}

function SfxLibrary() {
  return <LibraryImport endpoint="/sfx-library" unit="个" title="官方音效库" icon={<AudioLines className="size-4 text-accent" />}
    description={(d) => `精选 ${d.effects} 个音效与 ${d.ambiences} 段环境音，按有声书常用场景分为 ${d.categories.length} 类（风雨雷电、脚步、开关门、打斗、古风锣鼓、恐怖悬疑、搞笑综艺、转场等）。全部是 CC0 或公有领域的真实录音，来自 Wikimedia Commons 与 OpenGameArt；导入时会自动截取、淡入淡出并统一响度。导入完成后，内置的合成示例音效会自动下架。`} />
}

export function Assets() {
  const [kind, setKind] = useState('')
  const [q, setQ] = useState('')
  const [page, setPage] = useState(1)
  const [up, setUp] = useState(false)
  const [meta, setMeta] = useState({ kind: 'bgm', tags: '', license: 'commercial' })
  const { data } = useAdmin<any>('/assets', { kind, q, page, pageSize: 30 })
  return (
    <div>
      <PageHeader title="素材库" description="平台官方素材对所有用户可见，可以免费商用。" actions={<Button variant="primary" icon={<Upload className="size-4" />} onClick={() => setUp(true)}>上传素材</Button>} />
      <MusicLibrary />
      <SfxLibrary />
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <Segmented size="sm" value={kind} onChange={(v) => { setKind(v); setPage(1) }} items={[{ value: '', label: '全部' }, ...['bgm', 'sfx', 'ambience', 'image'].map((k) => ({ value: k, label: assetKindName[k] }))]} />
        <div className="ml-auto w-full max-w-xs"><Input icon={<Search />} placeholder="名称或标签" value={q} onChange={(e) => { setQ(e.target.value); setPage(1) }} /></div>
      </div>
      <Panel padded={false}>
        {!data?.items?.length ? <Empty title="没有素材" /> : (
          <Table>
            <thead><tr><th /><th>名称</th><th>类型</th><th>时长</th><th>标签</th><th>上架</th><th /></tr></thead>
            <tbody>
              {data.items.map((a: any) => (
                <tr key={a.id}>
                  <td className="w-10">{a.kind === 'image' ? <img src={a.url} className="size-7 rounded object-cover" alt="" /> : <PlayButton id={'asset:' + a.id} url={a.url} size="xs" />}</td>
                  <td className="font-medium">{a.name}</td>
                  <td className="text-muted">{assetKindName[a.kind]}</td>
                  <td className="tnum text-muted">{a.durationMs ? duration(a.durationMs) : '—'}</td>
                  <td className="max-w-56 truncate text-xs text-muted">{(a.tags ?? []).join('、')}</td>
                  <td><Switch checked={a.listed} onChange={async (x) => { await adminApi.patch(`/assets/${a.id}`, { listed: x }); ainv(['/assets']) }} /></td>
                  <td className="text-right"><Button size="xs" variant="ghost" className="text-danger" onClick={async () => { if (await confirm({ title: `删除「${a.name}」？`, danger: true, confirmText: '删除' })) { await adminApi.del(`/assets/${a.id}`); ainv(['/assets']) } }}>删除</Button></td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Panel>
      {data && <Pagination page={data.page} pageSize={data.pageSize} total={data.total} onChange={setPage} />}
      <Dialog open={up} onOpenChange={setUp} title="上传官方素材" width={500}>
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-3">
            <Field label="类型"><Select value={meta.kind} onChange={(v) => setMeta({ ...meta, kind: v })} options={['bgm', 'sfx', 'ambience', 'image'].map((k) => [k, assetKindName[k]] as [string, string])} /></Field>
            <Field label="授权"><Select value={meta.license} onChange={(v) => setMeta({ ...meta, license: v })} options={[['commercial', '可商用'], ['personal', '仅个人']]} /></Field>
          </div>
          <Field label="标签" hint="逗号分隔，如：古风,悬疑,可循环"><Input value={meta.tags} onChange={(e) => setMeta({ ...meta, tags: e.target.value })} /></Field>
          <UploadZone multiple accept={meta.kind === 'image' ? 'image/*' : 'audio/*'} onFiles={async (files) => { for (const file of files) await adminApi.upload('/assets', file, { ...meta, name: file.name.replace(/\.[^.]+$/, '') }); ainv(['/assets']); setUp(false); toast.success(`已上传 ${files.length} 个`) }} />
        </div>
      </Dialog>
    </div>
  )
}

// ---------------- Templates ----------------
export function Templates() {
  return (
    <CrudPage
      title="片头片尾模板"
      description="官方模板对所有工作室可用。文字支持占位符 {书名} {章节名} {作者} {主播}。"
      path="/templates"
      columns={[{ key: 'name', label: '名称' }, { key: 'kind', label: '类型', render: (r: any) => (r.kind === 'intro' ? '片头' : '片尾') }, { key: 'mode', label: '方式', render: (r: any) => (r.mode === 'audio' ? '固定音频' : '配乐 + 播报') }, { key: 'text', label: '播报文字', className: 'max-w-xs truncate' }, { key: 'disclaimer', label: 'AI 声明' }]}
      fields={[
        { key: 'name', label: '名称' },
        { key: 'kind', label: '类型', type: 'select', options: [['intro', '片头'], ['outro', '片尾']] },
        { key: 'mode', label: '制作方式', type: 'select', options: [['compose', '配乐 + AI 播报（每集念书名章节名）'], ['audio', '固定音频（每集完全一样）']] },
        { key: 'audioAssetId', label: '固定音频素材 ID', hint: '仅“固定音频”方式使用，填官方素材的 ID' },
        { key: 'audioGain', label: '固定音频音量微调（dB）', type: 'number', hint: '已自动与正文音量对齐，-12 ~ +6' },
        { key: 'text', label: '播报文字', type: 'textarea' },
        { key: 'musicAssetId', label: '背景音乐素材 ID' },
        { key: 'voiceId', label: '播报音色 ID', hint: '留空则使用图书旁白' },
        { key: 'musicVolume', label: '音乐音量（dB）', type: 'number' },
        { key: 'leadMs', label: '人声前音乐（毫秒）', type: 'number' },
        { key: 'tailMs', label: '人声后音乐（毫秒）', type: 'number' },
        { key: 'disclaimer', label: '追加 AI 生成声明', type: 'switch' },
      ]}
      defaults={{ kind: 'intro', mode: 'compose', scope: 'system', musicVolume: -10, leadMs: 2000, tailMs: 1500, disclaimer: false, audioGain: 0 }}
    />
  )
}

// ---------------- Lexicon ----------------
export function Lexicon() {
  return (
    <CrudPage
      title="平台读音词典"
      description="对所有图书生效的多音字与专有名词读音。工作室贡献的词条在这里审核。"
      path="/lexicon"
      filters={[{ key: 'status', label: '状态', options: [['active', '已生效'], ['pending', '待审核']] }]}
      columns={[
        { key: 'term', label: '词语' },
        { key: 'pinyin', label: '读音', render: (r: any) => r.pinyin || (r.replacement && `→ ${r.replacement}`) },
        { key: 'note', label: '备注' },
        { key: 'hits', label: '命中', align: 'right' },
        { key: 'status', label: '状态', render: (r: any) => (r.status === 'pending' ? <Badge tone="warn">待审核</Badge> : <Badge tone="ok">已生效</Badge>) },
      ]}
      rowActions={(r: any) => r.status === 'pending' && <>
        <Button size="xs" onClick={async () => { await adminApi.post(`/lexicon/${r.id}/review`, { approve: false }); ainv(['/lexicon']) }}>驳回</Button>
        <Button size="xs" variant="primary" onClick={async () => { await adminApi.post(`/lexicon/${r.id}/review`, { approve: true }); ainv(['/lexicon']) }}>通过</Button>
      </>}
      fields={[{ key: 'term', label: '词语' }, { key: 'pinyin', label: '拼音', placeholder: 'yā zhòu' }, { key: 'replacement', label: '或替换为' }, { key: 'note', label: '备注' }]}
      defaults={{ scope: 'platform', status: 'active' }}
    />
  )
}
