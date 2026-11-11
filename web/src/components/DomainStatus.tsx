import { Loader2, ShieldCheck } from 'lucide-react'
import { Badge } from './ui'
import { useT } from '../i18n'
import type { Domain } from '../api/types'

/** Status badge for a bound domain. Unknown statuses fall back to a neutral badge. */
export function DomainBadge({ d }: { d: Domain }) {
  const { t } = useT()
  switch (d.status) {
    case 'active':
      return <span title={d.error}><Badge tone="green"><ShieldCheck size={11} />{t('证书有效', 'Certificate OK')}</Badge></span>
    case 'provisioning':
      return <span title={t('DNS 记录已创建：等待公网解析生效，随后 Caddy 签发证书', 'DNS record created: waiting for it to resolve publicly, then Caddy issues the certificate')}>
        <Badge tone="amber"><Loader2 size={11} className="animate-spin" />{t('签发中', 'Provisioning')}</Badge></span>
    case 'pending':
      return <span title={d.error}><Badge tone="amber">{t('待配置 DNS', 'DNS pending')}</Badge></span>
    case 'error':
      return <span title={d.error}><Badge tone="red">{t('异常', 'Error')}</Badge></span>
    default:
      return <Badge tone="zinc">{d.status || t('未知', 'Unknown')}</Badge>
  }
}

/** One-line note under a domain: progress while provisioning, otherwise its error/warning. */
export function DomainNote({ d }: { d: Domain }) {
  const { t } = useT()
  if (d.status === 'provisioning') return <div className="text-[12px] text-muted truncate">{t('等待 DNS 生效并签发证书…', 'Waiting for DNS, then the certificate…')}</div>
  if (!d.error) return null
  return <div className={d.status === 'error' ? 'text-[12px] text-danger truncate' : 'text-[12px] text-amber truncate'} title={d.error}>{d.error}</div>
}
