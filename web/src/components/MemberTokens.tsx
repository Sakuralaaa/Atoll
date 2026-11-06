import { useState } from 'react'
import { AlertTriangle, FolderLock, KeyRound, Pencil, Plus, Trash2 } from 'lucide-react'
import { Badge, Button, Card, Hint } from './ui'
import { CodeBlock, ConfirmDialog, CopyButton } from './feedback'
import { api } from '../api/client'
import { run, useQuery } from '../api/hooks'
import { ago } from '../api/view'
import { useProjects } from '../api/queries'
import type { APIToken, Permission } from '../api/types'
import { useT } from '../i18n'
import { TokenDialog } from './TokenDialog'
import { permInfo, permTone, permsOf } from '../api/tokenPerms'

/** Settings → 账号与安全 → API 令牌, for a sub-account: its own tokens for the CLI, MCP or
 *  scripts. GET /tokens answers a member with its own tokens only; each one is limited to the
 *  member's permissions and servers at every request (sub-accounts B2). */
export default function MemberTokens() {
  const { t } = useT()
  const info = permInfo(t)
  const q = useQuery('tokens', api.tokens)
  const projects = useProjects().data ?? []
  const [create, setCreate] = useState(false)
  const [edit, setEdit] = useState<APIToken>()
  const [fresh, setFresh] = useState<APIToken>()
  const [revoke, setRevoke] = useState<APIToken>()
  return (
    <div className="mt-8">
      <div className="flex flex-wrap items-center justify-between gap-3 mb-3">
        <div className="text-[15px] font-semibold flex items-center gap-2"><KeyRound size={17} />{t('API 令牌', 'API tokens')}<Hint>{t('给 CLI、MCP（Claude Code、Codex 等 agent）或脚本用。令牌能做的不超过你的账号：只有你的服务器和项目、你的权限；管理员收回权限或停用账号时立即生效。', 'For the CLI, MCP (agents such as Claude Code or Codex) or scripts. A token can do no more than your account: only your servers and projects, only your permissions; when the admin narrows or disables your account it applies at once.')}</Hint></div>
        <Button size="sm" variant="primary" onClick={() => setCreate(true)}><Plus size={14} />{t('创建令牌', 'Create token')}</Button>
      </div>
      {fresh?.token && <Card className="p-5 mb-4 !bg-[#86efac] text-[#141414]">
        <div className="flex items-center gap-2 text-[13.5px] font-bold"><AlertTriangle size={16} />{t(`令牌「${fresh.name}」已创建，只显示这一次`, `Token "${fresh.name}" created — shown only once`)}</div>
        <div className="mt-3 flex flex-wrap items-center gap-3">
          <div className="flex-1 basis-full @2xl:basis-0 min-w-0 h-11 rounded-[4px] border-2 border-[#141414] bg-[#fffdf8] px-3.5 flex items-center font-mono text-[13px] truncate">{fresh.token}</div>
          <CopyButton text={fresh.token} label={t('复制令牌', 'Copy token')} />
          <Button size="sm" onClick={() => setFresh(undefined)}>{t('我已保存', "I've saved it")}</Button>
        </div>
        <div className="mt-3 [&>div]:text-[12.5px]"><CodeBlock>{`atoll login --url ${location.origin} --token ${fresh.token}`}</CodeBlock></div>
      </Card>}
      <Card className="divide-y divide-line">
        {!q.data && <div className="px-6 py-5 text-[13px] text-dim">{q.error ? t(`加载失败：${q.error.message}`, `Failed to load: ${q.error.message}`) : t('加载中…', 'Loading…')}</div>}
        {q.data && !q.data.length && <div className="px-6 py-5 text-[13px] text-muted">{t('还没有令牌。', 'No tokens yet.')}</div>}
        {q.data?.map(tk => {
          const names = (tk.projects ?? []).map(id => projects.find(p => p.id === id)?.name ?? id)
          return (
            <div key={tk.id} className="px-4 @2xl:px-6 py-3.5 flex flex-wrap items-center gap-x-4 gap-y-2 text-[13px]">
              <div className="min-w-0 flex-1 basis-[200px]"><div className="font-semibold truncate" title={tk.name}>{tk.name}</div><div className="text-[12px] text-dim mt-0.5 font-mono">{tk.prefix}•••• · {tk.last_used_at ? t(`最近使用 ${ago(tk.last_used_at)}`, `used ${ago(tk.last_used_at)}`) : t('从未使用', 'never used')}</div></div>
              <div className="flex flex-wrap items-center gap-1.5">{permsOf(tk).map(p => <span key={p} title={info[p as Permission]?.[1]}><Badge tone={permTone(p)}>{info[p as Permission]?.[0] ?? p}</Badge></span>)}</div>
              <div className="text-[12.5px] min-w-0 max-w-[260px] truncate" title={names.join(', ')}>{names.length ? <span className="flex items-center gap-1.5"><FolderLock size={13} className="shrink-0 text-muted" />{names.join(', ')}</span> : <span className="text-muted">{t('我的所有项目', 'All my projects')}</span>}</div>
              <div className="flex items-center ml-auto">
                <button title={t('编辑', 'Edit')} onClick={() => setEdit(tk)} className="h-8 w-8 grid place-items-center text-muted hover:text-fg"><Pencil size={15} /></button>
                <button title={t('吊销', 'Revoke')} onClick={() => setRevoke(tk)} className="h-8 w-8 grid place-items-center text-muted hover:text-[#ef4444]"><Trash2 size={15} /></button>
              </div>
            </div>
          )
        })}
      </Card>
      {create && <TokenDialog onClose={() => setCreate(false)} onSaved={tk => { setFresh(tk); setCreate(false) }} />}
      {edit && <TokenDialog token={edit} onClose={() => setEdit(undefined)} onSaved={() => setEdit(undefined)} />}
      {revoke && <ConfirmDialog title={t('吊销令牌', 'Revoke token')} action={t('确认吊销', 'Revoke')} name={revoke.name} onClose={() => setRevoke(undefined)}
        description={<>{t('使用', 'CLIs and agents using')} <span className="font-mono">{revoke.prefix}…</span> {t('的 CLI 和 agent 会立即失去访问权限。', 'lose access immediately.')}</>}
        onConfirm={() => run(() => api.deleteToken(revoke.id), { ok: t('令牌已吊销', 'Token revoked'), invalidate: ['tokens'] }).then(() => { if (fresh?.id === revoke.id) setFresh(undefined) })} />}
    </div>
  )
}
