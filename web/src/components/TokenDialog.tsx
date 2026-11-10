import { useState } from 'react'
import { Loader2, AlertTriangle, Check, ShieldAlert } from 'lucide-react'
import { Button } from './ui'
import { Label, Modal, TextInput, Toggle } from './feedback'
import { api } from '../api/client'
import { run, useBusy, useQuery } from '../api/hooks'
import { projName } from '../api/view'
import { ALL_PERMISSIONS, type APIToken, type Permission } from '../api/types'
import { DANGEROUS, isGlobal, permInfo, permsOf } from '../api/tokenPerms'
import { L, useT } from '../i18n'
import { cx } from './ui-utils'
import { useProjects } from '../api/queries'

const PRESETS: { k: string; zh: string; en: string; perms: Permission[] }[] = [
  { k: 'read', zh: L("只读"), en: 'Read-only', perms: ['read'] },
  { k: 'deploy', zh: L("部署"), en: 'Deploy', perms: ['read', 'deploy'] },
  { k: 'all', zh: L("全部"), en: 'All', perms: ALL_PERMISSIONS },
]

/** Create (POST /tokens) or edit (PATCH /tokens/{id}) a token. */
export function TokenDialog({ token, onClose, onSaved }: { token?: APIToken; onClose: () => void; onSaved: (t: APIToken) => void }) {
  const { t } = useT()
  const info = permInfo(t)
  const projects = useProjects().data ?? []
  // a member's token holds at most the member's own permissions (never the global ones) and needs no approval
  const me = useQuery('me', api.me).data
  const member = me?.role === 'member'
  const avail = member ? ALL_PERMISSIONS.filter(p => !isGlobal(p) && (me?.permissions ?? ['read']).includes(p)) : ALL_PERMISSIONS
  const [name, setName] = useState(token?.name ?? '')
  const [perms, setPerms] = useState<string[]>(() => token ? permsOf(token) : ['read', 'deploy'].filter(p => avail.includes(p as Permission)))
  const [scoped, setScoped] = useState(!!token?.projects?.length)
  const [pids, setPids] = useState<string[]>(token?.projects ?? [])
  const [approval, setApproval] = useState(!!token?.require_approval)
  const [busy, wrap] = useBusy()
  const has = (p: string) => perms.includes(p)
  const toggle = (p: Permission) => { if (p === 'read') return; setPerms(x => x.includes(p) ? x.filter(y => y !== p) : ALL_PERMISSIONS.filter(y => y === p || x.includes(y))) }
  const presetOf = (ps: Permission[]) => ps.filter(p => avail.includes(p) && !(scoped && isGlobal(p)))
  const preset = (ps: Permission[]) => setPerms(presetOf(ps))
  const setScope = (v: boolean) => { setScoped(v); if (v) setPerms(x => x.filter(p => !isGlobal(p))) }
  const activePreset = PRESETS.find(pr => { const want = presetOf(pr.perms); return want.length === perms.length && want.every(has) })?.k
  const danger = DANGEROUS.filter(has)
  const valid = name.trim() && (!scoped || pids.length > 0)
  const submit = () => wrap(async () => {
    if (!valid) return
    const body = { name: name.trim(), permissions: ALL_PERMISSIONS.filter(has), projects: scoped ? pids : [], require_approval: approval }
    const r = token
      ? await run(() => api.updateToken(token.id, body), { ok: t('令牌已更新', 'Token updated'), invalidate: ['tokens'] })
      : await run(() => api.createToken(body), { invalidate: ['tokens'] })
    if (r) onSaved(r)
  })
  const box = (on: boolean, disabled?: boolean) => cx('h-[18px] w-[18px] shrink-0 rounded-[3px] border-2 border-[var(--edge)] grid place-items-center', on ? 'bg-t-violet text-ink' : 'bg-card', disabled && 'opacity-40')
  return (
    <Modal title={token ? t('编辑 API 令牌', 'Edit API token') : t('创建 API 令牌', 'Create API token')} width={640} onClose={onClose}
      footer={<><Button size="sm" onClick={onClose}>{t('取消', 'Cancel')}</Button><Button size="sm" variant="primary" disabled={!valid || busy} onClick={submit}>{busy && <Loader2 size={13} className="animate-spin" />}{token ? t('保存', 'Save') : t('创建', 'Create')}</Button></>}>
      <Label t={t('名称', 'Name')} d={t('写清楚给谁用，方便以后吊销', 'Say who it is for so it is easy to revoke later')}><TextInput autoFocus className="w-full" value={name} onChange={e => setName(e.target.value)} placeholder="claude-code-macbook" /></Label>

      <div className="mb-4">
        <div className="flex flex-wrap items-center justify-between gap-2 mb-2">
          <div className="text-[13px] font-semibold">{t('权限', 'Permissions')}</div>
          <div className="flex gap-1.5">{PRESETS.map(pr => (
            <button key={pr.k} type="button" onClick={() => preset(pr.perms)} className={cx('h-7 px-2.5 rounded-[4px] text-[12px] font-bold font-mono italic border-2', activePreset === pr.k ? 'border-[var(--edge)] bg-hi text-hi-ink shadow-hard-sm' : 'border-line2 text-muted hover:text-fg')}>{t(pr.zh, pr.en)}</button>
          ))}</div>
        </div>
        <div className="rounded-lg field divide-y divide-line">
          {avail.map(p => {
            const disabled = p === 'read' || (scoped && isGlobal(p))
            return (
              <button key={p} type="button" disabled={disabled} onClick={() => toggle(p)} className={cx('w-full text-left px-3.5 py-2.5 flex items-start gap-3', !disabled && 'hover:bg-ov/5')}>
                <span className={cx(box(has(p), disabled && p !== 'read'), 'mt-0.5')}>{has(p) && <Check size={12} strokeWidth={3.5} />}</span>
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-2 text-[13px] font-semibold">{info[p][0]}<span className="font-mono text-[11.5px] text-dim font-normal">{p}</span>{(DANGEROUS as string[]).includes(p) && <ShieldAlert size={13} className="text-danger" />}</span>
                  <span className="block text-[12px] text-muted mt-0.5 leading-5">{scoped && isGlobal(p) ? t('全局权限，不能和项目范围一起用', 'Global permission — cannot be combined with a project scope') : p === 'read' ? t('始终包含。', 'Always included. ') + info[p][1] : info[p][1]}</span>
                </span>
              </button>
            )
          })}
        </div>
        {danger.length > 0 && <div className="mt-3 rounded-lg border-2 border-[var(--edge)] bg-t-red text-ink px-3.5 py-2.5 text-[12.5px] leading-5 flex gap-2 shadow-hard-md">
          <AlertTriangle size={15} className="shrink-0 mt-0.5" />
          <span><b>{t('高风险权限：', 'High-risk permissions: ')}</b>{danger.map(p => info[p][0]).join(t('、', ', '))}{L("。")}{t('拿到这个令牌的人或 agent 可以直接删除数据、迁移服务或接管其他令牌。', 'Whoever holds this token can delete data, move services or take over other tokens directly.')}{!approval && t('建议同时开启下面的「需要面板审批」。', ' Consider turning on "require panel approval" below.')}</span>
        </div>}
      </div>

      <div className="mb-4">
        <div className="text-[13px] font-semibold mb-2">{t('项目范围', 'Project scope')}</div>
        <div className="flex gap-2 mb-2">
          {[[false, t('所有项目', 'All projects')], [true, t('指定项目', 'Selected projects')]].map(([v, l]) => (
            <button key={String(v)} type="button" onClick={() => setScope(v as boolean)} className={cx('h-9 px-3 rounded-[4px] text-[13px] font-semibold border-2', scoped === v ? 'nav-active' : 'border-transparent text-muted hover:text-fg hover:border-line2')}>{l as string}</button>
          ))}
        </div>
        {scoped && <>
          <div className="rounded-lg field max-h-[180px] overflow-auto divide-y divide-line">
            {!projects.length && <div className="px-3.5 py-3 text-[12.5px] text-muted">{t('还没有项目', 'No projects yet')}</div>}
            {projects.map(p => {
              const on = pids.includes(p.id)
              return (
                <button key={p.id} type="button" onClick={() => setPids(x => on ? x.filter(y => y !== p.id) : [...x, p.id])} className="w-full text-left px-3.5 h-10 flex items-center gap-3 hover:bg-ov/5 text-[13px]">
                  <span className={box(on)}>{on && <Check size={12} strokeWidth={3.5} />}</span><span className="truncate min-w-0" title={p.name}>{projName(p)}</span><span className="font-mono text-[11.5px] text-dim truncate shrink-[2] min-w-0">{p.id}</span>
                </button>
              )
            })}
          </div>
          <div className="text-[12px] text-dim mt-1.5">{pids.length ? t(`已选 ${pids.length} 个项目。服务器、设置、令牌这类全局权限不可用。`, `${pids.length} selected. Global permissions (servers, settings, tokens) are unavailable.`) : t('至少选择一个项目', 'Pick at least one project')}</div>
        </>}
      </div>

      {member && <div className="text-[12px] text-muted leading-5">{t('令牌不会超过你账号的权限和服务器；管理员收回你的权限或停用账号时，令牌随之受限或失效。', 'A token never goes beyond your account\'s permissions and servers; when the admin narrows or disables your account, the token follows at once.')}</div>}
      {!member && <div className="flex items-start gap-4 rounded-lg border-2 border-dashed border-line2 px-3.5 py-3">
        <div className="flex-1 min-w-0">
          <div className="text-[13px] font-semibold">{t('即使有权限也需要面板审批', 'Require panel approval even with permission')}</div>
          <div className="text-[12px] text-muted mt-0.5 leading-5">{t('开启后，删除、迁移、改 DNS、还原等操作会进入「审批」，由你在面板里批准后才执行。', 'Deletes, moves, DNS changes and restores wait in Confirmations until you approve them in the panel.')}</div>
        </div>
        <Toggle on={approval} onChange={setApproval} />
      </div>}
    </Modal>
  )
}
