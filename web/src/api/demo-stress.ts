import { L } from '../i18n'
// Demo stress fixture: `?demo=1&stress=1` (search or hash query) adds deliberately long, realistic
// data — digest-pinned image refs, long domains/env values/names/errors, many services — so layout
// regressions (overflowing cards, squeezed columns, clipped text) show up in the demo at any width.
import type { APIToken, Confirmation, Deployment, Domain, EnvVar, Project, Server, Service } from './types'

export const stressEnabled = () => /[?&]stress=1\b/.test(location.search + location.hash)

const DIGEST = 'sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08'
const LONG_NAME = 'customer-notification-delivery-pipeline-worker'

export function addStressData(d: {
  now: number; servers: Server[]; projects: Project[]; services: Service[]; domains: Domain[]
  env: Record<string, EnvVar[]>; deployments: Record<string, Deployment[]>; tokens: APIToken[]; confirmations: Confirmation[]
}) {
  const iso = (ms: number) => new Date(d.now - ms).toISOString()
  const base = d.servers[0]
  d.servers.push({
    ...base, id: 'longsrv', name: L("Hetzner Cloud CCX63 Nuremberg 主力生产节点 (dedicated vCPU, 48 cores)"), provider: 'Hetzner', region: 'Nuremberg, Bavaria, Germany (eu-central)',
    public_ipv4: '203.0.113.99', public_ipv6: '2001:db8:85a3:0000:0000:8a2e:0370:7334', tailnet_host: 'hetzner-ccx63-nuremberg-production-primary.tail3c1a.ts.net', tailnet_ip: undefined, network: { provider: '', state: 'not_installed', via_tailnet: false, control_url: 'https://panel.example.com' },
    // pings and the fallback both time out: the panel shows 「超时」 with the error in the tooltip
    latency: { last_ms: 0, avg_ms: 0, samples: 0, measured_at: '', failures: 3, failures_total: 3, last_attempt: iso(9e3), last_error: 'WebSocket ping: no pong within 20s; fallback: no answer within 20s' },
  })
  // a fleet of small boxes so server pickers (migration wizards) have more rows than fit and must scroll
  for (const [i, [name, region]] of [['Hetzner CX22 Helsinki', 'Helsinki, FI'], ['Vultr Seoul', 'Seoul, KR'], ['Linode Mumbai', 'Mumbai, IN'], ['OVH Gravelines', 'Gravelines, FR'], ['BuyVM Las Vegas', 'Las Vegas, US'], ['Contabo Sydney', 'Sydney, AU']].entries())
    d.servers.push({ ...base, id: `fleet${i}`, name, region, public_ipv4: `203.0.113.${110 + i}`, tailnet_ip: undefined, network: { provider: '', state: 'not_installed', via_tailnet: false, control_url: 'https://panel.example.com' } })
  // many small projects on the long-named machine: its 按服务器分组 card clamps the names to two lines with "+N"
  for (const [i, n] of ['billing', 'auth-service', 'analytics-dashboard', 'image-resizer', 'docs', 'status-page', 'internal-tools-admin', 'newsletter', 'feature-flags'].entries())
    d.projects.push({ id: `stressp${i}`, name: n, server_id: 'longsrv', created_at: iso((i + 4) * 864e5) })
  const pid = 'stress'
  d.projects.push({ id: pid, name: 'enterprise-customer-portal-production-environment-eu-central', description: L("长名称压力测试项目：用于检查所有页面在极长的项目名、服务名、域名与镜像摘要下的布局是否正常"), server_id: 'longsrv', created_at: iso(3 * 864e5) })
  const mk = (i: number, name: string, image: string, status: string, kind = 'image'): Service => ({
    id: `stress${i}`, project_id: pid, server_id: 'longsrv', name, kind, image, status, replicas: status === 'running' ? '1/1' : '0/1', created_at: iso((i + 1) * 864e5),
    ports: [{ name: 'web', container_port: 8080 + i, protocol: 'http' }], volumes: [{ name: 'data', mount_path: '/var/lib/customer-notification-delivery/persistent-storage/data' }],
    cpu_limit: 2, mem_limit_mb: 2048, health: { http_path: '/api/v1/internal/health/readiness-probe', interval_sec: 30 },
  })
  const svcs = [
    mk(0, LONG_NAME, `registry.example.com/platform-team/customer-notification-delivery-pipeline-worker:v2026.11.02-rc.17@${DIGEST}`, 'running'),
    mk(1, 'notification-api-gateway', `ghcr.io/example-organization/notification-api-gateway@${DIGEST}`, 'failed'),
    mk(2, 'pg-primary', 'postgres:17-alpine', 'running', 'postgres'),
    ...Array.from({ length: 13 }, (_, k) => mk(3 + k, `batch-worker-${['emails', 'sms', 'push', 'webhooks', 'digests', 'reports', 'exports', 'imports', 'thumbnails', 'search-indexer', 'billing-sync', 'audit-trail', 'cleanup'][k]}`, `ghcr.io/example/batch-worker:${k}.0.${k * 3}`, ['running', 'running', 'deploying', 'stopped'][k % 4])),
  ]
  for (const s of svcs) {
    d.services.push(s)
    d.env[s.id] = [
      { key: 'PORT', value: '8080', secret: false, exposed: false },
      { key: 'NOTIFICATION_DELIVERY_PROVIDER_CALLBACK_URL_WITH_A_VERY_LONG_KEY_NAME', value: 'https://hooks.example.com/api/v2/notifications/delivery-status/callback?tenant=enterprise-customer-portal&region=eu-central-1&signature_version=4', secret: false, exposed: false },
      { key: 'JWT_PUBLIC_KEY', value: 'MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAu1SU1LfVLPHCozMxH2Mo4lgOEePzNm0tRgeLezV6ffAt0gunVTLw7onLRnrq0', secret: false, exposed: true },
      { key: 'DATABASE_URL', value: 'postgres://notification_service_user:Zq8vN2kPx4LmT7rWc9YbA3sD6fGh1jKe@pg-primary.enterprise-customer-portal.internal:5432/notifications_production?sslmode=require', secret: true, exposed: false },
    ]
    d.deployments[s.id] = [0, 1, 2, 3].map(i => ({
      id: `dep_${s.id}_${i}`, service_id: s.id, server_id: 'longsrv', image_digest: DIGEST,
      status: i === 0 ? (s.status === 'failed' ? 'failed' : s.status === 'deploying' ? 'pulling' : 'running') : 'superseded',
      trigger: ['image-update', 'env', 'redeploy', 'create'][i],
      message: i === 0 && s.status === 'failed'
        ? `pull access denied for ghcr.io/example-organization/notification-api-gateway@${DIGEST}: repository does not exist or may require 'docker login': denied: requested access to the resource is denied`
        : i === 1 ? `image updated ${s.image.split('@')[0]} → ${DIGEST}` : undefined,
      started_at: iso((i * 2 + 1) * 864e5), finished_at: iso((i * 2 + 1) * 864e5 - 52e3),
    }))
  }
  d.domains.push(
    { id: 'dom_stress0', service_id: 'stress0', hostname: 'customer-notification-delivery-pipeline.eu-central.production.example.com', port_name: 'web', proxied: true, status: 'active', created_at: iso(864e5) },
    { id: 'dom_stress1', service_id: 'stress1', hostname: 'notification-api-gateway-enterprise-customer-portal.example.com', port_name: 'web', proxied: false, status: 'error', error: 'TLS certificate issuance failed: ACME challenge for notification-api-gateway-enterprise-customer-portal.example.com returned 403 Forbidden (DNS points to 198.51.100.200, expected 203.0.113.99)', created_at: iso(864e5) },
  )
  // one domain per batch worker, so the 域名 list runs past ten rows and scrolls
  for (let k = 3; k < 16; k++) d.domains.push({ id: `dom_stress${k}`, service_id: `stress${k}`, hostname: `worker-${k}.example.com`, port_name: 'web', proxied: true, status: 'active', created_at: iso(k * 864e5) })
  d.tokens.push({ id: 'tok_stress', name: 'github-actions-enterprise-customer-portal-production-deployer', scope: 'deploy', permissions: ['read', 'deploy', 'domains', 'restore'], projects: [pid, 'acme', 'linkproxy', 'mailrelay'], require_approval: true, prefix: 'atl_pat_Lq9z', last_used_at: iso(3e5), created_at: iso(2 * 864e5) })
  d.confirmations.unshift({ id: 'cfm_stress', action: 'service.update', target: LONG_NAME, summary: L("更新 enterprise-customer-portal-production-environment-eu-central / {0} 的镜像为 registry.example.com/platform-team/customer-notification-delivery-pipeline-worker@{1}", LONG_NAME, DIGEST), status: 'pending', created_by: 'github-actions-enterprise-customer-portal-production-deployer', created_at: iso(6e4) })
}
