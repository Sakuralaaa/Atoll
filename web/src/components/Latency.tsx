import { useT } from '../i18n'
import { cx } from './ui-utils'
import type { Server } from '../api/types'

/** Colour for a control↔agent round trip: green < 100 ms, amber < 300 ms, red from 300 ms. */
const latencyTone = (ms: number) => ms < 100 ? 'text-emerald-600 dark:text-emerald-400' : ms < 300 ? 'text-amber-600 dark:text-amber-400' : 'text-danger'
const fmt = (ms: number) => ms < 10 ? ms.toFixed(1) : Math.round(ms).toString()

/**
 * 「延迟 38 ms」 from the latest round trip; 「超时」 (red, error in the tooltip) while the
 * measurements fail; "—" only while offline or before the first attempt.
 */
export function Latency({ s, label = true, avg, className }: { s: Server; label?: boolean; avg?: boolean; className?: string }) {
  const { t } = useT()
  const l = s.status === 'online' ? s.latency : undefined
  const failing = !!l && (l.failures ?? 0) > 0
  const ok = !!l && !failing && l.samples > 0
  const via = l?.method === 'message' ? t('（WebSocket ping 无响应，改用消息往返测量）', ' (WebSocket pings unanswered; measured with a message round trip)') : ''
  const title = failing ? t(`中控 ↔ Agent 往返测量连续失败 ${l!.failures} 次：${l!.last_error ?? ''}`, `Control ↔ agent round trip failed ${l!.failures} time(s) in a row: ${l!.last_error ?? ''}`)
    : ok ? t(`中控 ↔ Agent 往返延迟：最新 ${fmt(l!.last_ms)} ms，最近 ${l!.samples} 次平均 ${fmt(l!.avg_ms)} ms${via}`, `Control ↔ agent round trip: last ${fmt(l!.last_ms)} ms, average of the last ${l!.samples}: ${fmt(l!.avg_ms)} ms${via}`)
    : s.status === 'online' ? t('中控 ↔ Agent 往返延迟：还没有测量', 'Control ↔ agent round trip: not measured yet') : t('中控 ↔ Agent 往返延迟：服务器离线', 'Control ↔ agent round trip: server offline')
  return (
    <span title={title} className={cx('inline-flex items-baseline gap-1 whitespace-nowrap tnum', className)}>
      {label && <span className="text-muted">{t('延迟', 'Latency')}</span>}
      {failing ? <span className="font-semibold text-danger cursor-help">{t('超时', 'Timeout')}</span>
        : ok ? <span className={cx('font-semibold', latencyTone(l!.last_ms))}>{fmt(l!.last_ms)} ms</span> : <span className="text-dim">—</span>}
      {avg && ok && <span className="text-muted text-[12px]">{t(`· 平均 ${fmt(l!.avg_ms)} ms`, `· avg ${fmt(l!.avg_ms)} ms`)}</span>}
    </span>
  )
}
