import type { State, Task, TaskDetail } from '../types'
import { STATE_LABELS } from '../types'
import { TaskCard } from './TaskCard'

export function Column({
  state,
  tasks,
  active,
  filtered,
  details,
  now,
  openKey,
  onOpen,
  onClearFilters,
}: {
  state: State
  tasks: Task[]
  active: boolean
  filtered: boolean
  details: Record<string, TaskDetail>
  now: number
  openKey: string | null
  onOpen: (key: string) => void
  onClearFilters: () => void
}) {
  return (
    <section className={`calle calle-${state}${active ? ' calle-activa' : ''}`} aria-labelledby={`column-${state}`}>
      <div className="cabecera-calle">
        <div>
          <h2 id={`column-${state}`} tabIndex={-1}>
            {STATE_LABELS[state]}
          </h2>
          <span className="contador dato">{tasks.length}</span>
        </div>
      </div>
      <div className="lista-tareas">
        {tasks.map(task => (
          <TaskCard
            key={task.key}
            task={task}
            detail={details[task.key]}
            now={now}
            open={openKey === task.key}
            onOpen={() => onOpen(task.key)}
          />
        ))}
        {tasks.length === 0 && (
          <div className="vacio">
            <h3>{filtered ? 'No matches' : 'No tasks here'}</h3>
            <p>
              {filtered
                ? 'Try another search or clear the filters.'
                : 'Tasks appear when they move into this state.'}
            </p>
            {filtered && (
              <button type="button" className="secundario" onClick={onClearFilters}>
                Clear filters
              </button>
            )}
          </div>
        )}
      </div>
    </section>
  )
}
