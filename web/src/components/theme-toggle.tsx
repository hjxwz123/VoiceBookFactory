import { AnimatePresence, motion } from 'framer-motion'
import { useTheme } from '@/lib/theme'
import { Moon, Sun } from './icons'
import { cn } from './ui'

/** Day / night switch. Shows the mode in use; a click switches to the other one, spreading out from the button, and remembers it. */
export function ThemeToggle({ className }: { className?: string }) {
  const { dark, toggle } = useTheme()
  const label = dark ? '切换到白天模式' : '切换到夜间模式'
  return (
    <button type="button" onClick={(e) => { const b = e.currentTarget.getBoundingClientRect(); toggle({ x: b.left + b.width / 2, y: b.top + b.height / 2 }) }} aria-label={label} title={label}
      className={cn('grid size-9 shrink-0 place-items-center overflow-hidden rounded-full text-muted transition-colors hover:bg-sunken hover:text-ink', className)}>
      <AnimatePresence mode="wait" initial={false}>
        <motion.span key={dark ? 'moon' : 'sun'} className="grid place-items-center" initial={{ y: 14, opacity: 0, rotate: -40 }} animate={{ y: 0, opacity: 1, rotate: 0 }} exit={{ y: -14, opacity: 0, rotate: 40 }} transition={{ duration: 0.22, ease: [0.22, 1, 0.36, 1] }}>
          {dark ? <Moon className="size-[18px]" /> : <Sun className="size-[18px]" />}
        </motion.span>
      </AnimatePresence>
    </button>
  )
}
