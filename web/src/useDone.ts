import { useCallback, useEffect, useRef, useState } from 'react'
import { api, ApiError } from './api'
import type { Task } from './types'

export const DONE_PAGE_SIZE = 200

const STALE_MS = 15_000

export function useDone({
  active,
  onSessionExpired,
}: {
  active: boolean
  onSessionExpired: () => void
}) {
  const [rows, setRows] = useState<Task[]>([])
  const [nextCursor, setNextCursor] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<ApiError | null>(null)
  const [hasLoaded, setHasLoaded] = useState(false)
  const generation = useRef(0)
  const lastLoad = useRef(0)
  const loaded = useRef(false)

  const load = useCallback(async () => {
    const current = ++generation.current
    if (!loaded.current) setLoading(true)

    try {
      const page = await api.listTasks({ state: 'done', limit: DONE_PAGE_SIZE })
      if (current !== generation.current) return

      setRows(page.tasks)
      setNextCursor(page.next_cursor)
      setError(null)
      loaded.current = true
      lastLoad.current = Date.now()
      setHasLoaded(true)
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

  const reload = useCallback(() => load(), [load])

  useEffect(() => {
    if (!active) return
    if (Date.now() - lastLoad.current > STALE_MS) void load()
    const onFocus = () => void reload()
    window.addEventListener('focus', onFocus)
    return () => {
      window.removeEventListener('focus', onFocus)
      generation.current++
    }
  }, [active, load, reload])

  useEffect(() => {
    if (error?.status === 401) onSessionExpired()
  }, [error, onSessionExpired])

  return { rows, nextCursor, loading, error, hasLoaded, reload }
}
