import { L } from '../i18n'
import { useState } from 'react'
import { Loader2, Globe, ShieldCheck, Link2, Check, KeyRound, Save, Sparkles, Bell, Archive, Network, Cpu, Eye } from 'lucide-react'
import { SettingsLayout } from '../components/SettingsNav'
import { useSettingsGroup, type SettingsGroup } from '../components/settings-group'
import { MainShell } from '../components/Shell'
import { Badge, Button, Card, Hint } from '../components/ui'
import { ErrorState, Select, Skeleton, TextInput } from '../components/feedback'
import { api } from '../api/client'
import { run, useBusy, useQuery } from '../api/hooks'
import { useNavigate } from 'react-router-dom'
import { startOnboarding } from '../onboarding'
import { useCan } from '../api/queries'
import type { Settings as S } from '../api/types'
import ImageCheckSettings from '../components/ImageCheckSettings'
import ResourceDefaultsSettings from '../components/ResourceDefaultsSettings'
import BackupStorageSettings from '../components/BackupStorageSettings'
import NetworkSettings from '../components/NetworkSettings'
import GeoIPSettings from '../components/GeoIPSettings'
import PrivacySettings from '../components/PrivacySettings'
import NotificationSettings from '../components/NotificationSettings'
import RootDomainsSettings from '../components/RootDomainsSettings'
import { SettingsTabs } from './Security'
import AppearanceSettings from '../components/AppearanceSettings'
import { cx } from '../components/ui-utils'

const GROUPS: SettingsGroup[] = [
  { k: 'general', l: L("常规"), icon: <Link2 /> },
  { k: 'domains', l: L("域名"), icon: <Globe /> },
  { k: 'notify', l: L("通知"), icon: <Bell /> },
  { k: 'backup', l: L("备份"), icon: <Archive /> },
  { k: 'network', l: L("网络"), icon: <Network /> },
  { k: 'services', l: L("服务"), icon: <Cpu /> },
  { k: 'appearance', l: L("显示"), icon: <Eye /> },
]

export default function Settings() {
  const canSettings = useCan('settings')
  const q = useQuery(canSettings ? 'settings' : null, api.settings)
  // members only get 显示 here (theme and privacy are per browser); the rest needs settings (notes/subaccounts.md)
  const groups = canSettings ? GROUPS : GROUPS.filter(x => x.k === 'appearance')
  const g = useSettingsGroup(groups)
  return (
    <MainShell>
      <div className="max-w-[1240px] mx-auto px-4 pt-6 pb-12 @2xl:px-8 @2xl:pt-10 @5xl:px-14 @5xl:pt-12 @5xl:pb-16">
        <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em]">{L("设置")}</h1>
        <SettingsTabs />
        {canSettings && q.error && !q.data && <ErrorState error={q.error} onRetry={q.refetch} />}
        {canSettings && !q.data && !q.error && <Card className="p-6 space-y-5">{[0, 1, 2, 3].map(i => <Skeleton key={i} className="h-11 w-full" />)}</Card>}
        {(q.data || !canSettings) && <SettingsLayout groups={groups} active={g}>
          {q.data && g === 'general' && <><Form key={JSON.stringify(q.data)} s={q.data} part="panel" /><ReopenOnboarding /></>}
          {q.data && g === 'domains' && <><Form key={JSON.stringify(q.data)} s={q.data} part="domains" /><RootDomainsSettings /></>}
          {q.data && g === 'notify' && <NotificationSettings />}
          {q.data && g === 'backup' && <BackupStorageSettings s={q.data} />}
          {q.data && g === 'network' && <NetworkSettings s={q.data} />}
          {q.data && g === 'services' && <><ResourceDefaultsSettings key={`res-${q.data.default_mem_limit_mb}-${q.data.default_cpu_limit}`} s={q.data} /><ImageCheckSettings key={`img-${q.data.image_check_interval_hours}`} s={q.data} /></>}
          {g === 'appearance' && <><AppearanceSettings />{q.data && <GeoIPSettings s={q.data} />}<PrivacySettings /></>}
        </SettingsLayout>}
      </div>
    </MainShell>
  )
}

/** Settings → 首次启动向导: start the first-run wizard again (progress is per account, in this browser). */
function ReopenOnboarding() {
  const nav = useNavigate()
  const me = useQuery('me', api.me).data
  if (!me) return null
  return (
    <Section icon={<Sparkles size={17} />} t={L("首次启动向导")}>
      <Field t={L("重新打开向导")}>
        <Button size="sm" onClick={() => { startOnboarding(me.username); nav('/welcome') }}><Sparkles size={14} />{L("打开向导")}</Button>
      </Field>
    </Section>
  )
}

function Section({ icon, t, d, children }: { icon: React.ReactNode; t: string; d?: string; children: React.ReactNode }) {
  return (
    <div className="mb-8">
      <div className="text-[15px] font-semibold flex items-center gap-2">{icon}{t}{d && <Hint>{d}</Hint>}</div>
      <Card className="divide-y divide-line mt-3">{children}</Card>
    </div>
  )
}
function Field({ t, d, children, right }: { t: string; d?: React.ReactNode; children: React.ReactNode; right?: React.ReactNode }) {
  return (
    <div className="px-4 @2xl:px-6 py-5 grid grid-cols-1 @3xl:grid-cols-[260px_minmax(0,1fr)] gap-3 @3xl:gap-6 items-start">
      <div><div className="text-[13px] font-semibold flex items-center gap-2 flex-wrap">{t}{right}</div>{d && <div className="text-[12.5px] text-muted mt-1 leading-5">{d}</div>}</div>
      <div className="min-w-0">{children}</div>
    </div>
  )
}
const Saved = ({ on }: { on: boolean }) => on ? <Badge tone="green"><Check size={11} strokeWidth={3} />{L("已保存")}</Badge> : <Badge tone="zinc">{L("未设置")}</Badge>

function Form({ s, part }: { s: S; part: 'panel' | 'domains' }) {
  const [v, setV] = useState({ cloudflare_zone_id: s.cloudflare_zone_id, tls_mode: s.tls_mode || 'cloudflare-origin', public_url: s.public_url, acme_email: s.acme_email ?? '' })
  const [token, setToken] = useState(''); const [cert, setCert] = useState(''); const [key, setKey] = useState('')
  const [busy, wrap] = useBusy()
  const set = (k: keyof typeof v) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setV(x => ({ ...x, [k]: e.target.value }))
  const certPair = (cert.trim() === '') === (key.trim() === '')
  const certLooksOk = (!cert || cert.includes('BEGIN CERTIFICATE')) && (!key || key.includes('PRIVATE KEY'))
  const changed = token || cert || key || (Object.keys(v) as (keyof typeof v)[]).some(k => v[k] !== ((s as unknown as Record<string, string>)[k] ?? ''))
  const save = () => wrap(async () => {
    // base_domain is the default root domain, managed in the root domain list below
    const body: Partial<S> = { ...v, base_domain: s.base_domain, public_url: v.public_url.trim().replace(/\/$/, '') }
    if (token.trim()) body.cloudflare_token = token.trim()
    if (cert.trim() && key.trim()) { body.origin_cert_pem = cert.trim() + '\n'; body.origin_key_pem = key.trim() + '\n' }
    const r = await run(() => api.putSettings(body), { ok: L("设置已保存"), invalidate: ['settings', 'domains'] })
    if (r) { setToken(''); setCert(''); setKey('') }
  })
  const area = 'w-full rounded-lg field px-3.5 py-2.5 font-mono text-[12px] leading-5 outline-none text-fg placeholder:text-dim resize-y'
  return (
    <>
      {part === 'panel' && <Section icon={<Link2 size={17} />} t={L("面板")}>
        <Field t={L("面板地址")} d={L("Agent 和 CLI 用它连回中控")}>
          <TextInput mono className="w-full" value={v.public_url} onChange={set('public_url')} placeholder="https://panel.example.com" />
        </Field>
      </Section>}
      {part === 'domains' && <>
      <Section icon={<Globe size={17} />} t="Cloudflare" d={L("服务绑定根域名下的域名时，Atoll 用这个令牌自动创建和删除 DNS 记录。令牌需要 Zone.DNS 编辑权限；要给根域名自动签 Origin 证书，再加 SSL and Certificates 编辑权限。")}>
        <Field t="Zone ID" d={L("可选：默认根域名的 Zone，留空则按域名自动查找")}>
          <TextInput mono className="w-full" value={v.cloudflare_zone_id} onChange={set('cloudflare_zone_id')} placeholder={L("32 位十六进制")} />
        </Field>
        <Field t={L("API 令牌")} right={<Saved on={s.has_cloudflare_token} />}>
          <TextInput mono type="password" autoComplete="off" className="w-full" value={token} onChange={e => setToken(e.target.value)} placeholder={s.has_cloudflare_token ? L("已设置，留空保持不变") : L("粘贴 Cloudflare API 令牌")} />
        </Field>
      </Section>
      <Section icon={<ShieldCheck size={17} />} t={L("HTTPS 证书")} d={L("全局证书设置：证书模式为「跟随全局」的根域名和不在任何根域名下的域名用它。Cloudflare 代理模式推荐用 Origin CA 证书（15 年有效，推送到每台服务器的 atoll-caddy）；不走代理时用 ACME 自动签发。")}>
        <Field t={L("证书模式")}>
          <Select className="w-full" value={v.tls_mode} onChange={set('tls_mode')}>
            <option value="cloudflare-origin">Cloudflare Origin CA</option>
            <option value="acme">{L("ACME（Let's Encrypt）")}</option>
          </Select>
        </Field>
        {v.tls_mode === 'acme' ? (
          <Field t={L("ACME 邮箱")} d={L("证书到期提醒发到这里")}>
            <TextInput className="w-full" type="email" value={v.acme_email} onChange={set('acme_email')} placeholder="you@example.com" />
          </Field>
        ) : <>
          <Field t={L("Origin 证书")} right={<Saved on={s.has_origin_cert} />} d={L("PEM 格式，Cloudflare → SSL/TLS → 源服务器 → 创建证书")}>
            <textarea rows={2} className={area} value={cert} onChange={e => setCert(e.target.value)} placeholder={s.has_origin_cert ? L("已设置，留空保持不变") : '-----BEGIN CERTIFICATE-----'} spellCheck={false} />
          </Field>
          <Field t={L("私钥")} right={<KeyRound size={13} className="text-muted" />}>
            <textarea rows={2} className={area} value={key} onChange={e => setKey(e.target.value)} placeholder={s.has_origin_cert ? L("已设置，留空保持不变") : '-----BEGIN PRIVATE KEY-----'} spellCheck={false} />
            {!certPair && <div className="text-[12px] text-danger mt-1.5">{L("证书和私钥需要一起填写")}</div>}
            {certPair && !certLooksOk && <div className="text-[12px] text-danger mt-1.5">{L("看起来不是 PEM 格式")}</div>}
          </Field>
        </>}
      </Section>
      </>}
      <div className={cx('flex flex-wrap items-center justify-end gap-3 mb-8', changed && 'sticky bottom-6')}>
        {changed && <span className="text-[12.5px] text-muted">{L("有未保存的修改")}</span>}
        <Button variant="primary" size="sm" disabled={!changed || !certPair || !certLooksOk || busy} onClick={save}>{busy ? <Loader2 size={14} className="animate-spin" /> : <Save size={14} />}{L("保存")}</Button>
      </div>
    </>
  )
}
