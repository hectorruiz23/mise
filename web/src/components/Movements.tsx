import { formatDateTime, formatRelative, isoDate } from '../dates'
import type { Movement } from '../types'
import { actorLabel, STATE_LABELS } from '../types'

export function Movements({ movements, now }: { movements: Movement[]; now: number }) {
  const ordered = [...movements].sort((a, b) => b.at - a.at || b.task_version - a.task_version)

  return (
    <section className="sheet-section" aria-labelledby="movements-heading">
      <div className="sheet-section-head">
        <h3 id="movements-heading">Movements</h3>
        <span className="dato">{ordered.length}</span>
      </div>
      {ordered.length === 0 ? (
        <p className="sheet-empty">No movements yet. Every state change is recorded here.</p>
      ) : (
        <ol className="movement-list">
          {ordered.map(movement => (
            <li key={`${movement.task_version}-${movement.at}-${movement.to_state}`}>
              <div className="movement-route">
                <span>{movement.from_state ? STATE_LABELS[movement.from_state] : 'Created'}</span>
                <span aria-hidden="true">→</span>
                <strong>{STATE_LABELS[movement.to_state]}</strong>
              </div>
              <p className="movement-meta">
                <span>{actorLabel(movement.actor, movement.actor_id)}</span>
                <span className="dato">v{movement.task_version}</span>
              </p>
              {movement.reason && <p className="movement-reason">{movement.reason}</p>}
              <p className="movement-date dato">
                <time dateTime={isoDate(movement.at)}>{formatDateTime(movement.at)}</time>{' '}
                <span className="relative">({formatRelative(movement.at, now)})</span>
              </p>
            </li>
          ))}
        </ol>
      )}
    </section>
  )
}
