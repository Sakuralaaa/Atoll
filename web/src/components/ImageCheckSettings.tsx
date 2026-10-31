import { useState } from 'react'
import { ArrowUpCircle, Loader2 } from 'lucide-react'
import { Button, Card } from './ui'
import { TextInput } from './feedback'
import { api } from '../api/client'
import { run, useBusy } from '../api/hooks'
import type { Settings } from '../api/types'

/** Settings → 镜像更新: how often the server asks registries for new image digests. */
export default function ImageCheckSettings({ s }: { s: Settings }) {
  const cur = s.image_check_interval_hours || 6
  const [v, setV] = useState(String(cur))
  const [busy, wrap] = useBusy()
  const n = Number(v)
  const ok = Number.isInteger(n) && n >= 1 && n <= 168
  // PUT /settings replaces every field: send the current values back (write-only secrets are "" = keep).
  const save = () => wrap(() => run(() => api.putSettings({ ...s, image_check_interval_hours: n }), { ok: '检查间隔已保存', invalidate: ['settings'] }))
  return (
    <div className="mt-8 mb-8">
      <div className="text-[15px] font-semibold flex items-center gap-2"><ArrowUpCircle size={17} />镜像更新</div>
      <div className="text-[13px] text-muted mt-1 mb-3 leading-5">中控定期向镜像仓库查询每个服务所用标签的最新摘要（只发 HEAD 请求，不计入 Docker Hub 拉取次数），有新版本时在服务上显示「有更新」。每个服务可在「服务状态」里单独设为仅通知、自动更新或关闭。</div>
      <Card className="px-6 py-5 grid grid-cols-[260px_minmax(0,1fr)] gap-6 items-center">
        <div><div className="text-[13px] font-semibold">检查间隔</div><div className="text-[12.5px] text-muted mt-1">小时，1–168，默认 6</div></div>
        <div className="flex items-center gap-3">
          <TextInput mono type="number" min={1} max={168} className="w-[120px]" value={v} onChange={e => setV(e.target.value)} />
          <span className="text-[13px] text-muted">小时</span>
          {!ok && <span className="text-[12px] text-[#ef4444]">请输入 1 到 168 之间的整数</span>}
          <Button className="ml-auto" size="sm" variant="primary" disabled={!ok || n === cur || busy} onClick={save}>{busy && <Loader2 size={14} className="animate-spin" />}保存</Button>
        </div>
      </Card>
    </div>
  )
}
