import { useState } from 'react'
import { ArrowUpCircle, Loader2 } from 'lucide-react'
import { api } from '../api/client'
import { invalidate } from '../api/hooks'
import { agentOutdated } from '../api/view'
import type { AgentUpgradeRow, Server } from '../api/types'
import { Badge, Button } from './ui'
import { CodeBlock, CopyButton, Modal, toast } from './feedback'
import { useT } from '../i18n'

const installCmd = () =>
  `curl -fsSL https://raw.githubusercontent.com/Sakuralaaa/Atoll/main/install/install-agent.sh | sudo sh -s -- --server ${location.origin}`

/** Status of the last upgrade, or "can upgrade" when outdated. */
export function AgentUpgradeBadge({ s }: { s: Server }) {
  const { t } = useT()
  const u = s.agent_upgrade
  if (u?.state === 'upgrading') return <Badge tone="violet"><Loader2 size={12} className="animate-spin" />{t('升级中', 'Upgrading')}</Badge>
  if (u?.state === 'failed') return <span title={u.error}><Badge tone="red">{u.rolled_back ? t('升级失败，已回滚', 'Failed, rolled back') : t('升级失败', 'Upgrade failed')}</Badge></span>
  if (agentOutdated(s)) return <Badge>{t(`可升级到 ${s.agent_latest}`, `${s.agent_latest} available`)}</Badge>
  return null
}

/** Button for one server: upgrades in place, or explains the one-time manual step for old agents. */
export function AgentUpgradeAction({ s }: { s: Server }) {
  const { t } = useT()
  const [busy, setBusy] = useState(false)
  const [manual, setManual] = useState(false)
  if (!agentOutdated(s) || s.status !== 'online' || s.agent_upgrade?.state === 'upgrading') return null
  if (!s.agent_self_upgrade) return <>
    <Button size="sm" onClick={() => setManual(true)}><ArrowUpCircle size={14} />{t('升级 Agent', 'Upgrade agent')}</Button>
    {manual && <ManualUpgrade names={[s.name]} onClose={() => setManual(false)} />}
  </>
  const go = async () => {
    setBusy(true)
    try {
      await api.upgradeAgent(s.id)
      toast(t('已开始升级，Agent 几秒后重启，容器不受影响', 'Upgrade started; the agent restarts in a few seconds, containers keep running'), 'ok')
      invalidate('servers', `server:${s.id}`)
    } catch (e) {
      toast((e as Error).message, 'error')
    } finally { setBusy(false) }
  }
  return <Button size="sm" disabled={busy} onClick={go}>{busy ? <Loader2 size={14} className="animate-spin" /> : <ArrowUpCircle size={14} />}{t(`升级到 ${s.agent_latest}`, `Upgrade to ${s.agent_latest}`)}</Button>
}

function ManualUpgrade({ names, onClose }: { names: string[]; onClose: () => void }) {
  const { t } = useT()
  const cmd = installCmd()
  return (
    <Modal title={t('这些 Agent 需要手动升级一次', 'These agents need one manual upgrade')} width={680} onClose={onClose} footer={<Button size="sm" variant="primary" onClick={onClose}>{t('知道了', 'OK')}</Button>}>
      <div className="text-[13.5px] text-fg2 leading-6">{t('v0.1.6 之前的 Agent 不支持在面板里升级。在下面这些服务器上以 root 运行一次这条命令，之后就能在面板里一键升级。已加入的服务器不需要 token，容器不会重建。', 'Agents before v0.1.6 cannot upgrade from the panel. Run this once as root on each server below; after that, upgrades work from the panel. Joined servers need no token and containers are not recreated.')}</div>
      <div className="mt-3 text-[13px] font-semibold [overflow-wrap:anywhere]">{names.join('、')}</div>
      <div className="mt-3 [&>div]:whitespace-pre-wrap [&>div]:break-all"><CodeBlock>{cmd}</CodeBlock></div>
      <div className="mt-3 flex justify-end"><CopyButton text={cmd} label={t('复制命令', 'Copy')} /></div>
    </Modal>
  )
}

/** Banner on the servers page: upgrade every outdated agent at once. */
export function AgentUpgradeAll({ servers }: { servers: Server[] }) {
  const { t } = useT()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [rows, setRows] = useState<AgentUpgradeRow[]>()
  const outdated = servers.filter(s => s.status === 'online' && agentOutdated(s) && s.agent_upgrade?.state !== 'upgrading')
  if (!outdated.length && !rows) return null
  const auto = outdated.filter(s => s.agent_self_upgrade), manual = outdated.filter(s => !s.agent_self_upgrade)
  const name = (id: string) => servers.find(s => s.id === id)?.name ?? id
  const go = async () => {
    setBusy(true)
    try {
      const r = await api.upgradeAgents(auto.map(s => s.id))
      setRows(r)
      invalidate('servers')
    } catch (e) {
      toast((e as Error).message, 'error')
    } finally { setBusy(false) }
  }
  const close = () => { setOpen(false); setRows(undefined) }
  return <>
    {outdated.length > 0 && <div className="mb-5 rounded-xl border border-line bg-card2/60 px-5 py-3.5 flex flex-wrap items-center gap-3 text-[13.5px]">
      <ArrowUpCircle size={16} className="text-accent shrink-0" />
      <span className="flex-1 min-w-0">{t(`${outdated.length} 台服务器的 Agent 可以升级到 ${outdated[0].agent_latest}`, `${outdated.length} agents can be upgraded to ${outdated[0].agent_latest}`)}</span>
      <Button size="sm" variant="primary" onClick={() => setOpen(true)}>{t('全部升级', 'Upgrade all')}</Button>
    </div>}
    {open && !rows && (auto.length ? (
      <Modal title={t('升级 Agent', 'Upgrade agents')} width={620} onClose={close} footer={<>
        <Button size="sm" onClick={close}>{t('取消', 'Cancel')}</Button>
        <Button size="sm" variant="primary" disabled={busy} onClick={go}>{busy && <Loader2 size={14} className="animate-spin" />}{t(`升级 ${auto.length} 台`, `Upgrade ${auto.length}`)}</Button>
      </>}>
        <div className="text-[13.5px] text-fg2 leading-6">{t('每台服务器下载中控托管的新版 Agent，校验 SHA-256 后替换并重启（每次 3 台）。新 Agent 两分钟内没连回中控就自动换回旧版。容器一直运行，不会重建。', 'Each server downloads the hosted agent, checks its SHA-256, swaps it in and restarts (3 at a time). If the new agent does not reconnect within two minutes the old one is restored. Containers keep running and are not recreated.')}</div>
        <div className="mt-3 text-[13px] [overflow-wrap:anywhere]">{auto.map(s => `${s.name} (${s.agent_version})`).join('、')}</div>
        {manual.length > 0 && <div className="mt-4 text-[12.5px] text-muted leading-5">{t(`另有 ${manual.length} 台是旧版 Agent（${manual.map(s => s.name).join('、')}），需要先手动运行一次安装脚本。`, `${manual.length} more run an old agent (${manual.map(s => s.name).join(', ')}) and need the install script once.`)}</div>}
      </Modal>
    ) : <ManualUpgrade names={manual.map(s => s.name)} onClose={close} />)}
    {rows && <Modal title={t('升级已开始', 'Upgrades started')} width={620} onClose={close} footer={<Button size="sm" variant="primary" onClick={close}>{t('完成', 'Done')}</Button>}>
      <div className="space-y-2 text-[13px]">
        {rows.map(r => <div key={r.id} className="flex items-start gap-3"><span className="font-semibold w-40 shrink-0 truncate">{name(r.id)}</span>
          {r.upgrade ? <span className="text-fg2">{r.upgrade.from} → {r.upgrade.to}</span> : <span className="text-[#ef4444] [overflow-wrap:anywhere]">{r.error}</span>}</div>)}
      </div>
      <div className="mt-4 text-[12.5px] text-muted">{t('结果会显示在每台服务器的卡片上。', 'Results show on each server card.')}</div>
    </Modal>}
  </>
}
