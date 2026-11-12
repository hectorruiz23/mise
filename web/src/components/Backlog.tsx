import { useEffect, useMemo, useRef, useState } from 'react'
import { api, ApiError } from '../api'
import { formatShortDate, isoDate } from '../dates'
import type { Priority, Size, Task } from '../types'
import { PRIORITY_LABELS, STATE_LABELS } from '../types'

const PRIORITY_RANK: Record<Priority, number> = { low: 0, normal: 1, high: 2 }
const SIZE_RANK: Record<Size, number> = { S: 0, M: 1, L: 2 }

type SortDirection = 'asc' | 'desc'
type SortableColumn = 'key' | 'project' | 'size' | 'created_at'
type BacklogSort =
  | { column: 'priority'; direction: 'desc' }
  | { column: SortableColumn; direction: SortDirection }

const DEFAULT_SORT: BacklogSort = { column: 'priority', direction: 'desc' }

function compareTasks(left: Task, right: Task, sort: BacklogSort): number {
  let result = 0

  switch (sort.column) {
    case 'priority':
      result = PRIORITY_RANK[left.priority] - PRIORITY_RANK[right.priority]
      break
    case 'key':
      result = left.key.localeCompare(right.key, 'en', { numeric: true })
      break
    case 'project':
      result = left.project.localeCompare(right.project, 'en', { numeric: true })
      break
    case 'size':
      if (left.size === null && right.size === null) {
        result = 0
      } else if (left.size === null) {
        return 1
      } else if (right.size === null) {
        return -1
      } else {
        result = SIZE_RANK[left.size] - SIZE_RANK[right.size]
      }
      break
    case 'created_at':
      result = left.created_at - right.created_at
      break
  }

  if (result !== 0) return sort.direction === 'desc' ? -result : result
  if (left.created_at !== right.created_at) return left.created_at - right.created_at
  return left.key.localeCompare(right.key, 'en', { numeric: true })
}

function sortRows(tasks: Task[], sort: BacklogSort): Task[] {
  return [...tasks].sort((left, right) => compareTasks(left, right, sort))
}

export type BacklogProps = {
  rows: Task[]
  loadedCount: number
  flashKey: string | null
  onOpen: (key: string) => void
  onClearFilters: () => void
  onReload: () => Promise<void>
  onNoticeChange: (notice: string) => void
  onSessionExpired: () => void
}

export function Backlog({
  rows,
  loadedCount,
  flashKey,
  onOpen,
  onClearFilters,
  onReload,
  onNoticeChange,
  onSessionExpired,
}: BacklogProps) {
  const [sort, setSort] = useState<BacklogSort>(DEFAULT_SORT)
  const [movingKey, setMovingKey] = useState<string | null>(null)
  const [actionError, setActionError] = useState('')
  const focusDone = useRef<string | null>(null)

  useEffect(() => {
    const media = window.matchMedia('(max-width: 767px)')
    const onChange = () => {
      if (!media.matches) return
      setSort(current =>
        current.column === 'key' || current.column === 'project' || current.column === 'size'
          ? DEFAULT_SORT
          : current,
      )
    }
    media.addEventListener('change', onChange)
    return () => media.removeEventListener('change', onChange)
  }, [])

  const sorted = useMemo(() => sortRows(rows, sort), [rows, sort])

  useEffect(() => {
    if (!flashKey) {
      focusDone.current = null
      return
    }
    setActionError('')
    if (focusDone.current === flashKey) return
    const row = document.getElementById(`row-${flashKey}`)
    if (!row) return
    row.focus()
    focusDone.current = flashKey
  }, [flashKey, sorted])

  const orderValue =
    sort.column === 'priority'
      ? 'priority'
      : sort.column === 'created_at'
        ? sort.direction === 'desc'
          ? 'newest'
          : 'oldest'
        : 'priority'

  function sortBy(column: SortableColumn) {
    setSort(current => {
      if (current.column === column) {
        return { column, direction: current.direction === 'asc' ? 'desc' : 'asc' }
      }
      return { column, direction: 'asc' }
    })
  }

  function chooseOrder(value: string) {
    if (value === 'newest') setSort({ column: 'created_at', direction: 'desc' })
    else if (value === 'oldest') setSort({ column: 'created_at', direction: 'asc' })
    else setSort(DEFAULT_SORT)
  }

  function sortProps(column: SortableColumn): { 'aria-sort'?: 'ascending' | 'descending' } {
    if (sort.column !== column) return {}
    return { 'aria-sort': sort.direction === 'asc' ? 'ascending' : 'descending' }
  }

  async function moveToReady(task: Task) {
    setMovingKey(task.key)
    setActionError('')
    onNoticeChange('')
    let patched = false
    let stale = false

    try {
      await api.patchTask(task.key, { state: 'ready' }, task.version)
      patched = true
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 401) {
        setMovingKey(null)
        onSessionExpired()
        return
      }
      if (caught instanceof ApiError) {
        setActionError(caught.message)
        stale = caught.status === 412
      } else {
        setActionError('The card could not be moved.')
      }
    }

    try {
      if (patched) {
        await onReload()
        onNoticeChange(`${task.key} moved to ${STATE_LABELS.ready}.`)
      } else if (stale) {
        await onReload()
      }
    } finally {
      setMovingKey(null)
    }
  }

  return (
    <section className="cuerpo-vista" aria-label="Backlog">
      {actionError && (
        <p className="aviso error" role="alert">
          {actionError}
        </p>
      )}

      <div className="controles-vista">
        <div className="orden-movil">
          <label htmlFor="backlog-order">Order</label>
          <select
            id="backlog-order"
            name="order"
            value={orderValue}
            onChange={event => chooseOrder(event.target.value)}
          >
            <option value="priority">Priority</option>
            <option value="newest">Newest</option>
            <option value="oldest">Oldest</option>
          </select>
        </div>
      </div>

      <div className="abertura">
        <div className="lista-backlog">
          {loadedCount === 0 ? (
            <div className="vacio">
              <h3>Nothing queued. New tasks land here.</h3>
            </div>
          ) : sorted.length === 0 ? (
            <div className="vacio">
              <h3>No matches.</h3>
              <p>Try another search or clear the filters.</p>
              <button type="button" className="secundario" onClick={onClearFilters}>
                Clear filters
              </button>
            </div>
          ) : (
            <div className="tabla-marco">
              <table className="tabla-backlog">
                <colgroup>
                  <col className="col-clave" />
                  <col className="col-proyecto" />
                  <col />
                  <col className="col-prioridad" />
                  <col className="col-tamano" />
                  <col className="col-fecha" />
                  <col className="col-accion" />
                </colgroup>
                <thead>
                  <tr>
                    <th scope="col" {...sortProps('key')}>
                      <button type="button" onClick={() => sortBy('key')}>
                        Key
                        <span aria-hidden="true">{sort.column === 'key' ? (sort.direction === 'asc' ? '↑' : '↓') : '↕'}</span>
                      </button>
                    </th>
                    <th scope="col" {...sortProps('project')}>
                      <button type="button" onClick={() => sortBy('project')}>
                        Project
                        <span aria-hidden="true">{sort.column === 'project' ? (sort.direction === 'asc' ? '↑' : '↓') : '↕'}</span>
                      </button>
                    </th>
                    <th scope="col">
                      <span className="etiqueta-columna">Title</span>
                    </th>
                    <th scope="col" aria-sort={sort.column === 'priority' ? 'descending' : undefined}>
                      <span className="etiqueta-columna">Priority</span>
                    </th>
                    <th scope="col" {...sortProps('size')}>
                      <button type="button" onClick={() => sortBy('size')}>
                        Size
                        <span aria-hidden="true">{sort.column === 'size' ? (sort.direction === 'asc' ? '↑' : '↓') : '↕'}</span>
                      </button>
                    </th>
                    <th scope="col" {...sortProps('created_at')}>
                      <button type="button" onClick={() => sortBy('created_at')}>
                        Created
                        <span aria-hidden="true">{sort.column === 'created_at' ? (sort.direction === 'asc' ? '↑' : '↓') : '↕'}</span>
                      </button>
                    </th>
                    <th scope="col">
                      <span className="anuncio">Action</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {sorted.map(task => (
                    <tr
                      key={task.key}
                      id={`row-${task.key}`}
                      tabIndex={-1}
                      className={flashKey === task.key ? 'fila-resaltada' : undefined}
                    >
                      <td className="celda-clave dato">{task.key}</td>
                      <td className="celda-proyecto">{task.project}</td>
                      <td className="celda-titulo">
                        <button
                          type="button"
                          id={`open-${task.key}`}
                          className="abrir-fila"
                          onClick={() => onOpen(task.key)}
                        >
                          {task.title}
                        </button>
                      </td>
                      <td className="celda-prioridad">
                        <span
                          className={`etiqueta-prioridad${
                            task.priority === 'high'
                              ? ' alta'
                              : task.priority === 'low'
                                ? ' baja'
                                : ''
                          }`}
                        >
                          {PRIORITY_LABELS[task.priority]}
                        </span>
                      </td>
                      <td className="celda-tamano">{task.size ?? '—'}</td>
                      <td className="celda-fecha dato">
                        <time dateTime={isoDate(task.created_at)}>{formatShortDate(task.created_at)}</time>
                      </td>
                      <td className="celda-accion">
                        <button
                          type="button"
                          className="accion-fila"
                          disabled={movingKey === task.key}
                          onClick={() => void moveToReady(task)}
                        >
                          {movingKey === task.key ? 'Moving…' : 'Move to ready'}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>

      <div className="pie-tablero">
        <p>Ready to develop is where an agent can take it.</p>
        <p>Promotion is the only way out of the backlog.</p>
      </div>
    </section>
  )
}
