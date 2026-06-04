import type { ReactNode } from 'react'
import { Empty } from 'antd'

type PageHeaderProps = {
  eyebrow: string
  title: ReactNode
  description?: ReactNode
  actions?: ReactNode
  meta?: ReactNode
}

export function PageHeader({ eyebrow, title, description, actions, meta }: PageHeaderProps) {
  return (
    <header className="page-hero">
      <div className="page-hero-copy">
        <div className="page-eyebrow">{eyebrow}</div>
        <h1 className="page-title">{title}</h1>
        {description && <div className="page-description">{description}</div>}
        {meta && <div className="page-meta">{meta}</div>}
      </div>
      {actions && <div className="page-actions">{actions}</div>}
    </header>
  )
}

export function StatGrid({ children }: { children: ReactNode }) {
  return <div className="stat-grid">{children}</div>
}

type StatCardProps = {
  label: ReactNode
  value: ReactNode
  description?: ReactNode
  tone?: 'default' | 'success' | 'warning' | 'danger' | 'info'
}

export function StatCard({ label, value, description, tone = 'default' }: StatCardProps) {
  return (
    <div className={`stat-card stat-card--${tone}`}>
      <div className="stat-label">{label}</div>
      <div className="stat-value">{value}</div>
      {description && <div className="stat-description">{description}</div>}
    </div>
  )
}

type EmptyStateProps = {
  title: string
  description?: ReactNode
}

export function EmptyState({ title, description }: EmptyStateProps) {
  return (
    <div className="empty-state">
      <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={false} />
      <div className="empty-title">{title}</div>
      {description && <div className="empty-description">{description}</div>}
    </div>
  )
}
