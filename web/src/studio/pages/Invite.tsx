import { useQuery } from '@tanstack/react-query'
import { motion } from 'framer-motion'
import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { positionName, roleName } from '@/lib/format'
import { useSession } from '@/lib/session'
import { Avatar, Button, Spinner } from '@/components/ui'

export default function Invite() {
  const { token } = useParams()
  const nav = useNavigate()
  const user = useSession((s) => s.user)
  const { data, error, isLoading } = useQuery({ queryKey: ['invite', token], queryFn: () => api.get(`/invitations/${token}`), retry: false })
  const [busy, setBusy] = useState(false)
  const accept = async () => {
    setBusy(true)
    try {
      const r = await api.post(`/invitations/${token}/accept`)
      await useSession.getState().load()
      if (r.status === 'pending') {
        toast.success('已申请加入，等待管理员审批')
        nav('/')
      } else {
        useSession.getState().setStudio(r.studioId)
        toast.success(`已加入「${data.studio.name}」`)
        nav(data.book ? `/books/${data.book.id}` : '/')
      }
    } catch (e: any) {
      toast.error(e.message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="grid min-h-full place-items-center bg-bg px-6">
      <motion.div initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} className="w-full max-w-sm rounded-sheet border border-line bg-surface p-8 text-center shadow-[var(--shadow-pop)]">
        {isLoading ? <Spinner className="mx-auto size-6" /> : error ? (
          <>
            <p className="text-[16px] font-semibold">邀请链接无效</p>
            <p className="mt-2 text-[13px] text-muted">{(error as any).message || '链接可能已过期或被撤销，请联系邀请人重新发送。'}</p>
            <Link to="/" className="mt-6 inline-block text-[13px] text-accent">返回首页</Link>
          </>
        ) : (
          <>
            <Avatar name={data.studio.name} src={data.studio.logo} size={56} />
            <p className="mt-4 text-[13px] text-muted">{data.inviter} 邀请你加入</p>
            <h1 className="mt-1 text-[20px] font-semibold">{data.studio.name}</h1>
            {data.book && <p className="mt-1 text-[13px] text-muted">参与制作《{data.book.title}》</p>}
            <p className="mt-3 text-[13px]">身份：{roleName[data.role]}{data.positions?.length > 0 && ` · ${data.positions.map((p: string) => positionName[p]).join('、')}`}</p>
            {user ? (
              <Button variant="primary" size="lg" className="mt-6 w-full" loading={busy} onClick={accept}>接受邀请</Button>
            ) : (
              <div className="mt-6 space-y-2">
                <Button variant="primary" size="lg" className="w-full" onClick={() => nav(`/register?invitation=${token}&next=/invite/${token}`)}>注册并加入</Button>
                <Button size="lg" className="w-full" onClick={() => nav(`/login?next=/invite/${token}`)}>已有账号，登录</Button>
              </div>
            )}
          </>
        )}
      </motion.div>
    </div>
  )
}
