import { formatDateTime, formatRelative, isoDate } from '../dates'
import type { Capabilities } from '../permissions'
import type { Delivery, EvidenceItem } from '../types'
import { actorLabel } from '../types'

type EvidenceKind = 'link' | 'command' | 'artifact' | 'measured' | 'unclassified'

const KIND_LABELS: Record<EvidenceKind, string> = {
  link: 'LINK',
  command: 'COMMAND',
  artifact: 'ARTIFACT',
  measured: 'MEASURED',
  unclassified: 'UNCLASSIFIED',
}

const LINK_PATTERN = /https?:\/\/\S+/i
const COMMAND_PATTERN =
  /^\s*(?:\$ |\.\/|(?:[^\s:]{1,40}[:/]\s+)?(?:sudo|make|npm|npx|pnpm|yarn|go|git|gh|curl|docker|ssh|scp|systemctl|journalctl|grep|rg|ls|cat|python3?|node|sqlite3|psql|jq|sed|awk|tsc|vite)\s)/
const ARTIFACT_PATTERN =
  /(?:~\/|\/home\/|\/Users\/|\/etc\/|\/var\/|\/opt\/|\bdist\/)|\b[\w-]+\.(?:go|ts|tsx|js|jsx|css|html|sql|json|ya?ml|sh|py|md|png|jpe?g|svg|db|toml)\b|(?:^|[\s(])[0-9a-f]{7,40}(?:[\s).,]|$)/i
const MEASURED_PATTERN =
  /(?:^|[^\d])\d[\d.,]*\s*(?:%|ms\b|s\b|min\b|h\b|px\b|KB\b|MB\b|GB\b|checks?\b|tests?\b|runes?\b|palabras\b|requests?\b|peticiones\b)|(?:^|\s)\d+\s*(?:of|\/)\s*\d+(?:\s|$)|[<>≥≤]\s?\d/

function classify(raw: string): EvidenceKind {
  if (LINK_PATTERN.test(raw)) return 'link'
  if (COMMAND_PATTERN.test(raw)) return 'command'
  if (ARTIFACT_PATTERN.test(raw)) return 'artifact'
  if (MEASURED_PATTERN.test(raw)) return 'measured'
  return 'unclassified'
}

function itemsOf(delivery: Delivery): EvidenceItem[] {
  if (delivery.evidence_items && delivery.evidence_items.length > 0) return delivery.evidence_items
  return delivery.evidence.map(raw => ({ label: null, raw }))
}

function pullRequestNumber(url: string | null): string | null {
  if (!url) return null
  const match = url.match(/\/pull\/(\d+)/)
  return match ? match[1] : null
}

function verdictOf(summary: string): { head: string; rest: string } {
  const trimmed = summary.trim()
  const boundary = trimmed.search(/[.!?](\s|$)/)
  if (boundary < 0 || boundary > 280) return { head: trimmed, rest: '' }
  const head = trimmed.slice(0, boundary + 1)
  const rest = trimmed.slice(boundary + 1).trim()
  return { head, rest }
}

export function splitDeliveries(deliveries: Delivery[]): { newest: Delivery | null; earlier: Delivery[] } {
  const ordered = [...deliveries].sort((a, b) => b.created_at - a.created_at || b.id - a.id)
  return { newest: ordered[0] ?? null, earlier: ordered.slice(1) }
}

function EvidenceList({ delivery }: { delivery: Delivery }) {
  const items = itemsOf(delivery)
  return (
    <div className="ev-lista">
      {items.map((item, index) => {
        const kind = classify(item.raw)
        return (
          <div className="ev-fila" key={index}>
            <div className="ev-cab">
              <span className={`ev-tipo ${kind}`}>{KIND_LABELS[kind]}</span>
              {item.label ? <span className="ev-label">{item.label}</span> : null}
            </div>
            <p className="ev-raw">{item.raw}</p>
          </div>
        )
      })}
    </div>
  )
}

function DeliveryMeta({ delivery, now }: { delivery: Delivery; now: number }) {
  return (
    <p className="delivery-meta">
      <span>Delivered by {actorLabel(delivery.actor, delivery.actor_id)}</span>
      <span className="dato">
        <time dateTime={isoDate(delivery.created_at)}>{formatDateTime(delivery.created_at)}</time>{' '}
        <span className="relative">({formatRelative(delivery.created_at, now)})</span>
      </span>
      {delivery.lease_id !== null ? <span className="dato">Lease #{delivery.lease_id}</span> : null}
    </p>
  )
}

export function NewestDelivery({
  delivery,
  caps,
  now,
}: {
  delivery: Delivery | null
  caps: Capabilities
  now: number
}) {
  if (delivery === null) {
    return <p className="sheet-empty">No deliveries yet.</p>
  }

  const { head, rest } = verdictOf(delivery.summary)
  const count = itemsOf(delivery).length

  return (
    <section className="entrega-nueva" aria-labelledby="entrega-nueva-titulo">
      <p className="entrega-kicker" id="entrega-nueva-titulo">
        {caps.canWrite ? 'Newest delivery — this is what you are approving.' : 'Newest delivery.'}
      </p>
      <p className="entrega-veredicto">
        <span className="entrega-que">What was done — the index</span>
        {head}
      </p>
      {rest ? <p className="entrega-resto">{rest}</p> : null}
      {count > 0 ? (
        <details className="ev-grupo">
          <summary>
            <span>Evidence — {count}</span>
            <span className="ev-nota">one click shows every line</span>
          </summary>
          <EvidenceList delivery={delivery} />
        </details>
      ) : null}
      <DeliveryMeta delivery={delivery} now={now} />
    </section>
  )
}

export function EarlierDeliveries({
  deliveries,
  now,
}: {
  deliveries: Delivery[]
  now: number
}) {
  if (deliveries.length === 0) return null

  return (
    <section className="sheet-section" aria-labelledby="entregas-anteriores">
      <div className="sheet-section-head">
        <h3 id="entregas-anteriores">Earlier deliveries</h3>
        <span className="dato">{deliveries.length}</span>
      </div>
      <ul className="entrega-viejas">
        {deliveries.map(delivery => {
          const prNumber = pullRequestNumber(delivery.pr_url)
          return (
            <li key={delivery.id}>
              <details className="entrega-vieja">
                <summary>
                  <span>Delivery #{delivery.id}</span>
                  <span className="dato">
                    {prNumber ? `PR #${prNumber}` : 'no PR'} · {formatRelative(delivery.created_at, now)}
                  </span>
                </summary>
                <div className="entrega-vieja-cuerpo">
                  <p className="entrega-vieja-resumen">{delivery.summary}</p>
                  {itemsOf(delivery).length > 0 ? <EvidenceList delivery={delivery} /> : null}
                  <DeliveryMeta delivery={delivery} now={now} />
                </div>
              </details>
            </li>
          )
        })}
      </ul>
    </section>
  )
}
