import { useState } from 'react'
import { Cpu, Loader2, Save } from 'lucide-react'
import { Button, Card, Hint } from './ui'
import { TextInput } from './feedback'
import { api } from '../api/client'
import { run, useBusy } from '../api/hooks'
import type { Settings } from '../api/types'
import { useT } from '../i18n'
import { KEEP_NETWORK } from './net'

/** Settings → 默认资源上限: limits applied to new services (template, image, git, import). */
export default function ResourceDefaultsSettings({ s }: { s: Settings }) {
  const { t } = useT()
  const curMem = s.default_mem_limit_mb ?? 1024, curCpu = s.default_cpu_limit ?? 0
  const [mem, setMem] = useState(curMem ? String(curMem) : '')
  const [cpu, setCpu] = useState(curCpu ? String(curCpu) : '')
  const [busy, wrap] = useBusy()
  const m = mem.trim() === '' ? 0 : Number(mem), c = cpu.trim() === '' ? 0 : Number(cpu)
  const memOk = Number.isInteger(m) && (m === 0 || (m >= 6 && m <= 1048576))
  const cpuOk = Number.isFinite(c) && c >= 0 && c <= 1024
  const dirty = m !== curMem || c !== curCpu
  // PUT /settings replaces every field: send the current values back (write-only secrets are "" = keep).
  const save = () => wrap(() => run(() => api.putSettings({ ...s, ...KEEP_NETWORK, default_mem_limit_mb: m, default_cpu_limit: c }),
    { ok: t('默认资源上限已保存', 'Default limits saved'), invalidate: ['settings'] }))
  return (
    <div className="mt-8 mb-8">
      <div className="text-[15px] font-semibold flex items-center gap-2 mb-3"><Cpu size={17} />{t('默认资源上限', 'Default resource limits')}<Hint>{t(
        '新建服务（模板数据库、镜像、源码构建、Zeabur 导入）没有填写上限时使用这里的值。留空或填 0 表示不限。只影响之后新建的服务，已有服务不变，可在服务的「服务状态」里一键套用。',
        'Used when a new service (database template, image, git build, Zeabur import) is created without its own limits. Empty or 0 means no limit. Only services created from now on get them; existing services are unchanged (apply them from a service\'s Status tab).')}</Hint></div>
      <Card className="divide-y divide-line">
        <div className="px-4 @2xl:px-6 py-5 grid grid-cols-1 @3xl:grid-cols-[260px_minmax(0,1fr)] gap-3 @3xl:gap-6 items-center">
          <div><div className="text-[13px] font-semibold">{t('内存', 'Memory')}</div><div className="text-[12.5px] text-muted mt-1">{t('MB，默认 1024；超出后容器被 OOM 重启', 'MB, default 1024; the container is OOM-restarted above it')}</div></div>
          <div className="flex flex-wrap items-center gap-3">
            <TextInput mono type="number" min={0} className="w-[140px]" value={mem} onChange={e => setMem(e.target.value)} placeholder={t('不限', 'none')} />
            <span className="text-[13px] text-muted">MB</span>
            {!memOk && <span className="text-[12px] text-[#ef4444]">{t('填 0（不限）或 6 到 1048576 之间的整数', 'Enter 0 (no limit) or a whole number from 6 to 1048576')}</span>}
          </div>
        </div>
        <div className="px-4 @2xl:px-6 py-5 grid grid-cols-1 @3xl:grid-cols-[260px_minmax(0,1fr)] gap-3 @3xl:gap-6 items-center">
          <div><div className="text-[13px] font-semibold">CPU</div><div className="text-[12.5px] text-muted mt-1">{t('核，可填小数，默认不限', 'Cores, decimals allowed, default no limit')}</div></div>
          <div className="flex flex-wrap items-center gap-3">
            <TextInput mono type="number" min={0} step="0.25" className="w-[140px]" value={cpu} onChange={e => setCpu(e.target.value)} placeholder={t('不限', 'none')} />
            <span className="text-[13px] text-muted">{t('核', 'cores')}</span>
            {!cpuOk && <span className="text-[12px] text-[#ef4444]">{t('填 0（不限）到 1024 之间的数', 'Enter a number from 0 (no limit) to 1024')}</span>}
            <Button className="ml-auto" size="sm" variant="primary" disabled={!memOk || !cpuOk || !dirty || busy} onClick={save}>{busy ? <Loader2 size={14} className="animate-spin" /> : <Save size={14} />}{t('保存', 'Save')}</Button>
          </div>
        </div>
      </Card>
    </div>
  )
}
