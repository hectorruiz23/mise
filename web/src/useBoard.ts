import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, ApiError } from './api'
import type { BoardData, TaskDetail } from './types'

type DetailMap = Record<string, TaskDetail>

const STALE_MS = 15_000

export function useBoard(active: boolean) {
  const [board, setBoard] = useState<BoardData | null>(null)
  const [details, setDetails] = useState<DetailMap>({})
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<ApiError | null>(null)
  const generation = useRef(0)
  const lastLoad = useRef(0)
  const loaded = useRef(false)

  const load = useCallback(async () => {
    const current = ++generation.current
    if (!loaded.current) setLoading(true)

    try {
      const nextBoard = await api.board()
      const inProgress = nextBoard.columns.in_progress ?? []
      const blocked = nextBoard.columns.blocked ?? []
      const withDetails = [...inProgress, ...blocked]
      const settled = await Promise.allSettled(
        withDetails.map(async task => [task.key, await api.detail(task.key)] as const),
      )
      if (current !== generation.current) return

      const nextDetails: DetailMap = {}
      for (const result of settled) {
        if (result.status === 'fulfilled') nextDetails[result.value[0]] = result.value[1]
      }

      setBoard(nextBoard)
      setDetails(nextDetails)
      setError(null)
      loaded.current = true
      lastLoad.current = Date.now()
    } catch (caught) {
      if (current !== generation.current) return
      setError(caught instanceof ApiError ? caught : new ApiError(0, 'network', 'Cannot reach the server.'))
    } finally {
      if (current === generation.current) setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (!active) return
    if (Date.now() - lastLoad.current > STALE_MS) void load()
    const onFocus = () => void load()
    window.addEventListener('focus', onFocus)
    return () => {
      window.removeEventListener('focus', onFocus)
      generation.current++
    }
  }, [active, load])

  const mutate = useCallback(
    async <T,>(operation: () => Promise<T>): Promise<T> => {
      try {
        return await operation()
      } finally {
        await load()
      }
    },
    [load],
  )

  const tasks = useMemo(
    () => (board ? Object.values(board.columns).flat() : []),
    [board],
  )

  return {
    board,
    details,
    tasks,
    loading,
    error,
    reload: load,
    mutate,
    detailFor: (key: string) => details[key],
  }
}
