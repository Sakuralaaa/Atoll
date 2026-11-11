import { useRef, useState } from 'react'
import { ScrollRows } from '../components/ScrollRows'
import { IP } from '../components/IP'
import { NavLink } from 'react-router-dom'
import { Loader2, ShieldCheck, ShieldOff, KeyRound, RefreshCw, Download, Copy, Check, Monitor, Smartphone, LogOut, AlertTriangle, Terminal, LockKeyhole, UserRound, ImageUp, Trash2 } from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Avatar, Badge, Button, Card } from '../components/ui'
import { ErrorState, Modal, Skeleton, TextInput } from '../components/feedback'
import QRCode from '../components/QRCode'
import { api, ApiError, isDemo } from '../api/client'
import { invalidate, run, useBusy, useQuery } from '../api/hooks'
import type { SessionInfo, TwoFactorEnrollment, TwoFactorStatus } from '../api/types'
import { L, useT } from '../i18n'
import { cx } from '../components/ui-utils'
import { toast } from '../components/toast'
import NotificationSettings from '../components/NotificationSettings'
import MemberTokens from '../components/MemberTokens'
import { MyQuotaCard } from '../components/MemberQuota'
import AppearanceSettings from '../components/AppearanceSettings'

/** Tabs shared by the Settings pages. */
export function SettingsTabs() {
  const { t } = useT()
  const me = useQuery('me', api.me)
  const admin = me.data?.role !== 'member'
  const tab = ({ isActive }: { isActive: boolean }) => cx('h-9 px-4 grid place-items-center rounded-[4px] text-[13px] font-bold font-mono border-2', isActive ? 'border-[var(--edge)] bg-hi text-hi-ink' : 'border-transparent text-muted hover:text-fg')
  return (
    <div className="flex flex-wrap gap-2 mt-5 mb-8">
      {admin && <NavLink end to="/settings" className={tab}>{t('通用', 'General')}</NavLink>}
      <NavLink to="/settings/security" className={tab}>{t('账号与安全', 'Account & security')}</NavLink>
      {admin && <NavLink to="/settings/users" className={tab}>{t('子账号', 'Sub-accounts')}</NavLink>}
    </div>
  )
}

const fmt = (iso?: string) => iso ? new Date(iso).toLocaleString() : ''

export default function Security() {
  const { t } = useT()
  const me = useQuery('me', api.me)
  const q = useQuery('auth:2fa', api.twoFactor)
  return (
    <MainShell>
      <div className="max-w-[1240px] mx-auto px-4 pt-6 pb-12 @2xl:px-8 @2xl:pt-10 @5xl:px-14 @5xl:pt-12 @5xl:pb-16">
        <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em]">{t('设置', 'Settings')}</h1>
        <SettingsTabs />
        <AvatarCard />
        <AppearanceSettings />
        <UsernameCard twoFactor={q.data?.enabled} />
        <ChangePasswordCard twoFactor={q.data?.enabled} />
        {q.error && !q.data && <ErrorState error={q.error} onRetry={q.refetch} />}
        {!q.data && !q.error && <Card className="p-6 space-y-4"><Skeleton className="h-7 w-48" /><Skeleton className="h-11 w-full" /></Card>}
        {q.data && <TwoFactorCard st={q.data} />}
        <Sessions />
        {me.data?.role === 'member' && <MyQuotaCard quota={me.data.quota} usage={me.data.usage} />}
        {me.data?.role === 'member' && <MemberTokens />}
        {me.data?.role === 'member' && <NotificationSettings mine />}
        <Card className="p-5 mt-8 flex gap-3 items-start [overflow-wrap:anywhere]">
          <Terminal size={17} className="text-muted mt-0.5 shrink-0" />
          <div className="text-[12.5px] text-muted leading-6">
            {t('手机和恢复码都丢了：在中控服务器上运行', 'Lost your phone and the recovery codes? On the control host run')}{' '}
            <code className="font-mono text-fg bg-sunk px-1.5 py-0.5 rounded">sudo atoll-server admin reset-2fa &lt;{t('用户名', 'user')}&gt;</code>
            {t('，它会关闭两步验证并退出所有会话，然后用密码登录再重新启用。', ' — it turns 2FA off and signs the user out everywhere; then sign in with the password and enable it again.')}
          </div>
        </Card>
      </div>
    </MainShell>
  )
}

function Section({ icon, title, right, children }: { icon: React.ReactNode; title: string; right?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="mb-8">
      <div className="text-[15px] font-semibold flex flex-wrap items-center gap-2 mb-3"><span className="shrink-0">{icon}</span>{title}<span className="ml-auto">{right}</span></div>
      <Card>{children}</Card>
    </div>
  )
}

/** Center-crop an image file to a 128x128 square and encode it small (WebP, PNG fallback). */
async function squareAvatar(file: File): Promise<string> {
  const url = URL.createObjectURL(file)
  try {
    const img = await new Promise<HTMLImageElement>((ok, bad) => { const i = new Image(); i.onload = () => ok(i); i.onerror = () => bad(new Error(L("无法读取这张图片"))); i.src = url })
    const side = Math.min(img.naturalWidth, img.naturalHeight)
    const c = document.createElement('canvas'); c.width = c.height = 128
    const g = c.getContext('2d')!
    g.imageSmoothingQuality = 'high'
    g.drawImage(img, (img.naturalWidth - side) / 2, (img.naturalHeight - side) / 2, side, side, 0, 0, 128, 128)
    const webp = c.toDataURL('image/webp', 0.86)
    return webp.startsWith('data:image/webp') ? webp : c.toDataURL('image/png')
  } finally { URL.revokeObjectURL(url) }
}

function AvatarCard() {
  const { t } = useT()
  const q = useQuery('me', api.me)
  const me = q.data
  const file = useRef<HTMLInputElement>(null)
  const [busy, wrap] = useBusy()
  const save = (v: string) => wrap(async () => {
    try { await api.setAvatar(v); if (me) q.set({ ...me, avatar: v || undefined }); toast(v ? t('头像已更新', 'Avatar updated') : t('已恢复默认头像', 'Avatar removed'), 'ok') }
    catch (x) { toast(errText(t, x), 'error') }
  })
  const pick = async (f?: File) => {
    if (!f) return
    if (!/^image\//.test(f.type)) { toast(t('请选择图片文件', 'Choose an image file'), 'error'); return }
    try { save(await squareAvatar(f)) } catch (x) { toast((x as Error).message, 'error') }
  }
  return (
    <Section icon={<ImageUp size={17} />} title={t('头像', 'Avatar')}>
      <div className="px-4 @2xl:px-6 py-5 flex flex-wrap items-center gap-4">
        {me?.avatar ? <Avatar size={64} src={me.avatar} className="rounded-xl" />
          : <span className="tile h-16 w-16 shrink-0 rounded-xl bg-hi grid place-items-center text-[26px] font-extrabold font-mono text-hi-ink">{(me?.username ?? 'A')[0].toUpperCase()}</span>}
        <div className="min-w-0 flex-1">
          <div className="text-[13px] text-muted leading-5">{t('支持 PNG、JPG、WebP，会自动裁成正方形并压缩，只显示在面板里。', 'PNG, JPG or WebP; cropped to a square and shrunk. Shown in the panel only.')}</div>
          <div className="mt-3 flex flex-wrap gap-2">
            <Button size="sm" variant="primary" disabled={busy} onClick={() => file.current?.click()}>{busy ? <Loader2 size={14} className="animate-spin" /> : <ImageUp size={14} />}{me?.avatar ? t('更换头像', 'Change') : t('上传头像', 'Upload')}</Button>
            {me?.avatar && <Button size="sm" disabled={busy} onClick={() => save('')}><Trash2 size={14} />{t('恢复默认', 'Remove')}</Button>}
          </div>
        </div>
        <input ref={file} type="file" accept="image/png,image/jpeg,image/webp" className="hidden" onChange={e => { pick(e.target.files?.[0]); e.target.value = '' }} />
      </div>
    </Section>
  )
}

function UsernameCard({ twoFactor }: { twoFactor?: boolean }) {
  const { t } = useT()
  const me = useQuery('me', api.me).data
  const [name, setName] = useState<string>()
  const [cur, setCur] = useState(''); const [code, setCode] = useState('')
  const [err, setErr] = useState<string>()
  const [busy, wrap] = useBusy()
  const value = name ?? me?.username ?? ''
  const code6 = code.replace(/\s/g, '')
  const valid = /^[A-Za-z0-9_.@-]{1,64}$/.test(value.trim())
  const changed = !!me && value.trim() !== me.username
  const ok = changed && valid && !!cur && (!twoFactor || /^\d{6}$/.test(code6))
  const go = () => wrap(async () => {
    setErr(undefined)
    try {
      await api.changeUsername({ username: value.trim(), current_password: cur, ...(twoFactor ? { code: code6 } : {}) })
      setName(undefined); setCur(''); setCode('')
      invalidate('me')
      toast(t('用户名已修改，下次登录请用新用户名', 'Username changed; sign in with it next time'), 'ok')
    } catch (x) {
      const e = x as ApiError
      if (e.code === 'invalid_credentials') setErr(t('当前密码不正确', 'Current password is wrong'))
      else if (e.code === 'code_required') setErr(t('已启用两步验证，请输入验证码', '2FA is on — enter the code'))
      else if (e.status === 409) setErr(t('这个用户名已被占用', 'That username is taken'))
      else setErr(errText(t, x))
      setCode('')
    }
  })
  return (
    <Section icon={<UserRound size={17} />} title={t('用户名', 'Username')}>
      <form className="px-4 @2xl:px-6 py-5" onSubmit={e => { e.preventDefault(); if (ok && !busy) go() }}>
        <div className="grid grid-cols-1 @2xl:grid-cols-2 gap-x-5 gap-y-4">
          <label>
            <div className="text-[13px] font-semibold mb-1.5">{t('登录用户名', 'Login name')}</div>
            <TextInput autoComplete="username" className="w-full" value={value} maxLength={64} onChange={e => setName(e.target.value)} />
          </label>
          {changed && <label>
            <div className="text-[13px] font-semibold mb-1.5">{t('当前密码', 'Current password')}</div>
            <TextInput type="password" autoComplete="current-password" className="w-full" value={cur} onChange={e => setCur(e.target.value)} />
          </label>}
          {changed && twoFactor && <label>
            <div className="text-[13px] font-semibold mb-1.5">{t('两步验证码', '2FA code')}</div>
            <TextInput mono inputMode="numeric" autoComplete="one-time-code" maxLength={7} className="w-full tracking-[0.25em]" value={code} onChange={e => setCode(e.target.value.replace(/[^\d ]/g, ''))} placeholder="000000" />
          </label>}
        </div>
        {changed && !valid && <div className="mt-3 text-[12.5px] text-muted font-semibold">{t('1–64 个字符：字母、数字、_ . @ -', '1–64 characters: letters, digits, _ . @ -')}</div>}
        {err && <div role="alert" className="mt-3 text-[12.5px] text-danger font-semibold flex items-center gap-1.5"><AlertTriangle size={14} />{err}</div>}
        {changed && <Button type="submit" variant="primary" className="mt-5" disabled={!ok || busy}>{busy ? <Loader2 size={15} className="animate-spin" /> : <Check size={15} />}{t('保存用户名', 'Save username')}</Button>}
      </form>
    </Section>
  )
}

function ChangePasswordCard({ twoFactor }: { twoFactor?: boolean }) {
  const { t } = useT()
  const [cur, setCur] = useState(''); const [pw, setPw] = useState(''); const [pw2, setPw2] = useState(''); const [code, setCode] = useState('')
  const [err, setErr] = useState<string>()
  const [busy, wrap] = useBusy()
  const code6 = code.replace(/\s/g, '')
  const tooShort = pw.length > 0 && pw.length < 8
  const tooLong = pw.length > 72
  const mismatch = pw2.length > 0 && pw !== pw2
  const same = pw.length > 0 && pw === cur
  const ok = !!cur && pw.length >= 8 && !tooLong && pw === pw2 && !same && (!twoFactor || /^\d{6}$/.test(code6))
  const go = () => wrap(async () => {
    setErr(undefined)
    try {
      await api.changePassword({ current_password: cur, new_password: pw, ...(twoFactor ? { code: code6 } : {}) })
      setCur(''); setPw(''); setPw2(''); setCode('')
      invalidate('auth:sessions', 'me')
      toast(t('密码已修改，其他设备已退出', 'Password changed; other devices were signed out'), 'ok')
    } catch (x) {
      const e = x as ApiError
      if (e.code === 'invalid_credentials') setErr(t('当前密码不正确', 'Current password is wrong'))
      else if (e.code === 'code_required') setErr(t('已启用两步验证，请输入验证码', '2FA is on — enter the code'))
      else setErr(errText(t, x))
      setCode('')
    }
  })
  const hint = tooShort ? t('新密码至少 8 个字符', 'At least 8 characters') : tooLong ? t('新密码最多 72 个字符', 'At most 72 characters') : same ? t('新密码不能和当前密码相同', 'Must differ from the current password') : mismatch ? t('两次输入的新密码不一致', 'The new passwords do not match') : undefined
  return (
    <Section icon={<LockKeyhole size={17} />} title={t('修改密码', 'Change password')}>
      <form className="px-4 @2xl:px-6 py-5" onSubmit={e => { e.preventDefault(); if (ok && !busy) go() }}>
        <div className="text-[13.5px] text-fg2 leading-6">{t('修改后当前设备保持登录，其他设备上的会话会全部退出。', 'This device stays signed in; every other session is signed out.')}</div>
        <div className="grid grid-cols-1 @2xl:grid-cols-2 gap-x-5 gap-y-4 mt-4">
          <label className="@2xl:col-span-2 @2xl:max-w-[calc(50%-10px)]">
            <div className="text-[13px] font-semibold mb-1.5">{t('当前密码', 'Current password')}</div>
            <TextInput type="password" autoComplete="current-password" className="w-full" value={cur} onChange={e => setCur(e.target.value)} />
          </label>
          <label>
            <div className="text-[13px] font-semibold mb-1.5">{t('新密码', 'New password')}</div>
            <TextInput type="password" autoComplete="new-password" className="w-full" value={pw} onChange={e => setPw(e.target.value)} placeholder={t('8–72 个字符', '8–72 characters')} />
          </label>
          <label>
            <div className="text-[13px] font-semibold mb-1.5">{t('确认新密码', 'Confirm new password')}</div>
            <TextInput type="password" autoComplete="new-password" className="w-full" value={pw2} onChange={e => setPw2(e.target.value)} />
          </label>
          {twoFactor && <label className="@2xl:col-span-2 @2xl:max-w-[calc(50%-10px)]">
            <div className="text-[13px] font-semibold mb-1.5">{t('两步验证码', '2FA code')}</div>
            <TextInput mono inputMode="numeric" autoComplete="one-time-code" maxLength={7} className="w-full tracking-[0.25em]" value={code} onChange={e => setCode(e.target.value.replace(/[^\d ]/g, ''))} placeholder="000000" />
          </label>}
        </div>
        {hint && <div className="mt-3 text-[12.5px] text-muted font-semibold">{hint}</div>}
        {err && <div role="alert" className="mt-3 text-[12.5px] text-danger font-semibold flex items-center gap-1.5"><AlertTriangle size={14} />{err}</div>}
        <Button type="submit" variant="primary" className="mt-5" disabled={!ok || busy}>{busy ? <Loader2 size={15} className="animate-spin" /> : <KeyRound size={15} />}{t('修改密码', 'Change password')}</Button>
      </form>
    </Section>
  )
}

function TwoFactorCard({ st }: { st: TwoFactorStatus }) {
  const { t } = useT()
  const [flow, setFlow] = useState<'none' | 'enroll' | 'disable' | 'regen'>('none')
  const [codes, setCodes] = useState<string[]>()
  const low = st.enabled && st.recovery_codes_left <= 3
  return (
    <Section icon={<ShieldCheck size={17} />} title={t('两步验证（TOTP）', 'Two-factor authentication (TOTP)')}
      right={st.enabled ? <Badge tone="green"><Check size={11} strokeWidth={3} />{t('已启用', 'On')}</Badge> : <Badge tone="red">{t('未启用', 'Off')}</Badge>}>
      <div className="px-4 @2xl:px-6 py-5">
        {!st.enabled ? <>
          <div className="text-[13.5px] text-fg2 leading-6">{t('面板可以从公网访问。启用后，登录除了密码还需要验证器 App 里的 6 位动态码，密码泄露也进不来。', 'The panel is reachable from the internet. With 2FA, signing in needs the 6-digit code from an authenticator app as well as the password, so a leaked password is not enough.')}</div>
          <Button variant="primary" className="mt-4" onClick={() => setFlow('enroll')}><ShieldCheck size={15} />{t('启用两步验证', 'Turn on 2FA')}</Button>
        </> : <>
          <div className="grid grid-cols-2 gap-4 text-[13px]">
            <div><div className="text-muted text-[12px]">{t('启用时间', 'Enabled')}</div><div className="font-semibold mt-0.5">{fmt(st.enabled_at)}</div></div>
            <div><div className="text-muted text-[12px]">{t('剩余恢复码', 'Recovery codes left')}</div><div className={cx('font-semibold mt-0.5 font-mono', low && 'text-danger')}>{st.recovery_codes_left} / 10</div></div>
          </div>
          {low && <div className="mt-4 rounded-[4px] border-2 border-[var(--edge)] bg-hi text-hi-ink px-3.5 py-2.5 text-[13px] font-semibold flex items-center gap-2"><AlertTriangle size={15} className="shrink-0" />{t('恢复码快用完了，建议重新生成。', 'You are running out of recovery codes — regenerate them.')}</div>}
          <div className="flex flex-wrap gap-3 mt-5">
            <Button onClick={() => setFlow('regen')}><RefreshCw size={14} />{t('重新生成恢复码', 'Regenerate recovery codes')}</Button>
            <Button variant="danger-outline" onClick={() => setFlow('disable')}><ShieldOff size={14} />{t('停用两步验证', 'Turn off 2FA')}</Button>
          </div>
        </>}
      </div>
      {flow === 'enroll' && <EnrollModal onClose={() => setFlow('none')} onDone={c => { setFlow('none'); setCodes(c) }} />}
      {flow === 'regen' && <ReauthModal mode="regen" onClose={() => setFlow('none')} onDone={c => { setFlow('none'); if (c) setCodes(c) }} />}
      {flow === 'disable' && <ReauthModal mode="disable" onClose={() => setFlow('none')} onDone={() => setFlow('none')} />}
      {codes && <RecoveryCodesModal codes={codes} onClose={() => setCodes(undefined)} />}
    </Section>
  )
}

function errText(t: (z: string, e: string) => string, x: unknown) {
  const e = x as ApiError
  if (e.status === 429) return e.message
  if (e.code === 'invalid_code') return t('验证码不正确', 'Wrong code')
  if (e.code === 'invalid_credentials') return t('密码不正确', 'Wrong password')
  return e.message
}

function EnrollModal({ onClose, onDone }: { onClose: () => void; onDone: (codes: string[]) => void }) {
  const { t } = useT()
  const [pw, setPw] = useState(''); const [code, setCode] = useState('')
  const [en, setEn] = useState<TwoFactorEnrollment>(); const [err, setErr] = useState<string>()
  const [busy, wrap] = useBusy(); const [copied, setCopied] = useState(false)
  const start = () => wrap(async () => { setErr(undefined); try { setEn(await api.enroll2fa(pw)); setPw('') } catch (x) { setErr(errText(t, x)) } })
  const confirm = () => wrap(async () => {
    setErr(undefined)
    try { const r = await api.enable2fa(code.replace(/\s/g, '')); invalidate('auth:'); onDone(r.recovery_codes) }
    catch (x) { setErr(errText(t, x)); setCode('') }
  })
  const close = () => { if (en) api.disable2fa('', '').catch(() => {}); onClose() } // cancels the unfinished enrollment
  const secretGroups = en?.secret.match(/.{1,4}/g)?.join(' ')
  return (
    <Modal width={600} title={<><ShieldCheck size={17} className="text-accent" />{t('启用两步验证', 'Turn on two-factor authentication')}</>} onClose={close}
      footer={!en
        ? <><Button size="sm" onClick={close}>{t('取消', 'Cancel')}</Button><Button size="sm" variant="primary" disabled={!pw || busy} onClick={start}>{busy && <Loader2 size={14} className="animate-spin" />}{t('下一步', 'Next')}</Button></>
        : <><Button size="sm" onClick={close}>{t('取消', 'Cancel')}</Button><Button size="sm" variant="primary" disabled={!/^\d{6}$/.test(code.replace(/\s/g, '')) || busy} onClick={confirm}>{busy && <Loader2 size={14} className="animate-spin" />}{t('验证并启用', 'Verify and turn on')}</Button></>}>
      {!en ? (
        <form onSubmit={e => { e.preventDefault(); if (pw) start() }}>
          <div className="text-[13.5px] text-fg2 leading-6">{t('先确认是你本人：输入当前密码。', 'First confirm it is you: enter your current password.')}</div>
          <TextInput autoFocus type="password" autoComplete="current-password" className="w-full mt-4" value={pw} onChange={e => setPw(e.target.value)} placeholder={t('当前密码', 'Current password')} />
        </form>
      ) : (
        <form onSubmit={e => { e.preventDefault(); confirm() }}>
          <ol className="text-[13px] text-fg2 leading-6 list-decimal pl-5 space-y-1">
            <li>{t('用验证器 App（Google Authenticator、Microsoft Authenticator、1Password、Bitwarden 等）扫描二维码。', 'Scan the QR code with an authenticator app (Google Authenticator, Microsoft Authenticator, 1Password, Bitwarden, …).')}</li>
            <li>{t('输入 App 显示的 6 位验证码确认。', 'Enter the 6-digit code it shows to confirm.')}</li>
          </ol>
          <div className="flex flex-col sm:flex-row gap-6 items-center sm:items-start mt-5">
            <QRCode value={en.otpauth_uri} label={t('两步验证二维码', '2FA QR code')} />
            <div className="min-w-0 flex-1 w-full">
              <div className="text-[12px] text-muted">{t('无法扫码？手动输入密钥：', 'Can’t scan? Enter this key manually:')}</div>
              <div className="mt-1.5 flex items-center gap-2">
                <code className="font-mono text-[13px] font-bold break-all bg-sunk border-2 border-[var(--edge)] rounded-[4px] px-2.5 py-1.5">{secretGroups}</code>
                <button type="button" title={t('复制', 'Copy')} className="text-muted hover:text-fg" onClick={() => navigator.clipboard?.writeText(en.secret).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500) })}>{copied ? <Check size={15} /> : <Copy size={15} />}</button>
              </div>
              <div className="text-[11.5px] text-dim mt-1.5">{t('类型：基于时间 · SHA1 · 6 位 · 30 秒', 'Type: time-based · SHA1 · 6 digits · 30 s')}</div>
              <div className="text-[13px] font-semibold mt-5 mb-1.5">{t('验证码', 'Code')}</div>
              <TextInput autoFocus mono inputMode="numeric" autoComplete="one-time-code" maxLength={7} className="w-full text-[18px] tracking-[0.35em] text-center" value={code} onChange={e => setCode(e.target.value.replace(/[^\d ]/g, ''))} placeholder="000000" />
            </div>
          </div>
        </form>
      )}
      {err && <div role="alert" className="mt-4 text-[12.5px] text-danger font-semibold flex items-center gap-1.5"><AlertTriangle size={14} />{err}</div>}
    </Modal>
  )
}

function ReauthModal({ mode, onClose, onDone }: { mode: 'disable' | 'regen'; onClose: () => void; onDone: (codes?: string[]) => void }) {
  const { t } = useT()
  const [pw, setPw] = useState(''); const [code, setCode] = useState(''); const [err, setErr] = useState<string>()
  const [busy, wrap] = useBusy()
  const disable = mode === 'disable'
  const ok = !!pw && (disable ? code.trim().length >= 6 : /^\d{6}$/.test(code.replace(/\s/g, '')))
  const go = () => wrap(async () => {
    setErr(undefined)
    try {
      if (disable) { await api.disable2fa(pw, code.trim()); toast(t('两步验证已停用，其他会话已退出', '2FA turned off; other sessions were signed out'), 'ok'); invalidate('auth:'); onDone() }
      else { const r = await api.regenRecovery(pw, code.replace(/\s/g, '')); invalidate('auth:'); onDone(r.recovery_codes) }
    } catch (x) { setErr(errText(t, x)); setCode('') }
  })
  return (
    <Modal title={disable ? <><ShieldOff size={17} className="text-danger" />{t('停用两步验证', 'Turn off two-factor authentication')}</> : <><RefreshCw size={17} className="text-accent" />{t('重新生成恢复码', 'Regenerate recovery codes')}</>} onClose={onClose}
      footer={<><Button size="sm" onClick={onClose}>{t('取消', 'Cancel')}</Button><Button size="sm" variant={disable ? 'danger' : 'primary'} disabled={!ok || busy} onClick={go}>{busy && <Loader2 size={14} className="animate-spin" />}{disable ? t('停用', 'Turn off') : t('生成新的恢复码', 'Generate new codes')}</Button></>}>
      <form onSubmit={e => { e.preventDefault(); if (ok) go() }}>
        <div className="text-[13.5px] text-fg2 leading-6">{disable
          ? t('停用后只凭密码就能登录。需要输入密码和验证码（也可以用恢复码）。', 'After this, the password alone signs in. Enter your password and a code (a recovery code also works).')
          : t('旧的恢复码会全部作废。需要输入密码和验证器里的当前验证码。', 'All old recovery codes stop working. Enter your password and the current authenticator code.')}</div>
        <div className="text-[13px] font-semibold mt-4 mb-1.5">{t('密码', 'Password')}</div>
        <TextInput autoFocus type="password" autoComplete="current-password" className="w-full" value={pw} onChange={e => setPw(e.target.value)} />
        <div className="text-[13px] font-semibold mt-4 mb-1.5">{disable ? t('验证码或恢复码', 'Code or recovery code') : t('验证码', 'Code')}</div>
        <TextInput mono autoComplete="one-time-code" className="w-full" value={code} onChange={e => setCode(e.target.value)} placeholder={disable ? '000000 / xxxxx-xxxxx' : '000000'} />
        <button type="submit" hidden />
      </form>
      {err && <div role="alert" className="mt-4 text-[12.5px] text-danger font-semibold flex items-center gap-1.5"><AlertTriangle size={14} />{err}</div>}
    </Modal>
  )
}

function RecoveryCodesModal({ codes, onClose }: { codes: string[]; onClose: () => void }) {
  const { t } = useT()
  const [saved, setSaved] = useState(false); const [copied, setCopied] = useState(false)
  const [issued] = useState(() => new Date().toISOString().slice(0, 10))
  const text = `Atoll recovery codes (${location.host}, ${issued})\n${t('每个只能用一次。', 'Each code works once.')}\n\n${codes.join('\n')}\n`
  const download = () => {
    const a = document.createElement('a')
    a.href = URL.createObjectURL(new Blob([text], { type: 'text/plain' }))
    a.download = `atoll-recovery-codes-${location.hostname}.txt`
    a.click(); setTimeout(() => URL.revokeObjectURL(a.href), 1000); setSaved(true)
  }
  const copy = () => navigator.clipboard?.writeText(text).then(() => { setCopied(true); setSaved(true); setTimeout(() => setCopied(false), 1500) }, () => toast(t('复制失败，请手动选择', 'Copy failed — select the codes by hand'), 'error'))
  return (
    <Modal width={560} title={<><KeyRound size={17} className="text-accent" />{t('保存恢复码', 'Save your recovery codes')}</>} onClose={() => saved && onClose()}
      footer={<Button size="sm" variant="primary" disabled={!saved} onClick={onClose}>{t('我已保存好', 'I saved them')}</Button>}>
      <div className="text-[13.5px] text-fg2 leading-6">{t('手机丢失时可以用这些恢复码登录，每个只能用一次。它们只显示这一次——请下载或复制，存到密码管理器里。', 'If you lose your phone, each of these codes signs you in once. They are shown only now — download or copy them into your password manager.')}</div>
      <div className="grid grid-cols-1 min-[360px]:grid-cols-2 gap-x-6 gap-y-1.5 mt-4 rounded-[4px] border-2 border-[var(--edge)] bg-sunk px-5 py-4 font-mono text-[14px] font-bold">
        {codes.map(c => <div key={c}>{c}</div>)}
      </div>
      <div className="flex flex-wrap gap-3 mt-4">
        <Button size="sm" onClick={download}><Download size={14} />{t('下载 .txt', 'Download .txt')}</Button>
        <Button size="sm" onClick={copy}>{copied ? <Check size={14} /> : <Copy size={14} />}{copied ? t('已复制', 'Copied') : t('复制全部', 'Copy all')}</Button>
      </div>
      {!saved && <div className="text-[12px] text-dim mt-3">{t('下载或复制之后才能关闭。', 'Download or copy them to continue.')}</div>}
    </Modal>
  )
}

function device(ua?: string) {
  if (!ua) return { name: '—', mobile: false }
  const mobile = /iPhone|Android|Mobile|iPad/i.test(ua)
  const os = /iPhone|iPad/.test(ua) ? 'iOS' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : ''
  const br = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : /curl|Go-http|python/i.test(ua) ? ua.split(/[ /]/)[0] : ''
  return { name: [br, os].filter(Boolean).join(' · ') || ua.slice(0, 40), mobile }
}

function Sessions() {
  const { t } = useT()
  const q = useQuery('auth:sessions', api.sessions)
  const [busy, wrap] = useBusy()
  const others = (q.data ?? []).filter(s => !s.current).length
  const revoke = (s: SessionInfo) => wrap(() => run(() => api.revokeSession(s.id), { ok: t('已退出该会话', 'Session signed out'), invalidate: ['auth:sessions'] }))
  const revokeOthers = () => wrap(() => run(() => api.revokeOtherSessions(), { ok: t('其他会话已全部退出', 'All other sessions signed out'), invalidate: ['auth:sessions'] }))
  return (
    <Section icon={<Monitor size={17} />} title={t('登录会话', 'Active sessions')}
      right={others > 0 && <Button size="sm" variant="danger-outline" disabled={busy} onClick={revokeOthers}><LogOut size={13} />{t('退出其他所有会话', 'Sign out all other sessions')}</Button>}>
      {q.error && !q.data && <div className="p-5"><ErrorState error={q.error} onRetry={q.refetch} /></div>}
      {!q.data && !q.error && <div className="p-5 space-y-3"><Skeleton className="h-10 w-full" /><Skeleton className="h-10 w-full" /></div>}
      {q.data && <div className="divide-y divide-line">
        <ScrollRows visible={6} testId="session-list">
        {q.data.map(s => { const d = device(s.user_agent); return (
          <div key={s.id} className="px-4 @2xl:px-6 py-4 flex items-center gap-3 @2xl:gap-4">
            <div className="h-9 w-9 rounded-[4px] border-2 border-[var(--edge)] bg-sunk grid place-items-center shrink-0">{d.mobile ? <Smartphone size={16} /> : <Monitor size={16} />}</div>
            <div className="min-w-0 flex-1">
              <div className="text-[13.5px] font-semibold flex flex-wrap items-center gap-x-2 gap-y-1">{d.name}{s.current && <Badge tone="violet">{t('当前', 'This device')}</Badge>}</div>
              <div className="text-[12px] text-muted mt-0.5 font-mono truncate"><IP v={s.ip} /> · {t('登录于', 'signed in')} {fmt(s.created_at)}</div>
            </div>
            {!s.current && <Button size="sm" disabled={busy} onClick={() => revoke(s)}>{t('退出', 'Sign out')}</Button>}
          </div>
        ) })}
        </ScrollRows>
        {isDemo() && <div className="px-6 py-3 text-[12px] text-dim">{t('演示数据', 'Demo data')}</div>}
      </div>}
    </Section>
  )
}
