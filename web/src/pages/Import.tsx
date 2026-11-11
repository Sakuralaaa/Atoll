import { Link, useSearchParams } from 'react-router-dom'
import { X, Boxes, CloudCog, ArrowRight, ArrowLeft } from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Button } from '../components/ui'
import { ImportStepper } from '../components/ImportStepper'
import { Load, RowsSkeleton } from '../components/feedback'
import { api } from '../api/client'
import { useQuery } from '../api/hooks'
import type { ImportJob } from '../api/types'
import { StepScan, StepServer } from './import/StepScan'
import { StepSelect } from './import/SelectForm'
import { JobView } from './import/JobView'
import { AdvancedCli, History } from './import/History'
import { L, useT } from '../i18n'

const STEPS = [L("选择服务器"), L("扫描"), L("选择服务"), L("演练"), L("切换"), L("移除 Zeabur k3s")]

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
  // /import with nothing picked is a chooser; ?from=zeabur (or any server/job) is the Zeabur wizard
  const chooser = !jobId && !server && sp.get('from') !== 'zeabur'
  const job = useQuery(jobId ? `imports:${jobId}` : null, () => api.importJob(jobId!), { interval: 2000 })
  const go = (q: Record<string, string>) => setSp(q)
  const step = jobId ? (job.data ? jobStep(job.data, stepQ) : 4) : server ? (stepQ === 'select' && ns ? 3 : 2) : 1
  const completed = jobId && job.data?.status === 'cleaned' ? 7 : jobId && job.data && ['live'].includes(job.data.status) && step === 5 ? 6 : step
  if (chooser) return <Chooser />
  return (
    <MainShell>
      <div className="max-w-[1240px] mx-auto px-4 pt-6 pb-12 @2xl:px-8 @2xl:pt-10 @5xl:px-14 @5xl:pt-12 @5xl:pb-16">
        <Link to="/import" className="inline-flex items-center gap-1.5 text-[13.5px] text-fg2 hover:text-fg"><ArrowLeft size={15} />{L("迁移")}</Link>
        <div className="flex flex-wrap items-end justify-between gap-3 mt-2 mb-2">
          <div className="min-w-0">
            <h1 className="text-[24px] @2xl:text-[26px] font-extrabold italic tracking-[-0.03em] flex flex-wrap items-center gap-x-3 gap-y-1">{L("从 Zeabur 迁移")}</h1>
            <p className="text-[13.5px] text-muted mt-2 max-w-[760px] leading-6">{L("在 Zeabur 托管的那台 VPS 上装好 Atoll Agent，服务原样搬进本机 Docker：镜像从本机导出，数据卷本机复制，数据库同版本导出恢复。同一台机器、同一个 IP；Zeabur 上的东西一样不删，随时可以回滚。")}</p>
          </div>
          {(jobId || server) && <Button onClick={() => setSp({ from: 'zeabur' })}><X size={14} />{L("重新开始")}</Button>}
        </div>
        <ImportStepper steps={STEPS} step={step} done={completed} />
        <div className="mt-7">
          {step === 1 && <StepServer onPick={id => go({ server: id })} />}
          {step === 2 && server && <StepScan serverId={server} onPick={n => go({ server, step: 'select', ns: n })} />}
          {step === 3 && server && ns && <StepSelect serverId={server} ns={ns} onBack={() => go({ server })} onCreated={id => go({ job: id })} />}
          {jobId && <Load q={job} skeleton={<RowsSkeleton n={6} />}>{j => <JobView job={j} step={step} set={q => go({ job: j.id, ...q })} openConfirm={sp.get('confirm') === '1'} />}</Load>}
        </div>
        <History current={jobId} />
      </div>
    </MainShell>
  )
}

function Chooser() {
  const { t } = useT()
  const ways = [
    { to: '/import?from=zeabur', icon: <CloudCog size={22} />, title: t('从 Zeabur 迁移', 'Move off Zeabur'),
      desc: t('在 Zeabur 托管的 VPS 上装好 Agent，服务原样搬进本机 Docker。同一台机器、同一个 IP，Zeabur 上的东西不删。', 'Install the agent on the Zeabur-managed VPS and move its services into Docker on the same machine and IP. Nothing on Zeabur is deleted.'),
      steps: t('选择服务器 → 扫描 → 选择服务 → 演练 → 切换', 'Server → scan → pick services → rehearse → cut over') },
    { to: '/import/docker', icon: <Boxes size={22} />, title: t('接管 Docker 容器', 'Adopt Docker containers'),
      desc: t('把 docker run / compose 启动的容器交给 Atoll：同样的端口，数据原地挂载，旧容器保留。', 'Hand containers started with docker run / compose to Atoll: same ports, data mounted in place, old containers kept.'),
      steps: t('选择服务器 → 扫描容器 → 演练 → 切换', 'Server → scan → rehearse → cut over') },
  ]
  return (
    <MainShell>
      <div className="max-w-[1240px] mx-auto px-4 pt-6 pb-12 @2xl:px-8 @2xl:pt-10 @5xl:px-14 @5xl:pt-12 @5xl:pb-16">
        <h1 className="text-[24px] @2xl:text-[26px] font-extrabold italic tracking-[-0.03em]">{t('迁移', 'Migrate')}</h1>
        <p className="text-[13.5px] text-muted mt-2 max-w-[760px] leading-6">{t('把已有的服务接管到 Atoll。每种方式都先演练再切换，旧的东西一直保留，随时可以回滚。', 'Bring existing services under Atoll. Every path rehearses before it cuts over and keeps the old setup, so you can roll back any time.')}</p>
        <div className="mt-7 grid grid-cols-1 @3xl:grid-cols-2 gap-5">
          {ways.map(w => (
            <Link key={w.to} to={w.to} data-testid={`import-way-${w.to === '/import/docker' ? 'docker' : 'zeabur'}`} className="surface surface-hover rounded-2xl p-5 @2xl:p-6 flex flex-col gap-3 min-w-0">
              <div className="flex items-center gap-3">
                <span className="h-11 w-11 rounded-xl grid place-items-center bg-[var(--nav-bg)] text-[var(--nav-ink)] border-[length:var(--bw-thin)] border-[var(--edge)] shrink-0">{w.icon}</span>
                <span className="text-[16px] font-bold">{w.title}</span>
                <ArrowRight size={16} className="ml-auto text-dim shrink-0" />
              </div>
              <p className="text-[13px] text-fg2 leading-6">{w.desc}</p>
              <p className="text-[12px] text-muted mt-auto">{w.steps}</p>
            </Link>
          ))}
        </div>
        <History current={null} />
        <AdvancedCli />
      </div>
    </MainShell>
  )
}
