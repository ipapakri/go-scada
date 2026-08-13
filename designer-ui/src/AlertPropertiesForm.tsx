import { useEffect, useState } from 'react'
import { ApiError, errorMessage } from './api'
import type { AlertProperties } from './models'

interface AlertPropertiesFormProps {
  initial: AlertProperties
  mode: 'create' | 'edit'
  error: unknown
  busy: boolean
  onCancel: () => void
  onSave: (item: AlertProperties) => Promise<void>
}

export function AlertPropertiesForm({
  initial,
  mode,
  error,
  busy,
  onCancel,
  onSave,
}: AlertPropertiesFormProps) {
  const [item, setItem] = useState(initial)
  const [clientError, setClientError] = useState('')

  useEffect(() => {
    setItem(initial)
    setClientError('')
  }, [initial])

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    if (!/^AlertProperties\.[^.]+$/.test(item.subject)) {
      return setClientError('Subject must be AlertProperties followed by one name token.')
    }
    if (!item.color.trim() || !item.abbreviation.trim() || !item.short_sign.trim()) {
      return setClientError('Color, abbreviation, and short sign are required.')
    }
    if (item.priority < 0) return setClientError('Priority cannot be negative.')
    setClientError('')
    await onSave(item)
  }

  const serverError =
    error instanceof ApiError
      ? error.fields.properties || error.fields.subject || error.message
      : errorMessage(error)

  return (
    <form className="editor" onSubmit={submit}>
      <div className="editor-heading">
        <div>
          <p className="eyebrow">{mode === 'create' ? 'New resource' : 'Edit resource'}</p>
          <h2>Alarm properties</h2>
        </div>
        {mode === 'edit' && (
          <span className="reference-count">{item.reference_count ?? 0} references</span>
        )}
      </div>

      <label>
        Subject
        <input
          autoFocus={mode === 'create'}
          value={item.subject}
          disabled={mode === 'edit'}
          placeholder="AlertProperties.Alarm"
          onChange={(event) => setItem({ ...item, subject: event.target.value })}
        />
        <small>AlertProperties followed by a single name token</small>
      </label>

      <div className="color-field">
        <label>
          Color
          <input
            value={item.color}
            placeholder="#dc2626"
            onChange={(event) => setItem({ ...item, color: event.target.value })}
          />
        </label>
        <input
          type="color"
          aria-label="Choose alarm color"
          value={/^#[0-9a-f]{6}$/i.test(item.color) ? item.color : '#dc2626'}
          onChange={(event) => setItem({ ...item, color: event.target.value })}
        />
      </div>

      <div className="form-grid three">
        <label>
          Abbreviation
          <input
            value={item.abbreviation}
            placeholder="ALM"
            onChange={(event) => setItem({ ...item, abbreviation: event.target.value })}
          />
        </label>
        <label>
          Short sign
          <input
            value={item.short_sign}
            placeholder="!"
            onChange={(event) => setItem({ ...item, short_sign: event.target.value })}
          />
        </label>
        <label>
          Priority
          <input
            type="number"
            min="0"
            value={item.priority}
            onChange={(event) => setItem({ ...item, priority: Number(event.target.value) })}
          />
        </label>
      </div>

      <label className="check-field">
        <input
          type="checkbox"
          checked={item.requires_acknowledgement}
          onChange={(event) =>
            setItem({ ...item, requires_acknowledgement: event.target.checked })
          }
        />
        Require operator acknowledgement
      </label>

      {(clientError || Boolean(error)) && (
        <div className="inline-error" role="alert">
          {clientError || serverError}
        </div>
      )}
      <div className="form-actions">
        <button type="button" className="button ghost" onClick={onCancel}>Cancel</button>
        <button type="submit" className="button primary" disabled={busy}>
          {busy ? 'Saving…' : mode === 'create' ? 'Create properties' : 'Save changes'}
        </button>
      </div>
    </form>
  )
}
