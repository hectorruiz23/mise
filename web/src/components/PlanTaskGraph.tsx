import { useId, useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import { STATE_LABELS, type PlanEdge, type PlanGraph, type PlanNode } from '../types'

type Props = Pick<PlanGraph, 'nodes' | 'edges'>
type Wire = { edge: PlanEdge; path: string }
type Port = { peerY: number; y: number }
type MeasuredTask = {
  left: number
  right: number
  firstY: number
  lastY: number
  stage: number
  inputs: Port[]
  outputs: Port[]
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

function routeWires(canvas: HTMLElement, edges: PlanEdge[], compact: boolean) {
  if (compact) return { wires: [], bottom: 0 }
  const origin = canvas.getBoundingClientRect()
  const tasks = new Map<string, MeasuredTask>()
  const floors = Array.from(canvas.querySelectorAll('.plan-stage'), (element, stage) => {
    const column = element.getBoundingClientRect()
    for (const row of element.querySelectorAll<HTMLElement>('[data-task]')) {
      const icon = row.querySelector('.plan-symbol')?.getBoundingClientRect()
      if (row.dataset.task && icon) {
        tasks.set(row.dataset.task, {
          left: column.left - origin.left,
          right: column.right - origin.left,
          firstY: icon.top + icon.height / 2 - origin.top,
          lastY: row.getBoundingClientRect().bottom - origin.top - 12,
          stage,
          inputs: [],
          outputs: [],
        })
      }
    }
    return column.bottom - origin.top
  })
  const routes = edges.flatMap(edge => {
    const from = tasks.get(edge.From)
    const to = tasks.get(edge.To)
    if (!from || !to) return []
    const output = { peerY: to.firstY, y: from.firstY }
    const input = { peerY: from.firstY, y: to.firstY }
    from.outputs.push(output)
    to.inputs.push(input)
    return [{ edge, from, to, output, input }]
  }).sort((a, b) => a.from.stage - b.from.stage || a.from.firstY - b.from.firstY || a.to.stage - b.to.stage)

  for (const task of tasks.values()) {
    for (const ports of [task.inputs, task.outputs]) {
      if (ports.length < 2) continue
      ports.sort((a, b) => a.peerY - b.peerY)
      const spacing = Math.min(32, (task.lastY - task.firstY) / (ports.length - 1))
      ports.forEach((port, index) => { port.y = task.firstY + index * spacing })
    }
  }

  const ends: number[] = []
  function lane(start: number, end: number) {
    const free = ends.findIndex(last => last < start)
    const index = free < 0 ? ends.length : free
    ends[index] = end
    return index
  }

  const wires: Wire[] = routes.map(({ edge, from, to, output, input }) => {
    const y1 = output.y
    const y2 = input.y
    const x1 = from.right
    const x2 = to.left
    if (to.stage === from.stage + 1) {
      const middle = (x1 + x2) / 2
      return { edge, path: `M ${x1} ${y1} C ${middle} ${y1} ${middle} ${y2} ${x2} ${y2}` }
    }

    const track = lane(from.stage, to.stage)
    const floor = Math.max(y1, y2, ...floors.slice(from.stage + 1, to.stage).map(bottom => bottom + 16)) + track * 9
    const out = x1 + 12
    const back = x2 - 12
    return {
      edge,
      path: `M ${x1} ${y1} C ${out} ${y1} ${out} ${floor} ${out + 10} ${floor} H ${back - 10} C ${back} ${floor} ${back} ${y2} ${x2} ${y2}`,
    }
  })
  return { wires, bottom: ends.length ? ends.length * 9 + 16 : 0 }
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
  const marker = useId()
  const [drawing, setDrawing] = useState<{ wires: Wire[]; bottom: number }>({ wires: [], bottom: 0 })
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
            <defs>
              <marker id={marker} viewBox="0 0 6 8" refX="6" refY="4" markerUnits="userSpaceOnUse" markerWidth="6" markerHeight="8" orient="auto">
                <path className="plan-arrow" d="M0 0 6 4 0 8Z" />
              </marker>
            </defs>
            {drawing.wires.map(({ edge, path }) => (
              <path key={edge.From + '/' + edge.To + '/' + edge.Kind} className="plan-wire" data-kind={edge.Kind} d={path} markerEnd={`url(#${marker})`} />
            ))}
          </svg>
        </div>
      </div>
    </section>
  )
}
