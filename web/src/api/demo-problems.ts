import { L } from '../i18n'
import type { NotificationChannel, NotificationSettings, Problem, Server } from './types'

/** Demo data for problems and notification settings (GET /problems, /settings/notifications). */
export function makeDemoProblems(servers: Server[], now: number, memberServers: () => string[] | undefined) {
  const off = servers.find(s => s.status === 'offline')
  const busy = servers.find(s => s.status === 'online')
  const problems: Problem[] = []
  if (off) problems.push({ id: 'prb_demo1', kind: 'node_offline', subject_id: off.id, subject_name: off.name, server_id: off.id, message: L("agent 已断开"), since: new Date(now - 14 * 60e3).toISOString(), firing_at: new Date(now - 12 * 60e3).toISOString() })
  if (busy) problems.push({ id: 'prb_demo2', kind: 'disk_high', subject_id: busy.id, subject_name: busy.name, server_id: busy.id, message: L("磁盘已用 92%（73.6 / 80.0 GB）"), since: new Date(now - 3 * 3600e3).toISOString(), firing_at: new Date(now - 3 * 3600e3 + 5 * 60e3).toISOString() })
  const notif: NotificationSettings = {
    notify_resolved: true, quiet_start: '23:00', quiet_end: '08:00', timezone: 'Asia/Shanghai',
    channels: [{ id: 'nch_demo1', type: 'telegram', name: L("运维群"), enabled: true, has_bot_token: true, chat_id: '-1001234567890' }],
  }
  let mine: NotificationChannel[] = [{ id: 'nch_me1', type: 'webhook', name: L("我的告警"), enabled: true, has_url: true }]
  return {
    route(method: string, m: (p: string) => string[] | null, body: any): unknown {
      // a member sees the problems of its servers only (memberSeesProblem on the server)
      if (method === 'GET' && m('problems')) { const ms = memberServers(); return ms ? problems.filter(p => p.server_id && ms.includes(p.server_id) && p.subject_id !== 'backup_repo') : problems }
      if (method === 'GET' && m('me/notifications')) return { channels: mine }
      if (method === 'PUT' && m('me/notifications')) {
        mine = ((body?.channels ?? []) as NotificationChannel[]).map((c, i) => ({ ...c, id: c.id || `nch_me${Date.now()}${i}`,
          has_url: !!(c.url || mine.find(o => o.id === c.id)?.has_url), url: undefined,
          has_bot_token: !!(c.bot_token || mine.find(o => o.id === c.id)?.has_bot_token), bot_token: undefined }))
        return { channels: mine }
      }
      if (method === 'POST' && m('me/notifications/test')) return { ok: true }
      if (method === 'GET' && m('settings/notifications')) return notif
      if (method === 'PUT' && m('settings/notifications')) {
        const n = body as NotificationSettings
        notif.channels = (n.channels ?? []).map((c, i) => ({ ...c, id: c.id || `nch_demo${Date.now()}${i}`,
          has_url: !!(c.url || notif.channels.find(o => o.id === c.id)?.has_url), url: undefined,
          has_bot_token: !!(c.bot_token || notif.channels.find(o => o.id === c.id)?.has_bot_token), bot_token: undefined,
          has_password: !!(c.password || notif.channels.find(o => o.id === c.id)?.has_password), password: undefined }))
        Object.assign(notif, { notify_resolved: n.notify_resolved, quiet_start: n.quiet_start, quiet_end: n.quiet_end, timezone: n.timezone })
        return notif
      }
      if (method === 'POST' && m('settings/notifications/test')) return { ok: true }
      return undefined
    },
  }
}
