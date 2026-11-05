import { useState } from 'react'
import { useMask } from '../../api/privacy'
import { Link } from 'react-router-dom'
import { Pencil, Check, X, Loader2 } from 'lucide-react'
import { Button, Card } from '../../components/ui'
import { ConfirmDialog, Modal, Select, TextInput } from '../../components/feedback'
import { EditGitModal, GithubIcon } from '../../components/GitSource'
import { isGit, repoPath } from '../../api/git'
import { api } from '../../api/client'
import { run, useBusy, useQuery } from '../../api/hooks'
import type { Project, Service } from '../../api/types'
import { L, useT } from '../../i18n'
import { imageVersionLabel, splitImage, useImageUpdate } from '../../api/updates'

export const Row = ({ t, d, children }: { t: string; d?: string; children: React.ReactNode }) => <div className="px-4 @2xl:px-6 py-5 flex flex-wrap items-center gap-x-6 gap-y-3"><div className="flex-1 min-w-[min(100%,220px)]"><div className="text-[13px] font-semibold">{t}</div>{d && <div className="text-[13px] text-muted mt-1 [overflow-wrap:anywhere]">{d}</div>}</div>{children}</div>
export const Sec = ({ t, children }: { t: string; children: React.ReactNode }) => <div className="mb-8"><div className="text-[13px] font-semibold mb-3">{t}</div><Card className="divide-y divide-line">{children}</Card></div>

export function SettingsTab({ p, s, onDeleted }: { p: Project; s: Service; onDeleted: () => void }) {
  const upd = useImageUpdate(s.id).data
  const pinnedHint = splitImage(s.image ?? '').digest ? L("已固定到 {0}；改回 :latest 即跟随最新", imageVersionLabel(s.image, upd?.current_version)) : undefined
  const [f, setF] = useState({ display: s.display_name ?? '', name: s.name, image: s.image, command: (s.command ?? []).join(' '), health: s.health?.http_path ?? '', cpu: s.cpu_limit ? String(s.cpu_limit) : '', mem: s.mem_limit_mb ? String(s.mem_limit_mb) : '' })
  const [del, setDel] = useState(false); const [move, setMove] = useState(false); const [gitEdit, setGitEdit] = useState(false)
  const { t } = useT()
  const git = isGit(s)
  const [busy, wrap] = useBusy()
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement>) => setF({ ...f, [k]: e.target.value })
  const onlyLabel = f.display.trim() !== (s.display_name ?? '') && f.name === s.name && f.image === s.image && f.command === (s.command ?? []).join(' ') && f.health === (s.health?.http_path ?? '') && f.cpu === (s.cpu_limit ? String(s.cpu_limit) : '') && f.mem === (s.mem_limit_mb ? String(s.mem_limit_mb) : '')
  const dirty = f.display.trim() !== (s.display_name ?? '') || f.name !== s.name || f.image !== s.image || f.command !== (s.command ?? []).join(' ') || f.health !== (s.health?.http_path ?? '') || f.cpu !== (s.cpu_limit ? String(s.cpu_limit) : '') || f.mem !== (s.mem_limit_mb ? String(s.mem_limit_mb) : '')
  const save = () => wrap(() => run(() => onlyLabel ? api.updateService(s.id, { display_name: f.display.trim() }) : api.updateService(s.id, {
    display_name: f.display.trim(), name: f.name, ...(git ? {} : { image: f.image }), command: f.command.trim() ? f.command.trim().split(/\s+/) : [],
    health: f.health ? { ...s.health, http_path: f.health } : undefined, cpu_limit: +f.cpu || 0, mem_limit_mb: +f.mem || 0,
  }), { ok: onlyLabel ? L("显示名称已保存") : L("已保存，正在重新部署"), invalidate: [`project:${p.id}`, 'projects'] }))
  return (
    <>
      <Sec t={L("基本")}>
        <Row t={L("显示名称")} d={L("只改面板里显示的名字，不影响内网主机名，也不会重新部署；留空则显示服务名称")}><TextInput className="w-full @4xl:w-[420px]" value={f.display} maxLength={64} placeholder={s.name} onChange={set('display')} /></Row>
        <Row t={L("服务名称")} d={L("也是项目内网的主机名；改了以后其他服务里引用它的地址要一起改")}><TextInput className="w-full @4xl:w-[420px]" value={f.name} onChange={set('name')} /></Row>
        {git && s.git
          ? <Row t={t('源码', 'Source')} d={t(`从 GitHub 构建：${repoPath(s.git.repo)}@${s.git.branch || 'main'}`, `Built from GitHub: ${repoPath(s.git.repo)}@${s.git.branch || 'main'}`)}><Button size="sm" onClick={() => setGitEdit(true)}><Pencil size={13} />{t('编辑源码设置', 'Edit git settings')}</Button></Row>
          : <Row t={L("镜像")} d={pinnedHint ?? L("Docker Hub、GHCR 或私有仓库")}><div className="flex flex-wrap items-center gap-2 w-full @4xl:w-auto"><TextInput mono className="w-full @4xl:w-[420px]" value={f.image} onChange={set('image')} />{s.kind === 'image' && <Button size="sm" variant="ghost" title={t('改为从 GitHub 构建', 'Build from GitHub')} onClick={() => setGitEdit(true)}><GithubIcon size={14} />{t('改用源码', 'Use source')}</Button>}</div></Row>}
      </Sec>
      <Sec t={L("启动")}>
        <Row t={L("启动命令")} d={L("覆盖镜像的 CMD")}><TextInput mono className="w-full @4xl:w-[420px]" value={f.command} onChange={set('command')} placeholder={L("使用镜像默认值")} /></Row>
        <Row t={L("健康检查")} d={L("HTTP 路径，失败 3 次后自动重启")}><TextInput mono className="w-full @4xl:w-[420px]" value={f.health} onChange={set('health')} placeholder={L("例如 /health")} /></Row>
      </Sec>
      <Sec t={L("资源限制")}>
        <Row t="CPU" d={L("核，留空不限")}><TextInput className="w-full @4xl:w-[420px]" value={f.cpu} onChange={set('cpu')} placeholder={L("不限")} /></Row>
        <Row t={L("内存")} d={L("MB，超出后容器会被 OOM 重启。建议填写：不限时一个服务泄漏内存可能拖垮整台机器")}><TextInput className="w-full @4xl:w-[420px]" value={f.mem} onChange={set('mem')} placeholder={L("不限")} /></Row>
      </Sec>
      {dirty && <div className="sticky bottom-4 z-10 mb-8 surface px-5 py-3 flex flex-wrap items-center gap-3 text-[13px]"><span className="flex-1 min-w-[min(100%,200px)] font-semibold">{onlyLabel ? L("有未保存的修改") : L("有未保存的修改，保存后会重新部署")}</span><Button size="sm" onClick={() => setF({ display: s.display_name ?? '', name: s.name, image: s.image, command: (s.command ?? []).join(' '), health: s.health?.http_path ?? '', cpu: s.cpu_limit ? String(s.cpu_limit) : '', mem: s.mem_limit_mb ? String(s.mem_limit_mb) : '' })}><X size={13} />{L("放弃")}</Button><Button size="sm" variant="primary" disabled={busy} onClick={save}>{busy ? <Loader2 size={13} className="animate-spin" /> : <Check size={13} />}{onlyLabel ? L("保存") : L("保存并部署")}</Button></div>}
      <Sec t={L("危险区域")}>
        <Row t={t('迁移到其他服务器', 'Move to another server')} d={t('只搬这个服务（容器与卷），项目其他服务经 tailnet 用同一个服务名访问它；迁移期间服务会停止。', 'Moves only this service (container and volumes); the rest of the project reaches it over the tailnet under the same name. It stops during the copy.')}>
          <Link to={`/projects/${p.id}/settings`}><Button variant="ghost" size="sm">{t('迁移整个项目', 'Move whole project')}</Button></Link>
          <Button variant="danger-outline" size="sm" onClick={() => setMove(true)}>{t('迁移服务', 'Move service')}</Button>
        </Row>
        <Row t={L("删除服务")} d={L("容器、卷与域名记录都会被删除")}><Button variant="danger" size="sm" onClick={() => setDel(true)}>{L("删除服务")}</Button></Row>
      </Sec>
      {move && <MigrateService p={p} s={s} onClose={() => setMove(false)} />}
      {gitEdit && <EditGitModal s={s} onClose={() => setGitEdit(false)} />}
      {del && <ConfirmDialog title={L("删除服务")} name={s.name} onClose={() => setDel(false)}
        description={<>{L("将删除")} <b>{p.name} / {s.name}</b> {L("的容器、")}{s.volumes?.length ?? 0} {L("个卷")}{s.domains?.length ? L(" 和 {0} 条域名记录", s.domains.length) : ''}{L("。此操作不可撤销。")}</>}
        onConfirm={async () => { const r = await run(() => api.deleteService(s.id), { ok: L("服务已删除"), invalidate: [`project:${p.id}`, 'projects', 'domains'] }); if (r !== undefined) onDeleted() }} />}
    </>
  )
}

function MigrateService({ p, s, onClose }: { p: Project; s: Service; onClose: () => void }) {
  const { t } = useT()
  const all = useQuery('servers', api.servers).data ?? []
  const settings = useQuery('settings', api.settings).data
  const cur = s.server_id || p.server_id
  const targets = all.filter(x => x.id !== cur && x.role !== 'control')
  const [to, setTo] = useState('')
  const mask = useMask()
  const target = to || targets.find(x => x.status === 'online')?.id || ''
  const tgt = all.find(x => x.id === target), from = all.find(x => x.id === cur), home = all.find(x => x.id === p.server_id)
  const crossServer = target !== p.server_id || p.services?.some(x => x.id !== s.id && (x.server_id || p.server_id) !== target)
  const tailnetOff = settings && settings.network_provider !== 'tailscale'
  const offTailnet = [tgt, home, from].filter((x, i, a) => x && a.indexOf(x) === i && !x.tailnet_ip).map(x => x!.name)
  const warn = crossServer && (tailnetOff ? t('跨服务器需要先在设置里启用 Tailscale。', 'Cross-server placement needs Tailscale enabled in Settings.') : offTailnet.length ? t(`这些服务器还没有加入内网：${offTailnet.join('、')}`, `Not on the tailnet yet: ${offTailnet.join(', ')}`) : '')
  const [busy, wrap] = useBusy()
  const go = () => wrap(async () => {
    const r = await run(() => api.migrateService(s.id, target), { ok: t('已开始迁移服务', 'Service move started'), invalidate: [`project:${p.id}`, 'projects'] })
    if (r !== undefined) onClose()
  })
  return (
    <Modal title={t('迁移服务', 'Move service')} onClose={onClose} footer={<><Button size="sm" onClick={onClose}>{t('取消', 'Cancel')}</Button><Button size="sm" variant="danger" disabled={!target || busy || tgt?.status !== 'online'} onClick={go}>{busy && <Loader2 size={13} className="animate-spin" />}{t('开始迁移', 'Start move')}</Button></>}>
      <div className="text-[13.5px] text-fg2 leading-6 mb-4">{t(`把 ${p.name} / ${s.name} 从 ${from?.name ?? cur} 搬到：`, `Move ${p.name} / ${s.name} from ${from?.name ?? cur} to:`)}</div>
      {targets.length ? (
        <Select className="w-full" value={target} onChange={e => setTo(e.target.value)}>
          {targets.map(x => <option key={x.id} value={x.id} disabled={x.status !== 'online'}>{x.name}{x.id === p.server_id ? t('（项目服务器）', ' (project server)') : ''}{x.tailnet_ip ? ` · ${mask(x.tailnet_ip)}` : ''}{x.status !== 'online' ? ` · ${t('离线', 'offline')}` : ''}</option>)}
        </Select>
      ) : <div className="text-[13px] text-muted">{t('没有其他服务器。', 'No other servers.')}</div>}
      {warn && <div className="mt-4 rounded-lg border-2 border-dashed border-[#f59e0b] px-3 py-2 text-[12.5px] text-fg2">{warn}</div>}
      <div className="mt-4 text-[12.5px] text-muted leading-5">{t('服务会停止、复制卷后在目标服务器启动；绑定的域名会改指向新服务器。', 'The service stops, its volumes are copied, then it starts on the target; bound domains are repointed.')}</div>
    </Modal>
  )
}
