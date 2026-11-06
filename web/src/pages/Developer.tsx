import { useState } from 'react'
import { Plus, Terminal, KeyRound, Trash2, Bot, AlertTriangle, Pencil, ShieldAlert, FolderLock } from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Badge, Button, Card, Hint } from '../components/ui'
import { CodeBlock, ConfirmDialog, CopyButton, Empty, ErrorState, RowsSkeleton } from '../components/feedback'
import { api } from '../api/client'
import { run, useQuery } from '../api/hooks'
import { ago } from '../api/view'
import { ALL_PERMISSIONS, type APIToken, type Permission } from '../api/types'
import { permInfo, permTone, permsOf } from '../api/tokenPerms'
import { TokenDialog } from '../components/TokenDialog'
import { L, useT } from '../i18n'
import { useProjects } from '../api/queries'

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
        <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em] flex items-center gap-2">{t('连接', 'Connect')}<Hint>{t('Atoll 本身不内置 AI。用 API 令牌把 CLI 或 MCP 交给 Claude Code、Codex 这类外部 agent，让它们替你部署和排错。', 'Atoll has no built-in AI. Give the CLI or MCP to an external agent such as Claude Code or Codex with an API token and let it deploy and debug for you.')}</Hint></h1>

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
                  <div className="min-w-0"><div className="font-semibold truncate" title={tk.name}>{tk.name}</div><div className="text-[12px] text-dim mt-0.5 flex items-center gap-1.5 min-w-0">{tk.owner_role === 'member' && <Badge tone="violet">{t(`子账号 ${tk.owner}`, `sub-account ${tk.owner}`)}</Badge>}<span className="truncate">{t('创建于', 'Created')} {ago(tk.created_at)}</span></div></div>
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
          <Card className="p-5 flex flex-col"><div className="text-[13px] font-semibold mb-3">{t('安装并登录', 'Install and log in')}</div><CodeBlock grow>{L("# 从 GitHub Releases 下载 atoll 并放进 PATH\natoll login --url {0} \\\n  --token {1}", panel, tok)}</CodeBlock></Card>
          <Card className="p-5 flex flex-col"><div className="text-[13px] font-semibold mb-3">{t('常用命令', 'Common commands')}</div><CodeBlock grow>{`atoll deploy ghcr.io/me/api:v1.9 -p saas-api
atoll env set -s api DATABASE_URL=...
atoll logs api -f
atoll domain add api.example.com -s api`}</CodeBlock></Card>
        </div>

        <div className="mt-10 text-[15px] font-semibold flex items-center gap-2 mb-3"><Bot size={18} />{t('接入 AI agent（MCP）', 'Connect an AI agent (MCP)')}<Hint>{t('agent 能做什么取决于令牌的权限：没有的权限直接拒绝（403）；开启「需要面板审批」的令牌，删除、迁移、改 DNS 这类操作会进入「审批」，由你在面板里批准。', "What an agent can do follows its token's permissions: anything missing is refused (403). With \"require panel approval\" on, deletes, moves and DNS changes wait in Confirmations for you to approve.")}</Hint></div>
        <div className="grid grid-cols-1 @4xl:grid-cols-2 gap-5 [&>*]:min-w-0">
          <Card className="p-5 flex flex-col"><div className="flex flex-wrap items-center gap-2 text-[13px] font-semibold mb-3 min-h-6">Claude Code<Badge tone="zinc">{t('一行添加', 'one line')}</Badge></div><CodeBlock grow>{`claude mcp add atoll -- atoll mcp \\
  --url ${panel} --token ${tok}`}</CodeBlock></Card>
          <Card className="p-5 flex flex-col"><div className="flex flex-wrap items-center gap-2 text-[13px] font-semibold mb-3 min-h-6">Codex<Badge tone="zinc">~/.codex/config.toml</Badge></div><CodeBlock grow>{`[mcp_servers.atoll]
command = "atoll"
args = ["mcp", "--url", "${panel}",
        "--token", "${tok}"]`}</CodeBlock></Card>
        </div>
        <Card className="p-5 mt-5"><div className="text-[13px] font-semibold mb-3">REST API</div><CodeBlock grow>{`curl ${panel}/api/v1/projects \\
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

