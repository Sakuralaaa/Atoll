import { useT } from '../i18n'
import { cx } from './ui-utils'
import type { Server } from '../api/types'

/** Colour for a control↔agent round trip: green < 100 ms, amber < 300 ms, red from 300 ms. */
const latencyTone = (ms: number) => ms < 100 ? 'text-emerald-600 dark:text-emerald-400' : ms < 300 ? 'text-amber-600 dark:text-amber-400' : 'text-[#ef4444]'
const fmt = (ms: number) => ms < 10 ? ms.toFixed(1) : Math.round(ms).toString()

/** 「延迟 38 ms」 from the latest WebSocket ping; "—" while offline or not measured yet. */
export function Latency({ s, label = true, avg, className }: { s: Server; label?: boolean; avg?: boolean; className?: string }) {
  const { t } = useT()
  const l = s.status === 'online' ? s.latency : undefined
  const title = l ? t(`中控 ↔ Agent 往返延迟：最新 ${fmt(l.last_ms)} ms，最近 ${l.samples} 次平均 ${fmt(l.avg_ms)} ms`, `Control ↔ agent round trip: last ${fmt(l.last_ms)} ms, average of the last ${l.samples}: ${fmt(l.avg_ms)} ms`)
    : t('中控 ↔ Agent 往返延迟：暂无数据', 'Control ↔ agent round trip: not measured')
  return (
    <span title={title} className={cx('inline-flex items-baseline gap-1 whitespace-nowrap tnum', className)}>
      {label && <span className="text-muted">{t('延迟', 'Latency')}</span>}
      {l ? <span className={cx('font-semibold', latencyTone(l.last_ms))}>{fmt(l.last_ms)} ms</span> : <span className="text-dim">—</span>}
      {avg && l && <span className="text-muted text-[12px]">{t(`· 平均 ${fmt(l.avg_ms)} ms`, `· avg ${fmt(l.avg_ms)} ms`)}</span>}
    </span>
  )
}
