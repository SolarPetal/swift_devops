import { api } from './client'
import type { Artifact, ArtifactBundle, ArtifactInput } from '../types'

export async function listArtifacts(appId?: number): Promise<Artifact[]> {
  const r = await api.get<{ items: Artifact[] }>('/artifacts', {
    params: appId ? { app_id: appId } : undefined,
  })
  return r.data.items
}

export async function createArtifact(input: ArtifactInput): Promise<Artifact> {
  const r = await api.post<Artifact>('/artifacts', input)
  return r.data
}

export async function deleteArtifact(id: number): Promise<void> {
  await api.delete(`/artifacts/${id}`)
}

// uploadArtifact 走 multipart/form-data，把 file 流式发到 POST /artifacts/upload。
// onProgress 收到 0~100 的整数百分比；浏览器拿不到上传完成事件时不会回调，调用方需要自行兜底 100%。
export async function uploadArtifact(
  appId: number,
  versionTag: string,
  file: File | Blob,
  fileName?: string,
  onProgress?: (percent: number) => void,
): Promise<Artifact> {
  const fd = new FormData()
  fd.append('app_id', String(appId))
  fd.append('version_tag', versionTag)
  // 第三个参数显式给文件名，避免某些浏览器把 Blob 当 "blob"
  fd.append('file', file, fileName ?? (file instanceof File ? file.name : 'upload.bin'))

  const r = await api.post<Artifact>('/artifacts/upload', fd, {
    timeout: 5 * 60_000, // 上传最多 5 分钟
    // 注意：千万别手设 Content-Type；axios 会自动给 FormData 加上带 boundary 的 multipart/form-data。
    // 手设会把 boundary 抹掉，server 端 multipart 解析必 400。
    onUploadProgress: (e) => {
      if (!onProgress) return
      const total = e.total ?? 0
      if (total > 0) {
        onProgress(Math.round((e.loaded / total) * 100))
      }
    },
  })
  return r.data
}

// Sprint X.6：多 service 整组制品（ArtifactBundle）
export async function listBundles(appId?: number): Promise<ArtifactBundle[]> {
  const r = await api.get<{ items: ArtifactBundle[] }>('/bundles', {
    params: appId ? { app_id: appId } : undefined,
  })
  return r.data.items ?? []
}

// Sprint 5.7：滚动清理历史 Bundle（删旧 jar 文件）。
// keep 不传则用服务端 storage.max_history；被部署/回滚链引用的版本会被自动跳过。
export type CleanupResult = {
  app_id: number
  keep: number
  examined: number
  deleted_bundles: number[] | null
  skipped_in_use: number[] | null
  freed_bytes: number
}

export async function cleanupBundleHistory(appId: number, keep?: number): Promise<CleanupResult> {
  const r = await api.post<CleanupResult>(`/apps/${appId}/artifacts/cleanup`, null, {
    params: keep ? { keep } : undefined,
  })
  return r.data
}
