'use strict'

const REGISTER_MAP = Object.freeze({
  tank: Object.freeze({
    port: 1502,
    unitId: 1,
    floats: Object.freeze({
      level: 0,
      temperature: 4,
      pressure: 8,
      inletPosition: 12,
      outletPosition: 16
    }),
    discrete: Object.freeze({ levelHigh: 0, levelLow: 1, sensorBad: 2 }),
    coils: Object.freeze({ inletOpen: 0, outletOpen: 1 })
  }),
  pumps: Object.freeze({
    port: 1503,
    unitId: 2,
    floats: Object.freeze({
      pump1Speed: 0,
      pump1Current: 4,
      dischargePressure: 8,
      pump2Speed: 12,
      pump2Current: 16,
      totalFlow: 20,
      vibration: 24
    }),
    discrete: Object.freeze({
      pump1Running: 0,
      pump1Trip: 1,
      pump2Running: 2,
      pump2Trip: 3
    })
  }),
  utility: Object.freeze({
    port: 1504,
    unitId: 3,
    floats: Object.freeze({
      processFlow: 0,
      ambientTemperature: 4,
      conductivity: 8,
      coolingValvePosition: 12
    }),
    discrete: Object.freeze({ temperatureHigh: 0, flowLow: 1 }),
    coils: Object.freeze({ coolingValveOpen: 0 })
  })
})

const DEFAULT_CONTROLS = Object.freeze({
  automatic: true,
  speed: 1,
  inletCommand: 70,
  outletCommand: 45,
  coolingCommand: 40,
  pump1Command: true,
  pump2Command: false
})

const DEFAULT_FAULTS = Object.freeze({
  pump1Trip: false,
  pump2Trip: false,
  inletStuck: false,
  outletStuck: false,
  highTemperature: false,
  sensorFreeze: false,
  sensorBad: false
})

function clamp (value, min, max) {
  return Math.min(max, Math.max(min, value))
}

function initialState () {
  return {
    tick: 0,
    seed: 0x5ca1ab1e,
    level: 55,
    temperature: 24,
    pressure: 2.1,
    inletPosition: 70,
    outletPosition: 45,
    coolingValvePosition: 40,
    pump1Speed: 72,
    pump2Speed: 0,
    pump1Current: 18,
    pump2Current: 0,
    dischargePressure: 3.4,
    totalFlow: 42,
    vibration: 1.2,
    ambientTemperature: 21,
    conductivity: 480,
    controls: { ...DEFAULT_CONTROLS },
    faults: { ...DEFAULT_FAULTS },
    frozenMeasurements: null
  }
}

function nextNoise (state, amplitude) {
  state.seed = (Math.imul(state.seed, 1664525) + 1013904223) >>> 0
  return ((state.seed / 0xffffffff) * 2 - 1) * amplitude
}

function approach (value, target, amount) {
  return value + (target - value) * clamp(amount, 0, 1)
}

function step (previous, elapsedSeconds = 1) {
  const state = previous || initialState()
  const dt = clamp(Number(elapsedSeconds) || 1, 0.05, 10) *
    clamp(Number(state.controls.speed) || 1, 0.1, 10)

  if (state.controls.automatic) {
    const levelError = 60 - state.level
    state.controls.inletCommand = clamp(58 + levelError * 1.8, 5, 100)
    state.controls.outletCommand = clamp(42 - levelError * 0.7, 5, 90)
    state.controls.pump1Command = state.level > 18
    state.controls.pump2Command = state.level > 72
    state.controls.coolingCommand = clamp(35 + (state.temperature - 25) * 5, 10, 100)
  }

  if (!state.faults.inletStuck) {
    state.inletPosition = approach(state.inletPosition, state.controls.inletCommand, 0.18 * dt)
  }
  if (!state.faults.outletStuck) {
    state.outletPosition = approach(state.outletPosition, state.controls.outletCommand, 0.18 * dt)
  }
  state.coolingValvePosition = approach(
    state.coolingValvePosition,
    state.controls.coolingCommand,
    0.15 * dt
  )

  const pump1Running = Boolean(state.controls.pump1Command && !state.faults.pump1Trip && state.level > 3)
  const pump2Running = Boolean(state.controls.pump2Command && !state.faults.pump2Trip && state.level > 3)
  state.pump1Speed = approach(state.pump1Speed, pump1Running ? 72 : 0, 0.25 * dt)
  state.pump2Speed = approach(state.pump2Speed, pump2Running ? 65 : 0, 0.25 * dt)

  const inletFlow = state.inletPosition * 0.42
  const valveOutflow = state.outletPosition * 0.16
  const pumpedFlow = state.pump1Speed * 0.36 + state.pump2Speed * 0.32
  state.totalFlow = Math.max(0, valveOutflow + pumpedFlow + nextNoise(state, 0.25))
  state.level = clamp(state.level + (inletFlow - state.totalFlow) * 0.018 * dt, 0, 100)

  const temperatureTarget = state.faults.highTemperature
    ? 96
    : state.ambientTemperature + 3 + (pump1Running ? 2.5 : 0) + (pump2Running ? 2 : 0)
  const cooling = state.coolingValvePosition * 0.018
  state.temperature = clamp(
    state.temperature + (temperatureTarget - state.temperature) * 0.012 * dt - cooling * 0.025 * dt,
    -20,
    130
  )
  state.pressure = clamp(0.8 + state.level * 0.027 + pumpedFlow * 0.035 + nextNoise(state, 0.015), 0, 10)
  state.dischargePressure = clamp(0.6 + pumpedFlow * 0.075 + nextNoise(state, 0.02), 0, 12)
  state.pump1Current = Math.max(0, state.pump1Speed * 0.25 + nextNoise(state, 0.08))
  state.pump2Current = Math.max(0, state.pump2Speed * 0.27 + nextNoise(state, 0.08))
  state.vibration = Math.max(0, 0.25 + (state.pump1Speed + state.pump2Speed) * 0.009 + nextNoise(state, 0.03))
  state.ambientTemperature = clamp(21 + Math.sin(state.tick / 90) * 2, 15, 30)
  state.conductivity = clamp(470 + state.level * 0.25 + nextNoise(state, 1.2), 0, 2000)
  state.tick += 1
  return state
}

function applyControl (state, topic, payload) {
  const target = state || initialState()
  const booleanValue = payload === true || payload === 'true' || payload === 1 || payload === '1'
  const numericValue = Number(payload)
  const controls = {
    automatic: value => { target.controls.automatic = value },
    speed: value => { target.controls.speed = clamp(value, 0.1, 10) },
    inletCommand: value => { target.controls.inletCommand = clamp(value, 0, 100) },
    outletCommand: value => { target.controls.outletCommand = clamp(value, 0, 100) },
    coolingCommand: value => { target.controls.coolingCommand = clamp(value, 0, 100) },
    pump1Command: value => { target.controls.pump1Command = value },
    pump2Command: value => { target.controls.pump2Command = value }
  }
  const faults = Object.keys(DEFAULT_FAULTS)

  if (Object.hasOwn(controls, topic)) {
    if (['automatic', 'pump1Command', 'pump2Command'].includes(topic)) controls[topic](booleanValue)
    else if (Number.isFinite(numericValue)) controls[topic](numericValue)
  } else if (faults.includes(topic)) {
    target.faults[topic] = booleanValue
    if (topic === 'sensorFreeze') {
      target.frozenMeasurements = booleanValue ? measurements(target, false) : null
    }
  } else if (topic === 'resetFaults') {
    target.faults = { ...DEFAULT_FAULTS }
    target.frozenMeasurements = null
  }
  return target
}

function measurements (state, applySensorFaults = true) {
  if (applySensorFaults && state.faults.sensorFreeze && state.frozenMeasurements) {
    return { ...state.frozenMeasurements, sensorBad: false }
  }
  const values = {
    level: state.level,
    temperature: state.temperature,
    pressure: state.pressure,
    inletPosition: state.inletPosition,
    outletPosition: state.outletPosition,
    pump1Speed: state.pump1Speed,
    pump1Current: state.pump1Current,
    dischargePressure: state.dischargePressure,
    pump2Speed: state.pump2Speed,
    pump2Current: state.pump2Current,
    totalFlow: state.totalFlow,
    vibration: state.vibration,
    ambientTemperature: state.ambientTemperature,
    conductivity: state.conductivity,
    coolingValvePosition: state.coolingValvePosition,
    sensorBad: state.faults.sensorBad
  }
  if (applySensorFaults && state.faults.sensorBad) {
    values.level = 999.9
    values.temperature = 999.9
  }
  return values
}

function encodeFloat32 (value) {
  const buffer = Buffer.allocUnsafe(4)
  buffer.writeFloatBE(Number(value), 0)
  return [buffer.readUInt16BE(0), buffer.readUInt16BE(2)]
}

function floatWrites (register, address, value) {
  const buffer = Buffer.allocUnsafe(4)
  buffer.writeFloatBE(Number(value), 0)
  return {
    payload: {
      register,
      address: address / 4,
      value: Array.from(buffer)
    }
  }
}

function bitFieldWrite (register, values) {
  const packed = values.reduce(
    (result, value, index) => result | (value ? 1 << index : 0),
    0
  )
  return { payload: { register, address: 0, value: packed } }
}

function messagesForSlaves (state) {
  const value = measurements(state)
  const pump1Running = state.pump1Speed > 5 && !state.faults.pump1Trip
  const pump2Running = state.pump2Speed > 5 && !state.faults.pump2Trip
  const tank = []
  for (const [name, address] of Object.entries(REGISTER_MAP.tank.floats)) {
    tank.push(floatWrites('input', address, value[name]))
  }
  tank.push(
    bitFieldWrite('discrete', [state.level >= 80, state.level <= 20, value.sensorBad]),
    bitFieldWrite('coils', [state.inletPosition > 5, state.outletPosition > 5])
  )

  const pumps = []
  for (const [name, address] of Object.entries(REGISTER_MAP.pumps.floats)) {
    pumps.push(floatWrites('input', address, value[name]))
  }
  pumps.push(
    bitFieldWrite('discrete', [
      pump1Running,
      state.faults.pump1Trip,
      pump2Running,
      state.faults.pump2Trip
    ])
  )

  const utility = []
  for (const [name, address] of Object.entries(REGISTER_MAP.utility.floats)) {
    const sourceName = name === 'processFlow' ? 'totalFlow' : name
    utility.push(floatWrites('input', address, value[sourceName]))
  }
  utility.push(
    bitFieldWrite('discrete', [value.temperature >= 80, value.totalFlow < 10]),
    bitFieldWrite('coils', [state.coolingValvePosition > 5])
  )
  return { tank, pumps, utility }
}

function dashboardState (state) {
  const value = measurements(state)
  return {
    ...value,
    controls: { ...state.controls },
    faults: { ...state.faults },
    status: {
      levelHigh: state.level >= 80,
      levelLow: state.level <= 20,
      pump1Running: state.pump1Speed > 5 && !state.faults.pump1Trip,
      pump2Running: state.pump2Speed > 5 && !state.faults.pump2Trip,
      temperatureHigh: state.temperature >= 80,
      flowLow: state.totalFlow < 10
    }
  }
}

module.exports = {
  REGISTER_MAP,
  DEFAULT_CONTROLS,
  DEFAULT_FAULTS,
  initialState,
  step,
  applyControl,
  measurements,
  encodeFloat32,
  messagesForSlaves,
  dashboardState
}
