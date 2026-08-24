'use strict'

const test = require('node:test')
const assert = require('node:assert/strict')
const simulator = require('../lib/simulator')

function isValidWriteValue (value) {
  if (Number.isInteger(value)) return true
  return Array.isArray(value) && value.length > 0 && value.every(Number.isInteger)
}

test('encodes float32 as big-endian Modbus words', () => {
  assert.deepEqual(simulator.encodeFloat32(12.5), [0x4148, 0x0000])
  assert.deepEqual(simulator.encodeFloat32(-2.25), [0xc010, 0x0000])
})

test('each instance starts with ten independent tanks', () => {
  const state = simulator.initialState()
  assert.equal(state.tanks.length, simulator.TANK_COUNT)
  assert.equal(simulator.TANK_COUNT, 10)
  const levels = new Set(state.tanks.map(tank => tank.level))
  const seeds = new Set(state.tanks.map(tank => tank.seed))
  assert.equal(levels.size, simulator.TANK_COUNT)
  assert.equal(seeds.size, simulator.TANK_COUNT)
})

test('automatic model remains inside physical bounds', () => {
  const state = simulator.initialState()
  for (let index = 0; index < 10000; index++) simulator.step(state, 1)
  for (const tank of state.tanks) {
    assert.ok(tank.level >= 0 && tank.level <= 100)
    assert.ok(tank.temperature >= -20 && tank.temperature <= 130)
    assert.ok(tank.pressure >= 0 && tank.pressure <= 10)
    assert.ok(tank.inletPosition >= 0 && tank.inletPosition <= 100)
    assert.ok(tank.outletPosition >= 0 && tank.outletPosition <= 100)
  }
})

test('tanks keep distinct process values after a long automatic run', () => {
  const state = simulator.initialState()
  for (let index = 0; index < 200; index++) simulator.step(state, 1)
  const signatures = new Set(
    state.tanks.map(tank => `${tank.level.toFixed(4)}:${tank.pressure.toFixed(4)}`)
  )
  assert.ok(signatures.size > 1)
})

test('pump trip deterministically stops the selected pump', () => {
  const state = simulator.initialState()
  simulator.applyControl(state, 'automatic', false)
  simulator.applyControl(state, 'pump1Command', true)
  simulator.applyControl(state, 'pump1Trip', true)
  for (let index = 0; index < 30; index++) simulator.step(state, 1)
  assert.ok(state.pump1Speed < 0.1)
  assert.equal(simulator.dashboardState(state).status.pump1Running, false)
})

test('sensor freeze preserves published analog measurements', () => {
  const state = simulator.initialState()
  simulator.applyControl(state, 'sensorFreeze', true)
  const frozen = simulator.measurements(state)
  for (let index = 0; index < 20; index++) simulator.step(state, 1)
  assert.deepEqual(simulator.measurements(state), frozen)
  simulator.applyControl(state, 'sensorFreeze', false)
  assert.notDeepEqual(simulator.measurements(state), frozen)
})

test('tank valve commands and faults apply to the selected tank', () => {
  const state = simulator.initialState()
  simulator.applyControl(state, 'automatic', false)
  simulator.applyControl(state, 'selectedTank', 3)
  simulator.applyControl(state, 'inletCommand', 12)
  simulator.applyControl(state, 'inletStuck', true)
  assert.equal(state.tanks[3].inletCommand, 12)
  assert.equal(state.tanks[3].faults.inletStuck, true)
  assert.equal(state.tanks[0].inletCommand, 70)
  assert.equal(state.tanks[0].faults.inletStuck, false)
})

test('register map float ranges do not overlap', () => {
  for (const [name, slave] of Object.entries(simulator.REGISTER_MAP)) {
    const occupied = new Set()
    const count = name === 'tank' ? slave.count : 1
    const stride = name === 'tank' ? slave.floatStride : 0
    for (let index = 0; index < count; index++) {
      for (const address of Object.values(slave.floats)) {
        const start = index * stride + address
        assert.equal(occupied.has(start), false)
        assert.equal(occupied.has(start + 1), false)
        occupied.add(start)
        occupied.add(start + 1)
      }
    }
  }
})

test('tank ten publishes at the last 20-register block', () => {
  const writes = simulator.messagesForSlaves(simulator.initialState())
  const tankTenLevel = writes.tank.find(
    message => message.payload.register === 'input' &&
      message.payload.address === simulator.tankFloatAddress(9, 'level') / 4
  )
  assert.ok(tankTenLevel)
  assert.equal(simulator.tankFloatAddress(9, 'level'), 180)
  assert.equal(simulator.tankFloatAddress(9, 'outletPosition'), 196)
  assert.equal(simulator.tankDiscreteAddress(9, 'sensorBad'), 29)
  assert.equal(simulator.tankCoilAddress(9, 'outletOpen'), 19)
})

test('tank discrete and coil bitfields cover every tank', () => {
  const writes = simulator.messagesForSlaves(simulator.initialState())
  const discrete = writes.tank.find(message => message.payload.register === 'discrete')
  const coils = writes.tank.find(message => message.payload.register === 'coils')
  assert.ok(Array.isArray(discrete.payload.value))
  assert.ok(discrete.payload.value.length * 8 >= simulator.TANK_COUNT * simulator.TANK_DISCRETE_STRIDE)
  assert.ok(Array.isArray(coils.payload.value))
  assert.ok(coils.payload.value.length * 8 >= simulator.TANK_COUNT * simulator.TANK_COIL_STRIDE)
})

test('every slave emits valid memory writes', () => {
  const writes = simulator.messagesForSlaves(simulator.initialState())
  assert.ok(writes.tank.filter(message => message.payload.register === 'input').length >=
    simulator.TANK_COUNT * Object.keys(simulator.REGISTER_MAP.tank.floats).length)
  for (const slaveWrites of Object.values(writes)) {
    assert.ok(slaveWrites.length > 0)
    for (const message of slaveWrites) {
      assert.ok(['input', 'discrete', 'coils'].includes(message.payload.register))
      assert.ok(Number.isInteger(message.payload.address))
      assert.ok(isValidWriteValue(message.payload.value))
    }
  }
})
