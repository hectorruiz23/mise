import { useCallback, useEffect, useState } from 'react'
import { api } from './api'
import type { Project } from './types'

export function useProjects(enabled: boolean) {
  const [projects, setProjects] = useState<Project[]>([])

  const load = useCallback(async () => {
    try {
      setProjects(await api.projects())
    } catch {
    }
  }, [])

  useEffect(() => {
    if (enabled) void load()
  }, [enabled, load])

  return { projects, reload: load }
}
