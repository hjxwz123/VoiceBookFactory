import { Check, ChevronDown, Plus, Trash2, X } from '@/components/icons'
import { useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import { adminApi } from '@/lib/api'
import { bytes } from '@/lib/format'
import { Badge, Button, Collapse, Copyable, Field, Input, Menu, Panel, Progress, Segmented, Select, SuggestInput, Switch, cn, confirm } from '@/components/ui'
import { ainv, useAdmin } from '../kit'

// ---------------------------------------------------------------- file storage

const REGIONS: [string, string][] = [
  ['cn-hangzhou', '华东1（杭州）'], ['cn-shanghai', '华东2（上海）'], ['cn-nanjing', '华东5（南京）'], ['cn-fuzhou', '华东6（福州）'],
  ['cn-qingdao', '华北1（青岛）'], ['cn-beijing', '华北2（北京）'], ['cn-zhangjiakou', '华北3（张家口）'], ['cn-huhehaote', '华北5（呼和浩特）'], ['cn-wulanchabu', '华北6（乌兰察布）'],
  ['cn-shenzhen', '华南1（深圳）'], ['cn-heyuan', '华南2（河源）'], ['cn-guangzhou', '华南3（广州）'], ['cn-chengdu', '西南1（成都）'], ['cn-hongkong', '中国香港'],
  ['ap-southeast-1', '新加坡'], ['ap-northeast-1', '日本（东京）'], ['us-west-1', '美国（硅谷）'], ['us-east-1', '美国（弗吉尼亚）'], ['eu-central-1', '德国（法兰克福）'],
]
const regionLabel = (r: string) => REGIONS.find(([k]) => k === r)?.[1]

type Step = { name: string; ok: boolean; message?: string }

export function StorageSettings() {
  const q = useAdmin<any>('/settings/storage', {}, { refetch: 0 })
  const [f, setF] = useState<any>(null)
  const [secret, setSecret] = useState('')
  const [steps, setSteps] = useState<Step[] | null>(null)
  const [busy, setBusy] = useState<'' | 'check' | 'save' | 'sync'>('')
  const [advanced, setAdvanced] = useState(false)
  const saved = q.data?.value
  const status = q.data?.status
  useEffect(() => { if (saved && !f) setF(structuredClone(saved)) }, [saved])
  // poll while existing files are being uploaded
  useEffect(() => {
    if (!status?.sync?.running) return
    const t = setInterval(() => q.refetch(), 1000)
    return () => clearInterval(t)
  }, [status?.sync?.running])
  if (!f) return null
  const o = f.oss
  const setO = (k: string, v: any) => { setF({ ...f, oss: { ...o, [k]: v } }); setSteps(null) }
  const body = () => ({ ...f, secret })
  const oss = f.driver === 'oss'
  const leavingOss = saved.driver === 'oss' && !oss
  const bucketChanged = saved.oss.bucket && o.bucket !== saved.oss.bucket && status?.remoteFiles > 0

  const check = async () => {
    setBusy('check')
    try {
      const r = await adminApi.post('/settings/storage/check', body())
      setSteps(r.steps)
      r.ok ? toast.success('连接正常') : toast.error('连接测试没有通过')
    } catch (e: any) {
      toast.error(e.message)
    } finally {
      setBusy('')
    }
  }
  const save = async () => {
    setBusy('save')
    try {
      await adminApi.put('/settings/storage', body())
      setSecret('')
      setSteps(null)
      await q.refetch()
      ainv(['/settings/storage'])
      toast.success(oss ? '已保存，新文件会上传到阿里云 OSS' : '已保存，新文件存放在本机')
    } catch (e: any) {
      toast.error(e.message, { duration: 8000 })
    } finally {
      setBusy('')
    }
  }
  const sync = async () => {
    setBusy('sync')
    try {
      await adminApi.post('/settings/storage/sync', {})
      await q.refetch()
    } catch (e: any) {
      toast.error(e.message)
    } finally {
      setBusy('')
    }
  }
  const s = status?.sync

  return (
    <div className="space-y-6">
      <Panel title="存储位置" description="用户上传的书稿、录音、封面、头像，以及生成的配音、成品和导出文件，都保存在这里。">
        <Segmented value={f.driver} onChange={(v) => { setF({ ...f, driver: v }); setSteps(null) }} items={[{ value: 'local', label: '本机磁盘' }, { value: 'oss', label: '阿里云 OSS' }]} />
        <p className="mt-3 text-[13px] leading-relaxed text-muted">
          {oss
            ? '文件先写到本机，再上传到你的 OSS 存储桶，以 OSS 里的为准。本机只留最近用到的副本，需要时自动从 OSS 取回。'
            : '文件保存在服务器的数据目录（data/storage）。磁盘空间不够或想做异地备份时，可以改用阿里云 OSS。'}
        </p>
        {leavingOss && <p className="mt-3 rounded-md bg-warn-soft px-3 py-2 text-[13px] text-ink">切回本机后，新文件只存在服务器上；已经在 OSS 里的文件仍从 OSS 读取，所以请保留下面的 AccessKey，也不要清空存储桶。</p>}

        <Collapse open={oss || leavingOss}>
          <div className="mt-5 grid gap-4 sm:grid-cols-2">
            <Field label="地域" hint={regionLabel(o.region) ?? '存储桶所在的地域 ID，在 OSS 控制台的存储桶概览里能看到'}>
              <SuggestInput value={o.region ?? ''} onChange={(v) => setO('region', v.trim())} suggestions={REGIONS.map(([k]) => k)} placeholder="cn-hangzhou" />
            </Field>
            <Field label="存储桶名称" hint={bucketChanged ? `换存储桶后，旧桶里的 ${status.remoteFiles} 个文件不会自动搬过去，需要你在 OSS 控制台复制` : '小写字母、数字和短横线'}>
              <Input value={o.bucket ?? ''} onChange={(e) => setO('bucket', e.target.value.trim())} placeholder="my-audiobooks" autoComplete="off" />
            </Field>
            <Field label="AccessKey ID" hint="建议用只授权了这个存储桶读写权限的 RAM 用户">
              <Input value={o.accessKeyId ?? ''} onChange={(e) => setO('accessKeyId', e.target.value.trim())} autoComplete="off" />
            </Field>
            <Field label="AccessKey Secret" hint={q.data?.hasSecret && o.accessKeyId === saved.oss.accessKeyId ? '已保存，留空则不修改' : '加密保存，不会再显示'}>
              <Input type="password" value={secret} onChange={(e) => { setSecret(e.target.value); setSteps(null) }} autoComplete="new-password" />
            </Field>
            <Field label="访问方式" className="sm:col-span-2" hint={f.serveMode === 'proxy' ? '浏览器从本站下载，本站再去 OSS 取，会占用服务器带宽。' : '浏览器拿到 1 小时有效的签名链接，直接从 OSS 下载，省服务器带宽。存储桶保持私有即可。'}>
              <Segmented value={f.serveMode} onChange={(v) => { setF({ ...f, serveMode: v }); setSteps(null) }} items={[{ value: 'redirect', label: '浏览器直连 OSS（推荐）' }, { value: 'proxy', label: '经服务器转发' }]} />
            </Field>
            <Field label="本机副本保留天数" hint="已在 OSS 的文件，本机副本这么多天没用到就删掉，用到时再取回。0 表示一直保留。">
              <Input type="number" min={0} value={f.keepLocalDays} onChange={(e) => setF({ ...f, keepLocalDays: Number(e.target.value) })} className="w-32" />
            </Field>
          </div>
          <button type="button" onClick={() => setAdvanced(!advanced)} className="mt-5 inline-flex items-center gap-1 text-[13px] text-muted hover:text-ink">
            <ChevronDown className={cn('size-3.5 transition-transform', advanced && 'rotate-180')} />更多选项
          </button>
          <Collapse open={advanced}>
            <div className="mt-4 grid gap-4 sm:grid-cols-2">
              <Field label="内网传输" hint="服务器是同地域的阿里云 ECS 时打开：上传下载走内网，不产生外网流量费。">
                <Switch checked={!!o.internal} onChange={(v) => setO('internal', v)} label={o.internal ? '已开启' : '未开启'} />
              </Field>
              <Field label="自定义域名" hint="绑定到存储桶的域名或 CDN 加速域名，浏览器下载时用它。CDN 需开启私有 Bucket 回源。">
                <Input value={o.customDomain ?? ''} onChange={(e) => setO('customDomain', e.target.value.trim())} placeholder="https://media.example.com" />
              </Field>
              <Field label="路径前缀" hint="和其他程序共用一个存储桶时，用前缀把文件分开">
                <Input value={o.prefix ?? ''} onChange={(e) => setO('prefix', e.target.value.trim())} placeholder="voicebook/" />
              </Field>
              <Field label="Endpoint" hint="留空时按地域自动生成，例如 oss-cn-hangzhou.aliyuncs.com">
                <Input value={o.endpoint ?? ''} onChange={(e) => setO('endpoint', e.target.value.trim())} placeholder="自动" />
              </Field>
            </div>
          </Collapse>

          {steps && (
            <ol className="mt-5 space-y-1.5 rounded-md bg-sunken px-4 py-3 text-[13px]">
              {steps.map((st) => (
                <li key={st.name} className="flex gap-2">
                  {st.ok ? <Check className="mt-0.5 size-4 shrink-0 text-ok" /> : <X className="mt-0.5 size-4 shrink-0 text-danger" />}
                  <span><span className="font-medium">{st.name}</span>{st.message && <span className={st.ok ? 'text-muted' : 'text-danger'}>：{st.message}</span>}</span>
                </li>
              ))}
            </ol>
          )}
        </Collapse>

        <div className="mt-6 flex items-center justify-end gap-2">
          {(oss || leavingOss) && <Button onClick={check} loading={busy === 'check'} disabled={!o.bucket || !o.accessKeyId}>测试连接</Button>}
          <Button variant="primary" onClick={save} loading={busy === 'save'}>{oss && saved.driver !== 'oss' ? '测试并保存' : '保存'}</Button>
        </div>
      </Panel>

      {status && (
        <Panel title="文件分布">
          <div className="grid gap-6 sm:grid-cols-3">
            <div>
              <div className="text-xs text-muted">本机</div>
              <div className="mt-1 text-[20px] font-semibold tabular-nums">{bytes(status.localBytes)}</div>
              <div className="text-xs text-faint">{status.localFiles} 个文件</div>
            </div>
            <div>
              <div className="text-xs text-muted">阿里云 OSS</div>
              <div className="mt-1 text-[20px] font-semibold tabular-nums">{bytes(status.remoteBytes)}</div>
              <div className="text-xs text-faint">{status.remoteFiles} 个文件</div>
            </div>
            <div>
              <div className="text-xs text-muted">只在本机、还没上传</div>
              <div className="mt-1 text-[20px] font-semibold tabular-nums">{status.active ? status.unsyncedFiles : '—'}</div>
              <div className="text-xs text-faint">{status.active ? bytes(status.unsyncedBytes) : '开启 OSS 后显示'}</div>
            </div>
          </div>
          {status.active && (s?.running || status.unsyncedFiles > 0 || s?.finishedAt) && (
            <div className="mt-6 border-t border-line pt-5">
              {s?.running ? (
                <>
                  <div className="mb-2 flex justify-between text-[13px]"><span>正在上传已有文件…</span><span className="tabular-nums text-muted">{s.done} / {s.total}</span></div>
                  <Progress value={s.total ? (s.done / s.total) * 100 : 0} />
                </>
              ) : (
                <div className="flex flex-wrap items-center justify-between gap-3 text-[13px]">
                  <span className="text-muted">
                    {s?.finishedAt ? `上一轮上传了 ${s.done - s.failed} 个文件（${bytes(s.bytes)}）${s.failed ? `，${s.failed} 个失败：${s.lastError}` : ''}。` : ''}
                    {status.unsyncedFiles > 0 ? `开启 OSS 之前保存的 ${status.unsyncedFiles} 个文件还只在本机。` : '所有文件都已在 OSS。'}
                  </span>
                  {status.unsyncedFiles > 0 && <Button onClick={sync} loading={busy === 'sync'}>上传到 OSS</Button>}
                </div>
              )}
            </div>
          )}
        </Panel>
      )}
    </div>
  )
}

// ---------------------------------------------------------------- third-party sign-in

type Preset = { key: string; name: string; protocol: 'oidc' | 'oauth2' | 'wechat'; issuer?: string; authUrl?: string; tokenUrl?: string; userInfoUrl?: string; scopes?: string; authMethod?: string; fieldId?: string; fieldEmail?: string; fieldName?: string; fieldAvatar?: string; console: string }
type Provider = { id: string; preset: string; name: string; enabled: boolean; clientId: string; secret?: string; hasSecret?: boolean; callbackUrl?: string; issuer: string; authUrl: string; tokenUrl: string; userInfoUrl: string; scopes: string; authMethod: string; fieldId: string; fieldEmail: string; fieldName: string; fieldAvatar: string; allowSignup: boolean; _new?: boolean; _k?: string }

const GENERIC = new Set(['oidc', 'oauth2'])

export function OAuthSettings() {
  const q = useAdmin<any>('/settings/oauth')
  const [list, setList] = useState<Provider[] | null>(null)
  const [open, setOpen] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const presets: Preset[] = q.data?.presets ?? []
  const load = (ps: Provider[]) => setList(ps.map((p) => ({ ...p, secret: '', _k: p.id })))
  useEffect(() => { if (q.data && !list) load(q.data.value.providers) }, [q.data])
  const base = q.data?.callbackBase ?? ''
  const dirty = useMemo(() => JSON.stringify(list?.map(({ secret, _k, ...p }) => ({ ...p, s: !!secret }))) !== JSON.stringify(q.data?.value.providers.map((p: Provider) => ({ ...p, s: false }))), [list, q.data])
  if (!list) return null

  const add = (pr: Preset) => {
    let id = pr.key
    for (let i = 2; list.some((p) => p.id === id); i++) id = `${pr.key}${i}`
    const p: Provider = { id, preset: pr.key, name: GENERIC.has(pr.key) ? '' : pr.name, enabled: true, clientId: '', secret: '', issuer: '', authUrl: '', tokenUrl: '', userInfoUrl: '', scopes: '', authMethod: '', fieldId: '', fieldEmail: '', fieldName: '', fieldAvatar: '', allowSignup: true, _new: true, _k: `new-${Date.now()}` }
    setList([...list, p])
    setOpen(p._k!)
  }
  const update = (i: number, patch: Partial<Provider>) => setList(list.map((p, j) => (j === i ? { ...p, ...patch } : p)))
  const save = async () => {
    setBusy(true)
    try {
      await adminApi.put('/settings/oauth', { providers: list.map(({ _new, _k, hasSecret, callbackUrl, ...p }) => p) })
      const fresh = await q.refetch()
      load(fresh.data.value.providers)
      setOpen(null)
      ainv(['/settings/oauth'])
      toast.success('已保存，登录页会显示启用的登录方式')
    } catch (e: any) {
      toast.error(e.message, { duration: 8000 })
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-6">
      <Panel
        title="第三方登录"
        description="按标准 OAuth 2.0 / OpenID Connect 接入：授权码模式加 PKCE，登录状态绑定浏览器，OpenID Connect 的身份令牌会验签并核对签发方、受众和 nonce。"
        actions={
          <Menu
            trigger={<Button size="sm" icon={<Plus className="size-3.5" />}>添加登录方式</Button>}
            items={presets.map((pr) => ({ label: pr.name, hint: pr.protocol === 'oidc' ? 'OIDC' : pr.protocol === 'oauth2' ? 'OAuth 2.0' : undefined, onSelect: () => add(pr) }))}
          />
        }
        padded={false}
      >
        {list.length === 0 && <p className="px-5 py-10 text-center text-[13px] text-muted">还没有接入第三方登录。点右上角「添加登录方式」，选择 GitHub、Google、微信，或任何支持 OpenID Connect 的身份服务。</p>}
        <ul>
          {list.map((p, i) => {
            const pr = presets.find((x) => x.key === p.preset) ?? presets[presets.length - 1]
            const label = p.name || pr?.name
            const expanded = open === p._k
            const cb = base + p.id + '/callback'
            return (
              <li key={p._k} className="border-t border-line first:border-t-0">
                <div className="flex items-center gap-3 px-5 py-3.5">
                  <button type="button" onClick={() => setOpen(expanded ? null : p._k!)} className="flex min-w-0 flex-1 items-center gap-2 text-left">
                    <ChevronDown className={cn('size-4 shrink-0 text-faint transition-transform', !expanded && '-rotate-90')} />
                    <span className="truncate text-[14px] font-medium">{label || '未命名'}</span>
                    <Badge>{pr?.protocol === 'oidc' ? 'OpenID Connect' : pr?.protocol === 'wechat' ? '微信开放平台' : 'OAuth 2.0'}</Badge>
                    {p._new && <Badge tone="accent">未保存</Badge>}
                    {!p._new && p.enabled && !p.hasSecret && <Badge tone="warn">缺少密钥</Badge>}
                  </button>
                  <Switch checked={p.enabled} onChange={(v) => update(i, { enabled: v })} label={p.enabled ? '启用' : '停用'} />
                </div>
                <Collapse open={expanded}>
                  <ProviderForm p={p} pr={pr} callback={cb} onChange={(patch) => update(i, patch)} onRemove={async () => {
                    if (!p._new && !(await confirm({ title: `删除「${label}」登录方式？`, description: '已经绑定它的用户不能再用它登录，需要改用邮箱、手机号或其他方式。保存后生效。', danger: true, confirmText: '删除' }))) return
                    setList(list.filter((_, j) => j !== i))
                  }} />
                </Collapse>
              </li>
            )
          })}
        </ul>
      </Panel>
      <div className="flex items-center justify-between gap-4">
        <p className="text-[12.5px] leading-relaxed text-muted">没有账号的人第一次用第三方登录时，要先在登录页同意协议。邮箱已注册过的人不会被自动关联，要先登录原账号，再到「账号设置」里绑定。</p>
        <Button variant="primary" onClick={save} loading={busy} disabled={!dirty}>保存</Button>
      </div>
    </div>
  )
}

function ProviderForm({ p, pr, callback, onChange, onRemove }: { p: Provider; pr: Preset; callback: string; onChange: (patch: Partial<Provider>) => void; onRemove: () => void }) {
  const [disc, setDisc] = useState<any>(null)
  const [checking, setChecking] = useState(false)
  const proto = pr?.protocol
  const generic = GENERIC.has(p.preset)
  const wechat = proto === 'wechat'
  const set = (k: keyof Provider) => (e: React.ChangeEvent<HTMLInputElement>) => onChange({ [k]: e.target.value } as any)
  const discover = async () => {
    setChecking(true)
    try {
      setDisc(await adminApi.post('/settings/oauth/discover', { issuer: p.issuer || pr.issuer }))
    } catch (e: any) {
      setDisc(null)
      toast.error(e.message)
    } finally {
      setChecking(false)
    }
  }
  return (
    <div className="space-y-5 border-t border-line bg-sunken/40 px-5 py-5">
      <p className="text-[13px] leading-relaxed text-muted">{pr?.console}</p>
      <div>
        <div className="mb-1.5 text-[13px] font-medium">回调地址</div>
        <Copyable text={callback} className="font-mono" />
        <p className="mt-1 text-xs text-faint">{wechat ? '在微信开放平台把「授权回调域」设为本站域名。' : '在服务商那边创建应用时，把这个地址填到回调地址（Redirect URI）。'}</p>
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="按钮上的名称">
          <Input value={p.name} onChange={set('name')} placeholder={generic ? '例如：公司账号' : pr?.name} />
        </Field>
        <Field label="标识" hint={p._new ? '用在回调地址里，小写字母、数字和短横线' : '改了以后，服务商那边的回调地址也要跟着改'}>
          <Input value={p.id} onChange={(e) => onChange({ id: e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '') })} />
        </Field>
        <Field label={wechat ? 'AppID' : 'Client ID'}>
          <Input value={p.clientId} onChange={set('clientId')} autoComplete="off" />
        </Field>
        <Field label={wechat ? 'AppSecret' : 'Client Secret'} hint={p.hasSecret ? '已保存，留空则不修改' : '加密保存，不会再显示'}>
          <Input type="password" value={p.secret ?? ''} onChange={set('secret')} autoComplete="new-password" />
        </Field>
        {proto === 'oidc' && (
          <Field label="签发方（Issuer）" className="sm:col-span-2" hint={disc ? `已找到：授权 ${disc.authorization_endpoint} · 令牌 ${disc.token_endpoint}${disc.userinfo_endpoint ? ' · 用户信息 ' + disc.userinfo_endpoint : ''}` : '其余地址从 Issuer 下的 /.well-known/openid-configuration 自动读取'}>
            <div className="flex gap-2">
              <Input value={p.issuer} onChange={(e) => { onChange({ issuer: e.target.value.trim() }); setDisc(null) }} placeholder={pr?.issuer || 'https://login.example.com'} />
              <Button type="button" onClick={discover} loading={checking} disabled={!(p.issuer || pr?.issuer)} className="shrink-0">检测</Button>
            </div>
          </Field>
        )}
        {proto === 'oauth2' && generic && (
          <>
            <Field label="授权地址"><Input value={p.authUrl} onChange={set('authUrl')} placeholder="https://example.com/oauth/authorize" /></Field>
            <Field label="令牌地址"><Input value={p.tokenUrl} onChange={set('tokenUrl')} placeholder="https://example.com/oauth/token" /></Field>
            <Field label="用户信息地址" className="sm:col-span-2"><Input value={p.userInfoUrl} onChange={set('userInfoUrl')} placeholder="https://example.com/api/user" /></Field>
            <Field label="用户 ID 字段" hint="用户信息 JSON 里的路径，多个用逗号分隔，依次尝试"><Input value={p.fieldId} onChange={set('fieldId')} placeholder={pr.fieldId} /></Field>
            <Field label="邮箱字段" hint="第三方未确认的邮箱不会写进账号"><Input value={p.fieldEmail} onChange={set('fieldEmail')} placeholder={pr.fieldEmail} /></Field>
            <Field label="昵称字段"><Input value={p.fieldName} onChange={set('fieldName')} placeholder={pr.fieldName} /></Field>
            <Field label="头像字段"><Input value={p.fieldAvatar} onChange={set('fieldAvatar')} placeholder={pr.fieldAvatar} /></Field>
          </>
        )}
        {!wechat && (
          <Field label="授权范围（scope）" hint={generic ? '用空格分隔' : '一般不用改'}>
            <Input value={p.scopes} onChange={set('scopes')} placeholder={pr?.scopes || '按服务商要求填写'} />
          </Field>
        )}
        {!wechat && (generic || p.authMethod) && (
          <Field label="客户端认证方式" hint="令牌接口怎样核对 Client Secret">
            <Select value={p.authMethod} onChange={(v) => onChange({ authMethod: v })} options={[['', proto === 'oidc' ? '自动（按服务商的配置）' : `默认（${pr?.authMethod ?? 'client_secret_basic'}）`], ['client_secret_basic', 'HTTP Basic（client_secret_basic）'], ['client_secret_post', '表单参数（client_secret_post）']]} />
          </Field>
        )}
      </div>
      <Switch checked={p.allowSignup} onChange={(v) => onChange({ allowSignup: v })} label="没有账号的人可以用它直接注册（关闭后只能登录已绑定的账号）" />
      <div className="flex justify-end">
        <Button size="sm" variant="ghost" className="text-danger" icon={<Trash2 className="size-3.5" />} onClick={onRemove}>删除这个登录方式</Button>
      </div>
    </div>
  )
}
