import { useMemo, useState } from 'react'
import { errorMessage } from './api'
import type { AlertRecord, AlertState } from './models'

interface AlarmPanelProps {
  alerts: AlertRecord[]
  liveStates: Record<string, AlertState>
  onAcknowledge: (subject: string) => Promise<void>
}

function statusOf(state: AlertState) {
  if (state.active) return 'Active'
  if (state.pending) return 'Cleared · unacknowledged'
  return 'Normal'
}

function formatTime(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

export function AlarmPanel({ alerts, liveStates, onAcknowledge }: AlarmPanelProps) {
  const [showAll, setShowAll] = useState(false)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState<unknown>(null)

  const rows = useMemo(
    () =>
      alerts
        .map((item) => ({ ...item, state: liveStates[item.subject] ?? item.state }))
        .filter((item) => showAll || item.state.active || item.state.pending)
        .sort((left, right) => {
          const leftOpen = left.state.active || left.state.pending ? 1 : 0
          const rightOpen = right.state.active || right.state.pending ? 1 : 0
          return (
            rightOpen - leftOpen ||
            right.state.priority - left.state.priority ||
            left.subject.localeCompare(right.subject)
          )
        }),
    [alerts, liveStates, showAll],
  )

  const openCount = alerts.filter((item) => {
    const state = liveStates[item.subject] ?? item.state
    return state.active || state.pending
  }).length

  const acknowledge = async (subject: string) => {
    setBusy(subject)
    setError(null)
    try {
      await onAcknowledge(subject)
    } catch (ackError) {
      setError(ackError)
    } finally {
      setBusy('')
    }
  }

  return (
    <section className="alarm-panel" aria-label="Alarms">
      <div className="alarm-toolbar">
        <div>
          <strong>{openCount} requiring attention</strong>
          <span>{alerts.length} configured alarm{alerts.length === 1 ? '' : 's'}</span>
        </div>
        <label className="alarm-filter">
          <input
            type="checkbox"
            checked={showAll}
            onChange={(event) => setShowAll(event.target.checked)}
          />
          Show normal
        </label>
      </div>

      {Boolean(error) && (
        <div className="page-error" role="alert">
          <span>{errorMessage(error)}</span>
          <button type="button" onClick={() => setError(null)}>Dismiss</button>
        </div>
      )}

      {rows.length ? (
        <div className="alarm-list">
          {rows.map(({ subject, state }) => (
            <article
              key={subject}
              className={`alarm-row ${state.active ? 'active' : state.pending ? 'pending' : 'normal'}`}
            >
              <span
                className="alarm-color"
                style={{ backgroundColor: state.color || '#94a3b8' }}
                aria-hidden="true"
              />
              <div className="alarm-identity">
                <div>
                  <span className="alarm-sign">{state.short_sign || '·'}</span>
                  <strong>{state.text || subject}</strong>
                </div>
                <small>{subject}</small>
              </div>
              <div className="alarm-state">
                <span>{statusOf(state)}</span>
                <small>
                  {state.active ? 'Since' : state.pending ? 'Cleared' : 'Last change'}{' '}
                  {formatTime(state.active ? state.came_time : state.went_time ?? state.ack_time)}
                </small>
              </div>
              <div className="alarm-priority">
                <span>{state.abbreviation || '—'}</span>
                <small>Priority {state.priority}</small>
              </div>
              <div className="alarm-action">
                {state.pending && !state.acknowledged ? (
                  <button
                    className="button acknowledge"
                    type="button"
                    disabled={busy === subject}
                    onClick={() => void acknowledge(subject)}
                  >
                    {busy === subject ? 'Acknowledging…' : 'Acknowledge'}
                  </button>
                ) : (
                  <span className="acknowledged">
                    {state.acknowledged ? 'Acknowledged' : 'No action'}
                  </span>
                )}
              </div>
              {state.dominant && (
                <small className="alarm-dominant">Dominant: {state.dominant}</small>
              )}
            </article>
          ))}
        </div>
      ) : (
        <div className="empty-state alarm-empty">
          <strong>{alerts.length ? 'No alarms require attention' : 'No alarms available'}</strong>
          <span>
            {alerts.length
              ? 'All current alarm episodes are normal or acknowledged.'
              : 'Alarm states will appear after the alert service publishes them.'}
          </span>
        </div>
      )}
    </section>
  )
}
