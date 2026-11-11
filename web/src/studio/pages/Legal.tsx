import { useQuery } from '@tanstack/react-query'
import ReactMarkdown from 'react-markdown'
import { Link, NavLink, useParams } from 'react-router-dom'
import { ChevronLeft } from '@/components/icons'
import { api } from '@/lib/api'
import { useSession } from '@/lib/session'
import { useLegalDocs } from '@/components/legal'
import { cn, Skeleton } from '@/components/ui'

/** Public page for the user agreement, privacy policy, copyright statement and AI rules. */
export default function LegalPage() {
  const { slug = 'terms' } = useParams()
  const site = useSession((s) => s.site) ?? {}
  const user = useSession((s) => s.user)
  const docs = useLegalDocs()
  const doc = useQuery<any>({ queryKey: ['legal', slug], queryFn: () => api.get(`/legal/${slug}`) })
  return (
    <div className="min-h-full bg-bg">
      <header className="border-b border-line bg-surface">
        <div className="mx-auto flex h-14 max-w-5xl items-center gap-3 px-4 sm:px-6">
          <Link to={user ? '/' : '/login'} className="flex items-center gap-2">
            <img src="/favicon.svg" className="size-7" alt="" />
            <span className="font-semibold">{site.name ?? '声书工坊'}</span>
          </Link>
          <span className="text-faint">/</span>
          <span className="text-[13.5px] text-muted">协议与政策</span>
          <Link to={user ? '/' : '/login'} className="ml-auto inline-flex items-center gap-1 text-[13px] text-muted hover:text-ink"><ChevronLeft className="size-4" />{user ? '返回工作台' : '返回登录'}</Link>
        </div>
      </header>
      <div className="mx-auto grid max-w-5xl gap-8 px-4 py-8 sm:px-6 md:grid-cols-[200px_1fr]">
        <nav className="flex gap-1 overflow-x-auto md:flex-col">
          {(docs.data ?? []).map((d) => (
            <NavLink key={d.slug} to={`/legal/${d.slug}`} className={({ isActive }) => cn('shrink-0 rounded-md px-3 py-2 text-[13.5px] transition-colors', isActive ? 'bg-surface font-medium text-ink shadow-[0_0_0_1px_var(--line)]' : 'text-muted hover:text-ink')}>
              {d.title}
            </NavLink>
          ))}
        </nav>
        <article className="min-w-0 rounded-panel border border-line bg-surface px-5 py-6 sm:px-8 sm:py-8">
          {doc.isLoading ? (
            <div className="space-y-3"><Skeleton className="h-8 w-56" /><Skeleton className="h-4" /><Skeleton className="h-4" /><Skeleton className="h-4 w-2/3" /></div>
          ) : doc.error ? (
            <p className="text-muted">没有找到这份文档。</p>
          ) : (
            <>
              <h1 className="font-serif text-[26px] font-semibold leading-tight tracking-[0.01em]">{doc.data.title}</h1>
              <p className="mt-1 text-xs text-faint">版本 {doc.data.version}</p>
              <div className="prose-help prose-legal mt-6 text-[14px] leading-[1.9]"><ReactMarkdown>{doc.data.body}</ReactMarkdown></div>
            </>
          )}
        </article>
      </div>
    </div>
  )
}
