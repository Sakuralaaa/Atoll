import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { HashRouter } from 'react-router-dom'
import './index.css'
import App from './App.tsx'
import { applyStyle, applyTheme, initialStyle, initialTheme } from './theme'
import { installPreloadErrorReload } from './chunkReload'

applyTheme(initialTheme())
applyStyle(initialStyle(), false)
installPreloadErrorReload()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <HashRouter>
      <App />
    </HashRouter>
  </StrictMode>,
)
