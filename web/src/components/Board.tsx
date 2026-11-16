import { useMemo, useState } from 'react'
import type { Capabilities } from '../permissions'
import type { Me, State, Task, TaskDetail } from '../types'
import { STATE_LABELS, STATES } from '../types'
import { Column } from './Column'

const BOARD_STATES = STATES.filter(state => state !== 'backlog' && state !== 'done')

export type BoardProps = {
  me: Me
  caps: Capabilities
  tasks: Task[]
  details: Record<string, TaskDetail>
  now: number
  openKey: string | null
  filtered: boolean
  onOpen: (key: string) => void
  onClearFilters: () => void
  onStartReview: () => void
}

export function Board({
  me,
  caps,
  tasks,
  details,
  now,
  openKey,
  filtered,
  onOpen,
  onClearFilters,
  onStartReview,
}: BoardProps) {
  const [activeState, setActiveState] = useState<State>('ready')

  const byState = useMemo(() => {
    const grouped: Record<State, Task[]> = {
      backlog: [],
      ready: [],
      in_progress: [],
      blocked: [],
      review: [],
      done: [],
    }
    for (const task of tasks) grouped[task.state].push(task)
    return grouped
  }, [tasks])

  function chooseState(state: State) {
    setActiveState(state)
    requestAnimationFrame(() => document.getElementById(`column-${state}`)?.focus())
  }

  return (
    <section className="cuerpo-vista" aria-label="Board">
      <div className="controles-vista">
        <div className="atencion" aria-label="Work that needs attention">
          <button type="button" onClick={() => chooseState('blocked')}>
            <span className="dato">{byState.blocked.length}</span> blocked
          </button>
          <button
            type="button"
            id="board-review"
            onClick={() =>
              byState.review.length && caps.canWrite ? onStartReview() : chooseState('review')
            }
          >
            <span className="dato">{byState.review.length}</span> to review
          </button>
        </div>
      </div>

      <nav className="selector-estados" aria-label="State to show">
        {BOARD_STATES.map(state => (
          <button
            key={state}
            type="button"
            aria-current={activeState === state ? 'true' : undefined}
            onClick={() => chooseState(state)}
          >
            <span>{STATE_LABELS[state]}</span>
            <span className="dato">{byState[state].length}</span>
          </button>
        ))}
      </nav>

      <div className="abertura">
        <div className="tablero">
          {BOARD_STATES.map(state => (
            <Column
              key={state}
              state={state}
              tasks={byState[state]}
              active={activeState === state}
              filtered={filtered}
              details={details}
              now={now}
              openKey={openKey}
              onOpen={onOpen}
              onClearFilters={onClearFilters}
            />
          ))}
        </div>
      </div>

      <div className="pie-tablero">
        <p>
          {caps.canWrite
            ? 'The agent works. You review and close.'
            : 'The agent works. The owner reviews and closes.'}
        </p>
        <p>
          {caps.canWrite
            ? 'No drag and drop. Open a task to move it.'
            : 'No drag and drop. Open a task to see its detail.'}
        </p>
      </div>
    </section>
  )
}
