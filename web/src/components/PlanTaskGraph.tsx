import { useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import { STATE_LABELS, type PlanEdge, type PlanGraph, type PlanNode } from '../types'

type Props = Pick<PlanGraph, 'nodes' | 'edges'>
type Point = { x: number; y: number }
type Wire = { edge: PlanEdge; path: string; head?: Point }
type Junction = Point & { task: string; side: 'input' | 'output'; cardX: number; kind: PlanEdge['Kind']; head?: Point }
type Drawing = { wires: Wire[]; junctions: Junction[]; bottom: number }
type MeasuredTask = {
  left: number
  right: number
  y: number
  stage: number
  inputs: PlanEdge[]
  outputs: PlanEdge[]
}

// The stroke stops short of the card and a filled triangle covers the last
// stretch, so the tip is a point and no line shows under it.
const HEAD = 11
const HEAD_HALF = 6

function arrowPoints(tip: Point) {
  return `${tip.x},${tip.y} ${tip.x - HEAD},${tip.y - HEAD_HALF} ${tip.x - HEAD},${tip.y + HEAD_HALF}`
}

const conditions: Record<PlanEdge['Kind'], string> = {
  integration: 'Merged and verified',
  deployment: 'Deployed',
}

const symbols: Record<PlanNode['state'], string> = {
  backlog: '○',
  ready: '●',
  in_progress: '◐',
  blocked: '−',
  review: '◇',
  done: '✓',
}

function groupTasks({ nodes, edges }: Props) {
  const incoming = new Map(nodes.map(node => [node.task_key, new Array<PlanEdge>()]))
  for (const edge of edges) {
    if (incoming.has(edge.From)) incoming.get(edge.To)?.push(edge)
  }

  const depths = new Map<string, number>()
  function depth(key: string): number {
    const known = depths.get(key)
    if (known !== undefined) return known
    depths.set(key, 0)
    const parents = incoming.get(key) ?? []
    const level = Math.max(0, ...parents.map(edge => depth(edge.From) + 1))
    depths.set(key, level)
    return level
  }

  const stages: PlanNode[][] = []
  for (const node of nodes) {
    const index = depth(node.task_key)
    stages[index] ??= []
    stages[index].push(node)
  }
  return { stages, incoming }
}

function roundedPath(start: Point, bends: Point[], end: Point) {
  const points = [start]
  for (const point of [...bends, end]) {
    const last = points[points.length - 1]
    if (point.x === last.x && point.y === last.y) continue
    const previous = points[points.length - 2]
    if (previous && ((previous.x === last.x && last.x === point.x) || (previous.y === last.y && last.y === point.y))) {
      points.pop()
    }
    points.push(point)
  }

  let path = `M ${start.x} ${start.y}`
  for (let i = 1; i < points.length; i++) {
    const previous = points[i - 1]
    const corner = points[i]
    const dx = corner.x - previous.x
    const dy = corner.y - previous.y
    const next = points[i + 1]
    if (!next) return path + (dx ? ` H ${corner.x}` : ` V ${corner.y}`)
    const nx = next.x - corner.x
    const ny = next.y - corner.y
    const r = Math.min(8, (Math.abs(dx) + Math.abs(dy)) / 2, (Math.abs(nx) + Math.abs(ny)) / 2)
    path += dx ? ` H ${corner.x - Math.sign(dx) * r}` : ` V ${corner.y - Math.sign(dy) * r}`
    const sweep = dx * ny - dy * nx > 0 ? 1 : 0
    path += ` A ${r} ${r} 0 0 ${sweep} ${corner.x + Math.sign(nx) * r} ${corner.y + Math.sign(ny) * r}`
  }
  return path
}

function routeWires(canvas: HTMLElement, edges: PlanEdge[], compact: boolean): Drawing {
  if (compact) return { wires: [], junctions: [], bottom: 0 }
  const origin = canvas.getBoundingClientRect()
  const tasks = new Map<string, MeasuredTask>()
  const columns = Array.from(canvas.querySelectorAll('.plan-stage'), (element, stage) => {
    for (const row of element.querySelectorAll<HTMLElement>('[data-task]')) {
      const icon = row.querySelector('.plan-symbol')?.getBoundingClientRect()
      if (row.dataset.task && icon) {
        const card = row.getBoundingClientRect()
        tasks.set(row.dataset.task, {
          left: card.left - origin.left,
          right: card.right - origin.left,
          y: icon.top + icon.height / 2 - origin.top,
          stage,
          inputs: [],
          outputs: [],
        })
      }
    }
    return element.getBoundingClientRect()
  })
  const routes = edges.flatMap(edge => {
    const from = tasks.get(edge.From)
    const to = tasks.get(edge.To)
    if (!from || !to || to.stage <= from.stage) return []
    from.outputs.push(edge)
    to.inputs.push(edge)
    return [{ edge, from, to }]
  }).sort((a, b) => a.from.stage - b.from.stage || a.to.stage - b.to.stage)

  const gap = columns.length > 1 ? columns[1].left - columns[0].right : 0
  // Halfway between two cards: the junction dot sits at the centre of the run,
  // so the branch leaves from the middle of the shorter line, not from its end.
  const offset = gap / 2
  const junctions: Junction[] = []
  for (const [task, { left, right, y, inputs, outputs }] of tasks) {
    for (const side of ['input', 'output'] as const) {
      const group = side === 'input' ? inputs : outputs
      if (group.length < 2) continue
      const kind = group.every(edge => edge.Kind === 'deployment') ? 'deployment' : 'integration'
      const cardX = side === 'input' ? left : right
      const x = side === 'input' ? left - offset : right + offset
      junctions.push({ task, side, cardX, x, y, kind, head: side === 'input' ? { x: cardX, y } : undefined })
    }
  }

  const laneEnds: number[] = []
  let bottom = 0
  const wires = routes.map(({ edge, from, to }) => {
    const split = from.outputs.length > 1
    const join = to.inputs.length > 1
    const out = from.right + offset
    const back = to.left - offset
    const start = { x: split ? out : from.right, y: from.y }
    const end = { x: join ? back : to.left - HEAD, y: to.y }
    let bends: Point[]
    if (to.stage === from.stage + 1 && !(split && join)) {
      const turn = split ? out : join ? back : (from.right + to.left) / 2
      bends = [{ x: turn, y: from.y }, { x: turn, y: to.y }]
    } else {
      let y = (from.y + to.y) / 2
      if (to.stage > from.stage + 1) {
        const free = laneEnds.findIndex(last => last < from.stage)
        const lane = free < 0 ? laneEnds.length : free
        laneEnds[lane] = to.stage
        const obstacles = columns.slice(from.stage + 1, to.stage)
        y = Math.max(from.y, to.y, ...obstacles.map(rect => rect.bottom - origin.top + 16)) + lane * 12
        bottom = Math.max(bottom, y + 4 - origin.height)
      }
      bends = [{ x: out, y: from.y }, { x: out, y }, { x: back, y }, { x: back, y: to.y }]
    }
    return { edge, path: roundedPath(start, bends, end), head: join ? undefined : { x: to.left, y: to.y } }
  })
  return { wires, junctions, bottom }
}

function TaskRow({ node, parents }: { node: PlanNode; parents: PlanEdge[] }) {
  const waiting = node.waiting_on.length > 0
  return (
    <li data-task={node.task_key} className="plan-task">
      <div className="plan-task-ref">
        <span className="plan-symbol" data-state={node.state} aria-hidden="true">{symbols[node.state]}</span>
        <code>{node.task_key}</code>
      </div>
      <p className="plan-task-title">{node.title}</p>
      <p className="plan-task-state">
        {STATE_LABELS[node.state]}{waiting && ' · Waiting'}
      </p>
      {node.reveals && <p className="plan-reveal">Reveals the change</p>}
      {node.proof && <a className="plan-proof" href={node.proof.pr_url} target="_blank" rel="noreferrer">Merged PR ↗</a>}
      <div className="plan-requirements">
        {parents.length === 0 && <p>No prerequisites</p>}
        {parents.map(edge => (
          <p key={edge.From + edge.Kind}>
            {node.waiting_on.includes(edge.From) ? 'Waiting for' : 'Requires'} {edge.From} · {conditions[edge.Kind]}
          </p>
        ))}
      </div>
    </li>
  )
}

export function PlanTaskGraph({ nodes, edges }: Props) {
  const { stages, incoming } = useMemo(() => groupTasks({ nodes, edges }), [nodes, edges])
  const kinds = [...new Set(edges.map(edge => edge.Kind))]
  const canvasRef = useRef<HTMLOListElement>(null)
  const [drawing, setDrawing] = useState<Drawing>({ wires: [], junctions: [], bottom: 0 })
  const [scrollable, setScrollable] = useState(false)

  useLayoutEffect(() => {
    const canvas = canvasRef.current
    const viewport = canvas?.closest<HTMLElement>('.plan-viewport')
    if (!canvas || !viewport) return
    const measure = () => {
      const compact = getComputedStyle(canvas).gridAutoFlow === 'row'
      setDrawing(routeWires(canvas, edges, compact))
      setScrollable(canvas.offsetWidth > viewport.clientWidth)
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(viewport)
    observer.observe(canvas)
    for (const row of canvas.querySelectorAll('[data-task]')) observer.observe(row)
    return () => observer.disconnect()
  }, [stages, edges])

  const done = nodes.filter(node => node.state === 'done').length
  const sizing: CSSProperties & { '--stage-count': number } = { '--stage-count': stages.length }
  return (
    <section className="plan-graph" aria-labelledby="plan-graph-title">
      <header className="plan-graph-head">
        <h2 id="plan-graph-title">Task dependencies</h2>
        <div className="plan-progress">
          <span>{done} of {nodes.length} done</span>
          <progress value={done} max={nodes.length || 1} aria-label="Completed tasks" />
        </div>
      </header>
      {kinds.length > 0 && (
        <ul className="plan-legend" aria-label="Dependency types">
          {kinds.map(kind => <li key={kind} data-kind={kind}>{conditions[kind]}</li>)}
        </ul>
      )}
      <div className="plan-viewport" role="region" aria-label="Dependency graph" tabIndex={scrollable ? 0 : undefined}>
        <div className="plan-canvas" style={{ ...sizing, paddingBottom: drawing.bottom }}>
          <ol className="plan-stages" ref={canvasRef} aria-label="Stages in dependency order">
            {stages.map((stage, index) => (
              <li className={stage.length > 1 ? 'plan-stage plan-stage-parallel' : 'plan-stage'} key={index}>
                <h3>Stage {String(index + 1).padStart(2, '0')}{stage.length > 1 && <span>{stage.length} parallel</span>}</h3>
                <ol className="plan-stage-tasks" aria-label={`Stage ${index + 1} tasks`}>
                  {stage.map(node => <TaskRow key={node.task_key} node={node} parents={incoming.get(node.task_key) ?? []} />)}
                </ol>
              </li>
            ))}
          </ol>
          <svg className="plan-wires" aria-hidden="true" focusable="false">
            {drawing.wires.map(({ edge, path }) => (
              <path key={edge.From + '/' + edge.To + '/' + edge.Kind} className="plan-wire" data-kind={edge.Kind} d={path} />
            ))}
            {drawing.wires.map(({ edge, head }) =>
              head ? <polygon key={`head/${edge.From}/${edge.To}`} className="plan-arrow" points={arrowPoints(head)} /> : null,
            )}
            {drawing.junctions.map(({ task, side, x, y, cardX, kind, head }) => (
              <g key={task + '/' + side}>
                <line className="plan-wire" data-kind={kind} x1={side === 'input' ? x : cardX} x2={side === 'input' ? cardX - HEAD : x}
                  y1={y} y2={y} />
                <circle className="plan-junction" cx={x} cy={y} r="4.5" />
                {head && <polygon className="plan-arrow" points={arrowPoints(head)} />}
              </g>
            ))}
          </svg>
        </div>
      </div>
    </section>
  )
}
