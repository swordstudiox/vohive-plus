import assert from 'node:assert/strict'
import test from 'node:test'
import { normalizeDisplayVersion } from '../src/utils/version'

test('normalizeDisplayVersion falls back for empty and unknown values', () => {
  assert.equal(normalizeDisplayVersion('', '1.0.3'), '1.0.3')
  assert.equal(normalizeDisplayVersion('   ', '1.0.3'), '1.0.3')
  assert.equal(normalizeDisplayVersion('Unknown', '1.0.3'), '1.0.3')
  assert.equal(normalizeDisplayVersion('unknown', '1.0.3'), '1.0.3')
})

test('normalizeDisplayVersion keeps real version strings', () => {
  assert.equal(normalizeDisplayVersion('1.0.3', '1.0.2'), '1.0.3')
  assert.equal(normalizeDisplayVersion(' v1.0.3 ', '1.0.2'), 'v1.0.3')
})
