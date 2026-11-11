import { Check } from '@/components/icons'
import { motion } from 'framer-motion'
import { Link } from 'react-router-dom'
import { cn } from './ui'

export const FLOW = [
  { key: 'import', label: '导入书稿', hint: '上传书稿并拆分章节', tab: 'chapters' },
  { key: 'characters', label: '分析角色', hint: 'AI 找出会说话的角色并写好小传', tab: 'characters' },
  { key: 'casting', label: '选角', hint: '为每个角色挑选声音', tab: 'characters' },
  { key: 'script', label: '生成画本', hint: '标注说话人、情绪和读音', tab: 'chapters' },
  { key: 'voice', label: '配音', hint: '按角色生成或录制每一句', tab: 'chapters' },
  { key: 'render', label: '合成', hint: '拼接音频并加上背景音乐', tab: 'chapters' },
  { key: 'review', label: '审听', hint: '检查漏读错读，修复问题', tab: 'review' },
  { key: 'export', label: '导出发布', hint: '导出 MP3/M4B 或发布到平台', tab: 'deliver' },
] as const

/** Production pipeline as a sequence (it really is one), highlighting the next thing to do. */
export function FlowSteps({ bookId, steps, next, compact }: { bookId: string; steps: Record<string, { done: boolean; progress: number }>; next: string; compact?: boolean }) {
  return (
    <ol className={cn('grid gap-2', compact ? 'grid-cols-4 sm:grid-cols-8' : 'grid-cols-2 sm:grid-cols-4 xl:grid-cols-8')}>
      {FLOW.map((s, i) => {
        const st = steps?.[s.key]
        const done = st?.done
        const isNext = next === s.key
        return (
          <li key={s.key}>
            <Link to={`/books/${bookId}/${s.tab}`} className={cn('group block h-full rounded-[10px] border px-3 py-2.5 transition-colors', isNext ? 'border-accent bg-accent-soft' : 'border-line bg-surface hover:border-line-strong')}>
              <div className="flex items-center gap-2">
                <span className={cn('grid size-5 shrink-0 place-items-center rounded-full text-[11px] font-semibold tnum', done ? 'bg-accent text-accent-ink' : isNext ? 'bg-surface text-accent ring-1 ring-accent' : 'bg-sunken text-faint')}>
                  {done ? <Check className="size-3" strokeWidth={3} /> : i + 1}
                </span>
                <span className={cn('truncate text-[13px] font-medium', !done && !isNext && 'text-muted')}>{s.label}</span>
              </div>
              {!compact && <p className="mt-1.5 line-clamp-2 text-[11.5px] leading-snug text-faint">{isNext ? '下一步：' + s.hint : s.hint}</p>}
              {!compact && st && !done && st.progress > 0 && (
                <div className="mt-2 h-1 overflow-hidden rounded-full bg-sunken">
                  <motion.div className="h-full bg-accent" initial={false} animate={{ width: `${st.progress * 100}%` }} />
                </div>
              )}
            </Link>
          </li>
        )
      })}
    </ol>
  )
}
