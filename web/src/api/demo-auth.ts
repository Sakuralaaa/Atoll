import { ApiError } from './client'
import type { SessionInfo } from './types'
import type { makeDemoUsers } from './demo-users'

/** Demo: /auth/* and /me/* (login, 2FA, password, sessions, avatar, nav order, theme). */
export function makeDemoAuth(demoUsers: ReturnType<typeof makeDemoUsers>) {
  const now = Date.now()
  const iso = (msAgo: number) => new Date(now - msAgo).toISOString()
  const H = 3600e3, D = 24 * H
  const fail = (status: number, msg: string): never => { throw new ApiError(status, msg) }
  const DEMO_SECRET = 'JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP'
  const demoUser: { name: string; avatar: string; nav: string[]; theme: string } = { name: 'demo', avatar: '', nav: [], theme: '' }
  const demo2fa = { enabled: false, at: '', left: 10 }
  const demoCodes = () => ['k7m2q-x9tpw', 'h4rcz-m8vne', 'p3dqs-w7kua', 'y9bft-c2hrm', 'n6gxe-t4jqp', 'r2wmk-z8cdv', 'e5ujn-q3fyb', 'a8tpc-v6sgh', 'w4hzr-k9nem', 'f7qdy-b3xut']
  const demoSessions: SessionInfo[] = [
    { id: '3f9a1c0d7e2b4a65', ip: '203.0.113.24', user_agent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 14_6) AppleWebKit/605.1.15 Safari/605.1.15', current: true, created_at: iso(2 * H), expires_at: new Date(now + 30 * D).toISOString() },
    { id: 'b81e4f2a90c37d16', ip: '198.51.100.7', user_agent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Mobile/15E148', current: false, created_at: iso(3 * D), expires_at: new Date(now + 27 * D).toISOString() },
  ]
  return {
    twoFactor: () => demo2fa.enabled,
    route(method: string, m: (p: string) => string[] | null, body: any): unknown {
      const G = method === 'GET', P = method === 'POST', U = method === 'PUT', DEL = method === 'DELETE'
      let a: string[] | null
      if (m('auth/status')) return { setup_required: location.hash.includes('setup=1') }
      // demo 2FA: `#/login?demo=1&2fa=1` (or enabling it in Settings → Security) makes login ask for a code; any 6 digits pass
      if (P && m('auth/login') && (demo2fa.enabled || location.hash.includes('2fa=1'))) return { two_factor_required: true, pending_token: 'demo-pending', expires_at: new Date(now + 5 * 60e3).toISOString() }
      if (P && m('auth/login/2fa')) { if (!/^\d{6}$|^[a-z2-9]{5}-?[a-z2-9]{5}$/i.test(String(body?.code ?? '').trim())) fail(401, 'invalid verification code'); return { id: 'usr_demo', username: 'demo' } }
      if (P && m('auth/username')) {
        if (!body?.current_password) throw new ApiError(401, 'current password is wrong', 'invalid_credentials')
        if (demo2fa.enabled && !body?.code) throw new ApiError(401, 'two-factor code required', 'code_required')
        if (demo2fa.enabled && !/^\d{6}$/.test(String(body?.code))) throw new ApiError(401, 'invalid verification code', 'invalid_code')
        const n = String(body?.username ?? '').trim()
        if (!/^[A-Za-z0-9_.@-]{1,64}$/.test(n)) fail(400, 'username must be 1-64 characters: letters, digits, _ . @ -')
        demoUser.name = n; return { username: n, auth: 'session', scope: 'session', permissions: [] }
      }
      if (U && m('me/avatar')) { demoUser.avatar = String(body?.avatar ?? ''); return { avatar: demoUser.avatar } }
      if (U && m('me/nav-order')) { demoUser.nav = (body?.order as string[]) ?? []; return { order: demoUser.nav } }
      if (U && m('me/theme')) {
        const t = String(body?.theme ?? '')
        if (t && !/^[a-z][a-z0-9-]{0,31}$/.test(t)) fail(400, 'invalid theme: ' + t)
        demoUser.theme = t; return { theme: t }
      }
      if (m('auth/me')) return demoUsers.me({ id: 'usr_demo', username: demoUser.name, avatar: demoUser.avatar || undefined, nav_order: demoUser.nav.length ? demoUser.nav : undefined, theme: demoUser.theme || undefined })
      if (m('auth/login') || m('auth/setup')) return { id: 'usr_demo', username: (demoUser.name = body?.username || demoUser.name) }
      if (m('auth/logout')) return {}
      if (G && m('auth/2fa')) return { enabled: demo2fa.enabled, enabled_at: demo2fa.enabled ? demo2fa.at : undefined, recovery_codes_left: demo2fa.enabled ? demo2fa.left : 0 }
      if (P && m('auth/2fa/enroll')) { if (!body?.password) fail(401, 'wrong password'); return { secret: DEMO_SECRET, otpauth_uri: `otpauth://totp/Atoll:demo@${location.host}?secret=${DEMO_SECRET}&issuer=Atoll&algorithm=SHA1&digits=6&period=30` } }
      if (P && m('auth/2fa/enable')) { if (!/^\d{6}$/.test(String(body?.code ?? '').trim())) fail(401, 'invalid verification code'); Object.assign(demo2fa, { enabled: true, at: new Date().toISOString(), left: 10 }); return { recovery_codes: demoCodes() } }
      if (P && m('auth/2fa/disable')) { if (!body?.password || !body?.code) fail(401, 'wrong password or code'); demo2fa.enabled = false; return {} }
      if (P && m('auth/2fa/recovery-codes')) { if (!body?.password || !body?.code) fail(401, 'wrong password or code'); demo2fa.left = 10; return { recovery_codes: demoCodes() } }
      if (P && m('auth/password')) {
        if (!body?.current_password) throw new ApiError(401, 'invalid credentials', 'invalid_credentials')
        if (demo2fa.enabled && !body?.code) throw new ApiError(401, 'two-factor code required', 'code_required')
        if (demo2fa.enabled && !/^\d{6}$/.test(String(body?.code))) throw new ApiError(401, 'invalid verification code', 'invalid_code')
        const np = String(body?.new_password ?? '')
        if (np.length < 8 || np.length > 72) fail(400, 'new password must be 8-72 characters')
        if (np === body.current_password) fail(400, 'new password must differ from the current one')
        demoUsers.passwordChanged()
        const n = demoSessions.length - 1; demoSessions.splice(0, demoSessions.length, ...demoSessions.filter(x => x.current)); return { revoked_sessions: n }
      }
      if (G && m('auth/sessions')) return demoSessions
      if (P && m('auth/sessions/revoke-others')) { const n = demoSessions.length - 1; demoSessions.splice(0, demoSessions.length, ...demoSessions.filter(x => x.current)); return { revoked: n } }
      if (DEL && (a = m('auth/sessions/*'))) { const i = demoSessions.findIndex(x => x.id === a![0]); if (i >= 0) demoSessions.splice(i, 1); return {} }
      return undefined
    },
  }
}
