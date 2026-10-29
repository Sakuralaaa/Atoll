import { Routes, Route } from 'react-router-dom'
import Projects from './pages/Projects'
import Servers from './pages/Servers'
import ServerDetail from './pages/ServerDetail'
import ServicePage from './pages/ServicePage'
import LogsPage from './pages/LogsPage'
import FilesPage from './pages/FilesPage'
import Domains from './pages/Domains'
import Developer from './pages/Developer'
import ProjectSettings from './pages/ProjectSettings'

export default function App() {
  return (
    <Routes>
      <Route path="/" element={<Projects />} />
      <Route path="/servers" element={<Servers />} />
      <Route path="/servers/:id" element={<ServerDetail />} />
      <Route path="/domains" element={<Domains />} />
      <Route path="/developer" element={<Developer />} />
      <Route path="/projects/:pid/settings" element={<ProjectSettings />} />
      <Route path="/projects/:pid/services/:sid" element={<ServicePage />} />
      <Route path="/projects/:pid/services/:sid/logs" element={<LogsPage />} />
      <Route path="/projects/:pid/services/:sid/files" element={<FilesPage />} />
    </Routes>
  )
}
