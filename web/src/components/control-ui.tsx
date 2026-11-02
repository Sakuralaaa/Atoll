// Small display pieces shared by the 中控 page and its tabs.
import { Badge } from './ui'

export function KV({ k, v }: { k: string; v: React.ReactNode }) {
  return <div className="flex items-start justify-between gap-4 min-h-10 py-2.5 text-[13px] leading-5"><span className="text-muted shrink-0 whitespace-nowrap">{k}</span><span className="text-fg min-w-0 text-right [overflow-wrap:anywhere]">{v}</span></div>
}

/** Where a control backup lives: local copy and/or the bucket. */
export function Where({ local, remote }: { local: boolean; remote: boolean }) {
  return <span className="inline-flex flex-wrap justify-end gap-1.5">{local && <Badge tone="zinc">本地</Badge>}{remote && <Badge tone="green">存储桶</Badge>}{!local && !remote && <span className="text-dim">—</span>}</span>
}
