import { useRef, useState } from 'react'
import {
  DndContext, DragOverlay, KeyboardSensor, PointerSensor, TouchSensor, closestCenter, useSensor, useSensors,
  defaultDropAnimationSideEffects, type DragEndEvent, type DragStartEvent, type DropAnimation,
} from '@dnd-kit/core'
import { SortableContext, arrayMove, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable'
import { restrictToParentElement, restrictToVerticalAxis } from '@dnd-kit/modifiers'
import { CSS } from '@dnd-kit/utilities'
import { GripVertical } from 'lucide-react'
import { useT } from '../i18n'
import { cx } from './ui-utils'

/** A row's drag handle; spread onto the element that starts the drag. */
export type Grip = React.ReactNode

const drop: DropAnimation = {
  duration: 220, easing: 'cubic-bezier(.2,.8,.2,1)',
  sideEffects: defaultDropAnimationSideEffects({ styles: { active: { opacity: '0' } } }),
}

/** Vertical drag-to-reorder list (mouse, touch long-press, keyboard) with animated reflow.
 *  `items` keep their own order; `onReorder` gets the new id order after a drop. The caller
 *  saves it (optimistically) and passes the new order back in. */
export function SortableList<T>({ items, id, enabled, onReorder, render, className }: {
  items: T[]; id: (t: T) => string; enabled: boolean
  onReorder: (ids: string[]) => void
  render: (t: T, grip: Grip, dragging: boolean) => React.ReactNode
  className?: string
}) {
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(TouchSensor, { activationConstraint: { delay: 200, tolerance: 6 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  )
  const [active, setActive] = useState<string | null>(null)
  // the whole row drags; swallow the click a drop would otherwise send to the row's link
  const dropped = useRef(0)
  const ids = items.map(id)
  const byId = new Map(items.map(t => [id(t), t]))
  const start = (e: DragStartEvent) => { setActive(String(e.active.id)); navigator.vibrate?.(8) }
  const end = (e: DragEndEvent) => {
    setActive(null); dropped.current = Date.now()
    if (!e.over || e.active.id === e.over.id) return
    const from = ids.indexOf(String(e.active.id)), to = ids.indexOf(String(e.over.id))
    if (from < 0 || to < 0) return
    onReorder(arrayMove(ids, from, to))
  }
  if (!enabled) return <div className={className}>{items.map(t => <div key={id(t)}>{render(t, null, false)}</div>)}</div>
  const cur = active ? byId.get(active) : undefined
  return (
    <DndContext sensors={sensors} collisionDetection={closestCenter} modifiers={[restrictToVerticalAxis, restrictToParentElement]}
      onDragStart={start} onDragEnd={end} onDragCancel={() => setActive(null)}>
      <SortableContext items={ids} strategy={verticalListSortingStrategy}>
        <div className={className} onClickCapture={e => { if (Date.now() - dropped.current < 300) { e.preventDefault(); e.stopPropagation() } }}>
          {items.map(t => <Row key={id(t)} id={id(t)}>{grip => render(t, grip, false)}</Row>)}
        </div>
      </SortableContext>
      <DragOverlay dropAnimation={drop} modifiers={[restrictToVerticalAxis]}>
        {cur ? <div className="drag-lift">{render(cur, <span className="sort-grip is-on shrink-0 h-7 w-5 -mr-1 grid place-items-center text-fg2"><GripIcon on /></span>, true)}</div> : null}
      </DragOverlay>
    </DndContext>
  )
}

function GripIcon({ on }: { on?: boolean }) {
  return <GripVertical size={14} className={cx('transition-opacity', on ? 'opacity-80' : '')} />
}

function Row({ id, children }: { id: string; children: (grip: Grip) => React.ReactNode }) {
  const { t } = useT()
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id })
  const grip = (
    <span aria-hidden title={t('拖动排序', 'Drag to reorder')} className="sort-grip shrink-0 h-7 w-5 -mr-1 grid place-items-center text-dim">
      <GripIcon />
    </span>
  )
  return (
    <div ref={setNodeRef} {...attributes} {...listeners} aria-roledescription={t('可拖动排序', 'sortable')}
      style={{ transform: CSS.Translate.toString(transform), transition }} className={cx('sort-row relative', isDragging && 'drag-ghost')}>
      {children(grip)}
    </div>
  )
}
