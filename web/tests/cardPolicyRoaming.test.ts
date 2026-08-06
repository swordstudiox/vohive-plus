import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

const cardsService = await readFile(
  new URL('../src/services/cards.ts', import.meta.url),
  'utf8'
)
const devicesService = await readFile(
  new URL('../src/services/devices.ts', import.meta.url),
  'utf8'
)
const toggles = await readFile(
  new URL('../src/composables/useCardPolicyToggles.ts', import.meta.url),
  'utf8'
)
const cardPolicyPanel = await readFile(
  new URL('../src/components/CardPolicyPanel.vue', import.meta.url),
  'utf8'
)
const esimCardPolicyInline = await readFile(
  new URL('../src/components/EsimCardPolicyInline.vue', import.meta.url),
  'utf8'
)

test('card policy service accepts roaming_enabled in PUT payload', () => {
  assert.match(cardsService, /roaming_enabled\?: boolean/)
})

test('devices service exposes live roaming switch endpoint', () => {
  assert.match(devicesService, /setRoaming\(id: string, roamingEnabled: boolean\)/)
  assert.match(devicesService, /api\.patch\(`\/devices\/\$\{id\}\/roaming`, \{ enabled: roamingEnabled \}\)/)
})

test('card policy composable tracks roaming as independent switch', () => {
  assert.match(toggles, /roaming_enabled: boolean/)
  assert.match(toggles, /applyRoaming: \(enabled: boolean, next: PolicyMirror\) => Promise<ToggleResult>/)
  assert.match(toggles, /onRoamingToggle/)
})

test('main card policy panel renders cellular registration, cellular data, and data roaming switches', () => {
  assert.match(cardPolicyPanel, /驻网与短信/)
  assert.match(cardPolicyPanel, /蜂窝数据/)
  assert.match(cardPolicyPanel, /数据漫游/)
  assert.doesNotMatch(cardPolicyPanel, /关闭后模块不会注册漫游网络/)
  assert.match(cardPolicyPanel, /devicesService\.setRoaming/)
  assert.match(cardPolicyPanel, /@change="onRoamingToggle"/)
})

test('eSIM inline card policy renders cellular registration, cellular data, and data roaming switches', () => {
  assert.match(esimCardPolicyInline, /驻网与短信/)
  assert.match(esimCardPolicyInline, /蜂窝数据/)
  assert.match(esimCardPolicyInline, /数据漫游/)
  assert.match(esimCardPolicyInline, /onRoamingToggle/)
})
