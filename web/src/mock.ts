import { L } from './i18n'
export type Status = 'running' | 'deploying' | 'stopped' | 'failed'
export type Kind = 'image' | 'postgres' | 'mysql' | 'redis' | 'mongo'

export interface Server {
  id: string; name: string; provider: string; providerColor: string; ip: string
  region: string; flag: string; cpu: number; memGB: number; diskGB: number
  cpuUse: number; memUse: number; diskUse: number; status: 'online' | 'offline'
  attention?: boolean; latency: number; os: string; arch: string; role?: 'control'
}
export interface Service {
  id: string; name: string; kind: Kind; image: string; tag: string; status: Status
  domain?: string; port: number; created: string; replicas: string; letter?: string; color?: string
}
export interface Project {
  id: string; name: string; serverId: string; letter: string; color: string; services: Service[]
}

export const servers: Server[] = [
  { id: 'phx', name: 'Oracle ARM West Phoenix', provider: 'Oracle', providerColor: '#f80000', ip: '203.0.113.10', region: 'Phoenix, US', flag: '🇺🇸', cpu: 4, memGB: 24, diskGB: 200, cpuUse: 12, memUse: 41, diskUse: 34, status: 'online', latency: 38, os: 'Ubuntu 24.04', arch: 'arm64' },
  { id: 'fra', name: 'Hetzner CAX21 Falkenstein', provider: 'Hetzner', providerColor: '#d50c2d', ip: '203.0.113.20', region: 'Falkenstein, DE', flag: '🇩🇪', cpu: 4, memGB: 8, diskGB: 80, cpuUse: 27, memUse: 58, diskUse: 46, status: 'online', latency: 162, os: 'Debian 13', arch: 'arm64' },
  { id: 'sin', name: 'DigitalOcean SGP1', provider: 'DigitalOcean', providerColor: '#0080ff', ip: '203.0.113.30', region: 'Singapore, SG', flag: '🇸🇬', cpu: 2, memGB: 4, diskGB: 80, cpuUse: 64, memUse: 82, diskUse: 58, status: 'online', attention: true, latency: 71, os: 'Ubuntu 24.04', arch: 'amd64' },
  { id: 'tyo', name: L("Vultr Tokyo 控制节点"), provider: 'Vultr', providerColor: '#007bfc', ip: '203.0.113.40', region: 'Tokyo, JP', flag: '🇯🇵', cpu: 1, memGB: 2, diskGB: 30, cpuUse: 8, memUse: 36, diskUse: 21, status: 'online', latency: 4, os: 'Debian 13', arch: 'amd64', role: 'control' },
  { id: 'lax', name: 'RackNerd LA', provider: 'RackNerd', providerColor: '#16a34a', ip: '203.0.113.50', region: 'Los Angeles, US', flag: '🇺🇸', cpu: 2, memGB: 3, diskGB: 50, cpuUse: 0, memUse: 0, diskUse: 18, status: 'offline', latency: 0, os: 'Ubuntu 22.04', arch: 'amd64' },
]

const img = (id: string, name: string, image: string, tag: string, o: Partial<Service> = {}): Service =>
  ({ id, name, kind: 'image', image, tag, status: 'running', port: 8080, created: '2026-10-27', replicas: '1/1', ...o })

export const projects: Project[] = [
  { id: 'mailrelay', name: 'mail-relay', serverId: 'phx', letter: 'O', color: '#a855f7', services: [
    img('mail-relay', 'mail-relay', 'ghcr.io/example/mail-relay', 'v2.3.1', { domain: 'mail.example.com', port: 5000, letter: 'O', color: '#a855f7' }),
  ]},
  { id: 'acme', name: 'acme-api', serverId: 'phx', letter: 'G', color: '#22c55e', services: [
    img('acme-api', 'acme-api', 'ghcr.io/example/acme-api', 'latest', { domain: 'acme.example.com', port: 8000, letter: 'G', color: '#e5e7eb' }),
    { id: 'pg', name: 'postgresql', kind: 'postgres', image: 'postgres', tag: '17-alpine', status: 'running', port: 5432, created: '2026-10-20', replicas: '1/1' },
    { id: 'redis', name: 'redis', kind: 'redis', image: 'redis', tag: '7-alpine', status: 'running', port: 6379, created: '2026-10-20', replicas: '1/1' },
  ]},
  { id: 'linkproxy', name: 'LinkProxy', serverId: 'phx', letter: 'C', color: '#3b82f6', services: [
    img('linkproxy', 'LinkProxy', 'example/link-proxy', 'latest', { domain: 'cpa.example.com', port: 8317, letter: 'C', color: '#3b82f6' }),
    { id: 'pg2', name: 'postgresql', kind: 'postgres', image: 'postgres', tag: '17', status: 'running', port: 5432, created: '2026-10-11', replicas: '1/1' },
    img('panel', 'admin-panel', 'ghcr.io/router-for-me/admin-panel', 'main', { port: 3000, letter: 'P', color: '#f59e0b' }),
    img('cron', 'cron-worker', 'ghcr.io/example/cron-worker', 'v0.4', { status: 'deploying', letter: 'T', color: '#14b8a6' }),
  ]},
  { id: 'file-share', name: 'file-share', serverId: 'fra', letter: 'A', color: '#38bdf8', services: [
    img('file-share', 'file-share', 'xhofe/file-share', 'sha256:5c43b1beb91f2b513f11f4111ec7cccc66b087eae86c9fa160fdfd518caf0a57', { domain: 'pan.example.com', port: 5244, letter: 'A', color: '#38bdf8' }),
  ]},
  { id: 'chat', name: 'chat-app', serverId: 'fra', letter: 'D', color: '#f97316', services: [
    img('chat', 'chat-app', 'ghcr.io/example/chat', '1.12.0', { domain: 'chat.example.com', port: 3000, letter: 'D', color: '#f97316' }),
    { id: 'mongo', name: 'mongodb', kind: 'mongo', image: 'mongo', tag: '8.0', status: 'running', port: 27017, created: '2026-09-30', replicas: '1/1' },
  ]},
  { id: 'flare', name: 'flaresolverr', serverId: 'sin', letter: 'F', color: '#60a5fa', services: [
    img('flare', 'flaresolverr', 'ghcr.io/flaresolverr/flaresolverr', 'latest', { port: 8191, letter: 'F', color: '#60a5fa' }),
  ]},
  { id: 'qabot', name: 'qa-bot', serverId: 'sin', letter: 'Q', color: '#e5e7eb', services: [
    img('qabot', 'qa-bot', 'example/qa-bot', 'latest', { domain: 'qa.example.com', status: 'failed', replicas: '0/1', letter: 'Q', color: '#e5e7eb' }),
  ]},
  { id: 'trend', name: 'blog-studio', serverId: 'fra', letter: 'T', color: '#c084fc', services: [
    img('trend', 'blog-studio', 'ghcr.io/example/blog-studio', 'v1.0.3', { domain: 'trend.example.com', port: 3000, letter: 'T', color: '#c084fc' }),
    { id: 'mysql', name: 'mysql', kind: 'mysql', image: 'mysql', tag: '8.4', status: 'stopped', port: 3306, created: '2026-10-02', replicas: '0/1' },
  ]},
]

export const serverById = (id: string) => servers.find(s => s.id === id)!
export const projectById = (id?: string) => projects.find(p => p.id === id) ?? projects[0]
export const projectsOn = (sid: string) => projects.filter(p => p.serverId === sid)
