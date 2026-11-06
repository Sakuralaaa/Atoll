import { useState } from 'react'
import { Select, TextInput } from './feedback'
import { useRootDomains } from './RootDomainsSettings'
import { useT } from '../i18n'
import { api } from '../api/client'
import { useQuery } from '../api/hooks'

/** What POST /services/{id}/domains gets: a prefix plus the picked root, or a full hostname. */
export interface HostnameValue { hostname: string; root_domain?: string; full: string; ok: boolean }

const FULL = '__full__'
const prefixRe = /^(@|([a-z0-9]([a-z0-9-]*[a-z0-9])?)(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*)$/
const hostRe = /^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z][a-z0-9-]*$/

/** Hostname field with a suffix picker: the prefix ("blog") plus one of the root domains, or a full
 *  hostname of any other domain. Starts on the project's root domain, else the default one. */
export function useHostnameInput(projectRoot?: string) {
  // a sub-account gets only the roots an admin allowed it, and only subdomains of them (B7)
  const member = useQuery('me', api.me).data?.role === 'member'
  const roots = useRootDomains().data ?? []
  const names = roots.map(r => r.name)
  const initial = projectRoot && names.includes(projectRoot) ? projectRoot : roots.find(r => r.default)?.name ?? names[0] ?? FULL
  const [text, setText] = useState('')
  const [picked, setPicked] = useState<string>()
  const root = picked ?? initial
  const p = text.trim().toLowerCase().replace(/\.$/, '')
  let v: HostnameValue
  if (root === FULL || !names.length) v = { hostname: p, full: p, ok: hostRe.test(p) }
  else v = { hostname: p || '@', root_domain: root, full: !p || p === '@' ? root : p === root || p.endsWith('.' + root) ? p : `${p}.${root}`, ok: member ? !!p && p !== '@' && p !== root && !p.includes('*') && prefixRe.test(p) : !p || prefixRe.test(p) }
  return { v, text, setText, root, setRoot: setPicked, names, member }
}

export function HostnameInput({ h, autoFocus, onEnter, placeholder }: { h: ReturnType<typeof useHostnameInput>; autoFocus?: boolean; onEnter?: () => void; placeholder?: string }) {
  const { t } = useT()
  const full = h.root === FULL || !h.names.length
  return (
    <div className="flex flex-wrap items-center gap-2 min-w-0">
      <TextInput autoFocus={autoFocus} mono className="flex-1 min-w-[min(100%,160px)]" value={h.text} onChange={e => h.setText(e.target.value)} onKeyDown={e => e.key === 'Enter' && onEnter?.()}
        placeholder={full ? 'app.example.com' : h.member ? t('子域前缀，如 app', 'subdomain prefix, e.g. app') : placeholder ?? t('前缀，如 app；留空为根域名本身', 'prefix, e.g. app; empty = the root itself')} />
      {h.names.length > 0 && (
        <Select className="w-auto max-w-full font-mono" value={h.root} onChange={e => h.setRoot(e.target.value)} title={t('根域名', 'Root domain')}>
          {h.names.map(n => <option key={n} value={n}>.{n}</option>)}
          <option value={FULL}>{t('完整域名…', 'Full hostname…')}</option>
        </Select>
      )}
      {h.member && <div className="w-full text-[12px] text-muted">{h.names.length
        ? t('子账号只能用管理员允许的根域，且只能填子域前缀（不能用根域本身或通配符）；其他域名请选「完整域名」，自己添加 A 记录。', 'Sub-accounts use only the roots an admin allowed, as a subdomain prefix (never the root itself or a wildcard); for another domain pick "Full hostname" and add its A record yourself.')
        : t('没有可用的根域：请填自己的完整域名，并自己添加 A 记录。', 'No root domain is available to you: enter your own full hostname and add its A record yourself.')}</div>}
    </div>
  )
}
