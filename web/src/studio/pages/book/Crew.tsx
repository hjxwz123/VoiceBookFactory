import { useQuery } from '@tanstack/react-query'
import { UserPlus, X } from '@/components/icons'
import { useState } from 'react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { positionName, roleName } from '@/lib/format'
import { Avatar, Badge, Button, Checkbox, Chip, confirm, Dialog, Empty, Field, IconButton, Panel, Select } from '@/components/ui'
import { can, invalidate, useMembers } from '../../hooks'

export default function Crew({ book }: { book: any }) {
  const { data } = useQuery<any[]>({ queryKey: ['crew', book.id], queryFn: () => api.get(`/books/${book.id}/crew`) })
  const members = useMembers()
  const [add, setAdd] = useState(false)
  const [uid, setUid] = useState('')
  const [pos, setPos] = useState<string[]>(['voice'])
  const manage = can(book, 'manage')
  const save = async (userId: string, positions: string[]) => {
    await api.post(`/books/${book.id}/crew`, { userId, positions })
    invalidate(['crew', book.id])
  }
  const crew = data ?? []
  return (
    <div className="grid gap-6 lg:grid-cols-[1fr_300px]">
      <Panel title="参与这本书的成员" description="成员只能看到被加入的图书。职位决定能做什么：画本可以编辑台词，配音可以录音，后期可以合成，审听可以提工单。" padded={false} actions={manage && <Button size="sm" variant="primary" icon={<UserPlus className="size-4" />} onClick={() => setAdd(true)}>添加成员</Button>}>
        {!crew.length ? (
          <Empty title="还没有成员" />
        ) : (
          <ul>
            {crew.map((m) => (
              <li key={m.userId} className="flex flex-wrap items-center gap-3 border-t border-line px-5 py-3 first:border-t-0">
                <Avatar name={m.nickname} src={m.avatarUrl} size={32} />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2 text-[13.5px] font-medium">{m.nickname}{m.isLead && <Badge tone="accent">负责人</Badge>}<span className="text-xs font-normal text-faint">{roleName[m.studioRole]}</span></div>
                  {m.characters?.length > 0 && <div className="mt-0.5 truncate text-xs text-muted">配音角色：{m.characters.map((c: any) => c.name).join('、')}</div>}
                </div>
                <div className="flex flex-wrap gap-1.5">
                  {['script', 'voice', 'post', 'review'].map((p) => (
                    <Chip key={p} active={m.positions?.includes(p)} onClick={manage ? () => save(m.userId, m.positions?.includes(p) ? m.positions.filter((x: string) => x !== p) : [...(m.positions ?? []), p]) : undefined}>{positionName[p]}</Chip>
                  ))}
                </div>
                {manage && !m.isLead && (
                  <IconButton label="移出这本书" icon={<X className="size-4" />} onClick={async () => {
                    if (await confirm({ title: `把 ${m.nickname} 移出这本书？`, description: '对方将无法再访问这本书，已分配的角色会变成待指派。', danger: true, confirmText: '移出' })) {
                      await api.del(`/books/${book.id}/crew/${m.userId}`)
                      invalidate(['crew', book.id], ['characters', book.id])
                    }
                  }} />
                )}
              </li>
            ))}
          </ul>
        )}
      </Panel>
      <Panel title="职位说明">
        <dl className="space-y-3 text-[13px]">
          {[['负责人', '管理这本书的一切，包括成员和发布。'], ['画本', '编辑台词、标注说话人和情绪、维护读音。'], ['配音', '录制或生成被指派角色的台词。'], ['后期', '编排音乐音效，合成成品。'], ['审听', '试听成品、打点提工单、复核修复。']].map(([k, v]) => (
            <div key={k}><dt className="font-medium">{k}</dt><dd className="mt-0.5 text-muted">{v}</dd></div>
          ))}
        </dl>
      </Panel>
      <Dialog open={add} onOpenChange={setAdd} title="添加成员到这本书" width={460} footer={<><Button onClick={() => setAdd(false)}>取消</Button><Button variant="primary" disabled={!uid || !pos.length} onClick={async () => { await save(uid, pos); setAdd(false); setUid(''); toast.success('已添加') }}>添加</Button></>}>
        <div className="space-y-4">
          <Field label="工作室成员" hint="不在列表里？先到「工作室设置 → 成员」邀请。">
            <Select value={uid} onChange={setUid} placeholder="选择成员" options={(members.data ?? []).filter((m: any) => !crew.find((c) => c.userId === m.userId)).map((m: any) => [m.userId, `${m.nickname}（${m.email}）`] as [string, string])} />
          </Field>
          <Field label="职位">
            <div className="flex flex-wrap gap-4">
              {['script', 'voice', 'post', 'review'].map((p) => <Checkbox key={p} checked={pos.includes(p)} onChange={(v) => setPos(v ? [...pos, p] : pos.filter((x) => x !== p))} label={positionName[p]} />)}
            </div>
          </Field>
        </div>
      </Dialog>
    </div>
  )
}
