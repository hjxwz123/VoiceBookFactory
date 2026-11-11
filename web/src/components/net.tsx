import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { net } from '@/lib/api'
import { confirm } from './ui'

/**
 * Global feedback for anything waiting on the network:
 * - a thin bar at the top while a change is being saved (shown after 250 ms so fast saves don't flash);
 * - a "slow network" notice when a save takes longer than 6 s, so people don't click again;
 * - offline / back-online notices.
 */
export function NetActivity() {
  const [pending, setPending] = useState(net.pending)
  const [visible, setVisible] = useState(false)
  useEffect(() => net.subscribe(setPending), [])
  useEffect(() => {
    if (pending === 0) {
      setVisible(false)
      toast.dismiss('net-slow')
      return
    }
    const show = setTimeout(() => setVisible(true), 250)
    const slow = setTimeout(() => toast.loading('网络较慢，正在保存…', { id: 'net-slow', description: '请稍候，不用重复点击，完成后会自动更新。' }), 6000)
    return () => {
      clearTimeout(show)
      clearTimeout(slow)
    }
  }, [pending > 0])
  useEffect(() => {
    // closing or reloading the tab while a change is still on its way would lose it: ask first
    const guard = (e: BeforeUnloadEvent) => {
      if (net.pending > 0) {
        e.preventDefault()
        e.returnValue = ''
      }
    }
    window.addEventListener('beforeunload', guard)
    return () => window.removeEventListener('beforeunload', guard)
  }, [])
  useEffect(() => {
    const off = () => toast.error('网络已断开', { id: 'net-offline', description: '恢复连接前的修改可能无法保存。', duration: Infinity })
    const on = () => {
      toast.dismiss('net-offline')
      toast.success('网络已恢复', { id: 'net-online', duration: 2500 })
    }
    if (navigator.onLine === false) off()
    window.addEventListener('offline', off)
    window.addEventListener('online', on)
    return () => {
      window.removeEventListener('offline', off)
      window.removeEventListener('online', on)
    }
  }, [])
  return (
    <div aria-hidden className="netbar pointer-events-none fixed inset-x-0 top-0 z-[200] h-[2px] overflow-hidden transition-opacity duration-200" style={{ opacity: visible ? 1 : 0 }}>
      <span className="block h-full w-full origin-left bg-accent" />
    </div>
  )
}

/**
 * Keeps unsaved edits from being lost: asks before closing the tab, and before following an in-app link
 * while `dirty` is true. Works with plain <Link>s (the click is intercepted before the router sees it).
 */
export function useLeaveGuard(dirty: boolean, description = '这一页有修改还没有保存，离开后会丢失。') {
  const nav = useNavigate()
  useEffect(() => {
    if (!dirty) return
    const before = (e: BeforeUnloadEvent) => {
      e.preventDefault()
      e.returnValue = ''
    }
    const click = async (e: MouseEvent) => {
      if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
      const a = (e.target instanceof Element ? e.target.closest('a[href]') : null) as HTMLAnchorElement | null
      if (!a || a.target === '_blank' || a.origin !== location.origin || a.hasAttribute('download')) return
      if (a.pathname === location.pathname && a.search === location.search) return
      e.preventDefault()
      e.stopPropagation()
      if (await confirm({ title: '还有修改没保存', description, confirmText: '不保存，离开', danger: true })) nav(a.pathname + a.search + a.hash)
    }
    window.addEventListener('beforeunload', before)
    document.addEventListener('click', click, true)
    return () => {
      window.removeEventListener('beforeunload', before)
      document.removeEventListener('click', click, true)
    }
  }, [dirty, description])
}
