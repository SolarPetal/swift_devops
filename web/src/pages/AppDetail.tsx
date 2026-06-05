import { useEffect, useMemo, useRef, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import {
  Typography, Card, Button, Space, Table, Tag, Modal,
  Form, Select, InputNumber, Input, message, Descriptions, Skeleton, Tabs, Radio,
  Drawer, Steps, Badge, Upload, Progress, Tooltip, Switch,
} from 'antd'

import type {
  App, Deployment, Host, Artifact, PipelineRun, RunSnapshot, StepResult, PipelineStage,
  BuildRun, GitCredential, BuilderEnv,
  AppService, AppServiceInput, ArtifactBundle, DockerfileTemplate, DockerfileTemplateInput, AppInput,
  DeploymentRuntimeAction, DeploymentRuntimeLogs, AppServiceSuggestion,
  FrontendAppConfig, FrontendDeployInput, FrontendDeployResult, FrontendGateway,
  FrontendGatewayPreview, FrontendGatewayRoute, FrontendPackageManager,
  FrontendTemplatePreview,
} from '../types'
import { getApp, updateApp } from '../api/app'
import { listHosts } from '../api/host'
import {
  bindHost, checkDeploymentRuntime, listDeployments, unbindDeployment, getDeploymentRuntimeLogs,
  restartDeploymentRuntime, stopDeploymentRuntime,
} from '../api/deployment'
import { createArtifact, deleteArtifact, listArtifacts, uploadArtifact, listBundles, cleanupBundleHistory } from '../api/artifact'
import { cancelPipeline, deployApp, getPipeline, listPipelines, rollbackPipelineRun } from '../api/pipeline'
import { getBuild, getBuildLog, listBuilds, triggerBuild } from '../api/build'
import { listGitCreds } from '../api/gitcred'
import { getBuilderEnv } from '../api/builderEnv'
import {
  batchImportAppServices,
  createAppService,
  deleteAppService,
  discoverAppServices,
  listAppServices,
  updateAppService,
} from '../api/appService'
import { listDockerfileTemplates, createDockerfileTemplate, updateDockerfileTemplate, deleteDockerfileTemplate, ensureDefaultDockerfileTemplate } from '../api/dockerfile'
import {
  applyFrontendGateway,
  deployFrontendApp,
  ensureFrontendGateway,
  getFrontendConfig,
  getFrontendGateway,
  listFrontendGatewayRoutes,
  previewFrontendConfig,
  previewFrontendGatewayRoute,
  saveFrontendConfig,
} from '../api/frontend'
import { buildWSURL, issueWSTicket, type PipelineWSEvent, type BuildWSEvent } from '../api/ws'
import { formatError } from '../api/client'
import { EmptyState, PageHeader, StatCard, StatGrid } from '../components/PageFrame'
import { LogTerminal } from '../components/LogTerminal'
import {
  StatusTag,
  type StatusTone,
  statusToneFromBuild,
  statusToneFromPipeline,
} from '../components/StatusTag'

// 状态色
const hostStatusTag = (s: string) => {
  if (s === 'online') return <StatusTag tone="success">在线</StatusTag>
  if (s === 'offline') return <StatusTag tone="danger">离线</StatusTag>
  return <StatusTag>未知</StatusTag>
}
const pipeStatusTag = (s: string) => {
  if (s === 'success') return <StatusTag tone="success">成功</StatusTag>
  if (s === 'failed') return <StatusTag tone="danger">失败</StatusTag>
  if (s === 'running') return <StatusTag tone="processing">进行中</StatusTag>
  if (s === 'pending') return <StatusTag tone="warning">等待中</StatusTag>
  if (s === 'cancelled') return <StatusTag>已取消</StatusTag>
  return <StatusTag>{s}</StatusTag>
}
const deployHostStatusTag = (s?: string) => {
  if (s === 'success') return <StatusTag tone="success">成功</StatusTag>
  if (s === 'failed') return <StatusTag tone="danger">失败</StatusTag>
  if (s === 'running') return <StatusTag tone="processing">部署中</StatusTag>
  if (s === 'skipped') return <StatusTag tone="warning">跳过</StatusTag>
  if (s === 'pending') return <StatusTag tone="warning">等待中</StatusTag>
  return <Typography.Text type="secondary">暂无部署记录</Typography.Text>
}
const runtimeStatusTag = (s?: string) => {
  if (s === 'running') return <StatusTag tone="success">运行中</StatusTag>
  if (s === 'failed') return <StatusTag tone="danger">异常</StatusTag>
  if (s === 'stopped') return <StatusTag tone="danger">已停止</StatusTag>
  if (s === 'pending') return <StatusTag tone="warning">待部署</StatusTag>
  return <StatusTag>{s || '-'}</StatusTag>
}

const isDeploymentRunning = (d: Deployment) => d.status === 'running'

const buildStatusTag = (s: string) => {
  if (s === 'success') return <StatusTag tone="success">成功</StatusTag>
  if (s === 'failed') return <StatusTag tone="danger">失败</StatusTag>
  if (s === 'building') return <StatusTag tone="processing">构建中</StatusTag>
  if (s === 'cancelled') return <StatusTag>已取消</StatusTag>
  return <StatusTag>{s}</StatusTag>
}

const deploymentArtifactLabel = (d: Deployment) => {
  if (d.current_bundle_id || d.current_artifact_item_id) {
    return (
      <Space direction="vertical" size={0}>
        <Space size={4}>
          {d.current_bundle_id > 0 && <StatusTag tone="info">Bundle #{d.current_bundle_id}</StatusTag>}
          {d.current_bundle_version && <Typography.Text code>{d.current_bundle_version}</Typography.Text>}
        </Space>
        {d.current_artifact_item_id > 0 && (
          <Typography.Text type="secondary">Item #{d.current_artifact_item_id}</Typography.Text>
        )}
      </Space>
    )
  }
  if (d.current_artifact_id) return <StatusTag>Artifact #{d.current_artifact_id}</StatusTag>
  return <Typography.Text type="secondary">-</Typography.Text>
}

const lastDeployStatusView = (d: Deployment) => {
  const runtimeDetail = d.runtime_check_error || d.runtime_status_detail
  const content = (
    <Space direction="vertical" size={0}>
      <Space size={4}>
        {deployHostStatusTag(d.last_deploy_status)}
        {d.last_run_id > 0 && <Typography.Text type="secondary">Run #{d.last_run_id}</Typography.Text>}
      </Space>
      <Typography.Text type="secondary">
        运行态：{runtimeStatusTag(d.status)}
        {d.last_deploy_stage ? <> · {d.last_deploy_stage}</> : null}
        {d.runtime_check_error ? <> · 检测失败</> : null}
      </Typography.Text>
      {d.runtime_container_name && (
        <Typography.Text type="secondary" ellipsis style={{ maxWidth: 170 }}>
          容器：{d.runtime_container_name}
        </Typography.Text>
      )}
    </Space>
  )
  const tips = [d.last_deploy_error, runtimeDetail].filter(Boolean).join('\n\n')
  if (!tips) return content
  return <Tooltip title={<pre style={{ margin: 0, whiteSpace: 'pre-wrap' }}>{tips}</pre>}>{content}</Tooltip>
}

const appBuildRefs = (app: App) => {
  const out: string[] = []
  const add = (ref?: string) => {
    const v = (ref || '').trim()
    if (!v || out.includes(v)) return
    out.push(v)
  }
  add(app.git_ref || 'main')
  ;(app.git_refs || []).forEach(add)
  if (out.length === 0) out.push('main')
  return out
}

const appToInput = (app: App): AppInput => {
  const { id, created_at, updated_at, ...input } = app
  return input
}

const describeServiceMode = (services: AppService[]) => {
  const enabled = services.filter((s) => s.enabled)
  if (services.length === 0) {
    return {
      label: '未配置服务',
      color: 'default',
      desc: '还没有配置 service；请在「服务」里扫描 Maven 模块或手动新增。',
    }
  }
  if (enabled.length === 0) {
    return {
      label: '未启用服务',
      color: 'red',
      desc: '当前没有可部署 service，请先在「服务」启用至少一个 service。',
    }
  }
  if (enabled.length === 1) {
    return {
      label: '单服务模式',
      color: 'green',
      desc: `当前可部署服务：${enabled[0].service_code || 'default'}。`,
    }
  }
  return {
    label: '多服务模式',
    color: 'blue',
    desc: `当前启用 ${enabled.length} 个可部署 service，构建和部署会按 service 编排。`,
  }
}

const serviceModeTone = (color: string): StatusTone => {
  if (color === 'red') return 'danger'
  if (color === 'green') return 'success'
  if (color === 'blue') return 'info'
  return 'neutral'
}

const formatDateTime = (value?: string) => {
  if (!value) return '-'
  const time = new Date(value)
  return Number.isNaN(time.getTime()) ? value : time.toLocaleString()
}

const timeValue = (value?: string) => {
  if (!value) return 0
  const time = new Date(value).getTime()
  return Number.isFinite(time) ? time : 0
}

type StatCardTone = 'default' | 'success' | 'warning' | 'danger' | 'info'

const tagTone = (tone: StatCardTone): StatusTone => tone === 'default' ? 'neutral' : tone

const serviceCode = (s: AppService) => s.service_code || 'default'

const enabledServicesOf = (services: AppService[]) => services.filter((s) => s.enabled)

const effectiveServicePort = (app: App, service: AppService) => service.port || app.port

const servicePortRows = (app: App, services: AppService[]) => {
  const enabled = enabledServicesOf(services)
  if (enabled.length === 0) {
    return app.port ? [{ code: '应用默认', port: app.port }] : []
  }
  return enabled.map((service) => ({
    code: serviceCode(service),
    port: effectiveServicePort(app, service),
  }))
}

const duplicatePortGroups = (rows: Array<{ code: string; port: number }>) => {
  const groups = new Map<number, string[]>()
  rows.forEach((row) => {
    if (!row.port) return
    groups.set(row.port, [...(groups.get(row.port) ?? []), row.code])
  })
  return Array.from(groups.entries()).filter(([, codes]) => codes.length > 1)
}

const compactPairs = (rows: Array<{ code: string; port: number }>, max = 3) => {
  const pairs = rows
    .filter((row) => row.port)
    .slice(0, max)
    .map((row) => `${row.code}:${row.port}`)
  const more = rows.length > max ? ` 等 ${rows.length} 个 service` : ''
  return pairs.length > 0 ? `${pairs.join(' / ')}${more}` : '暂无端口'
}

const summarizeServiceCard = (services: AppService[]) => {
  const enabled = enabledServicesOf(services)
  const mode = describeServiceMode(services)
  return {
    label: 'Services',
    value: services.length === 0 ? '初始化中' : `${enabled.length}/${services.length}`,
    description: mode.desc,
    tone: serviceModeTone(mode.color) as StatCardTone,
    meta: mode.label,
  }
}

const summarizePortCard = (app: App, services: AppService[]) => {
  const enabled = enabledServicesOf(services)
  const rows = servicePortRows(app, services)
  const uniquePorts = new Set(rows.map((row) => row.port).filter(Boolean))
  const duplicated = duplicatePortGroups(rows)

  if (services.length === 0) {
    return {
      label: 'Ports',
      value: app.port || '-',
      description: '这是应用默认端口；service 加载后以 service 端口为准。',
      tone: 'warning' as StatCardTone,
      meta: app.port ? `默认端口 :${app.port}` : '端口未配置',
    }
  }

  if (enabled.length === 0) {
    return {
      label: 'Ports',
      value: '-',
      description: '没有启用的 service，无法判断实际监听端口。',
      tone: 'danger' as StatCardTone,
      meta: '无启用端口',
    }
  }

  if (enabled.length === 1) {
    const row = rows[0]
    return {
      label: 'Service Port',
      value: row.port || '-',
      description: `${row.code} 的实际监听端口；不是全应用唯一端口。`,
      tone: row.port ? 'info' as StatCardTone : 'warning' as StatCardTone,
      meta: row.port ? `${row.code}:${row.port}` : `${row.code}:未配置`,
    }
  }

  if (duplicated.length > 0) {
    const conflictText = duplicated
      .map(([port, codes]) => `${port}(${codes.join(', ')})`)
      .join(' / ')
    return {
      label: 'Service Ports',
      value: `${uniquePorts.size} 个端口`,
      description: `注意端口复用/冲突：${conflictText}`,
      tone: 'warning' as StatCardTone,
      meta: `端口 ${uniquePorts.size} 个`,
    }
  }

  return {
    label: 'Service Ports',
    value: `${uniquePorts.size} 个端口`,
    description: compactPairs(rows),
    tone: 'info' as StatCardTone,
    meta: `端口 ${uniquePorts.size} 个`,
  }
}

const summarizeBuildSourceCard = (app: App) => {
  const refs = appBuildRefs(app)
  return {
    label: 'Build Source',
    value: <code>{app.git_ref || 'main'}</code>,
    description: app.git_url ? `Git 已配置；${refs.length} 个可选 Ref。` : 'Git 仓库未配置，无法从仓库扫描和构建。',
    tone: app.git_url ? 'success' as StatCardTone : 'warning' as StatCardTone,
    meta: app.git_url ? 'Git Ready' : 'Git 未配置',
  }
}

const summarizeRuntimePlanCard = (app: App, services: AppService[]) => {
  const enabled = enabledServicesOf(services)
  const modes = new Set(enabled.map((service) => service.deploy_mode || app.deploy_mode || 'systemd'))
  const dockerEnabled = app.build_mode === 'local-docker' || app.build_mode === 'remote-docker' || app.deploy_mode === 'docker'
  const dockerImage = [app.docker_registry, app.docker_image_name || `${app.app_code}/{{SERVICE_CODE}}`].filter(Boolean).join('/') || `${app.app_code}/{{SERVICE_CODE}}`
  const value = modes.size > 1 ? `${modes.size} 种模式` : (Array.from(modes)[0] || app.deploy_mode || 'systemd')
  return {
    label: 'Runtime Plan',
    value,
    description: dockerEnabled
      ? `Docker 镜像模板：${dockerImage}:${app.docker_image_tag || 'latest'}`
      : enabled.length > 1
        ? `按 service 编排部署；默认路径 ${app.deploy_path}`
        : app.deploy_path,
    tone: dockerEnabled ? 'warning' as StatCardTone : 'default' as StatCardTone,
    meta: value,
  }
}

const renderServicePortMatrix = (app: App, services: AppService[]) => {
  const rows = servicePortRows(app, services)
  return (
    <div className="service-chip-list">
      {rows.map((row) => (
        <span className="service-chip" key={`${row.code}-${row.port}`}>
          <span className="service-chip-name">{row.code}</span>
          <code>:{row.port || '-'}</code>
        </span>
      ))}
    </div>
  )
}

const renderServiceHealthMatrix = (app: App, services: AppService[]) => {
  const enabled = enabledServicesOf(services)
  if (enabled.length === 0) return <code>{app.health_check_url || '-'}</code>
  return (
    <div className="service-chip-list">
      {enabled.map((service) => (
        <span className="service-chip service-chip--wide" key={serviceCode(service)}>
          <span className="service-chip-name">{serviceCode(service)}</span>
          <code>{service.health_check_url || app.health_check_url || '-'}</code>
        </span>
      ))}
    </div>
  )
}

type DockerfileTemplateFormValues = DockerfileTemplateInput & {
  df_base_image?: string
  df_workdir?: string
  df_jar_dest?: string
  df_expose_port?: string
  df_entrypoint?: string
  df_extra_instructions?: string
}

const defaultDockerfileFields = {
  df_base_image: 'eclipse-temurin:21-jdk',
  df_workdir: '/app',
  df_jar_dest: '/app/app.jar',
  df_expose_port: '{{PORT}}',
  df_entrypoint: '["java", "-jar", "/app/app.jar"]',
  df_extra_instructions: '',
}

const buildDockerfileFromFields = (v: Partial<DockerfileTemplateFormValues>) => {
  const baseImage = (v.df_base_image || defaultDockerfileFields.df_base_image).trim()
  const workdir = (v.df_workdir || defaultDockerfileFields.df_workdir).trim()
  const jarDest = (v.df_jar_dest || defaultDockerfileFields.df_jar_dest).trim()
  const exposePort = (v.df_expose_port || defaultDockerfileFields.df_expose_port).trim()
  const entrypoint = (v.df_entrypoint || defaultDockerfileFields.df_entrypoint).trim()
  const extra = (v.df_extra_instructions || '').trim()

  const lines = [
    `FROM ${baseImage}`,
    '',
    `WORKDIR ${workdir}`,
    '',
    `COPY {{JAR_FILE}} ${jarDest}`,
  ]
  if (exposePort) {
    lines.push('', `EXPOSE ${exposePort}`)
  }
  if (extra) {
    lines.push('', extra)
  }
  if (entrypoint) {
    lines.push('', `ENTRYPOINT ${entrypoint}`)
  }
  return `${lines.join('\n')}\n`
}

const parseDockerfileFields = (content: string): Partial<DockerfileTemplateFormValues> => {
  const pick = (re: RegExp) => content.match(re)?.[1]?.trim()
  return {
    ...defaultDockerfileFields,
    df_base_image: pick(/^FROM\s+(.+)$/im) || defaultDockerfileFields.df_base_image,
    df_workdir: pick(/^WORKDIR\s+(.+)$/im) || defaultDockerfileFields.df_workdir,
    df_jar_dest: pick(/^COPY\s+\{\{JAR_FILE\}\}\s+(.+)$/im) || defaultDockerfileFields.df_jar_dest,
    df_expose_port: pick(/^EXPOSE\s+(.+)$/im) || defaultDockerfileFields.df_expose_port,
    df_entrypoint: pick(/^ENTRYPOINT\s+(.+)$/im) || defaultDockerfileFields.df_entrypoint,
  }
}

// ---------- 主页 ----------

export default function AppDetail() {
  const { id } = useParams<{ id: string }>()
  const nav = useNavigate()
  const appId = Number(id)
  const [app, setApp] = useState<App | null>(null)
  const [appServices, setAppServices] = useState<AppService[]>([])
  const [activeTab, setActiveTab] = useState('overview')
  const [loadingApp, setLoadingApp] = useState(false)
  const [runtimeOpen, setRuntimeOpen] = useState(false)
  const [runtimeForm] = Form.useForm<AppInput>()
  const runtimeDeployMode = Form.useWatch('deploy_mode', runtimeForm)

  const refresh = async () => {
    setLoadingApp(true)
    try {
      const nextApp = await getApp(appId)
      setApp(nextApp)
      try {
        const svcRows = await listAppServices(appId)
        setAppServices(svcRows)
      } catch (e) {
        setAppServices([])
        message.warning(`服务模式读取失败：${formatError(e)}`)
      }
    } catch (e) {
      message.error(formatError(e))
    } finally {
      setLoadingApp(false)
    }
  }
  useEffect(() => { refresh() }, [appId])

  if (loadingApp && !app) return <Skeleton active />
  if (!app) return <Typography.Text type="danger">应用不存在</Typography.Text>
  const isFrontendApp = app.app_type === 'frontend'
  const dockerEnabled = isFrontendApp || app.build_mode === 'local-docker' || app.build_mode === 'remote-docker' || app.deploy_mode === 'docker'
  const serviceSummary = isFrontendApp
    ? {
        label: 'Frontend',
        value: 'SPA',
        description: '源码拉取后在目标机 docker build，静态资源容器通过统一 gateway container 暴露域名。',
        tone: 'info' as StatCardTone,
        meta: 'Frontend',
      }
    : summarizeServiceCard(appServices)
  const portSummary = isFrontendApp
    ? {
        label: 'Gateway',
        value: '80 / 443',
        description: '宿主机只暴露网关容器端口；每个前端项目容器不直接占用宿主机端口。',
        tone: 'success' as StatCardTone,
        meta: 'Gateway 模式',
      }
    : summarizePortCard(app, appServices)
  const buildSourceSummary = summarizeBuildSourceCard(app)
  const runtimePlanSummary = isFrontendApp
    ? {
        label: 'Runtime Plan',
        value: 'Docker + Nginx',
        description: '每个前端项目一个静态资源镜像；统一网关容器按 domain 转发。',
        tone: 'info' as StatCardTone,
        meta: 'Gateway Container',
      }
    : summarizeRuntimePlanCard(app, appServices)

  const openRuntimeEdit = async () => {
    runtimeForm.resetFields()
    runtimeForm.setFieldsValue({
      build_mode: app.build_mode || 'local-jar',
      deploy_mode: app.deploy_mode || 'systemd',
      jvm_args: app.jvm_args || '',
      env_vars: app.env_vars || '',
      systemd_user: app.systemd_user || '',
      java_path: app.java_path || '',
      docker_registry: app.docker_registry || '',
      docker_image_name: app.docker_image_name || '',
      docker_image_tag: app.docker_image_tag || 'latest',
      docker_build_args: app.docker_build_args || '',
      docker_container_name: app.docker_container_name || '',
      docker_run_args: app.docker_run_args || '',
    })
    setRuntimeOpen(true)
  }

  const saveRuntimeConfig = async () => {
    try {
      const v = await runtimeForm.validateFields()
      await updateApp(app.id, {
        ...appToInput(app),
        ...v,
        build_mode: v.build_mode || 'local-jar',
        deploy_mode: v.deploy_mode || 'systemd',
      })
      message.success('运行配置已更新')
      setRuntimeOpen(false)
      await refresh()
    } catch (e: any) {
      if (e?.errorFields) return
      message.error(formatError(e))
    }
  }

  return (
    <section className="page-shell app-detail-page">
      <PageHeader
        eyebrow="Application Detail"
        title={app.name}
        description={(
          <>
            应用代号 <code>{app.app_code}</code>
            {isFrontendApp
              ? <> · 前端 SPA 源码部署，按 Docker 静态资源容器 + gateway container 暴露域名。</>
              : <> · Git 仓库 / Maven Service 交付单元。这里按概览、服务、运行、构建、部署组织主路径。</>}
          </>
        )}
        actions={(
          <>
            <Button onClick={() => nav('/apps')}>返回列表</Button>
            {isFrontendApp
              ? <Button type="primary" onClick={() => setActiveTab('deploy')}>进入部署</Button>
              : <Button type="primary" onClick={openRuntimeEdit}>编辑运行配置</Button>}
          </>
        )}
        meta={(
          <>
            <StatusTag tone={tagTone(serviceSummary.tone)}>{serviceSummary.meta}</StatusTag>
            <StatusTag tone={tagTone(portSummary.tone)}>{portSummary.meta}</StatusTag>
            <StatusTag tone={dockerEnabled ? 'info' : 'neutral'}>{runtimePlanSummary.meta}</StatusTag>
          </>
        )}
      />

      <StatGrid>
        <StatCard label={serviceSummary.label} value={serviceSummary.value} description={serviceSummary.description} tone={serviceSummary.tone} />
        <StatCard label={portSummary.label} value={portSummary.value} description={portSummary.description} tone={portSummary.tone} />
        <StatCard label={buildSourceSummary.label} value={buildSourceSummary.value} description={buildSourceSummary.description} tone={buildSourceSummary.tone} />
        <StatCard label={runtimePlanSummary.label} value={runtimePlanSummary.value} description={runtimePlanSummary.description} tone={runtimePlanSummary.tone} />
      </StatGrid>

      <Modal
        title={`运行与发布配置 - ${app.name}`}
        open={runtimeOpen}
        onOk={saveRuntimeConfig}
        onCancel={() => setRuntimeOpen(false)}
        okText="保存"
        cancelText="取消"
        width={720}
        maskClosable={false}
        keyboard={false}
      >
        <Form form={runtimeForm} layout="vertical" className="runtime-config-form">
          <Form.Item
            name="build_mode"
            label="构建方式"
            rules={[{ required: true }]}
            tooltip="本地 jar：Maven 打包 jar；本地 Docker：本机 Maven 打 jar + docker build/push；远端 Docker：本机 Maven 打 jar，部署时上传 jar+Dockerfile 到目标机 docker build/run"
          >
            <Radio.Group
              onChange={(e: any) => {
                const next = e.target.value
                if ((next === 'local-docker' || next === 'remote-docker') && runtimeForm.getFieldValue('deploy_mode') !== 'docker') {
                  runtimeForm.setFieldsValue({ deploy_mode: 'docker' })
                }
              }}
            >
              <Radio.Button value="local-jar">本地 jar 构建</Radio.Button>
              <Radio.Button value="local-docker">本地 Docker 镜像构建</Radio.Button>
              <Radio.Button value="remote-docker">远端 Docker 部署构建</Radio.Button>
            </Radio.Group>
          </Form.Item>
          <Form.Item
            name="deploy_mode"
            label="部署方式"
            rules={[{ required: true }]}
          >
            <Radio.Group>
              <Radio.Button value="systemd">systemd</Radio.Button>
              <Radio.Button value="nohup">nohup</Radio.Button>
              <Radio.Button value="docker">Docker</Radio.Button>
            </Radio.Group>
          </Form.Item>
          {runtimeDeployMode !== 'docker' && (
            <>
              <Form.Item name="jvm_args" label="JVM 启动参数">
                <Input placeholder="-Xms512m -Xmx512m -XX:+UseG1GC" />
              </Form.Item>
              <Space style={{ display: 'flex' }} align="start">
                <Form.Item
                  name="systemd_user"
                  label="systemd 运行用户"
                  style={{ flex: 1 }}
                  rules={[{
                    pattern: /^([a-z_][a-z0-9_-]{0,31})?$/,
                    message: '小写字母/下划线开头，1-32 位，仅含小写字母/数字/下划线/连字符',
                  }]}
                >
                  <Input placeholder="留空 = 用 SSH 账号" />
                </Form.Item>
                <Form.Item
                  name="java_path"
                  label="Java 路径覆盖"
                  style={{ flex: 1 }}
                  rules={[{ pattern: /^(\/.*)?$/, message: '必须以 / 开头，或留空走主机默认' }]}
                >
                  <Input placeholder="留空沿用主机配置" />
                </Form.Item>
              </Space>
            </>
          )}
          {runtimeDeployMode === 'docker' && (
            <>
              <Typography.Title level={5}>Docker 配置</Typography.Title>
              <Typography.Paragraph type="secondary" style={{ marginTop: -4 }}>
                镜像名称不填时回退 <code>{app.app_code}/default</code>；容器名称不填时回退 <code>devops-{app.app_code}</code>。
                名称模板支持 <code>{'{{APP_CODE}}'}</code> / <code>{'{{SERVICE_CODE}}'}</code> / <code>{'{app}'}</code> / <code>{'{service}'}</code>。
              </Typography.Paragraph>
              <Space style={{ display: 'flex' }} align="start">
                <Form.Item
                  name="docker_registry"
                  label="镜像仓库"
                  style={{ flex: 1 }}
                  rules={[{ pattern: /^\S*$/, message: '不能包含空白字符' }]}
                >
                  <Input placeholder="如 registry.example.com；留空则不加 registry" />
                </Form.Item>
                <Form.Item
                  name="docker_image_name"
                  label="镜像名称"
                  style={{ flex: 1 }}
                  rules={[{ pattern: /^\S*$/, message: '不能包含空白字符' }]}
                  tooltip="支持 {{APP_CODE}} / {{SERVICE_CODE}}，例如 team/{{APP_CODE}}-{{SERVICE_CODE}}"
                >
                  <Input placeholder={`${app.app_code}/default`} />
                </Form.Item>
              </Space>
              <Space style={{ display: 'flex' }} align="start">
                <Form.Item
                  name="docker_image_tag"
                  label="镜像标签"
                  style={{ flex: 1 }}
                  rules={[{ pattern: /^\S*$/, message: '不能包含空白字符' }]}
                  tooltip="支持 {sha} / {build_id} / {{GIT_SHORT_SHA}} / {{BUILD_ID}}"
                >
                  <Input placeholder="latest / git-{sha}-{build_id}" />
                </Form.Item>
                <Form.Item
                  name="docker_container_name"
                  label="容器名称"
                  style={{ flex: 1 }}
                  rules={[{
                    pattern: /^([A-Za-z0-9_.{}-]{1,160})?$/,
                    message: '仅允许字母/数字/点/下划线/连字符和模板花括号',
                  }]}
                  tooltip="不要写到 docker run 参数里；这里会生成 docker run --name"
                >
                  <Input placeholder={`devops-${app.app_code}`} />
                </Form.Item>
              </Space>
              <Form.Item
                name="docker_build_args"
                label="docker build 参数"
                tooltip="如 --build-arg ENV=prod；remote-docker 会在目标机 build 时使用"
              >
                <Input placeholder="--build-arg ENV=prod" />
              </Form.Item>
              <Form.Item
                name="docker_run_args"
                label="docker run 参数"
                tooltip="如 -p 8080:8080 -e ENV=prod --restart=unless-stopped，留空则自动生成。容器名请填上面的“容器名称”。"
              >
                <Input.TextArea rows={3} placeholder="留空自动生成 -d --restart=unless-stopped -p <port>:<port>" />
              </Form.Item>
            </>
          )}
          <Form.Item name="env_vars" label="环境变量 (JSON)">
            <Input.TextArea rows={3} placeholder='{"SPRING_PROFILES_ACTIVE":"prod"}' />
          </Form.Item>
        </Form>
      </Modal>

      <Card className="surface-card detail-tabs-card">
        <Tabs
          activeKey={activeTab}
          onChange={setActiveTab}
          items={[
            {
              key: 'overview',
              label: '概览',
              children: (
                <ApplicationOverviewTab
                  app={app}
                  services={appServices}
                  onJump={setActiveTab}
                />
              ),
            },
            ...(isFrontendApp
              ? [
                  { key: 'services', label: '服务', children: <FrontendDeployTab app={app} phase="services" /> },
                  { key: 'runtime', label: '运行', children: <FrontendDeployTab app={app} phase="runtime" /> },
                  { key: 'build', label: '构建', children: <FrontendDeployTab app={app} phase="build" /> },
                  { key: 'deploy', label: '部署', children: <FrontendDeployTab app={app} phase="deploy" /> },
                ]
              : [
                  { key: 'services', label: '服务', children: <ServicesTab appId={appId} app={app} onServicesChange={setAppServices} /> },
                  { key: 'runtime', label: '运行', children: <HostBindTab appId={appId} app={app} /> },
                  { key: 'build', label: '构建', children: <ArtifactTab app={app} /> },
                  { key: 'deploy', label: '部署', children: <PipelineTab app={app} /> },
                ]),
          ]}
        />
      </Card>
    </section>
  )
}

function DetailActionItem({
  tone,
  label,
  title,
  description,
  action,
}: {
  tone: StatusTone
  label: string
  title: string
  description: string
  action?: React.ReactNode
}) {
  return (
    <div className="action-item">
      <StatusTag tone={tone}>{label}</StatusTag>
      <div>
        <div className="action-item-title">{title}</div>
        <div className="action-item-desc">{description}</div>
        {action && <div className="action-item-action">{action}</div>}
      </div>
    </div>
  )
}

function ApplicationOverviewTab({
  app,
  services,
  onJump,
}: {
  app: App
  services: AppService[]
  onJump: (key: string) => void
}) {
  const [deployments, setDeployments] = useState<Deployment[]>([])
  const [builds, setBuilds] = useState<BuildRun[]>([])
  const [pipelines, setPipelines] = useState<PipelineRun[]>([])
  const [bundles, setBundles] = useState<ArtifactBundle[]>([])

  const refresh = async () => {
    try {
      const [ds, bs, ps, bundleRows] = await Promise.all([
        listDeployments(app.id),
        listBuilds(app.id),
        listPipelines(app.id),
        listBundles(app.id),
      ])
      setDeployments(ds)
      setBuilds(bs)
      setPipelines(ps)
      setBundles(bundleRows)
    } catch (e) {
      message.error(formatError(e))
    }
  }

  useEffect(() => { refresh() }, [app.id])

  const serviceMode = describeServiceMode(services)
  const buildRefs = appBuildRefs(app)
  const isFrontendApp = app.app_type === 'frontend'
  const enabledServices = services.filter((s) => s.enabled)
  const dockerEnabled = app.build_mode === 'local-docker' || app.build_mode === 'remote-docker' || app.deploy_mode === 'docker'
  const dockerImage = [app.docker_registry, app.docker_image_name || `${app.app_code}/{{SERVICE_CODE}}`].filter(Boolean).join('/') || `${app.app_code}/{{SERVICE_CODE}}`
  const serviceSummary = summarizeServiceCard(services)
  const portSummary = summarizePortCard(app, services)
  const runtimePlanSummary = summarizeRuntimePlanCard(app, services)
  const orderedBuilds = [...builds].sort((a, b) => timeValue(b.created_at) - timeValue(a.created_at))
  const orderedPipelines = [...pipelines].sort((a, b) => timeValue(b.created_at) - timeValue(a.created_at))
  const latestBuild = orderedBuilds[0]
  const latestPipeline = orderedPipelines[0]
  const runningDeployments = deployments.filter((d) => d.status === 'running')
  const stoppedDeployments = deployments.filter((d) => d.status === 'stopped' || d.status === 'failed')
  const missingArtifacts = deployments.filter((d) => !d.current_bundle_id && !d.current_artifact_id && !d.current_artifact_item_id)
  const failedBuilds = builds.filter((b) => b.status === 'failed')
  const failedPipelines = pipelines.filter((p) => p.status === 'failed')

  if (isFrontendApp) {
    const blockers = [
      !app.git_url && {
        tone: 'warning' as StatusTone,
        label: '代码源',
        title: 'Git 仓库未配置',
        description: '构建会从 Git 拉取 React/Vue 源码，缺少 git_url 就无法生成前端镜像。',
        action: <Button size="small" onClick={() => onJump('build')}>查看构建配置</Button>,
      },
      {
        tone: 'info' as StatusTone,
        label: '服务',
        title: '前端 service 与 Java service 一样先建档',
        description: '先确认 service_code、容器名和运行端口，再进入构建与部署。',
        action: <Button size="small" onClick={() => onJump('services')}>配置服务</Button>,
      },
      {
        tone: 'info' as StatusTone,
        label: '部署',
        title: '网关容器统一入口',
        description: '每个前端项目一个静态资源容器；宿主机 80/443 只由 swift-devops-gateway 暴露。',
        action: <Button size="small" type="primary" onClick={() => onJump('deploy')}>进入部署</Button>,
      },
    ].filter(Boolean) as Array<{
      tone: StatusTone
      label: string
      title: string
      description: string
      action: React.ReactNode
    }>

    return (
      <section className="workbench-tab app-overview-tab">
        <div className="overview-grid">
          <div className="overview-stack">
            <Card className="workbench-card" title="前端应用摘要">
              <Descriptions size="small" column={2}>
                <Descriptions.Item label="模型">React / Vue SPA 源码部署</Descriptions.Item>
                <Descriptions.Item label="默认构建 Ref"><code>{app.git_ref || 'main'}</code></Descriptions.Item>
                <Descriptions.Item label="Git 仓库" span={2}><code>{app.git_url || '-'}</code></Descriptions.Item>
                <Descriptions.Item label="可选构建 Ref" span={2}>
                  {buildRefs.map(ref => <StatusTag key={ref}>{ref}</StatusTag>)}
                </Descriptions.Item>
                <Descriptions.Item label="远端工作目录" span={2}><code>{app.deploy_path}</code></Descriptions.Item>
                <Descriptions.Item label="入口模型" span={2}>
                  <Space style={{ flexWrap: 'wrap' }}>
                    <StatusTag tone="info">Docker 静态资源容器</StatusTag>
                    <StatusTag tone="success">Gateway Container</StatusTag>
                    <Typography.Text type="secondary">同一宿主机可以部署多个域名，各自独立容器，由网关按 domain 转发。</Typography.Text>
                  </Space>
                </Descriptions.Item>
              </Descriptions>
            </Card>
          </div>
          <div className="overview-stack">
            <Card className="workbench-card" title="需要关注">
              <div className="action-list">
                {blockers.map((item) => <DetailActionItem key={`${item.label}-${item.title}`} {...item} />)}
              </div>
            </Card>
          </div>
        </div>
      </section>
    )
  }

  const blockers = [
    !app.git_url && {
      tone: 'warning' as StatusTone,
      label: '配置态',
      title: 'Git 仓库未配置',
      description: '当前系统以远端 Git 仓库为应用源，缺失仓库会阻断 Maven 扫描和从仓库构建。',
      action: <Button size="small" onClick={() => onJump('services')}>查看服务配置</Button>,
    },
    enabledServices.length === 0 && {
      tone: 'danger' as StatusTone,
      label: '服务',
      title: '没有启用的 service',
      description: '部署按 service 编排，至少需要启用一个 service 才能构建和部署。',
      action: <Button size="small" onClick={() => onJump('services')}>处理 service</Button>,
    },
    deployments.length === 0 && {
      tone: 'warning' as StatusTone,
      label: '运行态',
      title: '还没有绑定部署主机',
      description: '主机绑定是部署、运行态检测和日志读取的前置条件。',
      action: <Button size="small" onClick={() => onJump('runtime')}>绑定主机</Button>,
    },
    bundles.length === 0 && {
      tone: 'warning' as StatusTone,
      label: '构建态',
      title: '还没有 Bundle 制品',
      description: '建议从远端 Git 仓库构建生成整组 Bundle，部署和回滚都会更清晰。',
      action: <Button size="small" onClick={() => onJump('build')}>去构建</Button>,
    },
    failedBuilds.length > 0 && {
      tone: 'danger' as StatusTone,
      label: '构建态',
      title: `${failedBuilds.length} 条构建失败`,
      description: '优先看构建日志，常见原因是凭证、Maven 参数、模块路径或本机构建环境。',
      action: <Button size="small" onClick={() => onJump('build')}>看构建</Button>,
    },
    failedPipelines.length > 0 && {
      tone: 'danger' as StatusTone,
      label: '部署态',
      title: `${failedPipelines.length} 条部署失败`,
      description: '优先查看部署详情中的阶段日志；状态=成功的历史才能作为回滚目标。',
      action: <Button size="small" onClick={() => onJump('deploy')}>查部署</Button>,
    },
    stoppedDeployments.length > 0 && {
      tone: 'danger' as StatusTone,
      label: '运行态',
      title: `${stoppedDeployments.length} 个绑定运行异常`,
      description: '部署成功不等于远端容器仍在运行，请刷新运行态检测并核对当前制品。',
      action: <Button size="small" onClick={() => onJump('runtime')}>检测运行态</Button>,
    },
    missingArtifacts.length > 0 && {
      tone: 'warning' as StatusTone,
      label: '制品',
      title: `${missingArtifacts.length} 个绑定缺少当前制品`,
      description: '当前制品为空会让日志、回滚和版本追踪变得模糊，需要重新部署成功版本。',
      action: <Button size="small" onClick={() => onJump('deploy')}>选择版本部署</Button>,
    },
  ].filter(Boolean) as Array<{
    tone: StatusTone
    label: string
    title: string
    description: string
    action: React.ReactNode
  }>

  return (
    <section className="workbench-tab app-overview-tab">
      <div className="overview-grid">
        <div className="overview-stack">
          <Card className="workbench-card" title="应用摘要">
            <Descriptions size="small" column={2}>
              <Descriptions.Item label="模型">远端 Git 仓库 / Maven Service</Descriptions.Item>
              <Descriptions.Item label="当前模式" span={2}>
                <Space style={{ flexWrap: 'wrap' }}>
                  <StatusTag tone={serviceModeTone(serviceMode.color)}>{serviceMode.label}</StatusTag>
                  <StatusTag tone={tagTone(portSummary.tone)}>{portSummary.meta}</StatusTag>
                  <Typography.Text type="secondary">{serviceMode.desc}</Typography.Text>
                </Space>
              </Descriptions.Item>
              <Descriptions.Item label="Service 端口" span={2}>{renderServicePortMatrix(app, services)}</Descriptions.Item>
              <Descriptions.Item label="默认构建 Ref"><code>{app.git_ref || 'main'}</code></Descriptions.Item>
              <Descriptions.Item label="Git 仓库" span={2}><code>{app.git_url || '-'}</code></Descriptions.Item>
              <Descriptions.Item label="可选构建 Ref" span={2}>
                {buildRefs.map(ref => <StatusTag key={ref}>{ref}</StatusTag>)}
              </Descriptions.Item>
              <Descriptions.Item label="部署口径" span={2}>
                <Space style={{ flexWrap: 'wrap' }}>
                  <StatusTag tone={tagTone(runtimePlanSummary.tone)}>{runtimePlanSummary.value}</StatusTag>
                  <Typography.Text type="secondary">{runtimePlanSummary.description}</Typography.Text>
                </Space>
              </Descriptions.Item>
              <Descriptions.Item label="Service 健康检查" span={2}>{renderServiceHealthMatrix(app, services)}</Descriptions.Item>
              {dockerEnabled && (
                <>
                  <Descriptions.Item label="Docker 镜像" span={2}>
                    <code>{dockerImage}</code>
                    <Typography.Text type="secondary">:{app.docker_image_tag || 'latest'}</Typography.Text>
                  </Descriptions.Item>
                  <Descriptions.Item label="Docker 容器" span={2}>
                    <code>{app.docker_container_name || `devops-${app.app_code}`}</code>
                  </Descriptions.Item>
                </>
              )}
            </Descriptions>
          </Card>
        </div>

        <div className="overview-stack">
          <Card className="workbench-card" title="需要关注">
            {blockers.length > 0 ? (
              <div className="action-list">
                {blockers.map((item) => <DetailActionItem key={`${item.label}-${item.title}`} {...item} />)}
              </div>
            ) : (
              <DetailActionItem
                tone="success"
                label="健康"
                title="当前没有阻断项"
                description="配置、构建、部署和运行态都没有明显异常；需要发布时直接进入部署页。"
                action={<Button size="small" onClick={() => onJump('deploy')}>进入部署</Button>}
              />
            )}
          </Card>

          <Card className="workbench-card" title="最近状态">
            <div className="action-list">
              <DetailActionItem
                tone={latestBuild ? statusToneFromBuild(latestBuild.status) : 'neutral'}
                label="构建"
                title={latestBuild ? `Build #${latestBuild.id}` : '暂无构建'}
                description={latestBuild ? `${latestBuild.git_ref || '-'} · ${formatDateTime(latestBuild.created_at)}` : '从构建页触发远端 Git + Maven 构建。'}
                action={<Button size="small" onClick={() => onJump('build')}>查看构建</Button>}
              />
              <DetailActionItem
                tone={latestPipeline ? statusToneFromPipeline(latestPipeline.status) : 'neutral'}
                label="部署"
                title={latestPipeline ? `Run #${latestPipeline.id}` : '暂无部署'}
                description={latestPipeline ? `${latestPipeline.strategy} · ${formatDateTime(latestPipeline.created_at)}` : '选择 Bundle 或 Artifact 后触发部署。'}
                action={<Button size="small" onClick={() => onJump('deploy')}>查看部署</Button>}
              />
              <DetailActionItem
                tone={stoppedDeployments.length > 0 || missingArtifacts.length > 0 ? 'danger' : deployments.length > 0 ? 'success' : 'warning'}
                label="运行"
                title={`${runningDeployments.length}/${deployments.length} 个绑定运行中`}
                description="运行态来自部署绑定记录的最近一次检测，不等于部署历史状态。"
                action={<Button size="small" onClick={() => onJump('runtime')}>查看运行态</Button>}
              />
            </div>
          </Card>
        </div>
      </div>
    </section>
  )
}


// ---------- 前端应用：服务 / 运行 / 构建 / 部署 ----------

type FrontendWorkbenchPhase = 'services' | 'runtime' | 'build' | 'deploy'

type FrontendConfigFormValues = {
  service_code: string
  package_manager: FrontendPackageManager
  install_command?: string
  build_command?: string
  dist_dir: string
  node_image: string
  nginx_image: string
  spa_fallback: boolean
  container_name?: string
  target_port: number
  dockerfile?: string
  nginx_config?: string
  docker_build_args?: string
  docker_run_args?: string
}

type FrontendDeployFormValues = FrontendDeployInput & {
  https?: boolean
  apply_gateway?: boolean
  force_recreate_gateway?: boolean
}

const frontendGatewayStatusTag = (s?: string) => {
  if (s === 'running') return <StatusTag tone="success">运行中</StatusTag>
  if (s === 'failed') return <StatusTag tone="danger">异常</StatusTag>
  if (s === 'pending') return <StatusTag tone="warning">待同步</StatusTag>
  return <StatusTag>{s || '-'}</StatusTag>
}

const frontendRouteStatusTag = (row: FrontendGatewayRoute) => {
  if (!row.enabled) return <StatusTag>停用</StatusTag>
  if (row.status === 'active') return <StatusTag tone="success">active</StatusTag>
  if (row.status === 'failed') return <StatusTag tone="danger">failed</StatusTag>
  return <StatusTag tone="warning">{row.status || 'pending'}</StatusTag>
}

function FrontendDeployTab({ app, phase }: { app: App; phase: FrontendWorkbenchPhase }) {
  const [config, setConfig] = useState<FrontendAppConfig | null>(null)
  const [hosts, setHosts] = useState<Host[]>([])
  const [gateway, setGateway] = useState<FrontendGateway | null>(null)
  const [routes, setRoutes] = useState<FrontendGatewayRoute[]>([])
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [deploying, setDeploying] = useState(false)
  const [deployOpen, setDeployOpen] = useState(false)
  const [deployResult, setDeployResult] = useState<FrontendDeployResult | null>(null)
  const [previewOpen, setPreviewOpen] = useState(false)
  const [preview, setPreview] = useState<FrontendTemplatePreview | null>(null)
  const [routePreview, setRoutePreview] = useState<FrontendGatewayPreview | null>(null)
  const [routePreviewOpen, setRoutePreviewOpen] = useState(false)
  const [gatewayHostID, setGatewayHostID] = useState<number | undefined>()
  const [gatewayLoading, setGatewayLoading] = useState(false)
  const [form] = Form.useForm<FrontendConfigFormValues>()
  const [deployForm] = Form.useForm<FrontendDeployFormValues>()

  const selectedHost = hosts.find((h) => h.id === gatewayHostID)
  const serviceCode = config?.service_code || form.getFieldValue('service_code') || 'web'

  const loadGatewayState = async (hostID?: number) => {
    const id = Number(hostID || gatewayHostID || 0)
    if (!id) {
      setGateway(null)
      setRoutes([])
      return
    }
    setGatewayLoading(true)
    try {
      const [gw, routeRows] = await Promise.all([
        getFrontendGateway(id).catch((e) => {
          const msg = formatError(e)
          if (msg.includes('不存在') || msg.includes('not found')) return null
          throw e
        }),
        listFrontendGatewayRoutes(id),
      ])
      setGateway(gw)
      setRoutes(routeRows)
    } catch (e) {
      setGateway(null)
      setRoutes([])
      message.warning(`网关状态读取失败：${formatError(e)}`)
    } finally {
      setGatewayLoading(false)
    }
  }

  const applyConfigToForm = (cfg: FrontendAppConfig) => {
    form.setFieldsValue({
      service_code: cfg.service_code,
      package_manager: cfg.package_manager,
      install_command: cfg.install_command,
      build_command: cfg.build_command,
      dist_dir: cfg.dist_dir,
      node_image: cfg.node_image,
      nginx_image: cfg.nginx_image,
      spa_fallback: cfg.spa_fallback,
      container_name: cfg.container_name,
      target_port: cfg.target_port,
      dockerfile: cfg.dockerfile,
      nginx_config: cfg.nginx_config,
      docker_build_args: cfg.docker_build_args,
      docker_run_args: cfg.docker_run_args,
    })
  }

  const refresh = async () => {
    setLoading(true)
    try {
      const requestedServiceCode = form.getFieldValue('service_code') || config?.service_code || 'web'
      const [cfg, hostRows] = await Promise.all([
        getFrontendConfig(app.id, requestedServiceCode),
        listHosts(),
      ])
      setConfig(cfg)
      setHosts(hostRows)
      applyConfigToForm(cfg)
      const nextHostID = gatewayHostID || hostRows[0]?.id
      if (nextHostID && !gatewayHostID) setGatewayHostID(nextHostID)
      if (nextHostID) void loadGatewayState(nextHostID)
    } catch (e) {
      message.error(formatError(e))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { refresh() }, [app.id, phase])

  useEffect(() => {
    if (!gatewayHostID) return
    loadGatewayState(gatewayHostID)
  }, [gatewayHostID])

  const persistConfig = async (showSuccess = false) => {
    const v = await form.validateFields()
    const saved = await saveFrontendConfig(app.id, {
      service_code: v.service_code || 'web',
      package_manager: v.package_manager || 'auto',
      install_command: v.install_command || '',
      build_command: v.build_command || '',
      dist_dir: v.dist_dir || 'dist',
      node_image: v.node_image || 'node:20-alpine',
      nginx_image: v.nginx_image || 'nginx:1.27-alpine',
      spa_fallback: Boolean(v.spa_fallback),
      container_name: v.container_name || '',
      target_port: Number(v.target_port || 80),
      dockerfile: v.dockerfile || '',
      nginx_config: v.nginx_config || '',
      docker_build_args: v.docker_build_args || '',
      docker_run_args: v.docker_run_args || '',
    })
    setConfig(saved)
    applyConfigToForm(saved)
    if (showSuccess) message.success('前端服务配置已保存')
    return saved
  }

  const handleSaveConfig = async () => {
    setSaving(true)
    try {
      await persistConfig(true)
    } catch (e: any) {
      if (!e?.errorFields) message.error(formatError(e))
    } finally {
      setSaving(false)
    }
  }

  const openPreview = async () => {
    try {
      await persistConfig(false)
      const code = form.getFieldValue('service_code') || serviceCode
      setPreview(await previewFrontendConfig(app.id, code))
      setPreviewOpen(true)
    } catch (e: any) {
      if (!e?.errorFields) message.error(formatError(e))
    }
  }

  const openDeploy = () => {
    const cfg = config
    deployForm.resetFields()
    deployForm.setFieldsValue({
      host_id: gatewayHostID || hosts[0]?.id,
      service_code: cfg?.service_code || form.getFieldValue('service_code') || 'web',
      git_ref: app.git_ref || 'main',
      cred_id: Number(app.git_cred_id || 0),
      domain: '',
      https: false,
      apply_gateway: true,
      force_recreate_gateway: false,
    })
    setDeployOpen(true)
  }

  const triggerDeploy = async () => {
    setDeploying(true)
    try {
      const v = await deployForm.validateFields()
      const payload: FrontendDeployInput = {
        host_id: Number(v.host_id),
        service_code: v.service_code || serviceCode,
        git_ref: v.git_ref || app.git_ref || 'main',
        domain: v.domain,
        https: Boolean(v.https),
        cert_path: v.cert_path || '',
        key_path: v.key_path || '',
        apply_gateway: Boolean(v.apply_gateway),
        force_recreate_gateway: Boolean(v.force_recreate_gateway),
      }
      if (v.cred_id && Number(v.cred_id) > 0) payload.cred_id = Number(v.cred_id)
      const out = await deployFrontendApp(app.id, payload)
      setDeployResult(out)
      setGatewayHostID(out.host_id)
      await loadGatewayState(out.host_id)
      message.success(`部署完成：${out.domain}`)
      setDeployOpen(false)
    } catch (e: any) {
      if (e?.errorFields) return
      message.error(formatError(e))
    } finally {
      setDeploying(false)
    }
  }

  const ensureGateway = async (start: boolean) => {
    if (!gatewayHostID) {
      message.warning('请先选择目标主机')
      return
    }
    setGatewayLoading(true)
    try {
      const gw = await ensureFrontendGateway(gatewayHostID, { start })
      setGateway(gw)
      await loadGatewayState(gatewayHostID)
      message.success(start ? '网关容器已初始化并尝试启动' : '网关配置已初始化')
    } catch (e) {
      message.error(formatError(e))
    } finally {
      setGatewayLoading(false)
    }
  }

  const applyGateway = async (forceRecreate = false) => {
    if (!gatewayHostID) {
      message.warning('请先选择目标主机')
      return
    }
    setGatewayLoading(true)
    try {
      const out = await applyFrontendGateway(gatewayHostID, forceRecreate)
      setGateway(out.gateway)
      setRoutes(out.routes)
      message.success(out.message || '网关配置已应用')
    } catch (e) {
      message.error(formatError(e))
    } finally {
      setGatewayLoading(false)
    }
  }

  const openRoutePreview = async (row: FrontendGatewayRoute) => {
    try {
      setRoutePreview(await previewFrontendGatewayRoute(row.id))
      setRoutePreviewOpen(true)
    } catch (e) {
      message.error(formatError(e))
    }
  }

  const renderToolbar = () => {
    if (phase === 'services') {
      return (
        <>
          <Button type="primary" loading={saving} onClick={handleSaveConfig}>保存服务配置</Button>
          <Button loading={loading} onClick={refresh}>刷新</Button>
        </>
      )
    }
    if (phase === 'runtime') {
      return (
        <>
          <Button loading={gatewayLoading} disabled={!gatewayHostID} onClick={() => ensureGateway(false)}>初始化网关</Button>
          <Button loading={gatewayLoading} disabled={!gatewayHostID} onClick={() => ensureGateway(true)}>初始化并启动</Button>
          <Button type="primary" loading={gatewayLoading} disabled={!gatewayHostID} onClick={() => applyGateway(false)}>Apply Gateway</Button>
          <Button danger loading={gatewayLoading} disabled={!gatewayHostID} onClick={() => applyGateway(true)}>重建网关</Button>
          <Button loading={loading} onClick={refresh}>刷新</Button>
        </>
      )
    }
    if (phase === 'build') {
      return (
        <>
          <Button type="primary" loading={saving} onClick={handleSaveConfig}>保存构建配置</Button>
          <Button onClick={openPreview}>预览 Dockerfile</Button>
          <Button loading={loading} onClick={refresh}>刷新</Button>
        </>
      )
    }
    return (
      <>
        <Button type="primary" loading={deploying} disabled={!app.git_url || hosts.length === 0} onClick={openDeploy}>触发部署</Button>
        <Button loading={loading} onClick={refresh}>刷新</Button>
      </>
    )
  }

  const phaseHint: Record<FrontendWorkbenchPhase, string> = {
    services: '前端也先维护 service：service_code、容器名、端口与 SPA fallback，和 Java service 的入口心智保持一致。',
    runtime: '运行页只处理目标主机、gateway container 和 domain route，不混入构建参数。',
    build: '构建页只维护 package manager、build command、Dockerfile 与 nginx.conf，并提供模板预览。',
    deploy: app.git_url ? '部署页负责选择目标主机、Git Ref、域名并触发发布。' : '应用未配置 Git 仓库，无法拉取 React/Vue 源码。',
  }

  const serviceRows = config ? [config] : []

  const renderServiceTab = () => (
    <>
      <Card className="workbench-card" size="small" title="服务列表">
        <Space style={{ marginBottom: 12, flexWrap: 'wrap' }}>
          <Tag color="blue">前端 service</Tag>
          <Typography.Text type="secondary">一个前端 service 对应一个静态资源容器；多个域名/服务后续按 service_code 区分。</Typography.Text>
        </Space>
        <Table<FrontendAppConfig>
          rowKey="service_code"
          size="small"
          loading={loading}
          dataSource={serviceRows}
          pagination={false}
          locale={{ emptyText: <EmptyState title="还没有前端 service" description="保存下方服务配置后，会形成一个可部署的前端 service。" /> }}
          columns={[
            { title: 'service_code', dataIndex: 'service_code', render: (v: string) => <code>{v}</code> },
            { title: '容器名', dataIndex: 'container_name', render: (v: string) => v ? <code>{v}</code> : <Typography.Text type="secondary">自动生成</Typography.Text> },
            { title: '端口', dataIndex: 'target_port', width: 90 },
            { title: 'SPA', dataIndex: 'spa_fallback', width: 90, render: (v: boolean) => v ? <Tag color="green">fallback</Tag> : <Tag>static</Tag> },
            { title: '包管理器', dataIndex: 'package_manager', width: 120, render: (v: string) => <Tag>{v}</Tag> },
          ]}
        />
      </Card>

      <Card className="workbench-card" size="small" title="服务配置">
        <Form form={form} layout="vertical" initialValues={{ service_code: 'web', package_manager: 'auto', spa_fallback: true, target_port: 80 }}>
          <Space style={{ display: 'flex' }} align="start">
            <Form.Item
              name="service_code"
              label="service_code"
              style={{ flex: 1 }}
              rules={[{ required: true, pattern: /^[a-z][a-z0-9-]{1,49}$/, message: '小写字母开头，2-50 位，仅小写字母/数字/连字符' }]}
              tooltip="和 Java service_code 一样，是部署与路由的最小服务标识。"
            >
              <Input placeholder="web / h5 / business" />
            </Form.Item>
            <Form.Item name="container_name" label="容器名" style={{ flex: 1 }} tooltip="留空 = sd-fe-<app_code>-<service_code>">
              <Input placeholder={`sd-fe-${app.app_code}-web`} />
            </Form.Item>
          </Space>
          <Space style={{ display: 'flex' }} align="start">
            <Form.Item name="target_port" label="容器端口" style={{ flex: 1 }} rules={[{ required: true }]}>
              <InputNumber min={1} max={65535} style={{ width: '100%' }} />
            </Form.Item>
            <Form.Item name="spa_fallback" label="SPA fallback" valuePropName="checked" style={{ flex: 1 }} tooltip="React/Vue history 路由通常需要开启。">
              <Switch />
            </Form.Item>
          </Space>
          <Form.Item name="package_manager" hidden><Input /></Form.Item>
          <Form.Item name="install_command" hidden><Input /></Form.Item>
          <Form.Item name="build_command" hidden><Input /></Form.Item>
          <Form.Item name="dist_dir" hidden><Input /></Form.Item>
          <Form.Item name="node_image" hidden><Input /></Form.Item>
          <Form.Item name="nginx_image" hidden><Input /></Form.Item>
          <Form.Item name="dockerfile" hidden><Input /></Form.Item>
          <Form.Item name="nginx_config" hidden><Input /></Form.Item>
          <Form.Item name="docker_build_args" hidden><Input /></Form.Item>
          <Form.Item name="docker_run_args" hidden><Input /></Form.Item>
        </Form>
      </Card>
    </>
  )

  const renderRuntimeTab = () => (
    <>
      <Card className="workbench-card" title="运行入口 Gateway" size="small">
        <Space direction="vertical" style={{ width: '100%' }} size={12}>
          <Select
            value={gatewayHostID}
            placeholder="选择目标主机"
            onChange={setGatewayHostID}
            options={hosts.map((h) => ({ value: h.id, label: `${h.name} (${h.ip})${h.status === 'online' ? ' · 在线' : ''}` }))}
          />
          <Descriptions size="small" column={2}>
            <Descriptions.Item label="主机"><code>{selectedHost?.ip || '-'}</code></Descriptions.Item>
            <Descriptions.Item label="状态">{frontendGatewayStatusTag(gateway?.status)}</Descriptions.Item>
            <Descriptions.Item label="网关容器">{gateway?.container_name ? <code>{gateway.container_name}</code> : '-'}</Descriptions.Item>
            <Descriptions.Item label="Network">{gateway?.network_name ? <code>{gateway.network_name}</code> : 'swift-devops-gateway'}</Descriptions.Item>
            <Descriptions.Item label="端口">{gateway ? `${gateway.http_port}/${gateway.https_port}` : '80/443'}</Descriptions.Item>
            <Descriptions.Item label="配置目录" span={2}>{gateway?.config_dir ? <code>{gateway.config_dir}</code> : '-'}</Descriptions.Item>
          </Descriptions>
          <Typography.Text type="secondary">
            同一台机器部署多个前端时，只保留一个 gateway 容器占用宿主机 80/443；业务前端容器不直接暴露宿主机端口。
          </Typography.Text>
        </Space>
      </Card>
      {renderRoutesTable()}
    </>
  )

  const renderBuildTab = () => (
    <Card className="workbench-card" title="构建配置" size="small">
      <Form form={form} layout="vertical" initialValues={{ service_code: 'web', package_manager: 'auto', dist_dir: 'dist', node_image: 'node:20-alpine', nginx_image: 'nginx:1.27-alpine', spa_fallback: true, target_port: 80 }}>
        <Form.Item name="service_code" hidden><Input /></Form.Item>
        <Form.Item name="container_name" hidden><Input /></Form.Item>
        <Form.Item name="target_port" hidden><Input /></Form.Item>
        <Form.Item name="spa_fallback" hidden valuePropName="checked"><Switch /></Form.Item>
        <Space style={{ display: 'flex' }} align="start">
          <Form.Item name="package_manager" label="包管理器" style={{ flex: 1 }} rules={[{ required: true }]}>
            <Select
              options={[
                { value: 'auto', label: 'auto（按 lock 文件自动识别）' },
                { value: 'npm', label: 'npm' },
                { value: 'pnpm', label: 'pnpm' },
                { value: 'yarn', label: 'yarn' },
              ]}
            />
          </Form.Item>
          <Form.Item name="dist_dir" label="产物目录" style={{ flex: 1 }} rules={[{ required: true }]}>
            <Input placeholder="dist / build" />
          </Form.Item>
        </Space>
        <Space style={{ display: 'flex' }} align="start">
          <Form.Item name="build_command" label="构建命令" style={{ flex: 1 }} tooltip="留空会按包管理器默认生成，例如 npm run build">
            <Input placeholder="npm run build / pnpm build" />
          </Form.Item>
          <Form.Item name="install_command" label="安装命令覆盖" style={{ flex: 1 }} tooltip="留空走 lock 文件自动识别">
            <Input placeholder="npm ci / pnpm install --frozen-lockfile" />
          </Form.Item>
        </Space>
        <Space style={{ display: 'flex' }} align="start">
          <Form.Item name="node_image" label="Node 构建镜像" style={{ flex: 1 }} rules={[{ required: true }]}>
            <Input placeholder="node:20-alpine" />
          </Form.Item>
          <Form.Item name="nginx_image" label="运行镜像" style={{ flex: 1 }} rules={[{ required: true }]}>
            <Input placeholder="nginx:1.27-alpine" />
          </Form.Item>
        </Space>
        <Form.Item name="docker_build_args" label="docker build 参数">
          <Input placeholder="--build-arg VITE_API_BASE=/api" />
        </Form.Item>
        <Form.Item name="docker_run_args" label="docker run 参数" tooltip="通常留空；系统会自动加入 swift-devops-gateway network。">
          <Input.TextArea rows={2} placeholder="一般留空；需要环境变量时可写 -e KEY=value" />
        </Form.Item>
        <Form.Item name="dockerfile" label="自定义 Dockerfile（可选）" tooltip="留空使用系统生成模板；支持 {{NODE_IMAGE}} / {{NGINX_IMAGE}} / {{DIST_DIR}}。">
          <Input.TextArea rows={8} style={{ fontFamily: 'monospace' }} placeholder="留空使用默认多阶段 Dockerfile" />
        </Form.Item>
        <Form.Item name="nginx_config" label="自定义容器内 nginx.conf（可选）">
          <Input.TextArea rows={8} style={{ fontFamily: 'monospace' }} placeholder="留空使用默认 SPA 静态资源配置" />
        </Form.Item>
      </Form>
    </Card>
  )

  const renderDeployTab = () => (
    <>
      <Card className="workbench-card" size="small" title="部署入口">
        <Space direction="vertical" size={10} style={{ width: '100%' }}>
          <Space wrap>
            <StatusTag tone={app.git_url ? 'success' : 'warning'}>{app.git_url ? 'Git Ready' : 'Git 未配置'}</StatusTag>
            <StatusTag tone={hosts.length > 0 ? 'success' : 'warning'}>{hosts.length > 0 ? `${hosts.length} 台主机` : '无主机'}</StatusTag>
            <StatusTag tone="info">service: {serviceCode}</StatusTag>
          </Space>
          <Typography.Text type="secondary">
            部署会执行 Git clone、写入 Dockerfile/nginx.conf、上传目标机、docker build/run，并创建或更新 gateway route。
          </Typography.Text>
          <Button type="primary" loading={deploying} disabled={!app.git_url || hosts.length === 0} onClick={openDeploy}>选择主机与域名部署</Button>
        </Space>
      </Card>
      {deployResult && (
        <Card className="workbench-card" title="最近一次部署" size="small">
          <Descriptions size="small" column={2}>
            <Descriptions.Item label="域名"><code>{deployResult.domain}</code></Descriptions.Item>
            <Descriptions.Item label="容器"><code>{deployResult.container_name}</code></Descriptions.Item>
            <Descriptions.Item label="镜像" span={2}><code>{deployResult.image}</code></Descriptions.Item>
            <Descriptions.Item label="Commit"><code>{deployResult.commit_sha?.slice(0, 12)}</code></Descriptions.Item>
            <Descriptions.Item label="时间">{formatDateTime(deployResult.deployed_at)}</Descriptions.Item>
            <Descriptions.Item label="远端目录" span={2}><code>{deployResult.remote_work_dir}</code></Descriptions.Item>
          </Descriptions>
        </Card>
      )}
      {renderRoutesTable()}
    </>
  )

  const renderRoutesTable = () => (
    <Card className="workbench-card" title="Gateway routes" size="small">
      <Table<FrontendGatewayRoute>
        rowKey="id"
        loading={gatewayLoading}
        dataSource={routes}
        pagination={false}
        locale={{ emptyText: <EmptyState title="还没有前端域名路由" description="部署前端项目时会自动创建或更新 route。" /> }}
        columns={[
          { title: '域名', dataIndex: 'domain', render: (v: string, row) => <Space><code>{v}</code>{row.https && <Tag color="blue">HTTPS</Tag>}</Space> },
          { title: '应用', render: (_, row) => <>{row.app_name || row.app_code} <Typography.Text type="secondary">/ {row.service_code}</Typography.Text></> },
          { title: '容器', dataIndex: 'container_name', render: (v: string, row) => <code>{v}:{row.target_port}</code> },
          { title: '状态', width: 120, render: (_, row) => frontendRouteStatusTag(row) },
          { title: '更新时间', dataIndex: 'updated_at', width: 180, render: (v: string) => formatDateTime(v) },
          { title: '操作', width: 110, render: (_, row) => <Button size="small" onClick={() => openRoutePreview(row)}>预览</Button> },
        ]}
      />
    </Card>
  )

  return (
    <section className="workbench-tab frontend-deploy-tab">
      <div className="workbench-toolbar">
        <div className="workbench-toolbar-main">{renderToolbar()}</div>
        <div className="workbench-hint">{phaseHint[phase]}</div>
      </div>

      {phase === 'services' && renderServiceTab()}
      {phase === 'runtime' && renderRuntimeTab()}
      {phase === 'build' && renderBuildTab()}
      {phase === 'deploy' && renderDeployTab()}

      <Modal
        title="触发部署"
        open={deployOpen}
        onOk={triggerDeploy}
        onCancel={() => setDeployOpen(false)}
        okText="开始部署"
        cancelText="取消"
        width={680}
        confirmLoading={deploying}
        maskClosable={false}
        keyboard={false}
      >
        <Form form={deployForm} layout="vertical">
          <Space style={{ display: 'flex' }} align="start">
            <Form.Item name="host_id" label="目标主机" style={{ flex: 1 }} rules={[{ required: true, message: '请选择目标主机' }]}>
              <Select options={hosts.map((h) => ({ value: h.id, label: `${h.name} (${h.ip})` }))} />
            </Form.Item>
            <Form.Item name="service_code" label="service_code" style={{ flex: 1 }} rules={[{ required: true }]}>
              <Input />
            </Form.Item>
          </Space>
          <Space style={{ display: 'flex' }} align="start">
            <Form.Item name="git_ref" label="Git Ref" style={{ flex: 1 }} rules={[{ required: true }]}>
              <Select showSearch options={appBuildRefs(app).map(ref => ({ value: ref, label: ref }))} />
            </Form.Item>
            <Form.Item name="cred_id" label="Git 凭证 ID" style={{ flex: 1 }} tooltip="默认来自应用 git_cred_id；公网仓可留 0。">
              <InputNumber min={0} style={{ width: '100%' }} />
            </Form.Item>
          </Space>
          <Form.Item name="domain" label="访问域名" rules={[{ required: true, message: '请输入域名，例如 www.xxx.top' }]}>
            <Input placeholder="www.xxx.top / h5.xxx.top / business.xxx.top" />
          </Form.Item>
          <Space style={{ display: 'flex' }} align="start">
            <Form.Item name="https" label="HTTPS" valuePropName="checked" style={{ width: 120 }}>
              <Switch />
            </Form.Item>
            <Form.Item name="apply_gateway" label="部署后 Apply Gateway" valuePropName="checked" style={{ width: 190 }}>
              <Switch />
            </Form.Item>
            <Form.Item name="force_recreate_gateway" label="强制重建 Gateway" valuePropName="checked" style={{ width: 190 }}>
              <Switch />
            </Form.Item>
          </Space>
          <Space style={{ display: 'flex' }} align="start">
            <Form.Item name="cert_path" label="证书路径" style={{ flex: 1 }} tooltip="HTTPS 开启时需要是 gateway 容器内可读路径。">
              <Input placeholder="/etc/nginx/certs/www/fullchain.pem" />
            </Form.Item>
            <Form.Item name="key_path" label="私钥路径" style={{ flex: 1 }}>
              <Input placeholder="/etc/nginx/certs/www/privkey.pem" />
            </Form.Item>
          </Space>
          <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
            首次构建可能较慢。目标机器需要 Docker，且 gateway 需要能占用 80/443。
          </Typography.Paragraph>
        </Form>
      </Modal>

      <Modal title="生成模板预览" open={previewOpen} onCancel={() => setPreviewOpen(false)} footer={null} width={980} destroyOnClose>
        <Tabs
          items={[
            { key: 'dockerfile', label: 'Dockerfile', children: <LogTerminal text={preview?.dockerfile || ''} /> },
            { key: 'nginx', label: 'nginx.conf', children: <LogTerminal text={preview?.nginx_config || ''} /> },
          ]}
        />
      </Modal>

      <Modal title={routePreview?.file_name || 'Route Nginx 配置'} open={routePreviewOpen} onCancel={() => setRoutePreviewOpen(false)} footer={null} width={900} destroyOnClose>
        <LogTerminal text={routePreview?.content || ''} />
      </Modal>
    </section>
  )
}

// ---------- 主机绑定 Tab ----------

function HostBindTab({ appId, app }: { appId: number; app: App }) {
  const [deployments, setDeployments] = useState<Deployment[]>([])
  const [hosts, setHosts] = useState<Host[]>([])
  const [services, setServices] = useState<AppService[]>([])
  const [loading, setLoading] = useState(false)
  const [runtimeChecking, setRuntimeChecking] = useState(false)
  const [bindOpen, setBindOpen] = useState(false)
  const [bindForm] = Form.useForm()
  const [logOpen, setLogOpen] = useState(false)
  const [logDeployment, setLogDeployment] = useState<Deployment | null>(null)
  const [runtimeLog, setRuntimeLog] = useState<DeploymentRuntimeLogs | null>(null)
  const [logLoading, setLogLoading] = useState(false)
  const [logLines, setLogLines] = useState(300)
  const [logTimestamps, setLogTimestamps] = useState(false)
  const [logAutoRefresh, setLogAutoRefresh] = useState(true)
  const [runtimeActionKey, setRuntimeActionKey] = useState('')
  const latestRuntimeLogKeyRef = useRef('')

  const runtimeLogKey = (d: Deployment) =>
    `${d.id}:${d.host_id}:${logLines}:${logTimestamps ? 1 : 0}`

  const refresh = async (withRuntimeCheck = true) => {
    setLoading(true)
    try {
      const [ds, hs, svcRows] = await Promise.all([
        listDeployments(appId),
        listHosts(),
        listAppServices(appId),
      ])
      setDeployments(ds); setHosts(hs); setServices(svcRows)
      if (withRuntimeCheck && ds.length > 0) {
        setRuntimeChecking(true)
        try {
          setDeployments(await checkDeploymentRuntime(appId))
        } catch (e) {
          message.warning(`运行状态检测失败：${formatError(e)}`)
        } finally {
          setRuntimeChecking(false)
        }
      }
    } catch (e) {
      message.error(formatError(e))
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => { refresh(true) }, [appId])

  const fetchRuntimeLog = async (d = logDeployment, silent = false) => {
    if (!d) return
    const requestKey = runtimeLogKey(d)
    latestRuntimeLogKeyRef.current = requestKey
    if (!silent) setLogLoading(true)
    try {
      const data = await getDeploymentRuntimeLogs(d.id, logLines, logTimestamps)
      if (latestRuntimeLogKeyRef.current !== requestKey) return
      setRuntimeLog(data)
    } catch (e) {
      if (!silent) message.error(formatError(e))
    } finally {
      if (!silent && latestRuntimeLogKeyRef.current === requestKey) setLogLoading(false)
    }
  }

  useEffect(() => {
    if (!logOpen || !logDeployment) return
    fetchRuntimeLog(logDeployment)
  }, [logOpen, logDeployment?.id])

  useEffect(() => {
    if (!logOpen || !logDeployment) return
    fetchRuntimeLog(logDeployment)
  }, [logLines, logTimestamps])

  useEffect(() => {
    if (!logOpen || !logDeployment || !logAutoRefresh) return
    const timer = window.setInterval(() => {
      fetchRuntimeLog(logDeployment, true)
    }, 2000)
    return () => window.clearInterval(timer)
  }, [logOpen, logDeployment?.id, logLines, logTimestamps, logAutoRefresh])

  const enabledServices = services.filter((s) => s.enabled)
  const deploymentKeys = new Set(deployments.map((d) => `${d.host_id}:${d.service_code || 'default'}`))
  const availableHosts = hosts.filter((h) => {
    if (enabledServices.length === 0) return false
    return enabledServices.some((s) => !deploymentKeys.has(`${h.id}:${s.service_code || 'default'}`))
  })

  const handleBind = async () => {
    try {
      const v = await bindForm.validateFields()
      const serviceCodes = ((v.service_codes || []) as string[])
        .map((code) => code || 'default')
      if (serviceCodes.length === 0) {
        message.warning('请至少选择一个 service')
        return
      }
      const missing = serviceCodes.filter((code) => !deploymentKeys.has(`${v.host_id}:${code}`))
      if (missing.length === 0) {
        message.warning('选中的 service 已经绑定到这台主机')
        return
      }
      await Promise.all(missing.map((serviceCode) => bindHost(appId, {
        host_id: v.host_id,
        service_code: serviceCode,
        port: v.port,
      })))
      message.success('已绑定')
      setBindOpen(false); refresh()
    } catch (e) {
      if ((e as any)?.errorFields) return
      message.error(formatError(e))
    }
  }
  const handleUnbind = (d: Deployment) => {
    if (isDeploymentRunning(d)) {
      message.warning('当前容器仍在运行，请先停止容器后再解绑主机')
      return
    }
    Modal.confirm({
      title: `确认解绑 ${d.host_name}（${d.host_ip}）？`,
      content: '解绑后该主机上的应用部署关系会被删除（不会真正卸载远端服务）',
      okType: 'danger',
      onOk: async () => {
        try { await unbindDeployment(d.id); message.success('已解绑'); refresh() }
        catch (e) { message.error(formatError(e)) }
      },
    })
  }
  const applyRuntimeActionResult = (out: DeploymentRuntimeAction) => {
    setDeployments((rows) => rows.map((row) => row.id === out.deployment_id ? {
      ...row,
      status: out.status,
      runtime_container_name: out.container_name,
      runtime_status_detail: out.message,
      runtime_checked_at: out.operated_at,
    } : row))
    setLogDeployment((row) => row && row.id === out.deployment_id ? {
      ...row,
      status: out.status,
      runtime_container_name: out.container_name,
      runtime_status_detail: out.message,
      runtime_checked_at: out.operated_at,
    } : row)
  }
  const handleRuntimeAction = (d: Deployment, action: 'stop' | 'restart') => {
    const isStop = action === 'stop'
    if (isStop && !isDeploymentRunning(d)) {
      message.warning('当前容器不是运行中，无需停止')
      return
    }
    Modal.confirm({
      className: 'runtime-action-confirm',
      title: `${isStop ? '停止' : '重启'}容器 ${d.runtime_container_name || ''}？`,
      content: (
        <Space direction="vertical" size={4}>
          <Typography.Text>
            主机：<code>{d.host_ip}</code>　Service：<code>{d.service_code || 'default'}</code>
          </Typography.Text>
          <Typography.Text type="secondary">
            后端会按当前 deployment 推导容器名，不允许切换或指定其它容器。
          </Typography.Text>
        </Space>
      ),
      okText: isStop ? '停止容器' : '重启容器',
      okType: isStop ? 'danger' : 'primary',
      onOk: async () => {
        const key = `${d.id}:${action}`
        setRuntimeActionKey(key)
        try {
          const out = isStop
            ? await stopDeploymentRuntime(d.id)
            : await restartDeploymentRuntime(d.id)
          applyRuntimeActionResult(out)
          message.success(out.message || `${isStop ? '停止' : '重启'}容器操作已执行`)
          if (logOpen && logDeployment?.id === d.id) {
            fetchRuntimeLog(d, true)
          }
        } catch (e) {
          message.error(formatError(e))
        } finally {
          setRuntimeActionKey('')
        }
      },
    })
  }
  const openRuntimeLog = (d: Deployment) => {
    setLogDeployment(d)
    setRuntimeLog(null)
    latestRuntimeLogKeyRef.current = ''
    setLogOpen(true)
  }

  return (
    <section className="workbench-tab host-bind-tab">
      <div className="workbench-toolbar">
        <div className="workbench-toolbar-main">
          <Button type="primary" disabled={availableHosts.length === 0}
            onClick={() => {
              bindForm.resetFields()
              bindForm.setFieldsValue({
                service_codes: enabledServices.map((s) => s.service_code),
              })
              setBindOpen(true)
            }}>
            绑定主机
          </Button>
          <Button loading={loading || runtimeChecking} onClick={() => refresh(true)}>
            刷新并检测运行状态
          </Button>
        </div>
        <div className="workbench-hint">
          {runtimeChecking ? '正在 SSH 到远端校验容器状态…' : '绑定关系按 app × host × service 管理，运行态建议刷新时同步检测。'}
        </div>
      </div>
      <Table<Deployment>
        rowKey="id" loading={loading} dataSource={deployments} pagination={false}
        locale={{ emptyText: <EmptyState title="还没绑定主机" description="先选择目标主机和 service，建立部署关系后才能触发发布。" /> }}
        columns={[
          { title: '部署 ID', dataIndex: 'id', width: 80 },
          { title: '主机', render: (_, d) => <><code>{d.host_ip}</code>　<Typography.Text>{d.host_name}</Typography.Text></> },
          {
            title: 'Service',
            dataIndex: 'service_code',
            width: 170,
            render: (v: string) => v
              ? <Tag color="blue">{v}</Tag>
              : <Tag>default</Tag>,
          },
          { title: '主机状态', dataIndex: 'host_status', width: 100, render: (s) => hostStatusTag(s) },
          { title: '端口覆盖', dataIndex: 'port', width: 90, render: (p) => p || <Typography.Text type="secondary">（继承）</Typography.Text> },
          { title: '当前制品', width: 210, render: (_, d) => deploymentArtifactLabel(d) },
          { title: '最近部署', width: 180, render: (_, d) => lastDeployStatusView(d) },
          {
            title: '操作', width: 300,
            render: (_, d) => {
              const anyRuntimeActionLoading = runtimeActionKey !== ''
              const stopKey = `${d.id}:stop`
              const restartKey = `${d.id}:restart`
              const stopDisabled = !isDeploymentRunning(d) || anyRuntimeActionLoading
              const restartDisabled = (d.status === 'pending' && !d.runtime_container_name && !d.current_artifact_id && !d.current_artifact_item_id) || anyRuntimeActionLoading
              const unbindDisabled = isDeploymentRunning(d) || anyRuntimeActionLoading
              return (
                <Space size={4} wrap>
                  <Button size="small" onClick={() => openRuntimeLog(d)}>运行日志</Button>
                  <Tooltip title={stopDisabled ? (isDeploymentRunning(d) ? '正在执行运行操作' : '当前容器不是运行中，无需停止') : ''}>
                    <span>
                      <Button
                        size="small"
                        danger
                        loading={runtimeActionKey === stopKey}
                        disabled={stopDisabled}
                        onClick={() => handleRuntimeAction(d, 'stop')}
                      >
                        停止
                      </Button>
                    </span>
                  </Tooltip>
                  <Tooltip title={restartDisabled ? (anyRuntimeActionLoading ? '正在执行运行操作' : '当前还没有可重启的容器') : ''}>
                    <span>
                      <Button
                        size="small"
                        loading={runtimeActionKey === restartKey}
                        disabled={restartDisabled}
                        onClick={() => handleRuntimeAction(d, 'restart')}
                      >
                        重启
                      </Button>
                    </span>
                  </Tooltip>
                  <Tooltip title={unbindDisabled ? (anyRuntimeActionLoading ? '正在执行运行操作' : '当前容器运行中，请先停止容器后再解绑主机') : ''}>
                    <span>
                      <Button danger size="small" disabled={unbindDisabled} onClick={() => handleUnbind(d)}>
                        解绑
                      </Button>
                    </span>
                  </Tooltip>
                </Space>
              )
            },
          },
        ]}
      />
      <Modal title="绑定主机" open={bindOpen} onOk={handleBind} onCancel={() => setBindOpen(false)} okText="绑定" cancelText="取消" maskClosable={false} keyboard={false}>
        <Form form={bindForm} layout="vertical">
          <Form.Item name="host_id" label="选择主机" rules={[{ required: true, message: '必选' }]}>
            <Select placeholder="从可用主机里选一台"
              options={availableHosts.map((h) => ({
                value: h.id,
                label: `${h.name} (${h.ip})${h.status === 'online' ? ' ● 在线' : ''}`,
              }))} />
          </Form.Item>
          <Form.Item
            name="service_codes"
            label="选择 Service"
            rules={[{ required: true, message: '至少选择一个 service' }]}
            tooltip="会为每个 service 创建一条 app × host × service 绑定关系；只有一个启用 service 时就是单服务部署。"
          >
            <Select
              mode="multiple"
              placeholder={enabledServices.length === 0 ? '请先在「服务」启用 service' : '选择要部署到这台主机的 service'}
              options={enabledServices.map((s) => ({
                value: s.service_code,
                label: `${s.service_code} · :${s.port}`,
              }))}
            />
          </Form.Item>
          <Form.Item name="port" label="端口覆盖（可选，留空继承 service / 应用默认）">
            <InputNumber min={1} max={65535} style={{ width: 200 }} placeholder={String(app.port)} />
          </Form.Item>
        </Form>
      </Modal>
      <Drawer
        className="runtime-log-drawer"
        title={
          <Space>
            <span>容器运行日志</span>
            {runtimeLog?.container_name && <Tag color="blue">{runtimeLog.container_name}</Tag>}
            {runtimeLog?.status && <Tag>{runtimeLog.status}</Tag>}
          </Space>
        }
        open={logOpen}
        width={920}
        onClose={() => setLogOpen(false)}
        destroyOnClose
        extra={
          <Space>
            <Typography.Text type="secondary">自动刷新</Typography.Text>
            <Switch size="small" checked={logAutoRefresh} onChange={setLogAutoRefresh} />
            <Button size="small" loading={logLoading} onClick={() => fetchRuntimeLog()}>
              刷新
            </Button>
          </Space>
        }
      >
        <div className="drawer-toolbar">
          <Typography.Text type="secondary">
            主机：<code>{logDeployment?.host_ip || '-'}</code>
          </Typography.Text>
          <Typography.Text type="secondary">
            service：<code>{
              runtimeLog?.service_code || logDeployment?.service_code || 'default'
            }</code>
          </Typography.Text>
          <Typography.Text type="secondary">
            当前容器：<code>{runtimeLog?.container_name || logDeployment?.runtime_container_name || '读取中'}</code>
          </Typography.Text>
          <Typography.Text type="secondary">行数</Typography.Text>
          <Select
            size="small"
            value={logLines}
            style={{ width: 110 }}
            onChange={setLogLines}
            options={[
              { value: 100, label: '100 行' },
              { value: 300, label: '300 行' },
              { value: 800, label: '800 行' },
              { value: 1500, label: '1500 行' },
            ]}
          />
          <Typography.Text type="secondary">时间戳</Typography.Text>
          <Switch size="small" checked={logTimestamps} onChange={setLogTimestamps} />
          {runtimeLog?.captured_at && (
            <Typography.Text type="secondary">
              获取时间：{new Date(runtimeLog.captured_at).toLocaleString()}
            </Typography.Text>
          )}
        </div>
        <RuntimeLogBox
          loading={logLoading}
          text={runtimeLog?.logs || (logLoading ? '正在读取远端 docker logs ...' : '暂无日志')}
        />
        <Typography.Paragraph type="secondary" style={{ marginTop: 12 }}>
          这里读取的是远端 <code>docker logs --tail {logLines}</code>。
          如果应用把日志写入容器内文件而不是 stdout/stderr，这里仍然看不到，需要调整 Java 日志输出到 console。
        </Typography.Paragraph>
      </Drawer>
    </section>
  )
}

// ---------- 制品 Tab ----------

function ArtifactTab({ app }: { app: App }) {
  const appId = app.id
  const [list, setList] = useState<Artifact[]>([])
  const [bundles, setBundles] = useState<ArtifactBundle[]>([])
  const [loading, setLoading] = useState(false)
  const [open, setOpen] = useState(false)
  const [form] = Form.useForm()

  // 上传 Modal 状态
  const [upOpen, setUpOpen] = useState(false)
  const [upForm] = Form.useForm()
  const [upFile, setUpFile] = useState<File | null>(null)
  const [upPercent, setUpPercent] = useState(0)
  const [uploading, setUploading] = useState(false)

  // 构建 Modal 状态（Sprint 5.3）
  const [bdOpen, setBdOpen] = useState(false)
  const [bdForm] = Form.useForm()
  const [creds, setCreds] = useState<GitCredential[]>([])
  const [builds, setBuilds] = useState<BuildRun[]>([])
  const [buildsLoading, setBuildsLoading] = useState(false)
  const [logTarget, setLogTarget] = useState<BuildRun | null>(null)
  const [builderEnv, setBuilderEnv] = useState<BuilderEnv | null>(null)
  const [logText, setLogText] = useState('')
  const [logLoading, setLogLoading] = useState(false)

  const refresh = async () => {
    setLoading(true)
    try {
      // Sprint X.6 修复：构建成功后落 ArtifactBundle 表，旧上传/注册落 Artifact 表。
      // 两路并行拉，否则「从仓库构建」产出的 Bundle 在制品 Tab 里会完全看不见。
      const [as, bs] = await Promise.all([
        listArtifacts(appId),
        listBundles(appId),
      ])
      setList(as)
      setBundles(bs)
    } catch (e) { message.error(formatError(e)) }
    finally { setLoading(false) }
  }
  const refreshBuilds = async () => {
    setBuildsLoading(true)
    try { setBuilds(await listBuilds(appId)) }
    catch (e) { message.error(formatError(e)) }
    finally { setBuildsLoading(false) }
  }
  useEffect(() => {
    refresh()
    refreshBuilds()
    getBuilderEnv().then(setBuilderEnv).catch(() => {})
  }, [appId])

  // 有构建在 running → 每 3s 轮询刷新
  useEffect(() => {
    const hasBuilding = builds.some((b) => b.status === 'building')
    if (!hasBuilding) return
    const t = setInterval(() => { refreshBuilds(); refresh() }, 3000)
    return () => clearInterval(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [builds])

  const handleAdd = async () => {
    try {
      const v = await form.validateFields()
      await createArtifact({ app_id: appId, ...v })
      message.success('已注册')
      setOpen(false); refresh()
    } catch (e) {
      if ((e as any)?.errorFields) return
      message.error(formatError(e))
    }
  }
  const handleDelete = (a: Artifact) => {
    Modal.confirm({
      title: `删除制品 ${a.version_tag}？`,
      content: `${a.file_name}（仅删 DB 记录，本地文件不动）`,
      okType: 'danger',
      onOk: async () => {
        try { await deleteArtifact(a.id); message.success('已删除'); refresh() }
        catch (e) { message.error(formatError(e)) }
      },
    })
  }

  const openUpload = () => {
    upForm.resetFields()
    setUpFile(null)
    setUpPercent(0)
    setUpOpen(true)
  }
  const handleUpload = async () => {
    if (uploading) return // 防重入：React 状态尚未刷新前 onOk 二次触发会送空 body 400
    try {
      const v = await upForm.validateFields()
      if (!upFile) { message.warning('请选择要上传的文件'); return }
      setUploading(true); setUpPercent(0)
      await uploadArtifact(appId, v.version_tag, upFile, upFile.name, (p) => setUpPercent(p))
      setUpPercent(100)
      message.success('上传成功')
      setUpOpen(false); refresh()
    } catch (e) {
      if ((e as any)?.errorFields) return
      message.error(formatError(e))
    } finally {
      setUploading(false)
    }
  }

  // 构建：拉远端代码 + mvn package
  const openBuild = async () => {
    try { setCreds(await listGitCreds()) } catch (e) { message.error(formatError(e)) }
    bdForm.resetFields()
    const refs = appBuildRefs(app)
    bdForm.setFieldsValue({
      git_ref: refs[0],
      mvn_args: 'clean package -DskipTests',
      cred_id: 0,
    })
    setBdOpen(true)
  }
  const handleBuild = async () => {
    try {
      const v = await bdForm.validateFields()
      const payload: any = {
        git_ref: v.git_ref || app.git_ref || 'main',
        mvn_args: v.mvn_args || '',
      }
      if (v.cred_id && v.cred_id > 0) payload.cred_id = v.cred_id
      const r = await triggerBuild(appId, payload)
      message.success(`已触发构建：#${r.id}`)
      setBdOpen(false)
      refreshBuilds()
      // 触发后立即打开日志并订阅 WS，避免用户还要手点“日志”才看到实时输出。
      void openLog(r)
    } catch (e) {
      if ((e as any)?.errorFields) return
      message.error(formatError(e))
    }
  }

  // 查看构建日志（Sprint 5.5：building 中优先 WS 实时推，连不上回退 2s 轮询）
  const startLogPolling = (id: number) => {
    const t = setInterval(async () => {
      try {
        const [fresh, freshLog] = await Promise.all([getBuild(id), getBuildLog(id)])
        setLogText(freshLog)
        setLogTarget(fresh)
        if (fresh.status !== 'building') {
          clearInterval(t)
          ;(window as any).__buildLogTimer = null
          refreshBuilds()
          refresh()
        }
      } catch {}
    }, 2000)
    ;(window as any).__buildLogTimer = t
  }
  const openLog = async (b: BuildRun) => {
    setLogTarget(b)
    setLogLoading(true)
    setLogText('')
    try {
      const txt = await getBuildLog(b.id)
      setLogText(txt)
      if (b.status !== 'building') return // 已结束，全文已拿到，无需订阅
      // 实时通道：snapshot 覆盖全量 → log 增量 append → status 终态收尾
      try {
        const { ticket } = await issueWSTicket(`build:${b.id}`)
        const ws = new WebSocket(buildWSURL(`/api/v1/ws/builds/${b.id}`, ticket))
        ;(window as any).__buildLogWS = ws
        ws.onmessage = (ev) => {
          try {
            const e = JSON.parse(ev.data) as BuildWSEvent
            if (e.type === 'snapshot') setLogText(e.log)
            else if (e.type === 'log') setLogText((prev) => prev + e.chunk)
            else if (e.type === 'status') {
              ws.onerror = null // 先摘错误回调，避免主动 close 误触发回退轮询
              // 补全 builder 之后的尾部日志 + 同步顶部状态标签
              Promise.all([getBuild(b.id), getBuildLog(b.id)])
                .then(([fresh, freshLog]) => { setLogTarget(fresh); setLogText(freshLog) })
                .catch(() => {})
              refreshBuilds(); refresh()
              try { ws.close() } catch {}
              ;(window as any).__buildLogWS = null
            }
          } catch {}
        }
        ws.onerror = () => {
          try { ws.close() } catch {}
          ;(window as any).__buildLogWS = null
          startLogPolling(b.id) // 连不上 → 回退轮询
        }
      } catch {
        startLogPolling(b.id)
      }
    } catch (e) {
      message.error(formatError(e))
    } finally { setLogLoading(false) }
  }
  const closeLog = () => {
    const t = (window as any).__buildLogTimer
    if (t) { clearInterval(t); (window as any).__buildLogTimer = null }
    const ws = (window as any).__buildLogWS
    if (ws) { try { ws.onerror = null; ws.onmessage = null; ws.close() } catch {}; (window as any).__buildLogWS = null }
    setLogTarget(null)
    setLogText('')
  }

  // Sprint 5.7：手动清理历史制品（删旧 jar）。被部署/回滚链引用的版本由后端自动跳过。
  const handleCleanup = () => {
    Modal.confirm({
      title: '清理历史制品？',
      content: '将保留最近若干个 Bundle（由服务端 max_history 配置，默认 30），删除更早版本及其 jar 文件。正在被部署 / 回滚链引用的版本会自动跳过。此操作不可恢复。',
      okType: 'danger',
      okText: '确认清理',
      cancelText: '取消',
      onOk: async () => {
        try {
          const r = await cleanupBundleHistory(appId)
          const deleted = r.deleted_bundles?.length ?? 0
          const skipped = r.skipped_in_use?.length ?? 0
          message.success(`清理完成：删除 ${deleted} 组，跳过 ${skipped} 组（在用），释放 ${(r.freed_bytes / 1024 / 1024).toFixed(1)} MB`)
          refresh()
        } catch (e) { message.error(formatError(e)) }
      },
    })
  }

  return (
    <section className="workbench-tab artifact-tab">
      <div className="workbench-toolbar">
        <div className="workbench-toolbar-main">
          <Tooltip title={
            !app.git_url ? '应用未配 git_url' :
            !builderEnv?.valid ? '构建环境未就绪，去「构建环境」配置 java_home / maven_home 并检测' :
            ''
          }>
            <Button type="primary" onClick={openBuild}
              disabled={!app.git_url || !builderEnv?.valid}>
              从仓库构建
            </Button>
          </Tooltip>
          <Button onClick={openUpload}>上传文件</Button>
          <Button onClick={() => { form.resetFields(); setOpen(true) }}>注册路径</Button>
          <Button onClick={() => { refresh(); refreshBuilds(); getBuilderEnv().then(setBuilderEnv).catch(() => {}) }}>刷新</Button>
          {bundles.length > 0 && (
            <Button danger onClick={handleCleanup}>清理历史</Button>
          )}
        </div>
        <div className="workbench-hint">
          {!app.git_url && '应用未配 git_url，构建按钮不可用；先去「应用管理」补上。'}
          {app.git_url && builderEnv && !builderEnv.valid && '构建环境未就绪，去「系统设置」里的构建环境配一下。'}
          {app.git_url && builderEnv?.valid && '构建产物会优先落 Bundle，多 service 场景按整组制品部署。'}
        </div>
      </div>

      {/* 构建历史：仅有记录时显示 */}
      {builds.length > 0 && (
        <Card className="workbench-card" size="small" title={`构建历史（最近 ${Math.min(builds.length, 10)} 条）`}>
          <Table<BuildRun>
            rowKey="id" size="small" pagination={false}
            loading={buildsLoading}
            dataSource={builds.slice(0, 10)}
            columns={[
              { title: '#', dataIndex: 'id', width: 60 },
              { title: 'Ref', dataIndex: 'git_ref', width: 120 },
              { title: 'Commit', dataIndex: 'commit_sha', width: 90,
                render: (s) => s ? <code>{s.substring(0, 7)}</code> : '-' },
              { title: '状态', dataIndex: 'status', width: 100, render: (s) => buildStatusTag(s) },
              { title: '产物', width: 140,
                render: (_, b) =>
                  b.bundle_id ? <Tag color="blue">Bundle #{b.bundle_id}</Tag>
                  : b.artifact_id ? `#${b.artifact_id}`
                  : '-' },
              { title: '触发人', dataIndex: 'triggered_by', width: 110 },
              { title: '开始', dataIndex: 'started_at', width: 170,
                render: (s) => s ? new Date(s).toLocaleString() : '-' },
              { title: '操作', width: 100,
                render: (_, b) => <Button size="small" onClick={() => openLog(b)}>日志</Button> },
            ]}
          />
        </Card>
      )}

      {/* Sprint X.6：整组制品（多 service 构建产出）。bundles 为空时不显示，
          避免对仅用上传/注册的老应用造成视觉噪音。 */}
      {bundles.length > 0 && (
        <Card className="workbench-card" size="small"
          title={`整组制品 Bundle（从仓库构建产出 · 共 ${bundles.length} 组）`}>
          <Table<ArtifactBundle>
            rowKey="id" size="small" pagination={false}
            dataSource={bundles}
            expandable={{
              expandedRowRender: (b) => (
                <Table
                  rowKey="id" size="small" pagination={false}
                  dataSource={b.items ?? []}
                  columns={[
                    { title: 'Service', dataIndex: 'service_code', width: 140,
                      render: (v) => <Tag color="purple">{v}</Tag> },
                    { title: '文件', dataIndex: 'file_name' },
                    { title: '路径', dataIndex: 'file_path', ellipsis: true,
                      render: (v) => <code>{v}</code> },
                    { title: 'Docker Image', dataIndex: 'docker_image', ellipsis: true,
                      render: (v) => v ? <code>{v}</code> : '—' },
                    { title: 'Dockerfile', dataIndex: 'dockerfile_name', width: 160,
                      render: (v) => v ? <Tag>{v}</Tag> : '—' },
                    { title: 'MD5', dataIndex: 'file_md5', width: 280,
                      render: (v) => <code style={{ fontSize: 11 }}>{v}</code> },
                    { title: '大小', dataIndex: 'file_size', width: 100,
                      render: (n) => `${(n/1024).toFixed(1)} KB` },
                  ]}
                />
              ),
              rowExpandable: (b) => (b.items?.length ?? 0) > 0,
            }}
            columns={[
              { title: '#', dataIndex: 'id', width: 60 },
              { title: '版本', dataIndex: 'version_tag', width: 220,
                render: (v) => <Tag color="blue">{v}</Tag> },
              { title: 'Commit', dataIndex: 'git_commit_sha', width: 100,
                render: (s) => s ? <code>{s.substring(0, 7)}</code> : '-' },
              { title: 'Service 数', width: 100,
                render: (_, b) => b.items?.length ?? 0 },
              { title: '触发人', dataIndex: 'triggered_by', width: 110,
                render: (v) => v || '-' },
              { title: '创建', dataIndex: 'created_at', width: 170,
                render: (s) => new Date(s).toLocaleString() },
            ]}
          />
        </Card>
      )}

      <Table<Artifact>
        rowKey="id" loading={loading} dataSource={list} pagination={false}
        locale={{ emptyText: <EmptyState title="还没有制品" description="可以从仓库构建、上传 jar 文件，或者注册本地 jar 路径。" /> }}
        columns={[
          { title: 'ID', dataIndex: 'id', width: 60 },
          { title: '版本', dataIndex: 'version_tag', width: 180, render: (v) => <Tag>{v}</Tag> },
          { title: '文件', dataIndex: 'file_name' },
          { title: '路径', dataIndex: 'file_path', ellipsis: true, render: (v) => <code>{v}</code> },
          { title: 'MD5', dataIndex: 'file_md5', width: 280, render: (v) => <code style={{ fontSize: 11 }}>{v}</code> },
          { title: '大小', dataIndex: 'file_size', width: 100, render: (n) => `${(n/1024).toFixed(1)} KB` },
          { title: '创建', dataIndex: 'created_at', width: 170, render: (s) => new Date(s).toLocaleString() },
          { title: '操作', width: 100, render: (_, a) => <Button danger size="small" onClick={() => handleDelete(a)}>删除</Button> },
        ]}
      />

      <Modal title={`从仓库构建 - ${app.name}`} open={bdOpen}
        onOk={handleBuild} onCancel={() => setBdOpen(false)}
        okText="触发构建" cancelText="取消" width={560}
        maskClosable={false} keyboard={false}>
        <Form form={bdForm} layout="vertical">
          <Typography.Text type="secondary" style={{ display: 'block', marginBottom: 12 }}>
            git_url: <code>{app.git_url}</code>
          </Typography.Text>
          <Form.Item name="git_ref" label="分支 / Tag / Commit" rules={[{ required: true }]}>
            <Select
              showSearch
              options={appBuildRefs(app).map(ref => ({ value: ref, label: ref }))}
              placeholder="选择构建 Ref"
            />
          </Form.Item>
          <Form.Item name="cred_id" label="Git 凭证">
            <Select
              options={[
                { value: 0, label: '— 无凭证（公网 / 走本机 SSH 默认 key）—' },
                ...creds.map((c) => ({
                  value: c.id,
                  label: `${c.name} (${c.type === 'token' ? 'HTTPS Token' : 'SSH Key'})`,
                })),
              ]}
            />
          </Form.Item>
          <Form.Item name="mvn_args" label="Maven 参数（可选）"
            tooltip="留空 = clean package -DskipTests">
            <Input placeholder="clean package -DskipTests -pl module-a -am" />
          </Form.Item>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            构建在 swift-devops 服务器本机进程跑（需装 git + mvn + JDK）。
            成功后自动落 Artifact，version_tag 形如 <code>git-&lt;短SHA&gt;-&lt;BuildID&gt;</code>。
            构建模块 / Jar 匹配请在「服务」里维护。
          </Typography.Text>
        </Form>
      </Modal>

      <Modal title={logTarget ? `构建 #${logTarget.id} 日志` : ''}
        open={!!logTarget} onCancel={closeLog} footer={null}
        width={900} destroyOnClose>
        {logLoading && <Typography.Text type="secondary">加载中…</Typography.Text>}
        {logTarget && (
          <>
            <Descriptions size="small" column={2} style={{ marginBottom: 12 }}>
              <Descriptions.Item label="状态">{buildStatusTag(logTarget.status)}</Descriptions.Item>
              <Descriptions.Item label="Ref">{logTarget.git_ref}</Descriptions.Item>
              <Descriptions.Item label="Commit">
                {logTarget.commit_sha ? <code>{logTarget.commit_sha.substring(0, 7)}</code> : '-'}
              </Descriptions.Item>
              <Descriptions.Item label="产物">
                {logTarget.bundle_id ? `Bundle #${logTarget.bundle_id}` : logTarget.artifact_id ? `#${logTarget.artifact_id}` : '-'}
              </Descriptions.Item>
            </Descriptions>
            {logTarget.error && (
              <Card className="danger-card" size="small" type="inner">
                <Typography.Text type="danger">{logTarget.error}</Typography.Text>
              </Card>
            )}
            <pre className="runtime-log-box build-log-box">
              {logText || '(空)'}
            </pre>
          </>
        )}
      </Modal>

      <Modal title="注册制品（本地 jar 路径）" open={open} onOk={handleAdd} onCancel={() => setOpen(false)} okText="注册" cancelText="取消" maskClosable={false} keyboard={false}>
        <Form form={form} layout="vertical">
          <Form.Item name="version_tag" label="版本标签" rules={[{ required: true }]}>
            <Input placeholder="v1.0.0 / 20260520-01" />
          </Form.Item>
          <Form.Item name="file_path" label="jar 绝对路径" rules={[{ required: true }]}
            extra="必须落在 storage.artifact_dir 之下，否则会被路径白名单拒绝">
            <Input placeholder="/var/lib/swift-devops/artifacts/demo-1.0.0.jar" />
          </Form.Item>
          <Form.Item name="file_name" label="文件名（可选，留空取 basename）">
            <Input placeholder="demo-1.0.0.jar" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title="上传制品"
        open={upOpen}
        onOk={handleUpload}
        onCancel={() => { if (!uploading) setUpOpen(false) }}
        okText={uploading ? '上传中…' : '开始上传'}
        cancelText="取消"
        confirmLoading={uploading}
        maskClosable={false}
        keyboard={false}
        closable={!uploading}
      >
        <Form form={upForm} layout="vertical">
          <Form.Item name="version_tag" label="版本标签" rules={[{ required: true }]}>
            <Input placeholder="v1.0.0" />
          </Form.Item>
          <Form.Item label="文件" required>
            <Upload.Dragger
              multiple={false}
              maxCount={1}
              // 显式禁用 Antd 自带 ajax：即便 beforeUpload 返回 false，少数路径下 Antd 仍会发空 multipart 探测请求
              customRequest={({ onSuccess }) => { setTimeout(() => onSuccess?.({}, new XMLHttpRequest()), 0) }}
              beforeUpload={(f) => { setUpFile(f as File); return false }}
              onRemove={() => { setUpFile(null); return true }}
              fileList={upFile ? [{ uid: '-1', name: upFile.name, status: 'done', size: upFile.size } as any] : []}
              disabled={uploading}
            >
              <p className="ant-upload-drag-icon upload-mark">⬆</p>
              <p className="ant-upload-text">点击或拖拽 jar 文件到此区域</p>
              <p className="ant-upload-hint">单文件，最大受服务端 storage.max_upload_mb 限制</p>
            </Upload.Dragger>
          </Form.Item>
          {(uploading || upPercent > 0) && (
            <Progress percent={upPercent} status={uploading ? 'active' : 'success'} />
          )}
        </Form>
      </Modal>
    </section>
  )
}

// ---------- 部署历史 Tab ----------

function PipelineTab({ app }: { app: App }) {
  const [list, setList] = useState<PipelineRun[]>([])
  const [arts, setArts] = useState<Artifact[]>([])
  const [bundles, setBundles] = useState<ArtifactBundle[]>([])
  const [loading, setLoading] = useState(false)
  const [open, setOpen] = useState(false)
  const [form] = Form.useForm()
  const [drawerRun, setDrawerRun] = useState<PipelineRun | null>(null)

  const refresh = async () => {
    setLoading(true)
    try {
      const [ps, as, bs] = await Promise.all([
        listPipelines(app.id),
        listArtifacts(app.id),
        listBundles(app.id),
      ])
      setList(ps); setArts(as); setBundles(bs)
    } catch (e) {
      message.error(formatError(e))
    } finally { setLoading(false) }
  }
  useEffect(() => { refresh() }, [app.id])

  // 简单轮询：有 running 的 pipeline 时每 3s 刷一次
  useEffect(() => {
    const hasRunning = list.some((r) => r.status === 'running')
    if (!hasRunning) return
    const t = setInterval(refresh, 3000)
    return () => clearInterval(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [list])

  // drawer 内若 run 仍在跑，自动刷新（WS 失败时的兜底）
  // 实时事件优先由 PipelineDetailDrawer 内部 WS 处理；这里只做兜底防止 WS 完全断开
  useEffect(() => {
    if (!drawerRun || drawerRun.status !== 'running') return
    const t = setInterval(async () => {
      try { setDrawerRun(await getPipeline(drawerRun.id)) } catch {}
    }, 8000)
    return () => clearInterval(t)
  }, [drawerRun])

  const handleDeploy = async () => {
    try {
      const v = await form.validateFields()
      const strategy = (v.strategy ?? 'single') as 'single' | 'rolling'
      const batchSize = strategy === 'rolling' ? Number(v.batch_size ?? 1) : undefined
      const sourceType = (v.source_type ?? 'bundle') as 'bundle' | 'artifact'
      const source = sourceType === 'bundle'
        ? { bundle_id: Number(v.bundle_id) }
        : { artifact_id: Number(v.artifact_id) }
      const run = await deployApp(app.id, source, strategy, batchSize)
      const detail = strategy === 'rolling' ? `, batch=${batchSize}` : ''
      message.success(`已触发：#${run.id}（${strategy}${detail}）`)
      setOpen(false); setDrawerRun(run); refresh()
    } catch (e) {
      if ((e as any)?.errorFields) return
      message.error(formatError(e))
    }
  }

  const rollbackTargetLabel = (r: PipelineRun) => {
    const bundleID = r.bundle_id || r.previous_bundle_id
    if (bundleID) return `Bundle #${bundleID}`
    if (r.artifact_id) return `Artifact #${r.artifact_id}`
    return '该版本'
  }

  const canRollbackToRun = (r: PipelineRun) =>
    r.status === 'success' && Boolean(r.bundle_id || r.previous_bundle_id || r.artifact_id)

  const handleRollbackToRun = (r: PipelineRun) => {
    const label = rollbackTargetLabel(r)
    Modal.confirm({
      title: `回滚 ${app.name} 到 ${label}？`,
      content: (
        <>
          <div>将根据部署历史 <code>#{r.id}</code> 重新部署它对应的成功版本。</div>
          <div style={{ marginTop: 8 }}>
            <Typography.Text type="secondary">
              只有状态=成功且关联了 Bundle / Artifact 的历史才能作为回滚目标；本操作会新建一条 rollback 流水线。
            </Typography.Text>
          </div>
        </>
      ),
      okType: 'danger',
      okText: '确认回滚到此版本',
      cancelText: '取消',
      onOk: async () => {
        try {
          const run = await rollbackPipelineRun(r.id)
          message.success(`已触发回滚到 ${label}：#${run.id}`)
          setDrawerRun(run); refresh()
        } catch (e) {
          message.error(formatError(e))
        }
      },
    })
  }

  return (
    <section className="workbench-tab pipeline-tab">
      <div className="workbench-toolbar">
        <div className="workbench-toolbar-main">
          <Button type="primary" disabled={arts.length === 0 && bundles.length === 0}
            onClick={() => {
              form.resetFields()
              const defaultSource = bundles.length > 0 ? 'bundle' : 'artifact'
              form.setFieldsValue({
                strategy: 'single', batch_size: 2,
                source_type: defaultSource,
                bundle_id: bundles[0]?.id,
                artifact_id: arts[0]?.id,
              })
              setOpen(true)
            }}>
            触发部署
          </Button>
          <Button onClick={refresh}>刷新</Button>
        </div>
        <div className="workbench-hint">
          {arts.length === 0 && bundles.length === 0
            ? '先去「构建」注册 jar 或触发构建产出 Bundle，再来部署。'
            : '选择状态=成功的部署历史，点击“回滚到此版本”。'}
        </div>
      </div>
      <Table<PipelineRun>
        rowKey="id" loading={loading} dataSource={list} pagination={false}
        locale={{ emptyText: <EmptyState title="还没有部署记录" description="先选择一个 Bundle 或 Artifact 触发部署，成功记录才能作为回滚目标。" /> }}
        columns={[
          { title: '#', dataIndex: 'id', width: 60 },
          { title: '策略', dataIndex: 'strategy', width: 90, render: (v) => <Tag>{v}</Tag> },
          {
            title: '产物', width: 180,
            render: (_: any, r: PipelineRun) => {
              const version = r.bundle_id
                ? <Tag color="blue">Bundle #{r.bundle_id}</Tag>
                : r.previous_bundle_id
                  ? <Tag color="purple">目标 Bundle #{r.previous_bundle_id}</Tag>
                  : r.artifact_id
                    ? <Tag>Art #{r.artifact_id}</Tag>
                    : null
              if (!version) return '-'
              return (
                <Space size={4} wrap>
                  {version}
                  {r.is_current && (
                    <Tag color={r.current_partial ? 'gold' : 'green'}>
                      {r.current_partial ? '当前部分部署' : '当前部署'}
                    </Tag>
                  )}
                </Space>
              )
            },
          },
          { title: '状态', dataIndex: 'status', width: 110, render: (s) => pipeStatusTag(s) },
          { title: '触发人', dataIndex: 'triggered_by', width: 110 },
          { title: '开始', dataIndex: 'started_at', width: 170, render: (s) => s ? new Date(s).toLocaleString() : '-' },
          { title: '结束', dataIndex: 'finished_at', width: 170, render: (s) => s ? new Date(s).toLocaleString() : '-' },
          {
            title: '操作',
            width: 210,
            render: (_, r) => {
              const canRollback = canRollbackToRun(r)
              return (
                <Space size={8}>
                  <Button size="small" onClick={() => setDrawerRun(r)}>详情</Button>
                  <Tooltip title={canRollback ? `回滚到 ${rollbackTargetLabel(r)}` : '只有状态=成功且关联了制品版本的历史才能回滚'}>
                    <span>
                      <Button size="small" danger disabled={!canRollback} onClick={() => handleRollbackToRun(r)}>
                        回滚到此版本
                      </Button>
                    </span>
                  </Tooltip>
                </Space>
              )
            },
          },
        ]}
      />
      <Modal title={`触发部署 - ${app.name}`} open={open} onOk={handleDeploy} onCancel={() => setOpen(false)} okText="触发" cancelText="取消" maskClosable={false} keyboard={false} width={600}>
        <Form form={form} layout="vertical" initialValues={{ strategy: 'single', batch_size: 2, source_type: 'bundle' }}>
          <Form.Item name="source_type" label="产物来源" rules={[{ required: true }]}>
            <Select
              options={[
                { value: 'bundle', label: 'Bundle（多 service 整组制品 · X.6 推荐）', disabled: bundles.length === 0 },
                { value: 'artifact', label: 'Artifact（单 jar · 老链路兼容）', disabled: arts.length === 0 },
              ]}
            />
          </Form.Item>
          <Form.Item
            noStyle
            shouldUpdate={(prev, cur) => prev.source_type !== cur.source_type}
          >
            {({ getFieldValue }) => {
              const t = getFieldValue('source_type')
              if (t === 'bundle') {
                return (
                  <Form.Item name="bundle_id" label="选择 Bundle" rules={[{ required: true }]}>
                    <Select placeholder="选择整组制品"
                      options={bundles.map((b) => ({
                        value: b.id,
                        label: `#${b.id}  ${b.version_tag}  (${b.items?.length ?? 0} services, ${b.git_commit_sha?.slice(0, 7) || ''})`,
                      }))} />
                  </Form.Item>
                )
              }
              return (
                <Form.Item name="artifact_id" label="选择制品" rules={[{ required: true }]}>
                  <Select placeholder="选择要部署的版本"
                    options={arts.map((a) => ({
                      value: a.id,
                      label: `#${a.id}  ${a.version_tag}  (${a.file_name})`,
                    }))} />
                </Form.Item>
              )
            }}
          </Form.Item>
          <Form.Item name="strategy" label="部署策略" rules={[{ required: true }]}>
            <Select
              options={[
                { value: 'single', label: 'single（顺序逐台、任一失败立刻停止）' },
                { value: 'rolling', label: 'rolling（分批并行、批级 fail-fast）' },
              ]}
            />
          </Form.Item>
          <Form.Item
            noStyle
            shouldUpdate={(prev, cur) => prev.strategy !== cur.strategy}
          >
            {({ getFieldValue }) => {
              const strategy = getFieldValue('strategy')
              if (strategy === 'rolling') {
                return (
                  <Form.Item
                    name="batch_size"
                    label="批大小（每批同时部署的主机数）"
                    rules={[{ required: true, message: 'rolling 必须指定 batch_size' }]}
                  >
                    <InputNumber min={1} max={50} style={{ width: 200 }} placeholder="2" />
                  </Form.Item>
                )
              }
              return null
            }}
          </Form.Item>
        </Form>
      </Modal>
      <PipelineDetailDrawer run={drawerRun} onClose={() => setDrawerRun(null)} />
    </section>
  )
}

// ---------- 流水线详情抽屉 ----------

const stageLabel: Record<PipelineStage, string> = {
  dial: 'SSH 拨号',
  env_check: '远端环境预检',
  upload: '上传产物',
  write_unit: '准备运行单元',
  restart: '重启服务',
  health: '健康探针',
  nginx_apply: '历史切流步骤',
}

function stageTitle(step: StepResult): string {
  const text = `${step.detail ?? ''}\n${step.error ?? ''}`.toLowerCase()
  const modeMatch = text.match(/mode=(docker|nohup|systemd)/)
  const mode = modeMatch?.[1]

  if (step.stage === 'upload') {
    if (text.includes('dockerfile')) return '上传 Dockerfile'
    if (text.includes('skip jar upload')) return '跳过 jar 上传'
    return '上传产物'
  }

  if (step.stage === 'write_unit') {
    if (mode === 'docker' || text.includes('docker container')) return '准备 Docker 容器'
    if (mode === 'nohup' || text.includes('start.sh') || text.includes('stop.sh')) return '写 nohup 脚本'
    if (mode === 'systemd' || text.includes('.service')) return '写 systemd unit'
    return stageLabel[step.stage] ?? step.stage
  }

  if (step.stage === 'restart') {
    if (mode === 'docker') return '重启 Docker 容器'
    if (mode === 'nohup') return '重启 nohup 进程'
    if (mode === 'systemd') return 'systemctl restart'
    return stageLabel[step.stage] ?? step.stage
  }

  return stageLabel[step.stage] ?? step.stage
}

function StepLogText({ text, danger = false }: { text?: string; danger?: boolean }) {
  if (!text) return null
  const multiLine = text.includes('\n')
  return (
    <Typography.Text
      type={danger ? 'danger' : 'secondary'}
      className={`step-log-text ${multiLine ? 'step-log-text--block' : ''} ${danger ? 'step-log-text--danger' : ''}`}
    >
      {text}
    </Typography.Text>
  )
}

function RuntimeLogBox({ text, loading = false }: { text?: string; loading?: boolean }) {
  return <LogTerminal text={text} loading={loading} />
}

function PipelineDetailDrawer({ run, onClose }: { run: PipelineRun | null; onClose: () => void }) {
  // 当 WS snapshot/step 事件到达后，用本地 state 覆盖 props.run 的快照与状态
  const [liveSnap, setLiveSnap] = useState<RunSnapshot | null>(null)
  const [liveStatus, setLiveStatus] = useState<PipelineRun['status'] | null>(null)
  const [wsState, setWsState] = useState<'idle' | 'connecting' | 'open' | 'closed' | 'error'>('idle')
  const wsRef = useRef<WebSocket | null>(null)

  // 切换 run 时重置实时状态
  useEffect(() => {
    setLiveSnap(null)
    setLiveStatus(null)
    setWsState('idle')
  }, [run?.id])

  // 建立 WebSocket：拿 ticket → 升级 → onmessage 累积事件
  useEffect(() => {
    if (!run) return
    let cancelled = false
    setWsState('connecting')

    ;(async () => {
      try {
        const { ticket } = await issueWSTicket(`pipeline:${run.id}`)
        if (cancelled) return
        const url = buildWSURL(`/api/v1/ws/pipelines/${run.id}`, ticket)
        const ws = new WebSocket(url)
        wsRef.current = ws

        ws.onopen = () => { if (!cancelled) setWsState('open') }
        ws.onerror = () => { if (!cancelled) setWsState('error') }
        ws.onclose = () => { if (!cancelled) setWsState((s) => (s === 'error' ? s : 'closed')) }
        ws.onmessage = (ev) => {
          if (cancelled) return
          try {
            const e: PipelineWSEvent = JSON.parse(ev.data)
            if (e.type === 'snapshot') {
              setLiveSnap(e.snapshot)
              setLiveStatus(e.status)
            } else if (e.type === 'status') {
              setLiveStatus(e.status)
            } else if (e.type === 'step') {
              setLiveSnap((prev) => {
                const base: RunSnapshot = prev ?? {
                  strategy: run.strategy, artifact_id: run.artifact_id,
                  started_at: run.started_at ?? '', steps: [],
                }
                return { ...base, steps: [...(base.steps ?? []), e.step] }
              })
            }
          } catch {
            // ignore malformed frame
          }
        }
      } catch (err) {
        if (!cancelled) {
          setWsState('error')
          // ticket 失败一般是 401，提示一下
          message.error(`实时通道连接失败：${formatError(err)}（已回退轮询）`)
        }
      }
    })()

    return () => {
      cancelled = true
      const ws = wsRef.current
      wsRef.current = null
      if (ws && ws.readyState <= WebSocket.OPEN) ws.close()
    }
  }, [run?.id])

  // 优先用 live snapshot，否则回退到 run.state_snapshot（适合首屏未连 WS 的瞬间）
  const snap: RunSnapshot | null = useMemo(() => {
    if (liveSnap) return liveSnap
    if (!run?.state_snapshot) return null
    try { return JSON.parse(run.state_snapshot) } catch { return null }
  }, [liveSnap, run?.state_snapshot])

  const displayStatus = liveStatus ?? run?.status ?? 'pending'

  // 按 host 聚合 step
  const byHost = useMemo(() => {
    if (!snap?.steps) return new Map<number, StepResult[]>()
    const m = new Map<number, StepResult[]>()
    for (const s of snap.steps) {
      if (!m.has(s.host_id)) m.set(s.host_id, [])
      m.get(s.host_id)!.push(s)
    }
    return m
  }, [snap])

  const handleCancel = () => {
    if (!run) return
    Modal.confirm({
      title: `取消部署 #${run.id}？`,
      content: '已开始的主机会跑完当前阶段，未开始的主机标 skipped，最终 status 标 cancelled。',
      okType: 'danger',
      okText: '确认取消',
      cancelText: '不取消',
      onOk: async () => {
        try {
          await cancelPipeline(run.id)
          message.success('已请求取消，等待执行线程退出…')
        } catch (e) {
          message.error(formatError(e))
        }
      },
    })
  }

  return (
    <Drawer
      className="pipeline-detail-drawer"
      title={run ? `部署详情 #${run.id}` : ''}
      open={!!run}
      onClose={onClose}
      width={720}
      extra={
        <Space>
          {displayStatus === 'running' && (
            <Button danger size="small" onClick={handleCancel}>取消部署</Button>
          )}
          <WSStatusTag state={wsState} />
        </Space>
      }
    >
      {run && (
        <>
          <Descriptions size="small" column={2} style={{ marginBottom: 16 }}>
            <Descriptions.Item label="状态">{pipeStatusTag(displayStatus)}</Descriptions.Item>
            <Descriptions.Item label="策略">{run.strategy}</Descriptions.Item>
            <Descriptions.Item label="制品">
              {run.bundle_id
                ? <Tag color="blue">Bundle #{run.bundle_id}</Tag>
                : run.previous_bundle_id
                  ? <Tag color="purple">Prev Bundle #{run.previous_bundle_id}</Tag>
                  : run.artifact_id
                    ? <Tag>Art #{run.artifact_id}</Tag>
                    : '-'}
            </Descriptions.Item>
            <Descriptions.Item label="触发人">{run.triggered_by}</Descriptions.Item>
            <Descriptions.Item label="开始">{run.started_at ? new Date(run.started_at).toLocaleString() : '-'}</Descriptions.Item>
            <Descriptions.Item label="结束">{run.finished_at ? new Date(run.finished_at).toLocaleString() : '-'}</Descriptions.Item>
          </Descriptions>
          {snap?.error && (
            <Card className="danger-card" size="small" type="inner">
              <Typography.Text type="danger">{snap.error}</Typography.Text>
            </Card>
          )}
          {Array.from(byHost.entries()).map(([hostId, steps]) => (
            <Card key={hostId} className="pipeline-host-card" size="small" type="inner"
              title={<><Badge status={steps.every((s) => s.ok) ? 'success' : 'error'} />
                {steps[0]?.host_name}（<code>{steps[0]?.host_ip}</code>）</>}>
              <Steps direction="vertical" size="small" current={steps.length - 1}
                items={steps.map((s) => ({
                  title: stageTitle(s),
                  status: s.ok ? 'finish' : 'error',
                  description: (
                    <>
                      {s.detail && <StepLogText text={s.detail} />}
                      {s.error && <StepLogText danger text={`✗ ${s.error}`} />}
                    </>
                  ),
                }))} />
            </Card>
          ))}
          {(!snap || (snap.steps?.length ?? 0) === 0) && (
            <Typography.Text type="secondary">还没有步骤记录{displayStatus === 'running' ? '，正在执行…' : ''}</Typography.Text>
          )}
        </>
      )}
    </Drawer>
  )
}

function WSStatusTag({ state }: { state: 'idle' | 'connecting' | 'open' | 'closed' | 'error' }) {
  if (state === 'open') return <StatusTag tone="success">实时</StatusTag>
  if (state === 'connecting') return <StatusTag tone="processing">连接中</StatusTag>
  if (state === 'error') return <StatusTag tone="danger">连接失败（轮询兜底）</StatusTag>
  if (state === 'closed') return <StatusTag>已断开</StatusTag>
  return null
}

// ---------- 服务配置 Tab (Sprint X.4) ----------
//
// 列表 + 新建/编辑/删除。旧应用会自动补一行 service_code="default"。
// 多 service 应用通过这里加 N 行（每个可部署模块一个 AppService）。
type DiscoverServiceRow = AppServiceSuggestion & { key: string }

function ServicesTab({
  appId,
  app,
  onServicesChange,
}: {
  appId: number
  app: App
  onServicesChange?: (items: AppService[]) => void
}) {
  const [items, setItems] = useState<AppService[]>([])
  const [loading, setLoading] = useState(false)
  const [editing, setEditing] = useState<AppService | null>(null)
  const [creating, setCreating] = useState(false)
  const [form] = Form.useForm<AppServiceInput>()
  const [discoverOpen, setDiscoverOpen] = useState(false)
  const [discoverLoading, setDiscoverLoading] = useState(false)
  const [importLoading, setImportLoading] = useState(false)
  const [discoverRows, setDiscoverRows] = useState<DiscoverServiceRow[]>([])
  const [selectedDiscoverKeys, setSelectedDiscoverKeys] = useState<string[]>([])
  const [dockerfiles, setDockerfiles] = useState<DockerfileTemplate[]>([])
  const [dfEditing, setDfEditing] = useState<DockerfileTemplate | null>(null)
  const [dfCreating, setDfCreating] = useState(false)
  const [dfForm] = Form.useForm<DockerfileTemplateFormValues>()
  const inheritedDeployMode = app.deploy_mode || 'systemd'
  const renderRuntimeConfig = () => <Tag color="processing">继承应用：{inheritedDeployMode}</Tag>

  const refresh = async () => {
    setLoading(true)
    try {
      const [svcRows, dfRows] = await Promise.all([
        listAppServices(appId),
        listDockerfileTemplates(appId),
      ])
      setItems(svcRows)
      setDockerfiles(dfRows)
      onServicesChange?.(svcRows)
    } catch (e) {
      message.error(formatError(e))
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    refresh()
  }, [appId])

  const openCreate = () => {
    setCreating(true); setEditing(null)
    form.resetFields()
    form.setFieldsValue({
      service_code: '', name: '',
      port: app.port,
      systemd_user: '',
      java_path: '',
      startup_order: 100,
      optional: false, enabled: true,
      dockerfile_template_id: dockerfiles.find(d => d.is_default)?.id || dockerfiles[0]?.id || 0,
    })
  }
  const openEdit = (row: AppService) => {
    setEditing(row); setCreating(false)
    form.resetFields()
    form.setFieldsValue({
      service_code: row.service_code, name: row.name,
      build_module: row.build_module, build_jar_pattern: row.build_jar_pattern,
      port: row.port,
      systemd_user: row.systemd_user, java_path: row.java_path,
      dockerfile_template_id: row.dockerfile_template_id || dockerfiles.find(d => d.is_default)?.id || 0,
      startup_order: row.startup_order, optional: row.optional, enabled: row.enabled,
    })
  }
  const closeModal = () => { setCreating(false); setEditing(null); form.resetFields() }

  const onSubmit = async () => {
    try {
      const values = await form.validateFields()
      const preservedValues: Partial<AppServiceInput> = editing ? {
        service_code: editing.service_code,
        name: editing.name,
        build_module: editing.build_module,
        build_jar_pattern: editing.build_jar_pattern,
        port: editing.port,
        systemd_user: editing.systemd_user,
        java_path: editing.java_path,
        startup_order: editing.startup_order,
        optional: editing.optional,
        enabled: editing.enabled,
        dockerfile_template_id: editing.dockerfile_template_id,
      } : {}
      const payload = { ...preservedValues, ...values }
      if (editing) {
        await updateAppService(editing.id, payload)
        message.success('已更新')
      } else {
        await createAppService(appId, payload as AppServiceInput)
        message.success('已创建')
      }
      closeModal()
      await refresh()
    } catch (e: any) {
      if (e?.errorFields) return // 表单校验
      message.error(formatError(e))
    }
  }

  const onDelete = (row: AppService) => {
    Modal.confirm({
      title: `删除 service ${row.service_code}?`,
      content: '若仍被 deployment 绑定将拒绝。',
      okType: 'danger', okText: '删除',
      onOk: async () => {
        try {
          await deleteAppService(row.id)
          message.success('已删除')
          await refresh()
        } catch (e) {
          message.error(formatError(e))
        }
      },
    })
  }

  const updateDiscoverRow = (key: string, patch: Partial<DiscoverServiceRow>) => {
    setDiscoverRows((rows) => rows.map((row) => row.key === key ? { ...row, ...patch } : row))
  }

  const openDiscover = async () => {
    setDiscoverOpen(true)
    setDiscoverLoading(true)
    setDiscoverRows([])
    setSelectedDiscoverKeys([])
    try {
      const rows = await discoverAppServices(appId, { git_ref: app.git_ref || undefined })
      const withKey = rows.map((row, idx) => ({ ...row, key: `${row.service_code}-${idx}` }))
      setDiscoverRows(withKey)
      setSelectedDiscoverKeys(withKey.filter((row) => row.recommended).map((row) => row.key))
      if (withKey.length === 0) {
        message.warning('没有扫描到可导入的 Maven service')
      }
    } catch (e) {
      message.error(formatError(e))
    } finally {
      setDiscoverLoading(false)
    }
  }

  const submitDiscoverImport = async () => {
    const selected = discoverRows.filter((row) => selectedDiscoverKeys.includes(row.key))
    if (selected.length === 0) {
      message.warning('请至少选择一个 service')
      return
    }
    setImportLoading(true)
    try {
      const defaultTpl = dockerfiles.find(d => d.is_default)?.id || dockerfiles[0]?.id || 0
      const payload: AppServiceInput[] = selected.map((row) => ({
        service_code: row.service_code,
        name: row.name || row.service_code,
        build_module: row.build_module,
        build_jar_pattern: row.build_jar_pattern,
        port: row.port || app.port,
        dockerfile_template_id: defaultTpl,
        startup_order: row.startup_order || 100,
        optional: row.optional,
        enabled: row.enabled,
      }))
      const result = await batchImportAppServices(appId, payload, { upsert: true, disable_default: true })
      message.success(`导入完成：新增 ${result.created}，更新 ${result.updated}，跳过 ${result.skipped}`)
      setDiscoverOpen(false)
      await refresh()
    } catch (e) {
      message.error(formatError(e))
    } finally {
      setImportLoading(false)
    }
  }

  const openDockerfileCreate = () => {
    setDfCreating(true); setDfEditing(null)
    dfForm.resetFields()
    const content = buildDockerfileFromFields(defaultDockerfileFields)
    dfForm.setFieldsValue({
      name: 'dockerfile-jdk21',
      description: 'Java 21 Spring Boot 默认模板',
      ...defaultDockerfileFields,
      content,
      is_default: dockerfiles.length === 0,
    })
  }
  const openDockerfileEdit = (row: DockerfileTemplate) => {
    setDfEditing(row); setDfCreating(false)
    dfForm.resetFields()
    dfForm.setFieldsValue({
      name: row.name,
      description: row.description,
      ...parseDockerfileFields(row.content),
      content: row.content,
      is_default: row.is_default,
    })
  }
  const generateDockerfilePreview = () => {
    const content = buildDockerfileFromFields(dfForm.getFieldsValue())
    dfForm.setFieldsValue({ content })
  }
  const closeDockerfileModal = () => { setDfCreating(false); setDfEditing(null); dfForm.resetFields() }
  const submitDockerfile = async () => {
    try {
      const v = await dfForm.validateFields()
      const payload: DockerfileTemplateInput = {
        name: v.name,
        description: v.description,
        content: v.content,
        is_default: v.is_default,
      }
      if (dfEditing) {
        await updateDockerfileTemplate(dfEditing.id, payload)
        message.success('Dockerfile 模板已更新')
      } else {
        await createDockerfileTemplate(appId, payload)
        message.success('Dockerfile 模板已创建')
      }
      closeDockerfileModal()
      await refresh()
    } catch (e: any) {
      if (e?.errorFields) return
      message.error(formatError(e))
    }
  }
  const removeDockerfile = (row: DockerfileTemplate) => {
    Modal.confirm({
      title: `删除 Dockerfile 模板 ${row.name}?`,
      content: '若已有 service 绑定该模板，后端会拒绝删除。',
      okType: 'danger', okText: '删除',
      onOk: async () => {
        try {
          await deleteDockerfileTemplate(row.id)
          message.success('已删除')
          await refresh()
        } catch (e) {
          message.error(formatError(e))
        }
      },
    })
  }
  const serviceMode = describeServiceMode(items)

  return (
    <section className="workbench-tab services-tab">
      <Card
        className="workbench-card"
        title="Dockerfile 模板"
        size="small"
        extra={
          <Space>
            <Button size="small" onClick={async () => { await ensureDefaultDockerfileTemplate(appId); await refresh(); message.success('已确认默认模板') }}>
              初始化默认模板
            </Button>
            <Button size="small" type="primary" onClick={openDockerfileCreate}>+ 新建模板</Button>
          </Space>
        }
      >
        <Typography.Paragraph type="secondary" style={{ marginBottom: 12 }}>
          支持变量：<code>{'{{JAR_FILE}}'}</code> / <code>{'{{PORT}}'}</code> / <code>{'{{APP_CODE}}'}</code> / <code>{'{{SERVICE_CODE}}'}</code>。
          Service 绑定模板后，构建会把 Dockerfile 快照写入制品；remote-docker 部署会把 jar 与 Dockerfile 一起推到目标机。
        </Typography.Paragraph>
        <Table
          rowKey="id"
          size="small"
          dataSource={dockerfiles}
          pagination={false}
          locale={{ emptyText: <EmptyState title="还没有 Dockerfile 模板" description="初始化默认模板后，可按 service 绑定不同构建模板。" /> }}
          columns={[
            { title: '模板名称', dataIndex: 'name', render: (v: string, row: DockerfileTemplate) => <Space><code>{v}</code>{row.is_default && <Tag color="blue">default</Tag>}</Space> },
            { title: '说明', dataIndex: 'description', render: (v: string) => v || '—' },
            {
              title: '操作', width: 160,
              render: (_: any, row: DockerfileTemplate) => (
                <Space size={4}>
                  <Button size="small" onClick={() => openDockerfileEdit(row)}>编辑</Button>
                  <Button size="small" danger onClick={() => removeDockerfile(row)}>删除</Button>
                </Space>
              ),
            },
          ]}
        />
      </Card>

      <Card
        className="workbench-card"
        title="服务列表"
        size="small"
        extra={<Button type="primary" loading={discoverLoading} onClick={openDiscover}>扫描 Maven 模块</Button>}
      >
        <Space style={{ marginBottom: 12, flexWrap: 'wrap' }}>
          <Tag color={serviceMode.color}>当前：{serviceMode.label}</Tag>
          <Typography.Text type="secondary">{serviceMode.desc}</Typography.Text>
        </Space>
        <Typography.Paragraph type="secondary" style={{ marginBottom: 12 }}>
          应用统一按 service 管理：一个启用 service 就是单服务模式；扫描并导入多个 Maven 模块后就是多服务模式。
          Dockerfile / 镜像构建细节请在上方模板里维护，部署按 <strong>startup_order</strong> 分波，同波内并发。
        </Typography.Paragraph>
        <Table
        rowKey="id"
        size="small"
        loading={loading}
        dataSource={items}
        pagination={false}
        locale={{ emptyText: <EmptyState title="还没有 service 配置" description="点击扫描 Maven 模块，或手动新建 service。" /> }}
        columns={[
          { title: 'service_code', dataIndex: 'service_code', render: (v: string) => <code>{v}</code> },
          { title: '展示名', dataIndex: 'name' },
          { title: 'Port', dataIndex: 'port', width: 80 },
          { title: 'startup', dataIndex: 'startup_order', width: 80 },
          { title: 'module', dataIndex: 'build_module', render: (v: string) => v || '—' },
          {
            title: 'Dockerfile',
            dataIndex: 'dockerfile_template_id',
            render: (v: number) => dockerfiles.find(d => d.id === v)?.name || <Typography.Text type="secondary">默认</Typography.Text>,
          },
          {
            title: '运行配置',
            width: 150,
            render: renderRuntimeConfig,
          },
          {
            title: '状态', width: 130,
            render: (_: any, row: AppService) => (
              <Space size={4}>
                {row.enabled ? <Tag color="green">启用</Tag> : <Tag>停用</Tag>}
                {row.optional && <Tag color="orange">optional</Tag>}
              </Space>
            ),
          },
          {
            title: '操作', width: 160,
            render: (_: any, row: AppService) => (
              <Space size={4}>
                <Button size="small" onClick={() => openEdit(row)}>编辑</Button>
                <Button size="small" danger onClick={() => onDelete(row)}>删除</Button>
              </Space>
            ),
          },
        ]}
      />

      <Modal
        title={editing ? `编辑 service ${editing.service_code}` : '新建 service'}
        open={creating || !!editing}
        onCancel={closeModal}
        onOk={onSubmit}
        okText={editing ? '保存' : '创建'}
        width={680}
      >
        <Form form={form} layout="vertical" size="small">
          <Form.Item
            name="service_code" label="service_code"
            rules={[{ required: true, pattern: /^[a-z][a-z0-9-]{1,49}$/,
              message: '小写字母开头，2-50 位，仅小写字母/数字/连字符' }]}
            tooltip="systemd unit / Docker 容器命名的一部分，创建后不可改"
          >
            <Input placeholder="default / gateway / user-service" disabled={!!editing} />
          </Form.Item>
          <Form.Item name="name" label="展示名">
            <Input placeholder="留空走 service_code" />
          </Form.Item>
          <Space style={{ display: 'flex' }} align="start">
            <Form.Item name="port" label="Port" rules={[{ required: true }]} style={{ flex: 1 }}>
              <InputNumber min={1} max={65535} style={{ width: '100%' }} />
            </Form.Item>
            <Form.Item name="startup_order" label="startup_order" style={{ flex: 1 }}
              tooltip="同值的 service 同波并发；小先起。注册中心 0，网关 10，业务 100"
            >
              <InputNumber min={0} max={9999} style={{ width: '100%' }} />
            </Form.Item>
          </Space>
          <Typography.Paragraph type="secondary" style={{ marginBottom: 12 }}>
            健康检查、JVM 参数、环境变量、部署方式和 Docker 配置都由应用的「运行配置」统一管理。
            service 这里只维护模块、端口、启动顺序、Dockerfile 模板，以及必要的账号路径覆盖。
          </Typography.Paragraph>
          <Form.Item name="build_module" label="build_module" tooltip="multi-module 项目用，mvn -pl；根模块可留空">
            <Input placeholder="user-service；根模块 service 可留空" />
          </Form.Item>
          <Form.Item name="build_jar_pattern" label="build_jar_pattern" tooltip="glob 在 workspace 下匹配 jar">
            <Input placeholder="user-service/target/*.jar；根模块 service 可用 target/*.jar" />
          </Form.Item>
          <Form.Item
            name="dockerfile_template_id"
            label="Dockerfile 模板"
            tooltip="local-docker：本机 docker build 使用该模板；remote-docker：部署时与 jar 一起上传到目标机 build。留空则使用 app 默认模板。"
          >
            <Select
              allowClear
              placeholder="使用 app 默认模板"
              options={dockerfiles.map(d => ({ value: d.id, label: `${d.name}${d.is_default ? '（default）' : ''}` }))}
            />
          </Form.Item>
          <Space style={{ display: 'flex' }} align="start">
            <Form.Item name="systemd_user" label="systemd User 覆盖" style={{ flex: 1 }}>
              <Input placeholder="留空 = 沿用应用运行用户" />
            </Form.Item>
            <Form.Item name="java_path" label="JavaPath 覆盖" style={{ flex: 1 }}>
              <Input placeholder="留空 = 沿用应用 / 主机 JavaPath" />
            </Form.Item>
          </Space>
          <Space style={{ display: 'flex' }}>
            <Form.Item name="optional" label="Optional" valuePropName="checked" style={{ flex: 1 }}
              tooltip="勾上则该 service 失败不阻塞整组部署"
            >
              <Input type="checkbox" />
            </Form.Item>
            <Form.Item name="enabled" label="Enabled" valuePropName="checked" style={{ flex: 1 }}>
              <Input type="checkbox" />
            </Form.Item>
          </Space>
        </Form>
      </Modal>

      <Modal
        title="扫描 Maven 模块并导入 Service"
        open={discoverOpen}
        onCancel={() => setDiscoverOpen(false)}
        onOk={submitDiscoverImport}
        okText="导入选中"
        confirmLoading={importLoading}
        width={980}
        destroyOnClose
      >
        <Typography.Paragraph type="secondary">
          姐姐会从 Git 仓库读取 <code>pom.xml</code>、Spring Boot 入口和 <code>server.port</code>，
          自动推断 <code>service_code</code>、<code>build_module</code> 和 <code>build_jar_pattern</code>。
          导入非 default 服务后会自动停用占位的 <code>default</code> service；如果只启用一个 service，它就是单服务部署。
        </Typography.Paragraph>
        <Table<DiscoverServiceRow>
          rowKey="key"
          size="small"
          loading={discoverLoading}
          dataSource={discoverRows}
          pagination={false}
          rowSelection={{
            selectedRowKeys: selectedDiscoverKeys,
            onChange: (keys) => setSelectedDiscoverKeys(keys.map(String)),
          }}
          columns={[
            {
              title: 'Service',
              dataIndex: 'service_code',
              width: 190,
              render: (v: string, row) => (
                <Space direction="vertical" size={2}>
                  <Input
                    size="small"
                    value={v}
                    onChange={(e) => updateDiscoverRow(row.key, { service_code: e.target.value })}
                  />
                  <Space size={4}>
                    <Tag color={row.confidence === 'high' ? 'green' : row.confidence === 'medium' ? 'blue' : 'orange'}>
                      {row.confidence}
                    </Tag>
                    {row.existing && <Tag color="purple">已存在</Tag>}
                  </Space>
                </Space>
              ),
            },
            {
              title: 'module',
              dataIndex: 'build_module',
              width: 190,
              render: (v: string, row) => (
                <Input
                  size="small"
                  value={v}
                  placeholder="根模块留空"
                  onChange={(e) => updateDiscoverRow(row.key, { build_module: e.target.value })}
                />
              ),
            },
            {
              title: 'jar pattern',
              dataIndex: 'build_jar_pattern',
              render: (v: string, row) => (
                <Input
                  size="small"
                  value={v}
                  onChange={(e) => updateDiscoverRow(row.key, { build_jar_pattern: e.target.value })}
                />
              ),
            },
            {
              title: 'Port',
              dataIndex: 'port',
              width: 100,
              render: (v: number, row) => (
                <InputNumber
                  size="small"
                  min={1}
                  max={65535}
                  value={v}
                  style={{ width: 88 }}
                  onChange={(value) => updateDiscoverRow(row.key, { port: Number(value || app.port) })}
                />
              ),
            },
            {
              title: '启动',
              dataIndex: 'startup_order',
              width: 90,
              render: (v: number, row) => (
                <InputNumber
                  size="small"
                  min={0}
                  max={9999}
                  value={v}
                  style={{ width: 78 }}
                  onChange={(value) => updateDiscoverRow(row.key, { startup_order: Number(value || 100) })}
                />
              ),
            },
            {
              title: '原因',
              dataIndex: 'reason',
              render: (v: string) => <Typography.Text type="secondary">{v}</Typography.Text>,
            },
          ]}
        />
        <Typography.Paragraph type="secondary" style={{ marginTop: 12, marginBottom: 0 }}>
          小宝，导入只是写 service 配置；之后还要在「运行」里选择这些 service 要部署到哪台主机。
        </Typography.Paragraph>
      </Modal>

      <Modal
        title={dfEditing ? `编辑 Dockerfile 模板 ${dfEditing.name}` : '新建 Dockerfile 模板'}
        open={dfCreating || !!dfEditing}
        onCancel={closeDockerfileModal}
        onOk={submitDockerfile}
        okText={dfEditing ? '保存' : '创建'}
        width={760}
      >
        <Form form={dfForm} layout="vertical" size="small">
          <Form.Item
            name="name"
            label="模板名称"
            tooltip="只作为页面展示和 service 绑定标识；远端构建时统一上传为 Dockerfile"
            rules={[{ required: true, message: '请输入模板名称' }]}
          >
            <Input placeholder="如 dockerfile-jdk21" />
          </Form.Item>
          <Form.Item name="description" label="说明">
            <Input placeholder="如 Java 21 Spring Boot 默认模板" />
          </Form.Item>
          <Card
            className="workbench-card"
            size="small"
            title="字段生成 Dockerfile"
            extra={<Button size="small" type="primary" onClick={generateDockerfilePreview}>生成/刷新预览</Button>}
          >
            <Space style={{ display: 'flex' }} align="start">
              <Form.Item
                name="df_base_image"
                label="基础镜像 FROM"
                style={{ flex: 1, minWidth: 260 }}
                tooltip="远端 docker build 时真正使用的基础镜像，如 eclipse-temurin:21-jre / openjdk:17-jdk"
              >
                <Input placeholder="eclipse-temurin:21-jdk" />
              </Form.Item>
              <Form.Item
                name="df_workdir"
                label="工作目录 WORKDIR"
                style={{ flex: 1, minWidth: 180 }}
              >
                <Input placeholder="/app" />
              </Form.Item>
            </Space>
            <Space style={{ display: 'flex' }} align="start">
              <Form.Item
                name="df_jar_dest"
                label="Jar 目标路径"
                style={{ flex: 1, minWidth: 260 }}
                tooltip={'生成 COPY {{JAR_FILE}} <目标路径>，通常是 /app/app.jar'}
              >
                <Input placeholder="/app/app.jar" />
              </Form.Item>
              <Form.Item
                name="df_expose_port"
                label="暴露端口 EXPOSE"
                style={{ flex: 1, minWidth: 180 }}
                tooltip="可填 {{PORT}}，构建时会按 service 端口替换"
              >
                <Input placeholder="{{PORT}}" />
              </Form.Item>
            </Space>
            <Form.Item
              name="df_entrypoint"
              label="启动命令 ENTRYPOINT"
              tooltip={'填写 ENTRYPOINT 后面的内容；需要 shell 展开环境变量时可写 ["sh", "-c", "java $JAVA_OPTS -jar /app/app.jar"]'}
            >
              <Input placeholder='["java", "-jar", "/app/app.jar"]' />
            </Form.Item>
            <Form.Item
              name="df_extra_instructions"
              label="额外 Dockerfile 指令（可选）"
              tooltip="如 ENV / RUN / USER / HEALTHCHECK，会插入到 EXPOSE 与 ENTRYPOINT 之间"
            >
              <Input.TextArea rows={3} style={{ fontFamily: 'monospace' }} placeholder={'ENV TZ=Asia/Shanghai\n# USER 10001'} />
            </Form.Item>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              小宝，字段区只负责生成预览；下面的 Dockerfile 预览仍可高级编辑，最终保存的是预览内容。
            </Typography.Text>
          </Card>
          <Form.Item name="content" label="Dockerfile 预览 / 高级编辑" rules={[{ required: true, message: '请先生成 Dockerfile 预览' }]}>
            <Input.TextArea rows={12} style={{ fontFamily: 'monospace' }} />
          </Form.Item>
          <Form.Item name="is_default" label="设为默认" valuePropName="checked">
            <Input type="checkbox" />
          </Form.Item>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            变量：<code>{'{{JAR_FILE}}'}</code>、<code>{'{{PORT}}'}</code>、<code>{'{{APP_CODE}}'}</code>、<code>{'{{SERVICE_CODE}}'}</code>。
          </Typography.Text>
        </Form>
      </Modal>

    </Card>
    </section>
  )
}
