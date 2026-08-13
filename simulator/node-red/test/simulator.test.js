'use strict'

const test = require('node:test')
const assert = require('node:assert/strict')
const simulator = require('../lib/simulator')

test('encodes float32 as big-endian Modbus words', () => {
  assert.deepEqual(simulator.encodeFloat32(12.5), [0x4148, 0x0000])
  assert.deepEqual(simulator.encodeFloat32(-2.25), [0xc010, 0x0000])
})

test('automatic model remains inside physical bounds', () => {
  const state = simulator.initialState()
  for (let index = 0; index < 10000; index++) simulator.step(state, 1)
  assert.ok(state.level >= 0 && state.level <= 100)
  assert.ok(state.temperature >= -20 && state.temperature <= 130)
  assert.ok(state.pressure >= 0 && state.pressure <= 10)
  assert.ok(state.inletPosition >= 0 && state.inletPosition <= 100)
  assert.ok(state.outletPosition >= 0 && state.outletPosition <= 100)
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

test('register map float ranges do not overlap', () => {
  for (const slave of Object.values(simulator.REGISTER_MAP)) {
    const occupied = new Set()
    for (const address of Object.values(slave.floats)) {
      assert.equal(occupied.has(address), false)
      assert.equal(occupied.has(address + 1), false)
      occupied.add(address)
      occupied.add(address + 1)
    }
  }
})

test('every slave emits valid memory writes', () => {
  const writes = simulator.messagesForSlaves(simulator.initialState())
  for (const slaveWrites of Object.values(writes)) {
    assert.ok(slaveWrites.length > 0)
    for (const message of slaveWrites) {
      assert.ok(['input', 'discrete', 'coils'].includes(message.payload.register))
      assert.ok(Number.isInteger(message.payload.address))
      assert.ok(
        Number.isInteger(message.payload.value) ||
        (
          Array.isArray(message.payload.value) &&
          message.payload.value.length === 4 &&
          message.payload.value.every(Number.isInteger)
        )
      )
    }
  }
})
