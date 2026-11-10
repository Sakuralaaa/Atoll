import { L } from '../i18n'
import { cx } from './ui-utils'
import { useState } from 'react'
import { Bell, Loader2, Pencil, Plus, Send, Trash2, Webhook, Mail, MessageCircle, Save } from 'lucide-react'
import { Badge, Button, Card, Hint } from './ui'
import { ConfirmDialog, Label, Modal, Select, TextInput, Toggle } from './feedback'
import { api } from '../api/client'
import { run, useBusy, useQuery } from '../api/hooks'
import type { NotificationChannel, NotificationSettings as NS } from '../api/types'

const TYPES: { k: string; l: string; i: React.ReactNode }[] = [
  { k: 'telegram', l: 'Telegram', i: <MessageCircle size={15} /> },
  { k: 'webhook', l: 'Webhook', i: <Webhook size={15} /> },
  { k: 'email', l: L("邮件 (SMTP)"), i: <Mail size={15} /> },
]
const MINE_TYPES = TYPES.filter(t => t.k !== 'email')
const typeOf = (k: string) => TYPES.find(t => t.k === k) ?? TYPES[1]
const blank = (type = 'telegram'): NotificationChannel => ({ id: '', type, enabled: true, name: '' })

function target(c: NotificationChannel) {
  if (c.type === 'telegram') return c.chat_id ? `chat ${c.chat_id}` : ''
  if (c.type === 'email') return c.to ?? ''
  return c.has_url ? L("URL 已保存") : ''
}

/** Settings → 通知: where problems (offline servers, full disks, failing services, failed
 *  deploys/backups) are sent, quiet hours, and recovery messages. `mine`: a sub-account's own
 *  channels (/me/notifications, webhook or Telegram, no quiet hours), which get only the
 *  problems of its servers and projects. */
export default function NotificationSettings({ mine = false }: { mine?: boolean }) {
  const key = mine ? 'me:notifications' : 'notifications'
  const q = useQuery<NS>(key, mine ? async () => ({ notify_resolved: true, ...(await api.myNotifications()) }) : api.notifications)
  const [edit, setEdit] = useState<{ i: number; c: NotificationChannel }>()
  const [del, setDel] = useState<number>()
  const [busy, wrap] = useBusy()
  const n = q.data
  const put = (next: NS) => mine ? api.putMyNotifications(next) : api.putNotifications(next)
  const save = (next: NS, ok = L("通知设置已保存")) => wrap(() => run(() => put(next), { ok, invalidate: [key] }))
  return (
    <div className="mt-8 mb-8">
      <div className="text-[15px] font-semibold flex items-center gap-2 mb-3"><Bell size={17} />{L("通知")}<Hint>{mine ? L("你的服务器离线或磁盘快满、你的项目里服务异常、部署或备份失败、域名配置失败时发到这里；恢复后再发一条。管理员的通知渠道照常收到全部异常。") : L("服务器离线超过 2 分钟、磁盘用到 90%、服务持续异常 3 分钟、部署、备份或备份校验失败、域名配置失败时发送通知；恢复后再发一条。同一个问题只通知一次。")}</Hint></div>
      <Card className="divide-y divide-line">
        {!n && <div className="px-6 py-5 text-[13px] text-dim">{q.error ? L("加载失败：{0}", q.error.message) : L("加载中…")}</div>}
        {n && !n.channels.length && <div className="px-6 py-5 text-[13px] text-muted">{L("还没有通知渠道，异常只会显示在面板上。")}</div>}
        {n?.channels.map((c, i) => (
          <div key={c.id} className="px-4 @2xl:px-6 py-3.5 flex flex-wrap items-center gap-3 text-[13px]">
            <span className="text-muted">{typeOf(c.type).i}</span>
            <span className="font-semibold">{c.name || typeOf(c.type).l}</span>
            <span title={target(c)} className="text-dim truncate min-w-0 flex-1 font-mono text-[12px] @max-2xl:basis-full @max-2xl:order-last @max-2xl:whitespace-normal @max-2xl:break-all">{target(c)}</span>
            {!c.enabled && <Badge tone="zinc">{L("已停用")}</Badge>}
            <TestButton c={c} mine={mine} />
            <button title={L("编辑")} onClick={() => setEdit({ i, c })} className="text-muted hover:text-fg"><Pencil size={15} /></button>
            <button title={L("删除")} onClick={() => setDel(i)} className="text-muted hover:text-danger"><Trash2 size={15} /></button>
          </div>
        ))}
        {n && <div className="px-4 @2xl:px-6 py-3"><Button size="sm" onClick={() => setEdit({ i: -1, c: blank() })}><Plus size={14} />{L("添加渠道")}</Button></div>}
        {n && !mine && <Quiet n={n} busy={busy} onSave={v => save({ ...n, ...v })} />}
      </Card>
      {edit && n && <ChannelEditor mine={mine} init={edit.c} onClose={() => setEdit(undefined)} onSave={async c => {
        const next = n.channels.slice(); if (edit.i < 0) next.push(c); else next[edit.i] = c
        const r = await run(() => put({ ...n, channels: next }), { ok: L("渠道已保存"), invalidate: [key] })
        if (r) setEdit(undefined)
      }} />}
      {del !== undefined && n && <ConfirmDialog title={L("删除通知渠道")} action={L("删除")} onClose={() => setDel(undefined)}
        description={<>{L("不再向「")}{n.channels[del]?.name || typeOf(n.channels[del]?.type).l}{L("」发送通知。")}</>}
        onConfirm={() => save({ ...n, channels: n.channels.filter((_, j) => j !== del) }, L("渠道已删除"))} />}
    </div>
  )
}

const HHMM = /^([01]\d|2[0-3]):[0-5]\d$/
/** 24-hour HH:MM (a native time input follows the browser locale and may show AM/PM). */
function TimeField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const bad = value !== '' && !HHMM.test(value)
  return <TextInput mono inputMode="numeric" maxLength={5} placeholder="23:00" className={cx('w-[84px] text-center', bad && 'ring-2 ring-danger')} value={value}
    onChange={e => { let v = e.target.value.replace(/[^\d:]/g, ''); if (/^\d{4}$/.test(v)) v = `${v.slice(0, 2)}:${v.slice(2)}`; onChange(v) }} />
}

function Quiet({ n, busy, onSave }: { n: NS; busy: boolean; onSave: (v: Partial<NS>) => void }) {
  const [on, setOn] = useState(!!n.quiet_start)
  const [start, setStart] = useState(n.quiet_start || '23:00')
  const [end, setEnd] = useState(n.quiet_end || '08:00')
  const [tz] = useState(n.timezone || Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC')
  const [resolved, setResolved] = useState(n.notify_resolved)
  const changed = on !== !!n.quiet_start || (on && (start !== n.quiet_start || end !== n.quiet_end)) || resolved !== n.notify_resolved
  return (
    <div className="px-4 @2xl:px-6 py-4 space-y-3 text-[13px]">
      <div className="flex flex-wrap items-center gap-3">
        <Toggle on={resolved} onChange={setResolved} /><span>{L("恢复时也发送通知")}</span>
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <Toggle on={on} onChange={setOn} /><span>{L("静默时段")}</span>
        {on && <>
          <TimeField value={start} onChange={setStart} />
          <span className="text-muted">{L("到")}</span>
          <TimeField value={end} onChange={setEnd} />
          <span className="text-dim">{tz}</span>
        </>}
        <Button className="ml-auto" size="sm" variant="primary" disabled={!changed || busy || (on && !(HHMM.test(start) && HHMM.test(end)))}
          onClick={() => onSave({ notify_resolved: resolved, quiet_start: on ? start : '', quiet_end: on ? end : '', timezone: on ? tz : '' })}>
          {busy ? <Loader2 size={14} className="animate-spin" /> : <Save size={14} />}{L("保存")}
        </Button>
      </div>
      {on && <div className="text-[12px] text-dim">{L("静默时段内只发送服务器离线；其他仍未恢复的问题在静默结束后发送。")}</div>}
    </div>
  )
}

function TestButton({ c, mine }: { c: NotificationChannel; mine: boolean }) {
  const [busy, wrap] = useBusy()
  return <Button size="sm" disabled={busy} onClick={() => wrap(() => run(() => mine ? api.testMyNotification(c) : api.testNotification(c), { ok: L("测试通知已发送") }))}>
    {busy ? <Loader2 size={13} className="animate-spin" /> : <Send size={13} />}{L("测试")}
  </Button>
}

function ChannelEditor({ init, mine, onClose, onSave }: { init: NotificationChannel; mine: boolean; onClose: () => void; onSave: (c: NotificationChannel) => Promise<void> }) {
  const [c, setC] = useState<NotificationChannel>(init)
  const [busy, wrap] = useBusy()
  const set = <K extends keyof NotificationChannel>(k: K) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setC(x => ({ ...x, [k]: k === 'smtp_port' ? Number(e.target.value) || undefined : e.target.value }))
  const isNew = !init.id
  const ok = c.type === 'telegram' ? (!!c.chat_id && (!!c.bot_token || !!c.has_bot_token))
    : c.type === 'webhook' ? (/^https?:\/\//.test(c.url ?? '') || (!c.url && !!c.has_url))
    : !!c.smtp_host && !!c.from && !!c.to
  const keep = (has?: boolean) => has ? L("已保存，留空保持不变") : ''
  return (
    <Modal title={<><Bell size={17} />{isNew ? L("添加通知渠道") : L("编辑通知渠道")}</>} onClose={onClose} width={560}
      footer={<><TestButton c={c} mine={mine} /><span className="flex-1" /><Button size="sm" onClick={onClose}>{L("取消")}</Button>
        <Button size="sm" variant="primary" disabled={!ok || busy} onClick={() => wrap(() => onSave(c))}>{busy && <Loader2 size={14} className="animate-spin" />}{L("保存")}</Button></>}>
      <div className="grid grid-cols-2 gap-x-4">
        <Label t={L("类型")}><Select className="w-full" value={c.type} disabled={!isNew} onChange={set('type')}>{(mine ? MINE_TYPES : TYPES).map(t => <option key={t.k} value={t.k}>{t.l}</option>)}</Select></Label>
        <Label t={L("名称")}><TextInput className="w-full" value={c.name ?? ''} onChange={set('name')} placeholder={typeOf(c.type).l} /></Label>
      </div>
      {c.type === 'telegram' && <>
        <Label t="Bot Token" d={L("在 @BotFather 创建机器人后获得；只写")}>
          <TextInput mono className="w-full" value={c.bot_token ?? ''} onChange={set('bot_token')} placeholder={keep(c.has_bot_token) || '123456:ABC-DEF…'} autoComplete="off" />
        </Label>
        <Label t="Chat ID" d={L("私聊填你的用户 ID；群组以 -100 开头。先给机器人发一条消息或把它拉进群。")}>
          <TextInput mono className="w-full" value={c.chat_id ?? ''} onChange={set('chat_id')} placeholder="-1001234567890" />
        </Label>
      </>}
      {c.type === 'webhook' && <Label t="URL" d={L("POST JSON：{\"event\":\"firing|resolved|test\",\"problem\":{…},\"text\":\"…\"}。URL 常带令牌，只写。")}>
        <TextInput mono className="w-full" value={c.url ?? ''} onChange={set('url')} placeholder={keep(c.has_url) || 'https://hooks.example.com/atoll'} autoComplete="off" />
      </Label>}
      {c.type === 'email' && <>
        <div className="grid grid-cols-[minmax(0,1fr)_110px] gap-x-4">
          <Label t={L("SMTP 服务器")}><TextInput mono className="w-full" value={c.smtp_host ?? ''} onChange={set('smtp_host')} placeholder="smtp.example.com" /></Label>
          <Label t={L("端口")} d="465 = SSL"><TextInput mono type="number" className="w-full" value={c.smtp_port ?? ''} onChange={set('smtp_port')} placeholder="587" /></Label>
        </div>
        <div className="grid grid-cols-2 gap-x-4">
          <Label t={L("用户名")}><TextInput mono className="w-full" value={c.username ?? ''} onChange={set('username')} autoComplete="off" /></Label>
          <Label t={L("密码")} d={L("只写")}><TextInput mono type="password" className="w-full" value={c.password ?? ''} onChange={set('password')} placeholder={keep(c.has_password)} autoComplete="new-password" /></Label>
        </div>
        <Label t={L("发件人")}><TextInput mono className="w-full" value={c.from ?? ''} onChange={set('from')} placeholder="Atoll <atoll@example.com>" /></Label>
        <Label t={L("收件人")} d={L("多个用逗号分隔")}><TextInput mono className="w-full" value={c.to ?? ''} onChange={set('to')} placeholder="ops@example.com" /></Label>
      </>}
      <div className="flex items-center gap-3 text-[13px]"><Toggle on={c.enabled} onChange={v => setC(x => ({ ...x, enabled: v }))} />{L("启用")}</div>
    </Modal>
  )
}
