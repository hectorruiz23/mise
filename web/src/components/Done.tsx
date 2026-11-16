import { useMemo } from 'react'
import { formatDate, isoDate, isoWeekLabel } from '../dates'
import type { Task } from '../types'
import { STATE_LABELS } from '../types'

type DoneGroup = { label: string; rows: Task[] }

function compareClosed(left: Task, right: Task): number {
  if (left.closed_at === null && right.closed_at === null) {
    return left.key.localeCompare(right.key, 'en', { numeric: true })
  }
  if (left.closed_at === null) return 1
  if (right.closed_at === null) return -1
  if (left.closed_at !== right.closed_at) return right.closed_at - left.closed_at
  return left.key.localeCompare(right.key, 'en', { numeric: true })
}

function groupByWeek(rows: Task[]): DoneGroup[] {
  const groups: DoneGroup[] = []
  for (const task of [...rows].sort(compareClosed)) {
    const label = task.closed_at === null ? 'No date' : isoWeekLabel(task.closed_at)
    const current = groups[groups.length - 1]
    if (current && current.label === label) current.rows.push(task)
    else groups.push({ label, rows: [task] })
  }
  return groups
}

export type DoneProps = {
  rows: Task[]
  loadedCount: number
  hasMore: boolean
  onOpen: (key: string) => void
  onClearFilters: () => void
}

export function Done({ rows, loadedCount, hasMore, onOpen, onClearFilters }: DoneProps) {
  const groups = useMemo(() => groupByWeek(rows), [rows])
  const weeks = useMemo(() => groups.filter(group => group.label !== 'No date').length, [groups])

  return (
    <section className="cuerpo-vista" aria-label={STATE_LABELS.done}>
      <div className="controles-vista">
        <p className="chip-cierre">
          <span className="dato">{rows.length}</span> closed
          {weeks > 0 && (
            <>
              {' '}in <span className="dato">{weeks}</span> {weeks === 1 ? 'week' : 'weeks'}
            </>
          )}
        </p>
      </div>
      <div className="abertura">
        <div className="lista-backlog">
          {loadedCount === 0 ? (
            <div className="vacio">
              <h3>Nothing closed yet.</h3>
            </div>
          ) : groups.length === 0 ? (
            <div className="vacio">
              <h3>No matches.</h3>
              <p>Try another search or clear the filters.</p>
              <button type="button" className="secundario" onClick={onClearFilters}>
                Clear filters
              </button>
            </div>
          ) : (
            <div className="tabla-marco">
              <table className="tabla-backlog tabla-done">
                <caption className="anuncio">
                  Closed tasks, grouped by ISO week, newest first.
                </caption>
                <colgroup>
                  <col className="col-clave" />
                  <col className="col-proyecto" />
                  <col />
                  <col className="col-tamano" />
                  <col className="col-fecha" />
                </colgroup>
                <thead>
                  <tr>
                    <th scope="col">Key</th>
                    <th scope="col">Project</th>
                    <th scope="col">
                      <span className="etiqueta-columna">Title</span>
                    </th>
                    <th scope="col">Size</th>
                    <th scope="col">Closed</th>
                  </tr>
                </thead>
                {groups.map(group => (
                  <tbody key={group.label}>
                    <tr className="fila-grupo">
                      <th scope="colgroup" colSpan={5}>
                        {group.label} · {group.rows.length} closed
                      </th>
                    </tr>
                    {group.rows.map(task => (
                      <tr key={task.key} id={`row-${task.key}`} tabIndex={-1}>
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
                        <td className="celda-tamano">{task.size ?? '—'}</td>
                        <td className="celda-fecha dato">
                          {task.closed_at === null ? (
                            '—'
                          ) : (
                            <time dateTime={isoDate(task.closed_at)}>{formatDate(task.closed_at)}</time>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                ))}
              </table>
            </div>
          )}
        </div>
      </div>

      <div className="pie-tablero">
        <p>Done is the record of what shipped, not a column to work in.</p>
        <p>{hasMore ? 'Showing the 200 most recent closed tasks.' : 'Every closed task keeps its history.'}</p>
      </div>
    </section>
  )
}
