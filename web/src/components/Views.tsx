import type { View } from '../types'

export type ViewTabsProps = {
  activeView: View
  backlogCount: number | null
  hasMore: boolean
}

export function ViewTabs({ activeView, backlogCount, hasMore }: ViewTabsProps) {
  const count = backlogCount === null ? '—' : hasMore ? `${backlogCount}+` : String(backlogCount)

  return (
    <nav className="pestanas" aria-label="Views">
      <a href="#board" aria-current={activeView === 'board' ? 'page' : undefined}>
        Board
      </a>
      <a href="#backlog" aria-current={activeView === 'backlog' ? 'page' : undefined}>
        Backlog <span className="dato">{count}</span>
      </a>
    </nav>
  )
}
