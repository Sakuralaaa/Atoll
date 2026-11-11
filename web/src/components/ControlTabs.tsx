import { L } from '../i18n'
// 中控 → 日志 / 审计日志 / 迁移.
import { ScrollRows } from './ScrollRows'
import { ImportStepper } from './ImportStepper'
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { IP } from './IP'
import { Link } from 'react-router-dom'
import { ArrowDown, ChevronDown, ChevronRight, Loader2, RefreshCw, ScrollText, Search, ShieldCheck, X, AlertTriangle, CheckCircle2, Info } from 'lucide-react'
import { Badge, Button, Card } from './ui'
import { CodeBlock, Empty, ErrorState, RowsSkeleton, Select, TextInput, Toggle } from './feedback'
import { controlApi, domainOf } from '../api/control'
import { useQuery } from '../api/hooks'
import { ago, dateTime } from '../api/view'
import type { AuditEntry, ControlStatus } from '../api/types'
import { cx, installServerURL } from './ui-utils'

// ---------- 日志 ----------
const SINCE = [{ k: '15m', l: L("最近 15 分钟") }, { k: '1h', l: L("最近 1 小时") }, { k: '6h', l: L("最近 6 小时") }, { k: '24h', l: L("最近 24 小时") }, { k: '', l: L("不限时间") }]
const TAIL = [200, 1000, 5000]
const lineTone = (l: string) => /level=(ERROR|FATAL)|\b(ERROR|panic)\b/.test(l) ? 'text-danger' : /level=WARN|\bWARN\b/.test(l) ? 'text-amber' : 'text-fg2'

export function LogsTab() {
  const [since, setSince] = useState('1h')
  const [tail, setTail] = useState(1000)
  const [auto, setAuto] = useState(true)
  const [follow, setFollow] = useState(true)
  const q = useQuery(`control-logs:${tail}:${since}`, () => controlApi.logs(tail, since || undefined), { interval: auto ? 5000 : undefined })
  const box = useRef<HTMLDivElement>(null)
  const lines = q.data?.lines
  useLayoutEffect(() => { if (follow && box.current) box.current.scrollTop = box.current.scrollHeight }, [lines, follow])
  const onScroll = () => { const el = box.current; if (el) setFollow(el.scrollHeight - el.scrollTop - el.clientHeight < 40) }
  const toBottom = () => { setFollow(true); if (box.current) box.current.scrollTop = box.current.scrollHeight }
  return (
    <div>
      <div className="flex flex-wrap items-center gap-3 mb-4">
        <h2 className="text-[18px] font-semibold tracking-tight mr-auto">{L("atoll-server 日志")}</h2>
        <Select value={since} onChange={e => setSince(e.target.value)} className="!h-9">{SINCE.map(s => <option key={s.k} value={s.k}>{s.l}</option>)}</Select>
        <Select value={tail} onChange={e => setTail(Number(e.target.value))} className="!h-9">{TAIL.map(n => <option key={n} value={n}>{L("最近")} {n} {L("行")}</option>)}</Select>
        <label className="flex items-center gap-2 text-[13px] text-fg2 whitespace-nowrap"><Toggle on={auto} onChange={setAuto} />{L("自动刷新")}</label>
        <Button size="sm" onClick={q.refetch}><RefreshCw size={14} />{L("刷新")}</Button>
      </div>
      {q.error && !q.data && <ErrorState error={q.error} onRetry={q.refetch} />}
      {!q.data && !q.error && <RowsSkeleton n={6} />}
      {q.data && !q.data.available && <Empty icon={<ScrollText size={22} />} title={L("无法读取 journald 日志")} desc={<>{q.data.message || L("atoll-server 没有以 systemd 服务运行。")}<br />{L("日志来自")} <span className="font-mono">journalctl -u atoll-server</span>{L("，只有用 install-server.sh 安装（systemd 服务）时可用。")}</>} />}
      {q.data?.available && (
        <Card className="bg-sunk overflow-hidden relative">
          <div className="h-10 px-4 flex items-center gap-2 text-[12.5px] text-muted border-b border-line min-w-0">
            <ScrollText size={14} className="shrink-0" /><span className="font-mono truncate">journalctl -u atoll-server -o short-iso{since ? ` --since ${since}` : ''} -n {tail}</span>
            <span className="ml-auto shrink-0 tnum">{q.data.lines.length} {L("行")}</span>
          </div>
          <div ref={box} onScroll={onScroll} className="h-[62vh] min-h-[320px] overflow-y-auto overflow-x-hidden px-4 py-3 font-mono text-[12px] leading-[1.7]">
            {!q.data.lines.length && <div className="text-dim py-6 text-center">{L("这段时间内没有日志")}</div>}
            {q.data.lines.map((l, i) => <div key={i} className={cx('whitespace-pre-wrap [overflow-wrap:anywhere]', lineTone(l))}>{l}</div>)}
          </div>
          {!follow && <button onClick={toBottom} className="absolute right-4 bottom-4 chip h-8 px-3 inline-flex items-center gap-1.5 bg-hi text-hi-ink text-[12px] font-bold"><ArrowDown size={13} />{L("跳到最新")}</button>}
        </Card>
      )}
    </div>
  )
}

// ---------- 审计日志 ----------
const KIND: Record<string, [string, 'violet' | 'amber' | 'zinc' | 'green']> = { session: [L("会话"), 'violet'], token: [L("令牌"), 'amber'], cli: ['CLI', 'green'], system: [L("系统"), 'zinc'] }
const PAGE = 50

export function AuditTab() {
  const [actor, setActor] = useState(''); const [action, setAction] = useState('')
  const [f, setF] = useState({ actor: '', action: '', user: '' })
  const [rows, setRows] = useState<AuditEntry[]>()
  const [next, setNext] = useState<number>()
  const [err, setErr] = useState<Error>()
  const [loading, setLoading] = useState(true)
  const [open, setOpen] = useState<number>()
  const seq = useRef(0)
  // fetch only; callers flip `loading` first, so the mount effect sets no state synchronously
  const fetchPage = useCallback(async (before?: number) => {
    const my = ++seq.current
    try {
      const p = await controlApi.audit({ limit: PAGE, before, actor: f.actor || undefined, action: f.action || undefined, user: f.user || undefined })
      if (my !== seq.current) return
      setRows(r => before ? [...(r ?? []), ...p.entries] : p.entries); setNext(p.next_before)
    } catch (x) { if (my === seq.current) setErr(x as Error) } finally { if (my === seq.current) setLoading(false) }
  }, [f])
  // data fetch: state is only set after the request settles (the analyzer cannot see past the await)
  // oxlint-disable-next-line react/set-state-in-effect
  useEffect(() => { fetchPage() }, [fetchPage])
  const load = (before?: number) => { setLoading(true); setErr(undefined); fetchPage(before) }
  const refilter = (nf: typeof f) => { setLoading(true); setErr(undefined); setF(nf) }
  const apply = () => refilter({ actor: actor.trim(), action: action.trim(), user: f.user })
  const clear = () => { setActor(''); setAction(''); refilter({ actor: '', action: '', user: '' }) }
  const filterBy = (k: 'actor' | 'action' | 'user', v: string) => { if (k === 'actor') setActor(v); else if (k === 'action') setAction(v); refilter({ ...f, [k]: v }) }
  const COLS = '@4xl:grid-cols-[150px_minmax(0,1.1fr)_minmax(0,1fr)_minmax(0,1.4fr)_minmax(0,0.9fr)_minmax(0,1fr)]'
  return (
    <div>
      <div className="flex flex-wrap items-center gap-3 mb-4">
        <h2 className="text-[18px] font-semibold tracking-tight mr-auto">{L("审计日志")}</h2>
        <Button size="sm" onClick={() => load()} disabled={loading}>{loading ? <Loader2 size={14} className="animate-spin" /> : <RefreshCw size={14} />}{L("刷新")}</Button>
      </div>
      <form onSubmit={e => { e.preventDefault(); apply() }} className="grid grid-cols-1 @2xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] gap-3 mb-4">
        <label className="min-w-0"><div className="text-[12.5px] text-muted mb-1">{L("操作者（精确，或以 : 结尾的前缀）")}</div><TextInput list="audit-actors" className="w-full font-mono" value={actor} onChange={e => setActor(e.target.value)} placeholder={L("user:admin、token:、cli、system")} /></label>
        <label className="min-w-0"><div className="text-[12.5px] text-muted mb-1">{L("操作（精确，或以 . 结尾的前缀）")}</div><TextInput list="audit-actions" className="w-full font-mono" value={action} onChange={e => setAction(e.target.value)} placeholder={L("control.、auth.、api.")} /></label>
        <div className="flex items-end gap-2"><Button type="submit" variant="primary" className="!h-11"><Search size={15} />{L("筛选")}</Button>{(f.actor || f.action || f.user) && <Button type="button" className="!h-11" onClick={clear}><X size={15} />{L("清除")}</Button>}</div>
        <datalist id="audit-actors"><option value="user:" /><option value="token:" /><option value="cli" /><option value="system" /></datalist>
        <datalist id="audit-actions"><option value="control." /><option value="auth." /><option value="api." /><option value="service." /><option value="settings." /></datalist>
      </form>
      {f.user && <div className="mb-3 flex items-center gap-2 text-[13px] text-fg2">{L("只看账号")} <Badge tone="violet">{f.user}</Badge><button onClick={() => filterBy('user', '')} className="text-muted hover:text-fg" title={L("清除")}><X size={14} /></button></div>}
      {err && !rows && <ErrorState error={err} onRetry={() => load()} />}
      {!rows && !err && <RowsSkeleton n={6} />}
      {rows && !rows.length && <Empty icon={<ShieldCheck size={22} />} title={L("没有匹配的审计记录")} desc={f.actor || f.action || f.user ? L("换个筛选条件试试。") : L("中控的每个操作都会记录在这里。")} />}
      {rows && rows.length > 0 && (
        <Card className="overflow-hidden">
          <div className={cx('hidden @4xl:grid gap-4 px-6 h-11 items-center text-[12.5px] text-muted border-b-2 border-[var(--edge)]', COLS)}><span>{L("时间")}</span><span>{L("操作者")}</span><span>{L("操作")}</span><span>{L("对象")}</span><span>IP</span><span>{L("令牌 / 会话")}</span></div>
          <ScrollRows visible={10} divide={false} testId="audit-list">
          {rows.map(r => {
            const k = KIND[r.actor_kind] ?? [r.actor_kind, 'zinc']
            const hasDetail = r.detail !== null && r.detail !== undefined && !(typeof r.detail === 'object' && !Object.keys(r.detail as object).length)
            const ref = r.token_id ? L("令牌 {0}", r.token_id) : r.session_id ? L("会话 {0}", r.session_id) : '—'
            return (
              <div key={r.id} className="border-b border-line last:border-0">
                <div className={cx('grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-1 px-4 @2xl:px-6 py-3 items-center text-[13px]', COLS)}>
                  <span className="text-fg2 tnum @max-4xl:order-1 @max-4xl:col-start-2 @max-4xl:row-start-1 @max-4xl:text-right @max-4xl:text-[12px] whitespace-nowrap" title={dateTime(r.created_at)}>{dateTime(r.created_at)}<span className="@4xl:hidden text-dim"> · {ago(r.created_at)}</span></span>
                  <span className="flex items-center gap-2 min-w-0 @max-4xl:col-span-2 @max-4xl:order-2"><Badge tone={k[1]}>{k[0]}</Badge><span className="min-w-0 flex flex-col"><button onClick={() => filterBy('actor', r.actor)} className="font-mono text-[12.5px] truncate hover:underline min-w-0 text-left" title={L("只看 {0}", r.actor)}>{r.actor}</button>{r.user_name && (r.actor !== `user:${r.user_name}` || r.user_role === 'member') && <button onClick={() => filterBy('user', r.user_name!)} className="text-[12px] text-muted truncate hover:underline text-left" title={L("只看账号 {0} 的操作", r.user_name)}>{r.user_role === 'member' ? L("子账号 {0}", r.user_name) : L("账号 {0}", r.user_name)}</button>}</span></span>
                  <span className="min-w-0 @max-4xl:order-0 @max-4xl:col-start-1 @max-4xl:row-start-1 flex items-center gap-1.5">
                    {hasDetail ? <button onClick={() => setOpen(open === r.id ? undefined : r.id)} className="text-muted hover:text-fg shrink-0" title={L("详情")}>{open === r.id ? <ChevronDown size={14} /> : <ChevronRight size={14} />}</button> : <span className="w-[14px] shrink-0" />}
                    <button onClick={() => filterBy('action', r.action)} className={cx('font-mono text-[12.5px] font-semibold truncate hover:underline min-w-0', /failed|denied/.test(r.action) && 'text-danger')} title={L("只看 {0}", r.action)}>{r.action}</button>
                  </span>
                  <span className="truncate text-fg2 @max-4xl:col-span-2 @max-4xl:order-3 @max-4xl:pl-5" title={r.target}>{r.target || <span className="text-dim">—</span>}</span>
                  <span className="font-mono text-[12px] text-muted truncate @max-4xl:order-4 @max-4xl:pl-5" ><IP v={r.ip} /></span>
                  <span className="font-mono text-[12px] text-muted truncate @max-4xl:order-4 @max-4xl:text-right" title={ref}>{ref}</span>
                </div>
                {open === r.id && hasDetail && <pre className="mx-4 @2xl:mx-6 mb-3 rounded-lg field px-3 py-2 font-mono text-[12px] text-fg2 whitespace-pre-wrap [overflow-wrap:anywhere]">{JSON.stringify(r.detail, null, 2)}</pre>}
              </div>
            )
          })}
          </ScrollRows>
        </Card>
      )}
      {rows && rows.length > 0 && <div className="mt-4 flex items-center justify-center gap-3 text-[12.5px] text-dim">
        {next ? <Button size="sm" onClick={() => load(next)} disabled={loading}>{loading ? <Loader2 size={14} className="animate-spin" /> : <ChevronDown size={14} />}{L("加载更早的记录")}</Button> : <span>{L("没有更早的记录了")}</span>}
        <span className="tnum">{L("已显示")} {rows.length} {L("条")}</span>
      </div>}
    </div>
  )
}

// ---------- 迁移 ----------

function Cmd({ children }: { children: string }) {
  return <div className="[&>div]:whitespace-pre-wrap [&>div]:[overflow-wrap:anywhere] [&>div]:text-[12.5px]"><CodeBlock>{children}</CodeBlock></div>
}

const MIGRATE_DOCS = 'https://sakuralaaa.github.io/Atoll/control#把中控迁移到另一台机器'

// Terse on purpose: the why and the edge cases live in docs/control.md; the
// tab only shows the five commands, one step at a time.
export function MigrateTab({ s }: { s: ControlStatus }) {
  const [step, setStep] = useState(0)
  const pub = s.public_url || 'https://panel.example.com'
  const dom = domainOf(s.public_url)
  const data = s.data_dir || '/var/lib/atoll'
  const ts = s.tailnet ? ' \\\n      --tailscale-authkey tskey-auth-...' : ''
  const tagged = /^v\d+\.\d+\.\d+/.test(s.version)
  const INSTALL = installServerURL(s.version)
  const ver = tagged ? ` --version ${s.version}` : ''
  const steps: { t: string; body: React.ReactNode }[] = [
    { t: L("旧中控导出"), body: <>
      <Cmd>{L("printf '%s\\n' '你的口令至少12个字符' > /root/pass && chmod 600 /root/pass\nsudo atoll-server export --data {0} --out /root/atoll-control.atollbak --passphrase-file /root/pass", data)}</Cmd>
      <Cmd>{L("scp /root/atoll-control.atollbak /root/pass root@<新机器 IP>:/root/")}</Cmd>
      <p className="text-muted">{L("导出后不要再在旧面板上改动。")}</p>
    </> },
    { t: L("新机器恢复"), body: <Cmd>{`curl -fsSL ${INSTALL} \\\n  | sudo sh -s -- --restore /root/atoll-control.atollbak --restore-passphrase-file /root/pass \\\n      --adopt-public-ip${ver}${ts}`}</Cmd> },
    { t: L("DNS 切换"), body: <>
      {dom ? <Cmd>{`dig +short ${dom}`}</Cmd> : null}
      <p className="text-muted">{L("用了 --adopt-public-ip 会自动改面板域名的 A 记录；业务域名不用动。")}</p>
    </> },
    { t: L("agent 重连"), body: <>
      <p className="text-muted">{L("在")} <Link to="/servers" className="text-accent hover:underline">{L("服务器")}</Link> {L("页确认全部在线。没回来的那台上执行：")}</p>
      <Cmd>{`sudo atoll-agent tailnet switch --url ${pub}`}</Cmd>
    </> },
    { t: L("关闭旧中控"), body: <>
      <Cmd>{`sudo systemctl disable --now atoll-server`}</Cmd>
      <p className="text-muted">{L("在旧机器上执行，务必关掉，否则两个中控会同时管理同一批服务器。")}</p>
    </> },
  ]
  return (
    <div className="max-w-[920px]">
      <div className="flex flex-wrap items-center gap-3">
        <h2 className="text-[18px] font-semibold tracking-tight mr-auto">{L("把中控迁移到另一台机器")}</h2>
        <a href={MIGRATE_DOCS} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-[13px] text-fg2 hover:text-fg"><Info size={14} />{L("完整说明")}</a>
      </div>
      {dom
        ? <div className="mt-3 text-[13px] text-fg2 flex items-center gap-2 min-w-0"><CheckCircle2 size={15} className="text-ok shrink-0" /><span className="truncate">{L("面板域名")} <b className="font-mono">{dom}</b> {L("· agent 会跟着它找到新中控")}</span></div>
        : <div className="mt-3 text-[13px] text-danger flex items-center gap-2 min-w-0"><AlertTriangle size={15} className="shrink-0" /><span className="min-w-0">{L("面板用 IP 访问，迁移后要逐台切换地址。建议先把 ATOLL_PUBLIC_URL 改成域名。")}</span></div>}
      <div className="mt-6" data-testid="control-migrate-steps"><ImportStepper steps={steps.map(x => x.t)} step={step + 1} onPick={i => setStep(i)} /></div>
      <div data-testid="control-migrate-step"><Card className="mt-6 p-5 text-[13px] leading-6 space-y-3">
        <div className="text-[15px] font-semibold">{step + 1}. {steps[step].t}</div>
        {steps[step].body}
        <div className="flex items-center pt-1">
          {step > 0 && <Button size="sm" onClick={() => setStep(step - 1)}>{L("上一步")}</Button>}
          {step < steps.length - 1 && <Button size="sm" className="ml-auto" onClick={() => setStep(step + 1)}>{L("下一步")}</Button>}
        </div>
      </Card></div>
    </div>
  )
}
