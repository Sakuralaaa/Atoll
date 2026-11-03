import clsx from 'clsx'
import * as si from 'simple-icons'

export { si }
export const cx = clsx

export const statusText: Record<string, string> = { running: '运行中', deploying: '部署中', stopped: '已停止', failed: '崩溃', unknown: '未知', online: '在线', offline: '离线', pending: '等待连接', draining: '迁出中' }

export const series = (n: number, base: number, amp: number, seed = 1) =>
  Array.from({ length: n }, (_, i) => Math.max(0.2, base + Math.sin(i / 2.3 + seed) * amp + Math.cos(i / 1.1 + seed * 2) * amp * 0.45 + (i % 9 === 0 ? amp * 1.2 : 0)))

/** install-server.sh of one release (a tag), or of main for a dev build: a restore must run the
 *  same version that wrote the backup, not whatever main is today. */
export const installServerURL = (version: string) =>
  `https://raw.githubusercontent.com/Sakuralaaa/Atoll/${/^v\d+\.\d+\.\d+/.test(version) ? `refs/tags/${version}` : 'main'}/install/install-server.sh`
