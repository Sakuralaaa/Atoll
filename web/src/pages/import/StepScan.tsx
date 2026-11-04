import { useState } from 'react'
import { IP } from '../../components/IP'
import { Server as ServerIcon, ScanSearch, ListChecks, Loader2, HardDrive, MemoryStick, Cpu, AlertTriangle, RotateCcw, Box, ArrowRight } from 'lucide-react'
import { Button, Card, ProviderIcon, ServiceIcon } from '../../components/ui'
import { CodeBlock, CopyButton, Empty, ErrorState, RowsSkeleton, Skeleton } from '../../components/feedback'
import { api } from '../../api/client'
import { run, useBusy, useQuery } from '../../api/hooks'
import { bytes, colorFor, gb, letterFor, usagePct } from '../../api/view'
import { Flag } from '../../components/Flag'
import type { JoinToken, Server, ZeaburInventory } from '../../api/types'
import { cx } from '../../components/ui-utils'
import { ZRow } from './zeabur'
import { countryOf, providerOf, regionOf } from '../../api/providers'
import { projBytes, useScan } from './model'
import { useBackendText } from '../../api/backendText'

// ---------------- step 1 ----------------
export function StepServer({ onPick }: { onPick: (id: string) => void }) {
  const q = useQuery('servers', api.servers)
  const list = (q.data ?? []).filter(s => s.role !== 'control')
  const [tok, setTok] = useState<JoinToken>(); const [busy, wrap] = useBusy()
  return (
    <div className="grid grid-cols-1 @5xl:grid-cols-[minmax(0,1fr)_420px] gap-6 items-start [&>*]:min-w-0">
      <div>
        <div className="text-[15px] font-semibold mb-1">Zeabur 托管的是哪台服务器？</div>
        <div className="text-[13px] text-muted mb-4">选择已经装好 Atoll Agent 的那台 VPS，下一步会读取本机 k3s 里的 Zeabur 项目。只读，不会改动任何东西。</div>
        {q.error && <ErrorState error={q.error} onRetry={q.refetch} />}
        {!q.data && !q.error && <RowsSkeleton n={3} />}
        {q.data && !list.length && <Empty icon={<ServerIcon size={22} />} title="还没有连上的服务器" desc="先在右侧生成安装命令，在 Zeabur 托管的 VPS 上运行。" />}
        <div className="space-y-3">
          {list.map(s => <ServerPick key={s.id} s={s} onPick={() => onPick(s.id)} />)}
        </div>
      </div>
      <Card className="p-6">
        <div className="text-[13px] font-semibold flex items-center gap-2"><Box size={16} />还没装 Agent？</div>
        <div className="text-[13px] text-muted mt-1.5 leading-6">在 Zeabur 托管的 VPS 上以 root 运行安装命令。Agent 与 Zeabur 的 k3s 并存，不占 80/443，直到你确认切换。</div>
        <div className="mt-4">
          {tok ? <>
            <div className="[&>div]:whitespace-pre-wrap [&>div]:break-all"><CodeBlock>{tok.command}</CodeBlock></div>
            <div className="mt-3 flex items-center text-[12px] text-dim">一次性令牌 · {new Date(tok.expires_at).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })} 前有效<span className="ml-auto"><CopyButton text={tok.command} label="复制命令" /></span></div>
          </> : <Button variant="primary" disabled={busy} onClick={() => wrap(async () => { const t = await run(api.joinToken); if (t) setTok(t) })}>{busy && <Loader2 size={14} className="animate-spin" />}生成安装命令</Button>}
        </div>
      </Card>
    </div>
  )
}
function ServerPick({ s, onPick }: { s: Server; onPick: () => void }) {
  const u = usagePct(s), on = s.status === 'online'
  return (
    <button disabled={!on} onClick={onPick} className={cx('w-full text-left rounded-2xl surface px-4 @2xl:px-5 py-4 flex flex-wrap @3xl:flex-nowrap items-center gap-x-4 gap-y-3', on ? 'surface-hover' : 'opacity-60')}>
      <ProviderIcon provider={providerOf(s)} online={on} />
      <div className="flex-1 min-w-[min(100%,180px)]">
        <div className="text-[15px] font-semibold truncate">{s.name}</div>
        <div className="text-[13px] text-muted mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-0.5"><IP className="font-mono" v={s.public_ipv4} />{regionOf(s) && <span className="inline-flex items-center gap-1.5"><Flag code={countryOf(s)} size={12} />{regionOf(s)}</span>}<span>{s.cpu_cores} vCPU · {gb(s.mem_mb)} · {s.arch}</span></div>
      </div>
      <div className="@3xl:text-right text-[12.5px] text-muted tnum shrink-0 flex @3xl:flex-col gap-x-3 gap-y-0.5 whitespace-nowrap">{on ? <><span>内存 {u.mem}%</span><span>硬盘 {u.disk}%</span></> : '离线'}</div>
      <span className="btn-secondary h-9 px-3.5 inline-flex items-center gap-1.5 text-[13px] shrink-0 ml-auto">扫描<ArrowRight size={14} /></span>
    </button>
  )
}

// ---------------- step 2 ----------------

export function StepScan({ serverId, onPick }: { serverId: string; onPick: (ns: string) => void }) {
  const q = useScan(serverId)
  const srv = useQuery('servers', api.servers).data?.find(s => s.id === serverId)
  return (
    <>
      <div className="flex items-center justify-between mb-4">
        <div><div className="text-[15px] font-semibold">在 {srv?.name ?? serverId} 上找到的 Zeabur 项目</div><div className="text-[13px] text-muted mt-0.5">读取本机 k3s 的 Deployment / StatefulSet / PVC / Ingress，环境变量中的密钥已隐藏。</div></div>
        <Button onClick={q.refetch}><RotateCcw size={14} />重新扫描</Button>
      </div>
      {q.error && <ErrorState error={q.error} onRetry={q.refetch} />}
      {!q.data && !q.error && <ScanSkeleton />}
      {q.data && <ScanResult inv={q.data} onPick={onPick} />}
    </>
  )
}
export function ScanSkeleton() {
  return <div className="space-y-5"><div className="grid grid-cols-2 @4xl:grid-cols-4 gap-4">{[0, 1, 2, 3].map(i => <Card key={i} className="p-5"><Skeleton className="h-3 w-20" /><Skeleton className="h-6 w-28 mt-3" /></Card>)}</div><RowsSkeleton n={3} /></div>
}
function Stat({ i, l, v, sub, warn }: { i: React.ReactNode; l: string; v: React.ReactNode; sub?: string; warn?: boolean }) {
  return <Card className={cx('px-5 py-4', warn && '!bg-[#fcd34d] text-[#141414]')}><div className={cx('flex items-center gap-1.5 text-[12.5px]', warn ? '' : 'text-muted')}>{i}{l}</div><div className="text-[20px] font-extrabold italic tracking-[-0.02em] mt-1 tnum [overflow-wrap:anywhere]">{v}</div>{sub && <div className={cx('text-[12px] mt-0.5', warn ? '' : 'text-dim')}>{sub}</div>}</Card>
}
function ScanResult({ inv, onPick }: { inv: ZeaburInventory; onPick: (ns: string) => void }) {
  const { bt } = useBackendText()
  const total = inv.projects.reduce((a, p) => a + projBytes(p), 0)
  if (!inv.k3s) return <Empty icon={<ScanSearch size={22} />} title="这台服务器上没有 Zeabur" desc="没有检测到 k3s。确认选择的是 Zeabur 托管的那台 VPS，或者用下方「高级：用 CLI 导入」。" />
  return (
    <>
      <div className="grid grid-cols-1 @md:grid-cols-2 @4xl:grid-cols-4 gap-4 [&>*]:min-w-0">
        <Stat i={<Cpu size={14} />} l="Zeabur k3s" v={inv.k3s_version ?? '已安装'} sub={inv.ingress_running ? '入口控制器运行中 · 占用 80/443' : '入口控制器未运行'} />
        <Stat i={<HardDrive size={14} />} l="可用硬盘" v={`${inv.disk_free_gb.toFixed(1)} GB`} sub={`演练需要约 ${bytes(total * 1.2)}（数据 ×1.2）`} warn={inv.disk_free_gb * 2 ** 30 < total * 1.2} />
        <Stat i={<MemoryStick size={14} />} l="可用内存" v={gb(inv.mem_free_mb)} sub="演练期间两套服务同时运行" warn={inv.mem_free_mb < 1024} />
        <Stat i={<ListChecks size={14} />} l="发现" v={`${inv.projects.length} 个项目`} sub={`${inv.projects.reduce((a, p) => a + p.services.length, 0)} 个服务 · 数据 ${bytes(total)}`} />
      </div>
      <div className="mt-6 space-y-6">
        {inv.projects.map(p => (
          <Card key={p.namespace} className="overflow-hidden">
            <div className="px-4 @2xl:px-6 py-4 flex flex-wrap items-center gap-x-4 gap-y-3 border-b-2 border-[var(--edge)] bg-card2">
              <ServiceIcon s={{ kind: 'image', letter: letterFor(p.project_name), color: colorFor(p.project_name) }} size={36} />
              <div className="flex-1 min-w-[min(100%,180px)]"><div className="text-[15px] font-semibold [overflow-wrap:anywhere]">{p.project_name} <span className="text-[12.5px] text-muted font-normal ml-1">· {p.environment_name}</span></div><div className="font-mono text-[11.5px] text-dim mt-0.5 truncate">{p.namespace}</div></div>
              <span className="text-[13px] text-muted">{p.services.length} 个服务 · {bytes(projBytes(p))}</span>
              <Button variant="primary" onClick={() => onPick(p.namespace)}>迁移这个项目<ArrowRight size={14} /></Button>
            </div>
            {!!p.warnings?.length && (
              <div className="px-4 @2xl:px-6 py-3 border-b border-line bg-[#fcd34d]/20 space-y-1">
                {p.warnings.map(w => <div key={w} className="flex items-start gap-1.5 text-[12.5px] text-fg"><AlertTriangle size={13} className="text-[#d97706] mt-0.5 shrink-0" /><span className="min-w-0 break-words">{bt(w)}</span></div>)}
              </div>
            )}
            <div className="divide-y divide-line">{p.services.map(s => <ZRow key={s.service_id} s={s} names={Object.fromEntries(p.services.map(x => [x.service_id, x.name]))} />)}</div>
          </Card>
        ))}
      </div>
    </>
  )
}
