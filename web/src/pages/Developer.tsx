import { Plus, Copy, Terminal, KeyRound, Trash2, Bot } from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Badge, Button, Card } from '../components/ui'

const Code = ({ children }: { children: string }) => (
  <div className="relative rounded-lg field px-4 py-3.5 font-mono text-[13px] leading-6 text-fg2 whitespace-pre overflow-auto">{children}<Copy size={14} className="absolute right-3 top-3.5 text-muted" /></div>
)

export default function Developer() {
  return (
    <MainShell>
      <div className="max-w-[1180px] mx-auto px-16 pt-16 pb-16">
        <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em]">CLI 与 API</h1>
        <p className="text-[13px] text-muted mt-2">Atoll 本身不内置 AI。用 API 令牌把 CLI 或 MCP 交给 Claude Code、Codex 这类外部 agent，让它们替你部署和排错。</p>

        <div className="mt-8 flex items-center justify-between mb-3"><div className="text-[15px] font-semibold flex items-center gap-2"><KeyRound size={18} />API 令牌</div><Button variant="primary"><Plus size={15} />创建令牌</Button></div>
        <Card className="divide-y divide-line">
          {[
            ['claude-code-macbook', 'atl_pat_••••••••3fA9', '项目：全部 · 读写', '2 分钟前'],
            ['codex-ci', 'atl_pat_••••••••k21Q', '项目：saas-api · 仅部署', '昨天'],
            ['只读监控', 'atl_pat_••••••••Zx07', '全部 · 只读', '5 天前'],
          ].map(([n, t, sc, u]) => (
            <div key={n} className="px-6 h-16 grid grid-cols-[1.3fr_1.2fr_1.4fr_1fr_40px] items-center text-[13px]">
              <span className="font-semibold">{n}</span><span className="font-mono text-[13px] text-muted">{t}</span><span className="text-fg2">{sc}</span><span className="text-muted text-[13px]">最近使用 {u}</span><Trash2 size={15} className="text-muted" />
            </div>
          ))}
        </Card>

        <div className="mt-10 text-[15px] font-semibold flex items-center gap-2 mb-3"><Terminal size={18} />CLI</div>
        <div className="grid grid-cols-2 gap-5">
          <Card className="p-5"><div className="text-[13px] font-semibold mb-3">安装并登录</div><Code>{`curl -fsSL https://get.atoll.sh | sh
atoll login --url https://panel.example.com \\
             --token atl_pat_xxx`}</Code></Card>
          <Card className="p-5"><div className="text-[13px] font-semibold mb-3">常用命令</div><Code>{`atoll deploy ghcr.io/me/api:v1.9 -p saas-api
atoll env set DATABASE_URL=... -s api
atoll logs api --follow
atoll domain add api.example.com -s api`}</Code></Card>
        </div>

        <div className="mt-10 text-[15px] font-semibold flex items-center gap-2 mb-1"><Bot size={18} />接入 AI agent（MCP）</div>
        <div className="text-[13.5px] text-muted mb-4">agent 能查看项目、部署镜像、改变量、看日志。删除服务、迁移、改 DNS 这类操作默认需要你在面板里确认。</div>
        <div className="grid grid-cols-2 gap-5">
          <Card className="p-5"><div className="flex items-center gap-2 text-[13px] font-semibold mb-3">Claude Code<Badge tone="zinc">一行添加</Badge></div><Code>{`claude mcp add atoll -- atoll mcp \\
  --token atl_pat_xxx`}</Code></Card>
          <Card className="p-5"><div className="flex items-center gap-2 text-[13px] font-semibold mb-3">Codex<Badge tone="zinc">~/.codex/config.toml</Badge></div><Code>{`[mcp_servers.atoll]
command = "atoll"
args = ["mcp", "--token", "atl_pat_xxx"]`}</Code></Card>
        </div>
        <Card className="p-5 mt-5"><div className="text-[13px] font-semibold mb-3">REST API</div><Code>{`curl https://panel.example.com/api/v1/projects \\
  -H "Authorization: Bearer atl_pat_xxx"      # OpenAPI 文档：/api/docs`}</Code></Card>
      </div>
    </MainShell>
  )
}
