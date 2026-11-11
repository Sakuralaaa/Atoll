import { L } from '../i18n'
import { useState } from 'react'
import { AlertTriangle, Archive, Check, CheckCircle2, Download, Eye, KeyRound, Loader2, PlugZap, Save, Trash2, XCircle } from 'lucide-react'
import { Badge, Button, Card, Hint } from './ui'
import { ConfirmDialog, CopyButton, TextInput } from './feedback'
import { api } from '../api/client'
import { run, useBusy } from '../api/hooks'
import { backupsApi } from '../api/backups'
import type { BackupTestResult, Settings } from '../api/types'
import { cx } from './ui-utils'
import { KEEP_NETWORK } from './net'

type Secret = { password: string; repository: string; first: boolean }

/** Settings → 备份存储: S3-compatible bucket for restic, connection test, repository password. */
export default function BackupStorageSettings({ s }: { s: Settings }) {
  // Lives outside the keyed form so the once-only password survives the settings refetch after save.
  const [secret, setSecret] = useState<Secret | undefined>()
  const key = [s.backup_endpoint, s.backup_bucket, s.backup_prefix, s.backup_region, s.has_backup_credentials, s.has_backup_password].join('|')
  return (
    <div id="backup" className="mt-8 mb-8 scroll-mt-6">
      <div className="text-[15px] font-semibold flex items-center gap-2">
        <Archive size={17} />{L("备份存储")}
        <Hint>{L("任何 S3 兼容存储：Cloudflare R2、Backblaze B2、AWS S3、MinIO。每台服务器的 Agent 用 restic 加密后直接上传，数据不经过中控。密钥只写不读，留空表示保持原值。")}</Hint>
        {s.backup_configured ? <Badge tone="green"><Check size={11} strokeWidth={3} />{L("已配置")}</Badge> : <Badge tone="zinc">{L("未配置")}</Badge>}
      </div>
      <div className="mb-3" />
      {secret && <PasswordCard secret={secret} onClose={() => setSecret(undefined)} />}
      <Form key={key} s={s} onGenerated={pw => setSecret(pw)} onReveal={setSecret} />
    </div>
  )
}

function Row({ t, d, right, children }: { t: string; d?: React.ReactNode; right?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="px-4 @2xl:px-6 py-5 grid grid-cols-1 @3xl:grid-cols-[260px_minmax(0,1fr)] gap-3 @3xl:gap-6 items-start">
      <div><div className="text-[13px] font-semibold flex items-center gap-2 flex-wrap">{t}{right}</div>{d && <div className="text-[12.5px] text-muted mt-1 leading-5">{d}</div>}</div>
      <div className="min-w-0">{children}</div>
    </div>
  )
}
const Saved = ({ on }: { on?: boolean }) => on ? <Badge tone="green"><Check size={11} strokeWidth={3} />{L("已保存")}</Badge> : <Badge tone="zinc">{L("未设置")}</Badge>
const repoUrl = (s: Pick<Settings, 'backup_endpoint' | 'backup_bucket' | 'backup_prefix'>) =>
  s.backup_endpoint && s.backup_bucket ? `s3:${/^https?:\/\//.test(s.backup_endpoint) ? s.backup_endpoint : `https://${s.backup_endpoint}`}/${s.backup_bucket}/${s.backup_prefix || 'atoll'}`.replace(/\/+$/, '') : ''

function Form({ s, onGenerated, onReveal }: { s: Settings; onGenerated: (x: Secret) => void; onReveal: (x: Secret) => void }) {
  const init = { backup_endpoint: s.backup_endpoint ?? '', backup_bucket: s.backup_bucket ?? '', backup_prefix: s.backup_prefix ?? '', backup_region: s.backup_region ?? '' }
  const [v, setV] = useState(init)
  const [ak, setAk] = useState(''); const [sk, setSk] = useState(''); const [repoPw, setRepoPw] = useState('')
  const [test, setTest] = useState<BackupTestResult | { ok: false; message: string } | undefined>()
  const [busy, wrap] = useBusy()
  const [testing, wrapTest] = useBusy()
  const [clear, setClear] = useState(false)
  const set = (k: keyof typeof v) => (e: React.ChangeEvent<HTMLInputElement>) => { setV(x => ({ ...x, [k]: e.target.value })); setTest(undefined) }
  const dirty = !!(ak || sk || repoPw) || (Object.keys(v) as (keyof typeof v)[]).some(k => v[k].trim() !== init[k])
  const pairBad = !s.has_backup_credentials && (!!ak.trim() !== !!sk.trim())
  const bucketBad = v.backup_bucket.trim() !== '' && !/^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$/.test(v.backup_bucket.trim())
  const endpointBad = v.backup_endpoint.trim() !== '' && !/^(https?:\/\/)?[A-Za-z0-9.-]+(:[0-9]{1,5})?\/?$/.test(v.backup_endpoint.trim())
  const pwBad = repoPw !== '' && repoPw.length < 12
  const complete = (v.backup_endpoint.trim() || s.backup_endpoint) && (v.backup_bucket.trim() || s.backup_bucket) && (s.has_backup_credentials || (ak.trim() && sk.trim()))
  const body = (): Partial<Settings> => {
    const b: Partial<Settings> = {}
    for (const k of Object.keys(v) as (keyof typeof v)[]) if (v[k].trim()) b[k] = v[k].trim()
    if (ak.trim()) b.backup_access_key = ak.trim()
    if (sk.trim()) b.backup_secret_key = sk.trim()
    if (repoPw) b.backup_repo_password = repoPw
    return b
  }
  // PUT /settings replaces every field: send the current values back (write-only secrets "" = keep).
  const save = () => wrap(async () => {
    const r = await run(() => api.putSettings({ ...s, backup_access_key: '', backup_secret_key: '', backup_repo_password: '', ...KEEP_NETWORK, ...body() }), { ok: L("备份存储已保存"), invalidate: ['settings', 'backup-policy'] })
    if (r?.backup_repo_password) onGenerated({ password: r.backup_repo_password, repository: repoUrl(r), first: true })
    if (r) { setAk(''); setSk(''); setRepoPw('') }
  })
  const runTest = () => wrapTest(async () => {
    setTest(undefined)
    try { setTest(await backupsApi.test(body())) } catch (e) { setTest({ ok: false, message: (e as Error).message }) }
  })
  const reveal = () => wrap(async () => { const r = await run(() => backupsApi.revealPassword()); if (r) onReveal({ ...r, first: false }) })

  return (
    <>
      <Card className="divide-y divide-line">
        <Row t="S3 Endpoint" d={<>{L("R2：")}<span className="font-mono">{L("https://&lt;账户 ID&gt;.r2.cloudflarestorage.com")}</span><br />{L("B2：")}<span className="font-mono">https://s3.us-west-004.backblazeb2.com</span></>}>
          <TextInput mono className="w-full" value={v.backup_endpoint} onChange={set('backup_endpoint')} placeholder="https://0123456789abcdef.r2.cloudflarestorage.com" />
          {endpointBad && <div className="text-[12px] text-danger mt-1.5">{L("只填主机或 http(s) 地址，不带路径")}</div>}
        </Row>
        <Row t={L("存储桶")} d={L("小写字母、数字、点和连字符")}>
          <TextInput mono className="w-full" value={v.backup_bucket} onChange={set('backup_bucket')} placeholder="atoll-backups" />
          {bucketBad && <div className="text-[12px] text-danger mt-1.5">{L("存储桶名称不合法")}</div>}
        </Row>
        <Row t={L("路径前缀")} d={L("仓库在存储桶里的目录，默认 atoll")}>
          <TextInput mono className="w-full" value={v.backup_prefix} onChange={set('backup_prefix')} placeholder="atoll" />
        </Row>
        <Row t={L("区域")} d={L("R2 填 auto；B2 例如 us-west-004；可留空")}>
          <TextInput mono className="w-full" value={v.backup_region} onChange={set('backup_region')} placeholder="auto" />
        </Row>
        <Row t="Access Key" right={<Saved on={s.has_backup_credentials} />}>
          <TextInput mono type="password" autoComplete="off" className="w-full" value={ak} onChange={e => { setAk(e.target.value); setTest(undefined) }} placeholder={s.has_backup_credentials ? L("已设置，留空保持不变") : 'Access Key ID'} />
        </Row>
        <Row t="Secret Key" right={<Saved on={s.has_backup_credentials} />} d={L("需要对存储桶有读写和删除权限")}>
          <TextInput mono type="password" autoComplete="off" className="w-full" value={sk} onChange={e => { setSk(e.target.value); setTest(undefined) }} placeholder={s.has_backup_credentials ? L("已设置，留空保持不变") : 'Secret Access Key'} />
          {pairBad && <div className="text-[12px] text-danger mt-1.5">{L("Access Key 和 Secret Key 需要一起填写")}</div>}
        </Row>
        <Row t={L("仓库密码")} right={<Saved on={s.has_backup_password} />}
          d={s.has_backup_password ? L("restic 用它加密所有快照。丢了它，备份就无法恢复。") : L("首次保存时自动生成并只显示一次。在新中控上接管已有仓库时，在这里填原来的密码。")}>
          {s.has_backup_password
            ? <Button size="sm" disabled={busy} onClick={reveal}><Eye size={14} />{L("显示密码")}</Button>
            : <>
              <TextInput mono type="password" autoComplete="new-password" className="w-full" value={repoPw} onChange={e => setRepoPw(e.target.value)} placeholder={L("留空自动生成（推荐）")} />
              {pwBad && <div className="text-[12px] text-danger mt-1.5">{L("至少 12 个字符")}</div>}
            </>}
        </Row>
      </Card>
      {test && (
        <Card className={cx('mt-4 px-5 py-3.5 flex items-start gap-3 text-[13px]', test.ok ? '!bg-t-green text-ink' : '!bg-t-red text-ink')}>
          {test.ok ? <CheckCircle2 size={16} className="mt-0.5 shrink-0" /> : <XCircle size={16} className="mt-0.5 shrink-0" />}
          <div className="min-w-0"><div className="font-bold">{test.ok ? L("连接成功") : L("连接失败")}{'server_id' in test && test.server_id ? <span className="font-normal"> {L("· 由")} {test.server_id} {L("检查")}</span> : null}</div>
            {test.message && <div className="font-mono text-[12px] mt-0.5 break-all">{test.message}</div>}</div>
        </Card>
      )}
      <div className="mt-4 flex flex-wrap items-center gap-3">
        {(s.backup_bucket || s.has_backup_credentials) && <Button variant="danger-outline" size="sm" disabled={busy} onClick={() => setClear(true)}><Trash2 size={14} />{L("清除存储配置")}</Button>}
        <span className="flex-1" />
        {dirty && <span className="text-[12.5px] text-muted">{L("有未保存的修改")}</span>}
        <Button size="sm" disabled={testing || !complete || pairBad || bucketBad || endpointBad} onClick={runTest} title={!complete ? L("先填写 Endpoint、存储桶和密钥") : undefined}>{testing ? <Loader2 size={14} className="animate-spin" /> : <PlugZap size={14} />}{L("测试连接")}</Button>
        <Button variant="primary" size="sm" disabled={!dirty || busy || pairBad || bucketBad || endpointBad || pwBad} onClick={save}>{busy ? <Loader2 size={14} className="animate-spin" /> : <Save size={14} />}{L("保存")}</Button>
      </div>
      {!s.has_backup_password && <div className="text-[12px] text-dim mt-2 text-right">{L("还没有仓库密码时，测试连接需要先保存一次（会生成密码）。")}</div>}
      {clear && <ConfirmDialog title={L("清除备份存储配置")} name={s.backup_bucket || 'clear'} action={L("清除")} onClose={() => setClear(false)}
        onConfirm={() => run(() => backupsApi.clear(), { ok: L("已清除备份存储配置"), invalidate: ['settings', 'backup-policy'] })}
        description={<>{L("所有服务的定时备份会停止。存储桶里的快照")}<b className="text-fg">{L("不会")}</b>{L("被删除，仓库密码也会保留，重新填写存储后仍可恢复。")}</>} />}
    </>
  )
}

function PasswordCard({ secret, onClose }: { secret: Secret; onClose: () => void }) {
  const download = () => {
    const text = L("Atoll 备份仓库密码（restic）\n\n仓库: {0}\n密码: {1}\n\n没有这个密码，备份无法恢复。请保存到密码管理器，不要和存储桶密钥放在一起。\n恢复示例: RESTIC_PASSWORD=<密码> restic -r {2} snapshots\n", secret.repository, secret.password, secret.repository)
    const url = URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' }))
    const a = document.createElement('a'); a.href = url; a.download = 'atoll-backup-password.txt'; a.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  return (
    <Card className="mb-4 p-5 !bg-hi text-hi-ink">
      <div className="flex items-center gap-2 text-[14px] font-extrabold italic"><KeyRound size={16} />{secret.first ? L("已生成仓库密码：只显示这一次") : L("仓库密码")}</div>
      <div className="mt-1.5 text-[13px] leading-5 flex gap-2"><AlertTriangle size={15} className="shrink-0 mt-0.5" />
        <span>{L("restic 用它加密全部快照。中控数据库丢失时，只有凭这个密码才能从存储桶恢复数据。请立即复制或下载，保存到密码管理器，不要和存储桶密钥放在一起。")}</span></div>
      <div className="mt-3 rounded-lg field px-3.5 py-3 font-mono text-[13px] text-fg break-all select-all">{secret.password}</div>
      {secret.repository && <div className="mt-1.5 font-mono text-[12px] break-all opacity-80">{secret.repository}</div>}
      <div className="mt-3 flex items-center gap-2">
        <CopyButton text={secret.password} label={L("复制密码")} />
        <Button size="sm" onClick={download}><Download size={14} />{L("下载 .txt")}</Button>
        <Button size="sm" className="ml-auto" onClick={onClose}>{secret.first ? L("我已妥善保存") : L("隐藏")}</Button>
      </div>
    </Card>
  )
}
