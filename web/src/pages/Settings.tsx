import { useState } from 'react'
import { Loader2, Globe, Cloud, ShieldCheck, Link2, Check, KeyRound } from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Badge, Button, Card } from '../components/ui'
import { ErrorState, Select, Skeleton, TextInput } from '../components/feedback'
import { api } from '../api/client'
import { run, useBusy, useQuery } from '../api/hooks'
import type { Settings as S } from '../api/types'
import ImageCheckSettings from '../components/ImageCheckSettings'
import ResourceDefaultsSettings from '../components/ResourceDefaultsSettings'
import BackupStorageSettings from '../components/BackupStorageSettings'
import NetworkSettings from '../components/NetworkSettings'
import NotificationSettings from '../components/NotificationSettings'
import { SettingsTabs } from './Security'
import { cx } from '../components/ui-utils'

export default function Settings() {
  const q = useQuery('settings', api.settings)
  return (
    <MainShell>
      <div className="max-w-[1000px] mx-auto px-4 pt-6 pb-12 @2xl:px-8 @2xl:pt-10 @5xl:px-14 @5xl:pt-12 @5xl:pb-16">
        <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em]">设置</h1>
        <p className="text-[13px] text-muted mt-2 mb-6">域名、证书、面板地址、通知和备份存储。密钥只写不读：保存后不会再显示，留空表示保持原值。</p>
        <SettingsTabs />
        {q.error && !q.data && <ErrorState error={q.error} onRetry={q.refetch} />}
        {!q.data && !q.error && <Card className="p-6 space-y-5">{[0, 1, 2, 3].map(i => <Skeleton key={i} className="h-11 w-full" />)}</Card>}
        {q.data && <Form key={JSON.stringify(q.data)} s={q.data} />}
        {q.data && <NotificationSettings />}
        {q.data && <ImageCheckSettings key={`img-${q.data.image_check_interval_hours}`} s={q.data} />}
        {q.data && <ResourceDefaultsSettings key={`res-${q.data.default_mem_limit_mb}-${q.data.default_cpu_limit}`} s={q.data} />}
        {q.data && <BackupStorageSettings s={q.data} />}
        {q.data && <NetworkSettings s={q.data} />}
      </div>
    </MainShell>
  )
}

function Section({ icon, t, d, children }: { icon: React.ReactNode; t: string; d?: string; children: React.ReactNode }) {
  return (
    <div className="mb-8">
      <div className="text-[15px] font-semibold flex items-center gap-2">{icon}{t}</div>
      {d && <div className="text-[13px] text-muted mt-1 mb-3 leading-5">{d}</div>}
      <Card className={cx('divide-y divide-line', !d && 'mt-3')}>{children}</Card>
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
const Saved = ({ on }: { on: boolean }) => on ? <Badge tone="green"><Check size={11} strokeWidth={3} />已保存</Badge> : <Badge tone="zinc">未设置</Badge>

function Form({ s }: { s: S }) {
  const [v, setV] = useState({ base_domain: s.base_domain, cloudflare_zone_id: s.cloudflare_zone_id, tls_mode: s.tls_mode || 'cloudflare-origin', public_url: s.public_url, acme_email: s.acme_email ?? '' })
  const [token, setToken] = useState(''); const [cert, setCert] = useState(''); const [key, setKey] = useState('')
  const [busy, wrap] = useBusy()
  const set = (k: keyof typeof v) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setV(x => ({ ...x, [k]: e.target.value }))
  const certPair = (cert.trim() === '') === (key.trim() === '')
  const certLooksOk = (!cert || cert.includes('BEGIN CERTIFICATE')) && (!key || key.includes('PRIVATE KEY'))
  const changed = token || cert || key || (Object.keys(v) as (keyof typeof v)[]).some(k => v[k] !== ((s as unknown as Record<string, string>)[k] ?? ''))
  const save = () => wrap(async () => {
    const body: Partial<S> = { ...v, base_domain: v.base_domain.trim().toLowerCase(), public_url: v.public_url.trim().replace(/\/$/, '') }
    if (token.trim()) body.cloudflare_token = token.trim()
    if (cert.trim() && key.trim()) { body.origin_cert_pem = cert.trim() + '\n'; body.origin_key_pem = key.trim() + '\n' }
    const r = await run(() => api.putSettings(body), { ok: '设置已保存', invalidate: ['settings', 'domains'] })
    if (r) { setToken(''); setCert(''); setKey('') }
  })
  const area = 'w-full rounded-lg field px-3.5 py-3 font-mono text-[12px] leading-5 outline-none text-fg placeholder:text-dim resize-y'
  return (
    <>
      <Section icon={<Link2 size={17} />} t="面板">
        <Field t="面板地址" d="Agent 和 CLI 用它连回中控；安装命令里也会用到。">
          <TextInput mono className="w-full" value={v.public_url} onChange={set('public_url')} placeholder="https://panel.example.com" />
        </Field>
      </Section>
      <Section icon={<Globe size={17} />} t="域名与 Cloudflare" d="服务绑定主域名的子域名时，Atoll 用这个令牌自动创建和删除 DNS 记录。令牌需要 Zone.DNS 编辑权限。">
        <Field t="主域名" d="例如 example.com；从 Zeabur 迁移时，*.zeabur.app 会映射到它的子域名。">
          <TextInput mono className="w-full" value={v.base_domain} onChange={set('base_domain')} placeholder="example.com" />
        </Field>
        <Field t="Zone ID" d="Cloudflare 域名概览页右下角">
          <TextInput mono className="w-full" value={v.cloudflare_zone_id} onChange={set('cloudflare_zone_id')} placeholder="32 位十六进制" />
        </Field>
        <Field t="API 令牌" right={<Saved on={s.has_cloudflare_token} />} d="只写：保存后不再显示">
          <TextInput mono type="password" autoComplete="off" className="w-full" value={token} onChange={e => setToken(e.target.value)} placeholder={s.has_cloudflare_token ? '已设置，留空保持不变' : '粘贴 Cloudflare API 令牌'} />
        </Field>
      </Section>
      <Section icon={<ShieldCheck size={17} />} t="HTTPS 证书" d="Cloudflare 代理模式推荐用 Origin CA 证书（15 年有效，推送到每台服务器的 atoll-caddy）；不走代理时用 ACME 自动签发。">
        <Field t="证书模式">
          <Select className="w-full" value={v.tls_mode} onChange={set('tls_mode')}>
            <option value="cloudflare-origin">Cloudflare Origin CA</option>
            <option value="acme">ACME（Let's Encrypt）</option>
          </Select>
        </Field>
        {v.tls_mode === 'acme' ? (
          <Field t="ACME 邮箱" d="证书到期提醒发到这里">
            <TextInput className="w-full" type="email" value={v.acme_email} onChange={set('acme_email')} placeholder="you@example.com" />
          </Field>
        ) : <>
          <Field t="Origin 证书" right={<Saved on={s.has_origin_cert} />} d="PEM 格式，Cloudflare → SSL/TLS → 源服务器 → 创建证书">
            <textarea rows={4} className={area} value={cert} onChange={e => setCert(e.target.value)} placeholder={s.has_origin_cert ? '已设置，留空保持不变' : '-----BEGIN CERTIFICATE-----'} spellCheck={false} />
          </Field>
          <Field t="私钥" right={<KeyRound size={13} className="text-muted" />} d="与证书一起保存；只写">
            <textarea rows={4} className={area} value={key} onChange={e => setKey(e.target.value)} placeholder={s.has_origin_cert ? '已设置，留空保持不变' : '-----BEGIN PRIVATE KEY-----'} spellCheck={false} />
            {!certPair && <div className="text-[12px] text-[#ef4444] mt-1.5">证书和私钥需要一起填写</div>}
            {certPair && !certLooksOk && <div className="text-[12px] text-[#ef4444] mt-1.5">看起来不是 PEM 格式</div>}
          </Field>
        </>}
      </Section>
      <div className={cx('flex flex-wrap items-center justify-end gap-3', changed && 'sticky bottom-6')}>
        {changed && <span className="text-[12.5px] text-muted">有未保存的修改</span>}
        <Button variant="primary" size="lg" disabled={!changed || !certPair || !certLooksOk || busy} onClick={save}>{busy ? <Loader2 size={15} className="animate-spin" /> : <Cloud size={15} />}保存设置</Button>
      </div>
    </>
  )
}
