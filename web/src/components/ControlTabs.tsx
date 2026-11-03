// 中控 → 日志 / 审计日志 / 迁移.
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
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
const SINCE = [{ k: '15m', l: '最近 15 分钟' }, { k: '1h', l: '最近 1 小时' }, { k: '6h', l: '最近 6 小时' }, { k: '24h', l: '最近 24 小时' }, { k: '', l: '不限时间' }]
const TAIL = [200, 1000, 5000]
const lineTone = (l: string) => /level=(ERROR|FATAL)|\b(ERROR|panic)\b/.test(l) ? 'text-[#f87171]' : /level=WARN|\bWARN\b/.test(l) ? 'text-[#fbbf24]' : 'text-fg2'

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
        <h2 className="text-[18px] font-semibold tracking-tight mr-auto">atoll-server 日志</h2>
        <Select value={since} onChange={e => setSince(e.target.value)} className="!h-9">{SINCE.map(s => <option key={s.k} value={s.k}>{s.l}</option>)}</Select>
        <Select value={tail} onChange={e => setTail(Number(e.target.value))} className="!h-9">{TAIL.map(n => <option key={n} value={n}>最近 {n} 行</option>)}</Select>
        <label className="flex items-center gap-2 text-[13px] text-fg2 whitespace-nowrap"><Toggle on={auto} onChange={setAuto} />自动刷新</label>
        <Button size="sm" onClick={q.refetch}><RefreshCw size={14} />刷新</Button>
      </div>
      {q.error && !q.data && <ErrorState error={q.error} onRetry={q.refetch} />}
      {!q.data && !q.error && <RowsSkeleton n={6} />}
      {q.data && !q.data.available && <Empty icon={<ScrollText size={22} />} title="无法读取 journald 日志" desc={<>{q.data.message || 'atoll-server 没有以 systemd 服务运行。'}<br />日志来自 <span className="font-mono">journalctl -u atoll-server</span>，只有用 install-server.sh 安装（systemd 服务）时可用。</>} />}
      {q.data?.available && (
        <Card className="bg-sunk overflow-hidden relative">
          <div className="h-10 px-4 flex items-center gap-2 text-[12.5px] text-muted border-b border-line min-w-0">
            <ScrollText size={14} className="shrink-0" /><span className="font-mono truncate">journalctl -u atoll-server -o short-iso{since ? ` --since ${since}` : ''} -n {tail}</span>
            <span className="ml-auto shrink-0 tnum">{q.data.lines.length} 行</span>
          </div>
          <div ref={box} onScroll={onScroll} className="h-[62vh] min-h-[320px] overflow-y-auto overflow-x-hidden px-4 py-3 font-mono text-[12px] leading-[1.7]">
            {!q.data.lines.length && <div className="text-dim py-6 text-center">这段时间内没有日志</div>}
            {q.data.lines.map((l, i) => <div key={i} className={cx('whitespace-pre-wrap [overflow-wrap:anywhere]', lineTone(l))}>{l}</div>)}
          </div>
          {!follow && <button onClick={toBottom} className="absolute right-4 bottom-4 chip h-8 px-3 inline-flex items-center gap-1.5 bg-[#fde047] text-[#141414] text-[12px] font-bold"><ArrowDown size={13} />跳到最新</button>}
        </Card>
      )}
    </div>
  )
}

// ---------- 审计日志 ----------
const KIND: Record<string, [string, 'violet' | 'amber' | 'zinc' | 'green']> = { session: ['会话', 'violet'], token: ['令牌', 'amber'], cli: ['CLI', 'green'], system: ['系统', 'zinc'] }
const PAGE = 50

export function AuditTab() {
  const [actor, setActor] = useState(''); const [action, setAction] = useState('')
  const [f, setF] = useState({ actor: '', action: '' })
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
      const p = await controlApi.audit({ limit: PAGE, before, actor: f.actor || undefined, action: f.action || undefined })
      if (my !== seq.current) return
      setRows(r => before ? [...(r ?? []), ...p.entries] : p.entries); setNext(p.next_before)
    } catch (x) { if (my === seq.current) setErr(x as Error) } finally { if (my === seq.current) setLoading(false) }
  }, [f])
  // data fetch: state is only set after the request settles (the analyzer cannot see past the await)
  // oxlint-disable-next-line react/set-state-in-effect
  useEffect(() => { fetchPage() }, [fetchPage])
  const load = (before?: number) => { setLoading(true); setErr(undefined); fetchPage(before) }
  const refilter = (nf: typeof f) => { setLoading(true); setErr(undefined); setF(nf) }
  const apply = () => refilter({ actor: actor.trim(), action: action.trim() })
  const clear = () => { setActor(''); setAction(''); refilter({ actor: '', action: '' }) }
  const filterBy = (k: 'actor' | 'action', v: string) => { if (k === 'actor') setActor(v); else setAction(v); refilter({ ...f, [k]: v }) }
  const COLS = '@4xl:grid-cols-[150px_minmax(0,1.1fr)_minmax(0,1fr)_minmax(0,1.4fr)_minmax(0,0.9fr)_minmax(0,1fr)]'
  return (
    <div>
      <div className="flex flex-wrap items-center gap-3 mb-4">
        <h2 className="text-[18px] font-semibold tracking-tight mr-auto">审计日志</h2>
        <Button size="sm" onClick={() => load()} disabled={loading}>{loading ? <Loader2 size={14} className="animate-spin" /> : <RefreshCw size={14} />}刷新</Button>
      </div>
      <form onSubmit={e => { e.preventDefault(); apply() }} className="grid grid-cols-1 @2xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] gap-3 mb-4">
        <label className="min-w-0"><div className="text-[12.5px] text-muted mb-1">操作者（精确，或以 : 结尾的前缀）</div><TextInput list="audit-actors" className="w-full font-mono" value={actor} onChange={e => setActor(e.target.value)} placeholder="user:admin、token:、cli、system" /></label>
        <label className="min-w-0"><div className="text-[12.5px] text-muted mb-1">操作（精确，或以 . 结尾的前缀）</div><TextInput list="audit-actions" className="w-full font-mono" value={action} onChange={e => setAction(e.target.value)} placeholder="control.、auth.、api." /></label>
        <div className="flex items-end gap-2"><Button type="submit" variant="primary" className="!h-11"><Search size={15} />筛选</Button>{(f.actor || f.action) && <Button type="button" className="!h-11" onClick={clear}><X size={15} />清除</Button>}</div>
        <datalist id="audit-actors"><option value="user:" /><option value="token:" /><option value="cli" /><option value="system" /></datalist>
        <datalist id="audit-actions"><option value="control." /><option value="auth." /><option value="api." /><option value="service." /><option value="settings." /></datalist>
      </form>
      {err && !rows && <ErrorState error={err} onRetry={() => load()} />}
      {!rows && !err && <RowsSkeleton n={6} />}
      {rows && !rows.length && <Empty icon={<ShieldCheck size={22} />} title="没有匹配的审计记录" desc={f.actor || f.action ? '换个筛选条件试试。' : '中控的每个操作都会记录在这里。'} />}
      {rows && rows.length > 0 && (
        <Card className="overflow-hidden">
          <div className={cx('hidden @4xl:grid gap-4 px-6 h-11 items-center text-[12.5px] text-muted border-b-2 border-[var(--edge)]', COLS)}><span>时间</span><span>操作者</span><span>操作</span><span>对象</span><span>IP</span><span>令牌 / 会话</span></div>
          {rows.map(r => {
            const k = KIND[r.actor_kind] ?? [r.actor_kind, 'zinc']
            const hasDetail = r.detail !== null && r.detail !== undefined && !(typeof r.detail === 'object' && !Object.keys(r.detail as object).length)
            const ref = r.token_id ? `令牌 ${r.token_id}` : r.session_id ? `会话 ${r.session_id}` : '—'
            return (
              <div key={r.id} className="border-b border-line last:border-0">
                <div className={cx('grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-1 px-4 @2xl:px-6 py-3 items-center text-[13px]', COLS)}>
                  <span className="text-fg2 tnum @max-4xl:order-1 @max-4xl:col-start-2 @max-4xl:row-start-1 @max-4xl:text-right @max-4xl:text-[12px] whitespace-nowrap" title={dateTime(r.created_at)}>{dateTime(r.created_at)}<span className="@4xl:hidden text-dim"> · {ago(r.created_at)}</span></span>
                  <span className="flex items-center gap-2 min-w-0 @max-4xl:col-span-2 @max-4xl:order-2"><Badge tone={k[1]}>{k[0]}</Badge><button onClick={() => filterBy('actor', r.actor)} className="font-mono text-[12.5px] truncate hover:underline min-w-0" title={`只看 ${r.actor}`}>{r.actor}</button></span>
                  <span className="min-w-0 @max-4xl:order-0 @max-4xl:col-start-1 @max-4xl:row-start-1 flex items-center gap-1.5">
                    {hasDetail ? <button onClick={() => setOpen(open === r.id ? undefined : r.id)} className="text-muted hover:text-fg shrink-0" title="详情">{open === r.id ? <ChevronDown size={14} /> : <ChevronRight size={14} />}</button> : <span className="w-[14px] shrink-0" />}
                    <button onClick={() => filterBy('action', r.action)} className={cx('font-mono text-[12.5px] font-semibold truncate hover:underline min-w-0', /failed|denied/.test(r.action) && 'text-[#ef4444]')} title={`只看 ${r.action}`}>{r.action}</button>
                  </span>
                  <span className="truncate text-fg2 @max-4xl:col-span-2 @max-4xl:order-3 @max-4xl:pl-5" title={r.target}>{r.target || <span className="text-dim">—</span>}</span>
                  <span className="font-mono text-[12px] text-muted truncate @max-4xl:order-4 @max-4xl:pl-5" title={r.ip}>{r.ip || '—'}</span>
                  <span className="font-mono text-[12px] text-muted truncate @max-4xl:order-4 @max-4xl:text-right" title={ref}>{ref}</span>
                </div>
                {open === r.id && hasDetail && <pre className="mx-4 @2xl:mx-6 mb-3 rounded-lg field px-3 py-2 font-mono text-[12px] text-fg2 whitespace-pre-wrap [overflow-wrap:anywhere]">{JSON.stringify(r.detail, null, 2)}</pre>}
              </div>
            )
          })}
        </Card>
      )}
      {rows && rows.length > 0 && <div className="mt-4 flex items-center justify-center gap-3 text-[12.5px] text-dim">
        {next ? <Button size="sm" onClick={() => load(next)} disabled={loading}>{loading ? <Loader2 size={14} className="animate-spin" /> : <ChevronDown size={14} />}加载更早的记录</Button> : <span>没有更早的记录了</span>}
        <span className="tnum">已显示 {rows.length} 条</span>
      </div>}
    </div>
  )
}

// ---------- 迁移 ----------

function Step({ n, title, children }: { n: number; title: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[32px_minmax(0,1fr)] gap-x-4">
      <div className="flex flex-col items-center"><span className="tile h-8 w-8 rounded-lg bg-[#fde047] text-[#141414] grid place-items-center font-mono font-extrabold text-[14px]">{n}</span><span className="flex-1 w-[2px] bg-line mt-2" /></div>
      <div className="pb-8 min-w-0"><div className="text-[15px] font-semibold mt-1 mb-2">{title}</div><div className="text-[13px] text-fg2 leading-6 space-y-3">{children}</div></div>
    </div>
  )
}
function Cmd({ children }: { children: string }) {
  return <div className="[&>div]:whitespace-pre-wrap [&>div]:[overflow-wrap:anywhere] [&>div]:text-[12.5px]"><CodeBlock>{children}</CodeBlock></div>
}

export function MigrateTab({ s }: { s: ControlStatus }) {
  const pub = s.public_url || 'https://panel.example.com'
  const dom = domainOf(s.public_url)
  const data = s.data_dir || '/var/lib/atoll'
  const ts = s.tailnet ? ' \\\n      --tailscale-authkey tskey-auth-...' : ''
  const tagged = /^v\d+\.\d+\.\d+/.test(s.version)
  const INSTALL = installServerURL(s.version)
  const ver = tagged ? ` --version ${s.version}` : ''
  return (
    <div className="max-w-[920px]">
      <h2 className="text-[18px] font-semibold tracking-tight">把中控迁移到另一台机器</h2>
      <p className="text-[13px] text-muted mt-1.5 leading-6">中控 VPS 到期、换配置或换机房时使用。全程业务不停，只有面板 / API 和 agent 的控制连接中断几分钟。本页只是操作指南，命令需要在服务器上执行。</p>
      {dom
        ? <div className="mt-4 rounded-lg border-2 border-[var(--edge)] bg-[#bbf7d0] text-[#141414] px-4 py-3 text-[13px] flex items-start gap-2.5 [overflow-wrap:anywhere]"><CheckCircle2 size={16} className="mt-0.5 shrink-0" /><span className="min-w-0">面板用域名 <b className="font-mono">{dom}</b> 访问：中控会把它推送给每个 agent 作为备用地址，迁移后 agent 靠它找到新中控。迁移前请确认所有服务器都已在线连过当前中控。</span></div>
        : <div className="mt-4 rounded-lg border-2 border-[var(--edge)] bg-[#fca5a5] text-[#141414] px-4 py-3 text-[13px] flex items-start gap-2.5 [overflow-wrap:anywhere]"><AlertTriangle size={16} className="mt-0.5 shrink-0" /><span className="min-w-0">面板当前用 IP 访问（<span className="font-mono">{pub}</span>）。agent 没有可以跟着走的域名，迁移后需要在每台服务器上手动切换地址（第 4 步）。强烈建议先把 <span className="font-mono">ATOLL_PUBLIC_URL</span> 改成 Cloudflare 区域里的域名。</span></div>}
      <div className="mt-8">
        <Step n={1} title="在旧中控导出">
          <p>在「备份」里点 <b>导出加密包</b>（需要密码和验证码，再设置导出口令），或在旧主机上离线导出：</p>
          <Cmd>{`printf '%s\\n' '你的口令至少12个字符' > /root/pass && chmod 600 /root/pass\nsudo atoll-server export --data ${data} --out /root/atoll-control.atollbak --passphrase-file /root/pass`}</Cmd>
          <p>导出后<b>不要再在旧面板上做改动</b>。想做到零丢失，先 <span className="font-mono">sudo systemctl stop atoll-server</span> 再离线导出——停掉中控不影响业务。然后把备份包和口令文件拷到新机器：</p>
          <Cmd>{`scp /root/atoll-control.atollbak /root/pass root@<新机器 IP>:/root/`}</Cmd>
        </Step>
        <Step n={2} title="在新 VPS 上安装并恢复">
          <Cmd>{`curl -fsSL ${INSTALL} \\\n  | sudo sh -s -- --restore /root/atoll-control.atollbak --restore-passphrase-file /root/pass \\\n      --adopt-public-ip${ver}${ts}`}</Cmd>
          <p>安装脚本会在第一次启动前把备份恢复到数据目录，合并备份里的 <span className="font-mono">server.env</span>，公网地址是域名时自动装 Caddy 并签证书，然后启动。</p>
          <ul className="list-disc pl-5 space-y-1.5 text-[12.5px] text-muted">
            <li><span className="font-mono text-fg2">--adopt-public-ip</span>：新中控第一次启动时把 {dom ? <b className="font-mono">{dom}</b> : '面板域名'} 的 A 记录改成本机公网 IPv4（保留代理开关）。不想自动改 DNS 就去掉，第 3 步手动改。</li>
            {s.tailnet && <li><span className="font-mono text-fg2">--tailscale-authkey</span>：当前中控在 tailnet 上，新中控也要加入，agent 才能回到私网连接。</li>}
            <li>恢复时会清掉旧的 tailnet IP 地址，也不会带过来 <span className="font-mono">ATOLL_AGENT_TAILNET_ONLY=1</span>：迁移期间 agent 要从公网域名找到新中控。</li>
          </ul>
        </Step>
        <Step n={3} title="DNS 切换">
          <p>用了 <span className="font-mono">--adopt-public-ip</span> 且域名在 Cloudflare 区域内：新中控启动后自动完成，审计日志里记为 <span className="font-mono">control.dns_adopt</span>。否则手动把 {dom ? <b className="font-mono">{dom}</b> : '面板域名'} 的 A 记录改为新机器 IP。业务域名指向各 worker，<b>不需要改</b>。</p>
          {dom && <Cmd>{`dig +short ${dom}`}</Cmd>}
        </Step>
        <Step n={4} title="agent 自动重连">
          <p>每个 agent 先试主地址（旧中控，连不上），再试备用地址 <span className="font-mono">{pub}</span>（现在指向新中控）。在 <Link to="/servers" className="text-accent hover:underline">服务器</Link> 页确认全部恢复在线。没收到过备用地址的 agent，在那台服务器上执行：</p>
          <Cmd>{`sudo atoll-agent tailnet switch --url ${pub}`}</Cmd>
        </Step>
        <Step n={5} title="关闭旧中控">
          <p>所有 agent 都连上新中控后，在<b>旧机器</b>上执行。务必关闭：旧中控还开着时，经旧 tailnet 地址连接的 agent 会一直连在它上面（两个中控同时管理同一批服务器）。确认无误后再退订旧 VPS。</p>
          <Cmd>{`sudo systemctl disable --now atoll-server`}</Cmd>
        </Step>
      </div>
      <Card className="overflow-hidden">
        <div className="px-5 h-11 flex items-center gap-2 text-[13px] font-semibold border-b-2 border-[var(--edge)]"><Info size={15} />停机影响</div>
        {([
          ['业务容器、对外域名、HTTPS', '不受影响（入口和证书都在各 worker 上）', true],
          ['定时服务备份', '不受影响（agent 本地调度，重连后补报结果）', true],
          ['面板 / API / CLI / MCP', '旧中控停止到新中控启动、DNS 生效，通常几分钟', false],
          ['部署、迁移、域名变更', '中控不可用期间无法操作', false],
          ['agent 控制连接', '主地址失败 3 次后切到备用地址，通常一两分钟内重连', false],
        ] as const).map(([k, v, ok]) => (
          <div key={k} className="grid grid-cols-1 @2xl:grid-cols-[240px_minmax(0,1fr)] gap-x-4 gap-y-0.5 px-5 py-3 text-[13px] border-b border-line last:border-0">
            <span className="font-semibold">{k}</span><span className={ok ? 'text-emerald-600 dark:text-emerald-400 font-semibold' : 'text-fg2'}>{v}</span>
          </div>
        ))}
      </Card>
    </div>
  )
}
