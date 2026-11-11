import { FlaskConical, Play, Plus, RefreshCw, Trash2 } from '@/components/icons'
import { useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import { adminApi } from '@/lib/api'
import { dateTime, featureName, fromNow, num, pct, tierName } from '@/lib/format'
import { Badge, Button, Checkbox, cn, confirm, Dialog, Empty, Field, IconButton, Input, PageHeader, Panel, Segmented, Select, Sheet, Slider, SuggestInput, Switch, Table, Textarea } from '@/components/ui'
import { ainv, CrudPage, useAdmin } from '../kit'

const kindLabel: Record<string, string> = { llm: '大语言模型', tts: '语音合成', asr: '语音识别', image: '图像生成', music: '音乐生成', sfx: '音效生成' }
const protocolName: Record<string, string> = { openai_chat: 'OpenAI Chat Completions', openai_responses: 'OpenAI Responses', anthropic: 'Anthropic Messages（Claude）' }
const adapterName = (p: any): string =>
  protocolName[p.provider] ??
  ({ openai_image: 'OpenAI Images API', gemini_image: p.config?.protocol === 'generate_content' ? 'Gemini generateContent' : 'Gemini Interactions', placeholder: '内置', qwen: '千问', aliyun: '阿里云百炼', minimax: 'MiniMax', openai_compat: 'OpenAI 兼容' } as Record<string, string>)[p.provider] ??
  p.provider

// ---------------- Providers ----------------
export function Providers() {
  const { data } = useAdmin<any[]>('/providers')
  const presets = useAdmin<any[]>('/providers/presets')
  const [kind, setKind] = useState('llm')
  const [edit, setEdit] = useState<any>(null)
  const [testing, setTesting] = useState('')
  const list = (data ?? []).filter((p) => p.kind === kind && p.scope === 'platform')
  return (
    <div>
      <PageHeader title="模型与供应商" description="平台调用 AI 服务使用的账号。大语言模型支持 OpenAI Chat、OpenAI Responses 和 Anthropic Claude 三种协议，任何兼容服务都可以接入。语音合成已接入千问与 MiniMax，图像生成已接入 OpenAI GPT Image 与 Google Nano Banana，音乐和音效生成已预留接口。" actions={<Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEdit({ kind })}>添加账号</Button>} />
      <Segmented className="mb-4" value={kind} onChange={setKind} items={Object.entries(kindLabel).map(([k, l]) => ({ value: k, label: `${l} ${(data ?? []).filter((p) => p.kind === k && p.scope === 'platform').length || ''}` }))} />
      <Panel padded={false}>
        {!list.length ? <Empty title={`还没有${kindLabel[kind]}账号`} description={kind === 'llm' ? '角色分析、AI 画本、选角等核心功能依赖大语言模型，请至少配置一个。' : kind === 'image' ? '没有接入图像模型时，封面和角色头像由内置生成器画抽象图，不能写书名。推荐接入 OpenAI GPT Image 或 Google Nano Banana。' : '未配置时会使用内置演示引擎。'} action={<Button variant="primary" onClick={() => setEdit({ kind })}>添加</Button>} /> : (
          <Table>
            <thead><tr><th>名称</th><th>协议 / 适配器</th><th>模型</th><th className="!text-right">优先级</th><th className="!text-right">调用</th><th className="!text-right">失败</th><th>健康</th><th>启用</th><th /></tr></thead>
            <tbody>
              {list.map((p) => (
                <tr key={p.id}>
                  <td><div className="font-medium">{p.name}</div><div className="max-w-56 truncate font-mono text-[11px] text-faint">{p.baseUrl}</div></td>
                  <td className="text-muted">{adapterName(p)}</td>
                  <td className="font-mono text-xs">{p.model || '—'}</td>
                  <td className="text-right tnum">{p.priority}</td>
                  <td className="text-right tnum">{num(p.calls)}</td>
                  <td className="text-right tnum">{num(p.failures)}</td>
                  <td>{p.healthStatus === 'ok' ? <Badge tone="ok">正常</Badge> : p.healthStatus === 'error' ? <Badge tone="danger" className="max-w-36 truncate" >{p.healthNote || '异常'}</Badge> : <Badge>未检测</Badge>}</td>
                  <td><Switch checked={p.enabled} onChange={async (v) => { await adminApi.patch(`/providers/${p.id}`, { enabled: v }); ainv(['/providers']) }} /></td>
                  <td className="whitespace-nowrap text-right">
                    <Button size="xs" variant="ghost" loading={testing === p.id} onClick={async () => { setTesting(p.id); try { const r = await adminApi.post(`/providers/${p.id}/test`); if (r?.ok === false) toast.error(r.message); else toast.success(r?.message || '连接正常') } catch (e: any) { toast.error(e.message) } finally { setTesting(''); ainv(['/providers']) } }}>测试</Button>
                    {p.kind === 'tts' && <Button size="xs" variant="ghost" icon={<RefreshCw className="size-3" />} onClick={async () => { try { const r = await adminApi.post(`/providers/${p.id}/sync-voices`); toast.success(r.imported ? `新增 ${r.imported} 个音色` : '音色库已是最新') } catch (e: any) { toast.error(e.message) } }}>同步音色</Button>}
                    {(p.provider === 'qwen' || p.provider === 'aliyun') && <Button size="xs" variant="ghost" onClick={async () => {
                      if (!(await confirm({ title: '导入基础音色试听音频？', description: '将从千问官方下载约 200 MB 的试听音频包，为 600 个基础音色配上试听，后台执行，约需几分钟。系统音色的试听已直接使用官方链接。', confirmText: '开始导入' }))) return
                      const j = await adminApi.post(`/providers/${p.id}/import-previews`, {})
                      toast.success('已开始导入，可在「任务队列」查看进度', { description: j?.title })
                    }}>导入基础音色试听</Button>}
                    <Button size="xs" variant="ghost" onClick={() => setEdit(p)}>编辑</Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Panel>
      <ProviderEditor value={edit} presets={presets.data ?? []} onClose={() => setEdit(null)} />
    </div>
  )
}

function ProviderEditor({ value, presets, onClose }: { value: any; presets: any[]; onClose: () => void }) {
  const [f, setF] = useState<any>(null)
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (!value) return setF(null)
    setF({ preset: '', apiKey: '', ...value, config: { ...(value.config ?? {}) }, modelsText: (value.models ?? []).map((m: any) => m.id).join('\n'), priority: value.priority ?? 1, rpm: value.rpm ?? 120, concurrency: value.concurrency ?? 4, costPerUnit: value.costPerUnit ?? 0, name: value.name ?? '', baseUrl: value.baseUrl ?? '', model: value.model ?? '', provider: value.provider ?? '' })
  }, [value])
  if (!f) return <Sheet open={false} onOpenChange={() => {}} />
  const set = (k: string, v: any) => setF({ ...f, [k]: v })
  const setC = (k: string, v: any) => setF({ ...f, config: { ...f.config, [k]: v === '' ? undefined : v } })
  const kindPresets = presets.filter((p) => p.kind === f.kind)
  const preset = presets.find((p) => p.key === f.preset)
  const isLLM = f.kind === 'llm'
  const isMiniMax = f.kind === 'tts' && f.provider === 'minimax'
  const isSuno = f.provider === 'suno'
  const isQwen = f.kind === 'tts' && (f.provider === 'qwen' || f.provider === 'aliyun')
  const isOpenAIImage = f.kind === 'image' && f.provider === 'openai_image'
  const isGeminiImage = f.kind === 'image' && f.provider === 'gemini_image'
  const modelOptions = (f.modelsText as string).split('\n').map((s) => s.trim()).filter(Boolean)
  const save = async () => {
    setBusy(true)
    try {
      const body: any = { kind: f.kind, provider: f.provider, name: f.name, baseUrl: f.baseUrl, model: f.model, config: f.config, priority: Number(f.priority), rpm: Number(f.rpm), concurrency: Number(f.concurrency), costPerUnit: Number(f.costPerUnit), models: f.modelsText.split('\n').map((s: string) => s.trim()).filter(Boolean).map((id: string) => ({ id, name: id, capabilities: [], priceTier: '' })) }
      if (f.apiKey) body.apiKey = f.apiKey
      if (value.id) await adminApi.patch(`/providers/${value.id}`, body)
      else await adminApi.post('/providers', body)
      ainv(['/providers'])
      toast.success('已保存，建议点「测试」确认连接')
      onClose()
    } finally {
      setBusy(false)
    }
  }
  return (
    <Sheet open={!!value} onOpenChange={(o) => !o && onClose()} title={value?.id ? `编辑「${value.name}」` : `添加${kindLabel[f.kind]}账号`} width={580} footer={<>
      {value?.id && <IconButton label="删除" className="mr-auto text-danger" icon={<Trash2 className="size-4" />} onClick={async () => { if (await confirm({ title: '删除这个账号？', description: '使用它的路由规则会自动回退到其他账号。', danger: true, confirmText: '删除' })) { await adminApi.del(`/providers/${value.id}`); ainv(['/providers']); onClose() } }} />}
      <Button onClick={onClose}>取消</Button><Button variant="primary" loading={busy} disabled={!f.provider || !f.name} onClick={save}>保存</Button>
    </>}>
      <div className="space-y-5">
        {!value?.id && (
          <Field label="从预设开始" hint="预设只是填好常用参数，所有字段都可以修改">
            <Select value={f.preset} onChange={(k) => { const p = presets.find((x) => x.key === k); setF({ ...f, preset: k, provider: p.provider, name: p.name, baseUrl: p.baseUrl, model: p.models?.[0] ?? '', modelsText: (p.models ?? []).join('\n'), rpm: p.rpm ?? f.rpm, concurrency: p.concurrency ?? f.concurrency }) }} placeholder="选择" options={kindPresets.map((p) => [p.key, `${p.name}${p.implemented ? '' : '（预留接口）'}`] as [string, string])} />
          </Field>
        )}
        {preset?.note && <p className="rounded-md bg-sunken px-3 py-2 text-xs text-muted">{preset.note}{preset.docs && <> · <a href={preset.docs} target="_blank" className="text-accent">文档</a></>}</p>}
        <div className="grid grid-cols-2 gap-4">
          <Field label="名称"><Input value={f.name} onChange={(e) => set('name', e.target.value)} /></Field>
          {isLLM ? (
            <Field label="接口协议"><Select value={f.provider} onChange={(v) => set('provider', v)} placeholder="选择协议" options={Object.entries(protocolName)} /></Field>
          ) : (
            <Field label="适配器"><Input value={f.provider} onChange={(e) => set('provider', e.target.value)} /></Field>
          )}
          <Field label="接口地址（Base URL）" className="col-span-2" hint={isSuno ? '支持 Suno 的 New API / One API 中转服务的根地址，例如 https://relay.example.com，不要带 /v1' : undefined}><Input value={f.baseUrl} onChange={(e) => set('baseUrl', e.target.value)} placeholder={isSuno ? 'https://relay.example.com' : undefined} className="font-mono text-[12.5px]" /></Field>
          <Field label="API Key" className="col-span-2" hint={value?.id ? `当前 ••••${value.keyLast4}，留空则不修改` : '加密保存'}><Input type="password" value={f.apiKey} onChange={(e) => set('apiKey', e.target.value)} autoComplete="off" /></Field>
          <Field label="默认模型" hint={isMiniMax ? 'HD 音质最好，Turbo 更快更便宜；音色库里的音色自带模型' : undefined}><SuggestInput value={f.model} onChange={(v) => set('model', v)} suggestions={modelOptions} className="font-mono text-[12.5px]" /></Field>
          <Field label="优先级" hint="同类账号中数字大的优先使用"><Input type="number" value={f.priority} onChange={(e) => set('priority', e.target.value)} /></Field>
          <Field label="每分钟请求上限" hint={isMiniMax ? '充值用户 20，免费用户 10；超出会自动排队' : undefined}><Input type="number" value={f.rpm} onChange={(e) => set('rpm', e.target.value)} /></Field>
          <Field label="并发上限"><Input type="number" value={f.concurrency} onChange={(e) => set('concurrency', e.target.value)} /></Field>
          <Field label="成本（元 / 单位）" hint={isLLM ? '每千 token' : f.kind === 'tts' ? '每千字' : f.kind === 'asr' ? '每分钟' : f.kind === 'image' ? '每张图' : '每次'} className="col-span-2"><Input type="number" step="0.0001" value={f.costPerUnit} onChange={(e) => set('costPerUnit', e.target.value)} /></Field>
          <Field label="可选模型列表" hint="每行一个，路由规则可以按功能指定" className="col-span-2"><Textarea rows={3} value={f.modelsText} onChange={(e) => set('modelsText', e.target.value)} className="font-mono text-[12.5px]" /></Field>
        </div>
        {isSuno && (
          <Panel title="Suno 生成设置" description="一首歌通常 1–3 分钟出结果，不确定时保持默认即可。">
            <div className="grid grid-cols-2 gap-4">
              <Field label="查询进度间隔（秒）" hint="多久问一次服务方生成好了没有"><Input type="number" min={2} value={f.config.pollSec ?? ''} placeholder="5" onChange={(e) => setC('pollSec', e.target.value ? Number(e.target.value) : undefined)} /></Field>
              <Field label="最长等待（秒）" hint="超过后任务记为失败，不扣积分"><Input type="number" min={60} value={f.config.maxWaitSec ?? ''} placeholder="600" onChange={(e) => setC('maxWaitSec', e.target.value ? Number(e.target.value) : undefined)} /></Field>
            </div>
            <p className="mt-3 text-xs leading-relaxed text-muted">点「测试」只检查接口地址和密钥，不会生成歌曲，也不产生费用。</p>
          </Panel>
        )}
        {isMiniMax && (
          <Panel title="MiniMax 合成设置" description="不确定时保持默认即可。">
            <div className="grid grid-cols-2 gap-4">
              <Field label="合成方式" className="col-span-2" hint={(f.config.synthMode ?? 'auto') === 'auto' ? '普通句子走同步接口，最快；超过 3000 字的长文本自动改用异步任务' : f.config.synthMode === 'async' ? '每次合成都提交异步任务再取回结果，速度较慢，适合额度或限速只开在异步接口上的账号' : '始终使用同步接口（单次最长 1 万字符）'}>
                <Segmented value={f.config.synthMode ?? 'auto'} onChange={(v) => setC('synthMode', v === 'auto' ? '' : v)} items={[{ value: 'auto', label: '自动' }, { value: 'sync', label: '同步' }, { value: 'async', label: '异步（长文本）' }]} />
              </Field>
              <Field label="文本规范化" hint="让数字、日期、单位读得更自然；「高质量」仅 2.8 / 2.6 模型生效，会稍慢">
                <Select value={f.config.textNormalization ?? ''} onChange={(v) => setC('textNormalization', v)} options={[['', '关闭'], ['basic', '快速'], ['quality', '高质量']]} />
              </Field>
              <Field label="小语种与方言增强" hint="外语书和粤语音色总会按语言自动增强">
                <Select value={f.config.languageBoost ?? ''} onChange={(v) => setC('languageBoost', v)} options={[['', '按书籍语言'], ['auto', '中文书也让模型自动判断']]} />
              </Field>
              <Field label="GroupId（可选）" className="col-span-2" hint="只有早期开通的账号需要，新账号留空"><Input value={f.config.groupId ?? ''} onChange={(e) => setC('groupId', e.target.value.trim())} className="font-mono text-[12.5px]" /></Field>
              <div className="col-span-2"><Switch checked={!!f.config.watermark} onChange={(v) => setC('watermark', v ? true : '')} label="在每段音频末尾加入 MiniMax 的 AI 生成标识（音频水印）" /></div>
            </div>
          </Panel>
        )}
        {isQwen && (
          <Panel title="千问复刻与声音设计" description="用户复刻或设计出的音色会绑定到这里选的模型，之后只能用这个模型合成。">
            <div className="grid grid-cols-2 gap-4">
              <Field label="复刻音色使用的模型" hint="Qwen-Audio 3.1 Plus 音质最好，对有噪音、回声的录音也更稳，适合有声书；Flash 更快更省">
                <Select value={f.config.cloneModel ?? ''} onChange={(v) => setC('cloneModel', v)} options={[['', 'Qwen-Audio 3.1 Plus（推荐）'], ['qwen-audio-3.1-tts-flash', 'Qwen-Audio 3.1 Flash'], ['qwen-audio-3.0-tts-plus', 'Qwen-Audio 3.0 Plus'], ['qwen-audio-3.0-tts-flash', 'Qwen-Audio 3.0 Flash'], ['cosyvoice-v3.5-plus', 'CosyVoice v3.5 Plus'], ['cosyvoice-v3.5-flash', 'CosyVoice v3.5 Flash'], ['cosyvoice-v3-plus', 'CosyVoice v3 Plus'], ['cosyvoice-v3-flash', 'CosyVoice v3 Flash'], ['cosyvoice-v2', 'CosyVoice v2']]} />
              </Field>
              <Field label="设计音色使用的模型" hint="「AI 设计音色」用的模型；CosyVoice v2 不支持声音设计，复刻选它时设计会改用 Qwen-Audio 3.1 Plus">
                <Select value={f.config.designModel ?? ''} onChange={(v) => setC('designModel', v)} options={[['', '与复刻相同'], ['qwen-audio-3.1-tts-plus', 'Qwen-Audio 3.1 Plus'], ['qwen-audio-3.1-tts-flash', 'Qwen-Audio 3.1 Flash'], ['qwen-audio-3.0-tts-plus', 'Qwen-Audio 3.0 Plus'], ['qwen-audio-3.0-tts-flash', 'Qwen-Audio 3.0 Flash'], ['cosyvoice-v3.5-plus', 'CosyVoice v3.5 Plus'], ['cosyvoice-v3.5-flash', 'CosyVoice v3.5 Flash'], ['cosyvoice-v3-plus', 'CosyVoice v3 Plus'], ['cosyvoice-v3-flash', 'CosyVoice v3 Flash']]} />
              </Field>
            </div>
            <p className="mt-3 text-[12px] text-muted">创建复刻、设计音色都免费，合成时按所绑定模型计费；每个千问账号最多保留 1000 个自定义音色（复刻和设计共用），一年没用过的会被千问自动删除。每次设计会生成 3 个候选，用户没保存的候选 24 小时后自动清理。已经创建的音色不受这里修改的影响。</p>
          </Panel>
        )}
        {isOpenAIImage && (
          <Panel title="GPT Image 生成设置" description="不确定时保持默认即可。用户每生成一张图按「积分定价」里的 AI 封面价格扣积分。">
            <div className="grid grid-cols-2 gap-4">
              <Field label="画质" hint={f.config.quality === 'high' ? '书名等文字最清楚，价格约为「中」的 4 倍' : f.config.quality === 'low' ? '最快最便宜，文字容易出错，适合打草稿' : f.config.quality === 'medium' ? '速度和价格适中' : '由模型自己决定'}>
                <Select value={f.config.quality ?? ''} onChange={(v) => setC('quality', v)} options={[['', '自动'], ['low', '低'], ['medium', '中'], ['high', '高']]} />
              </Field>
              <Field label="分辨率" hint="高清需要 gpt-image-2 及更新的模型，旧模型自动用标准尺寸">
                <Select value={f.config.resolution ?? ''} onChange={(v) => setC('resolution', v)} options={[['', '标准（1024×1024）'], ['2k', '高清（2048×2048）']]} />
              </Field>
              <Field label="内容审核" hint="OpenAI 对画面描述的过滤强度">
                <Select value={f.config.moderation ?? ''} onChange={(v) => setC('moderation', v)} options={[['', '标准'], ['low', '宽松']]} />
              </Field>
              <Field label="超时（秒）" hint="默认 300"><Input type="number" value={f.config.timeoutSec ?? ''} onChange={(e) => setC('timeoutSec', e.target.value ? Number(e.target.value) : '')} /></Field>
            </div>
            <p className="mt-3 text-[12px] text-muted">「测试」只核对密钥和模型，不会生成图片，也不产生费用。首次使用前需要在 OpenAI 后台完成组织验证（Organization Verification）。</p>
          </Panel>
        )}
        {isGeminiImage && (
          <Panel title="Nano Banana 生成设置" description="不确定时保持默认即可。用户每生成一张图按「积分定价」里的 AI 封面价格扣积分。">
            <div className="grid grid-cols-2 gap-4">
              <Field label="接口协议" className="col-span-2" hint={f.config.protocol === 'generate_content' ? '旧的 models/模型:generateContent 接口，只在中转服务还不支持 Interactions 时使用' : '官方文档当前推荐的 Interactions API'}>
                <Segmented value={f.config.protocol ?? ''} onChange={(v) => setC('protocol', v)} items={[{ value: '', label: 'Interactions（推荐）' }, { value: 'generate_content', label: 'generateContent（兼容）' }]} />
              </Field>
              <Field label="分辨率" hint="越大越清晰，费用越高；512 只有 Nano Banana 2（3.1 Flash Image）支持">
                <Select value={f.config.imageSize ?? ''} onChange={(v) => setC('imageSize', v)} options={[['', '1K（1024×1024）'], ['2K', '2K（2048×2048）'], ['4K', '4K（4096×4096）'], ['512', '512']]} />
              </Field>
              {f.config.protocol !== 'generate_content' && (
                <Field label="思考强度" hint="想得越多构图和文字越稳，但更慢，思考也计费">
                  <Select value={f.config.thinkingLevel ?? ''} onChange={(v) => setC('thinkingLevel', v)} options={[['', '模型默认'], ['minimal', '最少'], ['medium', '中'], ['high', '高']]} />
                </Field>
              )}
              <Field label="超时（秒）" hint="默认 300"><Input type="number" value={f.config.timeoutSec ?? ''} onChange={(e) => setC('timeoutSec', e.target.value ? Number(e.target.value) : '')} /></Field>
            </div>
            <p className="mt-3 text-[12px] text-muted">「测试」只核对密钥和模型，不会生成图片，也不产生费用。密钥在 Google AI Studio 创建；服务器所在地区需要在 Gemini API 的支持范围内，否则请填中转地址。</p>
          </Panel>
        )}
        {isLLM && (
          <Panel title="生成参数" description="不同协议支持的参数不同，不填则使用默认值。">
            <div className="grid grid-cols-2 gap-4">
              <Field label="推理强度（effort）" hint={f.provider === 'anthropic' ? 'Claude 输出配置' : f.provider === 'openai_responses' ? 'reasoning.effort' : '支持的模型才生效'}>
                <Select value={f.config.effort ?? ''} onChange={(v) => setC('effort', v)} options={[['', '默认'], ['low', '低'], ['medium', '中'], ['high', '高'], ...(f.provider === 'anthropic' ? [['xhigh', '更高'], ['max', '最高']] as [string, string][] : [])]} />
              </Field>
              <Field label="最大输出 token"><Input type="number" value={f.config.maxTokens ?? ''} onChange={(e) => setC('maxTokens', e.target.value ? Number(e.target.value) : '')} /></Field>
              {f.provider !== 'anthropic' && <Field label="温度"><Input type="number" step="0.1" value={f.config.temperature ?? ''} onChange={(e) => setC('temperature', e.target.value ? Number(e.target.value) : '')} /></Field>}
              {f.provider === 'openai_chat' && <Field label="JSON 模式"><Select value={f.config.jsonMode ?? ''} onChange={(v) => setC('jsonMode', v)} options={[['', '自动'], ['json_schema', 'json_schema'], ['json_object', 'json_object'], ['none', '不使用（提示词约束）']]} /></Field>}
              <Field label="超时（秒）"><Input type="number" value={f.config.timeoutSec ?? ''} onChange={(e) => setC('timeoutSec', e.target.value ? Number(e.target.value) : '')} /></Field>
              {f.provider === 'openai_chat' && <div className="col-span-2"><Switch checked={!!f.config.maxCompletionTokens} onChange={(v) => setC('maxCompletionTokens', v)} label="使用 max_completion_tokens（新版 OpenAI 推理模型需要）" /></div>}
              {f.provider === 'anthropic' && <div className="col-span-2"><Switch checked={f.config.fallbacks !== false} onChange={(v) => setC('fallbacks', v)} label="官方接口过载时允许服务端自动降级到备用模型" /></div>}
            </div>
          </Panel>
        )}
      </div>
    </Sheet>
  )
}

// ---------------- Routing ----------------
export function Routing() {
  const { data } = useAdmin<any[]>('/providers')
  const opts = (data ?? []).filter((p) => p.scope === 'platform').map((p) => [p.id, `${kindLabel[p.kind]} · ${p.name}`] as [string, string])
  const name = (id: string) => (data ?? []).find((p) => p.id === id)?.name ?? id
  return (
    <CrudPage
      title="路由规则"
      description="指定每个功能使用哪个账号和模型，主账号失败时依次尝试备用账号。没有规则的功能按账号优先级自动选择。"
      path="/routing"
      searchable={false}
      columns={[
        { key: 'feature', label: '功能', render: (r: any) => featureName[r.feature] ?? r.feature },
        { key: 'tier', label: '档位', render: (r: any) => (r.tier && r.tier !== '*' ? tierName[r.tier] : '全部') },
        { key: 'primaryId', label: '主账号', render: (r: any) => name(r.primaryId) },
        { key: 'fallbacks', label: '备用', render: (r: any) => (r.fallbacks ?? []).map(name).join(' → ') || '—' },
        { key: 'model', label: '模型' },
        { key: 'studioId', label: '适用工作室', render: (r: any) => r.studioId || '全部' },
        { key: 'enabled', label: '启用' },
      ]}
      fields={[
        { key: 'feature', label: '功能', type: 'select', options: Object.entries(featureName) },
        { key: 'tier', label: '质量档', type: 'select', options: [['*', '全部'], ...Object.entries(tierName)] },
        { key: 'primaryId', label: '主账号', type: 'select', options: opts },
        { key: 'model', label: '模型（可选）', hint: '覆盖账号的默认模型' },
        { key: 'fallbacks', label: '备用账号 ID', type: 'tags', hint: `按顺序填写，用顿号分隔。可选：${opts.map(([id, l]) => `${l}=${id.slice(0, 8)}…`).join('；')}` },
        { key: 'studioId', label: '仅对某工作室生效（ID）', hint: '用于大客户专线，留空为全局' },
        { key: 'enabled', label: '启用', type: 'switch' },
        { key: 'note', label: '备注' },
      ]}
      defaults={{ feature: 'script', tier: '*', enabled: true, fallbacks: [] }}
      keyName={(r: any) => featureName[r.feature]}
    />
  )
}

// ---------------- Pricing ----------------
export function Pricing() {
  return (
    <CrudPage
      title="积分定价"
      description="每个功能按什么单位、扣多少积分。修改后对新任务立即生效。"
      path="/pricing"
      searchable={false}
      columns={[
        { key: 'name', label: '名称', render: (r: any) => r.name || featureName[r.feature] },
        { key: 'feature', label: '功能', render: (r: any) => <span className="font-mono text-xs">{r.feature}</span> },
        { key: 'tier', label: '档位', render: (r: any) => (r.tier && r.tier !== '*' ? tierName[r.tier] ?? r.tier : '不区分') },
        { key: 'credits', label: '积分', align: 'right' },
        { key: 'unit', label: '单位', render: (r: any) => ({ per_1k_chars: '每千字', per_minute: '每分钟', per_second: '每秒', per_call: '每次', per_1k_tokens: '每千 token' } as any)[r.unit] ?? r.unit },
        { key: 'freeDaily', label: '每日免费', align: 'right' },
      ]}
      fields={[
        { key: 'feature', label: '功能', type: 'select', options: Object.entries(featureName) },
        { key: 'tier', label: '档位', type: 'select', options: [['*', '不区分'], ...Object.entries(tierName)] },
        { key: 'name', label: '显示名称' },
        { key: 'unit', label: '单位', type: 'select', options: [['per_1k_chars', '每千字'], ['per_minute', '每分钟'], ['per_second', '每秒'], ['per_call', '每次'], ['per_1k_tokens', '每千 token']] },
        { key: 'credits', label: '积分', type: 'number' },
        { key: 'freeDaily', label: '每日免费次数', type: 'number' },
        { key: 'note', label: '备注', type: 'textarea' },
      ]}
      defaults={{ unit: 'per_1k_chars', tier: '*', credits: 10, freeDaily: 0 }}
    />
  )
}

// ---------------- Prompts ----------------
export function Prompts() {
  const { data } = useAdmin<any[]>('/prompts')
  const providers = useAdmin<any[]>('/providers')
  const [key, setKey] = useState('')
  const [draft, setDraft] = useState('')
  const [note, setNote] = useState('')
  const [sample, setSample] = useState('夜色渐深，她推开窗。\n“你来做什么？”林黛玉冷冷地问。\n宝玉笑道：“我来看看妹妹。”')
  const [account, setAccount] = useState('')
  const [result, setResult] = useState<any>(null)
  const [busy, setBusy] = useState('')
  const cur = (data ?? []).find((p) => p.key === key) ?? data?.[0]
  useEffect(() => {
    if (!cur) return
    if (!key) setKey(cur.key)
    const active = (cur.versions ?? []).find((v: any) => v.active)
    setDraft(active?.content ?? cur.default)
    setResult(null)
  }, [cur?.key, data])
  const llms = (providers.data ?? []).filter((p) => p.kind === 'llm' && p.scope === 'platform')
  const versions = cur?.versions ?? []
  return (
    <div>
      <PageHeader title="提示词" description="每个 AI 功能的系统提示词都可以在这里修改、测试，并按比例灰度发布。没有自定义版本时使用内置默认值。" />
      <div className="grid gap-6 lg:grid-cols-[220px_1fr]">
        <nav className="space-y-0.5">
          {(data ?? []).map((p) => (
            <button key={p.key} onClick={() => setKey(p.key)} className={cn('flex w-full items-center justify-between rounded-[8px] px-3 py-2 text-left text-[13.5px]', cur?.key === p.key ? 'bg-sunken font-medium' : 'text-muted hover:bg-sunken/60 hover:text-ink')}>
              {p.name}{p.versions?.some((v: any) => v.active) && <span className="size-1.5 rounded-full bg-accent" />}
            </button>
          ))}
        </nav>
        {cur && (
          <div className="space-y-6">
            <Panel title={cur.name} description={<span className="font-mono">{cur.key}</span>}>
              <Textarea rows={16} value={draft} onChange={(e) => setDraft(e.target.value)} className="font-mono text-[12.5px] leading-relaxed" />
              <div className="mt-3 flex flex-wrap items-center gap-2">
                <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder="版本说明，例如：加强对内心独白的识别" className="max-w-sm" />
                <Button variant="ghost" size="sm" onClick={() => setDraft(cur.default)}>恢复默认</Button>
                <Button size="sm" className="ml-auto" loading={busy === 'save'} onClick={async () => { setBusy('save'); try { await adminApi.post('/prompts', { key: cur.key, content: draft, note }); setNote(''); ainv(['/prompts']); toast.success('已保存为新版本（未生效）') } finally { setBusy('') } }}>保存为新版本</Button>
              </div>
            </Panel>
            <Panel title="测试" description="用当前编辑框里的提示词和下面的样例文本调用模型，查看输出。">
              <div className="grid gap-4 lg:grid-cols-2">
                <div className="space-y-3">
                  <Textarea rows={6} value={sample} onChange={(e) => setSample(e.target.value)} />
                  <div className="flex gap-2">
                    <Select value={account} onChange={setAccount} options={[['', '按路由规则选择模型'], ...llms.map((p) => [p.id, `${p.name} · ${p.model}`] as [string, string])]} className="flex-1" />
                    <Button variant="primary" icon={<Play className="size-4" />} loading={busy === 'test'} onClick={async () => { setBusy('test'); try { setResult(await adminApi.post('/prompts/test', { key: cur.key, content: draft, sample, accountId: account })) } finally { setBusy('') } }}>运行</Button>
                  </div>
                </div>
                <div className="max-h-80 overflow-auto rounded-[10px] bg-sunken/60 p-3">
                  {!result ? <p className="text-[13px] text-faint">运行后在这里显示结果</p> : result.units ? (
                    <ul className="space-y-1.5 text-[12.5px]">{result.units.map((u: any) => <li key={u.id}><Badge>{u.type}</Badge> <span className="font-medium">{u.speaker || '旁白'}</span> <span className="text-muted">[{u.emotion}]</span> {u.text}</li>)}</ul>
                  ) : <pre className="whitespace-pre-wrap font-mono text-[12px]">{JSON.stringify(result, null, 2)}</pre>}
                </div>
              </div>
            </Panel>
            <Panel title="版本" padded={false}>
              {!versions.length ? <p className="px-5 py-6 text-center text-[13px] text-faint">还没有自定义版本，正在使用内置默认提示词</p> : (
                <Table>
                  <thead><tr><th>版本</th><th>说明</th><th>状态</th><th>创建</th><th /></tr></thead>
                  <tbody>
                    {versions.map((v: any) => (
                      <tr key={v.id}>
                        <td className="tnum">v{v.version}</td>
                        <td className="max-w-xs truncate text-muted">{v.note || '—'}</td>
                        <td>{v.active ? <Badge tone="ok">{v.rollout && v.rollout < 100 ? `灰度 ${v.rollout}%` : '全量生效'}</Badge> : <Badge>未生效</Badge>}</td>
                        <td className="text-xs text-faint">{fromNow(v.createdAt)}</td>
                        <td className="whitespace-nowrap text-right">
                          <Button size="xs" variant="ghost" onClick={() => setDraft(v.content)}>载入</Button>
                          <Rollout v={v} />
                          {v.active && <Button size="xs" variant="ghost" onClick={async () => { await adminApi.post(`/prompts/${v.id}/deactivate`); ainv(['/prompts']) }}>停用</Button>}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </Table>
              )}
            </Panel>
          </div>
        )}
      </div>
    </div>
  )
}

function Rollout({ v }: { v: any }) {
  const [open, setOpen] = useState(false)
  const [r, setR] = useState(v.rollout || 100)
  return (
    <>
      <Button size="xs" variant={v.active ? 'ghost' : 'primary'} onClick={() => setOpen(true)}>{v.active ? '调整比例' : '发布'}</Button>
      <Dialog open={open} onOpenChange={setOpen} title={`发布 v${v.version}`} description="灰度发布时，按工作室随机分配，部分请求使用新版本，其余继续使用旧版本。" width={420} footer={<><Button onClick={() => setOpen(false)}>取消</Button><Button variant="primary" onClick={async () => { await adminApi.post(`/prompts/${v.id}/activate`, { rollout: r }); ainv(['/prompts']); setOpen(false); toast.success(r >= 100 ? '已全量发布' : `已灰度 ${r}%`) }}>确定</Button></>}>
        <Field label={`流量比例 ${r}%`}><Slider value={r} min={5} max={100} step={5} onChange={setR} /></Field>
      </Dialog>
    </>
  )
}

// ---------------- Evals ----------------
export function Evals() {
  const { data } = useAdmin<any>('/evals', { pageSize: 50 })
  const providers = useAdmin<any[]>('/providers')
  const llms = (providers.data ?? []).filter((p) => p.kind === 'llm' && p.scope === 'platform')
  const [account, setAccount] = useState('')
  const [edit, setEdit] = useState<any>(null)
  const [running, setRunning] = useState('')
  const items = data?.items ?? []
  return (
    <div>
      <PageHeader title="效果评测" description="用标注好的样例评测画本标注的准确率（说话人、类型、情绪），在切换模型或修改提示词前后对比。" actions={<Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEdit({ name: '', kind: 'script_annotation', items: [] })}>新建评测集</Button>} />
      <div className="mb-4 flex items-center gap-2">
        <span className="text-[13px] text-muted">评测模型</span>
        <Select size="sm" value={account} onChange={setAccount} options={[['', '按路由规则'], ...llms.map((p) => [p.id, `${p.name} · ${p.model}`] as [string, string])]} className="w-72" />
      </div>
      {!items.length ? <Empty icon={<FlaskConical />} title="还没有评测集" /> : (
        <div className="space-y-4">
          {items.map((e: any) => (
            <Panel key={e.id} title={e.name} description={`${e.items?.length ?? 0} 个样例 · ${e.items?.reduce((s: number, x: any) => s + (x.expected?.length ?? 0), 0)} 条标注`} actions={<><Button size="xs" variant="ghost" onClick={() => setEdit(e)}>编辑</Button><Button size="xs" variant="primary" icon={<Play className="size-3.5" />} loading={running === e.id} onClick={async () => {
              setRunning(e.id)
              try {
                const j = await adminApi.post(`/evals/${e.id}/run`, { accountId: account })
                for (let i = 0; i < 120; i++) {
                  await new Promise((r) => setTimeout(r, 1500))
                  const s = await adminApi.get(`/jobs/${j.id}`)
                  if (['succeeded', 'failed', 'canceled'].includes(s.status)) { s.status === 'succeeded' ? toast.success('评测完成') : toast.error(s.error || '评测失败'); break }
                }
                ainv(['/evals'])
              } finally {
                setRunning('')
              }
            }}>运行评测</Button></>}>
              {e.lastRun?.ranAt ? (
                <div className="grid grid-cols-2 gap-6 sm:grid-cols-4">
                  <div><div className="text-xs text-muted">说话人准确率</div><div className="mt-1 text-[22px] font-semibold tnum">{num(e.lastRun.speakerAccuracy)}%</div></div>
                  <div><div className="text-xs text-muted">类型准确率</div><div className="mt-1 text-[22px] font-semibold tnum">{num(e.lastRun.typeAccuracy)}%</div></div>
                  <div><div className="text-xs text-muted">情绪准确率</div><div className="mt-1 text-[22px] font-semibold tnum">{num(e.lastRun.emotionAccuracy)}%</div></div>
                  <div><div className="text-xs text-muted">最近运行</div><div className="mt-1 text-[13px]">{e.lastRun.model}<div className="text-xs text-faint">{dateTime(e.lastRun.ranAt)}</div></div></div>
                </div>
              ) : <p className="text-[13px] text-faint">还没有运行过</p>}
              {(e.history ?? []).length > 1 && (
                <Table className="mt-4">
                  <thead><tr><th>时间</th><th>模型</th><th>提示词</th><th className="!text-right">说话人</th><th className="!text-right">类型</th><th className="!text-right">情绪</th></tr></thead>
                  <tbody>{e.history.slice(-8).reverse().map((h: any, i: number) => <tr key={i}><td className="text-xs text-faint">{dateTime(h.ranAt)}</td><td className="font-mono text-xs">{h.model}</td><td>{h.promptVersion ? `v${h.promptVersion}` : '默认'}</td><td className="text-right tnum">{num(h.speakerAccuracy)}%</td><td className="text-right tnum">{num(h.typeAccuracy)}%</td><td className="text-right tnum">{num(h.emotionAccuracy)}%</td></tr>)}</tbody>
                </Table>
              )}
              {e.lastRun?.failures?.length > 0 && (
                <details className="mt-4 text-[12.5px]"><summary className="cursor-pointer text-muted">查看 {e.lastRun.failures.length} 条错误</summary>
                  <ul className="mt-2 space-y-1">{e.lastRun.failures.slice(0, 30).map((x: any, i: number) => <li key={i} className="text-muted">{x.text ?? JSON.stringify(x)}{x.expected && <> — 期望 <span className="text-ok">{JSON.stringify(x.expected)}</span>，实际 <span className="text-danger">{JSON.stringify(x.actual)}</span></>}</li>)}</ul>
                </details>
              )}
            </Panel>
          ))}
        </div>
      )}
      <EvalEditor value={edit} onClose={() => setEdit(null)} />
    </div>
  )
}

function EvalEditor({ value, onClose }: { value: any; onClose: () => void }) {
  const [name, setName] = useState('')
  const [json, setJson] = useState('')
  useEffect(() => {
    if (!value) return
    setName(value.name)
    setJson(JSON.stringify(value.items?.length ? value.items : [{ text: '夜色渐深，她推开窗。\n“你来做什么？”林黛玉冷冷地问。', characters: [{ name: '林黛玉', aliases: ['黛玉'], gender: 'female' }], expected: [{ index: 0, speaker: '旁白', type: 'narration', emotion: 'neutral' }, { index: 1, speaker: '林黛玉', type: 'dialogue', emotion: 'angry' }] }], null, 2))
  }, [value])
  return (
    <Sheet open={!!value} onOpenChange={(o) => !o && onClose()} title={value?.id ? '编辑评测集' : '新建评测集'} width={640} footer={<>
      {value?.id && <Button variant="ghost" className="mr-auto text-danger" onClick={async () => { await adminApi.del(`/evals/${value.id}`); ainv(['/evals']); onClose() }}>删除</Button>}
      <Button onClick={onClose}>取消</Button>
      <Button variant="primary" onClick={async () => {
        let items
        try { items = JSON.parse(json) } catch { return toast.error('样例不是合法的 JSON') }
        const body = { name, kind: 'script_annotation', items }
        if (value.id) await adminApi.put(`/evals/${value.id}`, { ...value, ...body })
        else await adminApi.post('/evals', body)
        ainv(['/evals'])
        onClose()
      }}>保存</Button>
    </>}>
      <div className="space-y-4">
        <Field label="名称"><Input value={name} onChange={(e) => setName(e.target.value)} /></Field>
        <Field label="样例（JSON）" hint="text 为原文；expected 按拆分后的句子序号给出期望的说话人、类型和情绪"><Textarea rows={22} value={json} onChange={(e) => setJson(e.target.value)} className="font-mono text-[12px]" /></Field>
      </div>
    </Sheet>
  )
}

export { Checkbox, pct, useMemo }
