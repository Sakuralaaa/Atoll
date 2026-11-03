import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Globe, Loader2, Lock, Network } from 'lucide-react'
import { Badge, Button } from './ui'
import { Modal } from './feedback'
import { api } from '../api/client'
import { run, useBusy, useQuery } from '../api/hooks'
import type { NetworkResult, Server, ServerNetworkRequest } from '../api/types'
import { useT } from '../i18n'
import { onTailnet } from './net'

/** 内网 / 公网 badge for the agent's management channel. */
export function NetBadge({ s }: { s: Server }) {
  const { t } = useT()
  if (onTailnet(s)) return <Badge tone="green"><Lock size={11} strokeWidth={2.5} />{t('内网', 'Tailnet')}</Badge>
  return <Badge tone="zinc"><Globe size={11} strokeWidth={2.5} />{t('公网', 'Public')}</Badge>
}

/** Join / switch / leave the tailnet for one server (POST /servers/{id}/network). */
export function ServerNetworkAction({ s }: { s: Server }) {
  const { t } = useT()
  const st = useQuery('settings', api.settings).data
  const [busy, wrap] = useBusy()
  const [res, setRes] = useState<NetworkResult>()
  const [ask, setAsk] = useState<ServerNetworkRequest['action']>()
  const enabled = st?.network_provider === 'tailscale'
  const online = s.status === 'online'
  const action: ServerNetworkRequest['action'] = onTailnet(s) ? 'public' : s.tailnet_ip ? 'switch' : 'join'
  const label = { join: t('加入内网', 'Join tailnet'), switch: t('切换到内网', 'Switch to tailnet'), public: t('切回公网', 'Back to public') }[action]
  const desc = {
    join: t('在这台服务器上安装 Tailscale（如未安装），用保存的 auth key 加入 tailnet，验证通过后把 Agent 切到内网地址；公网地址保留为备用。可能需要几分钟。',
      'Installs Tailscale if missing, joins the tailnet with the stored auth key, then moves the agent to the tailnet URL after verifying it. The public URL stays as a fallback. May take a few minutes.'),
    switch: t('这台服务器已在 tailnet 上。Agent 会先验证内网地址，再切换管理通道；公网地址保留为备用。', 'This server is already on the tailnet. The agent verifies the tailnet URL before switching; the public URL stays as a fallback.'),
    public: t('Agent 改回经公网地址连接中控。跨服务器的服务互通仍需要 tailnet。', 'The agent reconnects through the public URL. Cross-server service traffic still needs the tailnet.'),
  }[action]
  const blocked = !online ? t('服务器离线', 'Server is offline') : action !== 'public' && st && !enabled ? t('先在设置里启用 Tailscale', 'Enable Tailscale in Settings first') : action === 'join' && st && !st.has_tailscale_auth_key ? t('先在设置里保存 auth key', 'Save an auth key in Settings first') : ''
  const go = () => wrap(async () => {
    const r = await run(() => api.serverNetwork(s.id, action), { invalidate: ['servers', `server:${s.id}`] })
    if (r) setRes(r)
    setAsk(undefined)
  })
  return (
    <>
      <div className="flex items-center gap-3">
        {blocked && <span className="text-[12.5px] text-muted">{blocked}{action !== 'public' && online && <> · <Link to="/settings#network" className="underline text-fg">{t('去设置', 'Settings')}</Link></>}</span>}
        <Button size="sm" variant={action === 'public' ? 'secondary' : 'primary'} disabled={!!blocked || busy} onClick={() => setAsk(action)}>{busy ? <Loader2 size={14} className="animate-spin" /> : <Network size={14} />}{label}</Button>
      </div>
      {ask && <Modal title={<><Network size={17} />{label}</>} onClose={() => !busy && setAsk(undefined)}
        footer={<><Button size="sm" disabled={busy} onClick={() => setAsk(undefined)}>{t('取消', 'Cancel')}</Button><Button size="sm" variant="primary" disabled={busy} onClick={go}>{busy && <Loader2 size={14} className="animate-spin" />}{label}</Button></>}>
        <div className="text-[13.5px] text-fg2 leading-6"><b>{s.name}</b>：{desc}</div>
      </Modal>}
      {res && <Modal title={<><Network size={17} />{res.switched ? t('已切换', 'Switched') : t('未切换', 'Not switched')}</>} onClose={() => setRes(undefined)} footer={<Button size="sm" variant="primary" onClick={() => setRes(undefined)}>{t('好', 'OK')}</Button>}>
        <div className="space-y-2 text-[13px]">
          <div className="flex justify-between"><span className="text-muted">{t('管理通道', 'Control URL')}</span><span className="font-mono">{res.control_url || '—'}</span></div>
          <div className="flex justify-between"><span className="text-muted">Tailnet IP</span><span className="font-mono">{res.tailnet_ip || '—'}</span></div>
          <div className="flex justify-between"><span className="text-muted">{t('状态', 'State')}</span><span>{res.state || '—'}</span></div>
          {res.message && <div className="mt-3 rounded-lg border-2 border-dashed border-line2 px-3 py-2 text-fg2">{res.message}</div>}
        </div>
      </Modal>}
    </>
  )
}
