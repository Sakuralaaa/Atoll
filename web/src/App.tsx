import { Suspense } from 'react'
import { Routes, Route, Navigate, useLocation } from 'react-router-dom'
import Projects from './pages/Projects'
import Login from './pages/Login'
import { lazyPage } from './chunkReload'
import { RouteBoundary } from './components/RouteBoundary'
const Servers = lazyPage(() => import('./pages/Servers'))
const ServerDetail = lazyPage(() => import('./pages/ServerDetail'))
const ServicePage = lazyPage(() => import('./pages/ServicePage'))
const LogsPage = lazyPage(() => import('./pages/LogsPage'))
const FilesPage = lazyPage(() => import('./pages/FilesPage'))
const Domains = lazyPage(() => import('./pages/Domains'))
const Developer = lazyPage(() => import('./pages/Developer'))
const ProjectSettings = lazyPage(() => import('./pages/ProjectSettings'))
const ProjectIndex = lazyPage(() => import('./pages/ProjectIndex'))
const Import = lazyPage(() => import('./pages/Import'))
const Settings = lazyPage(() => import('./pages/Settings'))
const Security = lazyPage(() => import('./pages/Security'))
const Confirmations = lazyPage(() => import('./pages/Confirmations'))
const Control = lazyPage(() => import('./pages/Control'))

import { Toaster } from './components/feedback'
import { CommandPalette } from './components/CommandPalette'
import { api } from './api/client'
import { useQuery } from './api/hooks'
import { useLiveEvents } from './api/ws'

export default function App() {
  const { pathname } = useLocation()
  const onLogin = pathname === '/login'
  // a 401 here redirects to #/login (client.ts); the live socket only runs once we know who we are
  const me = useQuery(onLogin ? null : 'me', api.me)
  useLiveEvents(!onLogin && !!me.data)
  return (
    <>
      {/* pages load on first visit; Projects and Login stay in the entry chunk */}
      {/* a page that fails to load or render shows a retry; navigating elsewhere clears it */}
      <RouteBoundary resetKey={pathname}>
      <Suspense fallback={null}>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="/" element={<Projects />} />
        <Route path="/servers" element={<Servers />} />
        <Route path="/servers/:id" element={<ServerDetail />} />
        <Route path="/control" element={<Control />} />
        <Route path="/domains" element={<Domains />} />
        <Route path="/developer" element={<Developer />} />
        <Route path="/confirmations" element={<Confirmations />} />
        <Route path="/import" element={<Import />} />
        <Route path="/settings" element={<Settings />} />
        <Route path="/settings/security" element={<Security />} />
        <Route path="/projects" element={<Navigate replace to="/" />} />
        <Route path="/projects/:pid" element={<ProjectIndex />} />
        <Route path="/projects/:pid/settings" element={<ProjectSettings />} />
        <Route path="/projects/:pid/services/:sid" element={<ServicePage />} />
        <Route path="/projects/:pid/services/:sid/logs" element={<LogsPage />} />
        <Route path="/projects/:pid/services/:sid/files" element={<FilesPage />} />
        <Route path="*" element={<Navigate replace to="/" />} />
      </Routes>
      </Suspense>
      </RouteBoundary>
      <CommandPalette />
      <Toaster />
    </>
  )
}
