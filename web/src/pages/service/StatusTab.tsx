import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Square, Play, Globe, FolderOpen, ScrollText, SquareTerminal, ExternalLink, Plus, Cloud, Box, History, Undo2, GitCommitHorizontal } from 'lucide-react'
import { Button, Card, Dot, Mini, Spark } from '../../components/ui'
import { ConfirmDialog, Label, Modal, RowsSkeleton, Select, Skeleton, TextInput, Toggle } from '../../components/feedback'
import ImageUpdateCard from '../../components/ImageUpdateCard'
import { imageVersionLabel, splitImage, useImageUpdate } from '../../api/updates'
import { BuildLogModal, CommitLink, GithubIcon, SourceCard } from '../../components/GitSource'
import { commitUrl, hasBuildLog, repoPath, shortSha } from '../../api/git'
import { api, isDemo } from '../../api/client'
import { run, useBusy, useQuery } from '../../api/hooks'
import { ago, dateTime, deployStatusText, latestDeploy, shortDigests, triggerText } from '../../api/view'
import type { Deployment, Project, Server, Service } from '../../api/types'
import { DomainBadge } from '../../components/DomainStatus'
import { cx, statusText } from '../../components/ui-utils'
import { providerOf } from '../../api/providers'
import { useT } from '../../i18n'

export function StatusTab({ p, s, srv }: { p: Project; s: Service; srv?: Server }) {
  const base = `/projects/${p.id}/services/${s.id}`
  const deps = useQuery(`deployments:${s.id}`, () => api.deployments(s.id))
  const m = useQuery(`svc-metrics:${s.id}:1h`, () => api.serviceMetrics(s.id, '1h'), { interval: 60000 })
  const last = latestDeploy(deps.data)
  const [hist, setHist] = useState(false)
  const [bind, setBind] = useState(false)
  const [stop, setStop] = useState(false)
  const [applyDef, setApplyDef] = useState(false)
  const { t } = useT()
  // Default limits from Settings, offered for a service created without a memory limit (no
  // global read permission = no button).
  const st = useQuery(s.mem_limit_mb ? null : 'settings', api.settings)
  const defMem = st.data?.default_mem_limit_mb ?? 0, defCpu = st.data?.default_cpu_limit ?? 0
  const offerDefaults = !s.mem_limit_mb && defMem > 0
  const [busy, wrap] = useBusy()
  const stopped = s.status === 'stopped'
  const cpu = (m.data ?? []).slice(-30).map(x => x.cpu_pct), mem = (m.data ?? []).slice(-30).map(x => x.mem_mb)
  const doms = s.domains ?? []
  return (
    <>
      <Card className="p-4 @2xl:p-6">
        <div className="flex flex-col @5xl:flex-row @5xl:items-start gap-6 @5xl:gap-8">
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-3"><Dot status={s.status} /><span className="text-[17px] font-semibold tracking-tight">{statusText[s.status] ?? s.status}</span><span className="text-muted text-[13px]">· {s.replicas}</span></div>
            <div className="text-[13px] text-muted mt-1 [overflow-wrap:anywhere]" title={last?.message}>{last ? <>最近一次部署 {ago(last.started_at)} · 由「{triggerText[last.trigger] ?? last.trigger}」触发{last.message ? ` · ${shortDigests(last.message)}` : ''}</> : deps.data ? '还没有部署记录' : <Skeleton className="h-3.5 w-64" />}</div>
            <div className="mt-6 grid grid-cols-[84px_minmax(0,1fr)] @2xl:grid-cols-[100px_minmax(0,1fr)] gap-y-3.5 text-[13px]">
              {s.git ? <>
                <span className="text-muted">镜像来源</span><span className="flex items-center gap-2 font-mono text-[13px] min-w-0"><GithubIcon size={14} /><span className="truncate" title={`${s.git.repo}@${s.git.branch || 'main'}`}>{repoPath(s.git.repo)}@{s.git.branch || 'main'}</span><span className="font-sans text-[12px] text-dim shrink-0">源码构建</span></span>
                <span className="text-muted">提交</span><span className="font-mono text-[13px] text-fg2">{s.git.last_commit ? <a href={commitUrl(s.git.repo, s.git.last_commit)} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1.5 hover:underline"><GitCommitHorizontal size={14} className="text-accent" />{shortSha(s.git.last_commit)}</a> : '—'}</span>
              </> : <>
                <span className="text-muted">镜像来源</span><span className="flex items-center gap-2 font-mono text-[13px] min-w-0"><Box size={14} className="text-[#60a5fa] shrink-0" /><ImageRef s={s} /></span>
                <span className="text-muted">镜像摘要</span><span className="font-mono text-[13px] text-fg2 truncate" title={last?.image_digest}>{last?.image_digest ? `${last.image_digest.slice(0, 15)}…${last.image_digest.slice(-4)}` : '—'}</span>
              </>}
              <span className="text-muted">服务器</span><span className="flex items-center gap-2 min-w-0">{srv ? <><Mini provider={providerOf(srv)} /><span className="truncate" title={srv.name}>{srv.name}</span></> : '—'}</span>
              <span className="text-muted">资源</span><span className="[overflow-wrap:anywhere]">CPU {s.cpu_limit ? `${s.cpu_limit} 核` : '不限'} · 内存 {s.mem_limit_mb ? `${s.mem_limit_mb} MB` : <span className="text-amber-500" title={t('没有内存上限：这个服务内存泄漏时可能拖垮整台机器。可在服务「设置」里填写上限，或套用默认上限。', 'No memory limit: a memory leak in this service can take the whole server down. Set one in the service settings or apply the default limits.')}>{t('未限制', 'unlimited')}</span>}{offerDefaults && <button type="button" className="ml-1.5 text-accent hover:underline disabled:opacity-50" disabled={busy} onClick={() => setApplyDef(true)}>{t('套用默认值', 'Apply defaults')}</button>} · 进程数上限 4096 · 重启策略 unless-stopped</span>
            </div>
          </div>
          <div className="w-full @5xl:w-[380px] @5xl:shrink-0 grid grid-cols-1 @2xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)] @5xl:grid-cols-1 gap-3">
            {isDemo() ? <Uptime failed={s.status === 'failed'} /> : <RecentDeploys deps={deps.data} />}
            <div className="grid grid-cols-2 gap-3">
              {([['CPU', cpu.length ? `${cpu[cpu.length - 1].toFixed(1)}%` : '—', cpu, '#8b5cf6'], ['内存', mem.length ? `${Math.round(mem[mem.length - 1])} MB` : '—', mem, '#38bdf8']] as const).map(([l, v, d, c]) => (
                <div key={l} className="rounded-xl field px-4 py-3">
                  <div className="text-[12px] text-muted">{l}</div>
                  <div className="flex items-end justify-between gap-2 mt-1 min-w-0"><span className="text-[17px] font-semibold tnum whitespace-nowrap">{v}</span><span className="min-w-0 overflow-hidden"><Spark data={[...d]} color={c} /></span></div>
                </div>
              ))}
            </div>
          </div>
        </div>
        <div className="mt-6 pt-5 border-t border-line flex flex-wrap items-center gap-2">
          <Button size="sm" onClick={() => setHist(true)}><History size={14} />部署记录</Button>
          {stopped ? <Button size="sm" disabled={busy} onClick={() => wrap(() => run(() => api.action(s.id, 'start'), { ok: '正在启动', invalidate: [`project:${p.id}`] }))}><Play size={13} />启动</Button>
            : <Button size="sm" disabled={busy} onClick={() => setStop(true)}><Square size={13} />停止</Button>}
          <span className="ml-auto text-[13px] text-dim @max-md:hidden">回滚到任意历史镜像摘要</span>
        </div>
      </Card>

      {stop && <ConfirmDialog title="停止服务" action="停止" onClose={() => setStop(false)}
        description={<>停止 {s.name} 的容器，域名访问会中断，数据卷保留，可随时再启动。</>}
        onConfirm={() => wrap(() => run(() => api.action(s.id, 'stop'), { ok: '已停止', invalidate: [`project:${p.id}`] }))} />}
      {applyDef && <ConfirmDialog title={t('套用默认资源上限', 'Apply default limits')} action={t('套用并部署', 'Apply and deploy')} danger={false} onClose={() => setApplyDef(false)}
        description={t(`把 ${s.name} 的内存上限设为 ${defMem} MB${!s.cpu_limit && defCpu > 0 ? `、CPU 上限设为 ${defCpu} 核` : ''}（中控设置里的默认值），保存后会重新部署。内存用量超出上限时容器会被 OOM 重启，请确认这个服务平时用不到这么多。`,
          `Set ${s.name}'s memory limit to ${defMem} MB${!s.cpu_limit && defCpu > 0 ? ` and its CPU limit to ${defCpu} cores` : ''} (the defaults from Settings) and redeploy. Above the limit the container is OOM-restarted, so make sure the service normally stays below it.`)}
        onConfirm={() => wrap(() => run(() => api.updateService(s.id, { mem_limit_mb: defMem, ...(!s.cpu_limit && defCpu > 0 ? { cpu_limit: defCpu } : {}) }), { ok: t('已套用，正在重新部署', 'Applied, redeploying'), invalidate: [`project:${p.id}`, 'projects'] }))} />}
      <SourceCard s={s} />
      <ImageUpdateCard s={s} />

      <div className="mt-6 text-[13px] font-semibold mb-3">域名</div>
      {doms.length ? <div className="space-y-3">{doms.map(d => (
        <Card key={d.id} className="px-4 @2xl:px-6 py-4 flex flex-wrap items-center gap-x-4 gap-y-2">
          <Globe size={18} className="text-accent shrink-0" />
          <a href={`https://${d.hostname}`} target="_blank" rel="noreferrer" className="text-[15px] font-semibold flex items-center gap-1.5 hover:underline min-w-0 [overflow-wrap:anywhere]">{d.hostname}<ExternalLink size={13} className="shrink-0" /></a>
          <DomainBadge d={d} />
          <span className="ml-auto flex flex-wrap items-center gap-1.5 text-[13px] text-muted">{d.proxied ? <><Cloud size={14} className="text-[#f6821f]" />Cloudflare 代理</> : '仅 DNS'} → {srv?.public_ipv4 ?? '—'}</span>
        </Card>
      ))}</div> : <Card className="px-6 py-5 text-[13px] text-muted flex flex-wrap gap-3 items-center justify-between">此服务未公开访问，仅项目内网可达<Button size="sm" disabled={!s.ports?.some(x => x.protocol === 'http')} onClick={() => setBind(true)}><Plus size={14} />绑定域名</Button></Card>}

      <div className="mt-6 grid grid-cols-1 @4xl:grid-cols-3 gap-5">
        {([
          [FolderOpen, '文件', '浏览容器与卷中的文件', `${base}/files`],
          [ScrollText, '日志', '实时运行日志', `${base}/logs`],
          [SquareTerminal, '命令', '在容器内执行命令', `${base}/logs?view=exec`],
        ] as const).map(([Icon, t, d, to]) => (
          <Link key={t} to={to} className="rounded-2xl surface surface-hover px-5 @2xl:px-6 py-5 flex items-center gap-4 min-w-0">
            <div className="h-11 w-11 shrink-0 rounded-lg bg-card2 ring-1 ring-line2 grid place-items-center text-fg"><Icon size={22} /></div>
            <div><div className="text-[13px] font-semibold">{t}</div><div className="text-[13px] text-muted mt-0.5">{d}</div></div>
          </Link>
        ))}
      </div>
      {hist && <DeployHistory s={s} deps={deps.data} onClose={() => setHist(false)} />}
      {bind && <BindDomain s={s} onClose={() => setBind(false)} />}
    </>
  )
}

function Uptime({ failed }: { failed: boolean }) {
  return (
    <div className="rounded-xl field px-4 py-3.5">
      <div className="flex items-center text-[12px] text-muted">最近 30 天可用性<span className="ml-auto text-fg font-semibold tnum">{failed ? '97.82%' : '99.98%'}</span></div>
      <div className="mt-2.5 flex gap-[3px]">{Array.from({ length: 30 }, (_, i) => {
        const bad = failed ? i > 26 : i === 11
        return <span key={i} className={cx('flex-1 h-6 rounded-[3px]', bad ? (failed && i > 27 ? 'bg-rose-500/80' : 'bg-amber-400/80') : 'bg-emerald-500/70')} />
      })}</div>
      <div className="mt-1.5 flex justify-between text-[10.5px] text-dim"><span>30 天前</span><span>今天</span></div>
    </div>
  )
}
function RecentDeploys({ deps }: { deps?: Deployment[] }) {
  const list = (deps ?? []).slice(0, 10).reverse()
  return (
    <div className="rounded-xl field px-4 py-3.5">
      <div className="flex items-center text-[12px] text-muted">最近部署<span className="ml-auto text-fg font-semibold tnum">{deps?.length ?? 0} 次</span></div>
      <div className="mt-2.5 flex gap-[3px]">{Array.from({ length: 10 }, (_, i) => {
        const d = list[i - (10 - list.length)]
        return <span key={i} title={d ? `${dateTime(d.started_at)} · ${deployStatusText[d.status] ?? d.status}` : ''} className={cx('flex-1 h-6 rounded-[3px]', !d ? 'bg-ov/[0.06]' : d.status === 'failed' ? 'bg-rose-500/80' : ['running', 'superseded'].includes(d.status) ? 'bg-emerald-500/70' : 'bg-amber-400/80')} />
      })}</div>
      <div className="mt-1.5 flex justify-between text-[10.5px] text-dim"><span>更早</span><span>最新</span></div>
    </div>
  )
}
function DeployHistory({ s, deps, onClose }: { s: Service; deps?: Deployment[]; onClose: () => void }) {
  const [target, setTarget] = useState<Deployment>()
  const [logs, setLogs] = useState<Deployment>()
  return (
    <Modal title={<><History size={17} />部署记录 · {s.name}</>} width={760} onClose={onClose}>
      {!deps ? <RowsSkeleton n={3} /> : !deps.length ? <div className="text-[13px] text-muted">还没有部署记录</div> : (
        <div className="divide-y divide-line -my-2">
          {deps.map((d, i) => (
            <div key={d.id} className="py-3 flex flex-wrap sm:flex-nowrap items-center gap-x-4 gap-y-2 text-[13px]">
              <Dot status={d.status === 'running' ? 'running' : d.status === 'failed' ? 'failed' : d.status === 'superseded' ? 'stopped' : 'deploying'} />
              <div className="flex-1 min-w-[calc(100%-32px)] sm:min-w-0"><div className="font-semibold flex flex-wrap items-center gap-x-2 gap-y-0.5">{deployStatusText[d.status] ?? d.status} <span className="text-muted font-normal">· {triggerText[d.trigger] ?? d.trigger}</span><CommitLink s={s} d={d} /></div>
                <div className="font-mono text-[12px] text-muted truncate mt-0.5" title={[d.image_digest ?? d.id, d.message].filter(Boolean).join(' · ')}>{shortDigests(d.image_digest) || d.id}{d.message ? ` · ${shortDigests(d.message)}` : ''}</div></div>
              {hasBuildLog(s, d) && <Button size="sm" variant="ghost" onClick={() => setLogs(d)}><ScrollText size={13} />构建日志</Button>}
              <span className="text-muted tnum whitespace-nowrap max-sm:ml-[23px]">{dateTime(d.started_at)}</span>
              {i > 0 && d.image_digest ? <Button size="sm" className="max-sm:ml-auto" onClick={() => setTarget(d)}><Undo2 size={13} />回滚</Button> : <span className="w-[74px] max-sm:hidden" />}
            </div>
          ))}
        </div>
      )}
      {target && <ConfirmDialog title="回滚部署" action="确认回滚" danger={false} onClose={() => setTarget(undefined)}
        description={<>将用 <span className="font-mono">{target.image_digest?.slice(0, 19)}</span>（{dateTime(target.started_at)}）重新部署 {s.name}。</>}
        onConfirm={() => run(() => api.rollback(s.id, target.id), { ok: '正在回滚', invalidate: [`deployments:${s.id}`, `project:${s.project_id}`] })} />}
      {logs && <BuildLogModal key={logs.id} s={s} d={logs} onClose={() => setLogs(undefined)} />}
    </Modal>
  )
}

export function BindDomain({ s, onClose }: { s: Service; onClose: () => void }) {
  const settings = useQuery('settings', api.settings).data
  const httpPorts = (s.ports ?? []).filter(p => p.protocol === 'http')
  const [host, setHost] = useState(''); const [port, setPort] = useState(httpPorts[0]?.name ?? ''); const [proxied, setProxied] = useState(true)
  const [busy, wrap] = useBusy()
  const hostname = host.includes('.') ? host : host && settings?.base_domain ? `${host}.${settings.base_domain}` : host
  const submit = () => wrap(async () => { const r = await run(() => api.addDomain(s.id, { hostname, port_name: port, proxied }), { ok: '域名已添加，正在配置 DNS 与证书', invalidate: [`project:${s.project_id}`, 'domains'] }); if (r) onClose() })
  return (
    <Modal title="绑定域名" onClose={onClose} footer={<><Button size="sm" onClick={onClose}>取消</Button><Button size="sm" variant="primary" disabled={!hostname || !port || busy} onClick={submit}>绑定</Button></>}>
      <Label t="域名" d={settings?.base_domain ? `只填前缀则使用 *.${settings.base_domain}；完整域名需由同一 Cloudflare 账号托管` : '尚未设置主域名，请填写完整域名'}>
        <div className="flex items-center gap-2 min-w-0"><TextInput autoFocus mono className="flex-1 min-w-0" value={host} onChange={e => setHost(e.target.value.trim())} placeholder={settings?.base_domain ? 'app' : 'app.example.com'} />{settings?.base_domain && !host.includes('.') && <span className="font-mono text-[13px] text-muted truncate max-w-[50%]">.{settings.base_domain}</span>}</div>
      </Label>
      <Label t="端口">{httpPorts.length ? <Select className="w-full" value={port} onChange={e => setPort(e.target.value)}>{httpPorts.map(p => <option key={p.name} value={p.name}>{p.name} · {p.container_port}</option>)}</Select> : <div className="text-[13px] text-muted">没有 HTTP 端口，请先在「网络」里添加。</div>}</Label>
      <div className="flex items-center justify-between gap-4"><div><div className="text-[13px] font-semibold">Cloudflare 代理</div><div className="text-[12px] text-dim mt-0.5">橙色云朵：隐藏源站 IP，80/443 只放行 Cloudflare</div></div><Toggle on={proxied} onChange={setProxied} /></div>
    </Modal>
  )
}

/** Image source without the raw digest: "ghcr.io/x/komari" + "1.2.5 · 5c43b1b" (pinned) or the tag. */
function ImageRef({ s }: { s: Service }) {
  const u = useImageUpdate(s.id).data
  if (!s.image) return <span>—</span>
  const p = splitImage(s.image)
  return (
    <span className="min-w-0 flex items-center gap-1.5" title={s.image}>
      <span className="truncate">{p.name}</span>
      <span className="shrink-0 text-fg2">{imageVersionLabel(s.image, u?.current_version)}</span>
    </span>
  )
}
