import { useState } from 'react'
import { Plus, Trash2, UserRound } from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Badge, Button, Card } from '../components/ui'
import { ErrorState, Modal, Skeleton, TextInput } from '../components/feedback'
import { api } from '../api/client'
import { run, useQuery } from '../api/hooks'
import type { PanelUser, Project, Server } from '../api/types'
import { useProjects } from '../api/queries'
import { projName } from '../api/view'
import { useT } from '../i18n'
import { SettingsTabs } from './Security'
import { QuotaEditor, QuotaUsage } from '../components/MemberQuota'
import { quotaDraft, quotaDraftValid, quotaFromDraft } from '../api/quota'

/** Permissions an admin may give a sub-account (servers/settings/tokens stay admin-only). */
const PERMS: { k: string; zh: string; en: string }[] = [
  { k: 'deploy', zh: '部署与修改', en: 'Deploy & edit' },
  { k: 'exec', zh: '容器终端', en: 'Container shell' },
  { k: 'domains', zh: '绑定域名', en: 'Bind domains' },
  { k: 'delete', zh: '删除', en: 'Delete' },
  { k: 'restore', zh: '恢复备份', en: 'Restore backups' },
  { k: 'migrate', zh: '迁移', en: 'Migrate' },
]

/** Admin page: sub-accounts and the servers assigned to them (notes/subaccounts.md). */
export default function Users() {
  const { t } = useT()
  const q = useQuery('users', api.users)
  const servers = useQuery('servers', api.servers)
  const [edit, setEdit] = useState<PanelUser | 'new' | null>(null)
  const projects = useProjects().data ?? []
  const workers = (servers.data ?? []).filter(s => s.role !== 'control')
  const name = (id: string) => servers.data?.find(s => s.id === id)?.name ?? id
  return (
    <MainShell>
      <div className="max-w-[1240px] mx-auto px-4 pt-6 pb-12 @2xl:px-8 @2xl:pt-10 @5xl:px-14 @5xl:pt-12 @5xl:pb-16">
        <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em]">{t('设置', 'Settings')}</h1>
        <SettingsTabs />
        <div className="flex items-center justify-between mb-4">
          <p className="text-[13px] text-muted">{t('子账号只能看到分配给它的服务器，以及这些服务器上的项目。', 'A sub-account sees only the servers assigned to it and the projects on them.')}</p>
          <Button variant="primary" onClick={() => setEdit('new')}><Plus size={15} />{t('新建子账号', 'New sub-account')}</Button>
        </div>
        {q.error ? <ErrorState error={q.error} onRetry={q.refetch} /> : !q.data ? <Skeleton className="h-40" /> : (
          <Card className="divide-y divide-[var(--line)]">
            {q.data.map(u => (
              <div key={u.id} className="px-5 py-4 flex items-center gap-4">
                <UserRound size={18} className="text-muted shrink-0" />
                <div className="min-w-0 flex-1">
                  <div className="font-bold text-[14px] flex items-center gap-2">{u.username}
                    {u.role === 'admin' ? <Badge tone="violet">{t('管理员', 'Admin')}</Badge> : <Badge tone="zinc">{t('子账号', 'Sub-account')}</Badge>}
                    {u.disabled && <Badge tone="red">{t('已停用', 'Disabled')}</Badge>}
                  </div>
                  {u.role === 'member' && (
                    <div className="text-[12px] text-muted mt-1 truncate">
                      {(u.servers ?? []).length ? (u.servers ?? []).map(name).join(', ') : t('未分配服务器', 'No servers assigned')}
                      {' · '}{(u.permissions ?? []).join(', ')}
                    </div>
                  )}
                  {u.role === 'member' && u.quota && <div className="mt-2"><QuotaUsage quota={u.quota} usage={u.usage} compact /></div>}
                </div>
                {u.role === 'member' && <Button size="sm" onClick={() => setEdit(u)}>{t('编辑', 'Edit')}</Button>}
              </div>
            ))}
          </Card>
        )}
        {edit && <UserModal user={edit === 'new' ? undefined : edit} servers={workers} projects={projects} onClose={() => setEdit(null)} />}
      </div>
    </MainShell>
  )
}

/** Projects already on a server: a member given that server sees and runs them (notes/subaccounts.md). */
function sharedProjects(projects: Project[], serverId: string) { return projects.filter(p => p.server_id === serverId) }

function UserModal({ user, servers, projects, onClose }: { user?: PanelUser; servers: Server[]; projects: Project[]; onClose: () => void }) {
  const { t } = useT()
  const [username, setUsername] = useState(user?.username ?? '')
  const [password, setPassword] = useState('')
  const [perms, setPerms] = useState<string[]>(user?.permissions ?? ['read', 'deploy'])
  const [srv, setSrv] = useState<string[]>(user?.servers ?? [])
  const [quota, setQuota] = useState(quotaDraft(user?.quota))
  const [busy, setBusy] = useState(false)
  const toggle = (list: string[], set: (v: string[]) => void, k: string) => set(list.includes(k) ? list.filter(x => x !== k) : [...list, k])
  const save = async () => {
    setBusy(true)
    const body = { permissions: perms, servers: srv, quota: quotaFromDraft(quota), ...(password ? { password } : {}) }
    const r = await run(() => user ? api.updateUser(user.id, body) : api.createUser({ ...body, username, password }), { ok: t('已保存', 'Saved'), invalidate: ['users'] })
    setBusy(false)
    if (r) onClose()
  }
  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true)
    const r = await run(fn, { ok: t('已完成', 'Done'), invalidate: ['users'] })
    setBusy(false)
    if (r !== undefined) onClose()
  }
  const box = 'flex items-center gap-2 text-[13px] py-1 cursor-pointer'
  return (
    <Modal title={user ? t(`编辑 ${user.username}`, `Edit ${user.username}`) : t('新建子账号', 'New sub-account')} onClose={onClose}
      footer={<div className="flex items-center gap-2 w-full">
        {user && <Button variant="danger-outline" size="sm" disabled={busy} onClick={() => act(() => api.updateUser(user.id, { disabled: !user.disabled }))}>{user.disabled ? t('启用', 'Enable') : t('停用', 'Disable')}</Button>}
        {user && <Button variant="danger-outline" size="sm" disabled={busy} onClick={() => confirm(t(`删除 ${user.username}？它的会话和令牌会一并失效。`, `Delete ${user.username}? Its sessions and tokens stop working.`)) && act(() => api.deleteUser(user.id).then(() => true))}><Trash2 size={14} /></Button>}
        <div className="flex-1" />
        <Button onClick={onClose}>{t('取消', 'Cancel')}</Button>
        <Button variant="primary" disabled={busy || !quotaDraftValid(quota) || (!user && (!username || password.length < 8))} onClick={save}>{t('保存', 'Save')}</Button>
      </div>}>
      <div className="flex flex-col gap-4">
        {!user && <TextInput placeholder={t('用户名', 'Username')} value={username} onChange={e => setUsername(e.target.value)} autoComplete="off" />}
        <TextInput type="password" placeholder={user ? t('重置密码（留空不改）', 'Reset password (blank = keep)') : t('初始密码（至少 8 位）', 'Initial password (8+ chars)')} value={password} onChange={e => setPassword(e.target.value)} autoComplete="new-password" />
        <div>
          <div className="text-[12px] font-bold text-muted mb-1">{t('服务器', 'Servers')}</div>
          {servers.length === 0 && <div className="text-[13px] text-muted">{t('还没有可分配的工作机', 'No worker servers yet')}</div>}
          {servers.map(s => {
            const shared = sharedProjects(projects, s.id)
            // warn when a server is newly given to this member and already runs projects
            const warn = srv.includes(s.id) && !user?.servers?.includes(s.id) && shared.length > 0
            return <div key={s.id}>
              <label className={box}><input type="checkbox" checked={srv.includes(s.id)} onChange={() => toggle(srv, setSrv, s.id)} />{s.name}
                {shared.length > 0 && <span className="text-[12px] text-muted">{t(`· ${shared.length} 个项目`, `· ${shared.length} project(s)`)}</span>}</label>
              {warn && <div role="alert" className="ml-6 mb-1 rounded-lg bg-[#fde047]/30 px-3 py-2 text-[12.5px] leading-5">
                {t(`这台服务器上已有 ${shared.length} 个项目（${shared.slice(0, 3).map(projName).join('、')}${shared.length > 3 ? ' 等' : ''}），分配后该子账号能看到并按权限操作它们。不想共享的话，先把它们迁到别的服务器。`,
                  `This server already runs ${shared.length} project(s) (${shared.slice(0, 3).map(projName).join(', ')}${shared.length > 3 ? ', …' : ''}). Once assigned, this sub-account sees them and can act on them within its permissions. Move them to another server first if they should stay private.`)}
              </div>}
            </div>
          })}
        </div>
        <div>
          <div className="text-[12px] font-bold text-muted mb-1">{t('权限（始终可查看）', 'Permissions (viewing is always allowed)')}</div>
          {PERMS.map(p => <label key={p.k} className={box}><input type="checkbox" checked={perms.includes(p.k)} onChange={() => toggle(perms, setPerms, p.k)} />{t(p.zh, p.en)}</label>)}
        </div>
        <QuotaEditor value={quota} onChange={setQuota} />
        {user?.usage && <div className="-mt-2"><div className="text-[12px] text-muted mb-1">{t('当前用量', 'Current usage')}</div><QuotaUsage quota={quotaFromDraft(quota)} usage={user.usage} /></div>}
      </div>
    </Modal>
  )
}
