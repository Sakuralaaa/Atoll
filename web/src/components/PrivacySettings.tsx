import { EyeOff } from 'lucide-react'
import { Card, Hint } from './ui'
import { Toggle } from './feedback'
import { useHideIPs } from '../api/privacy'

/** Settings → 隐私: mask server and session IPs in this browser (each one can still be shown with its eye). */
export default function PrivacySettings() {
  const [hide, set] = useHideIPs()
  return (
    <div className="mt-8 mb-8">
      <div className="text-[15px] font-semibold flex items-center gap-2 mb-3"><EyeOff size={17} />隐私<Hint>只影响这个浏览器里的显示，适合截图或共享屏幕。复制和搜索仍使用完整地址。</Hint></div>
      <Card className="px-4 @2xl:px-6 py-5 flex items-center gap-4">
        <div className="min-w-0 flex-1"><div className="text-[13px] font-semibold">隐藏 IP 地址</div><div className="text-[12.5px] text-muted mt-1">显示为 203.0.•.•，点地址旁的眼睛可单独查看；服务器页右上角可一键全部显示</div></div>
        <Toggle on={hide} onChange={set} />
      </Card>
    </div>
  )
}
