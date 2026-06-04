import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Alert, Button, Card, Progress, Skeleton, Table, Typography } from 'antd'

import { listApps } from '../api/app'
import { listBuilds } from '../api/build'
import { getBuilderEnv } from '../api/builderEnv'
import { formatError } from '../api/client'
import { listGitCreds } from '../api/gitcred'
import { listHostMetrics, listHosts } from '../api/host'
import { listPipelines } from '../api/pipeline'
import { EmptyState, PageHeader, StatCard, StatGrid } from '../components/PageFrame'
import {
  StatusTag,
  type StatusTone,
  statusToneFromBuild,
  statusToneFromHost,
  statusToneFromPipeline,
} from '../components/StatusTag'
import type { App, BuildRun, BuilderEnv, GitCredential, Host, HostMetrics, PipelineRun } from '../types'

type OverviewSnapshot = {
  apps: App[]
  hosts: Host[]
  creds: GitCredential[]
  builderEnv: BuilderEnv | null
  pipelines: PipelineRun[]
  builds: BuildRun[]
  hostMetrics: HostMetrics[]
  errors: string[]
}

type Guarded<T> = {
  data: T | null
  error?: string
}

async function guarded<T>(promise: Promise<T>): Promise<Guarded<T>> {
  try {
    return { data: await promise }
  } catch (e) {
    return { data: null, error: formatError(e) }
  }
}

function timeOf(value?: string) {
  if (!value) return 0
  const time = new Date(value).getTime()
  return Number.isFinite(time) ? time : 0
}

function formatTime(value?: string) {
  if (!value) return '—'
  const time = new Date(value)
  if (Number.isNaN(time.getTime())) return value
  return time.toLocaleString()
}

function compactText(value: string, max = 160) {
  const normalized = value.replace(/\s+/g, ' ').trim()
  if (normalized.length <= max) return normalized
  return `${normalized.slice(0, max)}…`
}

function formatPercent(value?: number) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '—'
  return `${Math.max(0, Math.min(100, value)).toFixed(1)}%`
}

function progressStatus(value?: number): 'normal' | 'active' | 'exception' | 'success' {
  if (typeof value !== 'number' || !Number.isFinite(value)) return 'normal'
  if (value >= 90) return 'exception'
  if (value >= 75) return 'active'
  return 'normal'
}

function averageMetric(items: HostMetrics[], key: 'cpu_usage' | 'memory_usage' | 'disk_usage') {
  const values = items
    .filter((item) => !item.error && typeof item[key] === 'number' && Number.isFinite(item[key]))
    .map((item) => item[key])
  if (values.length === 0) return null
  return values.reduce((sum, value) => sum + value, 0) / values.length
}

function MetricCell({ value }: { value?: number }) {
  const percent = typeof value === 'number' && Number.isFinite(value)
    ? Math.max(0, Math.min(100, value))
    : 0
  return (
    <div className="metric-cell">
      <div className="metric-cell-header">
        <span>{formatPercent(value)}</span>
      </div>
      <Progress
        percent={percent}
        showInfo={false}
        size="small"
        status={progressStatus(value)}
      />
    </div>
  )
}

function pipelineStatusText(status: PipelineRun['status']) {
  const map: Record<PipelineRun['status'], string> = {
    pending: '待执行',
    running: '部署中',
    success: '成功',
    failed: '失败',
    cancelled: '已取消',
  }
  return map[status] ?? status
}

function buildStatusText(status: BuildRun['status']) {
  const map: Record<BuildRun['status'], string> = {
    building: '构建中',
    success: '成功',
    failed: '失败',
    cancelled: '已取消',
  }
  return map[status] ?? status
}

function hostStatusText(status: Host['status']) {
  const map: Record<Host['status'], string> = {
    online: '在线',
    offline: '离线',
    unknown: '未知',
  }
  return map[status] ?? status
}

function ActionItem({
  tone,
  label,
  title,
  description,
  to,
}: {
  tone: StatusTone
  label: string
  title: string
  description: string
  to?: string
}) {
  return (
    <div className="action-item">
      <StatusTag tone={tone}>{label}</StatusTag>
      <div>
        <div className="action-item-title">{title}</div>
        <div className="action-item-desc">
          {description}
          {to && (
            <>
              {' '}
              <Link to={to}>去处理</Link>
            </>
          )}
        </div>
      </div>
    </div>
  )
}

export default function OverviewPage() {
  const nav = useNavigate()
  const [snapshot, setSnapshot] = useState<OverviewSnapshot>({
    apps: [],
    hosts: [],
    creds: [],
    builderEnv: null,
    pipelines: [],
    builds: [],
    hostMetrics: [],
    errors: [],
  })
  const [loading, setLoading] = useState(false)

  const refresh = async () => {
    setLoading(true)
    const [
      appsResult,
      hostsResult,
      credsResult,
      builderEnvResult,
      pipelinesResult,
      buildsResult,
      hostMetricsResult,
    ] = await Promise.all([
      guarded(listApps()),
      guarded(listHosts()),
      guarded(listGitCreds()),
      guarded(getBuilderEnv()),
      guarded(listPipelines()),
      guarded(listBuilds()),
      guarded(listHostMetrics()),
    ])

    setSnapshot({
      apps: appsResult.data ?? [],
      hosts: hostsResult.data ?? [],
      creds: credsResult.data ?? [],
      builderEnv: builderEnvResult.data ?? null,
      pipelines: pipelinesResult.data ?? [],
      builds: buildsResult.data ?? [],
      hostMetrics: hostMetricsResult.data ?? [],
      errors: [
        appsResult.error,
        hostsResult.error,
        credsResult.error,
        builderEnvResult.error,
        pipelinesResult.error,
        buildsResult.error,
        hostMetricsResult.error,
      ].filter(Boolean) as string[],
    })
    setLoading(false)
  }

  useEffect(() => { refresh() }, [])

  const appById = useMemo(() => {
    return new Map(snapshot.apps.map((app) => [app.id, app]))
  }, [snapshot.apps])
  const hostMetricById = useMemo(() => {
    return new Map(snapshot.hostMetrics.map((metric) => [metric.host_id, metric]))
  }, [snapshot.hostMetrics])

  const effectiveHostStatus = (host: Host) => (hostMetricById.get(host.id)?.host_status || host.status) as Host['status']
  const onlineHosts = snapshot.hosts.filter((h) => effectiveHostStatus(h) === 'online')
  const offlineHosts = snapshot.hosts.filter((h) => effectiveHostStatus(h) === 'offline')
  const runningInstances = snapshot.hosts.reduce((sum, h) => sum + (h.running_instance_count || 0), 0)
  const gitReadyApps = snapshot.apps.filter((app) => !!app.git_url)
  const encryptedCreds = snapshot.creds.filter((cred) => cred.has_secret)
  const failedPipelines = snapshot.pipelines.filter((p) => p.status === 'failed')
  const activePipelines = snapshot.pipelines.filter((p) => p.status === 'pending' || p.status === 'running')
  const failedBuilds = snapshot.builds.filter((b) => b.status === 'failed')
  const activeBuilds = snapshot.builds.filter((b) => b.status === 'building')
  const builderReady = snapshot.builderEnv?.valid === true
  const healthyMetrics = snapshot.hostMetrics.filter((item) => !item.error)
  const metricErrors = snapshot.hostMetrics.filter((item) => !!item.error)
  const avgCPU = averageMetric(snapshot.hostMetrics, 'cpu_usage')
  const avgMemory = averageMetric(snapshot.hostMetrics, 'memory_usage')
  const avgDisk = averageMetric(snapshot.hostMetrics, 'disk_usage')

  const recentPipelines = [...snapshot.pipelines]
    .sort((a, b) => timeOf(b.created_at) - timeOf(a.created_at))
    .slice(0, 6)
  const recentBuilds = [...snapshot.builds]
    .sort((a, b) => timeOf(b.created_at) - timeOf(a.created_at))
    .slice(0, 6)

  return (
    <section className="page-shell">
      <PageHeader
        eyebrow="Overview"
        title="交付运行总览"
        description="把应用、主机、凭证、构建环境和最近构建/部署收敛到一个状态优先的工作台，优先看真实运行资源与交付趋势。"
        actions={(
          <>
            <Button type="primary" onClick={() => nav('/apps')}>进入应用</Button>
            <Button onClick={refresh} loading={loading}>刷新状态</Button>
          </>
        )}
        meta={(
          <>
            <StatusTag tone={builderReady ? 'success' : 'warning'}>
              {builderReady ? '构建环境就绪' : '构建环境待检测'}
            </StatusTag>
            <StatusTag tone={offlineHosts.length > 0 ? 'danger' : 'success'}>
              主机 {onlineHosts.length}/{snapshot.hosts.length} 在线
            </StatusTag>
            {(activePipelines.length + activeBuilds.length) > 0 && (
              <StatusTag tone="processing">
                {activePipelines.length + activeBuilds.length} 个任务进行中
              </StatusTag>
            )}
          </>
        )}
      />

      {snapshot.errors.length > 0 && (
        <Alert
          type="warning"
          showIcon
          message="部分总览数据读取失败"
          description={snapshot.errors.slice(0, 3).join('；')}
        />
      )}

      <StatGrid>
        <StatCard
          label="Applications"
          value={snapshot.apps.length}
          description={`${gitReadyApps.length} 个已配置 Git 仓库`}
          tone={snapshot.apps.length > 0 ? 'info' : 'warning'}
        />
        <StatCard
          label="Hosts"
          value={`${onlineHosts.length}/${snapshot.hosts.length}`}
          description={avgCPU == null
            ? `${runningInstances} 个远端运行实例；${offlineHosts.length} 台离线`
            : `平均 CPU ${formatPercent(avgCPU)} / 内存 ${formatPercent(avgMemory ?? undefined)} / 磁盘 ${formatPercent(avgDisk ?? undefined)}`}
          tone={metricErrors.length > 0 || offlineHosts.length > 0 ? 'danger' : 'success'}
        />
        <StatCard
          label="Builder"
          value={builderReady ? 'Ready' : 'Pending'}
          description={snapshot.builderEnv?.detected_at ? `最近检测：${formatTime(snapshot.builderEnv.detected_at)}` : '尚未完成检测'}
          tone={builderReady ? 'success' : 'warning'}
        />
        <StatCard
          label="Recent Failures"
          value={failedPipelines.length + failedBuilds.length}
          description={`${failedPipelines.length} 部署失败 / ${failedBuilds.length} 构建失败`}
          tone={(failedPipelines.length + failedBuilds.length) > 0 ? 'danger' : 'success'}
        />
      </StatGrid>

      <div className="overview-grid">
        <div className="overview-stack">
          <Card
            className="surface-card"
            title="主机性能监控"
            loading={loading && snapshot.hostMetrics.length === 0}
            extra={snapshot.hostMetrics.some((item) => item.cached) && (
              <StatusTag tone="info">缓存快照</StatusTag>
            )}
          >
            {snapshot.hostMetrics.length > 0 ? (
              <>
                <div className="host-metrics-summary">
                  <div className="host-metrics-summary-item">
                    <span>可用快照</span>
                    <strong>{healthyMetrics.length}/{snapshot.hostMetrics.length}</strong>
                  </div>
                  <div className="host-metrics-summary-item">
                    <span>平均 CPU</span>
                    <strong>{formatPercent(avgCPU ?? undefined)}</strong>
                  </div>
                  <div className="host-metrics-summary-item">
                    <span>平均内存</span>
                    <strong>{formatPercent(avgMemory ?? undefined)}</strong>
                  </div>
                  <div className="host-metrics-summary-item">
                    <span>平均磁盘</span>
                    <strong>{formatPercent(avgDisk ?? undefined)}</strong>
                  </div>
                </div>
                <Table<HostMetrics>
                  rowKey="host_id"
                  size="small"
                  loading={loading}
                  dataSource={snapshot.hostMetrics}
                  pagination={false}
                  scroll={{ x: 820 }}
                  columns={[
                    {
                      title: '主机',
                      dataIndex: 'host_name',
                      width: 180,
                      render: (_, row) => (
                        <div className="host-metric-host">
                          <strong>{row.host_name || row.host_ip || `Host #${row.host_id}`}</strong>
                          <span>{row.host_ip || '—'}</span>
                        </div>
                      ),
                    },
                    {
                      title: '状态',
                      width: 112,
                      render: (_, row) => row.error
                        ? <StatusTag tone="danger">采样失败</StatusTag>
                        : <StatusTag tone={statusToneFromHost(row.host_status as Host['status'])}>{hostStatusText(row.host_status as Host['status'])}</StatusTag>,
                    },
                    {
                      title: 'CPU',
                      dataIndex: 'cpu_usage',
                      width: 138,
                      render: (value: number) => <MetricCell value={value} />,
                    },
                    {
                      title: '内存',
                      dataIndex: 'memory_usage',
                      width: 138,
                      render: (value: number) => <MetricCell value={value} />,
                    },
                    {
                      title: '磁盘',
                      dataIndex: 'disk_usage',
                      width: 138,
                      render: (value: number) => <MetricCell value={value} />,
                    },
                    {
                      title: 'Load',
                      dataIndex: 'load1',
                      width: 88,
                      render: (value: number) => (Number.isFinite(value) ? value.toFixed(2) : '—'),
                    },
                    {
                      title: '快照',
                      dataIndex: 'checked_at',
                      width: 188,
                      render: (value: string, row) => (
                        <div className="host-metric-snapshot">
                          <span>{formatTime(value)}</span>
                          {row.cached && <em>cached</em>}
                        </div>
                      ),
                    },
                    {
                      title: '说明',
                      dataIndex: 'error',
                      ellipsis: true,
                      render: (value?: string) => value
                        ? <Typography.Text type="danger">{compactText(value, 80)}</Typography.Text>
                        : <Typography.Text type="secondary">—</Typography.Text>,
                    },
                  ]}
                />
              </>
            ) : (
              <EmptyState title="暂无主机性能快照" description="刷新后会通过 SSH 采样主机 CPU、内存、磁盘与 Load，并在短时间内复用缓存快照。" />
            )}
          </Card>

          <Card className="surface-card" title="最近部署">
            <Table<PipelineRun>
              rowKey="id"
              size="small"
              loading={loading}
              dataSource={recentPipelines}
              pagination={false}
              scroll={{ x: 680 }}
              locale={{
                emptyText: <EmptyState title="暂无部署历史" description="应用详情页触发部署后，这里会显示最近的部署与回滚记录。" />,
              }}
              columns={[
                {
                  title: '应用',
                  dataIndex: 'app_id',
                  render: (appId: number) => {
                    const app = appById.get(appId)
                    return app
                      ? <Link to={`/apps/${appId}`}>{app.name || app.app_code}</Link>
                      : <Typography.Text type="secondary">App #{appId}</Typography.Text>
                  },
                },
                { title: '策略', dataIndex: 'strategy', width: 92 },
                {
                  title: '状态',
                  dataIndex: 'status',
                  width: 110,
                  render: (status: PipelineRun['status']) => (
                    <StatusTag tone={statusToneFromPipeline(status)}>{pipelineStatusText(status)}</StatusTag>
                  ),
                },
                {
                  title: '当前',
                  width: 96,
                  render: (_, row) => row.is_current
                    ? <StatusTag tone={row.current_partial ? 'warning' : 'success'}>{row.current_partial ? '部分当前' : '当前版本'}</StatusTag>
                    : <Typography.Text type="secondary">—</Typography.Text>,
                },
                { title: '创建时间', dataIndex: 'created_at', width: 170, render: formatTime },
              ]}
            />
          </Card>
        </div>

        <div className="overview-stack">
          <Card className="surface-card" title="最近构建">
            <Table<BuildRun>
              rowKey="id"
              size="small"
              loading={loading}
              dataSource={recentBuilds}
              pagination={false}
              scroll={{ x: 720 }}
              locale={{
                emptyText: <EmptyState title="暂无构建记录" description="先在应用详情中扫描服务并触发构建。" />,
              }}
              columns={[
                {
                  title: '应用',
                  dataIndex: 'app_id',
                  render: (appId: number) => {
                    const app = appById.get(appId)
                    return app
                      ? <Link to={`/apps/${appId}`}>{app.name || app.app_code}</Link>
                      : <Typography.Text type="secondary">App #{appId}</Typography.Text>
                  },
                },
                {
                  title: 'Git Ref',
                  dataIndex: 'git_ref',
                  ellipsis: true,
                  render: (value: string) => value ? <code>{value}</code> : '—',
                },
                {
                  title: '状态',
                  dataIndex: 'status',
                  width: 110,
                  render: (status: BuildRun['status']) => (
                    <StatusTag tone={statusToneFromBuild(status)}>{buildStatusText(status)}</StatusTag>
                  ),
                },
                {
                  title: '制品',
                  width: 98,
                  render: (_, row) => row.bundle_id
                    ? <StatusTag tone="info">Bundle #{row.bundle_id}</StatusTag>
                    : <Typography.Text type="secondary">—</Typography.Text>,
                },
                { title: '创建时间', dataIndex: 'created_at', width: 170, render: formatTime },
              ]}
            />
          </Card>

          <Card className="surface-card" title="环境概况">
            {loading && snapshot.apps.length === 0 ? (
              <Skeleton active paragraph={{ rows: 5 }} />
            ) : (
              <div className="action-list">
                <ActionItem
                  tone={builderReady ? 'success' : 'warning'}
                  label="构建"
                  title={builderReady ? '构建环境可用' : '构建环境需要检测'}
                  description={snapshot.builderEnv?.detect_message
                    ? compactText(snapshot.builderEnv.detect_message)
                    : '检测结果会影响所有从远端 Git 仓库拉取代码的 Maven 构建。'}
                  to="/settings/build-env"
                />
                <ActionItem
                  tone={offlineHosts.length > 0 ? 'danger' : 'success'}
                  label="主机"
                  title={`${onlineHosts.length} 台在线 / ${snapshot.hosts.length} 台总计`}
                  description="主机在线只代表 SSH 可达，运行态仍需要针对应用部署绑定做检测。"
                  to="/hosts"
                />
                <ActionItem
                  tone={encryptedCreds.length === snapshot.creds.length ? 'success' : 'warning'}
                  label="凭证"
                  title={`${encryptedCreds.length} 个 Secret 完整 / ${snapshot.creds.length} 个凭证`}
                  description="凭证只用于拉取远端 Git 仓库；页面不会回显 Secret 原文。"
                  to="/credentials"
                />
              </div>
            )}
          </Card>
        </div>
      </div>
    </section>
  )
}
