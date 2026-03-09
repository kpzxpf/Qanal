// Thin shim over Wails runtime events.
// In Wails mode: delegates to window.runtime (injected by Wails).
// In web mode: uses a local in-process event bus.
import { isWails } from './mode'
import { EventsOn as _WailsOn, EventsOff as _WailsOff } from '../../wailsjs/runtime/runtime'

type Cb = (...args: any[]) => void
const bus = new Map<string, Set<Cb>>()

export function EventsOn(event: string, cb: Cb): () => void {
  if (isWails) return _WailsOn(event, cb) as unknown as () => void
  if (!bus.has(event)) bus.set(event, new Set())
  bus.get(event)!.add(cb)
  return () => EventsOff(event)
}

export function EventsOff(event: string): void {
  if (isWails) { _WailsOff(event); return }
  bus.delete(event)
}

/** Emit an event on the local bus (web mode only). */
export function _webEmit(event: string, data: unknown): void {
  bus.get(event)?.forEach(cb => cb(data))
}
