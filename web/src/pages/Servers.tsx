import { useEffect, useState } from 'react'
import { HideIPsButton, IP } from '../components/IP'
import { Link } from 'react-router-dom'
import { ArrowUpDown, LayoutGrid, List, AlertCircle, Server, Plus, Loader2, Clock, SearchX } from 'lucide-react'
import { ProblemsBanner } from '../components/Problems'
import { MainShell } from '../components/Shell'
import { Badge, Button, ProviderIcon, Ticks } from '../components/ui'
import { CardSkeleton, CodeBlock, CopyButton, Empty, ErrorState, Modal } from '../components/feedback'
import { api } from '../api/client'
import { invalidate, useQuery } from '../api/hooks'
import { gb, needsAttention, usagePct } from '../api/view'
import type { JoinToken } from '../api/types'
import { NetBadge } from '../components/ServerNetwork'
import { L, useT } from '../i18n'
import { ControlCard } from './Control'
import { AgentUpgradeAll, AgentUpgradeBadge } from '../components/AgentUpgrade'
import { cx } from '../components/ui-utils'
import { FilterChips } from '../components/StatusFilter'
import { serverFilters, useStatusFilter } from '../api/filters'
import { providerOf, countryOf, regionOf } from '../api/providers'
import { Flag } from '../components/Flag'
import { Latency } from '../components/Latency'
import { useAdmin, useProblems } from '../api/queries'

export default function Servers() {
  const { t } = useT()
  const q = useQuery('servers', api.servers, { interval: 30000 })
  const [sort, setSort] = useState(false)
  const [listView, setListView] = useState(false)
  const [add, setAdd] = useState(false)
  const admin = useAdmin() // members: no control card, no joining servers
  // the control host has its own card above the list and the 中控 page (an agent on the same box is listed as a normal server)
  const all = (q.data ?? []).filter(s => s.role !== 'control').sort((a, b) => sort ? a.name.localeCompare(b.name) : 0)
  const defs = serverFilters((useProblems().data ?? []).filter(p => !p.resolved_at))
  const f = useStatusFilter(all, defs)
  const list = f.list
  return (
    <MainShell>
      <div className="max-w-[1240px] mx-auto px-4 pt-6 pb-12 @2xl:px-8 @2xl:pt-10 @5xl:px-14 @5xl:pt-12 @5xl:pb-16">
        <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-4 mb-5">
          <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em]">{t('我的服务器', 'My servers')}</h1>
          <div className="flex items-center gap-3">
            <HideIPsButton />
            <button onClick={() => setSort(v => !v)} title={t('按名称排序', 'Sort by name')} className={cx('h-11 w-11 grid place-items-center', sort ? 'text-accent' : 'text-fg2')}><ArrowUpDown size={18} /></button>
            <div className="flex rounded-lg field p-0.5">
              <button onClick={() => setListView(false)} className={cx('h-8 w-10 grid place-items-center rounded-md', !listView ? 'nav-active' : 'text-muted')}><LayoutGrid size={16} /></button>
              <button onClick={() => setListView(true)} className={cx('h-8 w-10 grid place-items-center rounded-md', listView ? 'nav-active' : 'text-muted')}><List size={16} /></button>
            </div>
            {admin && <Button variant="primary" size="lg" className="@3xl:w-[200px]" onClick={() => setAdd(true)}>{t('添加服务器', 'Add server')}</Button>}
          </div>
        </div>
        <ProblemsBanner scope="servers" />
        {admin && <ControlCard />}
        <AgentUpgradeAll servers={all} />
        <div className="mt-6">
          {q.error && !q.data && <ErrorState error={q.error} onRetry={q.refetch} />}
          {!q.data && !q.error && <CardSkeleton n={3} h={220} />}
          {q.data && all.length > 0 && <FilterChips f={f} defs={defs} className="mb-5" />}
          {q.data && all.length > 0 && !list.length && f.def && <Empty icon={<SearchX size={22} />} title={t(`没有「${f.def.zh}」的服务器`, `No servers match “${f.def.en}”`)}
            action={<Button onClick={() => f.set('')}>{t('显示全部', 'Show all')}</Button>} />}
          {q.data && !all.length && <Empty icon={<Server size={22} />} title={t('还没有服务器', 'No servers yet')} desc={admin ? t('在任意 VPS 上运行一行安装命令，Agent 会主动连回中控，无需开放额外端口。', 'Run one install command on any VPS; the agent connects back to the control server, no extra ports to open.') : t('管理员还没有给你分配服务器。', 'An admin has not assigned you a server yet.')} action={admin && <Button variant="primary" onClick={() => setAdd(true)}><Plus size={15} />{t('添加服务器', 'Add server')}</Button>} />}
        </div>
        <div className={cx('grid gap-4', listView ? 'grid-cols-1' : 'grid-cols-1 @2xl:auto-rows-fr @2xl:grid-cols-2 @6xl:grid-cols-3')}>
          {list.map(s => {
            const u = usagePct(s), on = s.status === 'online'
            return (
              <Link key={s.id} to={`/servers/${s.id}`} className="rounded-2xl surface surface-hover overflow-hidden relative flex flex-col">
                <div className="absolute -right-6 top-6 w-48 h-36 dotgrid pointer-events-none" style={{ maskImage: 'radial-gradient(ellipse at 60% 50%, black 20%, transparent 70%)' }} />
                <div className="p-6 pb-5">
                  <div className="flex items-start gap-3">
                    <ProviderIcon provider={providerOf(s)} online={on} />
                    <div className="min-w-0 flex-1">
                      {/* full name, wrapping; badges live in the footer so nothing squeezes it */}
                      <div className="text-[15px] font-semibold leading-snug [overflow-wrap:anywhere]">{s.name}</div>
                      <div className="mt-1 flex items-center gap-1.5 text-[13px] text-fg2"><span className={on ? 'h-2 w-2 rounded-full bg-emerald-500' : s.status === 'offline' ? 'h-2 w-2 rounded-full bg-[#ef4444]' : 'h-2 w-2 rounded-full bg-zinc-500'} />{on ? t('在线 · Docker', 'Online · Docker') : s.status === 'offline' ? <span className="text-[#ef4444] font-semibold">{t('离线', 'Offline')}</span> : s.status === 'pending' ? t('等待连接', 'Waiting') : t('离线', 'Offline')}</div>
                      <div className="mt-0.5 text-[12.5px] text-muted [overflow-wrap:anywhere]" title={t('系统版本', 'OS')}>{s.os || '—'}</div>
                    </div>
                  </div>
                  <div className="mt-5 grid grid-cols-2 gap-x-4 text-[13px] [&>div]:min-w-0">
                    <div><div className="text-muted">{t('IP 地址', 'IP address')}</div><div className="font-mono text-[13px] mt-1.5 truncate"><IP v={s.public_ipv4} /></div></div>
                    <div><div className="text-muted">{t('区域', 'Region')}</div><div className="mt-1.5 flex items-start gap-2 [overflow-wrap:anywhere]"><Flag code={countryOf(s)} className="mt-[3px]" />{regionOf(s) || '—'}</div></div>
                  </div>
                  <div className="mt-4 grid grid-cols-2 gap-x-4 text-[13px] [&>div]:min-w-0">
                    <div><div className="text-muted">{t('内网 IP', 'Tailnet IP')}</div><div className="font-mono text-[13px] mt-1.5 truncate"><IP v={s.tailnet_ip} /></div></div>
                    <div><div className="text-muted">{t('管理通道', 'Agent channel')}</div><div className="mt-1.5"><NetBadge s={s} /></div></div>
                  </div>
                  <div className="mt-5 space-y-2.5 text-[13px]">
                    <div className="flex items-center gap-4"><span className="text-muted w-20 shrink-0 whitespace-nowrap">{t('CPU 使用率', 'CPU')}</span><Ticks value={on ? u.cpu : 0} n={34} /></div>
                    <div className="flex items-center gap-4"><span className="text-muted w-20 shrink-0 whitespace-nowrap">{t('内存使用率', 'Memory')}</span><Ticks value={on ? u.mem : 0} n={34} /></div>
                  </div>
                </div>
                <div className="mt-auto border-t border-line bg-sunk/60 px-6 min-h-11 py-2.5 flex flex-wrap items-center gap-x-3 gap-y-2 text-[13.5px] text-muted">
                  <span className="min-w-0">{`${s.cpu_cores} vCPU · ${gb(s.mem_mb)} · ${s.disk_gb} GB · ${s.arch}`}</span>
                  <Latency s={s} className="text-[13px]" />
                  <span className="ml-auto flex flex-wrap items-center justify-end gap-1.5 empty:hidden">
                    {needsAttention(s) && <Badge><AlertCircle size={12} />{t('需要处理', 'Needs attention')}</Badge>}
                    <AgentUpgradeBadge s={s} />
                  </span>
                </div>
              </Link>
            )
          })}
        </div>
      </div>
      {add && <AddServer onClose={() => { setAdd(false); invalidate('servers') }} />}
    </MainShell>
  )
}

function AddServer({ onClose }: { onClose: () => void }) {
  const { t: tr } = useT()
  const [t, setT] = useState<JoinToken>(); const [err, setErr] = useState<string>()
  useEffect(() => { api.joinToken().then(setT, e => setErr(e.message)) }, [])
  return (
    <Modal title={tr(L("添加服务器"), 'Add server')} width={720} onClose={onClose} footer={<Button size="sm" variant="primary" onClick={onClose}>{tr(L("完成"), 'Done')}</Button>}>
      <div className="text-[13.5px] text-fg2 leading-6">{tr(L("在新 VPS 上以 root 运行下面这行命令。它会安装 Docker 和 Atoll Agent，Agent 主动连回中控，连上后服务器会出现在列表里。"), 'Run this as root on the new VPS. It installs Docker and the Atoll agent; the agent connects back to the control server and the server shows up in the list.')}</div>
      <div className="mt-4">
        {err && <div className="text-[13px] text-[#ef4444]">{err}</div>}
        {!t && !err && <div className="h-[76px] rounded-lg field grid place-items-center text-muted"><Loader2 className="animate-spin" size={18} /></div>}
        {t && <>
          <div className="[&>div]:whitespace-pre-wrap [&>div]:break-all"><CodeBlock>{t.command}</CodeBlock></div>
          <div className="mt-3 flex items-center gap-3 text-[12.5px] text-muted"><Clock size={13} />{(() => { const at = new Date(t.expires_at).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' }); return tr(L("一次性令牌，{0} 前有效", at), `One-time token, valid until ${at}`) })()}<span className="ml-auto"><CopyButton text={t.command} label={tr(L("复制命令"), 'Copy')} /></span></div>
        </>}
      </div>
      <div className="mt-5 rounded-lg border-2 border-dashed border-line2 px-4 py-3 text-[12.5px] text-muted leading-6">{tr(L("支持 Debian 12+/Ubuntu 22.04+，amd64 与 arm64。80/443 默认只放行 Cloudflare IP。"), 'Debian 12+ / Ubuntu 22.04+, amd64 and arm64. Ports 80/443 accept only Cloudflare IPs by default.')}</div>
    </Modal>
  )
}
