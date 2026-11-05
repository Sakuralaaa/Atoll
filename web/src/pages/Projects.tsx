import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { BACK } from '../api/backTo'
import { ArrowLeft, ArrowUpDown, Eye, EyeOff, LayoutGrid, List, Package, Pin, Plus, SearchX, Server as ServerIcon } from 'lucide-react'
import { ProblemsBanner } from '../components/Problems'
import { MainShell } from '../components/Shell'
import { Button, Dot, Mini, ServiceIcon } from '../components/ui'
import { CardSkeleton, Empty, ErrorState, Label, Modal, Select, TextInput } from '../components/feedback'
import { HostnameInput, useHostnameInput } from '../components/HostnameInput'
import { api } from '../api/client'
import { run, useQuery } from '../api/hooks'
import { primaryDomain, projIcon, svcIcon, svcName, projName } from '../api/view'
import { useUpdatesByService } from '../api/updates'
import { UpdateBadge } from '../components/ImageUpdateCard'
import { cx } from '../components/ui-utils'
import { useProblems, useProjects } from '../api/queries'
import { FilterChips } from '../components/StatusFilter'
import { projectFilters, useStatusFilter, type FilterDef } from '../api/filters'
import { pinnedFirst, useProjectPrefs } from '../api/prefs'
import { PinControls, StarButton } from '../components/ProjectPrefs'
import { L, useT } from '../i18n'
import { ServerGroupCards } from '../components/ServerGroups'
import { groupByServer } from '../api/groups'
import { providerOf } from '../api/providers'
import type { AppTemplate, ImageUpdate, Project, Server } from '../api/types'

const GROUP_KEY = 'atoll.projects.groupByServer'

export default function Projects() {
  const q = useProjects()
  const servers = useQuery('servers', api.servers).data
  const updates = useUpdatesByService()
  const [sort, setSort] = useState(false)
  const [listView, setListView] = useState(false)
  const [create, setCreate] = useState(false)
  const { t } = useT()
  const prefs = useProjectPrefs()
  // pinned projects always come first (in their pinned order), then the rest
  const all = pinnedFirst((q.data ?? []).slice().sort((a, b) => sort ? projName(a).localeCompare(projName(b)) : 0), prefs.prefs.pinned)
  const problems = (useProblems().data ?? []).filter(p => !p.resolved_at)
  const defs: FilterDef<Project>[] = [{ id: 'fav', zh: L("收藏"), en: 'Favorites', test: p => prefs.isFav(p.id) }, ...projectFilters<Project>(problems)]
  const [sp, setSp] = useSearchParams()
  const sel = sp.get('server') // '_' = projects whose machine is unknown
  // one card per project by default; grouping by machine is opt-in (v0.1.27)
  const [grouped, setGroupedState] = useState(() => localStorage.getItem(GROUP_KEY) === '1')
  const setGrouped = (v: boolean) => { localStorage.setItem(GROUP_KEY, v ? '1' : '0'); setGroupedState(v) }
  const machines = new Set(all.map(p => p.server_id ?? '')).size
  const inServer = (p: Project) => (p.server_id || '_') === sel
  const f = useStatusFilter(sel ? all.filter(inServer) : all, defs)
  const list = f.list
  const showGroups = grouped && !sel && machines > 1
  const groups = groupByServer(list, servers)
  const totals = new Map(groupByServer(all, servers).map(g => [g.id, g.projects.length]))
  const selServer = sel ? servers?.find(s => s.id === sel) : undefined
  const pinned = list.filter(p => prefs.isPinned(p.id))
  const cardProps = { servers, updates, prefs }
  const back = () => { const n = new URLSearchParams(sp); n.delete('server'); setSp(n, { state: BACK }) }
  return (
    <MainShell>
      <div className="max-w-[1240px] mx-auto px-4 pt-6 pb-12 @2xl:px-8 @2xl:pt-10 @5xl:px-14 @5xl:pt-12 @5xl:pb-16">
        <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-4 mb-8">
          {sel ? <div className="flex items-center gap-2 min-w-0">
            <button onClick={back} title={t('全部机器', 'All machines')} className="h-11 w-11 -ml-2 grid place-items-center text-fg2 shrink-0"><ArrowLeft size={18} /></button>
            <div className="min-w-0"><div className="text-[12px] text-muted">{t('我的项目', 'My projects')}</div>
              <h1 className="text-[22px] font-extrabold italic tracking-[-0.03em] truncate">{selServer?.name ?? t('未知机器', 'Unknown machine')}</h1></div>
          </div> : <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em]">{L("我的项目")}</h1>}
          <div className="flex items-center gap-3">
            {!sel && machines > 1 && <button onClick={() => setGrouped(!grouped)} title={t('按机器分组', 'Group by machine')} className={cx('h-11 w-11 grid place-items-center', grouped ? 'text-accent' : 'text-fg2')}><ServerIcon size={18} /></button>}
            <button onClick={() => setSort(v => !v)} title={L("按名称排序")} className={cx('h-11 w-11 grid place-items-center', sort ? 'text-accent' : 'text-fg2')}><ArrowUpDown size={18} /></button>
            <div className="flex rounded-lg field p-0.5">
              <button onClick={() => setListView(false)} className={cx('h-8 w-10 grid place-items-center rounded-md', !listView ? 'nav-active' : 'text-muted')}><LayoutGrid size={16} /></button>
              <button onClick={() => setListView(true)} className={cx('h-8 w-10 grid place-items-center rounded-md', listView ? 'nav-active' : 'text-muted')}><List size={16} /></button>
            </div>
            <Button variant="primary" size="lg" className="@3xl:w-[200px]" onClick={() => setCreate(true)}>{L("创建项目")}</Button>
          </div>
        </div>
        <ProblemsBanner scope="projects" />
        {q.error && !q.data && <ErrorState error={q.error} onRetry={q.refetch} />}
        {!q.data && !q.error && <CardSkeleton n={6} h={120} />}
        {q.data && all.length > 0 && <FilterChips f={f} defs={defs} className="mb-5" />}
        {q.data && all.length > 0 && !list.length && f.def && <Empty icon={<SearchX size={22} />} title={t(`没有「${f.def.zh}」的项目`, `No projects match “${f.def.en}”`)}
          desc={f.def.id === 'fav' ? t('点项目卡片右上角的星标即可收藏，收藏会同步到你的所有设备。', 'Star a project (top right of its card) to add it here; favorites sync across your devices.') : t('项目里任一服务符合条件即会列出。', 'A project is listed when any of its services matches.')} action={<Button onClick={() => f.set('')}>{t('显示全部', 'Show all')}</Button>} />}
        {q.data && !all.length && <Empty icon={<Package size={22} />} title={L("还没有项目")} desc={L("项目是一组运行在同一台服务器上的服务，它们通过 Docker 内网互通。也可以从 Zeabur 直接迁移过来。")}
          action={<div className="flex gap-2"><Button variant="primary" onClick={() => setCreate(true)}><Plus size={15} />{L("创建项目")}</Button><Link to="/import"><Button>{L("从 Zeabur 迁移")}</Button></Link></div>} />}
        {showGroups && pinned.length > 0 && <>
          <SectionLabel icon={<Pin size={12} />} text={t('置顶', 'Pinned')} />
          <ProjectGrid list={pinned} listView={listView} className="mb-7" {...cardProps} />
          <SectionLabel icon={<ServerIcon size={12} />} text={t('按机器', 'By machine')} />
        </>}
        {showGroups && <ServerGroupCards groups={groups} total={totals} listView={listView} prefs={prefs} />}
        {!showGroups && <ProjectGrid list={list} listView={listView} {...cardProps} />}
        {sel && q.data && !all.some(inServer) && <Empty icon={<Package size={22} />} title={t('这台机器上还没有项目', 'No projects on this machine')} desc="" action={<Button onClick={back}>{t('返回全部机器', 'All machines')}</Button>} />}
      </div>
      {create && <CreateProject defaultServer={selServer?.id} onClose={() => setCreate(false)} />}
    </MainShell>
  )
}

function SectionLabel({ icon, text }: { icon: React.ReactNode; text: string }) {
  return <div className="flex items-center gap-1.5 mb-3 text-[12px] font-semibold text-dim tracking-wider">{icon}{text}</div>
}

type CardProps = { servers?: Server[]; updates: Record<string, ImageUpdate | undefined>; prefs: ReturnType<typeof useProjectPrefs> }

/** Project cards (or rows). Every card in a row has the same height; pinned cards can be dragged to reorder. */
function ProjectGrid({ list, listView, className, ...cp }: CardProps & { list: Project[]; listView: boolean; className?: string }) {
  const [drag, setDrag] = useState<string>()
  return (
    <div className={cx('grid gap-4', listView ? 'grid-cols-1' : 'grid-cols-1 @2xl:auto-rows-fr @2xl:grid-cols-2 @6xl:grid-cols-3', className)}>
      {list.map(p => <ProjectCard key={p.id} p={p} {...cp}
        drag={cp.prefs.isPinned(p.id) ? {
          active: drag === p.id,
          onStart: () => setDrag(p.id), onEnd: () => setDrag(undefined),
          onDrop: () => { if (drag && cp.prefs.isPinned(drag)) cp.prefs.dropPin(drag, p.id); setDrag(undefined) },
        } : undefined} />)}
    </div>
  )
}

function ProjectCard({ p, servers, updates, prefs, drag }: CardProps & { p: Project; drag?: { active: boolean; onStart: () => void; onEnd: () => void; onDrop: () => void } }) {
  const { t } = useT()
  const srv = servers?.find(s => s.id === p.server_id)
  const ss = p.services ?? []
  const run = ss.filter(s => s.status === 'running').length
  const bad = ss.some(s => s.status === 'failed')
  const dom = ss.map(primaryDomain).find(Boolean)
  return (
    <Link to={`/projects/${p.id}`} data-testid="project-card"
      draggable={!!drag} onDragStart={drag ? e => { e.dataTransfer.effectAllowed = 'move'; drag.onStart() } : undefined} onDragEnd={drag?.onEnd}
      onDragOver={drag ? e => e.preventDefault() : undefined} onDrop={drag ? e => { e.preventDefault(); drag.onDrop() } : undefined}
      className={cx('group rounded-2xl surface surface-hover overflow-hidden flex flex-col', drag?.active && 'opacity-50')}>
      <div className="px-5 pt-5">
        <div className="flex items-center gap-3">
          <ServiceIcon s={projIcon(p)} size={38} />
          <div className="min-w-0 flex-1">
            <div className="text-[15px] font-semibold truncate">{projName(p)}</div>
            <div className="text-[12px] text-muted truncate mt-0.5" title={dom}>{dom ?? (ss.length ? t('仅内网访问', 'Internal only') : t('空项目', 'Empty project'))}</div>
          </div>
          <span className="flex items-center -mr-2 self-start -mt-1.5">
            <PinControls id={p.id} prefs={prefs} className={prefs.isPinned(p.id) ? '' : 'opacity-0 group-hover:opacity-100 focus-within:opacity-100 [@media(hover:none)]:opacity-100'} />
            <StarButton id={p.id} prefs={prefs} />
          </span>
        </div>
        <div className="mt-5 flex items-center gap-1.5 h-[22px] overflow-hidden">
          {ss.map(s => <span key={s.id} title={svcName(s)}><ServiceIcon s={svcIcon(s)} size={22} /></span>)}
          <span className="ml-auto shrink-0 text-[12px] text-muted tnum">{t(`${ss.length} 个服务`, `${ss.length} service${ss.length === 1 ? '' : 's'}`)}</span>
        </div>
      </div>
      <div className="mt-auto pt-5"><div className="px-5 h-11 border-t border-line bg-sunk/60 flex items-center gap-2 text-[12.5px]">
        {srv ? <><Mini provider={providerOf(srv)} size={13} /><span className="text-fg2 truncate min-w-0">{srv.name}</span></> : <span className="text-dim">—</span>}
        <span className="ml-auto flex items-center gap-2 text-muted tnum shrink-0"><UpdateBadge u={ss.map(s => updates[s.id]).find(u => u?.update_available)} /><Dot status={!ss.length ? 'unknown' : bad ? 'failed' : run === ss.length ? 'running' : 'deploying'} /><span className={bad ? 'text-[#ef4444] font-semibold' : ''}>{!ss.length ? t('无服务', 'No services') : bad ? t('有异常', 'Failing') : run === ss.length ? t('运行中', 'Running') : t('部分运行', 'Partly running')}</span>{run}/{ss.length}</span>
      </div></div>
    </Link>
  )
}

const APP_CATS: [string, string][] = [['all', L('全部')], ['blog', L('博客')], ['tools', L('工具')], ['ops', L('运维')], ['dev', L('开发')], ['ai', 'AI']]

/** The typed inputs of this app; empty ones are left out so the server generates them. */
function appInputs(app: AppTemplate, typed: Record<string, string>): Record<string, string> | undefined {
  const out = Object.fromEntries((app.inputs ?? []).filter(i => typed[i.key]).map(i => [i.key, typed[i.key]]))
  return Object.keys(out).length ? out : undefined
}

function CreateProject({ onClose, defaultServer }: { onClose: () => void; defaultServer?: string }) {
  const nav = useNavigate()
  const { lang } = useT()
  const servers = (useQuery('servers', api.servers).data ?? []).filter(s => s.role !== 'control')
  const apps = useQuery('app-templates', api.appTemplates).data ?? []
  const [mode, setMode] = useState<'empty' | 'app'>('empty')
  const [cat, setCat] = useState('all')
  const [appId, setAppId] = useState('')
  const [name, setName] = useState(''); const [desc, setDesc] = useState(''); const [sid, setSid] = useState('')
  const h = useHostnameInput()
  const noDomain = !h.text.trim()
  const h2 = useHostnameInput() // the optional second domain (a service with domain: true)
  const no2 = !h2.text.trim()
  const [inputs, setInputs] = useState<Record<string, string>>({})
  const [reveal, setReveal] = useState<Record<string, boolean>>({})
  const [busy, setBusy] = useState(false)
  const server = sid || (defaultServer && servers.some(s => s.id === defaultServer) ? defaultServer : '') || servers.find(s => s.status === 'online')?.id || servers[0]?.id || ''
  const app = apps.find(a => a.id === appId)
  const extraSvc = app?.services.find(x => x.domain && !x.main)
  const projName = name.trim() || app?.id || ''
  const ready = mode === 'empty' ? !!name.trim() && !!server : !!app && !!server && !!projName && (noDomain ? !app.needs_domain : h.v.ok) && (!extraSvc || no2 || h2.v.ok)
  const submit = async () => {
    if (!ready || busy) return
    setBusy(true)
    if (mode === 'empty') {
      const p = await run(() => api.createProject({ name: name.trim(), server_id: server, description: desc || undefined }), { ok: L("项目已创建"), invalidate: ['projects'] })
      setBusy(false)
      if (p) { onClose(); nav(`/projects/${p.id}?add=1`) }
      return
    }
    const r = await run(() => api.createFromApp(app!.id, { name: projName, server_id: server, ...(noDomain ? {} : { hostname: h.v.hostname, ...(h.v.root_domain ? { root_domain: h.v.root_domain } : {}) }), ...(extraSvc && !no2 ? { domains: { [extraSvc.name]: h2.v.full } } : {}), inputs: appInputs(app!, inputs) }), { ok: L("{0} 已创建，正在部署", app!.name), invalidate: ['projects'] })
    setBusy(false)
    if (r) { const main = r.services.find(x => x.name === app!.services.find(t => t.main)?.name); onClose(); nav(main ? `/projects/${r.project.id}/services/${main.id}` : `/projects/${r.project.id}`) }
  }
  const shown = apps.filter(a => cat === 'all' || a.category === cat)
  const tab = (v: 'empty' | 'app', label: string) => (
    <button type="button" onClick={() => setMode(v)} className={cx('h-8 px-3 rounded-md text-[13px] font-semibold transition-colors', mode === v ? 'bg-[#fde047] text-[#141414] border-2 border-[var(--edge)]' : 'text-muted hover:text-fg')}>{label}</button>
  )
  return (
    <Modal title={L("创建项目")} width={mode === 'app' ? 760 : 560} onClose={onClose} footer={<><Button size="sm" onClick={onClose}>{L("取消")}</Button><Button size="sm" variant="primary" disabled={!ready || busy} onClick={submit}>{mode === 'app' ? L("创建并部署") : L("创建")}</Button></>}>
      <div className="inline-flex rounded-lg field p-[3px] gap-0.5 mb-4">{tab('empty', L('空项目'))}{tab('app', L('应用模板'))}</div>
      {mode === 'app' && <>
        <div className="flex flex-wrap gap-1.5 mb-3">{APP_CATS.map(([k, label]) => (
          <button key={k} type="button" onClick={() => setCat(k)} className={cx('h-7 px-2.5 rounded-md text-[12.5px] font-semibold border', cat === k ? 'border-[var(--edge)] bg-ov/10 text-fg' : 'border-transparent text-muted hover:text-fg')}>{label}</button>
        ))}</div>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-2 mb-4 max-h-[300px] overflow-auto pr-1 -mr-1">
          {shown.map(a => (
            <button key={a.id} type="button" onClick={() => { setAppId(a.id); if (!name.trim() || apps.some(x => x.id === name.trim())) setName(a.id) }}
              className={cx('text-left flex gap-3 p-3 rounded-lg border-2 transition-colors', appId === a.id ? 'border-[#8b5cf6] bg-[#8b5cf6]/10' : 'border-line hover:border-[var(--edge)]')}>
              <span style={{ background: a.color }} className="tile h-9 w-9 shrink-0 rounded-[8px] grid place-items-center text-[15px] font-extrabold font-mono text-white">{a.name[0]}</span>
              <span className="min-w-0">
                <span className="flex items-center gap-1.5 text-[13.5px] font-semibold">{a.name}{a.services.length > 1 && <span className="text-[11px] font-mono text-dim">+{a.services.filter(x => !x.main).map(x => x.template ?? x.name).join(', ')}</span>}</span>
                <span className="block text-[12px] text-muted leading-snug mt-0.5">{lang === 'en' ? a.description_en : a.description}</span>
              </span>
            </button>
          ))}
        </div>
      </>}
      {(mode === 'empty' || app) && <>
        <Label t={L("项目名称")}><TextInput autoFocus={mode === 'empty'} className="w-full" value={mode === 'app' ? projName : name} onChange={e => setName(e.target.value)} onKeyDown={e => e.key === 'Enter' && submit()} placeholder="my-app" /></Label>
        <Label t={L("运行服务器")} d={L("同一项目的服务都部署在这台服务器上，之后可以整体迁移")}>
          {servers.length ? <Select className="w-full" value={server} onChange={e => setSid(e.target.value)}>{servers.map(s => <option key={s.id} value={s.id}>{s.name}{s.status !== 'online' ? L("（离线）") : ''}</option>)}</Select>
            : <div className="text-[13px] text-muted">{L("还没有可用的服务器，请先到「服务器」页添加。")}</div>}
        </Label>
        {mode === 'empty' && <Label t={L("描述（可选）")}><TextInput className="w-full" value={desc} onChange={e => setDesc(e.target.value)} /></Label>}
        {app && mode === 'app' && <>
          <Label t={app.needs_domain ? (lang === "en" ? "Domain" : "域名") : L("域名（可选）")} d={app.needs_domain ? L("{0} 需要通过自己的 HTTPS 域名访问", app.name) : L("留空则之后在服务的「网络」里绑定")}>
            <HostnameInput h={h} onEnter={submit} placeholder={app.needs_domain ? L("前缀，如 {0}；填 @ 使用根域名本身", app.id) : L("前缀，如 {0}；留空则不绑定", app.id)} />
            {!noDomain && <div className={cx('text-[12px] font-mono mt-1.5 break-all', h.v.ok ? 'text-muted' : 'text-[#ef4444]')}>{h.v.ok ? <>→ https://{h.v.full}</> : L("域名格式不对")}</div>}
          </Label>
          {extraSvc && <Label t={(lang === 'en' ? extraSvc.domain_label_en : extraSvc.domain_label) || L("{0} 的域名（可选）", extraSvc.name)} d={(lang === 'en' ? extraSvc.domain_hint_en : extraSvc.domain_hint) || L("留空则之后在服务的「网络」里绑定")}>
            <HostnameInput h={h2} onEnter={submit} placeholder={L("前缀，如 {0}；留空则不绑定", 'api')} />
            {!no2 && <div className={cx('text-[12px] font-mono mt-1.5 break-all', h2.v.ok ? 'text-muted' : 'text-[#ef4444]')}>{h2.v.ok ? <>→ https://{h2.v.full}</> : L("域名格式不对")}</div>}
          </Label>}
          {(app.inputs ?? []).map(i => (
            <Label key={i.key} t={lang === 'en' ? i.label_en : i.label} d={L("留空自动生成")}>
              <div className="flex gap-1.5">
                <TextInput className="w-full" mono autoComplete="new-password" type={i.secret && !reveal[i.key] ? 'password' : 'text'} value={inputs[i.key] ?? ''}
                  onChange={e => setInputs(v => ({ ...v, [i.key]: e.target.value }))} placeholder={i.placeholder} />
                {i.secret && <Button size="sm" type="button" title={reveal[i.key] ? L("隐藏") : L("显示")} onClick={() => setReveal(v => ({ ...v, [i.key]: !v[i.key] }))}>{reveal[i.key] ? <EyeOff size={14} /> : <Eye size={14} />}</Button>}
              </div>
            </Label>
          ))}
          <div className="text-[12.5px] text-muted leading-relaxed">
            {L("会创建 {0} 个服务：{1}。建议内存至少 {2} MB。", app.services.length, app.services.map(x => x.name).join(L('、')), app.min_mem_mb)}
            {(lang === 'en' ? app.note_en : app.note) && <> {lang === 'en' ? app.note_en : app.note}</>}
          </div>
        </>}
      </>}
    </Modal>
  )
}
