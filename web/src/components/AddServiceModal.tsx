import { useCallback, useEffect, useState } from 'react'
import { Search, Box, Boxes, Database, ChevronRight, X, ArrowLeft, Loader2, Webhook } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { api } from '../api/client'
import { run, useQuery } from '../api/hooks'
import type { GitSource, Service } from '../api/types'
import { autoName, emptyGitForm, gitBody, gitFormError, type GitForm } from '../api/git'
import { GitFields, SourceToggle, WebhookSetup, type SourceKind } from './GitSource'
import { Button, ServiceIcon } from './ui'
import { Label, Select, TextInput } from './feedback'
import { useT } from '../i18n'
import { cx } from './ui-utils'

type Step = 'pick' | 'image' | 'db'
const items: { k: Step | 'docker'; i: React.ReactNode; t: string; d: string }[] = [
  { k: 'image', i: <Box size={20} className="text-[#3b9cff]" />, t: 'Docker 容器镜像 / GitHub 仓库', d: '从 Docker Hub、GHCR、私有仓库部署，或用 Dockerfile 从 GitHub 源码构建' },
  { k: 'db', i: <Database size={20} className="text-[#38bdf8]" />, t: '数据库', d: 'PostgreSQL · MySQL · Redis · MongoDB，自动生成密码与连接变量' },
  // not a step here: opens the adoption wizard (/import/docker) on the project's server
  { k: 'docker', i: <Boxes size={20} className="text-[#a78bfa]" />, t: '从已有 Docker 容器接管', d: '服务器上自己用 docker run / compose 跑的容器，原地交给 Atoll 管理，不复制数据' },
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
            ? <input autoFocus value={q} onChange={e => { setQ(e.target.value); setSel(0) }} placeholder="要部署什么服务？" className="text-[16px] flex-1 bg-transparent outline-none placeholder:text-dim text-fg" />
            : <span className="text-[16px] font-semibold flex-1">{step === 'image' ? '部署服务' : '数据库'}</span>}
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
          <div className="border-t border-line px-5 h-11 flex items-center gap-4 text-[12px] text-dim"><span>↑↓ 选择</span><span>↵ 确认</span><span>Esc 关闭</span></div>
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
      {other && (tailnetOff || !other.tailnet_ip || (home && !home.tailnet_ip)) && <div className="mb-4 -mt-2 rounded-lg border-2 border-dashed border-[#f59e0b] px-3 py-2 text-[12.5px] text-fg2">
        {tailnetOff ? t('跨服务器需要先在设置里启用 Tailscale。', 'Cross-server placement needs Tailscale enabled in Settings.') : t('两台服务器都需要先加入内网（tailnet）。', 'Both servers need to be on the tailnet first.')}
      </div>}
    </div>
  )
}

function ImageForm({ projectId, serverId, onDone, onWebhook }: { projectId: string; serverId: string; onDone: (s?: Service) => void; onWebhook: () => void }) {
  const { t } = useT()
  const [source, setSource] = useState<SourceKind>('image')
  const [image, setImage] = useState(''); const [name, setName] = useState(''); const [port, setPort] = useState('8080'); const [proto, setProto] = useState('http')
  const [git, setGit] = useState<GitForm>(emptyGitForm)
  const [busy, setBusy] = useState(false)
  const [created, setCreated] = useState<Service>()
  const auto = source === 'git' ? autoName(git.repo) : image.split('/').pop()?.split(':')[0]?.replace(/[^a-z0-9-]/gi, '-').toLowerCase() ?? ''
  const gitErr = source === 'git' ? gitFormError(git, t) : undefined
  const ready = source === 'image' ? !!image.trim() : !gitErr
  const submit = async () => {
    if (!ready || busy) return
    setBusy(true)
    const ports = port ? [{ name: 'web', container_port: +port, protocol: proto }] : []
    const place = serverId ? { server_id: serverId } : {}
    const s = source === 'image'
      ? await run(() => api.createService(projectId, { name: name || auto, kind: 'image', image: image.includes(':') || image.includes('@') ? image.trim() : `${image.trim()}:latest`, volumes: [], ports, ...place }),
        { ok: '服务已创建，正在部署', invalidate: [`project:${projectId}`, 'projects'] })
      : await run(() => api.createService(projectId, { name: name || auto, kind: 'image', git: gitBody(git) as GitSource, volumes: [], ports, ...place }),
        { ok: t('服务已创建，正在构建', 'Service created, building'), invalidate: [`project:${projectId}`, 'projects'] })
    setBusy(false)
    if (s?.webhook?.secret) { setCreated(s); onWebhook() }
    else onDone(s)
  }
  if (created?.webhook) return (
    <div className="p-5">
      <div className="text-[15px] font-extrabold italic tracking-[-0.02em] flex items-center gap-2 mb-1"><Webhook size={17} />{t('配置 GitHub Webhook', 'Set up the GitHub webhook')}</div>
      <div className="text-[13px] text-muted mb-4 leading-6">{t('服务已创建，正在构建。添加下面的 Webhook 后，推送代码会自动构建并部署。密钥只在这里完整显示一次。', 'The service is created and building. Add this webhook so pushes build and deploy automatically. The secret is shown here once.')}</div>
      <WebhookSetup wh={created.webhook} secret={created.webhook.secret} />
      <div className="flex justify-end gap-2 mt-5"><Button variant="primary" onClick={() => onDone(created)}>{t('我已保存', 'Done')}</Button></div>
    </div>
  )
  return (
    <div className="p-5">
      <div className="mb-4"><SourceToggle value={source} onChange={setSource} /></div>
      {source === 'image'
        ? <Label t="镜像" d="例如 ghcr.io/me/app:v1；不写标签时使用 latest"><TextInput autoFocus mono className="w-full" placeholder="ghcr.io/owner/image:tag" value={image} onChange={e => setImage(e.target.value)} onKeyDown={e => e.key === 'Enter' && submit()} /></Label>
        : <GitFields f={git} set={setGit} autoFocus />}
      <Label t="服务名称" d="也是项目内网的主机名"><TextInput className="w-full" placeholder={auto || 'my-service'} value={name} onChange={e => setName(e.target.value)} /></Label>
      <div className="grid grid-cols-2 gap-4">
        <Label t="容器端口"><TextInput mono className="w-full" value={port} onChange={e => setPort(e.target.value.replace(/\D/g, ''))} placeholder="留空表示不暴露" /></Label>
        <Label t="协议"><Select className="w-full" value={proto} onChange={e => setProto(e.target.value)}><option value="http">HTTP（可绑定域名）</option><option value="tcp">TCP</option><option value="udp">UDP</option></Select></Label>
      </div>
      {source === 'git' && <div className="text-[12px] text-dim -mt-1 mb-3">{t('在服务所在的服务器上执行 docker build，需要仓库里有 Dockerfile（不支持自动识别）。', "Runs docker build on the service's server; the repo needs a Dockerfile (no auto-detection).")}</div>}
      <div className="flex items-center justify-end gap-2 mt-2">
        {source === 'git' && git.repo && gitErr && <span className="mr-auto text-[12.5px] text-[#ef4444]">{gitErr}</span>}
        <Button variant="primary" disabled={!ready || busy} onClick={submit}>{busy && <Loader2 size={14} className="animate-spin" />}{source === 'git' ? t('构建并部署', 'Build and deploy') : '部署'}</Button>
      </div>
    </div>
  )
}

function DbPicker({ projectId, serverId, onDone }: { projectId: string; serverId: string; onDone: (s?: Service) => void }) {
  const t = useQuery('templates', api.templates)
  const [busy, setBusy] = useState<string>()
  const create = async (kind: string) => {
    setBusy(kind)
    const s = await run(() => api.createService(projectId, { name: kind === 'postgres' ? 'postgresql' : kind === 'mongo' ? 'mongodb' : kind, kind, ...(serverId ? { server_id: serverId } : {}) }), { ok: '数据库已创建，正在部署', invalidate: [`project:${projectId}`, 'projects'] })
    setBusy(undefined); onDone(s)
  }
  return (
    <div className="p-2">
      {t.error && <div className="p-4 text-[13px] text-[#ef4444]">{t.error.message}</div>}
      {!t.data && !t.error && <div className="p-6 flex justify-center text-muted"><Loader2 className="animate-spin" /></div>}
      {t.data?.map(x => (
        <button key={x.kind} disabled={!!busy} onClick={() => create(x.kind)} className="w-full text-left flex items-center gap-4 px-4 h-[68px] rounded-xl hover:bg-ov/[0.06]">
          <ServiceIcon s={{ kind: x.kind, letter: x.name[0] }} size={40} />
          <div className="flex-1"><div className="text-[14px] font-semibold">{x.name} <span className="font-mono text-[12px] text-dim font-normal ml-1">{x.image}</span></div><div className="text-[13px] text-muted mt-0.5">{x.description ?? '自动生成密码与连接变量'}</div></div>
          {busy === x.kind ? <Loader2 size={17} className="animate-spin text-muted" /> : <ChevronRight size={17} className="text-dim" />}
        </button>
      ))}
    </div>
  )
}
