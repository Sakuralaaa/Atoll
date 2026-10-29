import { Link, useParams } from 'react-router-dom'
import { ArrowLeft, Clock, Download, ChevronDown, ChevronRight, ArrowDown } from 'lucide-react'
import { CopyId, cx } from '../components/ui'
import { projectById } from '../mock'

export default function LogsPage() {
  const { pid, sid } = useParams()
  const p = projectById(pid)
  const s = p.services.find(x => x.id === sid) ?? p.services[0]
  const bars = Array.from({ length: 48 }, (_, i) => [4, 2, 2, 4, 2, 2, 4, 3, 2, 2, 4, 2][i % 12] + (i === 17 ? 10 : 0) + (i === 47 ? 4 : 0) + (i === 25 ? -1 : 0))
  const logs: [string, string, string][] = [
    ['INFO', '10/29 21:11:07', '[forward] skip job: no active channels configured'],
    ['INFO', '10/29 21:11:07', '2026-10-29 21:11:07,454 INFO [apscheduler.executors.default] Job "邮件转发轮询 (trigger: interval[0:05:00])" executed successfully'],
    ['INFO', '10/29 21:16:07', '2026-10-29 21:16:07,452 INFO [apscheduler.executors.default] Running job "邮件转发轮询 (trigger: interval[0:05:00], next run at: 21:21:07 CST)"'],
    ['INFO', '10/29 21:16:07', '[forward] skip job: no active channels configured'],
    ['WARN', '10/29 21:18:42', 'token refresh for account #3 took 4.2s (threshold 3s)'],
    ['INFO', '10/29 21:21:01', '104.28.158.90 - - [29/Oct/2026:21:21:01 +0800] "GET / HTTP/1.1" 302 199 "-" "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36"'],
    ['INFO', '10/29 21:21:01', '104.28.158.90 - - [29/Oct/2026:21:21:01 +0800] "GET /login HTTP/1.1" 200 8141 "-" "Mozilla/5.0 (X11; Linux x86_64)"'],
    ['ERROR', '10/29 21:21:02', '104.28.158.90 - - [29/Oct/2026:21:21:02 +0800] "GET /favicon.ico HTTP/1.1" 404 240 "https://mail.example.com/login"'],
    ['INFO', '10/29 21:21:07', '2026-10-29 21:21:07,452 INFO [apscheduler.executors.default] Running job "邮件转发轮询 (trigger: interval[0:05:00], next run at: 21:26:07 CST)"'],
    ['INFO', '10/29 21:21:07', '[forward] skip job: no active channels configured'],
    ['INFO', '10/29 21:21:07', '2026-10-29 21:21:07,453 INFO [apscheduler.executors.default] Job "邮件转发轮询 (trigger: interval[0:05:00])" executed successfully'],
    ['INFO', '10/29 21:26:07', '2026-10-29 21:26:07,451 INFO [apscheduler.executors.default] Running job "邮件转发轮询"'],
  ]
  return (
    <div className="h-full flex flex-col topline bg-bg">
      <div className="px-10 pt-8 pb-5 flex items-end justify-between border-b border-line">
        <div>
          <Link to={`/projects/${p.id}/services/${s.id}`} className="inline-flex items-center gap-1.5 text-[13.5px] text-fg2"><ArrowLeft size={15} />返回</Link>
          <div className="text-[13px] text-muted mt-3">{s.name} · 服务 ID</div>
          <div className="mt-1"><CopyId v="service-6ae031310ca070d4379490b1" /></div>
          <div className="mt-4 inline-flex rounded-lg field p-0.5 gap-1">
            <span className="h-9 px-4 rounded-md nav-active inline-flex items-center text-[13.5px] font-semibold">运行日志</span>
            <span className="h-9 px-4 rounded-md inline-flex items-center text-[13.5px] text-muted">部署记录</span>
            <span className="h-9 px-4 rounded-md inline-flex items-center text-[13.5px] text-muted">命令</span>
          </div>
        </div>
        <div className="flex gap-2">
          <button className="h-11 px-4 rounded-lg border border-line2 bg-card flex items-center gap-2 text-[13.5px] font-semibold"><Clock size={16} />全部时间<ChevronDown size={15} /></button>
          <button className="h-11 px-4 rounded-lg border border-line2 bg-card flex items-center gap-2 text-[13.5px] font-semibold"><Download size={16} />导出</button>
        </div>
      </div>
      <div className="px-10 pt-5 pb-3 border-b border-line">
        <div className="flex gap-3 h-[150px]">
          <div className="flex flex-col justify-between text-[11.5px] text-dim pb-5"><span>14</span><span>8</span><span>4</span><span>0</span></div>
          <div className="flex-1 flex flex-col">
            <div className="flex-1 flex items-end gap-[3px]">{bars.map((b, i) => <div key={i} className="flex-1 bg-[#8b5cf6] rounded-t-[2px]" style={{ height: `${(b / 14) * 100}%` }} />)}</div>
            <div className="flex justify-between text-[11.5px] text-dim mt-1.5">{['10/29 16:11', '10/29 16:56', '10/29 17:41', '10/29 18:26', '10/29 19:11', '10/29 19:57', '10/29 20:42'].map(t => <span key={t}>{t}</span>)}</div>
          </div>
        </div>
      </div>
      <div className="grid grid-cols-[130px_150px_1fr] px-4 h-10 items-center text-[11.5px] font-semibold tracking-wider text-muted border-b border-line bg-sunk"><span className="pl-6">SEVERITY</span><span>TIME</span><span>SUMMARY</span></div>
      <div className="flex-1 overflow-auto font-mono text-[13px] relative">
        {logs.map(([l, t, m], i) => (
          <div key={i} className="grid grid-cols-[130px_150px_1fr] px-4 h-[33px] items-center border-b border-line/60 hover:bg-ov/[0.02]">
            <span className="flex items-center gap-2"><ChevronRight size={14} className="text-dim" /><span className={cx('h-2 w-2 rounded-full', l === 'ERROR' ? 'bg-rose-500' : l === 'WARN' ? 'bg-amber-400' : 'bg-sky-500')} /><span className="text-[11.5px] font-semibold">{l}</span></span>
            <span className="text-muted">{t}</span><span className="truncate text-fg2">{m}</span>
          </div>
        ))}
        <div className="h-10 flex items-center justify-center text-[13px] text-muted gap-2 font-sans"><span className="h-2 w-2 rounded-full bg-emerald-500 animate-pulse" />Streaming logs</div>
        <button className="absolute right-6 bottom-4 h-10 w-10 rounded-full bg-card2 ring-1 ring-line2 grid place-items-center"><ArrowDown size={17} /></button>
      </div>
    </div>
  )
}
