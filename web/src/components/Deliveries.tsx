import { formatDateTime, formatRelative, isoDate } from '../dates'
import type { Delivery, Task } from '../types'
import { actorLabel } from '../types'

export function Deliveries({
  deliveries,
  task,
  now,
}: {
  deliveries: Delivery[]
  task: Task
  now: number
}) {
  const ordered = [...deliveries].sort((a, b) => b.created_at - a.created_at || b.id - a.id)
  const approvingId = task.state === 'review' ? ordered[0]?.id : undefined

  return (
    <section className="sheet-section" aria-labelledby="deliveries-heading">
      <div className="sheet-section-head">
        <h3 id="deliveries-heading">Deliveries</h3>
        <span className="dato">{ordered.length}</span>
      </div>
      {task.state === 'review' && ordered.length > 0 && (
        <p className="sheet-note">
          You are reviewing the newest delivery below. Approving closes the card in your name.
        </p>
      )}
      {ordered.length === 0 ? (
        <p className="sheet-empty">
          No deliveries yet. An agent must hand the card back with evidence before it can be reviewed.
        </p>
      ) : (
        <ol className="delivery-list">
          {ordered.map(delivery => (
            <li
              key={delivery.id}
              className={`delivery${delivery.id === approvingId ? ' delivery-approving' : ''}`}
            >
              {delivery.id === approvingId && (
                <p className="approving">Newest delivery — this is what you are approving.</p>
              )}
              <p className="delivery-summary">{delivery.summary}</p>
              <div className="delivery-meta">
                <span>Delivered by {actorLabel(delivery.actor, delivery.actor_id)}</span>
                <span className="dato">
                  <time dateTime={isoDate(delivery.created_at)}>
                    {formatDateTime(delivery.created_at)}
                  </time>{' '}
                  <span className="relative">({formatRelative(delivery.created_at, now)})</span>
                </span>
                {delivery.lease_id !== null && (
                  <span className="dato">Lease #{delivery.lease_id}</span>
                )}
                {delivery.pr_url && (
                  <a className="dato" href={delivery.pr_url} target="_blank" rel="noreferrer">
                    Pull request
                  </a>
                )}
              </div>
              {delivery.evidence.length > 0 && (
                <>
                  <h4>Evidence</h4>
                  <ul className="evidence-list">
                    {delivery.evidence.map((item, index) => (
                      <li key={index}>{item}</li>
                    ))}
                  </ul>
                </>
              )}
            </li>
          ))}
        </ol>
      )}
    </section>
  )
}
