import { L } from '../i18n'

/** Demo: a fake container filesystem behind POST /services/{id}/exec (mail-relay's files). */
export const CONFIG = L("# mail-relay 配置\nserver:\n  host: 0.0.0.0\n  port: 5000\n  public_url: https://mail.example.com\n\ndatabase:\n  path: /app/data/mail.db\n\nscheduler:\n  forward_interval: 300   # 秒\n  timezone: Asia/Shanghai\n\nforward:\n  channels: []            # telegram / webhook\n\nlog:\n  level: INFO\n  dir: /app/logs")
const FS: Record<string, string[] | string> = {
  '/': ['app/', 'bin/', 'etc/', 'tmp/', 'usr/'],
  '/app': ['.env.example', '.github/', 'VERSION', 'data/', 'docker-compose.yml', 'docs/', 'static/', 'main.py', 'config.yaml', 'requirements.txt', 'scripts/', 'start.sh', 'templates/', 'logs/'],
  '/app/data': ['mail.db', 'accounts.json'], '/app/logs': ['app.log'], '/app/.github': ['workflows/'], '/app/docs': ['README.md'],
  '/app/static': ['app.js', 'style.css'], '/app/scripts': ['migrate.sh'], '/app/templates': ['index.html', 'login.html'],
  '/app/config.yaml': CONFIG, '/app/VERSION': 'v2.3.1\n', '/app/.env.example': 'SECRET_KEY=\nADMIN_PASSWORD=\n', '/app/start.sh': '#!/bin/sh\nexec python main.py\n',
  '/app/requirements.txt': 'flask==3.1.0\napscheduler==3.11.0\nrequests==2.32.3\n', '/app/main.py': 'from app import create_app\n\napp = create_app()\n',
}
export function exec(cmd: string[]): { ok: boolean; output?: string; error?: string } {
  const script = cmd[0] === 'sh' && cmd[1] === '-c' ? cmd[2] : cmd.join(' ')
  if (script === 'pwd') return { ok: true, output: '/app\n' }
  const target = cmd[cmd.length - 1]
  if (script.startsWith('ls')) {
    const d = FS[target.replace(/\/$/, '') || '/']
    return Array.isArray(d) ? { ok: true, output: d.join('\n') + '\n' } : Array.isArray(FS[target]) ? { ok: true, output: (FS[target] as string[]).join('\n') } : { ok: true, output: '' }
  }
  if (script.startsWith('cat') || script.startsWith('head')) {
    const f = FS[target]
    return typeof f === 'string' ? { ok: true, output: f } : { ok: true, output: `# ${target}\n` }
  }
  if (script.startsWith('wc')) { const f = FS[target]; return { ok: true, output: `${typeof f === 'string' ? new TextEncoder().encode(f).length : 0}\n` } }
  if (script.startsWith('cat "$1" >')) { FS[cmd[5]] = String(FS[cmd[4]] ?? ''); delete FS[cmd[4]]; return { ok: true, output: '' } }
  if (script.startsWith('printf "%s" "$1" >>')) { const path = cmd[5]; FS[path] = String(FS[path] ?? '') + cmd[4]; return { ok: true, output: '' } }
  if (script.startsWith('printf')) {
    const path = cmd[5], dir = path.replace(/\/[^/]+$/, '') || '/', name = path.slice(dir.length + (dir === '/' ? 0 : 1))
    if (!(path in FS) && Array.isArray(FS[dir])) (FS[dir] as string[]).push(name)
    FS[path] = cmd[4]; return { ok: true, output: '' }
  }
  if (cmd[0] === 'mkdir') {
    const path = target.replace(/\/$/, ''), dir = path.replace(/\/[^/]+$/, '') || '/', name = path.slice(dir.length + (dir === '/' ? 0 : 1))
    if (!(path in FS)) { FS[path] = []; if (Array.isArray(FS[dir])) (FS[dir] as string[]).push(name + '/') }
    return { ok: true, output: '' }
  }
  return { ok: true, output: `$ ${cmd.join(' ')}\n(demo) ok\n` }
}
