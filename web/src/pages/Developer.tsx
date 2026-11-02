import { useState } from 'react'
import { Plus, Terminal, KeyRound, Trash2, Bot, Loader2, AlertTriangle, Pencil, Check, ShieldAlert, FolderLock } from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Badge, Button, Card, cx } from '../components/ui'
import { CodeBlock, ConfirmDialog, CopyButton, Empty, ErrorState, Label, Modal, RowsSkeleton, TextInput, Toggle } from '../components/feedback'
import { api } from '../api/client'
import { run, useBusy, useQuery } from '../api/hooks'
import { ago } from '../api/view'
import { ALL_PERMISSIONS, GLOBAL_PERMISSIONS, type APIToken, type Permission } from '../api/types'
import { useProjects } from './Projects'
import { useT } from '../i18n'

type Tr = (zh: string, en: string) => string
/** label + what it allows, per permission (internal/api Perm*) */
const permInfo = (t: Tr): Record<Permission, [string, string]> => ({
  read: [t('只读', 'read'), t('查看项目、服务、变量（打码）、日志、指标、设置', 'View projects, services, env (masked), logs, metrics, settings')],
  deploy: [t('部署', 'deploy'), t('创建和修改项目与服务、变量、启停重部署、回滚、exec、镜像更新、备份策略、导入', 'Create/edit projects and services, env, start/stop/redeploy, rollback, exec, image updates, backup policy, imports')],
  delete: [t('删除', 'delete'), t('删除服务和项目（连同卷）', 'Delete services and projects (with their volumes)')],
  migrate: [t('迁移', 'migrate'), t('在服务器间迁移项目和服务，Zeabur 切换与回滚', 'Move projects/services between servers, Zeabur cutover and rollback')],
  domains: [t('域名', 'domains'), t('绑定和移除域名（会改 DNS）', 'Bind and remove domains (changes DNS)')],
  restore: [t('还原', 'restore'), t('还原备份（覆盖线上数据）', 'Restore backups (replaces live data)')],
  servers: [t('服务器', 'servers'), t('加入令牌、改名和移除服务器、内网切换', 'Join tokens, rename/remove servers, tailnet actions')],
  settings: [t('设置', 'settings'), t('面板设置（Cloudflare、证书、备份存储、私有网络）', 'Panel settings (Cloudflare, TLS, backup storage, private network)')],
  tokens: [t('令牌', 'tokens'), t('查看、创建、修改、删除 API 令牌（不超过自身权限）', 'List/create/change/delete API tokens (never beyond its own)')],
})
const DANGEROUS: Permission[] = ['delete', 'migrate', 'restore', 'servers', 'tokens']
const isGlobal = (p: string) => (GLOBAL_PERMISSIONS as string[]).includes(p)
const permTone = (p: string) => p === 'read' ? 'zinc' : p === 'deploy' ? 'violet' : (DANGEROUS as string[]).includes(p) ? 'red' : 'amber'
/** older servers only report scope */
const permsOf = (tk: APIToken): string[] => tk.permissions?.length ? tk.permissions : tk.scope === 'admin' ? ALL_PERMISSIONS : tk.scope === 'deploy' ? ['read', 'deploy'] : ['read']

export default function Developer() {
  const { t } = useT()
  const info = permInfo(t)
  const q = useQuery('tokens', api.tokens)
  const projects = useProjects().data ?? []
  const [edit, setEdit] = useState<APIToken>()
  const panel = useQuery('settings', api.settings).data?.public_url || location.origin
  const [create, setCreate] = useState(false)
  const [fresh, setFresh] = useState<APIToken>()
  const [revoke, setRevoke] = useState<APIToken>()
  const tok = fresh?.token ?? 'atl_pat_xxx'
  return (
    <MainShell>
      <div className="max-w-[1180px] mx-auto px-4 pt-6 pb-12 @2xl:px-8 @2xl:pt-10 @5xl:px-16 @5xl:pt-14 @5xl:pb-16">
        <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em]">{t('CLI 与 API', 'CLI & API')}</h1>
        <p className="text-[13px] text-muted mt-2 max-w-[760px] leading-6">{t('Atoll 本身不内置 AI。用 API 令牌把 CLI 或 MCP 交给 Claude Code、Codex 这类外部 agent，让它们替你部署和排错。', 'Atoll has no built-in AI. Give the CLI or MCP to an external agent such as Claude Code or Codex with an API token and let it deploy and debug for you.')}</p>

        <div className="mt-8 flex flex-wrap items-center justify-between gap-3 mb-3"><div className="text-[15px] font-semibold flex items-center gap-2"><KeyRound size={18} />{t('API 令牌', 'API tokens')}</div><Button variant="primary" onClick={() => setCreate(true)}><Plus size={15} />{t('创建令牌', 'Create token')}</Button></div>
        {fresh?.token && (
          <Card className="p-5 mb-5 !bg-[#86efac] text-[#141414]">
            <div className="flex items-center gap-2 text-[13.5px] font-bold"><AlertTriangle size={16} />{t(`令牌「${fresh.name}」已创建，只显示这一次`, `Token "${fresh.name}" created — shown only once`)}</div>
            <div className="mt-3 flex flex-wrap items-center gap-3">
              <div className="flex-1 basis-full @2xl:basis-0 min-w-0 h-11 rounded-[4px] border-2 border-[#141414] bg-[#fffdf8] px-3.5 flex items-center font-mono text-[13px] truncate">{fresh.token}</div>
              <CopyButton text={fresh.token} label={t('复制令牌', 'Copy token')} />
              <Button size="sm" onClick={() => setFresh(undefined)}>{t('我已保存', "I've saved it")}</Button>
            </div>
            <div className="text-[12.5px] mt-2">{t('下方的命令已自动填入这个令牌。', 'The commands below already contain this token.')}</div>
          </Card>
        )}
        {q.error && !q.data && <ErrorState error={q.error} onRetry={q.refetch} />}
        {!q.data && !q.error && <RowsSkeleton n={3} />}
        {q.data && !q.data.length && <Empty icon={<KeyRound size={22} />} title={t('还没有 API 令牌', 'No API tokens yet')} desc={t('创建一个令牌给 CLI 或 AI agent 使用。令牌只在创建时显示一次。', 'Create a token for the CLI or an AI agent. It is shown only once.')} />}
        {q.data && q.data.length > 0 && (
          <Card className="divide-y divide-line">
            <div className="hidden px-6 h-10 @4xl:grid grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)_minmax(0,2fr)_minmax(0,1.1fr)_minmax(0,0.8fr)_72px] gap-4 items-center text-[12.5px] text-muted"><span>{t('名称', 'Name')}</span><span>{t('令牌', 'Token')}</span><span>{t('权限', 'Permissions')}</span><span>{t('项目范围', 'Projects')}</span><span>{t('最近使用', 'Last used')}</span><span /></div>
            {q.data.map(tk => {
              const perms = permsOf(tk)
              const scoped = !!tk.projects?.length
              const names = (tk.projects ?? []).map(id => projects.find(p => p.id === id)?.name ?? id)
              return (
                <div key={tk.id} className="px-4 @2xl:px-6 min-h-16 py-3 grid grid-cols-[minmax(0,1fr)_auto] @4xl:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)_minmax(0,2fr)_minmax(0,1.1fr)_minmax(0,0.8fr)_72px] gap-x-4 gap-y-2 items-center text-[13px] [&>*:nth-child(n+2):nth-child(-n+5)]:@max-4xl:col-span-2">
                  <div className="min-w-0"><div className="font-semibold truncate" title={tk.name}>{tk.name}</div><div className="text-[12px] text-dim mt-0.5">{t('创建于', 'Created')} {ago(tk.created_at)}</div></div>
                  <span className="font-mono text-[12.5px] text-muted truncate">{tk.prefix}••••••••</span>
                  <div className="min-w-0 flex flex-wrap items-center gap-1.5">
                    {perms.length === ALL_PERMISSIONS.length ? <Badge tone="red">{t('全部权限', 'all')}</Badge> : perms.map(p => <span key={p} title={info[p as Permission]?.[1]}><Badge tone={permTone(p)}>{info[p as Permission]?.[0] ?? p}</Badge></span>)}
                    {tk.require_approval && <Badge tone="amber"><ShieldAlert size={11} />{t('需审批', 'approval')}</Badge>}
                  </div>
                  <div className="min-w-0 text-[12.5px]" title={names.join(', ')}>{scoped ? <span className="flex items-center gap-1.5"><FolderLock size={13} className="shrink-0 text-muted" /><span className="truncate">{names.join(', ')}</span></span> : <span className="text-muted">{t('所有项目', 'All projects')}</span>}</div>
                  <span className="text-muted @max-4xl:text-[12px]"><span className="@4xl:hidden">{t('最近使用：', 'Last used: ')}</span>{tk.last_used_at ? ago(tk.last_used_at) : t('从未使用', 'Never')}</span>
                  <div className="flex items-center justify-end @max-4xl:row-start-1 @max-4xl:col-start-2">
                    <button title={t('编辑', 'Edit')} onClick={() => setEdit(tk)} className="h-8 w-8 grid place-items-center text-muted hover:text-fg"><Pencil size={15} /></button>
                    <button title={t('吊销', 'Revoke')} onClick={() => setRevoke(tk)} className="h-8 w-8 grid place-items-center text-muted hover:text-[#ef4444]"><Trash2 size={15} /></button>
                  </div>
                </div>
              )
            })}
          </Card>
        )}

        <div className="mt-10 text-[15px] font-semibold flex items-center gap-2 mb-3"><Terminal size={18} />CLI</div>
        <div className="grid grid-cols-1 @4xl:grid-cols-2 gap-5 [&>*]:min-w-0">
          <Card className="p-5"><div className="text-[13px] font-semibold mb-3">{t('安装并登录', 'Install and log in')}</div><CodeBlock>{`# 从 GitHub Releases 下载 atoll 并放进 PATH
atoll login --url ${panel} \\
  --token ${tok}`}</CodeBlock></Card>
          <Card className="p-5"><div className="text-[13px] font-semibold mb-3">{t('常用命令', 'Common commands')}</div><CodeBlock>{`atoll deploy ghcr.io/me/api:v1.9 -p saas-api
atoll env set -s api DATABASE_URL=...
atoll logs api -f
atoll domain add api.example.com -s api`}</CodeBlock></Card>
        </div>

        <div className="mt-10 text-[15px] font-semibold flex items-center gap-2 mb-1"><Bot size={18} />{t('接入 AI agent（MCP）', 'Connect an AI agent (MCP)')}</div>
        <div className="text-[13px] text-muted mb-4 leading-6">{t('agent 能做什么取决于令牌的权限：没有的权限直接拒绝（403）；开启「需要面板审批」的令牌，删除、迁移、改 DNS 这类操作会进入「待确认」，由你在面板里批准。', "What an agent can do follows its token's permissions: anything missing is refused (403). With \"require panel approval\" on, deletes, moves and DNS changes wait in Confirmations for you to approve.")}</div>
        <div className="grid grid-cols-1 @4xl:grid-cols-2 gap-5 [&>*]:min-w-0">
          <Card className="p-5"><div className="flex flex-wrap items-center gap-2 text-[13px] font-semibold mb-3">Claude Code<Badge tone="zinc">{t('一行添加', 'one line')}</Badge></div><CodeBlock>{`claude mcp add atoll -- atoll mcp \\
  --url ${panel} --token ${tok}`}</CodeBlock></Card>
          <Card className="p-5"><div className="flex flex-wrap items-center gap-2 text-[13px] font-semibold mb-3">Codex<Badge tone="zinc">~/.codex/config.toml</Badge></div><CodeBlock>{`[mcp_servers.atoll]
command = "atoll"
args = ["mcp", "--url", "${panel}",
        "--token", "${tok}"]`}</CodeBlock></Card>
        </div>
        <Card className="p-5 mt-5"><div className="text-[13px] font-semibold mb-3">REST API</div><CodeBlock>{`curl ${panel}/api/v1/projects \\
  -H "Authorization: Bearer ${tok}"`}</CodeBlock></Card>
      </div>
      {create && <TokenDialog onClose={() => setCreate(false)} onSaved={tk => { setFresh(tk); setCreate(false) }} />}
      {edit && <TokenDialog token={edit} onClose={() => setEdit(undefined)} onSaved={() => setEdit(undefined)} />}
      {revoke && <ConfirmDialog title={t('吊销令牌', 'Revoke token')} action={t('确认吊销', 'Revoke')} name={revoke.name} onClose={() => setRevoke(undefined)}
        description={<>{t('使用', 'CLIs and agents using')} <span className="font-mono">{revoke.prefix}…</span> {t('的 CLI 和 agent 会立即失去访问权限。', 'lose access immediately.')}</>}
        onConfirm={() => run(() => api.deleteToken(revoke.id), { ok: t('令牌已吊销', 'Token revoked'), invalidate: ['tokens'] }).then(() => { if (fresh?.id === revoke.id) setFresh(undefined) })} />}
    </MainShell>
  )
}

const PRESETS: { k: string; zh: string; en: string; perms: Permission[] }[] = [
  { k: 'read', zh: '只读', en: 'Read-only', perms: ['read'] },
  { k: 'deploy', zh: '部署', en: 'Deploy', perms: ['read', 'deploy'] },
  { k: 'all', zh: '全部', en: 'All', perms: ALL_PERMISSIONS },
]

/** Create (POST /tokens) or edit (PATCH /tokens/{id}) a token. */
function TokenDialog({ token, onClose, onSaved }: { token?: APIToken; onClose: () => void; onSaved: (t: APIToken) => void }) {
  const { t } = useT()
  const info = permInfo(t)
  const projects = useProjects().data ?? []
  const [name, setName] = useState(token?.name ?? '')
  const [perms, setPerms] = useState<string[]>(() => token ? permsOf(token) : ['read', 'deploy'])
  const [scoped, setScoped] = useState(!!token?.projects?.length)
  const [pids, setPids] = useState<string[]>(token?.projects ?? [])
  const [approval, setApproval] = useState(!!token?.require_approval)
  const [busy, wrap] = useBusy()
  const has = (p: string) => perms.includes(p)
  const toggle = (p: Permission) => { if (p === 'read') return; setPerms(x => x.includes(p) ? x.filter(y => y !== p) : ALL_PERMISSIONS.filter(y => y === p || x.includes(y))) }
  const preset = (ps: Permission[]) => setPerms(scoped ? ps.filter(p => !isGlobal(p)) : ps)
  const setScope = (v: boolean) => { setScoped(v); if (v) setPerms(x => x.filter(p => !isGlobal(p))) }
  const activePreset = PRESETS.find(pr => { const want = scoped ? pr.perms.filter(p => !isGlobal(p)) : pr.perms; return want.length === perms.length && want.every(has) })?.k
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
  const box = (on: boolean, disabled?: boolean) => cx('h-[18px] w-[18px] shrink-0 rounded-[3px] border-2 border-[var(--edge)] grid place-items-center', on ? 'bg-[#c4b5fd] text-[#141414]' : 'bg-card', disabled && 'opacity-40')
  return (
    <Modal title={token ? t('编辑 API 令牌', 'Edit API token') : t('创建 API 令牌', 'Create API token')} width={640} onClose={onClose}
      footer={<><Button size="sm" onClick={onClose}>{t('取消', 'Cancel')}</Button><Button size="sm" variant="primary" disabled={!valid || busy} onClick={submit}>{busy && <Loader2 size={13} className="animate-spin" />}{token ? t('保存', 'Save') : t('创建', 'Create')}</Button></>}>
      <Label t={t('名称', 'Name')} d={t('写清楚给谁用，方便以后吊销', 'Say who it is for so it is easy to revoke later')}><TextInput autoFocus className="w-full" value={name} onChange={e => setName(e.target.value)} placeholder="claude-code-macbook" /></Label>

      <div className="mb-4">
        <div className="flex flex-wrap items-center justify-between gap-2 mb-2">
          <div className="text-[13px] font-semibold">{t('权限', 'Permissions')}</div>
          <div className="flex gap-1.5">{PRESETS.map(pr => (
            <button key={pr.k} type="button" onClick={() => preset(pr.perms)} className={cx('h-7 px-2.5 rounded-[4px] text-[12px] font-bold font-mono italic border-2', activePreset === pr.k ? 'border-[var(--edge)] bg-[#fde047] text-[#141414] shadow-[2px_2px_0_0_var(--edge)]' : 'border-line2 text-muted hover:text-fg')}>{t(pr.zh, pr.en)}</button>
          ))}</div>
        </div>
        <div className="rounded-lg field divide-y divide-line">
          {ALL_PERMISSIONS.map(p => {
            const disabled = p === 'read' || (scoped && isGlobal(p))
            return (
              <button key={p} type="button" disabled={disabled} onClick={() => toggle(p)} className={cx('w-full text-left px-3.5 py-2.5 flex items-start gap-3', !disabled && 'hover:bg-ov/5')}>
                <span className={cx(box(has(p), disabled && p !== 'read'), 'mt-0.5')}>{has(p) && <Check size={12} strokeWidth={3.5} />}</span>
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-2 text-[13px] font-semibold">{info[p][0]}<span className="font-mono text-[11.5px] text-dim font-normal">{p}</span>{(DANGEROUS as string[]).includes(p) && <ShieldAlert size={13} className="text-[#ef4444]" />}</span>
                  <span className="block text-[12px] text-muted mt-0.5 leading-5">{scoped && isGlobal(p) ? t('全局权限，不能和项目范围一起用', 'Global permission — cannot be combined with a project scope') : p === 'read' ? t('始终包含。', 'Always included. ') + info[p][1] : info[p][1]}</span>
                </span>
              </button>
            )
          })}
        </div>
        {danger.length > 0 && <div className="mt-3 rounded-lg border-2 border-[var(--edge)] bg-[#fca5a5] text-[#141414] px-3.5 py-2.5 text-[12.5px] leading-5 flex gap-2 shadow-[3px_3px_0_0_var(--edge)]">
          <AlertTriangle size={15} className="shrink-0 mt-0.5" />
          <span><b>{t('高风险权限：', 'High-risk permissions: ')}</b>{danger.map(p => info[p][0]).join(t('、', ', '))}。{t('拿到这个令牌的人或 agent 可以直接删除数据、迁移服务或接管其他令牌。', 'Whoever holds this token can delete data, move services or take over other tokens directly.')}{!approval && t('建议同时开启下面的「需要面板审批」。', ' Consider turning on "require panel approval" below.')}</span>
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
                  <span className={box(on)}>{on && <Check size={12} strokeWidth={3.5} />}</span><span className="truncate min-w-0" title={p.name}>{p.name}</span><span className="font-mono text-[11.5px] text-dim truncate shrink-[2] min-w-0">{p.id}</span>
                </button>
              )
            })}
          </div>
          <div className="text-[12px] text-dim mt-1.5">{pids.length ? t(`已选 ${pids.length} 个项目。服务器、设置、令牌这类全局权限不可用。`, `${pids.length} selected. Global permissions (servers, settings, tokens) are unavailable.`) : t('至少选择一个项目', 'Pick at least one project')}</div>
        </>}
      </div>

      <div className="flex items-start gap-4 rounded-lg border-2 border-dashed border-line2 px-3.5 py-3">
        <div className="flex-1 min-w-0">
          <div className="text-[13px] font-semibold">{t('即使有权限也需要面板审批', 'Require panel approval even with permission')}</div>
          <div className="text-[12px] text-muted mt-0.5 leading-5">{t('开启后，删除、迁移、改 DNS、还原等操作会进入「待确认」，由你在面板里批准后才执行。', 'Deletes, moves, DNS changes and restores wait in Confirmations until you approve them in the panel.')}</div>
        </div>
        <Toggle on={approval} onChange={setApproval} />
      </div>
    </Modal>
  )
}
