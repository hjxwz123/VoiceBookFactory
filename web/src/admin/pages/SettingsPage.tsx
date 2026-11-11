import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { adminApi } from '@/lib/api'
import { tierName } from '@/lib/format'
import { Button, Field, Input, PageHeader, Panel, Select, Switch, Table, Tabs, Textarea } from '@/components/ui'
import { ainv, useAdmin } from '../kit'
import { OAuthSettings, StorageSettings } from './IntegrationSettings'

type F = { key: string; label: string; type?: 'text' | 'number' | 'switch' | 'password' | 'select' | 'textarea'; options?: [string, string][]; hint?: string }

const schemas: Record<string, { title: string; description: string; fields: F[] }> = {
  site: {
    title: '站点', description: '名称、注册策略和全局限额。',
    fields: [
      { key: 'name', label: '站点名称' }, { key: 'slogan', label: '标语' }, { key: 'logo', label: 'Logo 地址' }, { key: 'supportEmail', label: '客服邮箱', hint: '协议和隐私政策里的联系方式，也用于接收侵权投诉' }, { key: 'icp', label: 'ICP 备案号' }, { key: 'operator', label: '运营主体', hint: '运营本平台的公司或个人全称，会写进用户协议和隐私政策' },
      { key: 'registrationOpen', label: '开放注册', type: 'switch' }, { key: 'inviteOnly', label: '仅限邀请码注册', type: 'switch' }, { key: 'inviteCode', label: '邀请码' },
      { key: 'signupCredits', label: '注册赠送积分', type: 'number' }, { key: 'maxUploadMb', label: '单文件上传上限（MB）', type: 'number' }, { key: 'maxBookChars', label: '单本书字数上限', type: 'number', hint: '0 表示不限' },
      { key: 'freePreviewsDaily', label: '每日免费试听次数', type: 'number' }, { key: 'speechRate', label: '默认估算语速（字/分钟）', type: 'number' },
    ],
  },
  smtp: {
    title: '邮件', description: '用于发送验证码、邀请和通知邮件。未配置时验证码直接显示在页面上（仅开发模式）。',
    fields: [{ key: 'host', label: 'SMTP 服务器' }, { key: 'port', label: '端口', type: 'number' }, { key: 'user', label: '用户名' }, { key: 'password', label: '密码', type: 'password', hint: '留空则不修改' }, { key: 'from', label: '发件地址' }, { key: 'fromName', label: '发件人名称' }, { key: 'tls', label: '使用 TLS', type: 'switch' }],
  },
  sms: {
    title: '短信', description: '手机号验证码登录。短信平台接口已预留，填写后即可启用。',
    fields: [{ key: 'provider', label: '短信平台', type: 'select', options: [['', '未启用'], ['aliyun', '阿里云'], ['tencent', '腾讯云']] }, { key: 'accessKey', label: 'AccessKey' }, { key: 'secret', label: 'Secret', type: 'password', hint: '留空则不修改' }, { key: 'signName', label: '签名' }, { key: 'templateCode', label: '模板编号' }],
  },
  ai_label: {
    title: 'AI 标识', description: '按《人工智能生成合成内容标识办法》在成品中加入显式和隐式标识。',
    fields: [{ key: 'explicit', label: '在音频中播报 AI 生成提示', type: 'switch' }, { key: 'text', label: '提示语', type: 'textarea' }, { key: 'position', label: '位置', type: 'select', options: [['intro', '片头'], ['outro', '片尾']] }, { key: 'implicit', label: '在文件元数据中写入隐式标识', type: 'switch' }, { key: 'producer', label: '生成服务提供者名称' }],
  },
  moderation: {
    title: '内容审核', description: '导入书稿时的审核策略。',
    fields: [{ key: 'enabled', label: '启用内容审核', type: 'switch' }, { key: 'mode', label: '审核方式', type: 'select', options: [['pre', '先审后发（命中后暂停制作）'], ['post', '先发后审（命中后进入复核队列）']] }, { key: 'useLlm', label: '使用大模型做语义审核（消耗少量成本）', type: 'switch' }],
  },
  billing: {
    title: '计费', description: '平台计费参数。',
    fields: [
      { key: 'defaultPlan', label: '默认会员编码', hint: '没有开通会员的用户使用这个方案，通常是 free' },
      { key: 'trialPlan', label: '新用户试用会员编码', hint: '注册后自动试用的会员，例如 personal；留空表示不试用' },
      { key: 'trialDays', label: '试用天数', type: 'number', hint: '0 表示不试用' },
      { key: 'marketShare', label: '音色市场创作者分成比例', type: 'number', hint: '0~1，例如 0.7 表示创作者拿 70%' },
      { key: 'byokFeePer1k', label: '自带密钥服务费（积分/千字）', type: 'number' },
    ],
  },
}

export default function SettingsPage() {
  const [tab, setTab] = useState('site')
  return (
    <div>
      <PageHeader title="系统设置" />
      <Tabs className="mb-6" value={tab} onChange={setTab} items={[...Object.entries(schemas).map(([k, v]) => ({ value: k, label: v.title })), { value: 'oauth', label: '第三方登录' }, { value: 'storage', label: '文件存储' }, { value: 'export_presets', label: '平台标准' }, { value: 'casting_policy', label: '选角策略' }]} />
      {schemas[tab] && <SchemaForm k={tab} key={tab} />}
      {tab === 'oauth' && <OAuthSettings />}
      {tab === 'storage' && <StorageSettings />}
      {tab === 'export_presets' && <Presets />}
      {tab === 'casting_policy' && <Casting />}
    </div>
  )
}

function useSetting(k: string) {
  const { data } = useAdmin<any>(`/settings/${k}`)
  const save = async (v: any) => {
    await adminApi.put(`/settings/${k}`, v)
    ainv([`/settings/${k}`])
    toast.success('已保存')
  }
  return { value: data?.value, save }
}

function SchemaForm({ k }: { k: string }) {
  const s = schemas[k]
  const { value, save } = useSetting(k)
  const [f, setF] = useState<any>(null)
  const [to, setTo] = useState('')
  useEffect(() => { if (value) setF({ ...value }) }, [value])
  if (!f) return null
  return (
    <Panel title={s.title} description={s.description}>
      <div className="grid gap-4 sm:grid-cols-2">
        {s.fields.map((d) => (
          <Field key={d.key} label={d.type === 'switch' ? undefined : d.label} hint={d.hint} className={d.type === 'textarea' ? 'sm:col-span-2' : ''}>
            {d.type === 'switch' ? <Switch checked={!!f[d.key]} onChange={(v) => setF({ ...f, [d.key]: v })} label={d.label} />
              : d.type === 'select' ? <Select value={f[d.key] ?? ''} onChange={(v) => setF({ ...f, [d.key]: v })} options={d.options!} />
              : d.type === 'textarea' ? <Textarea rows={2} value={f[d.key] ?? ''} onChange={(e) => setF({ ...f, [d.key]: e.target.value })} />
              : <Input type={d.type === 'number' ? 'number' : d.type === 'password' ? 'password' : 'text'} step="any" value={f[d.key] ?? ''} onChange={(e) => setF({ ...f, [d.key]: d.type === 'number' ? Number(e.target.value) : e.target.value })} autoComplete="off" />}
          </Field>
        ))}
      </div>
      <div className="mt-6 flex items-center justify-end gap-2">
        {k === 'smtp' && <><Input value={to} onChange={(e) => setTo(e.target.value)} placeholder="测试收件地址" className="w-56" /><Button disabled={!to} onClick={async () => { try { await adminApi.post('/settings/smtp/test', { to }); toast.success('测试邮件已发送') } catch (e: any) { toast.error(e.message) } }}>发送测试</Button></>}
        <Button variant="primary" onClick={() => save(f)}>保存</Button>
      </div>
    </Panel>
  )
}

function Presets() {
  const { value, save } = useSetting('export_presets')
  const [rows, setRows] = useState<any[] | null>(null)
  useEffect(() => { if (value) setRows(value.map((x: any) => ({ ...x }))) }, [value])
  if (!rows) return null
  const cols: [string, string, string?][] = [['key', '代码'], ['name', '名称'], ['format', '格式'], ['bitrate', '码率', 'n'], ['sampleRate', '采样率', 'n'], ['channels', '声道', 'n'], ['loudnessI', '响度 LUFS', 'n'], ['truePeak', '峰值 dB', 'n'], ['noiseFloorMax', '底噪上限', 'n'], ['headSilenceMin', '片头静音≥', 'n'], ['headSilenceMax', '≤', 'n'], ['tailSilenceMin', '片尾静音≥', 'n'], ['tailSilenceMax', '≤', 'n']]
  return (
    <Panel title="平台标准" description="合成时的响度标准化目标和技术检测阈值。用户在导出和图书设置里选择这些标准。" padded={false}>
      <Table>
        <thead><tr>{cols.map(([, l]) => <th key={l}>{l}</th>)}<th /></tr></thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={i}>
              {cols.map(([k, , t]) => <td key={k} className="!px-1.5"><input value={r[k] ?? ''} onChange={(e) => setRows(rows.map((x, j) => (j === i ? { ...x, [k]: t ? Number(e.target.value) : e.target.value } : x)))} className="h-7 w-full min-w-14 rounded border border-transparent bg-transparent px-1.5 text-[12.5px] hover:border-line focus:border-accent focus:outline-none" /></td>)}
              <td><Button size="xs" variant="ghost" className="text-danger" onClick={() => setRows(rows.filter((_, j) => j !== i))}>删除</Button></td>
            </tr>
          ))}
        </tbody>
      </Table>
      <div className="flex justify-between px-5 py-3">
        <Button size="sm" onClick={() => setRows([...rows, { key: 'custom', name: '新标准', format: 'mp3', bitrate: 192, sampleRate: 44100, channels: 2, loudnessI: -16, truePeak: -1, noiseFloorMax: -55, headSilenceMin: 300, headSilenceMax: 2000, tailSilenceMin: 500, tailSilenceMax: 4000 }])}>添加</Button>
        <Button size="sm" variant="primary" onClick={() => save(rows)}>保存</Button>
      </div>
    </Panel>
  )
}

function Casting() {
  const { value, save } = useSetting('casting_policy')
  const [f, setF] = useState<any>(null)
  useEffect(() => { if (value) setF(JSON.parse(JSON.stringify(value))) }, [value])
  if (!f) return null
  const roles: [string, string][] = [['narrator', '旁白'], ['lead', '主角'], ['supporting', '配角'], ['minor', '次要角色'], ['crowd', '群演']]
  return (
    <Panel title="选角策略" description="图书选择某个质量档后，AI 选角为各类角色挑选哪个价位的音色。" padded={false}>
      <Table>
        <thead><tr><th>图书质量档</th>{roles.map(([, l]) => <th key={l}>{l}</th>)}</tr></thead>
        <tbody>
          {['economy', 'standard', 'premium'].map((tier) => (
            <tr key={tier}>
              <td className="font-medium">{tierName[tier]}</td>
              {roles.map(([k]) => <td key={k}><Select size="sm" value={f[tier]?.[k] ?? 'standard'} onChange={(v) => setF({ ...f, [tier]: { ...f[tier], [k]: v } })} options={Object.entries(tierName)} className="w-24" /></td>)}
            </tr>
          ))}
        </tbody>
      </Table>
      <div className="flex justify-end px-5 py-3"><Button size="sm" variant="primary" onClick={() => save(f)}>保存</Button></div>
    </Panel>
  )
}
