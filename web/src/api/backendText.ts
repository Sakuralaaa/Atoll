import { useT } from '../i18n'

// Chinese for the English texts the agent and server put into Docker adoption / Zeabur import
// results (scan warnings, risk reasons, step names and details, container states). They are
// matched by pattern rather than by code so results from older agents translate too; anything
// unknown is shown as it came. English UI shows the original text.

type Rule = [RegExp, (m: RegExpMatchArray) => string]

const STATES: Record<string, string> = {
  running: '运行中', exited: '已退出', created: '已创建', paused: '已暂停', restarting: '重启中', dead: '已失效', removing: '删除中', stopped: '已停止',
}

/** Container state (docker inspect State.Status) in Chinese. */
export const stateZh = (s: string) => STATES[s] ?? s

const STEPS: Record<string, string> = {
  // docker adoption (docker_adopt.go)
  'Check containers and resources': '检查容器和资源',
  'Prepare images': '准备镜像',
  'Start Atoll copies (no ports, scratch data)': '启动 Atoll 副本（不发布端口，临时数据）',
  'Verify': '校验',
  'Stop old containers': '停止旧容器',
  'Start Atoll services on the old ports and data': '在原端口和数据上启动 Atoll 服务',
  'Verify after cutover': '切换后校验',
  'Roll back': '回滚',
  // zeabur-local takeover (zeabur.go)
  'Check resources': '检查资源',
  'Import images': '导入镜像',
  'Copy volumes': '复制数据卷',
  'Copy databases': '复制数据库',
  'Start services (no public ingress)': '启动服务（不接公网）',
  'Stop Zeabur services': '停止 Zeabur 服务',
  'Final data sync': '最终数据同步',
  'Switch ingress to Atoll': '入口切换到 Atoll',
  'Configure DNS': '配置 DNS',
  // plan import (imports.go)
  'Create project and services': '创建项目和服务',
  'Deploy services': '部署服务',
}

const rules: Rule[] = [
  // ---- group / plan level ----
  [/^(.+?) needs manual handling and stays as it is: (.+)$/s, m => `${m[1]} 需要手动处理，保持原样：${m[2].split('; ').map(zh).join('；')}`],
  [/^(.+?) is not adopted and keeps running as it is$/, m => `${m[1]} 不接管，保持原样运行`],
  [/^(.+?): (\S+) names container (\S+); after adoption use the service name (\S+)$/, m => `${m[1]}：${m[2]} 写的是容器名 ${m[3]}；接管后请改用服务名 ${m[4]}`],
  [/^network (\S+) is shared with (.+); after adoption those containers no longer reach this group by name$/, m => `网络 ${m[1]} 与 ${m[2]} 共用；接管后这些容器无法再按名字访问这一组`],
  [/^(.+?): env variables Atoll cannot store were dropped: (.+)$/, m => `${m[1]}：Atoll 无法保存的环境变量已丢弃：${m[2]}`],
  [/^Adopted services without a memory limit get the default of (\d+) MB \(Settings\); raise it per service before cutover if a service needs more$/, m => `没有内存上限的服务会使用默认值 ${m[1]} MB（设置里可改）；需要更多内存的服务请在切换前单独调高`],
  // ---- container risk reasons (dockeradopt/scan.go) ----
  [/^its image (\S+) is no longer on this server$/, m => `镜像 ${m[1]} 已不在这台服务器上`],
  [/^clears the image's ENTRYPOINT, which Atoll cannot express$/, () => '清空了镜像的 ENTRYPOINT，Atoll 无法表达'],
  [/^sets its working directory to (.+) \(docker -w \/ compose working_dir\), which Atoll cannot set$/, m => `设置了工作目录 ${m[1]}（docker -w / compose working_dir），Atoll 无法设置`],
  [/^uses the host network \(network_mode: host\): its ports cannot be moved behind Atoll's network$/, () => '使用主机网络（network_mode: host）：端口无法移到 Atoll 网络之后'],
  [/^has no network \(network_mode: none\)$/, () => '没有网络（network_mode: none）'],
  [/^shares another container's network namespace \((.+)\)$/, m => `共用另一个容器的网络命名空间（${m[1]}）`],
  [/^publishes host port (\d+), reserved for (.+)$/, m => `发布了主机端口 ${m[1]}，该端口留给 ${m[2]}`],
  [/^mounts the container runtime socket (\S+): it controls the host's Docker$/, m => `挂载了容器运行时套接字 ${m[1]}：可以控制主机的 Docker`],
  [/^bind-mounts (\S+) from the host: (.+)$/, m => `从主机绑定挂载 ${m[1]}：${m[2]}`],
  [/^bind-mounts (\S+), which does not exist on the host$/, m => `绑定挂载的 ${m[1]} 在主机上不存在`],
  [/^uses volume (\S+) with driver "?([^"]+)"?; only local volumes can be mounted in place$/, m => `数据卷 ${m[1]} 使用驱动 ${m[2]}；只有 local 数据卷能原地挂载`],
  [/^volume (\S+) has an unusable mountpoint "?([^"]*)"?$/, m => `数据卷 ${m[1]} 的挂载点 ${m[2]} 不可用`],
  [/^volume (\S+): its mountpoint (\S+) does not exist$/, m => `数据卷 ${m[1]}：挂载点 ${m[2]} 不存在`],
  [/^has a (\S+) mount at (\S+)$/, m => `在 ${m[2]} 有 ${m[1]} 类型的挂载`],
  [/^uses --volumes-from (.+)$/, m => `使用了 --volumes-from ${m[1]}`],
  [/^runs privileged \(full access to the host\)$/, () => '以特权模式运行（可完全访问主机）'],
  [/^adds Linux capabilities \((.+)\)$/, m => `添加了 Linux capabilities（${m[1]}）`],
  [/^has host devices \((.+)\)$/, m => `挂载了主机设备（${m[1]}）`],
  [/^requests devices such as GPUs \(--gpus\)$/, () => '请求了 GPU 等设备（--gpus）'],
  [/^disables a security profile \(security_opt (.+)\)$/, m => `禁用了安全配置（security_opt ${m[1]}）`],
  [/^shares the host's (\S+) namespace$/, m => `共用主机的 ${m[1]} 命名空间`],
  [/^shares another container's IPC namespace \((.+)\)$/, m => `共用另一个容器的 IPC 命名空间（${m[1]}）`],
  [/^uses the (\S+) runtime$/, m => `使用 ${m[1]} 运行时`],
  // ---- container warnings ----
  [/^image (\S+) has no registry digest \(built or loaded locally\): Atoll runs the same local image under an atoll-adopt\/ tag, so this service cannot move to another server$/, m => `镜像 ${m[1]} 没有仓库摘要（本地构建或导入）：Atoll 以 atoll-adopt/ 标签运行同一个本地镜像，因此这个服务不能迁移到其他服务器`],
  [/^legacy --link entries are not kept; services reach each other by service name$/, () => '旧式 --link 不保留；服务之间用服务名互相访问'],
  [/^port (\S+) is published on a random host port; Atoll keeps it internal$/, m => `端口 ${m[1]} 发布在随机主机端口上；Atoll 只在内部保留`],
  [/^tmpfs at (\S+) is not kept \(writes go to the container's own filesystem\)$/, m => `${m[1]} 的 tmpfs 不保留（写入落在容器自己的文件系统）`],
  [/^security_opt (.+) is not kept$/, m => `security_opt ${m[1]} 不保留`],
  [/^sysctls are not kept$/, () => 'sysctls 不保留'],
  [/^extra_hosts \((.+)\) are not kept$/, m => `extra_hosts（${m[1]}）不保留`],
  [/^custom DNS servers are not kept$/, () => '自定义 DNS 服务器不保留'],
  [/^log driver (\S+) is replaced by Atoll's json-file logs$/, m => `日志驱动 ${m[1]} 改为 Atoll 的 json-file 日志`],
  [/^read-only root filesystem is not kept$/, () => '只读根文件系统不保留'],
  [/^shm_size (\d+) MB is not kept \(Atoll uses Docker's 64 MB\)$/, m => `shm_size ${m[1]} MB 不保留（Atoll 使用 Docker 默认的 64 MB）`],
  [/^ulimits are not kept$/, () => 'ulimits 不保留'],
  [/^--init is not kept$/, () => '--init 不保留'],
  [/^restart policy "?([^"]+)"?: Atoll restarts its copy automatically \(unless-stopped\)$/, m => `重启策略 ${m[1]}：Atoll 会自动重启它的副本（unless-stopped）`],
  [/^container is (\S+) now; Atoll starts its copy at cutover$/, m => `容器当前${stateZh(m[1])}；Atoll 在切换时启动它的副本`],
  [/^labels for other tools \(traefik\/caddy-docker-proxy\/watchtower…\) are not kept$/, () => '其他工具的标签（traefik/caddy-docker-proxy/watchtower…）不保留'],
  // ---- zeabur plans and inventories ----
  [/^service is suspended on Zeabur$/, () => '服务在 Zeabur 上已暂停'],
  [/^built from Git on Zeabur; without host access the image cannot be exported — rebuild it \(Dockerfile \/ zbpack behaviour\) and push to your registry$/, () => '在 Zeabur 上从 Git 构建；没有主机权限无法导出镜像，请重新构建（Dockerfile / zbpack）并推送到你的镜像仓库'],
  [/^unresolved variable references \(platform built-ins\?\): (.+)$/, m => `未解析的变量引用（平台内置变量？）：${m[1]}`],
  [/^port unknown; assumed 8080 \(Zeabur's default for Git services\)$/, () => '端口未知；按 8080 处理（Zeabur Git 服务的默认端口）'],
  [/^database backups \(Zeabur daily CronJob\) are not migrated: set up your own backups and test a restore$/, () => '数据库备份（Zeabur 每日 CronJob）不迁移：请自己设置备份并测试恢复'],
  [/^logs and metrics history are not migrated$/, () => '日志和监控历史不迁移'],
  [/^dropped Zeabur platform variables (.+)$/, m => `已去掉 Zeabur 平台变量 ${m[1]}`],
  [/^rewrote hostnames in (.+)$/, m => `已改写 ${m[1]} 里的主机名`],
  [/^domain (\S+) skipped: (.+)$/, m => `域名 ${m[1]} 已跳过：${m[2]}`],
  [/^Update by hand any OAuth callback URLs, webhooks and payment provider settings \(Stripe, GitHub, Google…\) that use these URLs: (.+)$/s, m => `请手动更新使用这些地址的 OAuth 回调、Webhook 和支付平台设置（Stripe、GitHub、Google…）：${m[1]}`],
  [/^Taken-over services get the default memory limit of (\d+) MB \(Settings\); a service that needs more is OOM-restarted\. Raise it per service before cutover if needed$/, m => `接管的服务使用默认内存上限 ${m[1]} MB（设置里可改）；需要更多内存的服务会因 OOM 重启，必要时在切换前单独调高`],
  [/^no user databases found$/, () => '没有找到用户数据库'],
  [/^per-collection counts are not verified for MongoDB$/, () => 'MongoDB 不校验每个集合的文档数'],
  [/^target is stopped; key count not verified$/, () => '目标已停止；未校验键数量'],
  // ---- step details (docker_adopt.go) ----
  [/^(\d+) container\(s\) found; ([\d.]+) GB disk free, (\d+) MB memory free(?:; (.+))?$/, m => `找到 ${m[1]} 个容器；可用硬盘 ${m[2]} GB，可用内存 ${m[3]} MB${m[4] ? `；${m[4]}` : ''}`],
  [/^(\d+) image\(s\) pinned to their registry digest, (\d+) kept as local image\(s\)$/, m => `${m[1]} 个镜像固定到仓库摘要，${m[2]} 个保留为本地镜像`],
  [/^(\d+) Atoll copy\(ies\) started without published ports; writable data dirs are scratch copies, the old containers keep serving$/, m => `已启动 ${m[1]} 个 Atoll 副本（不发布端口）；可写数据目录为临时副本，旧容器照常服务`],
  [/^all Atoll copies started and stayed healthy; stopped again until cutover$/, () => '所有 Atoll 副本启动并保持健康；已停止，等待切换'],
  [/^(\d+) old container\(s\) stopped and kept \(restart policy no\)$/, m => `已停止 ${m[1]} 个旧容器并保留（重启策略改为 no）`],
  [/^(\d+) service\(s\) running: (\d+) host port\(s\) published, (\d+) mount\(s\) in place$/, m => `${m[1]} 个服务运行中：发布 ${m[2]} 个主机端口，${m[3]} 个挂载原地使用`],
  [/^Atoll services stopped; (\d+) old container\(s\) restored$/, m => `Atoll 服务已停止；已恢复 ${m[1]} 个旧容器`],
  [/^container (\S+) is gone from the server$/, m => `容器 ${m[1]} 已不在服务器上`],
  [/^cutover failed and was rolled back: (.+)$/s, m => `切换失败，已回滚：${m[1]}`],
  [/^cutover failed: (.+); rollback failed too: (.+)$/s, m => `切换失败：${m[1]}；回滚也失败：${m[2]}`],
  [/^(.+) \(rolled back\)$/s, m => `${zh(m[1])}（已回滚）`],
  [/^no data uploaded; volume starts empty$/, () => '没有上传数据；数据卷为空'],
  [/^Upload volume (.+)$/, m => `上传数据卷 ${m[1]}`],
  [/^Copy volume (.+)$/, m => `复制数据卷 ${m[1]}`],
]

/** Chinese for one backend text; unknown text comes back unchanged. */
export function zh(s: string): string {
  if (!s) return s
  if (STEPS[s]) return STEPS[s]
  for (const [re, f] of rules) {
    const m = s.match(re)
    if (m) return f(m)
  }
  // "<container>: <warning>" (plan warnings prefix the container name)
  const p = s.match(/^([\w.-]+): (.+)$/s)
  if (p) {
    const rest = zh(p[2])
    if (rest !== p[2]) return `${p[1]}：${rest}`
  }
  return s
}

/** Translator for backend texts in the current UI language (English keeps the original). */
export function useBackendText() {
  const { lang } = useT()
  return {
    bt: (s: string) => lang === 'zh' ? zh(s) : s,
    state: (s: string) => lang === 'zh' ? stateZh(s) : s,
  }
}
