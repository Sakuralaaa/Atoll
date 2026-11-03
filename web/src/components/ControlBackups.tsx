// 中控 → 备份: encrypted control bundles (local keep 3 + bucket), run now, download, export with a
// one-time passphrase, and the passphrase scheduled bundles use (shown/changed after re-auth).
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Archive, Download, FileLock2, KeyRound, Eye, EyeOff, Loader2, Play, AlertTriangle, CalendarClock, HardDrive, Cloud } from 'lucide-react'
import { Badge, Button, Card } from './ui'
import { CopyButton, Empty, ErrorState, Modal, RowsSkeleton, TextInput } from './feedback'
import { controlApi, downloadBackup, exportBundle, MIN_PASSPHRASE, passphraseFromText, pickExportFile, triggerLabel } from '../api/control'
import { run, useBusy, useQuery } from '../api/hooks'
import { ago, bytes, dateTime } from '../api/view'
import type { ApiError } from '../api/client'
import type { ControlBackup, ControlPassphrase, ControlStatus } from '../api/types'
import { Where } from './control-ui'
import { useReauth } from './useReauth'

export function BackupsTab({ s }: { s: ControlStatus }) {
  const q = useQuery('control-backups', controlApi.backups, { interval: 60000 })
  const { guard, ensure, dialog } = useReauth()
  const [running, wrap] = useBusy()
  const [exporting, setExporting] = useState(false)
  const [changing, setChanging] = useState(false)
  const [shown, setShown] = useState<ControlPassphrase>()
  const l = q.data
  const runNow = () => wrap(() => run(() => controlApi.runBackup(), { ok: '中控备份完成', invalidate: ['control-backups', 'control'] }))
  const download = (b: ControlBackup) => guard(() => downloadBackup(b.id), { title: '下载中控备份', desc: '下载中控备份需要确认身份。' })
  const openExport = async () => { if (await ensure('导出加密包', '导出中控备份需要确认身份。')) setExporting(true) }
  const openChange = async () => { if (await ensure('更换备份口令', '更换备份口令需要确认身份。')) setChanging(true) }
  const reveal = async () => { const r = await guard(() => controlApi.passphrase(), { title: '查看备份口令', desc: '查看备份口令需要确认身份。' }); if (r) setShown(r) }
  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center gap-3">
        <h2 className="text-[18px] font-semibold tracking-tight mr-auto">中控备份</h2>
        <Button onClick={runNow} disabled={running}>{running ? <Loader2 size={15} className="animate-spin" /> : <Play size={15} />}立即备份</Button>
        <Button variant="primary" onClick={openExport}><FileLock2 size={15} />导出加密包</Button>
      </div>
      <div className="text-[13px] text-muted leading-6 -mt-3 max-w-[820px]">备份包含数据库快照、<span className="font-mono">master.key</span>、<span className="font-mono">server.env</span> 和 Caddyfile，用口令加密（scrypt + AES-256-GCM）。没有口令就打不开。</div>

      {/* passphrase: prominent, the one thing that makes bucket backups usable after losing the box */}
      <div className="rounded-[5px] border-2 border-[var(--edge)] shadow-[4px_4px_0_0_var(--edge)] bg-[#fef3c7] text-[#141414] p-5 @2xl:p-6">
        <div className="flex items-start gap-3">
          <AlertTriangle size={20} className="shrink-0 mt-0.5" />
          <div className="min-w-0 flex-1">
            <div className="text-[15px] font-extrabold italic tracking-[-0.02em]">把备份口令存到中控以外的地方</div>
            <div className="text-[13px] leading-6 mt-1">中控机器整台坏掉时，只能靠这个口令解开存储桶里的备份；<b>丢了口令就等于丢了备份</b>。请存进密码管理器，不要只放在这台服务器上。</div>
            <div className="text-[12.5px] mt-2">定时备份当前使用：<b>{l ? passphraseFromText[l.passphrase_from] ?? l.passphrase_from : '…'}</b></div>
            {shown && <div className="mt-3 rounded-lg border-2 border-[var(--edge)] bg-white/70 px-3 py-2 flex flex-wrap items-center gap-2">
              <span className="font-mono text-[13.5px] font-semibold min-w-0 flex-1 [overflow-wrap:anywhere]">{shown.passphrase}</span>
              <CopyButton text={shown.passphrase} />
            </div>}
            <div className="mt-4 flex flex-wrap gap-2">
              {shown ? <Button size="sm" onClick={() => setShown(undefined)}><EyeOff size={14} />隐藏</Button> : <Button size="sm" onClick={reveal}><Eye size={14} />显示口令</Button>}
              <Button size="sm" onClick={openChange}><KeyRound size={14} />更换口令</Button>
            </div>
          </div>
        </div>
      </div>

      {q.error && !l && <ErrorState error={q.error} onRetry={q.refetch} />}
      {!l && !q.error && <RowsSkeleton n={4} />}
      {l && <>
        <div className="grid grid-cols-1 @3xl:grid-cols-3 gap-4 [&>*]:min-w-0">
          <Card className="p-5"><div className="text-[12.5px] text-muted flex items-center gap-1.5"><CalendarClock size={14} />定时备份</div><div className="mt-1.5 text-[14px] font-semibold">每天一次</div><div className="text-[12.5px] text-dim mt-0.5">{l.next_scheduled ? `下次 ${dateTime(l.next_scheduled)}` : '—'}</div></Card>
          <Card className="p-5"><div className="text-[12.5px] text-muted flex items-center gap-1.5"><HardDrive size={14} />本地</div><div className="mt-1.5 text-[14px] font-semibold">保留最近 {l.keep_local} 份</div><div className="text-[12.5px] text-dim mt-0.5 font-mono [overflow-wrap:anywhere]">{s.data_dir.replace(/\/$/, '')}/control-backups</div></Card>
          <Card className="p-5"><div className="text-[12.5px] text-muted flex items-center gap-1.5"><Cloud size={14} />存储桶</div>
            {l.remote_enabled ? <><div className="mt-1.5 text-[14px] font-semibold">保留最近 {l.keep_remote} 份</div><div className="text-[12.5px] text-dim mt-0.5">与服务备份同一个 bucket，<span className="font-mono">&lt;前缀&gt;/control/</span></div></>
              : <><div className="mt-1.5 text-[14px] font-semibold">未配置</div><div className="text-[12.5px] text-dim mt-0.5">只有本地副本。在 <Link to="/settings" className="text-accent hover:underline">设置 → 备份</Link> 配置 S3 / R2 后自动上传。</div></>}
          </Card>
        </div>
        {l.remote_error && <div className="rounded-lg border-2 border-[var(--edge)] bg-[#fca5a5] text-[#141414] px-3.5 py-2.5 text-[12.5px] font-semibold flex items-start gap-2 [overflow-wrap:anywhere]"><AlertTriangle size={14} className="mt-0.5 shrink-0" /><span className="min-w-0">存储桶：{l.remote_error}</span></div>}
        {!l.backups.length ? <Empty icon={<Archive size={22} />} title="还没有中控备份" desc="每天自动备份一次；也可以现在就做一份。" action={<Button variant="primary" onClick={runNow}><Play size={15} />立即备份</Button>} /> : (
          <Card className="overflow-hidden">
            <div className="hidden @4xl:grid grid-cols-[minmax(0,1.5fr)_80px_90px_90px_minmax(0,1fr)_minmax(0,1fr)_92px] gap-4 px-6 h-11 items-center text-[12.5px] text-muted border-b-2 border-[var(--edge)]"><span>时间</span><span>触发</span><span>版本</span><span>大小</span><span>位置</span><span>SHA256</span><span /></div>
            {l.backups.map(b => (
              <div key={b.id} className="flex flex-wrap @4xl:grid @4xl:grid-cols-[minmax(0,1.5fr)_80px_90px_90px_minmax(0,1fr)_minmax(0,1fr)_92px] gap-x-4 gap-y-1.5 px-4 @2xl:px-6 py-3 items-center text-[13px] border-b border-line last:border-0">
                <div className="min-w-0 @max-4xl:flex-1"><div className="font-semibold">{dateTime(b.created_at)} <span className="text-dim font-normal text-[12px]">· {ago(b.created_at)}</span></div><div className="font-mono text-[11.5px] text-dim truncate" title={b.remote_key || b.id}>{b.id}</div></div>
                <span className="@max-4xl:order-2"><Badge tone={b.trigger === 'pre-update' ? 'violet' : b.trigger === 'manual' ? 'amber' : 'zinc'}>{triggerLabel[b.trigger] ?? b.trigger}</Badge></span>
                <span className="font-mono text-[12.5px] @max-4xl:order-2">{b.version}</span>
                <span className="tnum text-fg2 @max-4xl:order-2">{bytes(b.size_bytes)}</span>
                <span className="@max-4xl:order-2 flex @4xl:[&>span]:justify-start"><Where local={b.local} remote={b.remote} /></span>
                <span className="font-mono text-[12px] text-muted truncate @max-4xl:order-3 @max-4xl:basis-full" title={b.sha256}>{b.sha256 ? `${b.sha256.slice(0, 16)}…` : '—'}</span>
                <span className="flex justify-end @max-4xl:order-1"><Button size="sm" onClick={() => download(b)}><Download size={14} />下载</Button></span>
                {b.error && <div className="basis-full @4xl:col-span-7 order-4 text-[12px] text-[#f59e0b] font-semibold [overflow-wrap:anywhere]">{b.error}</div>}
              </div>
            ))}
          </Card>
        )}
      </>}
      {exporting && <ExportDialog host={s.host.hostname} guard={guard} onClose={() => setExporting(false)} />}
      {changing && <ChangePassphrase guard={guard} onClose={() => setChanging(false)} onDone={r => { setShown(r); setChanging(false); q.refetch() }} />}
      {dialog}
    </div>
  )
}

type Guard = ReturnType<typeof useReauth>['guard']

function PassFields({ a, b, setA, setB }: { a: string; b: string; setA: (v: string) => void; setB: (v: string) => void }) {
  const short = a.length > 0 && a.length < MIN_PASSPHRASE
  const mismatch = b.length > 0 && a !== b
  return (
    <>
      <label className="block mt-4">
        <div className="text-[13px] font-semibold mb-1.5">口令</div>
        <TextInput autoFocus type="password" autoComplete="new-password" className="w-full" value={a} onChange={e => setA(e.target.value)} placeholder={`至少 ${MIN_PASSPHRASE} 个字符`} />
      </label>
      <label className="block mt-4">
        <div className="text-[13px] font-semibold mb-1.5">确认口令</div>
        <TextInput type="password" autoComplete="new-password" className="w-full" value={b} onChange={e => setB(e.target.value)} />
      </label>
      {(short || mismatch) && <div className="mt-2 text-[12.5px] text-muted font-semibold">{short ? `口令至少 ${MIN_PASSPHRASE} 个字符（当前 ${a.length}）` : '两次输入的口令不一致'}</div>}
    </>
  )
}
const passOK = (a: string, b: string) => a.length >= MIN_PASSPHRASE && a === b

function ExportDialog({ host, guard, onClose }: { host: string; guard: Guard; onClose: () => void }) {
  const [a, setA] = useState(''); const [b, setB] = useState('')
  const [busy, setBusy] = useState(false); const [got, setGot] = useState(0); const [err, setErr] = useState<string>()
  const ok = passOK(a, b) && !busy
  const go = async () => {
    if (!ok) return
    setErr(undefined)
    const d = new Date(), p = (n: number) => String(n).padStart(2, '0')
    const name = `atoll-control-${host.replace(/[^A-Za-z0-9._-]/g, '_')}-${d.getUTCFullYear()}${p(d.getUTCMonth() + 1)}${p(d.getUTCDate())}T${p(d.getUTCHours())}${p(d.getUTCMinutes())}${p(d.getUTCSeconds())}Z.atollbak`
    // first thing in the click: the save picker needs the user gesture (streams to disk where supported)
    const sink = await pickExportFile(name)
    if (sink === null) return
    setBusy(true); setGot(0)
    try {
      const r = await guard(() => exportBundle(a, sink, setGot, name), { title: '导出加密包', rethrow: true })
      if (r) onClose()
    } catch (x) { setErr((x as ApiError).message || '导出失败') } finally { setBusy(false) }
  }
  return (
    <Modal width={500} title={<><FileLock2 size={17} className="text-accent" />导出加密包</>} onClose={() => !busy && onClose()}
      footer={<><Button size="sm" disabled={busy} onClick={onClose}>取消</Button><Button size="sm" variant="primary" disabled={!ok} onClick={go}>{busy ? <Loader2 size={14} className="animate-spin" /> : <Download size={14} />}{busy ? `正在导出 ${bytes(got)}` : '导出并下载'}</Button></>}>
      <form onSubmit={e => { e.preventDefault(); go() }}>
        <div className="text-[13.5px] text-fg2 leading-6">现场生成一个新的中控备份包，用你这次输入的口令加密。<b>口令不会被保存</b>，请先存进密码管理器——忘了就打不开这个包。</div>
        <PassFields a={a} b={b} setA={setA} setB={setB} />
        {err && <div role="alert" className="mt-3 text-[12.5px] text-[#ef4444] font-semibold flex items-start gap-1.5 [overflow-wrap:anywhere]"><AlertTriangle size={14} className="mt-0.5 shrink-0" />{err}</div>}
        <div className="mt-4 text-[12px] text-dim leading-5">用于迁移到新机器：<span className="font-mono">install-server.sh --restore 文件 --restore-passphrase-file 口令文件</span>（见「迁移」）。</div>
        <button type="submit" hidden />
      </form>
    </Modal>
  )
}

function ChangePassphrase({ guard, onClose, onDone }: { guard: Guard; onClose: () => void; onDone: (r: ControlPassphrase) => void }) {
  const [a, setA] = useState(''); const [b, setB] = useState('')
  const [busy, setBusy] = useState(false); const [err, setErr] = useState<string>()
  const ok = passOK(a, b) && !busy
  const go = async () => {
    if (!ok) return
    setBusy(true); setErr(undefined)
    try { const r = await guard(() => controlApi.passphrase(a), { title: '更换备份口令', rethrow: true }); if (r) onDone(r) }
    catch (x) { setErr((x as ApiError).message || '更换失败') } finally { setBusy(false) }
  }
  return (
    <Modal width={500} title={<><KeyRound size={17} className="text-accent" />更换备份口令</>} onClose={onClose}
      footer={<><Button size="sm" onClick={onClose}>取消</Button><Button size="sm" variant="primary" disabled={!ok} onClick={go}>{busy && <Loader2 size={14} className="animate-spin" />}保存新口令</Button></>}>
      <form onSubmit={e => { e.preventDefault(); go() }}>
        <div className="text-[13.5px] text-fg2 leading-6">之后的定时和手动备份都用新口令加密。<b>已有的备份仍然只能用旧口令打开</b>，旧口令也请继续保留到这些备份过期。</div>
        <PassFields a={a} b={b} setA={setA} setB={setB} />
        {err && <div role="alert" className="mt-3 text-[12.5px] text-[#ef4444] font-semibold flex items-start gap-1.5 [overflow-wrap:anywhere]"><AlertTriangle size={14} className="mt-0.5 shrink-0" />{err}</div>}
        <button type="submit" hidden />
      </form>
    </Modal>
  )
}
