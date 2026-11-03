import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { ArrowUpDown, LayoutGrid, List, AlertCircle, Server, Plus, Loader2, Clock } from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Badge, Button, ProviderIcon, Segment, Ticks } from '../components/ui'
import { CardSkeleton, CodeBlock, CopyButton, Empty, ErrorState, Modal } from '../components/feedback'
import { api } from '../api/client'
import { invalidate, useQuery } from '../api/hooks'
import { flagFor, gb, needsAttention, usagePct } from '../api/view'
import type { JoinToken } from '../api/types'
import { NetBadge } from '../components/ServerNetwork'
import { useT } from '../i18n'
import { ControlCard } from './Control'
import { AgentUpgradeAll, AgentUpgradeBadge } from '../components/AgentUpgrade'
import { cx } from '../components/ui-utils'

export default function Servers() {
  const { t } = useT()
  const q = useQuery('servers', api.servers, { interval: 30000 })
  const [sort, setSort] = useState(false)
  const [listView, setListView] = useState(false)
  const [add, setAdd] = useState(false)
  // the control host has its own card above the list and the 中控 page; it is not a server
  const list = (q.data ?? []).filter(s => s.role !== 'control').sort((a, b) => sort ? a.name.localeCompare(b.name) : 0)
  return (
    <MainShell>
      <div className="max-w-[1240px] mx-auto px-4 pt-6 pb-12 @2xl:px-8 @2xl:pt-10 @5xl:px-14 @5xl:pt-12 @5xl:pb-16">
        <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-4 mb-5">
          <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em]">我的服务器</h1>
          <div className="flex items-center gap-3">
            <button onClick={() => setSort(v => !v)} className={cx('h-11 w-11 grid place-items-center', sort ? 'text-accent' : 'text-fg2')}><ArrowUpDown size={18} /></button>
            <div className="flex rounded-lg field p-0.5">
              <button onClick={() => setListView(false)} className={cx('h-8 w-10 grid place-items-center rounded-md', !listView ? 'nav-active' : 'text-muted')}><LayoutGrid size={16} /></button>
              <button onClick={() => setListView(true)} className={cx('h-8 w-10 grid place-items-center rounded-md', listView ? 'nav-active' : 'text-muted')}><List size={16} /></button>
            </div>
            <Button variant="primary" size="lg" className="@3xl:w-[200px]" onClick={() => setAdd(true)}>添加服务器</Button>
          </div>
        </div>
        <ControlCard />
        <AgentUpgradeAll servers={list} />
        <Segment value="s" items={[{ k: 's', l: '服务器', icon: <Server size={15} /> }]} />
        <div className="mt-6">
          {q.error && !q.data && <ErrorState error={q.error} onRetry={q.refetch} />}
          {!q.data && !q.error && <CardSkeleton n={3} h={220} />}
          {q.data && !list.length && <Empty icon={<Server size={22} />} title="还没有服务器" desc="在任意 VPS 上运行一行安装命令，Agent 会主动连回中控，无需开放额外端口。" action={<Button variant="primary" onClick={() => setAdd(true)}><Plus size={15} />添加服务器</Button>} />}
        </div>
        <div className={cx('grid gap-4', listView ? 'grid-cols-1' : 'grid-cols-1 @2xl:grid-cols-2 @6xl:grid-cols-3')}>
          {list.map(s => {
            const u = usagePct(s), on = s.status === 'online'
            return (
              <Link key={s.id} to={`/servers/${s.id}`} className="rounded-2xl surface surface-hover overflow-hidden relative">
                <div className="absolute -right-6 top-6 w-48 h-36 dotgrid pointer-events-none" style={{ maskImage: 'radial-gradient(ellipse at 60% 50%, black 20%, transparent 70%)' }} />
                <div className="p-6 pb-5">
                  <div className="flex items-start gap-3">
                    <ProviderIcon provider={s.provider || '?'} online={on} />
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2 min-w-0"><span className="text-[15px] font-semibold truncate min-w-0">{s.name}</span>
                        {needsAttention(s) && <Badge><AlertCircle size={12} />需要处理</Badge>}
                        {s.role === 'control' && <Badge tone="violet">中控</Badge>}
                        {s.status === 'pending' && <Badge tone="zinc">等待连接</Badge>}
                        <AgentUpgradeBadge s={s} />
                      </div>
                      <div className="mt-1 flex items-center gap-3 text-[13.5px] text-fg2 whitespace-nowrap">
                        <span className="truncate">{s.os || '—'}</span>
                        <span className="flex items-center gap-1.5"><span className={on ? 'h-2 w-2 rounded-full bg-emerald-500' : 'h-2 w-2 rounded-full bg-zinc-500'} />{on ? 'Docker' : '离线'}</span>
                      </div>
                    </div>
                  </div>
                  <div className="mt-6 grid grid-cols-2 gap-x-4 text-[13px] [&>div]:min-w-0">
                    <div><div className="text-muted">IP 地址</div><div className="font-mono text-[13px] mt-1.5 truncate" title={s.public_ipv4}>{s.public_ipv4 || '—'}</div></div>
                    <div><div className="text-muted">区域</div><div className="mt-1.5 flex items-start gap-2 [overflow-wrap:anywhere]">{flagFor(s.region)} {s.region || '—'}</div></div>
                  </div>
                  <div className="mt-4 grid grid-cols-2 gap-x-4 text-[13px] [&>div]:min-w-0">
                    <div><div className="text-muted">{t('内网 IP', 'Tailnet IP')}</div><div className="font-mono text-[13px] mt-1.5 truncate">{s.tailnet_ip || '—'}</div></div>
                    <div><div className="text-muted">{t('管理通道', 'Agent channel')}</div><div className="mt-1.5"><NetBadge s={s} /></div></div>
                  </div>
                  <div className="mt-5 space-y-2.5 text-[13px]">
                    <div className="flex items-center gap-4"><span className="text-muted w-20 shrink-0 whitespace-nowrap">CPU 使用率</span><Ticks value={on ? u.cpu : 0} n={34} /></div>
                    <div className="flex items-center gap-4"><span className="text-muted w-20 shrink-0 whitespace-nowrap">内存使用率</span><Ticks value={on ? u.mem : 0} n={34} /></div>
                  </div>
                </div>
                <div className="border-t border-line bg-sunk/60 px-6 min-h-11 py-2.5 flex items-center text-[13.5px] text-muted">
                  {s.role === 'control' ? '仅运行面板与调度，不承载项目' : `${s.cpu_cores} vCPU · ${gb(s.mem_mb)} · ${s.disk_gb} GB · ${s.arch}`}
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
  const [t, setT] = useState<JoinToken>(); const [err, setErr] = useState<string>()
  useEffect(() => { api.joinToken().then(setT, e => setErr(e.message)) }, [])
  return (
    <Modal title="添加服务器" width={720} onClose={onClose} footer={<Button size="sm" variant="primary" onClick={onClose}>完成</Button>}>
      <div className="text-[13.5px] text-fg2 leading-6">在新 VPS 上以 root 运行下面这行命令。它会安装 Docker 和 Atoll Agent，Agent 主动连回中控，连上后服务器会出现在列表里。</div>
      <div className="mt-4">
        {err && <div className="text-[13px] text-[#ef4444]">{err}</div>}
        {!t && !err && <div className="h-[76px] rounded-lg field grid place-items-center text-muted"><Loader2 className="animate-spin" size={18} /></div>}
        {t && <>
          <div className="[&>div]:whitespace-pre-wrap [&>div]:break-all"><CodeBlock>{t.command}</CodeBlock></div>
          <div className="mt-3 flex items-center gap-3 text-[12.5px] text-muted"><Clock size={13} />一次性令牌，{new Date(t.expires_at).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })} 前有效<span className="ml-auto"><CopyButton text={t.command} label="复制命令" /></span></div>
        </>}
      </div>
      <div className="mt-5 rounded-lg border-2 border-dashed border-line2 px-4 py-3 text-[12.5px] text-muted leading-6">支持 Debian 12+/Ubuntu 22.04+，amd64 与 arm64。80/443 默认只放行 Cloudflare IP。</div>
    </Modal>
  )
}
