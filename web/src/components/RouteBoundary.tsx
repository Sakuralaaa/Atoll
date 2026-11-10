import { Component, type ErrorInfo, type ReactNode } from 'react'
import { AlertTriangle, RefreshCw } from 'lucide-react'
import { isChunkLoadError } from '../chunkReload'
import { useT } from '../i18n'

/** Catches a page that fails to load or render, so the app shows a retry instead of going blank. */
type Props = { children: ReactNode; resetKey?: string }
type State = { error?: Error; key?: string }
export class RouteBoundary extends Component<Props, State> {
  state: State = { key: this.props.resetKey }
  static getDerivedStateFromError(error: Error) { return { error } }
  // navigating to another route clears the error (props change, not an effect)
  static getDerivedStateFromProps(p: Props, s: State): Partial<State> | null {
    return p.resetKey !== s.key ? { key: p.resetKey, error: undefined } : null
  }
  componentDidCatch(error: Error, info: ErrorInfo) { console.error('page failed', error, info.componentStack) }
  render() {
    const { error } = this.state
    if (!error) return this.props.children
    // a lazy import that failed stays failed inside React.lazy: only a reload fetches the new chunk
    const retry = isChunkLoadError(error) ? () => location.reload() : () => this.setState({ error: undefined })
    return <RouteError error={error} stale={isChunkLoadError(error)} onRetry={retry} />
  }
}

function RouteError({ error, stale, onRetry }: { error: Error; stale: boolean; onRetry: () => void }) {
  const { t } = useT()
  return (
    <div className="min-h-[60vh] grid place-items-center px-4">
      <div role="alert" className="rounded-2xl surface !bg-t-red text-ink px-6 py-5 max-w-[560px] w-full flex items-start gap-4">
        <AlertTriangle size={18} className="mt-0.5 shrink-0" />
        <div className="flex-1 min-w-0">
          <div className="text-[13.5px] font-bold">{stale ? t('页面加载失败，面板可能刚更新过', 'The page failed to load; the panel may have just been updated') : t('页面出错了', 'This page hit an error')}</div>
          <div className="text-[13px] mt-0.5 [overflow-wrap:anywhere]">{error.message}</div>
        </div>
        <button onClick={onRetry} className="btn-secondary h-8 px-3 text-[12.5px] inline-flex items-center gap-1.5 shrink-0"><RefreshCw size={13} />{stale ? t('重新加载', 'Reload') : t('重试', 'Retry')}</button>
      </div>
    </div>
  )
}
