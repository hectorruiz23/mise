import { formatRelative } from '../dates'
import type { Anchors as AnchorsData, BuildInfo, Envelope, PullRequestAnchor } from '../types'

function shortSha(sha: string | null | undefined): string {
  if (!sha) return 'unknown'
  return sha.slice(0, 8)
}

function retryLine(envelope: Envelope<unknown>, now: number): string {
  if (!envelope.retry_at) return ''
  const minutes = Math.max(1, Math.ceil((envelope.retry_at - now) / 60))
  return `retry in ${minutes} min`
}

function ageLine(envelope: Envelope<unknown>, now: number): string {
  const reason = envelope.reason ?? ''
  if (envelope.status === 'stale') {
    const checked = envelope.checked_at ? `checked ${formatRelative(envelope.checked_at, now)}` : 'checked earlier'
    return [checked, reason].filter(Boolean).join(' · ')
  }
  if (envelope.status === 'unavailable') {
    const body = [reason || 'no usable data', retryLine(envelope, now)].filter(Boolean).join(' · ')
    return `unavailable — ${body}`
  }
  if (envelope.status === 'unknown') {
    return `unknown — ${reason || 'unresolved'}`
  }
  return ''
}

function pullRequestClass(state: string): string {
  if (state === 'merged') return 'merged'
  if (state === 'open') return 'open'
  if (state === 'closed') return 'closed'
  return 'unknown'
}

function PullRequestChip({ anchor, now }: { anchor: PullRequestAnchor; now: number }) {
  if (anchor.kind === 'link') {
    return (
      <a className="ancla ancla-neutra" href={anchor.url} target="_blank" rel="noreferrer">
        <span className="ancla-icono" aria-hidden="true">
          ↗
        </span>
        <span className="ancla-cuerpo">
          <span className="ancla-titulo">External link</span>
          <span className="ancla-sub">not a GitHub pull request</span>
        </span>
      </a>
    )
  }

  const known = anchor.state.status === 'fresh' || anchor.state.status === 'stale'
  const state = known ? anchor.state.data : 'unknown'
  const stateAge = ageLine(anchor.state, now)
  const checksResult = anchor.checks.data?.result
  const checksAge = ageLine(anchor.checks, now)
  const sub = [checksResult ? `checks ${checksResult}` : '', checksAge].filter(Boolean).join(' · ')

  return (
    <a className={`ancla ancla-pr ${pullRequestClass(state)}`} href={anchor.url} target="_blank" rel="noreferrer">
      <span className="ancla-icono" aria-hidden="true">
        <svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round">
          <circle cx="4" cy="3.5" r="1.6" />
          <circle cx="4" cy="12.5" r="1.6" />
          <circle cx="12" cy="12.5" r="1.6" />
          <path d="M4 5.1v5.8" />
          <path d="M12 10.9V6.6c0-1.6-1.3-2.9-2.9-2.9H8" />
          <path d="M9.2 2.5 7.9 3.7l1.3 1.3" />
        </svg>
      </span>
      <span className="ancla-cuerpo">
        <span className="ancla-titulo">
          {known ? state : 'pull request'}
          {stateAge ? <span className="ancla-sub"> {stateAge}</span> : null}
        </span>
        <span className="ancla-sub">{sub || 'checks unavailable'}</span>
      </span>
    </a>
  )
}

function RevisionChip({ anchors, build, now }: { anchors: AnchorsData | null; build: BuildInfo | null; now: number }) {
  const serving = build?.revision ? shortSha(build.revision) : 'unknown'
  const local = build !== null && build.revision === null
  const mainKnown = anchors !== null && (anchors.main.status === 'fresh' || anchors.main.status === 'stale')
  const mainLine = mainKnown
    ? `main ${shortSha(anchors?.main.data)}`
    : anchors === null
      ? 'main …'
      : `main ${ageLine(anchors.main, now) || 'unknown'}`

  const sub = build?.tree_state === 'dirty' ? 'local build · tree_state dirty' : local ? 'built without git' : mainLine

  return (
    <span className="ancla ancla-rev">
      <span className="ancla-icono" aria-hidden="true">
        <svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round">
          <circle cx="8" cy="8" r="6" />
          <path d="M8 4.6v3.8l2.4 1.4" />
        </svg>
      </span>
      <span className="ancla-cuerpo">
        <span className="ancla-titulo">serving {serving}</span>
        <span className="ancla-sub">{sub}</span>
      </span>
    </span>
  )
}

export function Anchors({
  anchors,
  build,
  failed,
  now,
}: {
  anchors: AnchorsData | null
  build: BuildInfo | null
  failed: boolean
  now: number
}) {
  if (anchors === null && build === null) {
    return (
      <div className="sheet-anclas" aria-live="polite">
        <span className="ancla ancla-cargando">
          {failed ? 'unavailable — the server did not answer' : 'Loading…'}
        </span>
      </div>
    )
  }

  return (
    <div className="sheet-anclas">
      {anchors?.pr ? <PullRequestChip anchor={anchors.pr} now={now} /> : null}
      <RevisionChip anchors={anchors} build={build} now={now} />
    </div>
  )
}
