import { useCallback, useEffect, useMemo, useState } from 'react'
import './App.css'
import { AlertConfigForm } from './AlertConfigForm'
import { AlarmPanel } from './AlarmPanel'
import { AlertPropertiesForm } from './AlertPropertiesForm'
import { PlantView } from './PlantView'
import { ApiError, api, errorMessage } from './api'
import {
  alertInputSubject,
  alertOutputSubject,
  emptyAddress,
  emptyAlertConfig,
  emptyAlertProperties,
  emptyConnection,
  encodingsForRegister,
  registerCount,
  telemetrySubject,
  valueTypeForEncoding,
  type Address,
  type AlertConfig,
  type AlertProperties,
  type AlertRecord,
  type Connection,
  type Encoding,
  type Register,
} from './models'
import { listTankPlants, OPERATOR_PLANT_ID, plantAddresses, plantAlerts } from './plant'
import { useLiveAlerts } from './useLiveAlerts'
import { useLiveValues } from './useLiveValues'

type EditorMode = 'create' | 'edit'

function fieldError(error: unknown, field: string) {
  return error instanceof ApiError ? error.fields[field] : undefined
}

function validateSubject(subject: string, suffix: '.config' | '.address') {
  if (!subject || subject.trim() !== subject) return 'Subject is required without surrounding spaces.'
  if (!subject.endsWith(suffix)) return `Subject must end in ${suffix}.`
  if (subject.startsWith('.') || subject.includes('..') || /[\s*>]/.test(subject)) {
    return 'Use a valid relative NATS subject.'
  }
  return ''
}

function Toggle({ enabled }: { enabled: boolean }) {
  return (
    <span className={`toggle ${enabled ? 'on' : ''}`} aria-hidden="true">
      <span />
    </span>
  )
}

interface ConnectionFormProps {
  initial: Connection
  mode: EditorMode
  error: unknown
  busy: boolean
  onCancel: () => void
  onSave: (item: Connection) => Promise<void>
}

function ConnectionForm({
  initial,
  mode,
  error,
  busy,
  onCancel,
  onSave,
}: ConnectionFormProps) {
  const [item, setItem] = useState(initial)
  const [clientError, setClientError] = useState('')

  useEffect(() => {
    setItem(initial)
    setClientError('')
  }, [initial])

  const setConfig = <K extends keyof Connection['config']>(
    key: K,
    value: Connection['config'][K],
  ) => setItem((current) => ({ ...current, config: { ...current.config, [key]: value } }))

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    const subjectError = validateSubject(item.subject, '.config')
    if (subjectError) return setClientError(subjectError)
    if (!/^tcp:\/\/[^/\s]+/.test(item.config.url)) {
      return setClientError('URL must be a tcp:// URL with a host.')
    }
    setClientError('')
    await onSave(item)
  }

  return (
    <form className="editor" onSubmit={submit}>
      <div className="editor-heading">
        <div>
          <p className="eyebrow">{mode === 'create' ? 'New resource' : 'Edit resource'}</p>
          <h2>{mode === 'create' ? 'Add connection' : 'Connection settings'}</h2>
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
          placeholder="plant.line1.plc.config"
          onChange={(event) => setItem({ ...item, subject: event.target.value })}
          aria-describedby="connection-subject-help"
        />
        <small id="connection-subject-help">Relative NATS subject ending in .config</small>
      </label>

      <label>
        Modbus TCP URL
        <input
          value={item.config.url}
          placeholder="tcp://192.168.1.20:502"
          onChange={(event) => setConfig('url', event.target.value)}
        />
      </label>

      <div className="form-grid three">
        <label>
          Unit ID
          <input
            type="number"
            min="0"
            max="255"
            value={item.config.unit_id}
            onChange={(event) => setConfig('unit_id', Number(event.target.value))}
          />
        </label>
        <label>
          Byte order
          <select
            value={item.config.byte_order}
            onChange={(event) => setConfig('byte_order', event.target.value as 'big' | 'little')}
          >
            <option value="big">Big endian</option>
            <option value="little">Little endian</option>
          </select>
        </label>
        <label>
          Word order
          <select
            value={item.config.word_order}
            onChange={(event) => setConfig('word_order', event.target.value as 'big' | 'little')}
          >
            <option value="big">Big endian</option>
            <option value="little">Little endian</option>
          </select>
        </label>
      </div>

      <div className="form-grid">
        <label>
          Timeout
          <input
            value={item.config.timeout}
            placeholder="2s"
            onChange={(event) => setConfig('timeout', event.target.value)}
          />
        </label>
        <label>
          Poll interval
          <input
            value={item.config.poll_interval}
            placeholder="1s"
            onChange={(event) => setConfig('poll_interval', event.target.value)}
          />
        </label>
      </div>

      {(clientError || Boolean(error)) && (
        <div className="inline-error" role="alert">
          {clientError || fieldError(error, 'connection') || errorMessage(error)}
        </div>
      )}
      <div className="form-actions">
        <button type="button" className="button ghost" onClick={onCancel}>
          Cancel
        </button>
        <button type="submit" className="button primary" disabled={busy}>
          {busy ? 'Saving…' : mode === 'create' ? 'Create connection' : 'Save changes'}
        </button>
      </div>
    </form>
  )
}

interface AddressFormProps {
  initial: Address
  mode: EditorMode
  connections: Connection[]
  error: unknown
  busy: boolean
  onCancel: () => void
  onSave: (item: Address) => Promise<void>
}

function AddressForm({
  initial,
  mode,
  connections,
  error,
  busy,
  onCancel,
  onSave,
}: AddressFormProps) {
  const [item, setItem] = useState(initial)
  const [clientError, setClientError] = useState('')

  useEffect(() => {
    setItem(initial)
    setClientError('')
  }, [initial])

  const eligibleConnections = connections.filter(
    (connection) => connection.enabled || connection.subject === item.connection,
  )

  const setRegister = (register: Register) => {
    const allowed = encodingsForRegister(register)
    const encoding = allowed.includes(item.config.encoding) ? item.config.encoding : allowed[0]
    setItem({
      ...item,
      value_type: valueTypeForEncoding(encoding),
      config: { ...item.config, register, encoding },
    })
  }

  const setEncoding = (encoding: Encoding) =>
    setItem({
      ...item,
      value_type: valueTypeForEncoding(encoding),
      config: { ...item.config, encoding },
    })

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    const subjectError = validateSubject(item.subject, '.address')
    if (subjectError) return setClientError(subjectError)
    if (!item.connection) return setClientError('Choose an enabled connection.')
    if (item.config.address + registerCount(item.config.encoding) > 65536) {
      return setClientError('Address and encoding exceed the Modbus register range.')
    }
    setClientError('')
    await onSave({
      ...item,
      value_type: valueTypeForEncoding(item.config.encoding),
      telemetry_subject: telemetrySubject(item.subject),
    })
  }

  const derivedTelemetry = telemetrySubject(item.subject)
  const width = registerCount(item.config.encoding)

  return (
    <form className="editor" onSubmit={submit}>
      <div className="editor-heading">
        <div>
          <p className="eyebrow">{mode === 'create' ? 'New resource' : 'Edit resource'}</p>
          <h2>{mode === 'create' ? 'Add address' : 'Address settings'}</h2>
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
          placeholder="plant.line1.temperature.address"
          onChange={(event) => setItem({ ...item, subject: event.target.value })}
        />
        <small>Relative NATS subject ending in .address</small>
      </label>

      <label>
        Connection
        <select
          value={item.connection}
          onChange={(event) => setItem({ ...item, connection: event.target.value })}
        >
          <option value="">Select an enabled connection</option>
          {eligibleConnections.map((connection) => (
            <option key={connection.subject} value={connection.subject}>
              {connection.subject}{connection.enabled ? '' : ' (disabled)'}
            </option>
          ))}
        </select>
        {!connections.some((connection) => connection.enabled) && (
          <small className="warning">Create or enable a connection before adding an address.</small>
        )}
      </label>

      <div className="form-grid three">
        <label>
          Register
          <select
            value={item.config.register}
            onChange={(event) => setRegister(event.target.value as Register)}
          >
            <option value="coil">Coil</option>
            <option value="discrete_input">Discrete input</option>
            <option value="input">Input register</option>
            <option value="holding">Holding register</option>
          </select>
        </label>
        <label>
          Address
          <input
            type="number"
            min="0"
            max={65536 - width}
            value={item.config.address}
            onChange={(event) =>
              setItem({
                ...item,
                config: { ...item.config, address: Number(event.target.value) },
              })
            }
          />
        </label>
        <label>
          Encoding
          <select
            value={item.config.encoding}
            onChange={(event) => setEncoding(event.target.value as Encoding)}
          >
            {encodingsForRegister(item.config.register).map((encoding) => (
              <option key={encoding} value={encoding}>
                {encoding}
              </option>
            ))}
          </select>
        </label>
      </div>

      <label className="switch-label">
        <input
          type="checkbox"
          checked={item.publish_on_change}
          onChange={(event) =>
            setItem({ ...item, publish_on_change: event.target.checked })
          }
        />
        Publish only when value changes
      </label>
      <small>
        Unchanged hardware values are not published, so alerts and other
        subscribers are not retriggered.
      </small>

      <div className="derived">
        <div>
          <span>Value type</span>
          <strong>{valueTypeForEncoding(item.config.encoding)}</strong>
        </div>
        <div>
          <span>Register width</span>
          <strong>{width} {width === 1 ? 'register' : 'registers'}</strong>
        </div>
        <div>
          <span>Telemetry subject</span>
          <strong title={derivedTelemetry}>{derivedTelemetry || '—'}</strong>
        </div>
      </div>

      {(clientError || Boolean(error)) && (
        <div className="inline-error" role="alert">
          {clientError ||
            fieldError(error, 'address') ||
            fieldError(error, 'connection') ||
            errorMessage(error)}
        </div>
      )}
      <div className="form-actions">
        <button type="button" className="button ghost" onClick={onCancel}>
          Cancel
        </button>
        <button type="submit" className="button primary" disabled={busy || !item.connection}>
          {busy ? 'Saving…' : mode === 'create' ? 'Create address' : 'Save changes'}
        </button>
      </div>
    </form>
  )
}

function App() {
  const [connections, setConnections] = useState<Connection[]>([])
  const [addresses, setAddresses] = useState<Address[]>([])
  const [alerts, setAlerts] = useState<AlertRecord[]>([])
  const [alertProperties, setAlertProperties] = useState<AlertProperties[]>([])
  const [alertConfigs, setAlertConfigs] = useState<AlertConfig[]>([])
  const [activeView, setActiveView] = useState<
    'plant' | 'connections' | 'addresses' | 'alarms' | 'alert-properties' | 'alert-configs'
  >('plant')
  const [selectedConnection, setSelectedConnection] = useState<Connection | null>(null)
  const [selectedAddress, setSelectedAddress] = useState<Address | null>(null)
  const [selectedAlertProperties, setSelectedAlertProperties] =
    useState<AlertProperties | null>(null)
  const [selectedAlertConfig, setSelectedAlertConfig] = useState<AlertConfig | null>(null)
  const [creating, setCreating] = useState(false)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState<unknown>(null)
  const [selectedPlantId, setSelectedPlantId] = useState(OPERATOR_PLANT_ID)
  const tankPlants = useMemo(() => listTankPlants(addresses), [addresses])
  const scopedAddresses = useMemo(
    () => plantAddresses(addresses, selectedPlantId),
    [addresses, selectedPlantId],
  )
  const allPlantAddresses = useMemo(() => plantAddresses(addresses), [addresses])
  const scopedAlerts = useMemo(
    () => plantAlerts(alerts, selectedPlantId),
    [alerts, selectedPlantId],
  )
  const allPlantAlerts = useMemo(() => plantAlerts(alerts), [alerts])
  const { values, status: liveStatus } = useLiveValues(
    activeView === 'addresses'
      ? addresses
      : activeView === 'plant'
        ? allPlantAddresses
        : scopedAddresses,
  )
  const { states: liveAlertStates, status: alarmLiveStatus } = useLiveAlerts(
    activeView === 'alarms'
      ? alerts
      : activeView === 'plant'
        ? allPlantAlerts
        : scopedAlerts,
  )

  useEffect(() => {
    if (tankPlants.length > 0 && !tankPlants.includes(selectedPlantId)) {
      setSelectedPlantId(tankPlants[0])
    }
  }, [tankPlants, selectedPlantId])

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const [connectionItems, addressItems, alertItems, propertyItems, configItems] =
        await Promise.all([
        api.listConnections(),
        api.listAddresses(),
        api.listAlerts(),
        api.listAlertProperties(),
        api.listAlertConfigs(),
      ])
      setConnections(connectionItems)
      setAddresses(addressItems)
      setAlerts(alertItems)
      setAlertProperties(propertyItems)
      setAlertConfigs(configItems)
    } catch (loadError) {
      setError(loadError)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const addressCounts = useMemo(() => {
    const counts: Record<string, number> = {}
    for (const address of addresses) counts[address.connection] = (counts[address.connection] ?? 0) + 1
    return counts
  }, [addresses])

  const openCreate = () => {
    setCreating(true)
    setError(null)
    setSelectedConnection(null)
    setSelectedAddress(null)
    setSelectedAlertProperties(null)
    setSelectedAlertConfig(null)
  }

  const closeEditor = () => {
    setCreating(false)
    setSelectedConnection(null)
    setSelectedAddress(null)
    setSelectedAlertProperties(null)
    setSelectedAlertConfig(null)
    setError(null)
  }

  const saveConnection = async (item: Connection) => {
    setBusy(item.subject || 'new-connection')
    setError(null)
    try {
      const saved = creating ? await api.createConnection(item) : await api.updateConnection(item)
      setConnections((current) => {
        const exists = current.some((entry) => entry.subject === saved.subject)
        return exists
          ? current.map((entry) => (entry.subject === saved.subject ? saved : entry))
          : [...current, saved].sort((a, b) => a.subject.localeCompare(b.subject))
      })
      closeEditor()
    } catch (saveError) {
      setError(saveError)
    } finally {
      setBusy('')
    }
  }

  const saveAddress = async (item: Address) => {
    setBusy(item.subject || 'new-address')
    setError(null)
    try {
      const saved = creating ? await api.createAddress(item) : await api.updateAddress(item)
      setAddresses((current) => {
        const exists = current.some((entry) => entry.subject === saved.subject)
        return exists
          ? current.map((entry) => (entry.subject === saved.subject ? saved : entry))
          : [...current, saved].sort((a, b) => a.subject.localeCompare(b.subject))
      })
      closeEditor()
    } catch (saveError) {
      setError(saveError)
    } finally {
      setBusy('')
    }
  }

  const saveAlertProperties = async (item: AlertProperties) => {
    setBusy(item.subject || 'new-alert-properties')
    setError(null)
    try {
      const saved = creating
        ? await api.createAlertProperties(item)
        : await api.updateAlertProperties(item)
      setAlertProperties((current) => {
        const exists = current.some((entry) => entry.subject === saved.subject)
        return exists
          ? current.map((entry) => (entry.subject === saved.subject ? saved : entry))
          : [...current, saved].sort((a, b) => a.subject.localeCompare(b.subject))
      })
      closeEditor()
    } catch (saveError) {
      setError(saveError)
    } finally {
      setBusy('')
    }
  }

  const saveAlertConfig = async (item: AlertConfig) => {
    setBusy(item.subject || 'new-alert-config')
    setError(null)
    try {
      const saved = creating
        ? await api.createAlertConfig(item)
        : await api.updateAlertConfig(item)
      setAlertConfigs((current) => {
        const exists = current.some((entry) => entry.subject === saved.subject)
        return exists
          ? current.map((entry) => (entry.subject === saved.subject ? saved : entry))
          : [...current, saved].sort((a, b) => a.subject.localeCompare(b.subject))
      })
      closeEditor()
    } catch (saveError) {
      setError(saveError)
    } finally {
      setBusy('')
    }
  }

  const toggleConnection = async (item: Connection) => {
    setBusy(item.subject)
    setError(null)
    try {
      const saved = item.enabled
        ? await api.disableConnection(item.subject)
        : await api.updateConnection({ ...item, enabled: true })
      setConnections((current) =>
        current.map((entry) => (entry.subject === saved.subject ? saved : entry)),
      )
    } catch (toggleError) {
      setError(toggleError)
    } finally {
      setBusy('')
    }
  }

  const toggleAddress = async (item: Address) => {
    setBusy(item.subject)
    setError(null)
    try {
      const saved = item.enabled
        ? await api.disableAddress(item.subject)
        : await api.updateAddress({ ...item, enabled: true })
      setAddresses((current) =>
        current.map((entry) => (entry.subject === saved.subject ? saved : entry)),
      )
    } catch (toggleError) {
      setError(toggleError)
    } finally {
      setBusy('')
    }
  }

  const toggleAlertConfig = async (item: AlertConfig) => {
    setBusy(item.subject)
    setError(null)
    try {
      const saved = item.enabled
        ? await api.disableAlertConfig(item.subject)
        : await api.updateAlertConfig({ ...item, enabled: true })
      setAlertConfigs((current) =>
        current.map((entry) => (entry.subject === saved.subject ? saved : entry)),
      )
    } catch (toggleError) {
      setError(toggleError)
    } finally {
      setBusy('')
    }
  }

  const acknowledgeAlert = async (subject: string) => {
    const saved = await api.acknowledgeAlert(subject)
    setAlerts((current) =>
      current.map((item) => (item.subject === saved.subject ? saved : item)),
    )
  }

  const showEditor =
    creating ||
    selectedConnection ||
    selectedAddress ||
    selectedAlertProperties ||
    selectedAlertConfig
  const enabledConnections = connections.filter((item) => item.enabled)
  const isAlarmWorkspace = activeView.startsWith('alert-') || activeView === 'alarms'
  const liveIndicator = isAlarmWorkspace ? alarmLiveStatus : liveStatus
  const hero = {
    plant: ['Operator workspace', 'Plant overview', 'Watch live values from all simulated tanks, pumps, valves, and utilities.'],
    alarms: ['Operator workspace', 'Alarm overview', 'Monitor current alarm episodes and acknowledge conditions requiring attention.'],
    'alert-properties': ['Alarm configuration', 'Presentation properties', 'Manage reusable priorities, colors, labels, and acknowledgement policies.'],
    'alert-configs': ['Alarm configuration', 'Alert definitions', 'Build binary, value-range, and summary alarm evaluations.'],
    connections: ['Device workspace', 'Modbus data model', 'Configure TCP connections and map registers to live telemetry subjects.'],
    addresses: ['Device workspace', 'Modbus data model', 'Configure TCP connections and map registers to live telemetry subjects.'],
  }[activeView]
  const addLabel = {
    connections: 'connection',
    addresses: 'address',
    'alert-properties': 'properties',
    'alert-configs': 'definition',
    alarms: '',
    plant: '',
  }[activeView]

  return (
    <div className="app-shell">
      <header className="topbar">
        <div className="brand">
          <span className="brand-mark">S</span>
          <div>
            <strong>SCADA Workspace</strong>
            <small>Operations & configuration</small>
          </div>
        </div>
        <div className={`live-status ${liveIndicator}`}>
          <span />
          Live {liveIndicator}
        </div>
      </header>

      <main>
        <section className="hero-copy">
          <div>
            <p className="eyebrow">{hero[0]}</p>
            <h1>{hero[1]}</h1>
            <p>{hero[2]}</p>
          </div>
          {addLabel && (
            <button className="button primary" type="button" onClick={openCreate}>
              + Add {addLabel}
            </button>
          )}
        </section>

        <nav className="tabs" aria-label="Designer sections">
          <button
            className={activeView === 'plant' ? 'active' : ''}
            onClick={() => {
              setActiveView('plant')
              closeEditor()
            }}
          >
            Plant
          </button>
          <button
            className={activeView === 'alarms' ? 'active' : ''}
            onClick={() => {
              setActiveView('alarms')
              closeEditor()
            }}
          >
            Alarms <span>{alerts.filter((item) => {
              const state = liveAlertStates[item.subject] ?? item.state
              return state.active || state.pending
            }).length}</span>
          </button>
          <button
            className={activeView === 'alert-properties' ? 'active' : ''}
            onClick={() => {
              setActiveView('alert-properties')
              closeEditor()
            }}
          >
            Alert properties <span>{alertProperties.length}</span>
          </button>
          <button
            className={activeView === 'alert-configs' ? 'active' : ''}
            onClick={() => {
              setActiveView('alert-configs')
              closeEditor()
            }}
          >
            Alert definitions <span>{alertConfigs.length}</span>
          </button>
          <button
            className={activeView === 'connections' ? 'active' : ''}
            onClick={() => {
              setActiveView('connections')
              closeEditor()
            }}
          >
            Connections <span>{connections.length}</span>
          </button>
          <button
            className={activeView === 'addresses' ? 'active' : ''}
            onClick={() => {
              setActiveView('addresses')
              closeEditor()
            }}
          >
            Addresses <span>{addresses.length}</span>
          </button>
        </nav>

        {Boolean(error) && !showEditor && (
          <div className="page-error" role="alert">
            <span>{errorMessage(error)}</span>
            <button type="button" onClick={() => void load()}>Retry</button>
          </div>
        )}

        <div className={`workspace ${showEditor ? 'with-editor' : ''}`}>
          <section className="resource-panel">
            {loading ? (
              <div className="empty-state">Loading configuration…</div>
            ) : activeView === 'plant' ? (
              <PlantView
                addresses={addresses}
                values={values}
                alerts={alerts}
                liveStates={liveAlertStates}
                selectedPlantId={selectedPlantId}
                onSelectPlant={setSelectedPlantId}
                onAcknowledge={acknowledgeAlert}
              />
            ) : activeView === 'alarms' ? (
              <AlarmPanel
                alerts={alerts}
                liveStates={liveAlertStates}
                onAcknowledge={acknowledgeAlert}
              />
            ) : activeView === 'alert-properties' ? (
              alertProperties.length ? (
                <div className="resource-list">
                  {alertProperties.map((item) => (
                    <article
                      key={item.subject}
                      className={`resource-card ${
                        selectedAlertProperties?.subject === item.subject ? 'selected' : ''
                      }`}
                    >
                      <button
                        className="card-main properties-card-main"
                        type="button"
                        onClick={() => {
                          setCreating(false)
                          setError(null)
                          setSelectedAlertProperties(item)
                        }}
                      >
                        <div className="card-title">
                          <span
                            className="resource-icon property-color"
                            style={{ backgroundColor: item.color }}
                          >
                            {item.short_sign}
                          </span>
                          <div>
                            <strong>{item.subject}</strong>
                            <small>{item.abbreviation} · priority {item.priority}</small>
                          </div>
                        </div>
                        <div className="card-meta">
                          <span>{item.reference_count ?? 0} references</span>
                          <span>{item.requires_acknowledgement ? 'Acknowledgement required' : 'No acknowledgement'}</span>
                        </div>
                      </button>
                    </article>
                  ))}
                </div>
              ) : (
                <div className="empty-state">
                  <strong>No alert properties yet</strong>
                  <span>Create presentation properties before defining alarms.</span>
                </div>
              )
            ) : activeView === 'alert-configs' ? (
              alertConfigs.length ? (
                <div className="resource-list">
                  {alertConfigs.map((item) => (
                    <article
                      key={item.subject}
                      className={`resource-card ${
                        selectedAlertConfig?.subject === item.subject ? 'selected' : ''
                      }`}
                    >
                      <button
                        className="card-main"
                        type="button"
                        onClick={() => {
                          setCreating(false)
                          setError(null)
                          setSelectedAlertConfig(item)
                        }}
                      >
                        <div className="card-title">
                          <span className="resource-icon">{item.type[0].toUpperCase()}</span>
                          <div>
                            <strong>{item.subject}</strong>
                            <small>{item.input_subject || alertInputSubject(item.subject)}</small>
                          </div>
                        </div>
                        <div className="card-meta">
                          <span>{item.type}</span>
                          <span>{item.output_subject || alertOutputSubject(item.subject)}</span>
                        </div>
                      </button>
                      <button
                        className="enable-button"
                        type="button"
                        disabled={busy === item.subject}
                        onClick={() => void toggleAlertConfig(item)}
                        aria-label={`${item.enabled ? 'Disable' : 'Enable'} ${item.subject}`}
                      >
                        <Toggle enabled={item.enabled} />
                        {item.enabled ? 'Enabled' : 'Disabled'}
                      </button>
                    </article>
                  ))}
                </div>
              ) : (
                <div className="empty-state">
                  <strong>No alert definitions yet</strong>
                  <span>Create a binary, value-range, or summary alarm.</span>
                </div>
              )
            ) : activeView === 'connections' ? (
              connections.length ? (
                <div className="resource-list">
                  {connections.map((item) => (
                    <article
                      key={item.subject}
                      className={`resource-card ${
                        selectedConnection?.subject === item.subject ? 'selected' : ''
                      }`}
                    >
                      <button
                        className="card-main"
                        type="button"
                        onClick={() => {
                          setCreating(false)
                          setError(null)
                          setSelectedConnection(item)
                        }}
                      >
                        <div className="card-title">
                          <span className="resource-icon">C</span>
                          <div>
                            <strong>{item.subject}</strong>
                            <small>{item.config.url}</small>
                          </div>
                        </div>
                        <div className="card-meta">
                          <span>Unit {item.config.unit_id}</span>
                          <span>{addressCounts[item.subject] ?? item.address_count ?? 0} addresses</span>
                        </div>
                      </button>
                      <button
                        className="enable-button"
                        type="button"
                        disabled={busy === item.subject}
                        onClick={() => void toggleConnection(item)}
                        aria-label={`${item.enabled ? 'Disable' : 'Enable'} ${item.subject}`}
                      >
                        <Toggle enabled={item.enabled} />
                        {item.enabled ? 'Enabled' : 'Disabled'}
                      </button>
                    </article>
                  ))}
                </div>
              ) : (
                <div className="empty-state">
                  <strong>No connections yet</strong>
                  <span>Add a Modbus TCP endpoint to get started.</span>
                </div>
              )
            ) : addresses.length ? (
              <div className="resource-list">
                {addresses.map((item) => {
                  const live = values[item.subject]
                  return (
                    <article
                      key={item.subject}
                      className={`resource-card address-card ${
                        selectedAddress?.subject === item.subject ? 'selected' : ''
                      }`}
                    >
                      <button
                        className="card-main"
                        type="button"
                        onClick={() => {
                          setCreating(false)
                          setError(null)
                          setSelectedAddress(item)
                        }}
                      >
                        <div className="card-title">
                          <span className="resource-icon">A</span>
                          <div>
                            <strong>{item.subject}</strong>
                            <small>{item.connection}</small>
                          </div>
                        </div>
                        <div className="card-meta">
                          <span>{item.config.register} · {item.config.address}</span>
                          <span>{item.config.encoding}</span>
                        </div>
                        <div className={`live-value ${live?.type === 'error' ? 'error' : ''}`}>
                          <small>{item.telemetry_subject || telemetrySubject(item.subject)}</small>
                          <strong>
                            {live?.type === 'error'
                              ? live.message
                              : live?.value === undefined
                                ? 'Waiting for value…'
                                : typeof live.value === 'string'
                                  ? live.value
                                  : JSON.stringify(live.value)}
                          </strong>
                          {live?.timestamp && (
                            <time dateTime={live.timestamp}>
                              {new Date(live.timestamp).toLocaleTimeString()}
                            </time>
                          )}
                        </div>
                      </button>
                      <button
                        className="enable-button"
                        type="button"
                        disabled={busy === item.subject}
                        onClick={() => void toggleAddress(item)}
                        aria-label={`${item.enabled ? 'Disable' : 'Enable'} ${item.subject}`}
                      >
                        <Toggle enabled={item.enabled} />
                        {item.enabled ? 'Enabled' : 'Disabled'}
                      </button>
                    </article>
                  )
                })}
              </div>
            ) : (
              <div className="empty-state">
                <strong>No addresses yet</strong>
                <span>
                  {enabledConnections.length
                    ? 'Map a register from an enabled connection.'
                    : 'Enable a connection before mapping a register.'}
                </span>
              </div>
            )}
          </section>

          {showEditor && (
            <aside className="editor-panel">
              {activeView === 'connections' ? (
                <ConnectionForm
                  key={creating ? 'new-connection' : selectedConnection?.subject}
                  initial={creating ? emptyConnection() : selectedConnection!}
                  mode={creating ? 'create' : 'edit'}
                  error={error}
                  busy={Boolean(busy)}
                  onCancel={closeEditor}
                  onSave={saveConnection}
                />
              ) : activeView === 'addresses' ? (
                <AddressForm
                  key={creating ? 'new-address' : selectedAddress?.subject}
                  initial={
                    creating
                      ? emptyAddress(enabledConnections[0]?.subject)
                      : selectedAddress!
                  }
                  mode={creating ? 'create' : 'edit'}
                  connections={connections}
                  error={error}
                  busy={Boolean(busy)}
                  onCancel={closeEditor}
                  onSave={saveAddress}
                />
              ) : activeView === 'alert-properties' ? (
                <AlertPropertiesForm
                  key={creating ? 'new-alert-properties' : selectedAlertProperties?.subject}
                  initial={creating ? emptyAlertProperties() : selectedAlertProperties!}
                  mode={creating ? 'create' : 'edit'}
                  error={error}
                  busy={Boolean(busy)}
                  onCancel={closeEditor}
                  onSave={saveAlertProperties}
                />
              ) : (
                <AlertConfigForm
                  key={creating ? 'new-alert-config' : selectedAlertConfig?.subject}
                  initial={
                    creating
                      ? emptyAlertConfig('binary', alertProperties[0]?.subject)
                      : selectedAlertConfig!
                  }
                  mode={creating ? 'create' : 'edit'}
                  properties={alertProperties}
                  knownMembers={[
                    ...new Set([
                      ...alertConfigs.map((item) => item.output_subject || alertOutputSubject(item.subject)),
                      ...alerts.map((item) => item.subject),
                    ]),
                  ]}
                  inputSubjects={addresses.map((item) => item.telemetry_subject || telemetrySubject(item.subject))}
                  error={error}
                  busy={Boolean(busy)}
                  onCancel={closeEditor}
                  onSave={saveAlertConfig}
                />
              )}
            </aside>
          )}
        </div>
      </main>
    </div>
  )
}

export default App
