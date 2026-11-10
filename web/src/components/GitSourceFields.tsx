// Git source form parts beyond the basics: forge picker, SSH deploy key, polling and builds
// without a Dockerfile (docs/api.md "Builds from source").
import { useState } from 'react'
import { ChevronRight, KeyRound, X } from 'lucide-react'
import { Badge, Brand } from './ui'
import { Label, Select, TextInput } from './feedback'
import type { GitForm } from '../api/git'
import type { GitBuilder, GitProvider } from '../api/types'
import { useT } from '../i18n'
import { cx, si } from './ui-utils'

const icons: Record<GitProvider, { path: string; hex: string }> = { github: si.siGithub, gitlab: si.siGitlab, gitea: si.siGitea, git: si.siGit }

/** Forge logo; GitHub follows the text color so it works in both themes. */
export function ProviderIcon({ p, size = 16 }: { p: GitProvider; size?: number }) {
  return <Brand icon={icons[p]} size={size} color={p === 'github' ? 'currentColor' : undefined} />
}

/** GitHub / GitLab / Gitea / generic chips; '' follows the URL's host. */
export function GitProviderPicker({ value, inferred, onChange }: { value: GitProvider | ''; inferred: GitProvider; onChange: (p: GitProvider | '') => void }) {
  const { t } = useT()
  const cur = value || inferred
  const items: [GitProvider, string][] = [['github', 'GitHub'], ['gitlab', 'GitLab'], ['gitea', 'Gitea / Forgejo'], ['git', t('通用 Git', 'Any git')]]
  return (
    <div className="mb-4">
      <div className="flex flex-wrap items-center gap-2" role="radiogroup" aria-label={t('代码托管平台', 'Git host')}>
        {items.map(([k, l]) => (
          <button key={k} type="button" role="radio" aria-checked={cur === k} onClick={() => onChange(k === inferred && !value ? '' : k)}
            className={cx('h-8 px-3 rounded-md inline-flex items-center gap-2 text-[13px] border-2', cur === k ? 'border-[var(--edge)] nav-active font-semibold' : 'border-transparent field text-muted hover:text-fg')}>
            <ProviderIcon p={k} size={14} />{l}
          </button>
        ))}
        {value && <button type="button" onClick={() => onChange('')} className="text-[12px] text-dim hover:text-fg">{t('按地址自动判断', 'Detect from URL')}</button>}
      </div>
      <div className="text-[12px] text-dim mt-1.5">{t('决定 Webhook 的格式和默认 Token 用户名；不确定就按地址自动判断。', 'Picks the webhook format and the default token username; when unsure let the URL decide.')}</div>
    </div>
  )
}

/** Write-only deploy key + optional pinned known_hosts for SSH URLs. */
export function SshKeyField({ f, set, hasSshKey }: { f: GitForm; set: (f: GitForm) => void; hasSshKey?: boolean }) {
  const { t } = useT()
  return (
    <>
      <Label t={t('SSH 部署密钥（私有仓库）', 'SSH deploy key (private repo)')} d={t('只写不读：加密保存，构建时写入 0600 临时文件，用完即删。在平台上添加对应公钥为只读部署密钥。', 'Write-only: stored encrypted, written to a 0600 temp file during the fetch and deleted after. Add the public key on your git host as a read-only deploy key.')}>
        {hasSshKey && !f.sshKey && <div className="mb-2 flex items-center gap-2 text-[13px]">
          {f.clearSshKey
            ? <><Badge tone="red"><X size={11} />{t('保存后清除', 'Cleared on save')}</Badge><button type="button" onClick={() => set({ ...f, clearSshKey: false })} className="text-accent text-[12.5px]">{t('撤销', 'Undo')}</button></>
            : <><Badge tone="green"><KeyRound size={11} />{t('部署密钥已保存', 'Deploy key saved')}</Badge><button type="button" onClick={() => set({ ...f, clearSshKey: true })} className="text-[12.5px] text-muted hover:text-[#ef4444]">{t('清除', 'Clear')}</button></>}
        </div>}
        <textarea value={f.sshKey} onChange={e => set({ ...f, sshKey: e.target.value })} rows={4} spellCheck={false} autoComplete="off"
          placeholder={hasSshKey ? t('留空保持不变；粘贴则替换', 'Leave empty to keep; paste to replace') : '-----BEGIN OPENSSH PRIVATE KEY-----'}
          className="w-full rounded-lg field px-3 py-2 font-mono text-[12.5px] outline-none text-fg resize-y" />
      </Label>
      <Label t={t('known_hosts（可选）', 'known_hosts (optional)')} d={t('固定服务器主机密钥。留空则首次连接时记住它（TOFU），之后变化会拒绝。', "Pins the server's host key. Empty: the key seen on the first fetch is remembered (TOFU) and a later change is refused.")}>
        <textarea value={f.knownHosts} onChange={e => set({ ...f, knownHosts: e.target.value })} rows={2} spellCheck={false}
          placeholder="git.example.com ssh-ed25519 AAAA…" className="w-full rounded-lg field px-3 py-2 font-mono text-[12.5px] outline-none text-fg resize-y" />
      </Label>
    </>
  )
}

const POLLS: [number, string, string][] = [[0, '关闭', 'Off'], [60, '每分钟', 'Every minute'], [300, '每 5 分钟', 'Every 5 min'], [900, '每 15 分钟', 'Every 15 min'], [3600, '每小时', 'Hourly'], [86400, '每天', 'Daily']]

/** Collapsible: polling, builder and the commands of a generated Dockerfile. */
export function GitAdvancedFields({ f, set }: { f: GitForm; set: (f: GitForm) => void }) {
  const { t } = useT()
  const [open, setOpen] = useState(f.poll > 0 || f.builder !== 'auto' || !!(f.stack || f.install || f.build || f.start))
  const up = (k: 'install' | 'build' | 'start') => (e: React.ChangeEvent<HTMLInputElement>) => set({ ...f, [k]: e.target.value })
  const polls = POLLS.some(p => p[0] === f.poll) ? POLLS : [...POLLS, [f.poll, `${f.poll} 秒`, `${f.poll} s`] as [number, string, string]]
  return (
    <div className="border-2 border-[var(--edge)] rounded-lg mb-4 bg-card">
      <button type="button" onClick={() => setOpen(v => !v)} className="w-full flex items-center gap-2 h-10 px-3 text-[13.5px] font-semibold text-left">
        <ChevronRight size={15} className={cx('text-muted transition-transform', open && 'rotate-90')} />
        <span className="flex-1">{t('高级设置：轮询与构建方式', 'Advanced: polling and build mode')}</span>
      </button>
      {open && <div className="px-3 pb-1 pt-0.5">
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-x-4">
          <Label t={t('定时检查新提交', 'Poll for new commits')} d={t('Webhook 到不了面板时用', 'When webhooks cannot reach the panel')}>
            <Select className="w-full" value={f.poll} onChange={e => set({ ...f, poll: +e.target.value })}>{polls.map(([v, zh, en]) => <option key={v} value={v}>{t(zh, en)}</option>)}</Select>
          </Label>
          <Label t={t('构建方式', 'Build mode')}>
            <Select className="w-full" value={f.builder} onChange={e => set({ ...f, builder: e.target.value as GitBuilder })}>
              <option value="auto">{t('自动（有 Dockerfile 就用）', 'Auto (Dockerfile if present)')}</option>
              <option value="dockerfile">{t('只用 Dockerfile', 'Dockerfile only')}</option>
              <option value="generated">{t('总是自动生成', 'Always generate')}</option>
            </Select>
          </Label>
          <Label t={t('技术栈', 'Stack')}>
            <Select className="w-full" value={f.stack} disabled={f.builder === 'dockerfile'} onChange={e => set({ ...f, stack: e.target.value })}>
              <option value="">{t('自动识别', 'Detect')}</option>
              {['node', 'bun', 'python', 'go', 'static'].map(s => <option key={s} value={s}>{s}</option>)}
            </Select>
          </Label>
        </div>
        {f.builder !== 'dockerfile' && <>
          <div className="text-[12px] text-dim -mt-1 mb-3">{t('仓库没有 Dockerfile 时按下面的命令生成；留空则按项目文件自动推断（npm/pnpm/yarn、requirements.txt、go.mod…）。服务的第一个端口通过 $PORT 传给程序。', 'Used when the repo has no Dockerfile; empty fields are inferred from the project files (npm/pnpm/yarn, requirements.txt, go.mod…). The first port of the service reaches the app as $PORT.')}</div>
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-x-4">
            <Label t={t('安装命令', 'Install command')}><TextInput mono className="w-full" value={f.install} onChange={up('install')} placeholder="npm ci" /></Label>
            <Label t={t('构建命令', 'Build command')}><TextInput mono className="w-full" value={f.build} onChange={up('build')} placeholder="npm run build" /></Label>
            <Label t={t('启动命令', 'Start command')}><TextInput mono className="w-full" value={f.start} onChange={up('start')} placeholder="node server.js" /></Label>
          </div>
        </>}
      </div>}
    </div>
  )
}

