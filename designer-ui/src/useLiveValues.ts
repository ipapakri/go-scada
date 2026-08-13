import { useEffect, useMemo, useState } from 'react'
import type { Address, LiveEvent } from './models'

export type LiveStatus = 'connecting' | 'connected' | 'disconnected'

export function useLiveValues(addresses: Address[]) {
  const subjects = useMemo(
    () => addresses.filter((item) => item.enabled).map((item) => item.subject).sort(),
    [addresses],
  )
  const subjectKey = subjects.join('\n')
  const [values, setValues] = useState<Record<string, LiveEvent>>({})
  const [status, setStatus] = useState<LiveStatus>('connecting')

  useEffect(() => {
    if (typeof WebSocket === 'undefined') {
      setStatus('disconnected')
      return
    }

    const subscribedSubjects = subjectKey ? subjectKey.split('\n') : []
    let socket: WebSocket | undefined
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined
    let closed = false

    const connect = () => {
      setStatus('connecting')
      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      socket = new WebSocket(`${protocol}//${window.location.host}/api/live`)

      socket.addEventListener('open', () => {
        setStatus('connected')
        for (const subject of subscribedSubjects) {
          socket?.send(JSON.stringify({ action: 'subscribe', subject }))
        }
      })
      socket.addEventListener('message', (event) => {
        try {
          const message = JSON.parse(String(event.data)) as LiveEvent
          if (message.subject) {
            setValues((current) => ({ ...current, [message.subject]: message }))
          }
        } catch {
          // Ignore malformed frames and keep the stream alive.
        }
      })
      socket.addEventListener('close', () => {
        setStatus('disconnected')
        if (!closed) reconnectTimer = setTimeout(connect, 2000)
      })
      socket.addEventListener('error', () => setStatus('disconnected'))
    }

    connect()
    return () => {
      closed = true
      if (reconnectTimer) clearTimeout(reconnectTimer)
      if (socket?.readyState === WebSocket.OPEN) {
        for (const subject of subscribedSubjects) {
          socket.send(JSON.stringify({ action: 'unsubscribe', subject }))
        }
      }
      socket?.close()
    }
  }, [subjectKey])

  return { values, status }
}
