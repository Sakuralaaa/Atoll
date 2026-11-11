// Applies the light/dark mode and the preset theme before the first paint, so a reload never
// flashes the default look. A plain file (the CSP has no 'unsafe-inline'); src/theme.ts owns the
// logic after start-up and keeps the same keys and lists.
(function () {
  try {
    var d = document.documentElement, q = new URLSearchParams(location.search)
    var styles = ['atoll', 'minimal', 'lagoon', 'macaron', 'contrast'], renamed = { zeabur: 'lagoon', terminal: 'atoll' }
    var m = q.get('theme'); if (m !== 'light' && m !== 'dark') m = localStorage.getItem('atoll-theme')
    if (m !== 'light' && m !== 'dark') m = matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
    d.classList.remove('dark', 'light'); d.classList.add(m)
    var s = q.get('style'); s = renamed[s] || s; if (styles.indexOf(s) < 0) s = localStorage.getItem('atoll-style')
    s = renamed[s] || s
    d.setAttribute('data-theme', styles.indexOf(s) < 0 ? 'atoll' : s)
  } catch { /* storage blocked: keep the defaults */ }
})()
