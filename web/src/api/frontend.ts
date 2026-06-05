import { api } from './client'
import type {
  FrontendAppConfig,
  FrontendAppConfigInput,
  FrontendDeployInput,
  FrontendDeployResult,
  FrontendGateway,
  FrontendGatewayApply,
  FrontendGatewayEnsureInput,
  FrontendGatewayPreview,
  FrontendGatewayRoute,
  FrontendTemplatePreview,
} from '../types'

export async function getFrontendConfig(appId: number, serviceCode = 'web'): Promise<FrontendAppConfig> {
  const r = await api.get<FrontendAppConfig>(`/apps/${appId}/frontend-config`, {
    params: { service_code: serviceCode },
  })
  return r.data
}

export async function saveFrontendConfig(appId: number, input: FrontendAppConfigInput): Promise<FrontendAppConfig> {
  const r = await api.put<FrontendAppConfig>(`/apps/${appId}/frontend-config`, input)
  return r.data
}

export async function previewFrontendConfig(appId: number, serviceCode = 'web'): Promise<FrontendTemplatePreview> {
  const r = await api.get<FrontendTemplatePreview>(`/apps/${appId}/frontend-config/preview`, {
    params: { service_code: serviceCode },
  })
  return r.data
}

export async function deployFrontendApp(appId: number, input: FrontendDeployInput): Promise<FrontendDeployResult> {
  const r = await api.post<FrontendDeployResult>(`/apps/${appId}/frontend-deploy`, input, {
    timeout: 15 * 60_000,
  })
  return r.data
}

export async function ensureFrontendGateway(hostId: number, input: FrontendGatewayEnsureInput = {}): Promise<FrontendGateway> {
  const r = await api.put<FrontendGateway>(`/hosts/${hostId}/frontend-gateway`, input, {
    timeout: 120_000,
  })
  return r.data
}

export async function getFrontendGateway(hostId: number): Promise<FrontendGateway> {
  const r = await api.get<FrontendGateway>(`/hosts/${hostId}/frontend-gateway`)
  return r.data
}

export async function applyFrontendGateway(hostId: number, forceRecreate = false): Promise<FrontendGatewayApply> {
  const r = await api.post<FrontendGatewayApply>(`/hosts/${hostId}/frontend-gateway/apply`, undefined, {
    params: { force_recreate: forceRecreate ? 1 : 0 },
    timeout: 120_000,
  })
  return r.data
}

export async function listFrontendGatewayRoutes(hostId: number): Promise<FrontendGatewayRoute[]> {
  const r = await api.get<{ items: FrontendGatewayRoute[] }>(`/hosts/${hostId}/frontend-gateway/routes`)
  return r.data.items ?? []
}

export async function previewFrontendGatewayRoute(routeId: number): Promise<FrontendGatewayPreview> {
  const r = await api.get<FrontendGatewayPreview>(`/frontend-gateway/routes/${routeId}/preview`)
  return r.data
}
