import { Tag } from 'antd'
import type { ReactNode } from 'react'

export type StatusTone = 'success' | 'warning' | 'danger' | 'info' | 'neutral' | 'processing'

const toneColor: Record<StatusTone, string> = {
  success: 'green',
  warning: 'gold',
  danger: 'red',
  info: 'blue',
  neutral: 'default',
  processing: 'processing',
}

type StatusTagProps = {
  tone?: StatusTone
  children: ReactNode
  title?: string
}

export function StatusTag({ tone = 'neutral', children, title }: StatusTagProps) {
  return (
    <Tag className={`status-tag status-tag--${tone}`} color={toneColor[tone]} title={title}>
      <span className="status-tag-dot" aria-hidden="true" />
      <span>{children}</span>
    </Tag>
  )
}

export function statusToneFromRuntime(status?: string): StatusTone {
  if (status === 'running') return 'success'
  if (status === 'failed' || status === 'stopped') return 'danger'
  if (status === 'pending') return 'warning'
  return 'neutral'
}

export function statusToneFromPipeline(status?: string): StatusTone {
  if (status === 'success') return 'success'
  if (status === 'failed' || status === 'cancelled') return 'danger'
  if (status === 'running') return 'processing'
  if (status === 'pending') return 'warning'
  return 'neutral'
}

export function statusToneFromBuild(status?: string): StatusTone {
  if (status === 'success') return 'success'
  if (status === 'failed' || status === 'cancelled') return 'danger'
  if (status === 'building') return 'processing'
  return 'neutral'
}

export function statusToneFromHost(status?: string): StatusTone {
  if (status === 'online') return 'success'
  if (status === 'offline') return 'danger'
  return 'neutral'
}
