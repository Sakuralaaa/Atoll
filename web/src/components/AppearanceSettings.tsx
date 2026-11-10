import { useEffect, useRef, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { Check, Moon, Palette, Sun } from 'lucide-react'
import { Card } from './ui'
import { cx } from './ui-utils'
import { toast } from './toast'
import { api, ApiError } from '../api/client'
import { useT } from '../i18n'
import { applyStyle, applyTheme, STYLES, useLook, type Style, type Theme } from '../theme'

type Swatch = { bg: string; card: string; edge: string; brand: string; fg: string; muted: string; hi: string }
type Preview = { name: [string, string]; desc: [string, string]; radius: number; bw: number; shadow: (s: Swatch) => string; mono?: boolean; italic?: boolean; light: Swatch; dark: Swatch }

// Hand-kept miniatures of each theme (the real colours live in index.css / themes/*.css, which
// only apply on <html>, so a preview cannot borrow them).
const PREVIEW: Record<Style, Preview> = {
  atoll: { name: ['Atoll', 'Atoll'], desc: ['默认 · 粗边框、硬阴影、斜体', 'Default · bold borders, hard shadows'], radius: 4, bw: 2, italic: true,
    shadow: s => `3px 3px 0 0 ${s.edge}`,
    light: { bg: '#f3efe6', card: '#fffdf8', edge: '#141414', brand: '#a78bfa', fg: '#141414', muted: '#8c887f', hi: '#fde047' },
    dark: { bg: '#1f1e26', card: '#2c2b35', edge: '#0c0b10', brand: '#8b5cf6', fg: '#f1f0ec', muted: '#6c6c76', hi: '#fde047' } },
  minimal: { name: ['极简', 'Minimal'], desc: ['细线、柔和阴影、大圆角', 'Hairlines, soft shadows, round corners'], radius: 9, bw: 1,
    shadow: () => '0 1px 3px rgba(0,0,0,.12)',
    light: { bg: '#fafafa', card: '#ffffff', edge: '#e4e4e7', brand: '#18181b', fg: '#09090b', muted: '#a1a1aa', hi: '#f4f4f5' },
    dark: { bg: '#0a0a0a', card: '#111111', edge: '#262626', brand: '#fafafa', fg: '#fafafa', muted: '#71717a', hi: '#27272a' } },
  zeabur: { name: ['Zeabur', 'Zeabur'], desc: ['紫色品牌、网格与顶部紫光', 'Purple, grid texture, top glow'], radius: 6, bw: 1,
    shadow: () => '0 4px 14px -6px rgba(99,0,255,.35)',
    light: { bg: '#fbfaff', card: '#ffffff', edge: '#e4def5', brand: '#6300ff', fg: '#18122b', muted: '#938cad', hi: '#ede5ff' },
    dark: { bg: '#0c0a13', card: '#14111f', edge: '#2b2544', brand: '#7b2fff', fg: '#f4f2ff', muted: '#78719a', hi: '#231c3d' } },
  terminal: { name: ['终端', 'Terminal'], desc: ['等宽字体、直角、磷光绿', 'Monospace, square, phosphor green'], radius: 0, bw: 1, mono: true,
    shadow: () => 'none',
    light: { bg: '#eef3ea', card: '#f7faf4', edge: '#527a49', brand: '#1a7f1f', fg: '#0e2a0b', muted: '#6c8762', hi: '#d4e8cc' },
    dark: { bg: '#050a05', card: '#081108', edge: '#1f5a29', brand: '#33ff66', fg: '#b8ffb4', muted: '#4f9453', hi: '#0f2e16' } },
  macaron: { name: ['马卡龙', 'Macaron'], desc: ['粉、薄荷、薰衣草，圆润柔和', 'Pink, mint, lavender, soft and round'], radius: 12, bw: 1,
    shadow: () => '0 6px 14px -6px rgba(236,72,153,.45)',
    light: { bg: '#fdf6f9', card: '#fffdfe', edge: '#f0d3e1', brand: '#f9a8d4', fg: '#3b2638', muted: '#a88ba0', hi: '#b9f0dc' },
    dark: { bg: '#211a29', card: '#2b2235', edge: '#4a3b59', brand: '#f9a8d4', fg: '#fbeff7', muted: '#94809a', hi: '#9de6cf' } },
  contrast: { name: ['高对比', 'High contrast'], desc: ['纯黑白、3px 边框、黄色焦点', 'Black and white, 3px borders, yellow focus'], radius: 3, bw: 3,
    shadow: () => 'none',
    light: { bg: '#ffffff', card: '#ffffff', edge: '#000000', brand: '#000000', fg: '#000000', muted: '#3d3d3d', hi: '#ffe600' },
    dark: { bg: '#000000', card: '#000000', edge: '#ffffff', brand: '#ffffff', fg: '#ffffff', muted: '#c4c4c4', hi: '#ffe600' } },
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
          <span className="self-start h-4 px-2.5 grid place-items-center text-[8px] font-bold leading-none" style={{ background: s.brand, borderRadius: p.radius, border: `${Math.min(p.bw, 2)}px solid ${s.edge}`, color: s.bg, fontStyle: p.italic ? 'italic' : 'normal' }}>Aa</span>
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
  const ref = useRef<HTMLDivElement>(null)
  const loc = useLocation()
  useEffect(() => { if (loc.hash === '#appearance') ref.current?.scrollIntoView({ block: 'start' }) }, [loc.hash])
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
    <div ref={ref} id="appearance" className="mb-8 scroll-mt-6">
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
