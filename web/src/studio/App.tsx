import React, { lazy, Suspense, useEffect } from 'react'
import { Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { api } from '@/lib/api'
import { useSession } from '@/lib/session'
import { Spinner } from '@/components/ui'
import Shell from './Shell'
import { AuthPage, OAuthDone } from './pages/Auth'
import { LegalGate } from '@/components/legal'

const Dashboard = lazy(() => import('./pages/Dashboard'))
const Books = lazy(() => import('./pages/Books'))
const NewBook = lazy(() => import('./pages/NewBook'))
const BookLayout = lazy(() => import('./pages/book/BookLayout'))
const Workspace = lazy(() => import('./pages/workspace/Workspace'))
const MyLines = lazy(() => import('./pages/MyLines'))
const Voices = lazy(() => import('./pages/Voices'))
const Assets = lazy(() => import('./pages/Assets'))
const AutoBook = lazy(() => import('./pages/AutoBook'))
const Quick = lazy(() => import('./pages/Quick'))
const Production = lazy(() => import('./pages/Production'))
const Jobs = lazy(() => import('./pages/Jobs'))
const StudioSettings = lazy(() => import('./pages/StudioSettings'))
const Account = lazy(() => import('./pages/Account'))
const Membership = lazy(() => import('./pages/Membership'))
const Help = lazy(() => import('./pages/Help'))
const Invite = lazy(() => import('./pages/Invite'))
const Share = lazy(() => import('./pages/Share'))
const LegalPage = lazy(() => import('./pages/Legal'))
const Landing = lazy(() => import('@/landing/Landing'))

export const Loading = () => (
  <div className="grid h-full min-h-60 place-items-center">
    <Spinner className="size-5" />
  </div>
)

function Protected({ children }: { children: React.ReactElement }) {
  const { user, ready } = useSession()
  const loc = useLocation()
  if (!ready) return <Loading />
  // visitors who are not signed in see the product page at the root instead of a login form
  if (!user && loc.pathname === '/') return <Landing />
  if (!user) return <Navigate to={`/login?next=${encodeURIComponent(loc.pathname + loc.search)}`} replace />
  return <>{children}<LegalGate /></>
}

export default function App() {
  const ready = useSession((s) => s.ready)
  useEffect(() => {
    ;(async () => {
      try {
        if (await api.refresh()) await useSession.getState().load()
      } catch {}
      try {
        useSession.setState({ site: await api.get('/site') })
      } catch {}
      useSession.setState({ ready: true })
    })()
  }, [])
  if (!ready) return <Loading />
  return (
    <Suspense fallback={<Loading />}>
      <Routes>
        <Route path="/login" element={<AuthPage mode="login" />} />
        <Route path="/register" element={<AuthPage mode="register" />} />
        <Route path="/forgot" element={<AuthPage mode="forgot" />} />
        <Route path="/oauth/done" element={<OAuthDone />} />
        <Route path="/invite/:token" element={<Invite />} />
        <Route path="/s/:token" element={<Share />} />
        <Route path="/legal/:slug?" element={<LegalPage />} />
        <Route path="/welcome" element={<Landing />} />
        <Route path="/books/:bid/chapters/:cid" element={<Protected><Workspace /></Protected>} />
        <Route element={<Protected><Shell /></Protected>}>
          <Route index element={<Dashboard />} />
          <Route path="books" element={<Books />} />
          <Route path="books/new" element={<NewBook />} />
          <Route path="books/:bid/*" element={<BookLayout />} />
          <Route path="my-lines" element={<MyLines />} />
          <Route path="voices/*" element={<Voices />} />
          <Route path="assets" element={<Assets />} />
          <Route path="autobook" element={<AutoBook />} />
          <Route path="quick" element={<Quick />} />
          <Route path="production" element={<Production />} />
          <Route path="jobs" element={<Jobs />} />
          <Route path="studio/*" element={<StudioSettings />} />
          <Route path="settings/account" element={<Account />} />
          <Route path="membership" element={<Membership />} />
          <Route path="help/*" element={<Help />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Route>
      </Routes>
    </Suspense>
  )
}
