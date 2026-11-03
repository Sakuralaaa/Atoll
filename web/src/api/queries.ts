import { api } from './client'
import { useQuery } from './hooks'
import type { Project } from './types'

/** Shared queries: one cache key per resource, reused across pages. */
export const usePending = () => useQuery('confirmations:pending', () => api.confirmations('pending'), { interval: 30000 })
export const useProjects = () => useQuery('projects', api.projectsFull)
export const useProject = (pid?: string) => useQuery(pid ? `project:${pid}` : null, () => api.project(pid!))
export const placeholder = (id = ''): Project => ({ id, name: '…', server_id: '', created_at: '' })
