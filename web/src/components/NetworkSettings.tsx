import { useEffect, useRef, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { AlertTriangle, Check, Loader2, Network, Save } from 'lucide-react'
import { Badge, Button, Card, Hint } from './ui'
import { TextInput, Toggle } from './feedback'
import { api } from '../api/client'
import { run, useBusy } from '../api/hooks'
import type { Settings } from '../api/types'
import { useT } from '../i18n'

/** Settings → 私有网络: Tailscale provider, write-only auth key, tailnet name, agent listener status. */
export default function NetworkSettings({ s }: { s: Settings }) {
  const { t } = useT()
  const ref = useRef<HTMLDivElement>(null)
  const loc = useLocation()
  useEffect(() => { if (loc.hash === '#network') ref.current?.scrollIntoView({ behavior: 'smooth', block: 'start' }) }, [loc.hash])
  const on = s.network_provider === 'tailscale'
  const key = [s.network_provider, s.has_tailscale_auth_key, s.tailnet_name, s.tailnet_control_url, s.tailnet_listen].join('|')
  return (
    <div ref={ref} id="network" className="mt-8 mb-8 scroll-mt-6">
      <div className="text-[15px] font-semibold flex items-center gap-2">
        <Network size={17} />{t('私有网络', 'Private network')}
        <Hint>{t(
        '面板仍走公网；Agent 与中控、跨服务器的服务互通走 tailnet。需要一个可复用、带 tag、最好预先批准的 auth key。',
        'The panel stays public; agent ↔ control and service ↔ service traffic across servers goes over the tailnet. Needs a reusable, tagged, ideally pre-approved auth key.')}</Hint>
        {on ? <Badge tone="green"><Check size={11} strokeWidth={3} />Tailscale</Badge> : <Badge tone="zinc">{t('未启用', 'Off')}</Badge>}
      </div>
      <div className="mb-3" />
      <Form key={key} s={s} />
    </div>
  )
}

function Row({ t, d, right, children }: { t: string; d?: React.ReactNode; right?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="px-4 @2xl:px-6 py-5 grid grid-cols-1 @3xl:grid-cols-[260px_minmax(0,1fr)] gap-3 @3xl:gap-6 items-start">
      <div><div className="text-[13px] font-semibold flex items-center gap-2 flex-wrap">{t}{right}</div>{d && <div className="text-[12.5px] text-muted mt-1 leading-5">{d}</div>}</div>
      <div className="min-w-0">{children}</div>
    </div>
  )
}

/** The control URL GET returns is derived from the listener when none is stored; don't echo it back as a stored value. */
function storedControlURL(s: Settings) {
  const u = s.tailnet_control_url ?? ''
  if (u && s.tailnet_listen && u.replace(/\/$/, '').endsWith(':' + s.tailnet_listen.split(':').pop()) && u.includes(s.tailnet_listen.split(':')[0])) return ''
  return u
}

function Form({ s }: { s: Settings }) {
  const { t } = useT()
  const initUrl = storedControlURL(s)
  const [on, setOn] = useState(s.network_provider === 'tailscale')
  const [authKey, setAuthKey] = useState('')
  const [name, setName] = useState(s.tailnet_name ?? '')
  const [url, setUrl] = useState(initUrl)
  const [busy, wrap] = useBusy()
  const wasOn = s.network_provider === 'tailscale'
  const dirty = on !== wasOn || !!authKey.trim() || name.trim() !== (s.tailnet_name ?? '') || url.trim() !== initUrl
  const keyBad = authKey.trim() !== '' && !authKey.trim().startsWith('tskey-')
  const urlBad = url.trim() !== '' && !/^https?:\/\//.test(url.trim())
  const needKey = on && !s.has_tailscale_auth_key && !authKey.trim()
  const save = () => wrap(async () => {
    // PUT /settings replaces the base fields: send them back; write-only secrets "" = keep.
    const body: Partial<Settings> = {
      ...s, cloudflare_token: '', origin_cert_pem: '', origin_key_pem: '', backup_access_key: '', backup_secret_key: '', backup_repo_password: '',
      network_provider: on ? 'tailscale' : 'none', tailnet_name: name.trim(), tailnet_control_url: url.trim(),
    }
    if (authKey.trim()) body.tailscale_auth_key = authKey.trim()
    const r = await run(() => api.putSettings(body), { ok: t('私有网络已保存', 'Private network saved'), invalidate: ['settings', 'servers'] })
    if (r) setAuthKey('')
  })
  const clearKey = () => wrap(async () => {
    await run(() => api.putSettings({ ...s, cloudflare_token: '', origin_cert_pem: '', origin_key_pem: '', backup_access_key: '', backup_secret_key: '', backup_repo_password: '', network_provider: 'none', tailnet_control_url: initUrl, clear_tailscale_auth_key: true }),
      { ok: t('auth key 已清除', 'Auth key cleared'), invalidate: ['settings'] })
  })
  return (
    <Card className="divide-y divide-line">
      <Row t={t('启用 Tailscale', 'Enable Tailscale')} d={t('新服务器安装时自动加入 tailnet；已加入的服务器在服务器详情里一键切换。', 'New servers join the tailnet during install; existing ones switch from the server page.')}>
        <Toggle on={on} onChange={setOn} />
      </Row>
      <Row t="Auth key" right={s.has_tailscale_auth_key ? <Badge tone="green"><Check size={11} strokeWidth={3} />{t('已设置', 'Set')}</Badge> : <Badge tone="zinc">{t('未设置', 'Not set')}</Badge>}
        d={t('只写：保存后不再显示。Tailscale 后台 → Settings → Keys → Generate auth key（Reusable + Tags）。', 'Write-only: never shown again. Tailscale admin → Settings → Keys → Generate auth key (Reusable + Tags).')}>
        <TextInput mono type="password" autoComplete="off" className="w-full" value={authKey} onChange={e => setAuthKey(e.target.value)}
          placeholder={s.has_tailscale_auth_key ? t('已设置，留空保持不变', 'Set — leave empty to keep') : 'tskey-auth-…'} />
        {keyBad && <div className="text-[12px] text-[#ef4444] mt-1.5">{t('应以 tskey- 开头', 'Should start with tskey-')}</div>}
        {needKey && <div className="text-[12px] text-[#ef4444] mt-1.5">{t('启用 Tailscale 需要 auth key', 'Tailscale needs an auth key')}</div>}
        {s.has_tailscale_auth_key && !wasOn && <button type="button" disabled={busy} onClick={clearKey} className="mt-2 text-[12.5px] text-muted underline hover:text-fg">{t('清除已保存的 auth key', 'Clear the stored auth key')}</button>}
      </Row>
      <Row t={t('Tailnet 名称', 'Tailnet name')} d={t('MagicDNS 后缀，可选，例如 tail1234.ts.net', 'MagicDNS suffix, optional, e.g. tail1234.ts.net')}>
        <TextInput mono className="w-full" value={name} onChange={e => setName(e.target.value)} placeholder="tail1234.ts.net" />
      </Row>
      <Row t={t('中控内网地址', 'Control URL over tailnet')} d={t('Agent 经 tailnet 连中控的地址。留空则用中控的 tailnet 监听地址（ATOLL_AGENT_LISTEN）。', 'Where agents reach the control server over the tailnet. Empty = derived from the tailnet listener (ATOLL_AGENT_LISTEN).')}>
        <TextInput mono className="w-full" value={url} onChange={e => setUrl(e.target.value)} placeholder={s.tailnet_control_url || 'http://100.x.y.z:7301'} />
        {urlBad && <div className="text-[12px] text-[#ef4444] mt-1.5">{t('需以 http:// 或 https:// 开头', 'Must start with http:// or https://')}</div>}
      </Row>
      <Row t={t('监听状态', 'Listener status')} d={t('中控在 tailnet 上为 Agent 开放的端口', 'The agent endpoint the control server serves on the tailnet')}>
        <div className="space-y-2 text-[13px]">
          <div className="flex items-center gap-2">
            {s.tailnet_listen ? <><Badge tone="green">{t('已监听', 'Listening')}</Badge><span className="font-mono text-[12.5px]">{s.tailnet_listen}</span></>
              : s.tailnet_listen_error ? <><Badge tone="red">{t('未就绪', 'Not up')}</Badge><span className="text-[12.5px] text-fg2">{s.tailnet_listen_error}</span></>
              : <><Badge tone="zinc">{t('未配置', 'Not configured')}</Badge><span className="text-[12.5px] text-muted">{t('设置 ATOLL_AGENT_LISTEN=tailscale:7301 启用', 'Set ATOLL_AGENT_LISTEN=tailscale:7301 to enable')}</span></>}
          </div>
          <div className="flex items-center gap-2">
            {s.agent_public === false ? <Badge tone="green">{t('仅 tailnet', 'Tailnet only')}</Badge> : <Badge tone="amber"><AlertTriangle size={11} />{t('公网也可访问', 'Also public')}</Badge>}
            <span className="text-[12.5px] text-muted">{s.agent_public === false ? t('Agent 端点不在公网应答（bootstrap 除外）', 'Agent endpoints do not answer publicly (except bootstrap)') : t('Agent 端点也在公网应答；全部迁入后可设 ATOLL_AGENT_TAILNET_ONLY=1', 'Agent endpoints also answer publicly; set ATOLL_AGENT_TAILNET_ONLY=1 once every server is on the tailnet')}</span>
          </div>
        </div>
      </Row>
      <div className="px-6 py-4 flex items-center justify-end gap-3">
        {dirty && <span className="text-[12.5px] text-muted">{t('有未保存的修改', 'Unsaved changes')}</span>}
        <Button variant="primary" size="sm" disabled={!dirty || keyBad || urlBad || needKey || busy} onClick={save}>{busy ? <Loader2 size={14} className="animate-spin" /> : <Save size={14} />}{t('保存私有网络', 'Save private network')}</Button>
      </div>
    </Card>
  )
}
