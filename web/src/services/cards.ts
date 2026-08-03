import { api } from '../stores/auth'
import { callService } from './http'
import type { CardPolicy } from '../types/api'

type PutCardPolicyRequest = {
  network_enabled?: boolean
  vowifi_enabled?: boolean
  airplane_enabled?: boolean
  roaming_enabled?: boolean
  ip_version?: string
  apn?: string
}

export const cardsService = {
  getPolicy(iccid: string) {
    return callService(async () => {
      const res = await api.get<CardPolicy>(`/cards/${iccid}/policy`)
      return res.data
    })
  },

  putPolicy(iccid: string, req: PutCardPolicyRequest) {
    return callService(async () => {
      const res = await api.put<CardPolicy>(`/cards/${iccid}/policy`, req)
      return res.data
    })
  },

  listPolicies() {
    return callService(async () => {
      const res = await api.get<{ policies: CardPolicy[] }>('/cards/policies')
      return res.data.policies ?? []
    })
  }
}
