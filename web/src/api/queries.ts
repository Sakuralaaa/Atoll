import { api } from './client'
import { useQuery } from './hooks'
import type { Project } from './types'

/** Shared queries: one cache key per resource, reused across pages. */
// members (sub-accounts) cannot read approvals or panel-wide problems: don't poll them
const useAdmin = () => { const me = useQuery('me', api.me).data; return !!me && me.role !== 'member' }
export const usePending = () => useQuery(useAdmin() ? 'confirmations:pending' : null, () => api.confirmations('pending'), { interval: 30000 })
export const useProblems = () => useQuery(useAdmin() ? 'problems' : null, () => api.problems(), { interval: 60000 })
export const useProjects = () => useQuery('projects', api.projectsFull)
export const useProject = (pid?: string) => useQuery(pid ? `project:${pid}` : null, () => api.project(pid!))
export const placeholder = (id = ''): Project => ({ id, name: '…', server_id: '', created_at: '' })
