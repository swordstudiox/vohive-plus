import { ref } from 'vue'
import { useCardPolicyToggles, type PolicyMirror } from './useCardPolicyToggles'

const src = ref<PolicyMirror | null>(null)
const toggles = useCardPolicyToggles(src, {
  applyNetwork: async () => ({ ok: true }),
  applyVoWiFi: async () => ({ ok: true }),
  applyAirplane: async () => ({ ok: true }),
  applyRoaming: async () => ({ ok: true }),
  onChanged: () => {}
})

void toggles.local
void toggles.networkPending
void toggles.onNetworkToggle
void toggles.onVoWiFiToggle
void toggles.onAirplaneToggle
void toggles.onRoamingToggle
