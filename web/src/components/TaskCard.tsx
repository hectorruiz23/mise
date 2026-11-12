import { leaseMinutesLeft } from '../dates'
import type { Task, TaskDetail } from '../types'
import { PRIORITY_LABELS, SIZE_LABELS } from '../types'

export function TaskCard({
  task,
  detail,
  now,
  open,
  onOpen,
}: {
  task: Task
  detail: TaskDetail | undefined
  now: number
  open: boolean
  onOpen: () => void
}) {
  const blockedReason = detail?.movements
    .filter(movement => movement.to_state === 'blocked')
    .slice(-1)[0]?.reason
  const lease = detail?.leases.filter(item => item.released_at === null).slice(-1)[0]

  let leaseLine = ''
  if (task.state === 'in_progress' && lease) {
    leaseLine =
      lease.expires_at > now
        ? `Leased by agent #${lease.agent_id}, expires in ${leaseMinutesLeft(lease.expires_at, now)} min`
        : 'Lease expired'
  }

  return (
    <article className={`tarea${open ? ' tarea-abierta' : ''}`} aria-labelledby={`title-${task.key}`}>
      <div className="tarea-referencia">
        <span className="proyecto">{task.project}</span>
        <span className="dato">{task.key}</span>
      </div>
      <h3 id={`title-${task.key}`}>{task.title}</h3>
      {task.priority === 'high' && <p className="prioridad">High priority</p>}
      {leaseLine && (
        <p className={`lease${leaseLine === 'Lease expired' ? ' lease-expired' : ''}`}>{leaseLine}</p>
      )}
      {task.state === 'blocked' && (
        <div className="bloqueo">
          <strong>Needs a decision</strong>
          <p>{blockedReason || task.notes || 'No reason was recorded.'}</p>
        </div>
      )}
      <div className="tarea-pie">
        <span className="responsable">
          {task.size ? SIZE_LABELS[task.size] : PRIORITY_LABELS[task.priority]}
        </span>
        <button
          type="button"
          className="abrir-tarea"
          id={`open-${task.key}`}
          aria-expanded={open}
          aria-haspopup="dialog"
          aria-label={`${task.state === 'review' ? 'Review' : 'Open'} ${task.key}: ${task.title}`}
          onClick={onOpen}
        >
          {task.state === 'review' ? 'Review' : 'Open'}
          <span aria-hidden="true">+</span>
        </button>
      </div>
    </article>
  )
}
