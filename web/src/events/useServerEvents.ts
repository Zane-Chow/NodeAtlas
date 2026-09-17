import { useEffect, useRef } from 'react'

export function useServerEvents(onInvalidate: () => void): void {
  const callback = useRef(onInvalidate)

  useEffect(() => {
    callback.current = onInvalidate
  }, [onInvalidate])

  useEffect(() => {
    if (typeof EventSource === 'undefined') return

    const source = new EventSource('/api/v1/events')
    const invalidate = () => callback.current()
    source.addEventListener('operation.updated', invalidate)
    source.addEventListener('server.updated', invalidate)
    return () => source.close()
  }, [])
}
