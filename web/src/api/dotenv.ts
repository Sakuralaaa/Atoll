import type { EnvVar } from './types'

// .env text, the same rules as the CLI (internal/cli/commands_env_logs.go: parseDotenv,
// formatDotenv), so a file exported here imports with `atoll env import` and the other way round.

/** parseEnvText reads KEY=VALUE lines like a .env file: blank lines and # comments are skipped, a
 *  leading `export` is dropped, quoted values are unwrapped ("..." understands \n, \" and \\), and
 *  an unquoted value loses a trailing " # comment". Bad lines are reported (1-based), later keys win. */
export function parseEnvText(text: string): { vars: { key: string; value: string }[]; errors: { line: number; text: string }[] } {
  const out = new Map<string, string>(); const errors: { line: number; text: string }[] = []
  text.split('\n').forEach((raw, n) => {
    let l = raw.trim()
    if (!l || l.startsWith('#')) return
    if (l.startsWith('export ')) l = l.slice(7).trim()
    const i = l.indexOf('=')
    const key = i > 0 ? l.slice(0, i).trim() : ''
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) { errors.push({ line: n + 1, text: raw.trim().slice(0, 40) }); return }
    let value = l.slice(i + 1).trim()
    const q = value[0]
    if (value.length >= 2 && (q === '"' || q === "'") && value.endsWith(q)) {
      value = value.slice(1, -1)
      if (q === '"') value = value.replace(/\\(n|"|\\)/g, (_, c: string) => c === 'n' ? '\n' : c)
    } else {
      const j = value.indexOf(' #'); if (j >= 0) value = value.slice(0, j).trim()
    }
    out.delete(key); out.set(key, value)
  })
  return { vars: [...out].map(([key, value]) => ({ key, value })), errors }
}

/** A bare value has no whitespace, #, quote or backslash; anything else is double-quoted with
 *  \\, \" and \n escapes (exactly what parseEnvText undoes). */
export function quoteEnvValue(v: string): string {
  if (!/[\s\u0085\ufeff#"'\\]/.test(v)) return v
  return `"${v.replace(/[\\"\n]/g, c => c === '\n' ? '\\n' : `\\${c}`)}"`
}

/** formatEnvText writes vars as .env lines. A secret without its value (not revealed, or
 *  includeSecrets off) becomes a comment line so the file never carries an empty stand-in. */
export function formatEnvText(vars: EnvVar[], includeSecrets: boolean): { text: string; omitted: number } {
  let omitted = 0
  const lines = vars.map(v => {
    if (v.secret && !includeSecrets) { omitted++; return `# ${v.key}: secret, value not exported` }
    return `${v.key}=${quoteEnvValue(v.value)}`
  })
  return { text: lines.length ? lines.join('\n') + '\n' : '', omitted }
}
