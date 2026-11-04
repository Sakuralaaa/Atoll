import { Earth } from 'lucide-react'
import { Card, Hint } from './ui'
import { Toggle } from './feedback'
import { api } from '../api/client'
import { run, useBusy } from '../api/hooks'
import type { Settings } from '../api/types'
import { KEEP_NETWORK } from './net'

/** Settings → 服务器归属: GeoIP lookups of each server's public IP (flag + provider guess). */
export default function GeoIPSettings({ s }: { s: Settings }) {
  const on = s.geoip !== 'off'
  const [busy, wrap] = useBusy()
  const set = (v: boolean) => wrap(() => run(() => api.putSettings({ ...s, ...KEEP_NETWORK, geoip: v ? 'on' : 'off' }), { ok: v ? '已开启 IP 归属查询' : '已关闭 IP 归属查询', invalidate: ['settings', 'servers'] }))
  return (
    <div className="mt-8 mb-8">
      <div className="text-[15px] font-semibold flex items-center gap-2 mb-3"><Earth size={17} />服务器归属<Hint>中控用 ipinfo.io（备用 ip-api.com）查询每台服务器的公网 IP，显示国旗，并在没填服务商时按 IP 所属网络推测图标。会把服务器 IP 发给这两个服务；结果缓存，地址变化或满 30 天才重新查询。</Hint></div>
      <Card className="px-4 @2xl:px-6 py-5 flex items-center gap-4">
        <div className="min-w-0 flex-1"><div className="text-[13px] font-semibold">按 IP 显示国旗和服务商</div><div className="text-[12.5px] text-muted mt-1">手动填写的区域和服务商优先</div></div>
        <span className={busy ? 'opacity-50 pointer-events-none' : ''}><Toggle on={on} onChange={set} /></span>
      </Card>
    </div>
  )
}
