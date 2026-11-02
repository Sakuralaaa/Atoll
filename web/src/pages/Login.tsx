import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Loader2, LogIn, Sparkles, AlertTriangle, ShieldCheck, ArrowLeft } from 'lucide-react'
import { Logo, Button } from '../components/ui'
import { LangToggle, ThemeToggle } from '../components/Shell'
import { Skeleton, TextInput } from '../components/feedback'
import { api, isChallenge, isDemo, ApiError } from '../api/client'
import { invalidate, useQuery } from '../api/hooks'
import { useT } from '../i18n'

export default function Login() {
  const { t } = useT()
  const status = useQuery('auth:status', api.authStatus)
  const setup = !!status.data?.setup_required
  const nav = useNavigate()
  const [u, setU] = useState(''); const [pw, setPw] = useState(''); const [pw2, setPw2] = useState('')
  const [err, setErr] = useState<string>(); const [busy, setBusy] = useState(false)
  // second step
  const [pending, setPending] = useState<string>(); const [code, setCode] = useState(''); const [useRecovery, setUseRecovery] = useState(false)
  const mismatch = setup && pw2 !== '' && pw !== pw2
  const short = setup && pw !== '' && pw.length < 8
  const ok = !!u.trim() && !!pw && (!setup || (pw === pw2 && pw.length >= 8))
  const codeOk = useRecovery ? code.replace(/[\s-]/g, '').length === 10 : /^\d{6}$/.test(code.replace(/\s/g, ''))

  const explain = (x: unknown) => {
    const e = x as ApiError
    if (e.status === 429) return t('尝试次数过多，请稍后再试。', 'Too many attempts — please wait and try again.') + ' ' + e.message
    if (e.code === 'challenge_expired') return t('验证已过期或错误次数过多，请重新输入密码。', 'This sign-in expired or had too many wrong codes. Enter your password again.')
    if (e.code === 'invalid_code') return useRecovery ? t('恢复码无效或已用过', 'Invalid or already used recovery code') : t('验证码不正确，请输入验证器里当前的 6 位数字', 'Wrong code — enter the current 6 digits from your authenticator')
    if (e.status === 401 || /invalid|unauthor/i.test(e.message)) return t('用户名或密码不正确', 'Wrong username or password')
    return e.message
  }
  const done = () => { invalidate(); nav('/', { replace: true }) }

  const submit = async (e: React.FormEvent) => {
    e.preventDefault(); if (!ok || busy) return
    setBusy(true); setErr(undefined)
    try {
      const r = await (setup ? api.setup(u.trim(), pw) : api.login(u.trim(), pw))
      if (isChallenge(r)) { setPending(r.pending_token); setCode(''); setUseRecovery(false); setPw('') }
      else done()
    } catch (x) { setErr(explain(x)) } finally { setBusy(false) }
  }
  const submitCode = async (e: React.FormEvent) => {
    e.preventDefault(); if (!pending || !codeOk || busy) return
    setBusy(true); setErr(undefined)
    try { await api.login2fa(pending, code.trim()); done() } catch (x) {
      const ae = x as ApiError
      setErr(explain(x))
      if (ae.code === 'challenge_expired') setPending(undefined)
      setCode('')
    } finally { setBusy(false) }
  }
  const back = () => { setPending(undefined); setErr(undefined); setCode('') }

  const errBox = err && <div role="alert" className="mt-4 rounded-[4px] border-2 border-[var(--edge)] bg-[#fca5a5] text-[#141414] px-3.5 py-2.5 text-[13px] font-semibold flex items-center gap-2"><AlertTriangle size={15} className="shrink-0" />{err}</div>

  return (
    <div className="h-full canvas topline flex flex-col">
      <div className="flex justify-end gap-2 p-5"><LangToggle /><ThemeToggle /></div>
      <div className="flex-1 flex items-center justify-center px-4 sm:px-6 pb-20">
        <div className="w-full max-w-[420px] min-w-0">
          <div className="flex justify-center mb-8"><Logo /></div>
          {pending ? (
            <form onSubmit={submitCode} className="rise surface p-6 sm:p-8">
              <h1 className="text-[22px] font-extrabold italic tracking-[-0.03em] flex items-center gap-2"><ShieldCheck size={20} className="text-accent" />{t('两步验证', 'Two-factor authentication')}</h1>
              <p className="text-[13px] text-muted mt-1.5 leading-6">{useRecovery
                ? t('输入一个还没用过的恢复码（如 k7m2q-x9tpw）。每个恢复码只能用一次。', 'Enter one of your unused recovery codes (like k7m2q-x9tpw). Each code works once.')
                : t('打开验证器 App（Google Authenticator、1Password 等），输入 Atoll 的 6 位验证码。', 'Open your authenticator app (Google Authenticator, 1Password, …) and enter the 6-digit Atoll code.')}</p>
              <label className="block mt-6">
                <div className="text-[13px] font-semibold mb-1.5">{useRecovery ? t('恢复码', 'Recovery code') : t('验证码', 'Code')}</div>
                {useRecovery
                  ? <TextInput key="rc" autoFocus mono autoComplete="off" spellCheck={false} className="w-full tracking-[0.12em]" value={code} onChange={e => setCode(e.target.value)} placeholder="xxxxx-xxxxx" />
                  : <TextInput key="otp" autoFocus mono inputMode="numeric" autoComplete="one-time-code" maxLength={7} className="w-full text-[20px] tracking-[0.4em] text-center" value={code} onChange={e => setCode(e.target.value.replace(/[^\d ]/g, ''))} placeholder="000000" />}
              </label>
              {errBox}
              <Button type="submit" variant="primary" size="lg" className="w-full mt-6 h-11" disabled={!codeOk || busy}>{busy ? <Loader2 size={15} className="animate-spin" /> : <LogIn size={15} />}{t('验证并登录', 'Verify and sign in')}</Button>
              <div className="mt-5 flex flex-wrap items-center justify-between gap-x-4 gap-y-2 text-[12.5px]">
                <button type="button" onClick={back} className="text-muted hover:text-fg flex items-center gap-1"><ArrowLeft size={13} />{t('返回', 'Back')}</button>
                <button type="button" onClick={() => { setUseRecovery(!useRecovery); setCode(''); setErr(undefined) }} className="text-accent font-semibold hover:underline">{useRecovery ? t('改用验证码', 'Use authenticator code') : t('手机丢了？用恢复码', 'Lost your phone? Use a recovery code')}</button>
              </div>
            </form>
          ) : (
            <form onSubmit={submit} className="rise surface p-6 sm:p-8">
              {!status.data && !status.error ? <div className="space-y-4"><Skeleton className="h-7 w-40" /><Skeleton className="h-11 w-full" /><Skeleton className="h-11 w-full" /></div> : <>
                <h1 className="text-[22px] font-extrabold italic tracking-[-0.03em] flex items-center gap-2">{setup ? <><Sparkles size={20} className="text-accent" />{t('创建管理员账号', 'Create the admin account')}</> : t('登录 Atoll', 'Sign in to Atoll')}</h1>
                <p className="text-[13px] text-muted mt-1.5 leading-6">{setup
                  ? t('这是第一次启动。创建的账号拥有全部权限，之后可以在「CLI 与 API」里给 agent 发令牌。', 'First start. This account has full access; later you can issue tokens for agents under "CLI & API".')
                  : t('自托管的 PaaS 控制台：你的服务器，你的服务。', 'Self-hosted PaaS console: your servers, your services.')}</p>
                {status.error && <div className="mt-4 text-[12.5px] text-[#ef4444] flex items-start gap-1.5"><AlertTriangle size={14} className="mt-0.5 shrink-0" />{t('无法获取服务器状态：', 'Cannot reach the server: ')}{status.error.message}</div>}
                <label className="block mt-6"><div className="text-[13px] font-semibold mb-1.5">{t('用户名', 'Username')}</div><TextInput autoFocus autoComplete="username" className="w-full" value={u} onChange={e => setU(e.target.value)} placeholder="admin" /></label>
                <label className="block mt-4"><div className="text-[13px] font-semibold mb-1.5">{t('密码', 'Password')}</div><TextInput type="password" autoComplete={setup ? 'new-password' : 'current-password'} className="w-full" value={pw} onChange={e => setPw(e.target.value)} />
                  {short && <div className="text-[12px] text-[#ef4444] mt-1.5">{t('至少 8 位', 'At least 8 characters')}</div>}</label>
                {setup && <label className="block mt-4"><div className="text-[13px] font-semibold mb-1.5">{t('确认密码', 'Confirm password')}</div><TextInput type="password" autoComplete="new-password" className="w-full" value={pw2} onChange={e => setPw2(e.target.value)} />
                  {mismatch && <div className="text-[12px] text-[#ef4444] mt-1.5">{t('两次输入不一致', 'Passwords do not match')}</div>}</label>}
                {errBox}
                <Button type="submit" variant="primary" size="lg" className="w-full mt-6 h-11" disabled={!ok || busy}>{busy ? <Loader2 size={15} className="animate-spin" /> : setup ? <Sparkles size={15} /> : <LogIn size={15} />}{setup ? t('创建并进入', 'Create and continue') : t('登录', 'Sign in')}</Button>
              </>}
            </form>
          )}
          <div className="text-center text-[12px] text-dim mt-6 leading-5 [overflow-wrap:anywhere]">{isDemo()
            ? t('演示模式：任意用户名和密码都能登录（加 &2fa=1 体验两步验证，任意 6 位数字）', 'Demo mode: any username and password work (add &2fa=1 to try 2FA; any 6 digits)')
            : pending
              ? t('验证器和恢复码都丢了？在中控服务器上运行 atoll-server admin reset-2fa <用户名>', 'Lost both? On the control host run: atoll-server admin reset-2fa <user>')
              : t('忘记密码？在中控服务器上运行 atoll-server reset-password', 'Forgot your password? On the control host run: atoll-server reset-password')}</div>
        </div>
      </div>
    </div>
  )
}
