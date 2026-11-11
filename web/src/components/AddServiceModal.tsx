import { useCallback, useEffect, useState } from 'react'
import { Search, Box, Boxes, Database, ChevronRight, X, ArrowLeft, Loader2, Webhook, Plus, Trash2, Lock, LockOpen, FileCode, List } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { api } from '../api/client'
import { run, useQuery } from '../api/hooks'
import type { GitSource, Service } from '../api/types'
import { autoName, emptyGitForm, gitBody, gitFormError, type GitForm } from '../api/git'
import { GitFields, SourceToggle, WebhookSetup, type SourceKind } from './GitSource'
import { Button, ServiceIcon } from './ui'
import { Label, Select, TextInput } from './feedback'
import { L, useT } from '../i18n'
import { cx } from './ui-utils'
import { parseEnvText, quoteEnvValue } from '../api/dotenv'

type Step = 'pick' | 'image' | 'db'
const items: { k: Step | 'docker'; i: React.ReactNode; t: string; d: string }[] = [
  { k: 'image', i: <Box size={20} className="text-c-1" />, t: L("Docker 容器镜像 / Git 仓库"), d: L("从 Docker Hub、GHCR、私有仓库部署，或从 GitHub、GitLab、Gitea 等 Git 仓库源码构建（无 Dockerfile 也可）") },
  { k: 'db', i: <Database size={20} className="text-c-4" />, t: L("数据库"), d: L("PostgreSQL · MySQL · Redis · MongoDB，自动生成密码与连接变量") },
  // not a step here: opens the adoption wizard (/import/docker) on the project's server
  { k: 'docker', i: <Boxes size={20} className="text-c-2" />, t: L("从已有 Docker 容器接管"), d: L("服务器上自己用 docker run / compose 跑的容器，原地交给 Atoll 管理，不复制数据") },
]

export default function AddServiceModal({ projectId, onClose, onCreated }: { projectId: string; onClose: () => void; onCreated?: (s: Service) => void }) {
  const [step, setStep] = useState<Step>('pick')
  const [sel, setSel] = useState(0)
  const [q, setQ] = useState('')
  const list = items.filter(x => !q || (x.t + x.d).toLowerCase().includes(q.toLowerCase()))
  const nav = useNavigate()
  const proj = useQuery(`project:${projectId}`, () => api.project(projectId)).data
  const [serverId, setServerId] = useState('')
  const home = proj?.server_id
  const pick = useCallback((k: Step | 'docker') => {
    if (k !== 'docker') return setStep(k)
    const srv = serverId || home
    onClose()
    nav(`/import/docker?${new URLSearchParams({ ...(srv ? { server: srv } : {}), project: projectId })}`)
  }, [serverId, home, onClose, nav, projectId])
  useEffect(() => {
    const k = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
      if (step !== 'pick') return
      if (e.key === 'ArrowDown') setSel(v => Math.min(list.length - 1, v + 1))
      if (e.key === 'ArrowUp') setSel(v => Math.max(0, v - 1))
      if (e.key === 'Enter' && list[sel]) pick(list[sel].k)
    }
    addEventListener('keydown', k); return () => removeEventListener('keydown', k)
  }, [step, sel, list, onClose, pick])
  const done = (s?: Service) => { if (s) { onCreated?.(s); onClose() } }
  const [showWebhook, setShowWebhook] = useState(false)

  return (
    <div className="fixed inset-0 z-50 bg-black/65 flex items-start justify-center pt-[14vh]" onClick={onClose}>
      <div className="w-[620px] max-h-[82vh] overflow-y-auto rounded-2xl border border-line2 bg-pop shadow-2xl" onClick={e => e.stopPropagation()}>
        <div className="flex items-center gap-3 px-5 h-16 border-b border-line">
          {step === 'pick' ? <Search size={18} className="text-muted" /> : <button onClick={() => setStep('pick')} className="text-muted hover:text-fg"><ArrowLeft size={18} /></button>}
          {step === 'pick'
            ? <input autoFocus value={q} onChange={e => { setQ(e.target.value); setSel(0) }} placeholder={L("要部署什么服务？")} className="text-[16px] flex-1 bg-transparent outline-none focus-visible:outline-none placeholder:text-dim text-fg" />
            : <span className="text-[16px] font-semibold flex-1">{step === 'image' ? L("部署服务") : L("数据库")}</span>}
          <button onClick={onClose} className="text-muted"><X size={18} /></button>
        </div>
        {step === 'pick' && <>
          <div className="p-2">
            {list.map((x, idx) => (
              <button key={x.t} onMouseEnter={() => setSel(idx)} onClick={() => pick(x.k)} className={cx('w-full text-left flex items-center gap-4 px-4 h-[68px] rounded-xl', idx === sel ? 'bg-ov/[0.06]' : 'hover:bg-ov/[0.04]')}>
                <div className="h-10 w-10 rounded-lg bg-card2 ring-1 ring-line2 grid place-items-center">{x.i}</div>
                <div className="flex-1"><div className="text-[14px] font-semibold flex items-center gap-2">{x.t}</div><div className="text-[13px] text-muted mt-0.5">{x.d}</div></div>
                <ChevronRight size={17} className="text-dim" />
              </button>
            ))}
          </div>
          <div className="border-t border-line px-5 h-11 flex items-center gap-4 text-[12px] text-dim"><span>{L("↑↓ 选择")}</span><span>{L("↵ 确认")}</span><span>{L("Esc 关闭")}</span></div>
        </>}
        {step !== 'pick' && !showWebhook && <Placement projectId={projectId} value={serverId} onChange={setServerId} />}
        {step === 'image' && <ImageForm projectId={projectId} serverId={serverId} onDone={done} onWebhook={() => setShowWebhook(true)} />}
        {step === 'db' && <DbPicker projectId={projectId} serverId={serverId} onDone={done} />}
      </div>
    </div>
  )
}

/** Optional placement (server_id) when there is more than one server; "" = the project's server. */
function Placement({ projectId, value, onChange }: { projectId: string; value: string; onChange: (v: string) => void }) {
  const { t } = useT()
  const proj = useQuery(`project:${projectId}`, () => api.project(projectId)).data
  const servers = (useQuery('servers', api.servers).data ?? []).filter(x => x.role !== 'control')
  const settings = useQuery('settings', api.settings).data
  if (servers.length < 2 || !proj) return null
  const home = servers.find(x => x.id === proj.server_id)
  const other = value && value !== proj.server_id ? servers.find(x => x.id === value) : undefined
  const tailnetOff = settings && settings.network_provider !== 'tailscale'
  return (
    <div className="px-5 pt-5 -mb-2">
      <Label t={t('服务器', 'Server')} d={t('默认和项目其他服务同一台。放到其他服务器时，两边经 tailnet 互通，服务名不变。', "Defaults to the project's server. On another server, services reach each other over the tailnet under the same name.")}>
        <Select className="w-full" value={value || proj.server_id} onChange={e => onChange(e.target.value === proj.server_id ? '' : e.target.value)}>
          {servers.map(x => <option key={x.id} value={x.id}>{x.name}{x.id === proj.server_id ? t('（项目服务器）', ' (project server)') : ''}{x.status !== 'online' ? ` · ${t('离线', 'offline')}` : ''}</option>)}
        </Select>
      </Label>
      {other && (tailnetOff || !other.tailnet_ip || (home && !home.tailnet_ip)) && <div className="mb-4 -mt-2 rounded-lg border-2 border-dashed border-warn px-3 py-2 text-[12.5px] text-fg2">
        {tailnetOff ? t('跨服务器需要先在设置里启用 Tailscale。', 'Cross-server placement needs Tailscale enabled in Settings.') : t('两台服务器都需要先加入内网（tailnet）。', 'Both servers need to be on the tailnet first.')}
      </div>}
    </div>
  )
}

type EnvRow = { key: string; value: string; secret: boolean }
type PortRow = { port: string; proto: string }
type VolRow = { name: string; path: string }
type CfgRow = { path: string; content: string }

/** Collapsible section of the deploy form (closed by default). */
function Section({ title, count, open, onToggle, children }: { title: string; count?: number; open: boolean; onToggle: () => void; children: React.ReactNode }) {
  return (
    <div className="border-2 border-[var(--edge)] rounded-lg mb-2.5 bg-card">
      <button type="button" onClick={onToggle} className="w-full flex items-center gap-2 h-10 px-3 text-[13.5px] font-semibold text-left">
        <ChevronRight size={15} className={cx('text-muted transition-transform', open && 'rotate-90')} />
        <span className="flex-1">{title}</span>
        {!!count && <span className="text-[11.5px] font-mono text-dim">{count}</span>}
      </button>
      {open && <div className="px-3 pb-3 pt-0.5">{children}</div>}
    </div>
  )
}
const iconBtn = 'h-9 w-9 shrink-0 grid place-items-center rounded-md text-muted hover:text-danger hover:bg-ov/5'
const addBtn = 'inline-flex items-center gap-1.5 text-[12.5px] font-semibold text-muted hover:text-fg mt-1'

/** Splits a start command the way a shell would for simple cases: spaces, '…' and "…" quotes. */
function splitCommand(s: string): string[] {
  const out: string[] = []; let cur = '', q = '', has = false
  for (const ch of s.trim()) {
    if (q) { if (ch === q) q = ''; else cur += ch; continue }
    if (ch === '"' || ch === "'") { q = ch; has = true; continue }
    if (/\s/.test(ch)) { if (cur || has) out.push(cur); cur = ''; has = false; continue }
    cur += ch
  }
  if (cur || has) out.push(cur)
  return out
}

function ImageForm({ projectId, serverId, onDone, onWebhook }: { projectId: string; serverId: string; onDone: (s?: Service) => void; onWebhook: () => void }) {
  const { t } = useT()
  const [source, setSource] = useState<SourceKind>('image')
  const [image, setImage] = useState(''); const [name, setName] = useState('')
  const [regUser, setRegUser] = useState(''); const [regPass, setRegPass] = useState('')
  const [envs, setEnvs] = useState<EnvRow[]>([])
  const [rawEnv, setRawEnv] = useState<string>()
  const [ports, setPorts] = useState<PortRow[]>([{ port: '8080', proto: 'http' }])
  const [vols, setVols] = useState<VolRow[]>([])
  const [cmd, setCmd] = useState('')
  const [cfgs, setCfgs] = useState<CfgRow[]>([])
  const [open, setOpen] = useState<Record<string, boolean>>({})
  const tog = (k: string) => setOpen(o => ({ ...o, [k]: !o[k] }))
  const [git, setGit] = useState<GitForm>(emptyGitForm)
  const [busy, setBusy] = useState(false)
  const [created, setCreated] = useState<Service>()
  const auto = source === 'git' ? autoName(git.repo) : image.split('/').pop()?.split(':')[0]?.replace(/[^a-z0-9-]/gi, '-').toLowerCase() ?? ''
  const gitErr = source === 'git' ? gitFormError(git, t) : undefined
  const rawParsed = rawEnv !== undefined ? parseEnvText(rawEnv) : undefined
  const envList: EnvRow[] = rawParsed ? rawParsed.vars.map(v => ({ ...v, secret: envs.find(e => e.key === v.key)?.secret ?? false })) : envs.filter(e => e.key.trim())
  const portList = ports.filter(p => p.port)
  const volList = vols.filter(v => v.name.trim() || v.path.trim())
  const cfgList = cfgs.filter(c => c.path.trim() || c.content)
  const formErr =
    rawParsed?.errors.length ? t('环境变量第 {0} 行格式不对', 'Variables: line {0} is not KEY=VALUE').replace('{0}', String(rawParsed.errors[0].line))
    : envList.some(e => !/^[A-Za-z_][A-Za-z0-9_]*$/.test(e.key.trim())) ? t('环境变量名只能用字母、数字和下划线', 'Variable names use letters, digits and _ only')
    : new Set(portList.map(p => p.port)).size !== portList.length ? t('端口重复', 'Duplicate port')
    : portList.some(p => +p.port < 1 || +p.port > 65535) ? t('端口范围 1-65535', 'Ports are 1-65535')
    : volList.some(v => !/^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/.test(v.name.trim()) || !v.path.trim().startsWith('/')) ? t('卷名用小写字母、数字和 -，挂载路径以 / 开头', 'Volume names are lowercase letters, digits and -; mount paths start with /')
    : cfgList.some(c => !c.path.trim().startsWith('/')) ? t('配置文件路径以 / 开头', 'Config file paths start with /')
    : cfgList.some(c => new TextEncoder().encode(c.content).length > 1 << 20) ? t('配置文件不能超过 1 MB', 'Config files are at most 1 MB')
    : source === 'image' && !!regUser.trim() !== !!regPass ? t('私有镜像需要同时填写用户名和密码', 'A private image needs both username and password')
    : undefined
  const ready = (source === 'image' ? !!image.trim() : !gitErr) && !formErr
  const submit = async () => {
    if (!ready || busy) return
    setBusy(true)
    const names = new Set<string>()
    const portBody = portList.map((p, i) => {
      let n = i === 0 ? 'web' : `${p.proto === 'http' ? 'web' : p.proto}${p.port}`
      while (names.has(n)) n += 'x'
      names.add(n)
      return { name: n, container_port: +p.port, protocol: p.proto }
    })
    const command = splitCommand(cmd)
    const extra = {
      ports: portBody,
      volumes: volList.map(v => ({ name: v.name.trim(), mount_path: v.path.trim() })),
      ...(envList.length ? { env: envList.map(e => ({ key: e.key.trim(), value: e.value, secret: e.secret, exposed: false })) } : {}),
      ...(command.length ? { command } : {}),
      ...(cfgList.length ? { configs: cfgList.map(c => ({ path: c.path.trim(), content: c.content })) } : {}),
      ...(serverId ? { server_id: serverId } : {}),
    }
    const s = source === 'image'
      ? await run(() => api.createService(projectId, { name: name || auto, kind: 'image', image: image.includes(':') || image.includes('@') ? image.trim() : `${image.trim()}:latest`, ...extra,
          ...(regUser.trim() ? { registry_auth: { username: regUser.trim(), password: regPass } } : {}) }),
        { ok: L("服务已创建，正在部署"), invalidate: [`project:${projectId}`, 'projects'] })
      : await run(() => api.createService(projectId, { name: name || auto, kind: 'image', git: gitBody(git) as GitSource, ...extra }),
        { ok: t('服务已创建，正在构建', 'Service created, building'), invalidate: [`project:${projectId}`, 'projects'] })
    setBusy(false)
    if (s?.webhook?.secret) { setCreated(s); onWebhook() }
    else onDone(s)
  }
  if (created?.webhook) return (
    <div className="p-5">
      <div className="text-[15px] font-extrabold italic tracking-[-0.02em] flex items-center gap-2 mb-1"><Webhook size={17} />{t('配置推送 Webhook', 'Set up the push webhook')}</div>
      <div className="text-[13px] text-muted mb-4 leading-6">{t('服务已创建，正在构建。添加下面的 Webhook 后，推送代码会自动构建并部署。密钥只在这里完整显示一次。', 'The service is created and building. Add this webhook so pushes build and deploy automatically. The secret is shown here once.')}</div>
      <WebhookSetup wh={created.webhook} secret={created.webhook.secret} />
      <div className="flex justify-end gap-2 mt-5"><Button variant="primary" onClick={() => onDone(created)}>{t('我已保存', 'Done')}</Button></div>
    </div>
  )
  const setRow = <T,>(set: React.Dispatch<React.SetStateAction<T[]>>, i: number, patch: Partial<T>) => set(rs => rs.map((r, j) => (j === i ? { ...r, ...patch } : r)))
  const delRow = <T,>(set: React.Dispatch<React.SetStateAction<T[]>>, i: number) => set(rs => rs.filter((_, j) => j !== i))
  return (
    <div className="p-5">
      <div className="mb-4"><SourceToggle value={source} onChange={setSource} /></div>
      {source === 'image'
        ? <Label t={L("镜像")} d={L("例如 ghcr.io/me/app:v1；不写标签时使用 latest")}><TextInput autoFocus mono className="w-full" placeholder="ghcr.io/owner/image:tag" value={image} onChange={e => setImage(e.target.value)} onKeyDown={e => e.key === 'Enter' && submit()} /></Label>
        : <GitFields f={git} set={setGit} autoFocus />}
      <Label t={L("服务名称")} d={L("也是项目内网的主机名")}><TextInput className="w-full" placeholder={auto || 'my-service'} value={name} onChange={e => setName(e.target.value)} /></Label>

      {source === 'image' && <Section title={L("私有镜像凭据")} count={regUser ? 1 : 0} open={!!open.reg} onToggle={() => tog('reg')}>
        <div className="text-[12px] text-dim mb-2">{L("拉取私有镜像时使用（如 GHCR 用 GitHub 用户名 + token）。加密保存，不会再显示。")}</div>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
          <TextInput className="w-full" autoComplete="off" placeholder={L("用户名")} value={regUser} onChange={e => setRegUser(e.target.value)} />
          <TextInput className="w-full" type="password" autoComplete="new-password" placeholder={L("密码 / Token")} value={regPass} onChange={e => setRegPass(e.target.value)} />
        </div>
      </Section>}

      <Section title={L("环境变量")} count={envList.length} open={!!open.env} onToggle={() => tog('env')}>
        {rawEnv === undefined ? <>
          {envs.map((e, i) => (
            <div key={i} className="flex flex-wrap sm:flex-nowrap items-center gap-2 mb-2">
              <TextInput mono className="w-full sm:w-[38%]" placeholder="KEY" value={e.key} onChange={ev => setRow(setEnvs, i, { key: ev.target.value })} />
              <TextInput mono className="flex-1 min-w-0" type={e.secret ? 'password' : 'text'} autoComplete="new-password" placeholder="value" value={e.value} onChange={ev => setRow(setEnvs, i, { value: ev.target.value })} />
              <button type="button" title={L("私有值")} onClick={() => setRow(setEnvs, i, { secret: !e.secret })} className={cx('h-9 w-9 shrink-0 grid place-items-center rounded-md hover:bg-ov/5', e.secret ? 'text-warn' : 'text-muted')}>{e.secret ? <Lock size={15} /> : <LockOpen size={15} />}</button>
              <button type="button" title={L("删除")} onClick={() => delRow(setEnvs, i)} className={iconBtn}><Trash2 size={15} /></button>
            </div>
          ))}
          <div className="flex flex-wrap gap-4">
            <button type="button" className={addBtn} onClick={() => setEnvs(r => [...r, { key: '', value: '', secret: false }])}><Plus size={14} />{L("添加环境变量")}</button>
            <button type="button" className={addBtn} onClick={() => setRawEnv(envs.filter(e => e.key.trim()).map(e => `${e.key.trim()}=${quoteEnvValue(e.value)}`).join('\n'))}><FileCode size={14} />{L("编辑原始环境变量")}</button>
          </div>
        </> : <>
          <textarea value={rawEnv} onChange={e => setRawEnv(e.target.value)} rows={6} spellCheck={false} placeholder={'KEY=value\nOTHER="with spaces"'}
            className="w-full rounded-lg field px-3 py-2 font-mono text-[12.5px] outline-none text-fg placeholder:text-dim resize-y" />
          <button type="button" className={addBtn} onClick={() => { if (rawParsed && !rawParsed.errors.length) { setEnvs(envList); setRawEnv(undefined) } }}><List size={14} />{L("切换回表格")}</button>
        </>}
      </Section>

      <Section title={L("端口")} count={portList.length} open={!!open.ports} onToggle={() => tog('ports')}>
        {ports.map((p, i) => (
          <div key={i} className="flex items-center gap-2 mb-2">
            <TextInput mono className="flex-1 min-w-0" placeholder="8080" value={p.port} onChange={e => setRow(setPorts, i, { port: e.target.value.replace(/\D/g, '') })} />
            <Select className="w-[150px] sm:w-[190px] shrink-0" value={p.proto} onChange={e => setRow(setPorts, i, { proto: e.target.value })}><option value="http">{L("HTTP（可绑定域名）")}</option><option value="tcp">TCP</option><option value="udp">UDP</option></Select>
            <button type="button" title={L("删除")} onClick={() => delRow(setPorts, i)} className={iconBtn}><Trash2 size={15} /></button>
          </div>
        ))}
        {!ports.length && <div className="text-[12px] text-dim mb-1">{L("不暴露端口")}</div>}
        <button type="button" className={addBtn} onClick={() => setPorts(r => [...r, { port: '', proto: 'tcp' }])}><Plus size={14} />{L("添加端口")}</button>
      </Section>

      <Section title={L("卷")} count={volList.length} open={!!open.vols} onToggle={() => tog('vols')}>
        <div className="text-[12px] text-dim mb-2">{L("持久化目录，重新部署后数据保留。")}</div>
        {vols.map((v, i) => (
          <div key={i} className="flex items-center gap-2 mb-2">
            <TextInput mono className="w-[34%] min-w-0" placeholder="data" value={v.name} onChange={e => setRow(setVols, i, { name: e.target.value.toLowerCase() })} />
            <TextInput mono className="flex-1 min-w-0" placeholder="/app/data" value={v.path} onChange={e => setRow(setVols, i, { path: e.target.value })} />
            <button type="button" title={L("删除")} onClick={() => delRow(setVols, i)} className={iconBtn}><Trash2 size={15} /></button>
          </div>
        ))}
        <button type="button" className={addBtn} onClick={() => setVols(r => [...r, { name: r.length ? `data${r.length + 1}` : 'data', path: '' }])}><Plus size={14} />{L("添加卷")}</button>
      </Section>

      <Section title={L("启动命令")} count={cmd.trim() ? 1 : 0} open={!!open.cmd} onToggle={() => tog('cmd')}>
        <div className="text-[12px] text-dim mb-2">{L("覆盖镜像的 ENTRYPOINT，留空使用镜像默认值。例如 sh -c \"npm run start\"")}</div>
        <TextInput mono className="w-full" placeholder="/app/server --port 8080" value={cmd} onChange={e => setCmd(e.target.value)} />
      </Section>

      <Section title={L("配置文件")} count={cfgList.length} open={!!open.cfg} onToggle={() => tog('cfg')}>
        <div className="text-[12px] text-dim mb-2">{L("以只读方式挂载到容器里；之后可在「文件」页修改（保存后会重新部署）。")}</div>
        {cfgs.map((c, i) => (
          <div key={i} className="mb-3">
            <div className="flex items-center gap-2 mb-1.5">
              <TextInput mono className="flex-1 min-w-0" placeholder="/app/config.yaml" value={c.path} onChange={e => setRow(setCfgs, i, { path: e.target.value })} />
              <button type="button" title={L("删除")} onClick={() => delRow(setCfgs, i)} className={iconBtn}><Trash2 size={15} /></button>
            </div>
            <textarea value={c.content} onChange={e => setRow(setCfgs, i, { content: e.target.value })} rows={5} spellCheck={false} placeholder={L("文件内容")}
              className="w-full rounded-lg field px-3 py-2 font-mono text-[12.5px] outline-none text-fg placeholder:text-dim resize-y" />
          </div>
        ))}
        <button type="button" className={addBtn} onClick={() => setCfgs(r => [...r, { path: '', content: '' }])}><Plus size={14} />{L("添加配置文件")}</button>
      </Section>

      {source === 'git' && <div className="text-[12px] text-dim mt-2 mb-3">{t('在服务所在的服务器上执行 docker build；仓库没有 Dockerfile 时自动识别 Node / Bun / Python / Go / 静态站点并生成。', "Runs docker build on the service's server; without a Dockerfile the stack (Node, Bun, Python, Go, static site) is detected and one is generated.")}</div>}
      <div className="flex items-center justify-end gap-2 mt-3">
        {(formErr || (source === 'git' && git.repo && gitErr)) && <span className="mr-auto text-[12.5px] text-danger">{formErr || gitErr}</span>}
        <Button variant="primary" disabled={!ready || busy} onClick={submit}>{busy && <Loader2 size={14} className="animate-spin" />}{source === 'git' ? t('构建并部署', 'Build and deploy') : L("部署")}</Button>
      </div>
    </div>
  )
}

function DbPicker({ projectId, serverId, onDone }: { projectId: string; serverId: string; onDone: (s?: Service) => void }) {
  const t = useQuery('templates', api.templates)
  const [busy, setBusy] = useState<string>()
  const create = async (kind: string) => {
    setBusy(kind)
    const s = await run(() => api.createService(projectId, { name: kind === 'postgres' ? 'postgresql' : kind === 'mongo' ? 'mongodb' : kind, kind, ...(serverId ? { server_id: serverId } : {}) }), { ok: L("数据库已创建，正在部署"), invalidate: [`project:${projectId}`, 'projects'] })
    setBusy(undefined); onDone(s)
  }
  return (
    <div className="p-2">
      {t.error && <div className="p-4 text-[13px] text-danger">{t.error.message}</div>}
      {!t.data && !t.error && <div className="p-6 flex justify-center text-muted"><Loader2 className="animate-spin" /></div>}
      {t.data?.map(x => (
        <button key={x.kind} disabled={!!busy} onClick={() => create(x.kind)} className="w-full text-left flex items-center gap-4 px-4 h-[68px] rounded-xl hover:bg-ov/[0.06]">
          <ServiceIcon s={{ kind: x.kind, letter: x.name[0] }} size={40} />
          <div className="flex-1"><div className="text-[14px] font-semibold">{x.name} <span className="font-mono text-[12px] text-dim font-normal ml-1">{x.image}</span></div><div className="text-[13px] text-muted mt-0.5">{x.description ?? L("自动生成密码与连接变量")}</div></div>
          {busy === x.kind ? <Loader2 size={17} className="animate-spin text-muted" /> : <ChevronRight size={17} className="text-dim" />}
        </button>
      ))}
    </div>
  )
}
