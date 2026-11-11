import { useState } from 'react'
import { Check, Moon, Palette, Sun } from 'lucide-react'
import { Card } from './ui'
import { cx } from './ui-utils'
import { toast } from './toast'
import { api, ApiError } from '../api/client'
import { useT } from '../i18n'
import { applyStyle, applyTheme, STYLES, useLook, type Style, type Theme } from '../theme'

type Swatch = { bg: string; card: string; edge: string; brand: string; ink: string; fg: string; muted: string; hi: string }
type Preview = { name: [string, string]; desc: [string, string]; radius: number; bw: number; shadow: (s: Swatch) => string; mono?: boolean; italic?: boolean; light: Swatch; dark: Swatch }

// Hand-kept miniatures of each theme (the real colours live in index.css / themes/*.css, which
// only apply on <html>, so a preview cannot borrow them).
const PREVIEW: Record<Style, Preview> = {
  atoll: { name: ['Atoll', 'Atoll'], desc: ['默认 · 粗边框、硬阴影、斜体', 'Default · bold, hard shadows'], radius: 4, bw: 2, italic: true,
    shadow: s => `3px 3px 0 0 ${s.edge}`,
    light: { bg: '#f3efe6', card: '#fffdf8', edge: '#141414', brand: '#a78bfa', ink: '#141414', fg: '#141414', muted: '#8c887f', hi: '#fde047' },
    dark: { bg: '#1f1e26', card: '#2c2b35', edge: '#0c0b10', brand: '#8b5cf6', ink: '#ffffff', fg: '#f1f0ec', muted: '#6c6c76', hi: '#fde047' } },
  minimal: { name: ['极简', 'Minimal'], desc: ['细线、柔和阴影、大圆角', 'Hairlines, soft, rounded'], radius: 9, bw: 1,
    shadow: () => '0 1px 3px rgba(0,0,0,.12)',
    light: { bg: '#fafafa', card: '#ffffff', edge: '#e4e4e7', brand: '#18181b', ink: '#ffffff', fg: '#09090b', muted: '#a1a1aa', hi: '#f4f4f5' },
    dark: { bg: '#0a0a0a', card: '#111111', edge: '#262626', brand: '#fafafa', ink: '#09090b', fg: '#fafafa', muted: '#71717a', hi: '#27272a' } },
  lagoon: { name: ['潟湖', 'Lagoon'], desc: ['块状 · 清爽绿、夜间低眩光', 'Blocky · fresh green, calm at night'], radius: 4, bw: 2, italic: true,
    shadow: s => `3px 3px 0 0 ${s.edge}`,
    light: { bg: '#eef3ea', card: '#f7faf4', edge: '#2c4a27', brand: '#1a7f1f', ink: '#f2fff0', fg: '#0e2a0b', muted: '#6c8762', hi: '#d4e8cc' },
    dark: { bg: '#1c211d', card: '#232924', edge: '#0d110e', brand: '#8db68f', ink: '#182019', fg: '#c3cdc1', muted: '#6f7a6e', hi: '#2e382f' } },
  terminal: { name: ['终端', 'Terminal'], desc: ['等宽字体、直角、柔和绿', 'Mono, square, soft green'], radius: 0, bw: 1, mono: true,
    shadow: () => 'none',
    light: { bg: '#eef3ea', card: '#f7faf4', edge: '#527a49', brand: '#1a7f1f', ink: '#f2fff0', fg: '#0e2a0b', muted: '#6c8762', hi: '#d4e8cc' },
    dark: { bg: '#0f1411', card: '#141b17', edge: '#2a3a30', brand: '#6fbf7f', ink: '#0b1a0f', fg: '#d3e2d6', muted: '#687d6f', hi: '#1b2a20' } },
  macaron: { name: ['马卡龙', 'Macaron'], desc: ['块状 · 玫红', 'Blocky · rose red'], radius: 4, bw: 2, italic: true,
    shadow: s => `3px 3px 0 0 ${s.edge}`,
    light: { bg: '#f9eef2', card: '#fdf6f9', edge: '#5a1f38', brand: '#d81b60', ink: '#fff5f8', fg: '#3d2430', muted: '#a5838f', hi: '#f8c9da' },
    dark: { bg: '#231a1e', card: '#2d2228', edge: '#100a0d', brand: '#e94f86', ink: '#2a0a17', fg: '#e6d3db', muted: '#826c77', hi: '#462a37' } },
  contrast: { name: ['高对比', 'High contrast'], desc: ['黑白、2px 边框、黄色焦点', 'B/W, 2px borders, yellow focus'], radius: 3, bw: 2,
    shadow: () => 'none',
    light: { bg: '#ffffff', card: '#ffffff', edge: '#000000', brand: '#000000', ink: '#ffffff', fg: '#000000', muted: '#3d3d3d', hi: '#ffe600' },
    dark: { bg: '#121212', card: '#181818', edge: '#bdbdbd', brand: '#ececec', ink: '#121212', fg: '#ececec', muted: '#ababab', hi: '#2a2a2a' } },
}

/** A tiny page in the theme's colours: sidebar, a card and a button. */
function Mini({ p, mode }: { p: Preview; mode: Theme }) {
  const s = p[mode]
  const box = { borderRadius: p.radius, border: `${Math.min(p.bw, 2)}px solid ${s.edge}` }
  return (
    <div aria-hidden="true" className="h-[86px] w-full flex overflow-hidden" style={{ background: s.bg, borderBottom: `1px solid ${s.edge}`, fontFamily: p.mono ? 'var(--font-mono)' : undefined }}>
      <div className="w-[26%] p-1.5 flex flex-col gap-1" style={{ borderRight: `1px solid ${s.edge}` }}>
        <span className="h-2 w-[80%]" style={{ background: s.fg, borderRadius: p.radius ? 2 : 0, opacity: .85 }} />
        <span className="h-3 mt-1" style={{ ...box, background: s.hi, border: `1px solid ${s.edge}` }} />
        <span className="h-1.5 w-[70%]" style={{ background: s.muted, borderRadius: 2 }} />
        <span className="h-1.5 w-[55%]" style={{ background: s.muted, borderRadius: 2 }} />
      </div>
      <div className="flex-1 p-2 min-w-0">
        <div className="h-full p-2 flex flex-col justify-between" style={{ ...box, background: s.card, boxShadow: p.shadow(s) }}>
          <div>
            <div className="h-2 w-[60%]" style={{ background: s.fg, borderRadius: 2 }} />
            <div className="h-1.5 w-[85%] mt-1.5" style={{ background: s.muted, borderRadius: 2, opacity: .7 }} />
          </div>
          <span className="self-start h-4 px-2.5 grid place-items-center text-[8px] font-bold leading-none" style={{ background: s.brand, borderRadius: p.radius, border: `${Math.min(p.bw, 2)}px solid ${s.edge}`, color: s.ink, fontStyle: p.italic ? 'italic' : 'normal' }}>Aa</span>
        </div>
      </div>
    </div>
  )
}

/** Settings → Account: preset theme (synced to the account) and light / dark (this device). */
export default function AppearanceSettings() {
  const { t, lang } = useT()
  const { mode, style } = useLook()
  const [saving, setSaving] = useState<Style>()
  const i = lang === 'en' ? 1 : 0

  const pick = async (s: Style) => {
    if (s === style) return
    applyStyle(s)
    setSaving(s)
    try { await api.setTheme(s) } catch (x) {
      const e = x as ApiError
      // an older control server has no /me/theme: keep the choice on this device only
      if (e.status !== 404 && e.status !== 405) {
        toast(t('主题已在本机切换，但没能同步到账号：', 'Theme applied here but not saved to the account: ') + (e.message || ''), 'error')
      }
    } finally { setSaving(undefined) }
  }

  return (
    <div id="appearance" className="mb-8 scroll-mt-6">
      <div className="text-[15px] font-semibold flex flex-wrap items-center gap-2 mb-3">
        <span className="shrink-0"><Palette size={17} /></span>{t('外观', 'Appearance')}
        <span className="ml-auto inline-flex rounded-lg field p-[3px] gap-0.5" role="radiogroup" aria-label={t('明暗模式', 'Light or dark')}>
          {(['light', 'dark'] as Theme[]).map(v => (
            <button key={v} type="button" role="radio" aria-checked={mode === v} onClick={() => applyTheme(v)}
              className={cx('h-7 px-2.5 inline-flex items-center gap-1.5 rounded-md text-[12px] font-bold transition-colors', mode === v ? 'bg-hi text-hi-ink border-2 border-[var(--edge)]' : 'text-dim hover:text-fg')}>
              {v === 'light' ? <Sun size={13} /> : <Moon size={13} />}{v === 'light' ? t('白天', 'Light') : t('黑夜', 'Dark')}
            </button>
          ))}
        </span>
      </div>
      <Card>
        <div className="px-4 @2xl:px-6 pt-5 text-[13px] text-muted leading-5">
          {t('主题会保存到账号，在其他设备登录也会使用；白天 / 黑夜按设备分别记住。', 'The theme is saved to your account and follows you to other devices; light / dark is remembered per device.')}
        </div>
        <div role="radiogroup" aria-label={t('预设主题', 'Preset themes')} className="px-4 @2xl:px-6 py-5 grid grid-cols-2 @2xl:grid-cols-3 @5xl:grid-cols-6 gap-3">
          {STYLES.map(s => {
            const p = PREVIEW[s], on = s === style
            return (
              <button key={s} type="button" role="radio" aria-checked={on} data-testid={`theme-${s}`} onClick={() => pick(s)}
                className={cx('text-left rounded-lg overflow-hidden border-2 transition-[transform,box-shadow,border-color] min-w-0',
                  on ? 'border-[var(--edge)] shadow-hard -translate-x-px -translate-y-px' : 'border-line hover:border-[var(--edge)]')}>
                <Mini p={p} mode={mode} />
                <div className="px-2.5 py-2 bg-card">
                  <div className="flex items-center gap-1.5 text-[13px] font-bold min-w-0">
                    <span className="truncate">{p.name[i]}</span>
                    {on && <span className="ml-auto h-[18px] w-[18px] shrink-0 rounded-full bg-pick text-pick-ink grid place-items-center">{saving === s ? <span className="h-1.5 w-1.5 rounded-full bg-current animate-pulse" /> : <Check size={12} strokeWidth={3} />}</span>}
                  </div>
                  <div className="mt-0.5 text-[11.5px] text-dim leading-4 line-clamp-2">{p.desc[i]}</div>
                </div>
              </button>
            )
          })}
        </div>
      </Card>
    </div>
  )
}
