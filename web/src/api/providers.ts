import type { Server } from './types'

/** Canonical provider names the panel draws an icon for, keyed by a normalized spelling. */
const ALIASES: Record<string, string> = {
  oracle: 'Oracle', oci: 'Oracle', oraclecloud: 'Oracle',
  hetzner: 'Hetzner', digitalocean: 'DigitalOcean', do: 'DigitalOcean', vultr: 'Vultr',
  racknerd: 'RackNerd', rn: 'RackNerd', vmiss: 'VMISS', vimss: 'VMISS',
  ovh: 'OVH', ovhcloud: 'OVH', googlecloud: 'Google Cloud', gcp: 'Google Cloud', google: 'Google Cloud',
  aws: 'AWS', amazon: 'AWS', lightsail: 'AWS', azure: 'Azure', microsoft: 'Azure',
  alibabacloud: 'Alibaba Cloud', aliyun: 'Alibaba Cloud', alibaba: 'Alibaba Cloud',
  tencent: 'Tencent Cloud', tencentcloud: 'Tencent Cloud', qcloud: 'Tencent Cloud',
  linode: 'Akamai', akamai: 'Akamai', scaleway: 'Scaleway', contabo: 'Contabo', netcup: 'netcup',
  upcloud: 'UpCloud', ionos: 'IONOS', hostinger: 'Hostinger', cloudflare: 'Cloudflare',
  bandwagonhost: 'BandwagonHost', bwh: 'BandwagonHost', bandwagon: 'BandwagonHost', dmit: 'DMIT', greencloud: 'GreenCloud', greencloudvps: 'GreenCloud',
}

/** The canonical name for what the user typed ("oracle", "RN", "Hetzner Cloud"), or the text itself. */
export function canonicalProvider(name: string): string {
  const k = name.toLowerCase().replace(/[^a-z0-9]/g, '')
  if (ALIASES[k]) return ALIASES[k]
  const hit = Object.keys(ALIASES).filter(a => a.length > 3).find(a => k.startsWith(a))
  return hit ? ALIASES[hit] : name
}

/** Network owners (ASN org) that point at a provider. AS36352 (HostPapa / ColoCrossing) is
 *  where most RackNerd machines sit, so it is a guess; setting the provider overrides it. */
const ORG: [RegExp, string][] = [
  [/oracle/i, 'Oracle'], [/hetzner/i, 'Hetzner'], [/digitalocean/i, 'DigitalOcean'], [/vultr|choopa|constant company/i, 'Vultr'],
  [/racknerd|colocrossing|hostpapa/i, 'RackNerd'], [/vmiss/i, 'VMISS'], [/\bovh/i, 'OVH'], [/google/i, 'Google Cloud'],
  [/amazon|aws/i, 'AWS'], [/microsoft/i, 'Azure'], [/alibaba|aliyun/i, 'Alibaba Cloud'], [/tencent/i, 'Tencent Cloud'],
  [/linode|akamai/i, 'Akamai'], [/scaleway|online s\.?a\.?s/i, 'Scaleway'], [/contabo/i, 'Contabo'], [/netcup/i, 'netcup'],
  [/upcloud/i, 'UpCloud'], [/ionos|1&1/i, 'IONOS'], [/hostinger/i, 'Hostinger'], [/cloudflare/i, 'Cloudflare'],
  [/it7 networks|bandwagon/i, 'BandwagonHost'], [/\bdmit/i, 'DMIT'], [/greencloud/i, 'GreenCloud'],
]

export function guessProvider(org?: string): string {
  if (!org) return ''
  return ORG.find(([re]) => re.test(org))?.[1] ?? ''
}

/** What the panel shows as a server's provider: what the user set, else a guess from GeoIP. */
export function providerOf(s?: Pick<Server, 'provider' | 'geo'>): string {
  if (!s) return '?'
  return s.provider ? canonicalProvider(s.provider) : guessProvider(s.geo?.org) || '?'
}

/** ISO country code for a server: the region's trailing code ("Phoenix, US") or GeoIP's. */
export function countryOf(s?: Pick<Server, 'region' | 'geo'>): string {
  const m = s?.region?.match(/\b([A-Z]{2})\s*$/)
  return m?.[1] ?? s?.geo?.country ?? ''
}

/** Region text: what the user set, else GeoIP's "City, CC". */
export function regionOf(s?: Pick<Server, 'region' | 'geo'>): string {
  if (s?.region) return s.region
  const g = s?.geo
  return g ? [g.city, g.country].filter(Boolean).join(', ') : ''
}
