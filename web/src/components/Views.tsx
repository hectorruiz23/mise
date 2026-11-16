import type { Capabilities } from '../permissions'
import type { Me, View } from '../types'

export type ViewTabsProps = {
  me: Me
  caps: Capabilities
  activeView: View
  backlogCount: number | null
  hasMore: boolean
  doneCount: number | null
  doneHasMore: boolean
}

export function ViewTabs({
  me,
  caps,
  activeView,
  backlogCount,
  hasMore,
  doneCount,
  doneHasMore,
}: ViewTabsProps) {
  const closed =
    doneCount === null ? null : doneHasMore ? `${doneCount}+` : String(doneCount)

  return (
    <nav className="pestanas" aria-label="Views">
      <a href="#board" aria-current={activeView === 'board' ? 'page' : undefined}>
        Board
      </a>
      {caps.canSeeBacklog && (
        <a href="#backlog" aria-current={activeView === 'backlog' ? 'page' : undefined}>
          Backlog
          {backlogCount !== null && (
            <span className="dato">{hasMore ? `${backlogCount}+` : backlogCount}</span>
          )}
        </a>
      )}
      <a href="#done" aria-current={activeView === 'done' ? 'page' : undefined}>
        Done
        {closed !== null && <span className="dato">{closed}</span>}
      </a>
    </nav>
  )
}
