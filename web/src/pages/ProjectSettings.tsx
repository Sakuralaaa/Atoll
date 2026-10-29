import { useParams } from 'react-router-dom'
import { ProjectShell } from '../components/Shell'
import { Button, Card, Mini } from '../components/ui'
import { projectById, serverById } from '../mock'

export default function ProjectSettings() {
  const p = projectById(useParams().pid)
  const srv = serverById(p.serverId)
  const Row = ({ t, d, children }: { t: string; d?: string; children: React.ReactNode }) => <div className="px-6 py-5 flex items-center gap-6"><div className="flex-1"><div className="text-[13px] font-semibold">{t}</div>{d && <div className="text-[13px] text-muted mt-1">{d}</div>}</div>{children}</div>
  return (
    <ProjectShell project={p}>
      <div className="max-w-[1200px] mx-auto px-14 pt-12 pb-16">
        <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em] mb-8">项目设置</h1>
        <div className="text-[13px] font-semibold mb-3">基本</div>
        <Card className="divide-y divide-line mb-8">
          <Row t="项目名称"><div className="w-[420px] h-11 rounded-lg field px-3.5 flex items-center text-[13px]">{p.name}</div></Row>
          <Row t="运行服务器" d="同一项目的服务都部署在这台服务器上，通过 Docker 内网互通"><span className="flex items-center gap-2 text-[13px]"><Mini provider={srv.provider} />{srv.name}</span></Row>
        </Card>
        <div className="text-[13px] font-semibold mb-3">迁移</div>
        <Card className="divide-y divide-line mb-8">
          <Row t="迁移到其他服务器" d="先同步一次卷数据，停机后再增量同步并导出导入数据库，最后自动切换 DNS。停机通常在一分钟内。"><Button variant="primary" size="sm">选择目标服务器</Button></Row>
        </Card>
        <div className="text-[13px] font-semibold mb-3">危险区域</div>
        <Card className="divide-y divide-line">
          <Row t="删除项目" d="删除项目下所有服务、卷和域名记录"><Button variant="danger" size="sm">删除项目</Button></Row>
        </Card>
      </div>
    </ProjectShell>
  )
}
