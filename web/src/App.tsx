import { Suspense, useEffect } from 'react'
import { Routes, Route, Navigate, useLocation, useNavigate } from 'react-router-dom'
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
const ImportDocker = lazyPage(() => import('./pages/ImportDocker'))
const Settings = lazyPage(() => import('./pages/Settings'))
const Security = lazyPage(() => import('./pages/Security'))
const Users = lazyPage(() => import('./pages/Users'))
const Confirmations = lazyPage(() => import('./pages/Confirmations'))
const Control = lazyPage(() => import('./pages/Control'))
const Onboarding = lazyPage(() => import('./pages/Onboarding'))

import { Toaster } from './components/feedback'
import { CommandPalette } from './components/CommandPalette'
import { api } from './api/client'
import { useQuery } from './api/hooks'
import { useLiveEvents } from './api/ws'
import { useTrackBackTo } from './api/backTo'
import { onboardingRequested, readOnboarding, startOnboarding } from './onboarding'

// ?onboarding=1 (e.g. the demo link) restarts the first-run wizard once per page load
let onboardingParamHandled = false

export default function App() {
  const { pathname } = useLocation()
  const onLogin = pathname === '/login'
  // a 401 here redirects to #/login (client.ts); the live socket only runs once we know who we are
  const me = useQuery(onLogin ? null : 'me', api.me)
  useLiveEvents(!onLogin && !!me.data)
  useTrackBackTo()
  // first-run wizard: opens on the projects page while it is active for this account (onboarding.ts)
  const nav = useNavigate()
  const user = me.data?.username
  const admin = !!me.data && me.data.role !== 'member'
  useEffect(() => {
    if (onLogin || !user || !admin) return
    if (!onboardingParamHandled && onboardingRequested()) {
      onboardingParamHandled = true
      startOnboarding(user)
      if (pathname !== '/welcome') nav('/welcome', { replace: true })
      return
    }
    if (pathname === '/' && readOnboarding(user)?.status === 'active') nav('/welcome', { replace: true })
  }, [onLogin, user, admin, pathname, nav])
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
        <Route path="/welcome" element={<Onboarding />} />
        <Route path="/domains" element={<Domains />} />
        <Route path="/developer" element={<Developer />} />
        <Route path="/confirmations" element={<Confirmations />} />
        <Route path="/import" element={<Import />} />
        <Route path="/import/docker" element={<ImportDocker />} />
        <Route path="/settings" element={<Settings />} />
        <Route path="/settings/security" element={<Security />} />
        <Route path="/settings/users" element={<Users />} />
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
