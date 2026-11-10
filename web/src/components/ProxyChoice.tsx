import { Cloud, Zap } from 'lucide-react'
import { useT } from '../i18n'

/** 绑定域名: Cloudflare proxy (orange cloud) or direct (grey cloud), as two cards with what each costs. */
export function ProxyChoice({ proxied, onChange }: { proxied: boolean; onChange: (v: boolean) => void }) {
  const { t } = useT()
  return (
    <div className="mb-1">
      <div className="text-[13px] font-semibold mb-1.5">{t('接入方式', 'Traffic')}</div>
      <div role="radiogroup" className="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <ProxyCard on={proxied} onClick={() => onChange(true)} icon={<Cloud size={18} />} title={t('Cloudflare 代理', 'Cloudflare proxy')} tag={t('推荐 · 防护', 'Recommended')}
          desc={t('隐藏源站 IP，有 CDN 和防护。代价：单个请求 100 秒超时，只代理 HTTP(S)。', 'Hides the origin IP, CDN and protection. Cost: 100-second request timeout, HTTP(S) only.')} />
        <ProxyCard on={!proxied} onClick={() => onChange(false)} icon={<Zap size={18} />} title={t('直连', 'Direct')} tag={t('长请求 / 非 HTTP', 'Long requests / non-HTTP')}
          desc={t('灰云，浏览器直连服务器，没有超时限制。代价：会暴露源站 IP，没有 Cloudflare 防护。', 'Grey cloud, browsers reach the server directly, no timeout. Cost: exposes the origin IP, no Cloudflare protection.')} />
      </div>
    </div>
  )
}

function ProxyCard({ on, onClick, icon, title, tag, desc }: { on: boolean; onClick: () => void; icon: React.ReactNode; title: string; tag: string; desc: string }) {
  return (
    <button type="button" role="radio" aria-checked={on} onClick={onClick}
      className={`text-left rounded-[4px] border-2 p-3.5 transition-[transform,box-shadow] ${on ? 'border-[var(--edge)] bg-card shadow-hard -translate-x-px -translate-y-px' : 'border-line bg-sunk text-fg2 hover:border-line2'}`}>
      <div className="flex items-center gap-2">
        <span className={`h-7 w-7 grid place-items-center rounded-[3px] border-[1.5px] border-[var(--edge)] shrink-0 ${on ? 'bg-t-orange text-ink' : 'bg-card2'}`}>{icon}</span>
        <span className="text-[13.5px] font-semibold">{title}</span>
        <span className={`ml-auto h-4 w-4 rounded-full border-2 border-[var(--edge)] shrink-0 ${on ? 'bg-brand' : ''}`} />
      </div>
      <div className="text-[11.5px] font-semibold text-muted mt-2">{tag}</div>
      <div className="text-[12px] text-dim mt-1 leading-5">{desc}</div>
    </button>
  )
}
