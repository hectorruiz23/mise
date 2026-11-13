import { formatDateTime, formatRelative, isoDate } from '../dates'
import type { Delivery, Task } from '../types'
import { actorLabel } from '../types'

function pullRequestNumber(url: string): string | null {
  const match = url.match(/\/pull\/(\d+)/)
  return match ? match[1] : null
}

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
          {ordered.map(delivery => {
            const prNumber = delivery.pr_url ? pullRequestNumber(delivery.pr_url) : null
            return (
            <li
              key={delivery.id}
              className={`delivery${delivery.id === approvingId ? ' delivery-approving' : ''}`}
            >
              <div className="delivery-head">
                {delivery.id === approvingId && (
                  <p className="approving">Newest delivery — this is what you are approving.</p>
                )}
                {delivery.pr_url && (
                  <a
                    className="delivery-pr"
                    href={delivery.pr_url}
                    target="_blank"
                    rel="noreferrer"
                    aria-label={prNumber ? `Pull request ${prNumber}` : 'Pull request'}
                  >
                    <svg
                      viewBox="0 0 16 16"
                      width="16"
                      height="16"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth="1.6"
                      strokeLinecap="round"
                      aria-hidden="true"
                    >
                      <circle cx="4" cy="3.5" r="1.6" />
                      <circle cx="4" cy="12.5" r="1.6" />
                      <circle cx="12" cy="12.5" r="1.6" />
                      <path d="M4 5.1v5.8" />
                      <path d="M12 10.9V6.6c0-1.6-1.3-2.9-2.9-2.9H8" />
                      <path d="M9.2 2.5 7.9 3.7l1.3 1.3" />
                    </svg>
                    <span className="num">{prNumber ? `#${prNumber}` : 'PR'}</span>
                  </a>
                )}
              </div>
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
            )
          })}
        </ol>
      )}
    </section>
  )
}
