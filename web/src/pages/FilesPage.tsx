import { L } from '../i18n'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import { ChevronLeft, ChevronRight, ChevronDown, Folder, FileText, FolderPlus, FilePlus, RefreshCw, Loader2, Save, AlertTriangle, FileCog, Undo2 } from 'lucide-react'
import { Button } from '../components/ui'
import { Empty, Skeleton } from '../components/feedback'
import { api } from '../api/client'
import { run, useBusy } from '../api/hooks'
import { bytes } from '../api/view'
import { cx } from '../components/ui-utils'
import { toast } from '../components/toast'
import { useProject } from '../api/queries'

type Entry = { name: string; dir: boolean }
type DirState = Entry[] | 'loading' | { error: string }

const join = (dir: string, name: string) => (dir === '/' ? '' : dir) + '/' + name
const parent = (p: string) => p.replace(/\/[^/]+\/?$/, '') || '/'

/** Run one command in the container; throws on agent/command error. */
async function sh(sid: string, cmd: string[]) {
  const r = await api.exec(sid, cmd)
  if (!r.ok) throw new Error(r.error || r.output || L("命令执行失败"))
  return r.output ?? ''
}

/** Largest file the editor opens or saves. */
const MAX_BYTES = 1 << 20
/** One exec argument stays far below Linux's 128 KiB per-argument limit (≤3 UTF-8 bytes per UTF-16 unit). */
const CHUNK = 32 * 1024

/** Write text to path in the container. Large files go in chunks to a temp file, then `cat` over the
 *  original (keeps its owner and mode). */
async function writeFile(sid: string, path: string, text: string) {
  if (text.length <= CHUNK) { await sh(sid, ['sh', '-c', 'printf "%s" "$1" > "$2"', 'sh', text, path]); return }
  const tmp = `/tmp/.atoll-edit-${Math.random().toString(36).slice(2, 10)}`
  for (let i = 0; i < text.length;) {
    let end = Math.min(text.length, i + CHUNK)
    if (end < text.length && /[\uD800-\uDBFF]/.test(text[end - 1])) end-- // never split a surrogate pair
    await sh(sid, ['sh', '-c', i === 0 ? 'printf "%s" "$1" > "$2"' : 'printf "%s" "$1" >> "$2"', 'sh', text.slice(i, end), tmp])
    i = end
  }
  await sh(sid, ['sh', '-c', 'cat "$1" > "$2" && rm -f "$1"', 'sh', tmp, path])
}

export default function FilesPage() {
  const { pid, sid = '' } = useParams()
  const s = useProject(pid).data?.services?.find(x => x.id === sid)
  const vols = useMemo(() => (s?.volumes ?? []).map(v => v.mount_path.replace(/\/$/, '')), [s])
  const isVol = (p: string) => vols.some(v => p === v || p.startsWith(v + '/'))
  // Atoll config-file mounts: read-only in the container, edited through the service's configs (redeploys)
  const configs = useMemo(() => s?.configs ?? [], [s])
  const cfgOf = (p?: string) => (p ? configs.find(c => c.path.replace(/\/+$/, '') === p) : undefined)
  const [params] = useSearchParams()
  const startPath = params.get('path') || ''

  const [root, setRoot] = useState<string>()
  const [rootErr, setRootErr] = useState<string>()
  const [dirs, setDirs] = useState<Record<string, DirState>>({})
  const [open, setOpen] = useState<Set<string>>(new Set())
  const [file, setFile] = useState<string>()
  const [orig, setOrig] = useState<string>()
  const [text, setText] = useState('')
  const [fileErr, setFileErr] = useState<string>()
  const [loadingFile, setLoadingFile] = useState(false)
  const [creating, setCreating] = useState<'file' | 'dir'>()
  const [newName, setNewName] = useState('')
  const [saving, wrapSave] = useBusy()

  const list = useCallback(async (dir: string) => {
    setDirs(d => ({ ...d, [dir]: 'loading' }))
    try {
      const out = await sh(sid, ['ls', '-1Ap', dir])
      const es = out.split('\n').filter(Boolean).map(n => ({ name: n.replace(/\/$/, ''), dir: n.endsWith('/') }))
      es.sort((a, b) => (a.dir === b.dir ? a.name.localeCompare(b.name) : a.dir ? -1 : 1))
      setDirs(d => ({ ...d, [dir]: es }))
    } catch (e) { setDirs(d => ({ ...d, [dir]: { error: (e as Error).message } })) }
  }, [sid])

  const start = useCallback(async () => {
    try {
      const r = startPath.replace(/\/+$/, '') || (await sh(sid, ['pwd'])).trim() || '/'
      setRootErr(undefined); setRoot(r); setOpen(new Set([r])); list(r)
    } catch (e) { setRootErr((e as Error).message) }
  }, [sid, list, startPath])
  // data fetch: state is only set after the request settles (the analyzer cannot see past the await)
  // oxlint-disable-next-line react/set-state-in-effect
  useEffect(() => { if (sid) start() }, [sid, start])

  const toggle = (p: string) => {
    setOpen(o => { const n = new Set(o); if (n.has(p)) n.delete(p); else { n.add(p); if (!Array.isArray(dirs[p])) list(p) } return n })
  }
  const openFile = async (p: string) => {
    if (dirty && !confirm(L("当前文件有未保存的修改，确定放弃？"))) return
    setFile(p); setFileErr(undefined); setLoadingFile(true)
    const cfg = cfgOf(p)
    if (cfg) { setOrig(cfg.content); setText(cfg.content); setLoadingFile(false); return }
    try {
      const n = parseInt((await sh(sid, ['sh', '-c', 'wc -c < "$1"', 'sh', p])).trim(), 10)
      if (n > MAX_BYTES) throw new Error(L("文件超过 1 MB（{0}），请用命令行编辑", bytes(n)))
      const c = await sh(sid, ['cat', p]); setOrig(c); setText(c)
    }
    catch (e) { setFileErr((e as Error).message); setOrig(undefined); setText('') }
    finally { setLoadingFile(false) }
  }
  const dirty = orig !== undefined && text !== orig
  const save = () => wrapSave(async () => {
    if (!file || !dirty) return
    if (new TextEncoder().encode(text).length > MAX_BYTES) { toast(L("文件超过 1 MB，无法在这里保存"), 'error'); return }
    const cfg = cfgOf(file)
    if (cfg) {
      if (!confirm(L("这是 Atoll 挂载的配置文件，保存后会重新部署服务。继续？"))) return
      const next = configs.map(c => (c === cfg ? { ...c, content: text } : c))
      const r = await run(() => api.updateService(sid, { configs: next }), { ok: L("配置已保存，正在重新部署"), invalidate: [`project:${pid}`, 'projects'] })
      if (r) setOrig(text)
      return
    }
    try { await writeFile(sid, file, text); setOrig(text); toast(L("已保存"), 'ok') }
    catch (e) { toast(L("保存失败：{0}", (e as Error).message), 'error') }
  })
  useEffect(() => {
    const k = (e: KeyboardEvent) => { if ((e.ctrlKey || e.metaKey) && e.key === 's') { e.preventDefault(); save() } }
    addEventListener('keydown', k); return () => removeEventListener('keydown', k)
  })

  const target = file ? parent(file) : root ?? '/'
  const create = async () => {
    const n = newName.trim(); if (!n || !creating) return
    const p = join(target, n)
    try {
      if (creating === 'dir') await sh(sid, ['mkdir', '-p', p]); else await writeFile(sid, p, '')
      setCreating(undefined); setNewName(''); setOpen(o => new Set(o).add(target)); await list(target)
      if (creating === 'file') openFile(p)
    } catch (e) { toast((e as Error).message, 'error') }
  }

  const renderDir = (dir: string, depth: number): React.ReactNode => {
    const st = dirs[dir]
    if (st === 'loading' || st === undefined) return <div style={{ paddingLeft: depth * 18 + 26 }} className="h-[30px] flex items-center"><Skeleton className="h-3 w-24" /></div>
    if (!Array.isArray(st)) return <div style={{ paddingLeft: depth * 18 + 26 }} className="py-1 text-[12px] text-[#ef4444] break-all">{st.error}</div>
    if (!st.length) return <div style={{ paddingLeft: depth * 18 + 26 }} className="h-[30px] flex items-center text-[12px] text-dim">{L("空目录")}</div>
    return st.map(e => {
      const p = join(dir, e.name), vol = isVol(p), isOpen = open.has(p)
      return (
        <div key={p}>
          <button onClick={() => (e.dir ? toggle(p) : openFile(p))} title={p}
            className={cx('w-full flex items-center gap-2 h-[30px] rounded-[4px] pr-2 text-left', file === p ? 'nav-active !shadow-none' : 'hover:bg-ov/5')} style={{ paddingLeft: depth * 18 + 4 }}>
            {e.dir ? (isOpen ? <ChevronDown size={14} className="text-muted shrink-0" /> : <ChevronRight size={14} className="text-muted shrink-0" />) : <span className="w-[14px] shrink-0" />}
            {e.dir ? <Folder size={15} className={cx('shrink-0', vol ? 'text-accent' : 'text-fg2')} fill="currentColor" /> : cfgOf(p) ? <FileCog size={15} className="text-[#f59e0b] shrink-0" /> : <FileText size={15} className="text-fg2 shrink-0" />}
            <span className={cx('truncate', vol ? 'text-accent font-semibold' : 'text-fg')}>{e.name}</span>
          </button>
          {e.dir && isOpen && renderDir(p, depth + 1)}
        </div>
      )
    })
  }

  const lines = text.split('\n').length
  return (
    <div className="h-full flex flex-col md:flex-row topline">
      <aside className="w-full md:w-[300px] lg:w-[340px] shrink-0 bg-side md:border-r border-line flex flex-col px-4 md:px-5 pt-6 md:pt-7 min-h-0 max-md:max-h-[45%] max-md:border-b-2 max-md:border-[var(--edge)]">
        <Link to={`/projects/${pid}/services/${sid}`} className="flex items-center gap-1.5 text-[13.5px] text-fg2 hover:text-fg min-w-0"><ChevronLeft size={16} className="shrink-0" /><span className="truncate">{L("返回")} {s?.name ?? ''}</span></Link>
        <div className="flex items-center mt-3 md:mt-5">
          <h1 className="text-[17px] font-extrabold italic tracking-[-0.02em] flex-1">{L("文件管理")}</h1>
          <span className="flex gap-1 text-muted">
            <button title={L("新建文件夹")} onClick={() => { setCreating('dir'); setNewName('') }} className="h-8 w-8 grid place-items-center rounded-[4px] hover:bg-ov/5 hover:text-fg"><FolderPlus size={16} /></button>
            <button title={L("新建文件")} onClick={() => { setCreating('file'); setNewName('') }} className="h-8 w-8 grid place-items-center rounded-[4px] hover:bg-ov/5 hover:text-fg"><FilePlus size={16} /></button>
            <button title={L("刷新")} onClick={() => { setDirs({}); [...open].forEach(list) }} className="h-8 w-8 grid place-items-center rounded-[4px] hover:bg-ov/5 hover:text-fg"><RefreshCw size={15} /></button>
          </span>
        </div>
        <div className="text-[12.5px] text-muted mt-1 mb-3 leading-5 max-md:hidden">{L("已挂载的卷以")}<span className="text-accent font-semibold">{L("紫色")}</span>{L("显示，重新部署后仍保留；其他位置的修改会在重新部署时丢失。")}</div>
        {configs.length > 0 && (
          <div className="mb-3">
            <div className="text-[11.5px] font-bold uppercase tracking-wide text-dim mb-1">{L("配置文件")}</div>
            {configs.map(c => (
              <button key={c.path} onClick={() => openFile(c.path.replace(/\/+$/, ''))} title={c.path}
                className={cx('w-full flex items-center gap-2 h-[30px] rounded-[4px] px-1 text-left font-mono text-[13px]', file === c.path ? 'nav-active !shadow-none' : 'hover:bg-ov/5')}>
                <FileCog size={15} className="text-[#f59e0b] shrink-0" /><span className="truncate">{c.path}</span>
              </button>
            ))}
          </div>
        )}
        {creating && (
          <div className="mb-3">
            <div className="text-[12px] text-dim mb-1.5 font-mono truncate">{L("在")} {target} {L("下新建")}{creating === 'dir' ? L("文件夹") : L("文件")}</div>
            <input autoFocus value={newName} onChange={e => setNewName(e.target.value)} onKeyDown={e => { if (e.key === 'Enter') create(); if (e.key === 'Escape') setCreating(undefined) }}
              placeholder={creating === 'dir' ? 'folder' : 'file.txt'} className="w-full h-9 rounded-lg field px-3 font-mono text-[13px] outline-none text-fg placeholder:text-dim" />
          </div>
        )}
        <div className="font-mono text-[13px] overflow-auto flex-1 pb-6 -mx-1 px-1">
          {rootErr ? <div className="text-[12.5px] text-[#ef4444] break-all font-sans">{L("无法读取容器文件：")}{rootErr}<div className="mt-2"><Button size="sm" onClick={start}><RefreshCw size={13} />{L("重试")}</Button></div></div>
            : !root ? <div className="space-y-2.5 pt-1">{[0, 1, 2, 3, 4].map(i => <Skeleton key={i} className="h-3.5" />)}</div>
            : <>
              <button onClick={() => toggle(root)} className="w-full flex items-center gap-2 h-[30px] px-1 rounded-[4px] hover:bg-ov/5 text-left">
                {open.has(root) ? <ChevronDown size={14} className="text-muted" /> : <ChevronRight size={14} className="text-muted" />}<Folder size={15} className="text-fg2" fill="currentColor" /><span className="font-semibold">{root}</span>
              </button>
              {open.has(root) && renderDir(root, 1)}
            </>}
        </div>
      </aside>
      <main className="flex-1 flex flex-col bg-bg min-w-0 min-h-0">
        <div className="min-h-14 py-2 border-b-2 border-[var(--edge)] flex items-center px-4 md:px-5 text-[13.5px] gap-3 shrink-0">
          {file ? <>
            <span className="font-mono truncate">{file}</span>
            {orig !== undefined && <span className="text-dim shrink-0 tnum">· {bytes(new TextEncoder().encode(text).length)}</span>}
            {cfgOf(file) ? <span className="text-[12px] text-[#f59e0b] font-semibold shrink-0">{L("· 配置文件，保存后会重新部署")}</span>
              : isVol(file) ? <span className="text-[12px] text-accent shrink-0">{L("· 卷内文件")}</span> : <span className="text-[12px] text-dim shrink-0 max-lg:hidden">{L("· 不在卷内，重新部署后恢复原样")}</span>}
            {dirty && <span className="h-2 w-2 rounded-full bg-[#f59e0b] shrink-0" title={L("未保存")} />}
            <span className="ml-auto" />
            <Button size="sm" disabled={!dirty || saving} onClick={() => setText(orig ?? '')}><Undo2 size={13} />{L("取消")}</Button>
            <Button size="sm" variant="primary" disabled={!dirty || saving} onClick={save}>{saving ? <Loader2 size={13} className="animate-spin" /> : <Save size={13} />}{L("保存")}</Button>
          </> : <span className="text-muted">{L("从左侧选择一个文件")}</span>}
        </div>
        {!file ? <div className="flex-1 flex items-center justify-center p-6 md:p-10 min-w-0 [&>*]:max-w-full"><Empty icon={<FileText size={22} />} title={L("浏览容器里的文件")} desc={L("通过容器内的 ls / cat 读取，保存时写回原路径（最大 1 MB）。Atoll 挂载的配置文件保存后会重新部署。大文件和二进制文件请用命令行。")} /></div>
          : loadingFile ? <div className="p-5 space-y-3">{[60, 40, 75, 30, 55].map((w, i) => <Skeleton key={i} className="h-3.5" style={{ width: `${w}%` }} />)}</div>
          : fileErr ? <div className="p-6 flex items-start gap-2 text-[13px] text-[#ef4444]"><AlertTriangle size={15} className="mt-0.5 shrink-0" /><span className="break-all">{fileErr}</span></div>
          : (
            <div className="flex-1 overflow-auto">
              <div className="flex min-h-full font-mono text-[13.5px] leading-7">
                <div className="w-12 shrink-0 text-right pr-3 pt-4 text-dim select-none border-r border-line bg-sunk/40">{Array.from({ length: lines }, (_, i) => <div key={i}>{i + 1}</div>)}</div>
                <textarea value={text} onChange={e => setText(e.target.value)} spellCheck={false} wrap="off" rows={lines}
                  className="flex-1 pt-4 px-4 bg-transparent outline-none resize-none overflow-y-hidden overflow-x-auto text-fg2 whitespace-pre" style={{ minHeight: '100%' }} />
              </div>
            </div>
          )}
      </main>
    </div>
  )
}
