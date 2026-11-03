import type { Server, Settings } from '../api/types'
import { useT } from '../i18n'

/** Agent reaches the control server over the tailnet. */
export const onTailnet = (s: Server) => !!s.network?.via_tailnet

/** Human text for NodeNetwork.state. */
export function useNetState() {
  const { t } = useT()
  return (st?: string) => ({
    running: t('运行中', 'running'), not_installed: t('未安装 Tailscale', 'Tailscale not installed'), stopped: t('已停止', 'stopped'),
    needs_login: t('需要登录', 'needs login'), starting: t('启动中', 'starting'), unknown: t('未知', 'unknown'),
  } as Record<string, string>)[st ?? ''] ?? (st || t('未上报', 'not reported'))
}

/** Spread into other PUT /settings bodies built from GET: keeps the stored network settings
 * (GET returns a derived tailnet_control_url that must not be stored back). */
export const KEEP_NETWORK: Partial<Settings> = { network_provider: '', tailnet_name: '', tailnet_control_url: '' }
