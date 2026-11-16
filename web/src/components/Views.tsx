import type { Capabilities } from '../permissions'
import type { Me, View } from '../types'

export type ViewTabsProps = {
  me: Me
  caps: Capabilities
  activeView: View
  backlogCount: number | null
  hasMore: boolean
}

export function ViewTabs({ me, caps, activeView, backlogCount, hasMore }: ViewTabsProps) {
  const count = backlogCount === null ? '—' : hasMore ? `${backlogCount}+` : String(backlogCount)

  return (
    <nav className="pestanas" aria-label="Views">
      <a href="#board" aria-current={activeView === 'board' ? 'page' : undefined}>
        Board
      </a>
      {caps.canWrite && (
        <a href="#backlog" aria-current={activeView === 'backlog' ? 'page' : undefined}>
          Backlog <span className="dato">{count}</span>
        </a>
      )}
    </nav>
  )
}
