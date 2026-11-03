// Shared re-authentication for sensitive 中控 actions (export, download, passphrase, update, rollback).
// POST /control/reauth {password, code?} opens a 5-minute window for this session; actions then go
// out without credentials. A wrong password stays inline (noRedirect); 429 shows the lockout.
import { useCallback, useEffect, useState, useSyncExternalStore } from 'react'
import { AlertTriangle, Loader2, LockKeyhole, ShieldCheck } from 'lucide-react'
import { Button } from './ui'
import { Modal, TextInput } from './feedback'
import { api, ApiError, isDemo } from '../api/client'
import { controlApi } from '../api/control'
import { useQuery } from '../api/hooks'
import { reauthSubs, reauthUntil, setUntil } from './reauthWindow'

function useWindowLeft() {
  const u = useSyncExternalStore(f => { reauthSubs.add(f); return () => { reauthSubs.delete(f) } }, reauthUntil)
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => { if (u <= Date.now()) return; const t = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(t) }, [u])
  return Math.max(0, Math.floor((u - now) / 1000))
}

/** Small "verified, n:nn left" chip for page headers. */
export function ReauthChip() {
  const left = useWindowLeft()
  if (left <= 15) return null
  return (
    <span title="5 分钟内的敏感操作无需重复输入密码" className="inline-flex items-center gap-1.5 h-7 px-2.5 rounded-[4px] border-[1.5px] border-[var(--edge)] bg-[#86efac] text-[#141414] text-[11.5px] font-bold font-mono whitespace-nowrap">
      <ShieldCheck size={13} />已验证 {Math.floor(left / 60)}:{String(left % 60).padStart(2, '0')}
    </span>
  )
}

function waitText(sec?: number) {
  if (!sec) return ''
  return sec < 120 ? `${sec} 秒` : `${Math.ceil(sec / 60)} 分钟`
}

export function ReauthDialog({ title = '确认身份', desc, onDone }: { title?: string; desc?: React.ReactNode; onDone: (ok: boolean) => void }) {
  const tf = useQuery('auth:2fa', api.twoFactor)
  const [pw, setPw] = useState(''); const [code, setCode] = useState('')
  const [needCode, setNeedCode] = useState(false)
  const [err, setErr] = useState<string>()
  const [lockedUntil, setLockedUntil] = useState(0)
  const [busy, setBusy] = useState(false)
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => { if (lockedUntil <= Date.now()) return; const t = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(t) }, [lockedUntil])
  const twoFactor = !!tf.data?.enabled || needCode
  const code6 = code.replace(/\s/g, '')
  const locked = lockedUntil > now
  const ok = !!pw && (!twoFactor || /^\d{6}$/.test(code6)) && !busy && !locked
  const close = useCallback(() => onDone(false), [onDone])
  const go = async () => {
    if (!ok) return
    setBusy(true); setErr(undefined)
    try {
      const r = await controlApi.reauth({ password: pw, ...(twoFactor ? { code: code6 } : {}) })
      // trust the server's expiry but never the client clock beyond 5 minutes from now
      setUntil(Math.min(Date.parse(r.expires_at) || 0, Date.now() + 5 * 60e3) || Date.now() + 5 * 60e3)
      onDone(true)
    } catch (x) {
      const e = x as ApiError
      if (e.status === 429) {
        const sec = e.retryAfter ?? Number(/(\d+)\s*second/.exec(e.message)?.[1] ?? 0)
        setLockedUntil(Date.now() + (sec || 30) * 1000)
        setErr(`失败次数过多，已临时锁定${sec ? `，请 ${waitText(sec)}后再试` : '，请稍后再试'}。锁定期间即使密码正确也会被拒绝。`)
      } else if (e.code === 'invalid_credentials') { setErr('当前密码不正确'); setPw('') }
      else if (e.code === 'code_required') { setNeedCode(true); setErr('已启用两步验证，请输入验证器里的 6 位验证码') }
      else if (e.code === 'invalid_code') setErr('验证码不正确或已过期')
      else setErr(e.message || '验证失败')
      setCode('')
    } finally { setBusy(false) }
  }
  const left = Math.max(0, Math.ceil((lockedUntil - now) / 1000))
  return (
    <Modal width={440} title={<><LockKeyhole size={17} className="text-accent" />{title}</>} onClose={close}
      footer={<><Button size="sm" onClick={close}>取消</Button><Button size="sm" variant="primary" disabled={!ok} onClick={go}>{busy ? <Loader2 size={14} className="animate-spin" /> : <ShieldCheck size={14} />}{locked ? `已锁定 ${left}s` : '验证'}</Button></>}>
      <form onSubmit={ev => { ev.preventDefault(); go() }}>
        <div className="text-[13.5px] text-fg2 leading-6 [overflow-wrap:anywhere]">{desc ?? '这是敏感操作，请输入当前密码。'}{' '}验证通过后 5 分钟内，本会话的其他敏感操作无需重复输入。</div>
        <label className="block mt-4">
          <div className="text-[13px] font-semibold mb-1.5">当前密码</div>
          <TextInput autoFocus type="password" autoComplete="current-password" className="w-full" value={pw} onChange={ev => setPw(ev.target.value)} />
        </label>
        {twoFactor && <label className="block mt-4">
          <div className="text-[13px] font-semibold mb-1.5">两步验证码</div>
          <TextInput mono inputMode="numeric" autoComplete="one-time-code" maxLength={7} className="w-full tracking-[0.25em]" value={code} onChange={ev => setCode(ev.target.value.replace(/[^\d ]/g, ''))} placeholder="000000" />
          <div className="text-[12px] text-dim mt-1.5">验证器里当前的验证码（不接受恢复码）</div>
        </label>}
        {err && <div role="alert" className="mt-3 text-[12.5px] text-[#ef4444] font-semibold flex items-start gap-1.5 [overflow-wrap:anywhere]"><AlertTriangle size={14} className="mt-0.5 shrink-0" />{err}</div>}
        {isDemo() && <div className="mt-3 text-[12px] text-dim">演示模式：任意密码均可；输入 wrong 体验密码错误，连续 3 次触发锁定。</div>}
        <button type="submit" hidden />
      </form>
    </Modal>
  )
}
