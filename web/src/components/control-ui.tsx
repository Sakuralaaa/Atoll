import { L } from '../i18n'
// Small display pieces shared by the 中控 page and its tabs.
import { Badge } from './ui'

export function KV({ k, v }: { k: string; v: React.ReactNode }) {
  return <div className="flex items-start justify-between gap-4 min-h-10 py-2.5 text-[13px] leading-5"><span className="text-muted shrink-0 whitespace-nowrap">{k}</span><span className="text-fg min-w-0 text-right [overflow-wrap:anywhere]">{v}</span></div>
}

/** Where a control backup lives: local copy and/or the bucket. */
export function Where({ local, remote }: { local: boolean; remote: boolean }) {
  return <span className="inline-flex flex-wrap justify-end gap-1.5">
    {local && <span title={L("备份文件在中控本机的数据目录里（同一块硬盘，机器坏了会一起丢）")}><Badge tone="zinc">{L("本机磁盘")}</Badge></span>}
    {remote && <span title={L("已上传到设置里的 S3 兼容存储桶（异地副本，中控机器丢了也能恢复）")}><Badge tone="green">{L("异地存储桶")}</Badge></span>}
    {!local && !remote && <span className="text-dim" title={L("本机副本已轮换删除，也没有上传到存储桶")}>—</span>}
  </span>
}
