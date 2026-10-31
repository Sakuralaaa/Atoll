import { Routes, Route, Navigate, useLocation } from 'react-router-dom'
import Projects from './pages/Projects'
import Servers from './pages/Servers'
import ServerDetail from './pages/ServerDetail'
import ServicePage from './pages/ServicePage'
import LogsPage from './pages/LogsPage'
import FilesPage from './pages/FilesPage'
import Domains from './pages/Domains'
import Developer from './pages/Developer'
import ProjectSettings from './pages/ProjectSettings'
import ProjectIndex from './pages/ProjectIndex'
import Import from './pages/Import'
import Login from './pages/Login'
import Settings from './pages/Settings'
import Security from './pages/Security'
import Confirmations from './pages/Confirmations'
import { Toaster } from './components/feedback'
import { api } from './api/client'
import { useQuery } from './api/hooks'
import { useLiveEvents } from './api/ws'

export default function App() {
  const onLogin = useLocation().pathname === '/login'
  // a 401 here redirects to #/login (client.ts); the live socket only runs once we know who we are
  const me = useQuery(onLogin ? null : 'me', api.me)
  useLiveEvents(!onLogin && !!me.data)
  return (
    <>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="/" element={<Projects />} />
        <Route path="/servers" element={<Servers />} />
        <Route path="/servers/:id" element={<ServerDetail />} />
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
      <Toaster />
    </>
  )
}
