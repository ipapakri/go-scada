export type ByteOrder = 'big' | 'little'
export type Register = 'coil' | 'discrete_input' | 'input' | 'holding'
export type Encoding =
  | 'bool'
  | 'int16'
  | 'uint16'
  | 'int32'
  | 'uint32'
  | 'float32'
  | 'float64'
export type ValueType = 'bool' | 'int64' | 'float64'

export interface ConnectionConfig {
  url: string
  unit_id: number
  byte_order: ByteOrder
  word_order: ByteOrder
  timeout: string
  poll_interval: string
}

export interface Connection {
  subject: string
  version: number
  driver: 'modbus'
  enabled: boolean
  config: ConnectionConfig
  address_count?: number
}

export interface AddressConfig {
  register: Register
  address: number
  encoding: Encoding
}

export interface Address {
  subject: string
  version: number
  driver: 'modbus'
  value_type: ValueType
  enabled: boolean
  connection: string
  config: AddressConfig
  telemetry_subject: string
}

export interface AlertState {
  version: number
  active: boolean
  pending: boolean
  property: string
  color: string
  abbreviation: string
  short_sign: string
  priority: number
  requires_acknowledgement: boolean
  text: string
  acknowledged: boolean
  came_time?: string
  went_time?: string
  ack_time?: string
  dominant?: string
  members?: string[]
  episode_id?: string
}

export interface AlertRecord {
  subject: string
  state: AlertState
}

export interface AlertProperties {
  subject: string
  version: number
  color: string
  abbreviation: string
  short_sign: string
  priority: number
  requires_acknowledgement: boolean
  reference_count?: number
}

export type AlertConfigType = 'binary' | 'value' | 'summary'
export type AlertValueType = 'int64' | 'float64'

export interface AlertMapping {
  property?: string
  text: string
}

export interface AlertBinaryConfig {
  bad_value: boolean
  true: AlertMapping
  false: AlertMapping
}

export interface AlertInterval {
  min: number | null
  max: number | null
  active: boolean
  property?: string
  text: string
}

export interface AlertValueConfig {
  value_type: AlertValueType
  intervals: AlertInterval[]
}

export interface AlertSummaryConfig {
  members: string[]
}

export interface AlertConfig {
  subject: string
  version: number
  enabled: boolean
  type: AlertConfigType
  binary?: AlertBinaryConfig
  value?: AlertValueConfig
  summary?: AlertSummaryConfig
  input_subject?: string
  output_subject?: string
}

export interface ApiErrorBody {
  error?: {
    code?: string
    message?: string
    fields?: Record<string, string>
  }
}

export interface LiveEvent {
  type: 'value' | 'alert' | 'error'
  subject: string
  telemetry_subject: string
  value?: unknown
  timestamp?: string
  message?: string
}

export const numericEncodings: Encoding[] = [
  'int16',
  'uint16',
  'int32',
  'uint32',
  'float32',
  'float64',
]

export function encodingsForRegister(register: Register): Encoding[] {
  return register === 'coil' || register === 'discrete_input'
    ? ['bool']
    : numericEncodings
}

export function valueTypeForEncoding(encoding: Encoding): ValueType {
  if (encoding === 'bool') return 'bool'
  if (encoding === 'float32' || encoding === 'float64') return 'float64'
  return 'int64'
}

export function registerCount(encoding: Encoding): number {
  if (encoding === 'float64') return 4
  if (['int32', 'uint32', 'float32'].includes(encoding)) return 2
  return 1
}

export function telemetrySubject(subject: string): string {
  return subject.endsWith('.address') ? subject.slice(0, -'.address'.length) : subject
}

export const emptyConnection = (): Connection => ({
  subject: '',
  version: 1,
  driver: 'modbus',
  enabled: true,
  config: {
    url: 'tcp://127.0.0.1:502',
    unit_id: 1,
    byte_order: 'big',
    word_order: 'big',
    timeout: '2s',
    poll_interval: '1s',
  },
})

export const emptyAddress = (connection = ''): Address => ({
  subject: '',
  version: 1,
  driver: 'modbus',
  value_type: 'int64',
  enabled: true,
  connection,
  config: { register: 'holding', address: 0, encoding: 'uint16' },
  telemetry_subject: '',
})

export const emptyAlertProperties = (): AlertProperties => ({
  subject: 'AlertProperties.',
  version: 1,
  color: '#dc2626',
  abbreviation: 'ALM',
  short_sign: '!',
  priority: 1,
  requires_acknowledgement: true,
})

export function alertInputSubject(subject: string): string {
  return subject.endsWith('.alert_config')
    ? subject.slice(0, -'.alert_config'.length)
    : subject
}

export function alertOutputSubject(subject: string): string {
  const input = alertInputSubject(subject)
  return input ? `${input}.alert` : ''
}

export function emptyAlertConfig(
  type: AlertConfigType = 'binary',
  property = '',
): AlertConfig {
  const base = {
    subject: '',
    version: 1,
    enabled: true,
    type,
  }
  if (type === 'binary') {
    return {
      ...base,
      binary: {
        bad_value: true,
        true: { property, text: 'Alarm active' },
        false: { text: 'Normal' },
      },
    }
  }
  if (type === 'value') {
    return {
      ...base,
      value: {
        value_type: 'float64',
        intervals: [
          { min: null, max: 0, active: false, text: 'Normal' },
          { min: 0, max: null, active: true, property, text: 'Alarm active' },
        ],
      },
    }
  }
  return { ...base, summary: { members: [] } }
}
