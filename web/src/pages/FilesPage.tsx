import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { ChevronLeft, ChevronRight, ChevronDown, Folder, FileText, FolderPlus, FilePlus, RefreshCw, Loader2, Save, AlertTriangle } from 'lucide-react'
import { Button, cx } from '../components/ui'
import { Empty, Skeleton, toast } from '../components/feedback'
import { api } from '../api/client'
import { useBusy } from '../api/hooks'
import { bytes } from '../api/view'
import { useProject } from './ServicePage'

type Entry = { name: string; dir: boolean }
type DirState = Entry[] | 'loading' | { error: string }

const join = (dir: string, name: string) => (dir === '/' ? '' : dir) + '/' + name
const parent = (p: string) => p.replace(/\/[^/]+\/?$/, '') || '/'

/** Run one command in the container; throws on agent/command error. */
async function sh(sid: string, cmd: string[]) {
  const r = await api.exec(sid, cmd)
  if (!r.ok) throw new Error(r.error || r.output || '命令执行失败')
  return r.output ?? ''
}

export default function FilesPage() {
  const { pid, sid = '' } = useParams()
  const s = useProject(pid).data?.services?.find(x => x.id === sid)
  const vols = useMemo(() => (s?.volumes ?? []).map(v => v.mount_path.replace(/\/$/, '')), [s])
  const isVol = (p: string) => vols.some(v => p === v || p.startsWith(v + '/'))

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
    setRootErr(undefined)
    try {
      const r = (await sh(sid, ['pwd'])).trim() || '/'
      setRoot(r); setOpen(new Set([r])); list(r)
    } catch (e) { setRootErr((e as Error).message) }
  }, [sid, list])
  useEffect(() => { if (sid) start() }, [sid, start])

  const toggle = (p: string) => {
    setOpen(o => { const n = new Set(o); if (n.has(p)) n.delete(p); else { n.add(p); if (!Array.isArray(dirs[p])) list(p) } return n })
  }
  const openFile = async (p: string) => {
    if (dirty && !confirm('当前文件有未保存的修改，确定放弃？')) return
    setFile(p); setFileErr(undefined); setLoadingFile(true)
    try { const c = await sh(sid, ['cat', p]); setOrig(c); setText(c) }
    catch (e) { setFileErr((e as Error).message); setOrig(undefined); setText('') }
    finally { setLoadingFile(false) }
  }
  const dirty = orig !== undefined && text !== orig
  const save = () => wrapSave(async () => {
    if (!file || !dirty) return
    try { await sh(sid, ['sh', '-c', 'printf "%s" "$1" > "$2"', 'sh', text, file]); setOrig(text); toast('已保存', 'ok') }
    catch (e) { toast(`保存失败：${(e as Error).message}`, 'error') }
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
      await sh(sid, creating === 'dir' ? ['mkdir', '-p', p] : ['sh', '-c', 'printf "%s" "$1" > "$2"', 'sh', '', p])
      setCreating(undefined); setNewName(''); setOpen(o => new Set(o).add(target)); await list(target)
      if (creating === 'file') openFile(p)
    } catch (e) { toast((e as Error).message, 'error') }
  }

  const renderDir = (dir: string, depth: number): React.ReactNode => {
    const st = dirs[dir]
    if (st === 'loading' || st === undefined) return <div style={{ paddingLeft: depth * 18 + 26 }} className="h-[30px] flex items-center"><Skeleton className="h-3 w-24" /></div>
    if (!Array.isArray(st)) return <div style={{ paddingLeft: depth * 18 + 26 }} className="py-1 text-[12px] text-[#ef4444] break-all">{st.error}</div>
    if (!st.length) return <div style={{ paddingLeft: depth * 18 + 26 }} className="h-[30px] flex items-center text-[12px] text-dim">空目录</div>
    return st.map(e => {
      const p = join(dir, e.name), vol = isVol(p), isOpen = open.has(p)
      return (
        <div key={p}>
          <button onClick={() => (e.dir ? toggle(p) : openFile(p))} title={p}
            className={cx('w-full flex items-center gap-2 h-[30px] rounded-[4px] pr-2 text-left', file === p ? 'nav-active !shadow-none' : 'hover:bg-ov/5')} style={{ paddingLeft: depth * 18 + 4 }}>
            {e.dir ? (isOpen ? <ChevronDown size={14} className="text-muted shrink-0" /> : <ChevronRight size={14} className="text-muted shrink-0" />) : <span className="w-[14px] shrink-0" />}
            {e.dir ? <Folder size={15} className={cx('shrink-0', vol ? 'text-accent' : 'text-fg2')} fill="currentColor" /> : <FileText size={15} className="text-fg2 shrink-0" />}
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
        <Link to={`/projects/${pid}/services/${sid}`} className="flex items-center gap-1.5 text-[13.5px] text-fg2 hover:text-fg min-w-0"><ChevronLeft size={16} className="shrink-0" /><span className="truncate">返回 {s?.name ?? ''}</span></Link>
        <div className="flex items-center mt-3 md:mt-5">
          <h1 className="text-[17px] font-extrabold italic tracking-[-0.02em] flex-1">文件管理</h1>
          <span className="flex gap-1 text-muted">
            <button title="新建文件夹" onClick={() => { setCreating('dir'); setNewName('') }} className="h-8 w-8 grid place-items-center rounded-[4px] hover:bg-ov/5 hover:text-fg"><FolderPlus size={16} /></button>
            <button title="新建文件" onClick={() => { setCreating('file'); setNewName('') }} className="h-8 w-8 grid place-items-center rounded-[4px] hover:bg-ov/5 hover:text-fg"><FilePlus size={16} /></button>
            <button title="刷新" onClick={() => { setDirs({}); [...open].forEach(list) }} className="h-8 w-8 grid place-items-center rounded-[4px] hover:bg-ov/5 hover:text-fg"><RefreshCw size={15} /></button>
          </span>
        </div>
        <div className="text-[12.5px] text-muted mt-1 mb-3 leading-5 max-md:hidden">已挂载的卷以<span className="text-accent font-semibold">紫色</span>显示，重新部署后仍保留；其他位置的修改会在重新部署时丢失。</div>
        {creating && (
          <div className="mb-3">
            <div className="text-[12px] text-dim mb-1.5 font-mono truncate">在 {target} 下新建{creating === 'dir' ? '文件夹' : '文件'}</div>
            <input autoFocus value={newName} onChange={e => setNewName(e.target.value)} onKeyDown={e => { if (e.key === 'Enter') create(); if (e.key === 'Escape') setCreating(undefined) }}
              placeholder={creating === 'dir' ? 'folder' : 'file.txt'} className="w-full h-9 rounded-lg field px-3 font-mono text-[13px] outline-none text-fg placeholder:text-dim" />
          </div>
        )}
        <div className="font-mono text-[13px] overflow-auto flex-1 pb-6 -mx-1 px-1">
          {rootErr ? <div className="text-[12.5px] text-[#ef4444] break-all font-sans">无法读取容器文件：{rootErr}<div className="mt-2"><Button size="sm" onClick={start}><RefreshCw size={13} />重试</Button></div></div>
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
            {isVol(file) ? <span className="text-[12px] text-accent shrink-0">· 卷内文件</span> : <span className="text-[12px] text-dim shrink-0 max-lg:hidden">· 不在卷内，重新部署后恢复原样</span>}
            {dirty && <span className="h-2 w-2 rounded-full bg-[#f59e0b] shrink-0" title="未保存" />}
            <span className="ml-auto" />
            <Button size="sm" variant="primary" disabled={!dirty || saving} onClick={save}>{saving ? <Loader2 size={13} className="animate-spin" /> : <Save size={13} />}保存</Button>
          </> : <span className="text-muted">从左侧选择一个文件</span>}
        </div>
        {!file ? <div className="flex-1 flex items-center justify-center p-6 md:p-10 min-w-0 [&>*]:max-w-full"><Empty icon={<FileText size={22} />} title="浏览容器里的文件" desc="通过容器内的 ls / cat 读取，保存时写回原路径。适合改配置文件，大文件和二进制文件请用命令行。" /></div>
          : loadingFile ? <div className="p-5 space-y-3">{[60, 40, 75, 30, 55].map((w, i) => <Skeleton key={i} className="h-3.5" style={{ width: `${w}%` }} />)}</div>
          : fileErr ? <div className="p-6 flex items-start gap-2 text-[13px] text-[#ef4444]"><AlertTriangle size={15} className="mt-0.5 shrink-0" /><span className="break-all">{fileErr}</span></div>
          : (
            <div className="flex-1 overflow-auto">
              <div className="flex min-h-full font-mono text-[13.5px] leading-7">
                <div className="w-12 shrink-0 text-right pr-3 pt-4 text-dim select-none border-r border-line bg-sunk/40">{Array.from({ length: lines }, (_, i) => <div key={i}>{i + 1}</div>)}</div>
                <textarea value={text} onChange={e => setText(e.target.value)} spellCheck={false} wrap="off" rows={lines}
                  className="flex-1 pt-4 px-4 bg-transparent outline-none resize-none overflow-hidden text-fg2 whitespace-pre" style={{ minHeight: '100%' }} />
              </div>
            </div>
          )}
      </main>
    </div>
  )
}
