import { cleanup, render, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { useServerEvents } from './useServerEvents'

class FakeEventSource {
  static instance: FakeEventSource
  readonly listeners = new Map<string, EventListener>()
  readonly url: string
  close = vi.fn()
  constructor(url: string) { this.url = url; FakeEventSource.instance = this }
  addEventListener(type: string, listener: EventListener) { this.listeners.set(type, listener) }
  emit(type: string) { this.listeners.get(type)?.(new MessageEvent(type, { data: '{}' })) }
}

function Probe({ onInvalidate }: { onInvalidate(): void }) {
  useServerEvents(onInvalidate)
  return null
}

afterEach(() => { cleanup(); vi.restoreAllMocks() })

it('invalidates data for operation and server events and closes on unmount', async () => {
  vi.stubGlobal('EventSource', FakeEventSource)
  const onInvalidate = vi.fn()
  const view = render(<Probe onInvalidate={onInvalidate} />)
  expect(FakeEventSource.instance.url).toBe('/api/v1/events')
  FakeEventSource.instance.emit('operation.updated')
  FakeEventSource.instance.emit('server.updated')
  await waitFor(() => expect(onInvalidate).toHaveBeenCalledTimes(2))
  view.unmount()
  expect(FakeEventSource.instance.close).toHaveBeenCalledOnce()
  vi.unstubAllGlobals()
})
