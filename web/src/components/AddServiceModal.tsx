import { Search, Box, Database, LayoutTemplate, FileCode2, ChevronRight, X } from 'lucide-react'

const items = [
  { i: <Box size={20} className="text-[#3b9cff]" />, t: 'Docker 容器镜像', d: '从 Docker Hub、GHCR 或私有仓库部署' },
  { i: <Database size={20} className="text-[#38bdf8]" />, t: '数据库', d: 'PostgreSQL · MySQL · Redis · MongoDB，自动生成密码与连接变量' },
  { i: <LayoutTemplate size={20} className="text-accent" />, t: '模板', d: '一键部署常用开源应用' },
  { i: <FileCode2 size={20} className="text-[#34d399]" />, t: 'Docker Compose', d: '粘贴 compose 文件，拆成多个服务' },
]

export default function AddServiceModal({ onClose }: { onClose: () => void }) {
  return (
    <div className="fixed inset-0 z-50 bg-black/65 flex items-start justify-center pt-[14vh]" onClick={onClose}>
      <div className="w-[620px] rounded-2xl border border-line2 bg-pop shadow-2xl overflow-hidden" onClick={e => e.stopPropagation()}>
        <div className="flex items-center gap-3 px-5 h-16 border-b border-line">
          <Search size={18} className="text-muted" /><span className="text-[16px] text-dim flex-1">要部署什么服务？</span>
          <button onClick={onClose} className="text-muted"><X size={18} /></button>
        </div>
        <div className="p-2">
          {items.map((x, idx) => (
            <div key={x.t} className={`flex items-center gap-4 px-4 h-[68px] rounded-xl ${idx === 0 ? 'bg-ov/[0.06]' : 'hover:bg-ov/[0.04]'}`}>
              <div className="h-10 w-10 rounded-lg bg-card2 ring-1 ring-line2 grid place-items-center">{x.i}</div>
              <div className="flex-1"><div className="text-[14px] font-semibold">{x.t}</div><div className="text-[13px] text-muted mt-0.5">{x.d}</div></div>
              <ChevronRight size={17} className="text-dim" />
            </div>
          ))}
        </div>
        <div className="border-t border-line px-5 h-11 flex items-center gap-4 text-[12px] text-dim"><span>↑↓ 选择</span><span>↵ 确认</span><span>Esc 关闭</span></div>
      </div>
    </div>
  )
}
