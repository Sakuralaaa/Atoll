import { useSearchParams } from 'react-router-dom'
import { Server as ServerIcon, ScanSearch, ListChecks, FlaskConical, Power, Trash2, Check, X, ChevronRight } from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Badge, Button } from '../components/ui'
import { Load, RowsSkeleton } from '../components/feedback'
import { api } from '../api/client'
import { useQuery } from '../api/hooks'
import type { ImportJob } from '../api/types'
import { cx } from '../components/ui-utils'
import { StepScan, StepServer } from './import/StepScan'
import { StepSelect } from './import/SelectForm'
import { JobView } from './import/JobView'
import { AdvancedCli, History } from './import/History'

const STEPS = [
  { n: 1, l: '选择服务器', i: <ServerIcon size={15} /> },
  { n: 2, l: '扫描', i: <ScanSearch size={15} /> },
  { n: 3, l: '选择服务', i: <ListChecks size={15} /> },
  { n: 4, l: '演练', i: <FlaskConical size={15} /> },
  { n: 5, l: '切换', i: <Power size={15} /> },
  { n: 6, l: '移除 Zeabur k3s', i: <Trash2 size={15} /> },
]

function jobStep(j: ImportJob, want: string | null) {
  if (['cleaning', 'cleaned'].includes(j.status)) return 6
  if (['live', 'rolling_back', 'rolled_back'].includes(j.status)) return want === 'cleanup' && j.status === 'live' ? 6 : 5
  if (j.status === 'cutting_over') return 5
  if (j.status === 'rehearsed' && want === 'cutover') return 5
  return 4
}

export default function Import() {
  const [sp, setSp] = useSearchParams()
  const jobId = sp.get('job'), server = sp.get('server'), stepQ = sp.get('step'), ns = sp.get('ns')
  const job = useQuery(jobId ? `imports:${jobId}` : null, () => api.importJob(jobId!), { interval: 2000 })
  const go = (q: Record<string, string>) => setSp(q)
  const step = jobId ? (job.data ? jobStep(job.data, stepQ) : 4) : server ? (stepQ === 'select' && ns ? 3 : 2) : 1
  const completed = jobId && job.data?.status === 'cleaned' ? 7 : jobId && job.data && ['live'].includes(job.data.status) && step === 5 ? 6 : step
  return (
    <MainShell>
      <div className="max-w-[1240px] mx-auto px-4 pt-6 pb-12 @2xl:px-8 @2xl:pt-10 @5xl:px-14 @5xl:pt-12 @5xl:pb-16">
        <div className="flex flex-wrap items-end justify-between gap-3 mb-2">
          <div className="min-w-0">
            <h1 className="text-[24px] @2xl:text-[26px] font-extrabold italic tracking-[-0.03em] flex flex-wrap items-center gap-x-3 gap-y-1">从 Zeabur 迁移<Badge tone="violet">原地接管</Badge></h1>
            <p className="text-[13.5px] text-muted mt-2 max-w-[760px] leading-6">在 Zeabur 托管的那台 VPS 上装好 Atoll Agent，服务原样搬进本机 Docker：镜像从本机导出，数据卷本机复制，数据库同版本导出恢复。同一台机器、同一个 IP；Zeabur 上的东西一样不删，随时可以回滚。</p>
          </div>
          {(jobId || server) && <Button onClick={() => setSp({})}><X size={14} />重新开始</Button>}
        </div>
        <Stepper step={step} completed={completed} />
        <div className="mt-7">
          {step === 1 && <StepServer onPick={id => go({ server: id })} />}
          {step === 2 && server && <StepScan serverId={server} onPick={n => go({ server, step: 'select', ns: n })} />}
          {step === 3 && server && ns && <StepSelect serverId={server} ns={ns} onBack={() => go({ server })} onCreated={id => go({ job: id })} />}
          {jobId && <Load q={job} skeleton={<RowsSkeleton n={6} />}>{j => <JobView job={j} step={step} set={q => go({ job: j.id, ...q })} openConfirm={sp.get('confirm') === '1'} />}</Load>}
        </div>
        <History current={jobId} />
        <AdvancedCli />
      </div>
    </MainShell>
  )
}

function Stepper({ step, completed }: { step: number; completed: number }) {
  return (
    <div className="mt-7 flex items-center gap-2 flex-wrap">
      {STEPS.map((s, i) => {
        const done = s.n < completed && s.n !== step || completed > 6
        const active = s.n === step && completed <= 6
        return (
          <div key={s.n} className="flex items-center gap-2">
            <div className={cx('h-10 pl-1.5 pr-3.5 rounded-lg inline-flex items-center gap-2 text-[13px] font-semibold border-2', active ? 'nav-active' : done ? 'border-[var(--edge)] bg-card text-fg' : 'border-dashed border-line2 text-muted')}>
              <span className={cx('h-6 w-6 rounded-[4px] grid place-items-center text-[12px] font-extrabold font-mono border-[1.5px] border-[var(--edge)] not-italic', done ? 'bg-[#86efac] text-[#141414]' : active ? 'bg-[#8b5cf6] text-white' : 'bg-sunk text-muted')}>{done ? <Check size={13} strokeWidth={3} /> : s.n}</span>
              {s.l}
            </div>
            {i < STEPS.length - 1 && <ChevronRight size={15} className="text-dim" />}
          </div>
        )
      })}
    </div>
  )
}
