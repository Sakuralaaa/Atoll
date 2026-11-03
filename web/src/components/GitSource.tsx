// Builds from source: form fields, webhook setup, the service source card and the build log viewer.
import { useEffect, useRef, useState } from 'react'
import { ArrowDown, Box, Eye, EyeOff, GitBranch, GitCommitHorizontal, Hammer, KeyRound, Loader2, Pencil, Plus, RefreshCw, Rocket, ScrollText, Trash2, Webhook, X } from 'lucide-react'
import { Badge, Brand, Button, Card } from './ui'
import { CodeBlock, ConfirmDialog, CopyButton, Label, Modal, Skeleton, TextInput } from './feedback'
import { api } from '../api/client'
import { invalidate, run, useBusy, useQuery } from '../api/hooks'
import { ago, dateTime, deployStatusText, latestDeploy, triggerText } from '../api/view'
import { buildActive, commitUrl, gitApi, gitBody, gitFormError, gitFormFrom, repoPath, repoUrl, shortSha, useBuildLogs, type GitForm } from '../api/git'
import type { Deployment, GitWebhook, Service } from '../api/types'
import { useT } from '../i18n'
import { cx, si } from './ui-utils'

/** GitHub mark (lucide has no brand icons); follows the text color so it works in both themes. */
export const GithubIcon = ({ size = 16 }: { size?: number }) => <Brand icon={si.siGithub} size={size} color="currentColor" />

// ---------- source toggle ----------
export type SourceKind = 'image' | 'git'
export function SourceToggle({ value, onChange }: { value: SourceKind; onChange: (v: SourceKind) => void }) {
  const { t } = useT()
  const items: [SourceKind, string, React.ReactNode][] = [['image', t('镜像', 'Image'), <Box key="i" size={14} />], ['git', t('GitHub 仓库', 'GitHub repo'), <GithubIcon key="g" size={14} />]]
  return (
    <div className="inline-flex rounded-lg field p-0.5 gap-1" role="tablist">
      {items.map(([k, l, i]) => (
        <button key={k} type="button" role="tab" aria-selected={value === k} onClick={() => onChange(k)}
          className={cx('h-8 px-3 rounded-md inline-flex items-center gap-2 text-[13px]', value === k ? 'nav-active font-semibold' : 'text-muted hover:text-fg')}>{i}{l}</button>
      ))}
    </div>
  )
}

// ---------- git fields ----------
export function GitFields({ f, set, hasToken, autoFocus }: { f: GitForm; set: (f: GitForm) => void; hasToken?: boolean; autoFocus?: boolean }) {
  const { t } = useT()
  const [showTok, setShowTok] = useState(false)
  const up = (k: 'repo' | 'branch' | 'context' | 'dockerfile' | 'token') => (e: React.ChangeEvent<HTMLInputElement>) => set({ ...f, [k]: e.target.value })
  const setArg = (i: number, a: { k: string; v: string }) => set({ ...f, args: f.args.map((x, j) => j === i ? a : x) })
  return (
    <>
      <Label t={t('仓库地址', 'Repository URL')} d={t('公开仓库直接填写；私有仓库在下方填写 Token。', 'Public repos work as is; for a private repo add a token below.')}>
        <TextInput autoFocus={autoFocus} mono className="w-full" placeholder="https://github.com/owner/repo" value={f.repo} onChange={up('repo')} />
      </Label>
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-x-4">
        <Label t={t('分支', 'Branch')}><TextInput mono className="w-full" value={f.branch} onChange={up('branch')} placeholder="main" /></Label>
        <Label t={t('构建目录', 'Context dir')}><TextInput mono className="w-full" value={f.context} onChange={up('context')} placeholder={t('仓库根目录', 'repo root')} /></Label>
        <Label t="Dockerfile"><TextInput mono className="w-full" value={f.dockerfile} onChange={up('dockerfile')} placeholder="Dockerfile" /></Label>
      </div>
      <div className="mb-4">
        <div className="flex flex-wrap items-center gap-x-2 mb-1.5"><span className="text-[13px] font-semibold">{t('构建参数', 'Build args')}</span><span className="text-[12px] text-dim">--build-arg KEY=VALUE</span>
          <button type="button" onClick={() => set({ ...f, args: [...f.args, { k: '', v: '' }] })} className="ml-auto text-accent text-[12.5px] flex items-center gap-1"><Plus size={13} />{t('添加', 'Add')}</button></div>
        {f.args.length ? <div className="space-y-2">{f.args.map((a, i) => (
          <div key={i} className="flex items-center gap-2">
            <TextInput mono className="w-[40%] min-w-0" placeholder="KEY" value={a.k} onChange={e => setArg(i, { ...a, k: e.target.value.trim() })} />
            <span className="text-muted font-mono">=</span>
            <TextInput mono className="flex-1 min-w-0" placeholder="value" value={a.v} onChange={e => setArg(i, { ...a, v: e.target.value })} />
            <button type="button" title={t('删除', 'Remove')} onClick={() => set({ ...f, args: f.args.filter((_, j) => j !== i) })} className="text-muted hover:text-[#ef4444] px-1"><Trash2 size={15} /></button>
          </div>
        ))}</div> : <div className="h-11 rounded-lg border-2 border-dashed border-line2 px-3.5 flex items-center text-[13px] text-dim">{t('没有构建参数', 'No build args')}</div>}
        <div className="text-[12px] text-dim mt-1.5">{t('构建参数不是机密：会留在镜像历史里。机密请用环境变量。', 'Build args are not secret: they stay in the image history. Use env vars for secrets.')}</div>
      </div>
      <Label t={t('GitHub Token（可选）', 'GitHub token (optional)')} d={t('只写不读：加密保存，之后不会再显示。需要仓库的 Contents 读取权限。', 'Write-only: stored encrypted and never shown again. Needs read access to the repo contents.')}>
        {hasToken && !f.token && <div className="mb-2 flex items-center gap-2 text-[13px]">
          {f.clearToken
            ? <><Badge tone="red"><X size={11} />{t('保存后清除', 'Cleared on save')}</Badge><button type="button" onClick={() => set({ ...f, clearToken: false })} className="text-accent text-[12.5px]">{t('撤销', 'Undo')}</button></>
            : <><Badge tone="green"><KeyRound size={11} />{t('Token 已保存', 'Token saved')}</Badge><button type="button" onClick={() => set({ ...f, clearToken: true })} className="text-[12.5px] text-muted hover:text-[#ef4444]">{t('清除', 'Clear')}</button></>}
        </div>}
        <div className="relative">
          <TextInput mono className="w-full pr-10" type={showTok ? 'text' : 'password'} autoComplete="new-password" value={f.token} onChange={up('token')}
            placeholder={hasToken ? t('留空保持不变；填写则替换', 'Leave empty to keep; type to replace') : 'github_pat_…'} />
          <button type="button" onClick={() => setShowTok(v => !v)} className="absolute right-3 top-1/2 -translate-y-1/2 text-muted hover:text-fg">{showTok ? <EyeOff size={15} /> : <Eye size={15} />}</button>
        </div>
      </Label>
    </>
  )
}

// ---------- webhook ----------
/** Webhook URL + secret with the GitHub setup steps. `secret` undefined = not shown (hidden / API token). */
export function WebhookSetup({ wh, secret }: { wh: GitWebhook; secret?: string }) {
  const { t } = useT()
  return (
    <>
      <div className="text-[13px] font-semibold mb-1.5">Payload URL</div>
      <CodeBlock>{wh.url}</CodeBlock>
      {secret && <><div className="text-[13px] font-semibold mb-1.5 mt-4">Secret</div><CodeBlock>{secret}</CodeBlock></>}
      <div className="mt-5 rounded-lg field px-4 py-3.5 text-[13px] leading-6 text-fg2">
        <div className="font-semibold text-fg mb-1">{t('在 GitHub 上设置', 'Set up on GitHub')}</div>
        <ol className="list-decimal pl-5 space-y-0.5">
          <li>{t('打开仓库 Settings → Webhooks → Add webhook', 'Open the repo: Settings → Webhooks → Add webhook')}</li>
          <li>{t('Payload URL 填上面的地址', 'Payload URL: the URL above')}</li>
          <li>Content type: <span className="font-mono">{wh.content_type || 'application/json'}</span></li>
          <li>{t('Secret 填上面的密钥', 'Secret: the secret above')}</li>
          <li>{t('事件选择 Just the push event，保存', 'Which events: Just the push event, then save')}</li>
        </ol>
        <div className="text-[12px] text-dim mt-2">{t(`只有推送到 ${wh.branch || 'main'} 分支会触发构建，其他分支和标签会被忽略。`, `Only pushes to ${wh.branch || 'main'} build; other branches and tags are ignored.`)}</div>
      </div>
    </>
  )
}

/** Shown once after a git source is created / first switched to git: the secret is not shown in full again without a panel session. */
export function WebhookOnceDialog({ wh, onClose }: { wh: GitWebhook; onClose: () => void }) {
  const { t } = useT()
  return (
    <Modal title={<><Webhook size={17} />{t('配置 GitHub Webhook', 'Set up the GitHub webhook')}</>} width={620} onClose={onClose}
      footer={<Button size="sm" variant="primary" onClick={onClose}>{t('我已保存', 'Done')}</Button>}>
      <div className="text-[13px] text-muted mb-4 leading-6">{t('推送代码后自动构建并部署。密钥只在这里完整显示一次，之后可在服务页查看或轮换。', 'Pushes then build and deploy automatically. The secret is shown here once; later you can reveal or rotate it on the service page.')}</div>
      <WebhookSetup wh={wh} secret={wh.secret} />
    </Modal>
  )
}

// ---------- service source card ----------
export function SourceCard({ s }: { s: Service }) {
  const { t } = useT()
  const g = s.git
  const deps = useQuery(`deployments:${s.id}`, () => api.deployments(s.id))
  const last = latestDeploy(deps.data)
  const [busy, wrap] = useBusy()
  const [edit, setEdit] = useState(false)
  const [toImage, setToImage] = useState(false)
  const [logs, setLogs] = useState<Deployment>()
  if (!g) return null
  const rebuild = () => wrap(() => run(() => api.action(s.id, 'redeploy'), { ok: t('已开始构建最新提交', 'Building the latest commit'), invalidate: [`deployments:${s.id}`, `project:${s.project_id}`] }))
  const building = last && buildActive(last.status) && last.status !== 'pulling' && last.status !== 'starting'
  return (
    <Card className="mt-6 p-4 @2xl:p-6">
      <div className="flex flex-wrap items-center gap-3">
        <GithubIcon size={18} />
        <span className="text-[15px] font-semibold tracking-tight">{t('源码构建', 'Build from source')}</span>
        {last?.status === 'building' ? <Badge tone="amber"><Loader2 size={11} className="animate-spin" />{t('构建中', 'Building')}</Badge> : last?.status === 'failed' ? <Badge tone="red">{t('上次失败', 'Last build failed')}</Badge> : null}
        <div className="ml-auto flex flex-wrap items-center gap-2">
          <Button size="sm" onClick={() => setEdit(true)}><Pencil size={13} />{t('编辑', 'Edit')}</Button>
          <Button size="sm" variant="primary" disabled={busy || !!building} onClick={rebuild}>{busy ? <Loader2 size={14} className="animate-spin" /> : <Hammer size={14} />}{t('重新构建最新提交', 'Rebuild latest')}</Button>
        </div>
      </div>
      <div className="mt-5 grid grid-cols-[84px_minmax(0,1fr)] @2xl:grid-cols-[100px_minmax(0,1fr)] gap-y-3.5 text-[13px]">
        <span className="text-muted">{t('仓库', 'Repository')}</span>
        <a href={repoUrl(g.repo)} target="_blank" rel="noreferrer" className="font-mono hover:underline truncate">{repoPath(g.repo)}</a>
        <span className="text-muted">{t('分支', 'Branch')}</span>
        <span className="flex flex-wrap items-center gap-x-2 gap-y-1 font-mono min-w-0 [overflow-wrap:anywhere]"><GitBranch size={14} className="text-accent shrink-0" />{g.branch || 'main'}
          <span className="text-dim font-sans text-[12.5px]">· {g.context ? `${g.context}/` : ''}{g.dockerfile || 'Dockerfile'}{g.has_token ? ` · ${t('私有仓库 Token 已保存', 'private repo token saved')}` : ''}</span></span>
        <span className="text-muted">{t('最近提交', 'Last commit')}</span>
        <span className="flex flex-wrap items-center gap-x-2 gap-y-1">{g.last_commit
          ? <a href={commitUrl(g.repo, g.last_commit)} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1.5 font-mono hover:underline"><GitCommitHorizontal size={14} className="text-accent" />{shortSha(g.last_commit)}</a>
          : <span className="text-dim">{t('还没有成功的构建', 'No successful build yet')}</span>}
          {last && <span className="text-dim text-[12.5px]">· {t('最近部署', 'latest deploy')} {ago(last.started_at)} · {deployStatusText[last.status] ?? last.status}</span>}
          {last && <button onClick={() => setLogs(last)} className="text-accent text-[12.5px] inline-flex items-center gap-1 ml-1"><ScrollText size={13} />{t('构建日志', 'Build log')}</button>}
        </span>
      </div>
      <WebhookSection s={s} />
      {edit && <EditGitModal s={s} onClose={() => setEdit(false)} />}
      {toImage && <SwitchToImageModal s={s} onClose={() => setToImage(false)} />}
      {logs && <BuildLogModal key={logs.id} s={s} d={logs} onClose={() => setLogs(undefined)} />}
      <div className="mt-5 pt-4 border-t border-line flex flex-wrap items-center gap-3 text-[12.5px] text-dim">
        <span>{t('构建在服务所在的服务器上进行，每台服务器同时只跑一个构建，超时 20 分钟。', "Builds run on the service's own server, one at a time per server, 20 min timeout.")}</span>
        <Button className="ml-auto" size="sm" variant="ghost" onClick={() => setToImage(true)}><Box size={13} />{t('改回镜像部署', 'Switch back to image')}</Button>
      </div>
    </Card>
  )
}

function WebhookSection({ s }: { s: Service }) {
  const { t } = useT()
  const q = useQuery(`webhook:${s.id}`, () => gitApi.webhook(s.id))
  const [show, setShow] = useState(false)
  const [rot, setRot] = useState(false)
  const [setup, setSetup] = useState(false)
  const wh = q.data
  const secret = wh?.secret
  return (
    <div className="mt-6">
      <div className="flex flex-wrap items-center gap-2 mb-2"><Webhook size={15} className="text-accent" /><span className="text-[13px] font-semibold">GitHub Webhook</span>
        <span className="text-[12.5px] text-dim">{t(`推送到 ${s.git?.branch || 'main'} 后自动构建`, `Pushes to ${s.git?.branch || 'main'} build automatically`)}</span>
        <button onClick={() => setSetup(true)} disabled={!wh} className="ml-auto text-accent text-[12.5px]">{t('设置步骤', 'Setup steps')}</button></div>
      {q.error && !wh ? <div className="text-[13px] text-[#ef4444]">{q.error.message}</div> : !wh ? <Skeleton className="h-24 w-full" /> : (
        <div className="space-y-2">
          <div className="h-11 rounded-lg field px-3.5 flex items-center gap-3 font-mono text-[13px] min-w-0"><span className="text-muted font-sans text-[12px] w-[52px] shrink-0 @max-md:hidden">URL</span><span className="truncate flex-1 min-w-0" title={wh.url}>{wh.url}</span><CopyButton text={wh.url} /></div>
          <div className="min-h-11 py-1.5 rounded-lg field px-3.5 flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[13px] min-w-0">
            <span className="text-muted font-sans text-[12px] w-[52px] shrink-0">Secret</span>
            <span className="truncate flex-1 min-w-[min(100%,120px)]">{!wh.has_secret ? <span className="text-dim font-sans">{t('未设置', 'not set')}</span> : show && secret ? secret : '••••••••••••••••••••••••'}</span>
            {secret ? <>
              <Button size="sm" variant="ghost" onClick={() => setShow(v => !v)}>{show ? <EyeOff size={14} /> : <Eye size={14} />}{show ? t('隐藏', 'Hide') : t('显示', 'Reveal')}</Button>
              <CopyButton text={secret} />
            </> : wh.has_secret && <span className="text-[12px] text-dim font-sans">{t('仅面板会话可查看', 'panel session only')}</span>}
            <Button size="sm" onClick={() => setRot(true)}><RefreshCw size={13} />{t('轮换', 'Rotate')}</Button>
          </div>
        </div>
      )}
      {rot && <ConfirmDialog title={t('轮换 Webhook 密钥', 'Rotate webhook secret')} action={t('确认轮换', 'Rotate')} danger={false} name={s.name} onClose={() => setRot(false)}
        description={t('旧密钥立即失效，在 GitHub 上更新为新密钥之前，推送不会触发构建。', 'The old secret stops working at once; pushes will not build until you update the secret on GitHub.')}
        onConfirm={async () => { const r = await run(() => gitApi.rotate(s.id), { ok: t('已生成新密钥，请更新到 GitHub', 'New secret issued, update it on GitHub') }); if (r) { q.set(r); setShow(true); invalidate(`webhook:${s.id}`) } }} />}
      {setup && wh && <Modal title={<><Webhook size={17} />{t('配置 GitHub Webhook', 'Set up the GitHub webhook')}</>} width={620} onClose={() => setSetup(false)}>
        <WebhookSetup wh={wh} secret={secret} />
        {!secret && <div className="mt-3 text-[12.5px] text-dim">{t('密钥只对面板会话显示；忘记了可以轮换。', 'The secret is only shown to a panel session; rotate it if lost.')}</div>}
      </Modal>}
    </div>
  )
}

export function EditGitModal({ s, onClose }: { s: Service; onClose: () => void }) {
  const { t } = useT()
  const isNew = !s.git
  const [f, setF] = useState<GitForm>(() => gitFormFrom(s.git))
  const [busy, wrap] = useBusy()
  const [wh, setWh] = useState<GitWebhook>()
  const err = gitFormError(f, t)
  const save = () => wrap(async () => {
    const r = await run(() => gitApi.patchSource(s.id, { git: gitBody(f, !isNew) }), { ok: t('已保存，正在构建', 'Saved, building'), invalidate: [`project:${s.project_id}`, 'projects', `deployments:${s.id}`, `webhook:${s.id}`, `image-update:${s.id}`] })
    if (!r) return
    if (r.webhook?.secret) setWh(r.webhook)
    else onClose()
  })
  if (wh) return <WebhookOnceDialog wh={wh} onClose={onClose} />
  return (
    <Modal title={<><GithubIcon size={17} />{isNew ? t('改为从 GitHub 构建', 'Build from GitHub') : t('编辑源码设置', 'Edit git settings')}</>} width={640} onClose={onClose}
      footer={<><span className="mr-auto self-center text-[12.5px] text-[#ef4444]">{f.repo && err}</span><Button size="sm" onClick={onClose}>{t('取消', 'Cancel')}</Button>
        <Button size="sm" variant="primary" disabled={!!err || busy} onClick={save}>{busy ? <Loader2 size={13} className="animate-spin" /> : <Rocket size={13} />}{t('保存并构建', 'Save and build')}</Button></>}>
      {isNew && <div className="mb-4 rounded-lg border-2 border-dashed border-[#f59e0b] px-3 py-2 text-[12.5px] text-fg2">{t('保存后不再拉取镜像，改为在服务器上用 Dockerfile 构建；之后可随时改回镜像。', 'After saving, the image is no longer pulled; the service is built from its Dockerfile on its server. You can switch back any time.')}</div>}
      <GitFields f={f} set={setF} hasToken={s.git?.has_token} autoFocus={isNew} />
    </Modal>
  )
}

function SwitchToImageModal({ s, onClose }: { s: Service; onClose: () => void }) {
  const { t } = useT()
  const [image, setImage] = useState('')
  const [busy, wrap] = useBusy()
  const ref = image.trim() && !image.includes(':') && !image.includes('@') ? `${image.trim()}:latest` : image.trim()
  const ok = !!ref && !ref.startsWith('atoll-build/')
  const go = () => wrap(async () => {
    const r = await run(() => gitApi.patchSource(s.id, { git: null, image: ref }), { ok: t('已改回镜像部署', 'Switched back to image'), invalidate: [`project:${s.project_id}`, 'projects', `deployments:${s.id}`, `image-update:${s.id}`, 'image-updates'] })
    if (r) onClose()
  })
  return (
    <Modal title={<><Box size={17} />{t('改回镜像部署', 'Switch back to image')}</>} onClose={onClose}
      footer={<><Button size="sm" onClick={onClose}>{t('取消', 'Cancel')}</Button><Button size="sm" variant="primary" disabled={!ok || busy} onClick={go}>{busy && <Loader2 size={13} className="animate-spin" />}{t('切换并部署', 'Switch and deploy')}</Button></>}>
      <div className="text-[13.5px] text-fg2 leading-6 mb-4">{t('停止从 GitHub 构建，改为拉取下面的镜像部署。仓库设置、Token 和 Webhook 会被删除，GitHub 上的 Webhook 请手动移除。', 'Stops building from GitHub and deploys the image below instead. The repo settings, token and webhook are removed; delete the webhook on GitHub yourself.')}</div>
      <Label t={t('镜像', 'Image')} d={t('例如 ghcr.io/me/app:v1；不写标签时使用 latest', 'e.g. ghcr.io/me/app:v1; latest when no tag is given')}>
        <TextInput autoFocus mono className="w-full" placeholder="ghcr.io/owner/image:tag" value={image} onChange={e => setImage(e.target.value)} onKeyDown={e => e.key === 'Enter' && ok && go()} />
      </Label>
    </Modal>
  )
}

// ---------- deployments: commit + build log ----------
export function CommitLink({ s, d }: { s: Service; d: Deployment }) {
  if (!d.commit) return null
  return <a href={commitUrl(s.git?.repo, d.commit)} target="_blank" rel="noreferrer" onClick={e => e.stopPropagation()} className="inline-flex items-center gap-1 font-mono text-[12px] text-fg2 hover:underline"><GitCommitHorizontal size={13} className="text-accent" />{shortSha(d.commit)}</a>
}

export function BuildLogModal({ s, d, onClose }: { s: Service; d: Deployment; onClose: () => void }) {
  const { t } = useT()
  const { lines, status, truncated, error, loaded } = useBuildLogs(s.id, d.id)
  const st = status ?? d.status
  const live = buildActive(st)
  const [follow, setFollow] = useState(true)
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => { if (follow && box.current) box.current.scrollTop = box.current.scrollHeight }, [lines, follow])
  const tone = st === 'failed' ? 'red' : st === 'running' ? 'green' : live ? 'amber' : 'zinc'
  return (
    <Modal width={920} onClose={onClose} title={<><ScrollText size={17} />{t('构建日志', 'Build log')} · {s.name}<Badge tone={tone}>{live && <Loader2 size={11} className="animate-spin" />}{deployStatusText[st] ?? st}</Badge></>}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[12.5px] text-muted mb-3">
        <span className="font-mono">{d.id}</span><span>· {triggerText[d.trigger] ?? d.trigger}</span><span>· {dateTime(d.started_at)}</span>
        {d.commit && <span>· <CommitLink s={s} d={d} /></span>}
        {truncated && <span className="ml-auto text-[#f59e0b]">{t('日志过长，只保留了最新的部分', 'Log truncated: only the newest lines are kept')}</span>}
      </div>
      <div className="relative">
        <div ref={box} onScroll={e => { const el = e.currentTarget; setFollow(el.scrollHeight - el.scrollTop - el.clientHeight < 40) }}
          className="surface bg-sunk h-[52vh] overflow-auto p-4 font-mono text-[12.5px] leading-5">
          {!loaded && <div className="space-y-2"><Skeleton className="h-3 w-2/3" /><Skeleton className="h-3 w-1/2" /><Skeleton className="h-3 w-3/5" /></div>}
          {loaded && !lines.length && <div className="text-dim font-sans text-[13px]">{error ? error.message : live ? t('等待构建输出…', 'Waiting for build output…') : t('这次部署没有构建日志（只保留最近 10 次部署的日志）', 'No build log for this deployment (logs are kept for the 10 newest deployments)')}</div>}
          {lines.map(l => <div key={l.seq} className={cx('whitespace-pre-wrap break-all', /\b(error|failed|fatal)\b/i.test(l.line) ? 'text-[#ef4444]' : 'text-fg2')}>{l.line}</div>)}
          {live && loaded && <div className="mt-2 flex items-center gap-2 text-muted font-sans text-[12px]"><span className="h-2 w-2 rounded-full bg-emerald-500 animate-pulse" />{t('实时输出中', 'Live')}</div>}
          {!live && st === 'failed' && d.message && <div className="mt-3 text-[#ef4444] whitespace-pre-wrap break-all">{d.message}</div>}
        </div>
        {!follow && <button onClick={() => { setFollow(true); box.current?.scrollTo({ top: box.current.scrollHeight }) }} className="absolute right-4 bottom-4 h-9 w-9 rounded-full tile bg-card2 grid place-items-center"><ArrowDown size={16} /></button>}
      </div>
      {error && lines.length > 0 && <div className="mt-2 text-[12.5px] text-[#ef4444]">{error.message}</div>}
    </Modal>
  )
}

