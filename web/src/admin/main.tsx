import React from 'react'
import ReactDOM from 'react-dom/client'
import { QueryClientProvider } from '@tanstack/react-query'
import * as RTooltip from '@radix-ui/react-tooltip'
import { BrowserRouter } from 'react-router-dom'
import { MotionConfig } from 'framer-motion'
import { Toaster } from 'sonner'
import '@/index.css'
import '@/lib/theme'
import { queryClient } from '@/lib/live'
import { ConfirmHost } from '@/components/ui'
import { NetActivity } from '@/components/net'
import AdminApp from './App'
import { toast } from 'sonner'
import { ApiError } from '@/lib/api'

// errors nobody caught (e.g. in click handlers) still reach the operator
window.addEventListener('unhandledrejection', (ev) => {
  if (ev.reason instanceof ApiError) {
    ev.preventDefault()
    toast.error(ev.reason.message)
  }
})

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <MotionConfig reducedMotion="user">
      <RTooltip.Provider>
        <BrowserRouter basename="/admin">
          <AdminApp />
        </BrowserRouter>
        <ConfirmHost />
        <NetActivity />
        <Toaster position="top-center" toastOptions={{ className: '!bg-surface !text-ink !border-line !rounded-[12px] !shadow-[var(--shadow-pop)]' }} />
      </RTooltip.Provider>
      </MotionConfig>
    </QueryClientProvider>
  </React.StrictMode>,
)
