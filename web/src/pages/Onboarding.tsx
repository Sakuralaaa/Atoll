import { useEffect, useMemo, useState } from 'react'
import { Navigate, useNavigate } from 'react-router-dom'
import { Archive, Check, CheckCircle2, Clock, Globe2, LayoutTemplate, Link2, Loader2, Package, Rocket, Server as ServerIcon, X } from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Badge, Button, Card } from '../components/ui'
import { CodeBlock, CopyButton, Skeleton, TextInput } from '../components/feedback'
import RootDomainsSettings from '../components/RootDomainsSettings'
import BackupStorageSettings from '../components/BackupStorageSettings'
import { CreateProject } from './Projects'
import { api } from '../api/client'
import { controlApi } from '../api/control'
import { invalidate, run, useBusy, useQuery } from '../api/hooks'
import type { JoinToken, Settings } from '../api/types'
import { useT } from '../i18n'
import { cx } from '../components/ui-utils'
import { ONBOARDING_STEPS, readOnboarding, writeOnboarding, type OnboardingState } from '../onboarding'

/** The settings PUT replaces these fields every time, so a partial save sends the current values along. */
function settingsBody(s: Settings, patch: Partial<Settings>): Partial<Settings> {
  return { cloudflare_zone_id: s.cloudflare_zone_id, tls_mode: s.tls_mode || 'cloudflare-origin', public_url: s.public_url, acme_email: s.acme_email ?? '', base_domain: s.base_domain, ...patch }
}

/** First-run wizard: shown after the first admin is created; every step can be skipped. */
export default function Onboarding() {
  const { t } = useT()
  const nav = useNavigate()
  const me = useQuery('me', api.me).data
  const user = me?.username
  // progress lives in localStorage (onboarding.ts); rev re-reads it after each save
  const [rev, setRev] = useState(0)
  const st: Omit<OnboardingState, 'updated_at'> | undefined = useMemo(() => user ? readOnboarding(user) ?? { status: 'active', step: 0, completed: [] } : undefined, [user, rev]) // eslint-disable-line react-hooks/exhaustive-deps
  const settings = useQuery(me && me.role !== 'member' ? 'settings' : null, api.settings)

  if (me && me.role === 'member') return <Navigate replace to="/" />
  const step = st?.step ?? 0
  const save = (n: Omit<OnboardingState, 'updated_at'>) => { if (user) { writeOnboarding(user, n); setRev(r => r + 1) } }
  const go = (i: number) => save({ status: 'active', step: i, completed: st?.completed ?? [] })
  const next = (markDone: boolean) => {
    const completed = markDone ? [...new Set([...(st?.completed ?? []), step])] : st?.completed ?? []
    if (step >= ONBOARDING_STEPS - 1) { save({ status: 'done', step, completed }); nav('/', { replace: true }); return }
    save({ status: 'active', step: step + 1, completed })
  }
  const skipAll = () => { save({ status: 'skipped', step, completed: st?.completed ?? [] }); nav('/', { replace: true }) }
  const finish = () => { save({ status: 'done', step, completed: [...new Set([...(st?.completed ?? []), step])] }) }

  const steps = [
    { icon: <Link2 size={15} />, title: t('中控名称与地址', 'Name and address') },
    { icon: <ServerIcon size={15} />, title: t('添加第一台节点', 'Add the first server') },
    { icon: <Globe2 size={15} />, title: t('Cloudflare 根域名', 'Cloudflare root domain') },
    { icon: <Archive size={15} />, title: t('S3 备份', 'S3 backups') },
    { icon: <Rocket size={15} />, title: t('部署第一个服务', 'Deploy the first service') },
  ]
  const optional = [false, false, true, true, false]
  const s = settings.data
  return (
    <MainShell>
      <div className="max-w-[1080px] mx-auto px-4 pt-6 pb-12 @2xl:px-8 @2xl:pt-10" data-testid="onboarding">
        <div className="flex flex-wrap items-start justify-between gap-3 mb-6">
          <div className="min-w-0">
            <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em]">{t('欢迎使用 Atoll', 'Welcome to Atoll')}</h1>
            <p className="text-[13.5px] text-muted mt-1 leading-6">{t('几步完成基础配置。每一步都可以跳过，之后在「设置」里随时可以重新打开这个向导。', 'A few steps to set things up. Every step can be skipped, and you can reopen this guide from Settings at any time.')}</p>
          </div>
          <Button size="sm" variant="ghost" onClick={skipAll}><X size={14} />{t('跳过引导', 'Skip the guide')}</Button>
        </div>
        <div className="grid grid-cols-1 @4xl:grid-cols-[240px_minmax(0,1fr)] gap-6 items-start">
          <ol className="flex @4xl:flex-col gap-1.5 overflow-x-auto @4xl:overflow-visible pb-1" aria-label={t('步骤', 'Steps')}>
            {steps.map((x, i) => {
              const done = st?.completed.includes(i)
              return (
                <li key={i} className="shrink-0">
                  <button type="button" onClick={() => go(i)} aria-current={i === step ? 'step' : undefined}
                    className={cx('w-full text-left flex items-center gap-2.5 rounded-lg px-3 py-2.5 text-[13px] font-semibold border-2 transition-colors',
                      i === step ? 'border-[var(--edge)] bg-hi text-hi-ink' : 'border-transparent text-fg2 hover:text-fg')}>
                    <span className={cx('h-6 w-6 shrink-0 rounded-full grid place-items-center text-[11.5px] font-mono border-2', done ? 'bg-ok border-ok-strong text-white' : 'border-current')}>{done ? <Check size={12} strokeWidth={3} /> : i + 1}</span>
                    <span className="whitespace-nowrap @4xl:whitespace-normal">{x.title}</span>
                    {optional[i] && <span className="ml-auto text-[11px] font-normal opacity-70 hidden @4xl:inline">{t('可选', 'optional')}</span>}
                  </button>
                </li>
              )
            })}
          </ol>
          <div className="min-w-0">
            <div className="text-[12.5px] text-muted mb-1">{t('第 {0} 步，共 {1} 步', 'Step {0} of {1}').replace('{0}', String(step + 1)).replace('{1}', String(ONBOARDING_STEPS))}</div>
            <h2 className="text-[19px] font-bold flex items-center gap-2 mb-4">{steps[step].icon}{steps[step].title}{optional[step] && <Badge tone="zinc">{t('可选', 'Optional')}</Badge>}</h2>
            {!s && <Card className="p-6 space-y-4"><Skeleton className="h-11 w-full" /><Skeleton className="h-11 w-full" /></Card>}
            {s && step === 0 && <StepControl s={s} onNext={() => next(true)} onSkip={() => next(false)} />}
            {s && step === 1 && <StepServer onNext={ok => next(ok)} />}
            {s && step === 2 && <StepCloudflare s={s} onNext={() => next(!!s.has_cloudflare_token)} onSkip={() => next(false)} />}
            {s && step === 3 && <StepBackup s={s} onNext={() => next(!!s.backup_configured)} onSkip={() => next(false)} />}
            {s && step === 4 && <StepDeploy onCreated={finish} onFinish={() => next(false)} />}
          </div>
        </div>
      </div>
    </MainShell>
  )
}

function Footer({ children, onSkip }: { children?: React.ReactNode; onSkip?: () => void }) {
  const { t } = useT()
  return (
    <div className="mt-5 flex flex-wrap items-center justify-end gap-3">
      {onSkip && <Button size="sm" variant="ghost" onClick={onSkip}>{t('跳过这一步', 'Skip this step')}</Button>}
      {children}
    </div>
  )
}

function StepControl({ s, onNext, onSkip }: { s: Settings; onNext: () => void; onSkip: () => void }) {
  const { t } = useT()
  const ctl = useQuery('control', controlApi.status).data
  const [name, setName] = useState<string>()
  const [url, setUrl] = useState(s.public_url || location.origin + (location.pathname === '/' ? '' : location.pathname.replace(/\/$/, '')))
  const [busy, wrap] = useBusy()
  const nameV = name ?? ctl?.name ?? ''
  const cleanUrl = url.trim().replace(/\/$/, '')
  const urlOk = cleanUrl === '' || /^https?:\/\/[^\s/]+/.test(cleanUrl)
  const save = () => wrap(async () => {
    if (!urlOk) return
    if (ctl && nameV.trim() !== (ctl.name ?? '')) {
      const r = await run(() => controlApi.rename(nameV.trim()), { invalidate: ['control'] })
      if (!r) return
    }
    if (cleanUrl !== (s.public_url ?? '')) {
      const r = await run(() => api.putSettings(settingsBody(s, { public_url: cleanUrl })), { ok: t('已保存', 'Saved'), invalidate: ['settings'] })
      if (!r) return
    }
    onNext()
  })
  return (
    <>
      <Card className="divide-y divide-line">
        <Row t={t('中控名称', 'Control server name')} d={t('显示在「中控」页和侧栏，留空则用主机名。', 'Shown on the Control page; empty uses the hostname.')}>
          <TextInput className="w-full" value={nameV} onChange={e => setName(e.target.value)} placeholder={ctl?.host.hostname || 'atoll-control'} maxLength={64} />
        </Row>
        <Row t={t('公开地址', 'Public URL')} d={t('浏览器、CLI 和其他服务器上的 Agent 用这个地址连回中控。建议用 HTTPS 域名。', 'Browsers, the CLI and agents on other servers reach the control server here. An HTTPS domain is recommended.')}>
          <TextInput mono className="w-full" value={url} onChange={e => setUrl(e.target.value)} placeholder="https://panel.example.com" />
          {!urlOk && <div className="text-[12px] text-danger mt-1.5">{t('需要以 http:// 或 https:// 开头', 'Must start with http:// or https://')}</div>}
          {urlOk && cleanUrl.startsWith('http://') && <div className="text-[12px] text-muted mt-1.5">{t('明文 HTTP 只适合私网或测试；公网请用 HTTPS。', 'Plain HTTP is only for private networks or testing; use HTTPS on the internet.')}</div>}
        </Row>
      </Card>
      <Footer onSkip={onSkip}>
        <Button size="sm" variant="primary" disabled={busy || !urlOk} onClick={save}>{busy ? <Loader2 size={14} className="animate-spin" /> : <Check size={14} />}{t('保存并继续', 'Save and continue')}</Button>
      </Footer>
    </>
  )
}

function StepServer({ onNext }: { onNext: (connected: boolean) => void }) {
  const { t } = useT()
  const [tok, setTok] = useState<JoinToken>(); const [err, setErr] = useState<string>()
  useEffect(() => { api.joinToken().then(setTok, e => setErr(e.message)) }, [])
  const q = useQuery('servers', api.servers, { interval: 3000 })
  const online = (q.data ?? []).filter(x => x.role !== 'control' && x.status === 'online')
  const ok = online.length > 0
  return (
    <>
      <Card className="p-5 @2xl:p-6">
        <div className="text-[13.5px] text-fg2 leading-6">{t('在一台 Debian 12+ / Ubuntu 22.04+ 的 VPS（amd64 或 arm64）上以 root 运行下面的命令。它会安装 Docker 和 Atoll Agent，Agent 主动连回中控，不需要开放额外端口。', 'Run this as root on a Debian 12+ / Ubuntu 22.04+ VPS (amd64 or arm64). It installs Docker and the Atoll agent; the agent connects back to the control server, no extra ports to open.')}</div>
        <div className="mt-4">
          {err && <div className="text-[13px] text-danger">{err}</div>}
          {!tok && !err && <div className="h-[76px] rounded-lg field grid place-items-center text-muted"><Loader2 className="animate-spin" size={18} /></div>}
          {tok && <>
            <div className="[&>div]:whitespace-pre-wrap [&>div]:break-all"><CodeBlock>{tok.command}</CodeBlock></div>
            <div className="mt-3 flex items-center gap-3 text-[12.5px] text-muted"><Clock size={13} />{t('一次性令牌，{0} 前有效', 'One-time token, valid until {0}').replace('{0}', new Date(tok.expires_at).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' }))}<span className="ml-auto"><CopyButton text={tok.command} label={t('复制命令', 'Copy')} /></span></div>
          </>}
        </div>
        <div className={cx('mt-5 rounded-lg px-4 py-3 text-[13px] flex items-center gap-2.5', ok ? 'bg-ok/12 text-fg' : 'border-2 border-dashed border-line2 text-muted')} data-testid="onboarding-server-status">
          {ok ? <><CheckCircle2 size={16} className="text-ok shrink-0" />{t('已连接：{0}', 'Connected: {0}').replace('{0}', online.map(x => x.name).join(', '))}</>
            : <><Loader2 size={15} className="animate-spin shrink-0" />{t('等待 Agent 连上中控…', 'Waiting for the agent to connect…')}</>}
        </div>
        <div className="mt-3 text-[12.5px] text-muted leading-5">{t('中控和节点在同一台机器上时，用 Docker 部署的中控请不要启用 Caddy（https profile）：节点的入口需要独占 80/443 端口。', 'If this machine is also the control server running in Docker, leave its Caddy (https profile) off: the node’s ingress needs ports 80/443 for itself.')}</div>
      </Card>
      <Footer onSkip={ok ? undefined : () => onNext(false)}>
        <Button size="sm" variant="primary" disabled={!ok} onClick={() => onNext(true)}><Check size={14} />{t('继续', 'Continue')}</Button>
      </Footer>
    </>
  )
}

function StepCloudflare({ s, onNext, onSkip }: { s: Settings; onNext: () => void; onSkip: () => void }) {
  const { t } = useT()
  const [token, setToken] = useState('')
  const [busy, wrap] = useBusy()
  const saveToken = () => wrap(async () => {
    const r = await run(() => api.putSettings(settingsBody(s, { cloudflare_token: token.trim() })), { ok: t('Cloudflare 令牌已保存', 'Cloudflare token saved'), invalidate: ['settings'] })
    if (r) setToken('')
  })
  return (
    <>
      <div className="text-[13px] text-muted leading-6 mb-4">{t('配置后，服务绑定根域名下的域名时，Atoll 会自动创建和删除 DNS 记录。令牌需要 Zone.DNS 编辑权限；要自动签 Origin 证书再加 SSL and Certificates 编辑权限。没有 Cloudflare 也可以跳过，绑定域名时手动添加 A 记录。', 'With this set, Atoll creates and deletes DNS records when services bind domains under a root domain. The token needs Zone.DNS edit; add SSL and Certificates edit for Origin certificates. Without Cloudflare, skip this and add A records yourself.')}</div>
      <Card className="divide-y divide-line mb-6">
        <Row t={t('API 令牌', 'API token')} right={s.has_cloudflare_token ? <Badge tone="green"><Check size={11} strokeWidth={3} />{t('已保存', 'Saved')}</Badge> : <Badge tone="zinc">{t('未设置', 'Not set')}</Badge>}>
          <div className="flex flex-wrap gap-2">
            <TextInput mono type="password" autoComplete="off" className="flex-1 min-w-[min(100%,240px)]" value={token} onChange={e => setToken(e.target.value)} placeholder={s.has_cloudflare_token ? t('已设置，留空保持不变', 'Set; leave empty to keep it') : t('粘贴 Cloudflare API 令牌', 'Paste a Cloudflare API token')} />
            <Button size="sm" variant="primary" disabled={!token.trim() || busy} onClick={saveToken}>{busy ? <Loader2 size={13} className="animate-spin" /> : <Check size={13} />}{t('保存令牌', 'Save token')}</Button>
          </div>
        </Row>
      </Card>
      <RootDomainsSettings />
      <Footer onSkip={onSkip}>
        <Button size="sm" variant="primary" onClick={onNext}><Check size={14} />{t('继续', 'Continue')}</Button>
      </Footer>
    </>
  )
}

function StepBackup({ s, onNext, onSkip }: { s: Settings; onNext: () => void; onSkip: () => void }) {
  const { t } = useT()
  return (
    <>
      <div className="text-[13px] text-muted leading-6 -mb-4">{t('填好 S3 兼容存储后，各服务器用 restic 加密备份直接上传，中控的每日备份也会多存一份到同一个存储桶。', 'With S3-compatible storage set, each server uploads restic-encrypted backups directly, and the daily control backup is copied to the same bucket.')}</div>
      <BackupStorageSettings s={s} />
      <Footer onSkip={onSkip}>
        <Button size="sm" variant="primary" onClick={onNext}><Check size={14} />{t('继续', 'Continue')}</Button>
      </Footer>
    </>
  )
}

function StepDeploy({ onCreated, onFinish }: { onCreated: () => void; onFinish: () => void }) {
  const { t } = useT()
  const [mode, setMode] = useState<'empty' | 'app'>()
  const servers = (useQuery('servers', api.servers).data ?? []).filter(x => x.role !== 'control')
  return (
    <>
      <div className="grid grid-cols-1 @2xl:grid-cols-2 gap-4">
        <button type="button" onClick={() => setMode('app')} className="text-left rounded-2xl surface surface-hover p-5">
          <LayoutTemplate size={20} className="text-accent" />
          <div className="mt-3 text-[15px] font-semibold">{t('从应用模板部署', 'Deploy from an app template')}</div>
          <div className="mt-1 text-[13px] text-muted leading-6">{t('选一个常用应用（数据库、工具等），一键创建项目并部署。', 'Pick a common app (databases, tools, …) and create and deploy a project in one go.')}</div>
        </button>
        <button type="button" onClick={() => setMode('empty')} className="text-left rounded-2xl surface surface-hover p-5">
          <Package size={20} className="text-accent" />
          <div className="mt-3 text-[15px] font-semibold">{t('创建空项目', 'Create an empty project')}</div>
          <div className="mt-1 text-[13px] text-muted leading-6">{t('先建项目，再添加 Docker 镜像、Git 仓库或数据库服务。', 'Create the project first, then add a Docker image, a Git repository or a database.')}</div>
        </button>
      </div>
      {!servers.length && <div className="mt-4 text-[12.5px] text-muted">{t('部署需要至少一台已连接的服务器，可以先回到第 2 步添加。', 'Deploying needs at least one connected server; go back to step 2 to add one.')}</div>}
      <Footer>
        <Button size="sm" variant="primary" onClick={onFinish}><Check size={14} />{t('完成引导', 'Finish')}</Button>
      </Footer>
      {mode && <CreateProject initialMode={mode} onCreated={() => { onCreated(); invalidate('projects') }} onClose={() => setMode(undefined)} />}
    </>
  )
}

function Row({ t, d, right, children }: { t: string; d?: React.ReactNode; right?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="px-4 @2xl:px-6 py-5 grid grid-cols-1 @3xl:grid-cols-[220px_minmax(0,1fr)] gap-3 @3xl:gap-6 items-start">
      <div><div className="text-[13px] font-semibold flex items-center gap-2 flex-wrap">{t}{right}</div>{d && <div className="text-[12.5px] text-muted mt-1 leading-5">{d}</div>}</div>
      <div className="min-w-0">{children}</div>
    </div>
  )
}
