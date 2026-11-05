import { L } from '../i18n'
import { Link, Navigate, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { projName } from '../api/view'
import { Package, Plus, Settings } from 'lucide-react'
import { ProjectShell } from '../components/Shell'
import { Button } from '../components/ui'
import { CardSkeleton, Empty, ErrorState } from '../components/feedback'
import AddServiceModal from '../components/AddServiceModal'
import { placeholder, useProject } from '../api/queries'

/** /projects/:pid — jump to the first service, or show an empty state with the add dialog. */
export default function ProjectIndex() {
  const { pid = '' } = useParams()
  const [sp, setSp] = useSearchParams()
  const nav = useNavigate()
  const q = useProject(pid)
  const p = q.data
  const first = p?.services?.[0]
  if (first && !sp.get('add')) return <Navigate replace to={`/projects/${pid}/services/${first.id}`} />
  const openAdd = () => { const n = new URLSearchParams(sp); n.set('add', '1'); setSp(n) }
  const closeAdd = () => { const n = new URLSearchParams(sp); n.delete('add'); setSp(n, { replace: true }) }
  return (
    <ProjectShell project={p ?? placeholder(pid)}>
      <div className="max-w-[1100px] mx-auto px-4 pt-6 pb-12 @2xl:px-8 @2xl:pt-10 @5xl:px-14 @5xl:pt-12 @5xl:pb-16">
        {q.error && !p && <ErrorState error={q.error} onRetry={q.refetch} />}
        {!p && !q.error && <CardSkeleton n={1} cols={1} h={200} />}
        {p && !first && <>
          <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em] mb-8">{projName(p)}</h1>
          <Empty icon={<Package size={22} />} title={L("项目里还没有服务")} desc={L("部署一个 Docker 镜像，或者一键创建 PostgreSQL / MySQL / Redis / MongoDB。同一项目的服务通过内网主机名互相访问。")}
            action={<div className="flex gap-2"><Button variant="primary" onClick={openAdd}><Plus size={15} />{L("新建服务")}</Button><Link to={`/projects/${pid}/settings`}><Button><Settings size={14} />{L("项目设置")}</Button></Link></div>} />
        </>}
      </div>
      {sp.get('add') && p && <AddServiceModal projectId={pid} onCreated={s => nav(`/projects/${pid}/services/${s.id}`)} onClose={closeAdd} />}
    </ProjectShell>
  )
}
