import clsx from 'clsx'
import * as si from 'simple-icons'

export { si }
export const cx = clsx

export const statusText: Record<string, string> = { running: '运行中', deploying: '部署中', stopped: '已停止', failed: '崩溃', unknown: '未知', online: '在线', offline: '离线', pending: '等待连接', draining: '迁出中' }

export const series = (n: number, base: number, amp: number, seed = 1) =>
  Array.from({ length: n }, (_, i) => Math.max(0.2, base + Math.sin(i / 2.3 + seed) * amp + Math.cos(i / 1.1 + seed * 2) * amp * 0.45 + (i % 9 === 0 ? amp * 1.2 : 0)))
