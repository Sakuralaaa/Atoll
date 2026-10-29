import { Link, useParams } from 'react-router-dom'
import { ChevronLeft, ChevronRight, ChevronDown, Folder, FileText, FolderPlus, FilePlus, RefreshCw } from 'lucide-react'
import { projectById } from '../mock'

const tree: [number, 'd' | 'f', string, boolean?, boolean?][] = [
  [0, 'd', 'app', true], [1, 'f', '.env.example'], [1, 'd', '.github'], [1, 'f', 'VERSION'], [1, 'd', 'data', false, true],
  [1, 'f', 'docker-compose.yml'], [1, 'd', 'docs'], [1, 'd', 'static'], [1, 'f', 'main.py'], [1, 'f', 'config.yaml'],
  [1, 'f', 'requirements.txt'], [1, 'd', 'scripts'], [1, 'f', 'start.sh'], [1, 'd', 'templates'], [0, 'd', 'logs', false, true],
]
const code = `# mail-relay 配置
server:
  host: 0.0.0.0
  port: 5000
  public_url: https://mail.example.com

database:
  path: /app/data/mail.db

scheduler:
  forward_interval: 300   # 秒
  timezone: Asia/Shanghai

forward:
  channels: []            # telegram / webhook

log:
  level: INFO
  dir: /app/logs`

export default function FilesPage() {
  const { pid, sid } = useParams()
  const p = projectById(pid)
  return (
    <div className="h-full flex topline">
      <aside className="w-[360px] shrink-0 bg-side border-r border-line flex flex-col px-6 pt-7">
        <Link to={`/projects/${p.id}/services/${sid}`} className="flex items-center gap-1.5 text-[15px] text-fg2"><ChevronLeft size={16} />返回</Link>
        <div className="flex items-center mt-5"><h1 className="text-[17px] font-semibold tracking-tight flex-1">文件管理</h1><span className="flex gap-3 text-muted"><FolderPlus size={16} /><FilePlus size={16} /><RefreshCw size={15} /></span></div>
        <div className="text-[13px] text-muted mt-1 mb-4">已挂载的卷以<span className="text-accent">紫色</span>显示，重新部署后仍保留。</div>
        <div className="font-mono text-[13.5px] overflow-auto">
          {tree.map(([lv, t, n, open, vol], i) => (
            <div key={i} className={`flex items-center gap-2 h-[34px] rounded-md px-1 ${n === 'config.yaml' ? 'bg-ov/[0.07]' : ''}`} style={{ paddingLeft: lv * 26 + 4 }}>
              {t === 'd' ? (open ? <ChevronDown size={14} className="text-muted" /> : <ChevronRight size={14} className="text-muted" />) : <span className="w-[14px]" />}
              {t === 'd' ? <Folder size={15} className="text-fg2" fill="currentColor" /> : <FileText size={15} className="text-fg2" />}
              <span className={vol ? 'text-accent' : 'text-fg'}>{n}</span>
            </div>
          ))}
        </div>
      </aside>
      <main className="flex-1 flex flex-col bg-bg">
        <div className="h-12 border-b border-line flex items-center px-5 text-[13.5px] gap-3"><span className="font-mono">/app/config.yaml</span><span className="text-dim">· 412 B</span><span className="ml-auto h-8 px-3 rounded-md bg-[#7c3aed] text-white text-[13px] font-semibold inline-flex items-center">保存</span></div>
        <pre className="flex-1 p-5 font-mono text-[13.5px] leading-7 overflow-auto">
          {code.split('\n').map((l, i) => <div key={i} className="flex"><span className="w-10 text-dim select-none text-right pr-4">{i + 1}</span><span className={l.trim().startsWith('#') ? 'text-dim' : l.includes(':') && !l.startsWith(' ') ? 'text-accent-soft' : 'text-fg2'}>{l}</span></div>)}
        </pre>
      </main>
    </div>
  )
}
