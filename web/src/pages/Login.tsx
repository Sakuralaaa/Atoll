import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Loader2, LogIn, Sparkles, AlertTriangle } from 'lucide-react'
import { Logo, Button } from '../components/ui'
import { ThemeToggle } from '../components/Shell'
import { Skeleton, TextInput } from '../components/feedback'
import { api, isDemo } from '../api/client'
import { invalidate, useQuery } from '../api/hooks'

export default function Login() {
  const status = useQuery('auth:status', api.authStatus)
  const setup = !!status.data?.setup_required
  const nav = useNavigate()
  const [u, setU] = useState(''); const [pw, setPw] = useState(''); const [pw2, setPw2] = useState('')
  const [err, setErr] = useState<string>(); const [busy, setBusy] = useState(false)
  const mismatch = setup && pw2 !== '' && pw !== pw2
  const short = setup && pw !== '' && pw.length < 8
  const ok = !!u.trim() && !!pw && (!setup || (pw === pw2 && pw.length >= 8))
  const submit = async (e: React.FormEvent) => {
    e.preventDefault(); if (!ok || busy) return
    setBusy(true); setErr(undefined)
    try {
      await (setup ? api.setup(u.trim(), pw) : api.login(u.trim(), pw))
      invalidate(); nav('/', { replace: true })
    } catch (x) {
      const m = (x as Error).message
      setErr(/401|invalid|unauthor/i.test(m) ? '用户名或密码不正确' : m)
    } finally { setBusy(false) }
  }
  return (
    <div className="h-full canvas topline flex flex-col">
      <div className="flex justify-end p-5"><ThemeToggle /></div>
      <div className="flex-1 grid place-items-center px-6 pb-20">
        <div className="w-[420px] max-w-full">
          <div className="flex justify-center mb-8"><Logo /></div>
          <form onSubmit={submit} className="rise surface p-8">
            {!status.data && !status.error ? <div className="space-y-4"><Skeleton className="h-7 w-40" /><Skeleton className="h-11 w-full" /><Skeleton className="h-11 w-full" /></div> : <>
              <h1 className="text-[22px] font-extrabold italic tracking-[-0.03em] flex items-center gap-2">{setup ? <><Sparkles size={20} className="text-accent" />创建管理员账号</> : '登录 Atoll'}</h1>
              <p className="text-[13px] text-muted mt-1.5 leading-6">{setup ? '这是第一次启动。创建的账号拥有全部权限，之后可以在「CLI 与 API」里给 agent 发令牌。' : '自托管的 PaaS 控制台：你的服务器，你的服务。'}</p>
              {status.error && <div className="mt-4 text-[12.5px] text-[#ef4444] flex items-start gap-1.5"><AlertTriangle size={14} className="mt-0.5 shrink-0" />无法获取服务器状态：{status.error.message}</div>}
              <label className="block mt-6"><div className="text-[13px] font-semibold mb-1.5">用户名</div><TextInput autoFocus autoComplete="username" className="w-full" value={u} onChange={e => setU(e.target.value)} placeholder="admin" /></label>
              <label className="block mt-4"><div className="text-[13px] font-semibold mb-1.5">密码</div><TextInput type="password" autoComplete={setup ? 'new-password' : 'current-password'} className="w-full" value={pw} onChange={e => setPw(e.target.value)} />
                {short && <div className="text-[12px] text-[#ef4444] mt-1.5">至少 8 位</div>}</label>
              {setup && <label className="block mt-4"><div className="text-[13px] font-semibold mb-1.5">确认密码</div><TextInput type="password" autoComplete="new-password" className="w-full" value={pw2} onChange={e => setPw2(e.target.value)} />
                {mismatch && <div className="text-[12px] text-[#ef4444] mt-1.5">两次输入不一致</div>}</label>}
              {err && <div className="mt-4 rounded-[4px] border-2 border-[var(--edge)] bg-[#fca5a5] text-[#141414] px-3.5 py-2.5 text-[13px] font-semibold flex items-center gap-2"><AlertTriangle size={15} className="shrink-0" />{err}</div>}
              <Button type="submit" variant="primary" size="lg" className="w-full mt-6 h-11" disabled={!ok || busy}>{busy ? <Loader2 size={15} className="animate-spin" /> : setup ? <Sparkles size={15} /> : <LogIn size={15} />}{setup ? '创建并进入' : '登录'}</Button>
            </>}
          </form>
          <div className="text-center text-[12px] text-dim mt-6">{isDemo() ? '演示模式：任意用户名和密码都能登录' : '忘记密码？在中控服务器上运行 atoll-server reset-password'}</div>
        </div>
      </div>
    </div>
  )
}
