import { describe, expect, it } from 'vitest'
import {
  alertInputSubject,
  alertOutputSubject,
  emptyAlertConfig,
  encodingsForRegister,
  registerCount,
  telemetrySubject,
  valueTypeForEncoding,
} from './models'

describe('Modbus model constraints', () => {
  it('limits bit registers to bool encoding', () => {
    expect(encodingsForRegister('coil')).toEqual(['bool'])
    expect(encodingsForRegister('discrete_input')).toEqual(['bool'])
    expect(encodingsForRegister('holding')).not.toContain('bool')
  })

  it('derives value types and register widths', () => {
    expect(valueTypeForEncoding('bool')).toBe('bool')
    expect(valueTypeForEncoding('uint32')).toBe('int64')
    expect(valueTypeForEncoding('float64')).toBe('float64')
    expect(registerCount('uint16')).toBe(1)
    expect(registerCount('float32')).toBe(2)
    expect(registerCount('float64')).toBe(4)
  })

  it('derives the telemetry subject', () => {
    expect(telemetrySubject('plant.line1.temperature.address')).toBe(
      'plant.line1.temperature',
    )
  })
})

describe('Alert configuration models', () => {
  it('derives input and output subjects', () => {
    expect(alertInputSubject('tank.temperature.alert_config')).toBe('tank.temperature')
    expect(alertOutputSubject('tank.temperature.alert_config')).toBe('tank.temperature.alert')
  })

  it('creates contiguous default value intervals', () => {
    const config = emptyAlertConfig('value', 'AlertProperties.Alarm')
    expect(config.value?.intervals).toEqual([
      { min: null, max: 0, active: false, text: 'Normal' },
      {
        min: 0,
        max: null,
        active: true,
        property: 'AlertProperties.Alarm',
        text: 'Alarm active',
      },
    ])
  })
})
