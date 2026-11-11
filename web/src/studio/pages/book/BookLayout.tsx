import { motion } from 'framer-motion'
import { ChevronLeft } from '@/components/icons'
import { lazy, Suspense } from 'react'
import { Link, NavLink, Navigate, Route, Routes, useLocation, useParams } from 'react-router-dom'
import { bookStatusName, modeName, tierName } from '@/lib/format'
import { Badge, cn, Fade, Skeleton } from '@/components/ui'
import { useBook } from '../../hooks'
import { BookCover } from '../Books'

const Overview = lazy(() => import('./Overview'))
const Chapters = lazy(() => import('./Chapters'))
const Characters = lazy(() => import('./Characters'))
const Lexicon = lazy(() => import('./Lexicon'))
const Crew = lazy(() => import('./Crew'))
const Review = lazy(() => import('./Review'))
const Deliver = lazy(() => import('./Deliver'))
const BookSettings = lazy(() => import('./BookSettings'))

const tabs = [
  ['', '概览'],
  ['chapters', '章节'],
  ['characters', '角色与选角'],
  ['lexicon', '读音'],
  ['review', '审听与工单'],
  ['deliver', '导出与发布'],
  ['crew', '成员'],
  ['settings', '设置'],
]

export default function BookLayout() {
  const { bid } = useParams()
  const { data: book, isLoading, error } = useBook(bid)
  const loc = useLocation()
  const sub = loc.pathname.split('/')[3] ?? ''
  if (error) return <Navigate to="/books" replace />
  return (
    <div>
      <Link to="/books" className="mb-4 inline-flex items-center gap-1 text-[13px] text-muted hover:text-ink">
        <ChevronLeft className="size-4" /> 全部图书
      </Link>
      {isLoading || !book ? (
        <div className="flex gap-5">
          <Skeleton className="h-24 w-[72px]" />
          <div className="flex-1 space-y-3"><Skeleton className="h-7 w-64" /><Skeleton className="w-40" /></div>
        </div>
      ) : (
        <div className="flex items-start gap-5">
          <BookCover book={book} className="w-[72px] shrink-0" />
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="truncate font-serif text-[26px] font-semibold tracking-[0.02em]">{book.title}</h1>
              <Badge tone={book.status === 'producing' ? 'accent' : book.status === 'completed' || book.status === 'published' ? 'ok' : 'neutral'}>{bookStatusName[book.status]}</Badge>
            </div>
            <p className="mt-1 text-[13px] text-muted">
              {[book.author, modeName[book.mode], tierName[book.settings?.qualityTier] + '档', book.genre, `${book.wordCount?.toLocaleString()} 字`].filter(Boolean).join(' · ')}
            </p>
          </div>
        </div>
      )}
      <nav className="no-scrollbar mt-6 flex gap-1 overflow-x-auto overflow-y-hidden border-b border-line">
        {tabs.map(([to, label]) => (
          <NavLink key={to} to={`/books/${bid}${to ? '/' + to : ''}`} end className={cn('relative shrink-0 px-3 pb-2.5 pt-1 text-[13.5px] transition-colors', sub === to ? 'font-medium text-ink' : 'text-muted hover:text-ink')}>
            {label}
            {sub === to && <motion.span layoutId="book-tab" className="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-ink" transition={{ type: 'spring', stiffness: 500, damping: 40 }} />}
          </NavLink>
        ))}
      </nav>
      <div className="pt-6">
        {book && (
          <Suspense fallback={<Skeleton className="h-40" />}>
            <Fade k={sub}>
              <Routes>
                <Route index element={<Overview book={book} />} />
                <Route path="chapters" element={<Chapters book={book} />} />
                <Route path="characters" element={<Characters book={book} />} />
                <Route path="lexicon" element={<Lexicon book={book} />} />
                <Route path="crew" element={<Crew book={book} />} />
                <Route path="review" element={<Review book={book} />} />
                <Route path="deliver" element={<Deliver book={book} />} />
                <Route path="settings" element={<BookSettings book={book} />} />
                <Route path="*" element={<Navigate to={`/books/${bid}`} replace />} />
              </Routes>
            </Fade>
          </Suspense>
        )}
      </div>
    </div>
  )
}
