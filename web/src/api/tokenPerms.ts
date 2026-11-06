import { ALL_PERMISSIONS, GLOBAL_PERMISSIONS, type APIToken, type Permission } from './types'

/** Token permission labels and helpers shared by Connect (Developer) and a member's own tokens. */
export type Tr = (zh: string, en: string) => string
/** label + what it allows, per permission (internal/api Perm*) */
export const permInfo = (t: Tr): Record<Permission, [string, string]> => ({
  read: [t('只读', 'read'), t('查看项目、服务、变量（打码）、日志、指标、设置', 'View projects, services, env (masked), logs, metrics, settings')],
  deploy: [t('部署', 'deploy'), t('创建和修改项目与服务、变量、启停重部署、回滚、镜像更新、备份策略、导入', 'Create/edit projects and services, env, start/stop/redeploy, rollback, image updates, backup policy, imports')],
  delete: [t('删除', 'delete'), t('删除服务和项目（连同卷）', 'Delete services and projects (with their volumes)')],
  migrate: [t('迁移', 'migrate'), t('在服务器间迁移项目和服务，Zeabur 切换与回滚', 'Move projects/services between servers, Zeabur cutover and rollback')],
  domains: [t('域名', 'domains'), t('绑定和移除域名（会改 DNS）', 'Bind and remove domains (changes DNS)')],
  exec: [t('执行命令', 'exec'), t('在服务容器里执行命令（可读取容器内的密钥与数据），以及读取私有变量的值', 'Run commands inside service containers (can read their secrets and data), and read secret variable values')],
  restore: [t('还原', 'restore'), t('还原备份（覆盖线上数据）', 'Restore backups (replaces live data)')],
  servers: [t('服务器', 'servers'), t('加入令牌、改名和移除服务器、内网切换', 'Join tokens, rename/remove servers, tailnet actions')],
  settings: [t('设置', 'settings'), t('面板设置（Cloudflare、证书、备份存储、私有网络）', 'Panel settings (Cloudflare, TLS, backup storage, private network)')],
  tokens: [t('令牌', 'tokens'), t('查看、创建、修改、删除 API 令牌（不超过自身权限）', 'List/create/change/delete API tokens (never beyond its own)')],
})
export const DANGEROUS: Permission[] = ['exec', 'delete', 'migrate', 'restore', 'servers', 'tokens']
export const isGlobal = (p: string) => (GLOBAL_PERMISSIONS as string[]).includes(p)
export const permTone = (p: string) => p === 'read' ? 'zinc' : p === 'deploy' ? 'violet' : (DANGEROUS as string[]).includes(p) ? 'red' : 'amber'
/** older servers only report scope */
export const permsOf = (tk: APIToken): string[] => tk.permissions?.length ? tk.permissions : tk.scope === 'admin' ? ALL_PERMISSIONS : tk.scope === 'deploy' ? ['read', 'deploy'] : ['read']

