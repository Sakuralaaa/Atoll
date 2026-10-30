import { useState } from 'react'
import { Plus, Terminal, KeyRound, Trash2, Bot, Loader2, AlertTriangle } from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Badge, Button, Card } from '../components/ui'
import { CodeBlock, ConfirmDialog, CopyButton, Empty, ErrorState, Label, Modal, RowsSkeleton, Select, TextInput } from '../components/feedback'
import { api } from '../api/client'
import { run, useBusy, useQuery } from '../api/hooks'
import { ago } from '../api/view'
import type { APIToken } from '../api/types'

const SCOPE: Record<string, [string, string, 'red' | 'violet' | 'zinc']> = {
  admin: ['管理员', '全部操作；删除、迁移、改 DNS 仍需在面板确认', 'red'],
  deploy: ['部署', '创建、部署、改变量、看日志；不能做破坏性操作', 'violet'],
  read: ['只读', '只能查看（GET）', 'zinc'],
}

export default function Developer() {
  const q = useQuery('tokens', api.tokens)
  const panel = useQuery('settings', api.settings).data?.public_url || location.origin
  const [create, setCreate] = useState(false)
  const [fresh, setFresh] = useState<APIToken>()
  const [revoke, setRevoke] = useState<APIToken>()
  const tok = fresh?.token ?? 'atl_pat_xxx'
  return (
    <MainShell>
      <div className="max-w-[1180px] mx-auto px-16 pt-14 pb-16">
        <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em]">CLI 与 API</h1>
        <p className="text-[13px] text-muted mt-2 max-w-[760px] leading-6">Atoll 本身不内置 AI。用 API 令牌把 CLI 或 MCP 交给 Claude Code、Codex 这类外部 agent，让它们替你部署和排错。</p>

        <div className="mt-8 flex items-center justify-between mb-3"><div className="text-[15px] font-semibold flex items-center gap-2"><KeyRound size={18} />API 令牌</div><Button variant="primary" onClick={() => setCreate(true)}><Plus size={15} />创建令牌</Button></div>
        {fresh?.token && (
          <Card className="p-5 mb-5 !bg-[#86efac] text-[#141414]">
            <div className="flex items-center gap-2 text-[13.5px] font-bold"><AlertTriangle size={16} />令牌「{fresh.name}」已创建，只显示这一次</div>
            <div className="mt-3 flex items-center gap-3">
              <div className="flex-1 min-w-0 h-11 rounded-[4px] border-2 border-[#141414] bg-[#fffdf8] px-3.5 flex items-center font-mono text-[13px] truncate">{fresh.token}</div>
              <CopyButton text={fresh.token} label="复制令牌" />
              <Button size="sm" onClick={() => setFresh(undefined)}>我已保存</Button>
            </div>
            <div className="text-[12.5px] mt-2">下方的命令已自动填入这个令牌。</div>
          </Card>
        )}
        {q.error && !q.data && <ErrorState error={q.error} onRetry={q.refetch} />}
        {!q.data && !q.error && <RowsSkeleton n={3} />}
        {q.data && !q.data.length && <Empty icon={<KeyRound size={22} />} title="还没有 API 令牌" desc="创建一个令牌给 CLI 或 AI agent 使用。令牌只在创建时显示一次。" />}
        {q.data && q.data.length > 0 && (
          <Card className="divide-y divide-line">
            <div className="px-6 h-10 grid grid-cols-[1.3fr_1.1fr_1.6fr_1fr_40px] gap-4 items-center text-[12.5px] text-muted"><span>名称</span><span>令牌</span><span>权限</span><span>最近使用</span><span /></div>
            {q.data.map(t => {
              const [l, d, tone] = SCOPE[t.scope] ?? [t.scope, '', 'zinc']
              return (
                <div key={t.id} className="px-6 min-h-16 py-3 grid grid-cols-[1.3fr_1.1fr_1.6fr_1fr_40px] gap-4 items-center text-[13px]">
                  <div className="min-w-0"><div className="font-semibold truncate">{t.name}</div><div className="text-[12px] text-dim mt-0.5">创建于 {ago(t.created_at)}</div></div>
                  <span className="font-mono text-[12.5px] text-muted truncate">{t.prefix}••••••••</span>
                  <div className="min-w-0 flex items-center gap-2"><Badge tone={tone}>{l}</Badge><span className="text-[12px] text-muted truncate" title={d}>{d}</span></div>
                  <span className="text-muted">{t.last_used_at ? ago(t.last_used_at) : '从未使用'}</span>
                  <button title="吊销" onClick={() => setRevoke(t)} className="h-8 w-8 grid place-items-center text-muted hover:text-[#ef4444]"><Trash2 size={15} /></button>
                </div>
              )
            })}
          </Card>
        )}

        <div className="mt-10 text-[15px] font-semibold flex items-center gap-2 mb-3"><Terminal size={18} />CLI</div>
        <div className="grid grid-cols-2 gap-5">
          <Card className="p-5"><div className="text-[13px] font-semibold mb-3">安装并登录</div><CodeBlock>{`# 从 GitHub Releases 下载 atoll 并放进 PATH
atoll login --url ${panel} \\
  --token ${tok}`}</CodeBlock></Card>
          <Card className="p-5"><div className="text-[13px] font-semibold mb-3">常用命令</div><CodeBlock>{`atoll deploy ghcr.io/me/api:v1.9 -p saas-api
atoll env set -s api DATABASE_URL=...
atoll logs api -f
atoll domain add api.example.com -s api`}</CodeBlock></Card>
        </div>

        <div className="mt-10 text-[15px] font-semibold flex items-center gap-2 mb-1"><Bot size={18} />接入 AI agent（MCP）</div>
        <div className="text-[13px] text-muted mb-4 leading-6">agent 能查看项目、部署镜像、改变量、看日志。删除服务、迁移、改 DNS 这类操作会进入「待确认」，需要你在面板里批准。</div>
        <div className="grid grid-cols-2 gap-5">
          <Card className="p-5"><div className="flex items-center gap-2 text-[13px] font-semibold mb-3">Claude Code<Badge tone="zinc">一行添加</Badge></div><CodeBlock>{`claude mcp add atoll -- atoll mcp \\
  --url ${panel} --token ${tok}`}</CodeBlock></Card>
          <Card className="p-5"><div className="flex items-center gap-2 text-[13px] font-semibold mb-3">Codex<Badge tone="zinc">~/.codex/config.toml</Badge></div><CodeBlock>{`[mcp_servers.atoll]
command = "atoll"
args = ["mcp", "--url", "${panel}",
        "--token", "${tok}"]`}</CodeBlock></Card>
        </div>
        <Card className="p-5 mt-5"><div className="text-[13px] font-semibold mb-3">REST API</div><CodeBlock>{`curl ${panel}/api/v1/projects \\
  -H "Authorization: Bearer ${tok}"`}</CodeBlock></Card>
      </div>
      {create && <CreateToken onClose={() => setCreate(false)} onCreated={t => { setFresh(t); setCreate(false) }} />}
      {revoke && <ConfirmDialog title="吊销令牌" action="确认吊销" name={revoke.name} onClose={() => setRevoke(undefined)}
        description={<>使用 <span className="font-mono">{revoke.prefix}…</span> 的 CLI 和 agent 会立即失去访问权限。</>}
        onConfirm={() => run(() => api.deleteToken(revoke.id), { ok: '令牌已吊销', invalidate: ['tokens'] }).then(() => { if (fresh?.id === revoke.id) setFresh(undefined) })} />}
    </MainShell>
  )
}

function CreateToken({ onClose, onCreated }: { onClose: () => void; onCreated: (t: APIToken) => void }) {
  const [name, setName] = useState(''); const [scope, setScope] = useState('deploy')
  const [busy, wrap] = useBusy()
  const submit = () => wrap(async () => {
    if (!name.trim()) return
    const t = await run(() => api.createToken({ name: name.trim(), scope }), { invalidate: ['tokens'] })
    if (t) onCreated(t)
  })
  return (
    <Modal title="创建 API 令牌" onClose={onClose} footer={<><Button size="sm" onClick={onClose}>取消</Button><Button size="sm" variant="primary" disabled={!name.trim() || busy} onClick={submit}>{busy && <Loader2 size={13} className="animate-spin" />}创建</Button></>}>
      <Label t="名称" d="写清楚给谁用，方便以后吊销"><TextInput autoFocus className="w-full" value={name} onChange={e => setName(e.target.value)} onKeyDown={e => e.key === 'Enter' && submit()} placeholder="claude-code-macbook" /></Label>
      <Label t="权限" d={SCOPE[scope][1]}>
        <Select className="w-full" value={scope} onChange={e => setScope(e.target.value)}>{Object.entries(SCOPE).map(([k, [l]]) => <option key={k} value={k}>{l}（{k}）</option>)}</Select>
      </Label>
    </Modal>
  )
}
