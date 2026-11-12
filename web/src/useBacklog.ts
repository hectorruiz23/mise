import { useCallback, useEffect, useRef, useState } from 'react'
import { api, ApiError } from './api'
import type { Task } from './types'

export const PAGE_SIZE = 200

const STALE_MS = 15_000

export function useBacklog({
  active,
  createdTask,
  onSessionExpired,
}: {
  active: boolean
  createdTask: Task | null
  onSessionExpired: () => void
}) {
  const [rows, setRows] = useState<Task[]>([])
  const [nextCursor, setNextCursor] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<ApiError | null>(null)
  const [hasLoaded, setHasLoaded] = useState(false)
  const [flashKey, setFlashKey] = useState<string | null>(null)
  const generation = useRef(0)
  const lastLoad = useRef(0)
  const loaded = useRef(false)
  const handledCreated = useRef<Task | null>(null)
  const pendingInjection = useRef<Task | null>(null)

  const load = useCallback(async () => {
    const current = ++generation.current
    const inject = pendingInjection.current
    if (!loaded.current) setLoading(true)

    try {
      const page = await api.listTasks({ state: 'backlog', limit: PAGE_SIZE })
      if (current !== generation.current) return

      let nextRows = page.tasks
      if (inject && !page.tasks.some(task => task.key === inject.key)) {
        nextRows = [...page.tasks.slice(0, PAGE_SIZE - 1), inject]
      }

      setRows(nextRows)
      setNextCursor(page.next_cursor)
      setError(null)
      loaded.current = true
      lastLoad.current = Date.now()
      setHasLoaded(true)

      if (inject) {
        pendingInjection.current = null
        setFlashKey(inject.key)
      } else {
        setFlashKey(null)
      }
    } catch (caught) {
      if (current !== generation.current) return
      if (caught instanceof ApiError && caught.status === 401) {
        onSessionExpired()
        return
      }
      setError(caught instanceof ApiError ? caught : new ApiError(0, 'network', 'Cannot reach the server.'))
    } finally {
      if (current === generation.current) setLoading(false)
    }
  }, [onSessionExpired])

  const reload = useCallback(() => {
    pendingInjection.current = null
    return load()
  }, [load])

  useEffect(() => {
    if (!active) return

    if (createdTask && handledCreated.current !== createdTask) {
      handledCreated.current = createdTask
      pendingInjection.current = createdTask
    }

    if (Date.now() - lastLoad.current > STALE_MS) void load()
    const onFocus = () => void reload()
    window.addEventListener('focus', onFocus)
    return () => {
      window.removeEventListener('focus', onFocus)
      generation.current++
    }
  }, [active, createdTask, load, reload])

  useEffect(() => {
    if (error?.status === 401) onSessionExpired()
  }, [error, onSessionExpired])

  return { rows, nextCursor, loading, error, hasLoaded, flashKey, reload }
}
