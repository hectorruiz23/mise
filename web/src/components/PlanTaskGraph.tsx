import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import { STATE_LABELS, type PlanEdge, type PlanNode } from '../types'

type Props = {
  nodes: PlanNode[]
  edges: PlanEdge[]
}

type Placement = {
  column: number
  row: number
}

type Box = {
  left: number
  top: number
  right: number
  bottom: number
}

const EDGE_LABELS: Record<PlanEdge['Kind'], string> = {
  integration: 'Merged and verified',
  deployment: 'Deployed',
}

const EDGE_CONDITIONS: Record<PlanEdge['Kind'], string> = {
  integration: 'merge and verification',
  deployment: 'deployment',
}

const INSET = 10
const LANE_GAP = 8

/**
 * A task sits at the longest path from a task with no prerequisite, so a chain
 * reads left to right and a fan-out shares a column. Isolated tasks land in the
 * first column. The API rejects cycles; the guard only keeps a malformed graph
 * from hanging the page.
 */
function layoutOf(nodes: PlanNode[], edges: PlanEdge[]): { placements: Map<string, Placement>; columns: number } {
  const known = new Set(nodes.map(node => node.task_key))
  const incoming = new Map<string, PlanEdge[]>()
  for (const edge of edges) {
    if (!known.has(edge.From) || !known.has(edge.To)) continue
    const list = incoming.get(edge.To)
    if (list) list.push(edge)
    else incoming.set(edge.To, [edge])
  }

  const depth = new Map<string, number>()
  const visiting = new Set<string>()
  const resolve = (key: string): number => {
    const cached = depth.get(key)
    if (cached !== undefined) return cached
    if (visiting.has(key)) return 0
    visiting.add(key)
    let value = 0
    for (const edge of incoming.get(key) ?? []) {
      value = Math.max(value, resolve(edge.From) + 1)
    }
    visiting.delete(key)
    depth.set(key, value)
    return value
  }

  const used = new Map<number, number>()
  const placements = new Map<string, Placement>()
  let columns = 0
  for (const node of nodes) {
    const column = resolve(node.task_key)
    columns = Math.max(columns, column + 1)
    const row = used.get(column) ?? 0
    used.set(column, row + 1)
    placements.set(node.task_key, { column, row })
  }
  return { placements, columns: Math.max(columns, 1) }
}

function prerequisites(node: PlanNode, edges: PlanEdge[]): string {
  const origins = edges.filter(edge => edge.To === node.task_key && edge.From !== node.task_key)
  if (origins.length === 0) return 'No prerequisites'
  const described = origins.map(edge => `${edge.From} (${EDGE_CONDITIONS[edge.Kind] ?? EDGE_CONDITIONS.integration})`)
  const list =
    described.length === 1
      ? described[0]
      : `${described.slice(0, -1).join(', ')} and ${described[described.length - 1]}`
  return `Waits for ${list}`
}

export function PlanTaskGraph({ nodes, edges }: Props) {
  const canvasRef = useRef<HTMLDivElement | null>(null)
  const wiresRef = useRef<SVGSVGElement | null>(null)
  const boxesRef = useRef<HTMLDivElement | null>(null)
  const [wide, setWide] = useState(() => window.matchMedia('(min-width: 768px)').matches)

  const layout = useMemo(() => layoutOf(nodes, edges), [nodes, edges])
  const groups = useMemo(() => {
    const byColumn = new Map<number, PlanNode[]>()
    for (const node of nodes) {
      const column = layout.placements.get(node.task_key)?.column ?? 0
      const list = byColumn.get(column)
      if (list) list.push(node)
      else byColumn.set(column, [node])
    }
    return [...byColumn.entries()].sort(([a], [b]) => a - b).map(([, list]) => list)
  }, [layout, nodes])
  const kinds = useMemo(
    () => [...new Set(edges.map(edge => edge.Kind))].filter(kind => kind in EDGE_LABELS),
    [edges],
  )

  useEffect(() => {
    const query = window.matchMedia('(min-width: 768px)')
    const sync = () => setWide(query.matches)
    sync()
    query.addEventListener('change', sync)
    return () => query.removeEventListener('change', sync)
  }, [])

  const draw = useCallback(() => {
    const canvas = canvasRef.current
    const wires = wiresRef.current
    const boxes = boxesRef.current
    if (!canvas || !wires || !boxes) return
    if (!wide) {
      wires.replaceChildren()
      boxes.replaceChildren()
      canvas.style.paddingBottom = ''
      return
    }

    const origin = canvas.getBoundingClientRect()
    const rows = new Map<string, DOMRect>()
    for (const row of canvas.querySelectorAll<HTMLElement>('.plan-row')) {
      const key = row.dataset.key
      if (key) rows.set(key, row.getBoundingClientRect())
    }

    // One container per dependency depth, drawn around the rows it holds.
    const grouped = new Map<number, DOMRect[]>()
    for (const [key, rect] of rows) {
      const column = layout.placements.get(key)?.column ?? 0
      const list = grouped.get(column)
      if (list) list.push(rect)
      else grouped.set(column, [rect])
    }
    const columnBoxes = new Map<number, Box>()
    const drawn: HTMLElement[] = []
    for (const [column, rects] of grouped) {
      const left = Math.min(...rects.map(rect => rect.left)) - INSET
      const top = Math.min(...rects.map(rect => rect.top)) - INSET
      const right = Math.max(...rects.map(rect => rect.right)) + INSET
      const bottom = Math.max(...rects.map(rect => rect.bottom)) + INSET
      const box = document.createElement('div')
      box.className = 'plan-column'
      box.style.left = `${left - origin.left}px`
      box.style.top = `${top - origin.top}px`
      box.style.width = `${right - left}px`
      box.style.height = `${bottom - top}px`
      drawn.push(box)
      columnBoxes.set(column, { left, top, right, bottom })
    }
    boxes.replaceChildren(...drawn)

    // Edges leave the container of the source and enter the one of the target,
    // so both ends hide under the box border instead of stopping mid-air.
    const paths: SVGPathElement[] = []
    let lanes = 0
    for (const edge of edges) {
      const fromColumn = layout.placements.get(edge.From)?.column
      const toColumn = layout.placements.get(edge.To)?.column
      if (fromColumn === undefined || toColumn === undefined) continue
      const from = columnBoxes.get(fromColumn)
      const to = columnBoxes.get(toColumn)
      const source = rows.get(edge.From)
      const target = rows.get(edge.To)
      if (!from || !to || !source || !target) continue

      const x1 = from.right - origin.left
      const x2 = to.left - origin.left
      const y1 = source.top - origin.top + source.height / 2
      const y2 = target.top - origin.top + target.height / 2
      let d: string
      if (toColumn - fromColumn <= 1) {
        const middle = x1 + (x2 - x1) / 2
        d = `M ${x1} ${y1} C ${middle} ${y1}, ${middle} ${y2}, ${x2} ${y2}`
      } else {
        // A long edge goes round the containers it skips, then comes back up.
        let floor = Math.max(y1, y2)
        for (let column = fromColumn + 1; column < toColumn; column += 1) {
          const skipped = columnBoxes.get(column)
          if (skipped) floor = Math.max(floor, skipped.bottom - origin.top)
        }
        const lane = floor + 12 + lanes * LANE_GAP
        lanes += 1
        const out = x1 + 14
        const back = x2 - 14
        d =
          `M ${x1} ${y1} C ${out} ${y1}, ${out} ${lane}, ${out + LANE_GAP} ${lane}` +
          ` L ${back - LANE_GAP} ${lane} C ${back} ${lane}, ${back} ${y2}, ${x2} ${y2}`
      }
      const path = document.createElementNS('http://www.w3.org/2000/svg', 'path')
      path.setAttribute('class', `plan-edge ${edge.Kind}`)
      path.setAttribute('d', d)
      paths.push(path)
    }
    wires.replaceChildren(...paths)
    wires.setAttribute('width', String(Math.ceil(canvas.scrollWidth)))
    wires.setAttribute('height', String(Math.ceil(canvas.scrollHeight)))
    canvas.style.paddingBottom = lanes > 0 ? `${lanes * LANE_GAP + 16}px` : ''
  }, [edges, layout, wide])

  useLayoutEffect(() => {
    draw()
    const canvas = canvasRef.current
    if (!canvas) return
    const observer = new ResizeObserver(() => draw())
    observer.observe(canvas)
    void document.fonts.ready.then(() => draw())
    return () => observer.disconnect()
  }, [draw])

  return (
    <div className="plan-graph">
      {kinds.length > 0 && (
        <p className="plan-legend">
          {kinds.map(kind => (
            <span key={kind} className={`plan-legend-item ${kind}`}>
              {EDGE_LABELS[kind]}
            </span>
          ))}
        </p>
      )}
      <div className="plan-scroll" role="region" aria-label="Task dependencies" tabIndex={wide ? 0 : -1}>
        <div className="plan-canvas" ref={canvasRef}>
          <svg className="plan-wires" aria-hidden="true" focusable="false" ref={wiresRef} />
          <div className="plan-boxes" aria-hidden="true" ref={boxesRef} />
          <ol className="plan-columns" aria-label="Tasks grouped by dependency depth">
            {groups.map((group, index) => (
              <li className="plan-column-group" key={index}>
                <ol className="plan-tasks">
                  {group.map(node => (
                    <li
                      key={node.task_key}
                      data-key={node.task_key}
                      className={`plan-row ${node.state}${node.waiting_on.length > 0 ? ' waiting' : ''}`}
                    >
                      <span className="plan-status" aria-hidden="true" />
                      <p className="plan-copy">
                        <span className="plan-title">{node.title}</span>
                        <span className="plan-label">{STATE_LABELS[node.state]}</span>
                        {node.reveals && <span className="plan-mark">Reveals the change</span>}
                        {node.proof && (
                          <a className="plan-pr" href={node.proof.pr_url} target="_blank" rel="noreferrer">
                            Pull request
                          </a>
                        )}
                        <span className="plan-deps">{prerequisites(node, edges)}</span>
                      </p>
                      <code className="plan-key">{node.task_key}</code>
                    </li>
                  ))}
                </ol>
              </li>
            ))}
          </ol>
        </div>
      </div>
    </div>
  )
}
