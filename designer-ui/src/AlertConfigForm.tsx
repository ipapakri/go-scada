import { useEffect, useMemo, useState } from 'react'
import { ApiError, errorMessage } from './api'
import {
  alertInputSubject,
  alertOutputSubject,
  emptyAlertConfig,
  type AlertConfig,
  type AlertConfigType,
  type AlertInterval,
  type AlertMapping,
  type AlertProperties,
} from './models'

interface AlertConfigFormProps {
  initial: AlertConfig
  mode: 'create' | 'edit'
  properties: AlertProperties[]
  knownMembers: string[]
  inputSubjects: string[]
  error: unknown
  busy: boolean
  onCancel: () => void
  onSave: (item: AlertConfig) => Promise<void>
}

function PropertySelect({
  value,
  properties,
  onChange,
}: {
  value?: string
  properties: AlertProperties[]
  onChange: (value: string) => void
}) {
  return (
    <select value={value ?? ''} onChange={(event) => onChange(event.target.value)}>
      <option value="">Choose alarm properties</option>
      {properties.map((item) => (
        <option key={item.subject} value={item.subject}>
          {item.abbreviation} · {item.subject}
        </option>
      ))}
    </select>
  )
}

export function AlertConfigForm({
  initial,
  mode,
  properties,
  knownMembers,
  inputSubjects,
  error,
  busy,
  onCancel,
  onSave,
}: AlertConfigFormProps) {
  const [item, setItem] = useState(initial)
  const [clientError, setClientError] = useState('')
  const [customMember, setCustomMember] = useState('')

  useEffect(() => {
    setItem(initial)
    setClientError('')
    setCustomMember('')
  }, [initial])

  const outputSubject = alertOutputSubject(item.subject)
  const availableMembers = useMemo(
    () => knownMembers.filter((subject) => subject !== outputSubject),
    [knownMembers, outputSubject],
  )

  const changeType = (type: AlertConfigType) => {
    const replacement = emptyAlertConfig(type, properties[0]?.subject)
    setItem({ ...replacement, subject: item.subject, enabled: item.enabled })
  }

  const updateBinaryMapping = (key: 'true' | 'false', mapping: AlertMapping) => {
    if (!item.binary) return
    setItem({ ...item, binary: { ...item.binary, [key]: mapping } })
  }

  const setBinaryBadValue = (badValue: boolean) => {
    if (!item.binary || item.binary.bad_value === badValue) return
    const oldBad = item.binary.bad_value ? item.binary.true : item.binary.false
    const nextTrue = badValue
      ? { ...item.binary.true, property: oldBad.property || properties[0]?.subject }
      : { ...item.binary.true, property: undefined }
    const nextFalse = badValue
      ? { ...item.binary.false, property: undefined }
      : { ...item.binary.false, property: oldBad.property || properties[0]?.subject }
    setItem({
      ...item,
      binary: { ...item.binary, bad_value: badValue, true: nextTrue, false: nextFalse },
    })
  }

  const updateInterval = (index: number, patch: Partial<AlertInterval>) => {
    if (!item.value) return
    const intervals = item.value.intervals.map((interval, current) =>
      current === index ? { ...interval, ...patch } : interval,
    )
    if ('max' in patch && index < intervals.length - 1) {
      intervals[index + 1] = { ...intervals[index + 1], min: patch.max ?? null }
    }
    setItem({ ...item, value: { ...item.value, intervals } })
  }

  const addInterval = () => {
    if (!item.value) return
    const intervals = [...item.value.intervals]
    const last = intervals[intervals.length - 1]
    const start = typeof last.min === 'number' ? last.min : 0
    const boundary = start + (item.value.value_type === 'int64' ? 1 : 10)
    intervals[intervals.length - 1] = { ...last, max: boundary }
    intervals.push({ ...last, min: boundary, max: null })
    setItem({ ...item, value: { ...item.value, intervals } })
  }

  const removeInterval = (index: number) => {
    if (!item.value || item.value.intervals.length <= 2) return
    const removed = item.value.intervals[index]
    const intervals = item.value.intervals.filter((_, current) => current !== index)
    if (index === 0) intervals[0] = { ...intervals[0], min: null }
    else if (index === item.value.intervals.length - 1) {
      intervals[intervals.length - 1] = { ...intervals[intervals.length - 1], max: null }
    } else {
      intervals[index - 1] = { ...intervals[index - 1], max: removed.max }
      intervals[index] = { ...intervals[index], min: removed.max }
    }
    setItem({ ...item, value: { ...item.value, intervals } })
  }

  const setMembers = (members: string[]) => {
    if (item.summary) setItem({ ...item, summary: { members } })
  }

  const addMember = (subject: string) => {
    const value = subject.trim()
    if (!item.summary || !value || item.summary.members.includes(value)) return
    setMembers([...item.summary.members, value])
    setCustomMember('')
  }

  const moveMember = (index: number, direction: -1 | 1) => {
    if (!item.summary) return
    const target = index + direction
    if (target < 0 || target >= item.summary.members.length) return
    const members = [...item.summary.members]
    ;[members[index], members[target]] = [members[target], members[index]]
    setMembers(members)
  }

  const validate = () => {
    if (!item.subject || !item.subject.endsWith('.alert_config')) {
      return 'Subject must end in .alert_config.'
    }
    if (item.type === 'binary' && item.binary) {
      const bad = item.binary.bad_value ? item.binary.true : item.binary.false
      const good = item.binary.bad_value ? item.binary.false : item.binary.true
      if (!bad.text.trim() || !good.text.trim()) return 'Both binary state texts are required.'
      if (!bad.property) return 'Choose properties for the alarm state.'
    }
    if (item.type === 'value' && item.value) {
      for (let index = 0; index < item.value.intervals.length; index += 1) {
        const interval = item.value.intervals[index]
        if (!interval.text.trim()) return `Interval ${index + 1} requires text.`
        if (interval.active && !interval.property) {
          return `Interval ${index + 1} requires alarm properties.`
        }
        if (
          interval.min !== null &&
          interval.max !== null &&
          interval.min >= interval.max
        ) {
          return `Interval ${index + 1} minimum must be less than its maximum.`
        }
      }
    }
    if (item.type === 'summary' && item.summary) {
      if (!item.summary.members.length) return 'Choose at least one summary member.'
      if (item.summary.members.includes(outputSubject)) return 'A summary cannot include itself.'
      if (item.summary.members.some((member) => !member.endsWith('.alert'))) {
        return 'Summary members must end in .alert.'
      }
    }
    return ''
  }

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    const validationError = validate()
    if (validationError) return setClientError(validationError)
    setClientError('')
    await onSave(item)
  }

  const serverError =
    error instanceof ApiError
      ? error.fields.config || error.fields.subject || error.message
      : errorMessage(error)

  return (
    <form className="editor alert-config-editor" onSubmit={submit}>
      <div className="editor-heading">
        <div>
          <p className="eyebrow">{mode === 'create' ? 'New definition' : 'Edit definition'}</p>
          <h2>{item.type[0].toUpperCase() + item.type.slice(1)} alarm</h2>
        </div>
        <label className="switch-label">
          <input
            type="checkbox"
            checked={item.enabled}
            onChange={(event) => setItem({ ...item, enabled: event.target.checked })}
          />
          Enabled
        </label>
      </div>

      <label>
        Subject
        <input
          autoFocus={mode === 'create'}
          value={item.subject}
          disabled={mode === 'edit'}
          list="alert-input-subjects"
          placeholder="tank.temperature.alert_config"
          onChange={(event) => setItem({ ...item, subject: event.target.value })}
        />
        <small>Definition subject ending in .alert_config</small>
      </label>
      <datalist id="alert-input-subjects">
        {inputSubjects.map((subject) => (
          <option key={subject} value={`${subject}.alert_config`} />
        ))}
      </datalist>

      <label>
        Definition type
        <select
          value={item.type}
          disabled={mode === 'edit'}
          onChange={(event) => changeType(event.target.value as AlertConfigType)}
        >
          <option value="binary">Binary</option>
          <option value="value">Value ranges</option>
          <option value="summary">Summary</option>
        </select>
        {mode === 'edit' && <small>Create a new definition to change its type.</small>}
      </label>

      <div className="derived">
        <div>
          <span>Input subject</span>
          <strong>{alertInputSubject(item.subject) || '—'}</strong>
        </div>
        <div>
          <span>Output alarm</span>
          <strong>{outputSubject || '—'}</strong>
        </div>
      </div>

      {item.type === 'binary' && item.binary && (
        <fieldset className="variant-fields">
          <legend>Binary evaluation</legend>
          <div className="segmented">
            <button
              type="button"
              className={item.binary.bad_value ? 'active' : ''}
              onClick={() => setBinaryBadValue(true)}
            >
              Alarm when true
            </button>
            <button
              type="button"
              className={!item.binary.bad_value ? 'active' : ''}
              onClick={() => setBinaryBadValue(false)}
            >
              Alarm when false
            </button>
          </div>
          {(['true', 'false'] as const).map((key) => {
            const mapping = item.binary![key]
            const isBad = item.binary!.bad_value === (key === 'true')
            return (
              <div className="mapping-row" key={key}>
                <strong>{key === 'true' ? 'True state' : 'False state'} · {isBad ? 'Alarm' : 'Normal'}</strong>
                <label>
                  Operator text
                  <input
                    value={mapping.text}
                    onChange={(event) =>
                      updateBinaryMapping(key, { ...mapping, text: event.target.value })
                    }
                  />
                </label>
                {isBad && (
                  <label>
                    Properties
                    <PropertySelect
                      value={mapping.property}
                      properties={properties}
                      onChange={(property) =>
                        updateBinaryMapping(key, { ...mapping, property })
                      }
                    />
                  </label>
                )}
              </div>
            )
          })}
        </fieldset>
      )}

      {item.type === 'value' && item.value && (
        <fieldset className="variant-fields">
          <legend>Value intervals</legend>
          <label>
            Input value type
            <select
              value={item.value.value_type}
              onChange={(event) =>
                setItem({
                  ...item,
                  value: { ...item.value!, value_type: event.target.value as 'int64' | 'float64' },
                })
              }
            >
              <option value="int64">Integer (int64)</option>
              <option value="float64">Decimal (float64)</option>
            </select>
          </label>
          <div className="interval-list">
            {item.value.intervals.map((interval, index) => (
              <div className="interval-row" key={index}>
                <div className="interval-bounds">
                  <span>{interval.min === null ? '−∞' : interval.min}</span>
                  <span>≤ value &lt;</span>
                  {index === item.value!.intervals.length - 1 ? (
                    <span>+∞</span>
                  ) : (
                    <input
                      aria-label={`Interval ${index + 1} maximum`}
                      type="number"
                      step={item.value!.value_type === 'int64' ? '1' : 'any'}
                      value={interval.max ?? ''}
                      onChange={(event) =>
                        updateInterval(index, { max: Number(event.target.value) })
                      }
                    />
                  )}
                </div>
                <input
                  aria-label={`Interval ${index + 1} text`}
                  value={interval.text}
                  placeholder="Operator text"
                  onChange={(event) => updateInterval(index, { text: event.target.value })}
                />
                <label className="check-field compact">
                  <input
                    type="checkbox"
                    checked={interval.active}
                    onChange={(event) =>
                      updateInterval(index, {
                        active: event.target.checked,
                        property: event.target.checked
                          ? interval.property || properties[0]?.subject
                          : undefined,
                      })
                    }
                  />
                  Alarm
                </label>
                {interval.active && (
                  <PropertySelect
                    value={interval.property}
                    properties={properties}
                    onChange={(property) => updateInterval(index, { property })}
                  />
                )}
                <button
                  type="button"
                  className="icon-button"
                  disabled={item.value!.intervals.length <= 2}
                  onClick={() => removeInterval(index)}
                  aria-label={`Remove interval ${index + 1}`}
                >
                  ×
                </button>
              </div>
            ))}
          </div>
          <button type="button" className="button ghost add-range" onClick={addInterval}>
            + Split last range
          </button>
          <small>Ranges are contiguous and use lower-inclusive, upper-exclusive bounds.</small>
        </fieldset>
      )}

      {item.type === 'summary' && item.summary && (
        <fieldset className="variant-fields">
          <legend>Summary members</legend>
          <div className="member-suggestions">
            {availableMembers
              .filter((subject) => !item.summary!.members.includes(subject))
              .map((subject) => (
                <button type="button" key={subject} onClick={() => addMember(subject)}>
                  + {subject}
                </button>
              ))}
          </div>
          <div className="custom-member">
            <input
              value={customMember}
              placeholder="external.source.alert"
              onChange={(event) => setCustomMember(event.target.value)}
            />
            <button type="button" className="button ghost" onClick={() => addMember(customMember)}>
              Add member
            </button>
          </div>
          <div className="member-list">
            {item.summary.members.map((member, index) => (
              <div key={member}>
                <span>{index + 1}</span>
                <strong>{member}</strong>
                <button
                  type="button"
                  disabled={index === 0}
                  onClick={() => moveMember(index, -1)}
                  aria-label={`Move ${member} up`}
                >↑</button>
                <button
                  type="button"
                  disabled={index === item.summary!.members.length - 1}
                  onClick={() => moveMember(index, 1)}
                  aria-label={`Move ${member} down`}
                >↓</button>
                <button
                  type="button"
                  onClick={() => setMembers(item.summary!.members.filter((value) => value !== member))}
                  aria-label={`Remove ${member}`}
                >×</button>
              </div>
            ))}
          </div>
          <small>Order breaks ties when members have the same priority.</small>
        </fieldset>
      )}

      {(clientError || Boolean(error)) && (
        <div className="inline-error" role="alert">{clientError || serverError}</div>
      )}
      <div className="form-actions">
        <button type="button" className="button ghost" onClick={onCancel}>Cancel</button>
        <button type="submit" className="button primary" disabled={busy}>
          {busy ? 'Saving…' : mode === 'create' ? 'Create definition' : 'Save changes'}
        </button>
      </div>
    </form>
  )
}
