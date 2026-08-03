import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

const devicesView = await readFile(
  new URL('../src/views/Devices.vue', import.meta.url),
  'utf8'
)

const cardPolicyPanel = await readFile(
  new URL('../src/components/CardPolicyPanel.vue', import.meta.url),
  'utf8'
)

test('card policy fetch clears stale policy and ignores late responses for old ICCIDs', () => {
  assert.match(devicesView, /cardPolicyRequestSeq/)
  assert.match(devicesView, /const requestSeq = \+\+cardPolicyRequestSeq\.value/)

  const fnStart = devicesView.indexOf('async function fetchCardPolicy')
  assert.ok(fnStart >= 0, 'fetchCardPolicy() not found')
  const fnBody = devicesView.slice(fnStart, devicesView.indexOf('// 卡策略热切换后', fnStart))
  assert.match(fnBody, /cardPolicy\.value = null/)
  assert.match(fnBody, /requestSeq !== cardPolicyRequestSeq\.value/)
  assert.match(fnBody, /samePolicyICCID\(result\.data\.iccid, iccid\)/)
})

test('card policy panel only enables switches when policy belongs to current ICCID', () => {
  assert.match(cardPolicyPanel, /policyMatchesCurrentCard/)
  assert.match(cardPolicyPanel, /samePolicyICCID\(props\.policy\?\.iccid, props\.iccid\)/)
  assert.match(cardPolicyPanel, /props\.deviceOnline && !!props\.iccid && policyMatchesCurrentCard\.value/)
})
