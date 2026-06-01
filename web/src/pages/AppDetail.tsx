import { useEffect, useMemo, useRef, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import {
  Typography, Card, Button, Space, Table, Tag, Modal,
  Form, Select, InputNumber, Input, message, Descriptions, Skeleton, Tabs, Radio,
  Drawer, Steps, Badge, Upload, Progress, Tooltip,
} from 'antd'

import type {
  App, Deployment, Host, Artifact, PipelineRun, RunSnapshot, StepResult, PipelineStage,
  BuildRun, GitCredential, BuilderEnv,
  AppService, AppServiceInput, ArtifactBundle, DockerfileTemplate, DockerfileTemplateInput, AppInput,
} from '../types'
import { getApp, updateApp } from '../api/app'
import { listHosts } from '../api/host'
import { bindHost, listDeployments, unbindDeployment, updateDeploymentGroup } from '../api/deployment'
import { createArtifact, deleteArtifact, listArtifacts, uploadArtifact, listBundles, cleanupBundleHistory } from '../api/artifact'
import { cancelPipeline, deployApp, getPipeline, listPipelines, rollbackApp } from '../api/pipeline'
import { getBuild, getBuildLog, listBuilds, triggerBuild } from '../api/build'
import { listGitCreds } from '../api/gitcred'
import { getBuilderEnv } from '../api/builderEnv'
import { listAppServices, createAppService, updateAppService, deleteAppService } from '../api/appService'
import { listDockerfileTemplates, createDockerfileTemplate, updateDockerfileTemplate, deleteDockerfileTemplate, ensureDefaultDockerfileTemplate } from '../api/dockerfile'
import { buildWSURL, issueWSTicket, type PipelineWSEvent, type BuildWSEvent } from '../api/ws'
import { formatError } from '../api/client'

// 状态色
const hostStatusTag = (s: string) => {
  if (s === 'online') return <Tag color="green">● 在线</Tag>
  if (s === 'offline') return <Tag color="red">● 离线</Tag>
  return <Tag>○ 未知</Tag>
}
const pipeStatusTag = (s: string) => {
  if (s === 'success') return <Tag color="green">✓ 成功</Tag>
  if (s === 'failed') return <Tag color="red">✗ 失败</Tag>
  if (s === 'running') return <Tag color="processing">● 进行中</Tag>
  return <Tag>{s}</Tag>
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
  const [loadingApp, setLoadingApp] = useState(false)
  const [runtimeOpen, setRuntimeOpen] = useState(false)
  const [runtimeHosts, setRuntimeHosts] = useState<Host[]>([])
  const [runtimeForm] = Form.useForm<AppInput>()
  const runtimeDeployMode = Form.useWatch('deploy_mode', runtimeForm)

  const refresh = async () => {
    setLoadingApp(true)
    try {
      setApp(await getApp(appId))
    } catch (e) {
      message.error(formatError(e))
    } finally {
      setLoadingApp(false)
    }
  }
  useEffect(() => { refresh() }, [appId])

  if (loadingApp && !app) return <Skeleton active />
  if (!app) return <Typography.Text type="danger">应用不存在</Typography.Text>

  const openRuntimeEdit = async () => {
    try { setRuntimeHosts(await listHosts()) } catch {}
    runtimeForm.resetFields()
    runtimeForm.setFieldsValue({
      build_mode: app.build_mode || 'local-jar',
      deploy_mode: app.deploy_mode || 'systemd',
      jvm_args: app.jvm_args || '',
      env_vars: app.env_vars || '',
      systemd_user: app.systemd_user || '',
      java_path: app.java_path || '',
      docker_run_args: app.docker_run_args || '',
      nginx_host_id: app.nginx_host_id || 0,
      nginx_upstream_name: app.nginx_upstream_name || '',
      active_group: app.active_group || '',
    })
    setRuntimeOpen(true)
  }

  const saveRuntimeConfig = async () => {
    try {
      const v = await runtimeForm.validateFields()
      const hasHost = !!v.nginx_host_id && v.nginx_host_id > 0
      const hasName = !!v.nginx_upstream_name && v.nginx_upstream_name.trim() !== ''
      if (hasHost !== hasName) {
        message.error('Nginx 主机和 upstream 名要么同时填，要么同时留空')
        return
      }
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
    <div>
      <Space style={{ marginBottom: 16 }}>
        <Button onClick={() => nav('/apps')}>← 返回列表</Button>
        <Typography.Title level={4} style={{ margin: 0 }}>
          {app.name}{' '}
          <Typography.Text type="secondary" style={{ fontSize: 14 }}>
            <code>{app.app_code}</code>
          </Typography.Text>
        </Typography.Title>
      </Space>

      <Card
        title="应用摘要"
        size="small"
        style={{ marginBottom: 16 }}
        extra={<Button size="small" onClick={openRuntimeEdit}>编辑运行配置</Button>}
      >
        <Descriptions size="small" column={2}>
          <Descriptions.Item label="类型">{app.app_type}</Descriptions.Item>
          <Descriptions.Item label="端口">{app.port}</Descriptions.Item>
          <Descriptions.Item label="默认构建 Ref"><code>{app.git_ref || 'main'}</code></Descriptions.Item>
          <Descriptions.Item label="Git 仓库"><code>{app.git_url || '-'}</code></Descriptions.Item>
          <Descriptions.Item label="可选构建 Ref" span={2}>
            {appBuildRefs(app).map(ref => <Tag key={ref}>{ref}</Tag>)}
          </Descriptions.Item>
          <Descriptions.Item label="部署路径" span={2}><code>{app.deploy_path}</code></Descriptions.Item>
          <Descriptions.Item label="健康检查">{app.health_check_url}</Descriptions.Item>
        </Descriptions>
      </Card>

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
        <Form form={runtimeForm} layout="vertical">
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
            <Form.Item
              name="docker_run_args"
              label="docker run 参数"
              tooltip="如 -p 8080:8080 -e ENV=prod --restart=unless-stopped，留空则自动生成"
            >
              <Input.TextArea rows={3} placeholder="留空自动生成" />
            </Form.Item>
          )}
          <Form.Item name="env_vars" label="环境变量 (JSON)">
            <Input.TextArea rows={3} placeholder='{"SPRING_PROFILES_ACTIVE":"prod"}' />
          </Form.Item>

          <Typography.Title level={5}>蓝绿配置</Typography.Title>
          <Form.Item name="nginx_host_id" label="Nginx 主机">
            <Select
              allowClear
              placeholder="不启用蓝绿请留空"
              options={[
                { value: 0, label: '— 不启用 —' },
                ...runtimeHosts.map((h) => ({
                  value: h.id,
                  label: `${h.name} (${h.ip})${h.status === 'online' ? ' ● 在线' : ''}`,
                })),
              ]}
            />
          </Form.Item>
          <Form.Item
            name="nginx_upstream_name"
            label="Upstream 名"
            rules={[{
              pattern: /^([A-Za-z][A-Za-z0-9_-]{1,99})?$/,
              message: '字母开头，2-100 位，仅含字母/数字/下划线/连字符',
            }]}
          >
            <Input placeholder="如：user-svc-backend（留空表示不启用）" />
          </Form.Item>
          <Form.Item name="active_group" label="当前活跃组">
            <Radio.Group>
              <Radio value="">未设置</Radio>
              <Radio value="blue">Blue</Radio>
              <Radio value="green">Green</Radio>
            </Radio.Group>
          </Form.Item>
        </Form>
      </Modal>

      <Card>
        <Tabs
          defaultActiveKey={
            app.app_type === 'spring-cloud' ||
            app.build_mode === 'local-docker' ||
            app.build_mode === 'remote-docker'
              ? 'services'
              : 'hosts'
          }
          items={
            app.app_type === 'spring-cloud'
              ? [
                  { key: 'services',  label: '微服务配置', children: <ServicesTab appId={appId} app={app} /> },
                  { key: 'hosts',     label: '主机绑定', children: <HostBindTab appId={appId} appPort={app.port} /> },
                  { key: 'artifacts', label: '制品 & 构建', children: <ArtifactTab app={app} /> },
                  { key: 'pipelines', label: '部署历史', children: <PipelineTab app={app} /> },
                ]
              : [
                  { key: 'services',  label: 'Dockerfile 配置', children: <ServicesTab appId={appId} app={app} /> },
                  { key: 'hosts',     label: '主机绑定', children: <HostBindTab appId={appId} appPort={app.port} /> },
                  { key: 'artifacts', label: '制品 & 构建', children: <ArtifactTab app={app} /> },
                  { key: 'pipelines', label: '部署历史', children: <PipelineTab app={app} /> },
                ]
          }
        />
      </Card>
    </div>
  )
}

// ---------- 主机绑定 Tab ----------

function HostBindTab({ appId, appPort }: { appId: number; appPort: number }) {
  const [deployments, setDeployments] = useState<Deployment[]>([])
  const [hosts, setHosts] = useState<Host[]>([])
  const [loading, setLoading] = useState(false)
  const [bindOpen, setBindOpen] = useState(false)
  const [bindForm] = Form.useForm()

  const refresh = async () => {
    setLoading(true)
    try {
      const [ds, hs] = await Promise.all([listDeployments(appId), listHosts()])
      setDeployments(ds); setHosts(hs)
    } catch (e) {
      message.error(formatError(e))
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => { refresh() }, [appId])

  const boundHostIds = new Set(deployments.map((d) => d.host_id))
  const availableHosts = hosts.filter((h) => !boundHostIds.has(h.id))

  const handleBind = async () => {
    try {
      const v = await bindForm.validateFields()
      await bindHost(appId, v)
      message.success('已绑定')
      setBindOpen(false); refresh()
    } catch (e) {
      if ((e as any)?.errorFields) return
      message.error(formatError(e))
    }
  }
  const handleUnbind = (d: Deployment) => {
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
  const handleGroupChange = async (d: Deployment, group: string) => {
    try { await updateDeploymentGroup(d.id, group); message.success(`已切换到 ${group || '未分组'}`); refresh() }
    catch (e) { message.error(formatError(e)) }
  }

  return (
    <>
      <Space style={{ marginBottom: 12 }}>
        <Button type="primary" disabled={availableHosts.length === 0}
          onClick={() => { bindForm.resetFields(); setBindOpen(true) }}>
          + 绑定主机
        </Button>
        <Button onClick={refresh}>刷新</Button>
      </Space>
      <Table<Deployment>
        rowKey="id" loading={loading} dataSource={deployments} pagination={false}
        locale={{ emptyText: '还没绑定主机，点上方"+ 绑定主机"开始' }}
        columns={[
          { title: '部署 ID', dataIndex: 'id', width: 80 },
          { title: '主机', render: (_, d) => <><code>{d.host_ip}</code>　<Typography.Text>{d.host_name}</Typography.Text></> },
          { title: '主机状态', dataIndex: 'host_status', width: 100, render: (s) => hostStatusTag(s) },
          {
            title: '蓝绿分组', dataIndex: 'group_tag', width: 140,
            render: (g, d) => (
              <Select size="small" value={g || ''} style={{ width: 110 }}
                onChange={(v) => handleGroupChange(d, v)}
                options={[
                  { value: '', label: '未分组' },
                  { value: 'blue', label: <Tag color="blue">Blue</Tag> },
                  { value: 'green', label: <Tag color="green">Green</Tag> },
                ]} />
            ),
          },
          { title: '端口覆盖', dataIndex: 'port', width: 90, render: (p) => p || <Typography.Text type="secondary">（继承）</Typography.Text> },
          { title: '当前制品', dataIndex: 'current_artifact_id', width: 100, render: (id) => id ? `#${id}` : '-' },
          { title: '部署状态', dataIndex: 'status', width: 100, render: (s) => <Tag>{s}</Tag> },
          { title: '操作', width: 100, render: (_, d) => <Button danger size="small" onClick={() => handleUnbind(d)}>解绑</Button> },
        ]}
      />
      <Modal title="绑定主机" open={bindOpen} onOk={handleBind} onCancel={() => setBindOpen(false)} okText="绑定" cancelText="取消" maskClosable={false} keyboard={false}>
        <Form form={bindForm} layout="vertical" initialValues={{ group_tag: '' }}>
          <Form.Item name="host_id" label="选择主机" rules={[{ required: true, message: '必选' }]}>
            <Select placeholder="从可用主机里选一台"
              options={availableHosts.map((h) => ({
                value: h.id,
                label: `${h.name} (${h.ip})${h.status === 'online' ? ' ● 在线' : ''}`,
              }))} />
          </Form.Item>
          <Form.Item name="group_tag" label="蓝绿分组">
            <Select options={[
              { value: '', label: '未分组' },
              { value: 'blue', label: 'Blue' },
              { value: 'green', label: 'Green' },
            ]} />
          </Form.Item>
          <Form.Item name="port" label="端口覆盖（可选，留空继承应用默认）">
            <InputNumber min={1} max={65535} style={{ width: 200 }} placeholder={String(appPort)} />
          </Form.Item>
        </Form>
      </Modal>
    </>
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

  const buildStatusTag = (s: string) => {
    if (s === 'success') return <Tag color="green">✓ 成功</Tag>
    if (s === 'failed') return <Tag color="red">✗ 失败</Tag>
    if (s === 'building') return <Tag color="processing">● 构建中</Tag>
    if (s === 'cancelled') return <Tag color="default">○ 已取消</Tag>
    return <Tag>{s}</Tag>
  }

  return (
    <>
      <Space style={{ marginBottom: 12 }} wrap>
        <Tooltip title={
          !app.git_url ? '应用未配 git_url' :
          !builderEnv?.valid ? '构建环境未就绪，去「⚙ 构建环境」配置 java_home / maven_home 并检测' :
          ''
        }>
          <Button type="primary" onClick={openBuild}
            disabled={!app.git_url || !builderEnv?.valid}>
            ⚙ 从仓库构建
          </Button>
        </Tooltip>
        <Button onClick={openUpload}>↑ 上传文件</Button>
        <Button onClick={() => { form.resetFields(); setOpen(true) }}>+ 注册路径</Button>
        <Button onClick={() => { refresh(); refreshBuilds(); getBuilderEnv().then(setBuilderEnv).catch(() => {}) }}>刷新</Button>
        {bundles.length > 0 && (
          <Button danger onClick={handleCleanup}>🧹 清理历史</Button>
        )}
        {!app.git_url && (
          <Typography.Text type="secondary">应用未配 git_url，构建按钮不可用；先去「应用管理」补上。</Typography.Text>
        )}
        {app.git_url && builderEnv && !builderEnv.valid && (
          <Typography.Text type="warning">构建环境未就绪，去左侧菜单「⚙ 构建环境」配一下。</Typography.Text>
        )}
      </Space>

      {/* 构建历史：仅有记录时显示 */}
      {builds.length > 0 && (
        <Card size="small" title={`构建历史（最近 ${Math.min(builds.length, 10)} 条）`}
          style={{ marginBottom: 12 }}>
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
        <Card size="small"
          title={`整组制品 Bundle（从仓库构建产出 · 共 ${bundles.length} 组）`}
          style={{ marginBottom: 12 }}>
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
        locale={{ emptyText: '还没有制品，点上方"⚙ 从仓库构建" / "↑ 上传文件" / "+ 注册路径"添加' }}
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
            构建模块 / Jar 匹配请在应用详情的服务配置里维护。
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
              <Card size="small" type="inner" style={{ marginBottom: 12, borderColor: '#ff4d4f' }}>
                <Typography.Text type="danger">{logTarget.error}</Typography.Text>
              </Card>
            )}
            <pre style={{
              background: '#1e1e1e', color: '#d4d4d4', padding: 12, borderRadius: 4,
              maxHeight: 500, overflow: 'auto', fontSize: 12,
              fontFamily: 'Consolas, Monaco, monospace',
              whiteSpace: 'pre-wrap', wordBreak: 'break-word',
            }}>
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
              <p className="ant-upload-drag-icon" style={{ fontSize: 36, color: '#1677ff' }}>⬆</p>
              <p className="ant-upload-text">点击或拖拽 jar 文件到此区域</p>
              <p className="ant-upload-hint">单文件，最大受服务端 storage.max_upload_mb 限制</p>
            </Upload.Dragger>
          </Form.Item>
          {(uploading || upPercent > 0) && (
            <Progress percent={upPercent} status={uploading ? 'active' : 'success'} />
          )}
        </Form>
      </Modal>
    </>
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
      const strategy = (v.strategy ?? 'single') as 'single' | 'rolling' | 'blue_green'
      const batchSize = strategy === 'rolling' ? Number(v.batch_size ?? 1) : undefined
      const sourceType = (v.source_type ?? 'bundle') as 'bundle' | 'artifact'
      const source = sourceType === 'bundle'
        ? { bundle_id: Number(v.bundle_id) }
        : { artifact_id: Number(v.artifact_id) }
      const run = await deployApp(app.id, source, strategy, batchSize)
      const detail =
        strategy === 'rolling' ? `, batch=${batchSize}` :
        strategy === 'blue_green' ? `（目标组由后端自动选）` : ''
      message.success(`已触发：#${run.id}（${strategy}${detail}）`)
      setOpen(false); setDrawerRun(run); refresh()
    } catch (e) {
      if ((e as any)?.errorFields) return
      message.error(formatError(e))
    }
  }

  const handleRollback = () => {
    Modal.confirm({
      title: `回滚 ${app.name}？`,
      content: (
        <>
          <div>多 service 应用整组退到上一个 Bundle（Sprint X.7）；单 jar 应用退到自己的 <code>previous_artifact_id</code>。</div>
          <div style={{ marginTop: 8 }}>
            <Typography.Text type="secondary">
              没有 previous 的主机会被跳过；按 startup_order 分波退回，任一 service 失败将停止后续 wave。
            </Typography.Text>
          </div>
        </>
      ),
      okType: 'danger',
      okText: '确认回滚',
      cancelText: '取消',
      onOk: async () => {
        try {
          const run = await rollbackApp(app.id)
          message.success(`已触发回滚：#${run.id}`)
          setDrawerRun(run); refresh()
        } catch (e) {
          message.error(formatError(e))
        }
      },
    })
  }

  return (
    <>
      <Space style={{ marginBottom: 12 }}>
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
          🚀 触发部署
        </Button>
        <Button danger onClick={handleRollback}>↩ 回滚</Button>
        <Button onClick={refresh}>刷新</Button>
        {arts.length === 0 && bundles.length === 0 && (
          <Typography.Text type="secondary">先去"制品"页注册一个 jar 或触发构建产出 Bundle，再来部署。</Typography.Text>
        )}
      </Space>
      <Table<PipelineRun>
        rowKey="id" loading={loading} dataSource={list} pagination={false}
        locale={{ emptyText: '还没有部署记录' }}
        columns={[
          { title: '#', dataIndex: 'id', width: 60 },
          { title: '策略', dataIndex: 'strategy', width: 90, render: (v) => <Tag>{v}</Tag> },
          {
            title: '产物', width: 110,
            render: (_: any, r: PipelineRun) => {
              const anyR = r as any
              if (anyR.bundle_id) return <Tag color="blue">Bundle #{anyR.bundle_id}</Tag>
              if (r.artifact_id) return <Tag>Art #{r.artifact_id}</Tag>
              return '-'
            },
          },
          { title: '状态', dataIndex: 'status', width: 110, render: (s) => pipeStatusTag(s) },
          { title: '触发人', dataIndex: 'triggered_by', width: 110 },
          { title: '开始', dataIndex: 'started_at', width: 170, render: (s) => s ? new Date(s).toLocaleString() : '-' },
          { title: '结束', dataIndex: 'finished_at', width: 170, render: (s) => s ? new Date(s).toLocaleString() : '-' },
          { title: '操作', width: 90, render: (_, r) => <Button size="small" onClick={() => setDrawerRun(r)}>详情</Button> },
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
                { value: 'single',     label: 'single（顺序逐台、任一失败立刻停止）' },
                { value: 'rolling',    label: 'rolling（分批并行、批级 fail-fast）' },
                { value: 'blue_green', label: 'blue_green（双组并存、切流到非活跃组；需先在应用配 nginx）' },
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
              if (strategy === 'blue_green') {
                const activeText = app.active_group
                  ? `当前活跃 ${app.active_group}，本次将部署到 ${app.active_group === 'blue' ? 'green' : 'blue'} 组`
                  : '首次蓝绿部署：将默认部署到 blue 组'
                return (
                  <Typography.Text type="secondary" style={{ display: 'block', marginBottom: 8 }}>
                    📘 {activeText}；目标组主机部署完成后会重写 nginx upstream 切流。
                  </Typography.Text>
                )
              }
              return null
            }}
          </Form.Item>
        </Form>
      </Modal>
      <PipelineDetailDrawer run={drawerRun} onClose={() => setDrawerRun(null)} />
    </>
  )
}

// ---------- 流水线详情抽屉 ----------

const stageLabel: Record<PipelineStage, string> = {
  dial: 'SSH 拨号',
  env_check: '远端环境预检',
  upload: '上传 jar',
  write_unit: '写 systemd unit',
  restart: 'systemctl restart',
  health: '健康探针',
  nginx_apply: 'Nginx 切流',
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
            <Descriptions.Item label="制品">#{run.artifact_id}</Descriptions.Item>
            <Descriptions.Item label="触发人">{run.triggered_by}</Descriptions.Item>
            <Descriptions.Item label="开始">{run.started_at ? new Date(run.started_at).toLocaleString() : '-'}</Descriptions.Item>
            <Descriptions.Item label="结束">{run.finished_at ? new Date(run.finished_at).toLocaleString() : '-'}</Descriptions.Item>
          </Descriptions>
          {snap?.error && (
            <Card size="small" type="inner" style={{ marginBottom: 16, borderColor: '#ff4d4f' }}>
              <Typography.Text type="danger">{snap.error}</Typography.Text>
            </Card>
          )}
          {Array.from(byHost.entries()).map(([hostId, steps]) => (
            <Card key={hostId} size="small" type="inner" style={{ marginBottom: 12 }}
              title={<><Badge status={steps.every((s) => s.ok) ? 'success' : 'error'} />
                {steps[0]?.host_name}（<code>{steps[0]?.host_ip}</code>）</>}>
              <Steps direction="vertical" size="small" current={steps.length - 1}
                items={steps.map((s) => ({
                  title: stageLabel[s.stage] ?? s.stage,
                  status: s.ok ? 'finish' : 'error',
                  description: (
                    <>
                      {s.detail && <div><Typography.Text type="secondary">{s.detail}</Typography.Text></div>}
                      {s.error && <div><Typography.Text type="danger">✗ {s.error}</Typography.Text></div>}
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
  if (state === 'open') return <Tag color="green">● 实时</Tag>
  if (state === 'connecting') return <Tag color="processing">○ 连接中</Tag>
  if (state === 'error') return <Tag color="red">⚠ 连接失败 (轮询兜底)</Tag>
  if (state === 'closed') return <Tag>○ 已断开</Tag>
  return null
}

// ---------- 微服务 Tab (Sprint X.4) ----------
//
// 列表 + 新建/编辑/删除。单体 App 自动有一行 service_code="default"。
// 多服务 App 通过这里加 N 行（每个 jar 一个 AppService）。
function ServicesTab({ appId, app }: { appId: number; app: App }) {
  const [items, setItems] = useState<AppService[]>([])
  const [loading, setLoading] = useState(false)
  const [editing, setEditing] = useState<AppService | null>(null)
  const [creating, setCreating] = useState(false)
  const [form] = Form.useForm<AppServiceInput>()
  const [dockerfiles, setDockerfiles] = useState<DockerfileTemplate[]>([])
  const [dfEditing, setDfEditing] = useState<DockerfileTemplate | null>(null)
  const [dfCreating, setDfCreating] = useState(false)
  const [dfForm] = Form.useForm<DockerfileTemplateFormValues>()

  const [builderEnv, setBuilderEnv] = useState<BuilderEnv | null>(null)

  const refresh = async () => {
    setLoading(true)
    try {
      const [svcRows, dfRows] = await Promise.all([
        listAppServices(appId),
        listDockerfileTemplates(appId),
      ])
      setItems(svcRows)
      setDockerfiles(dfRows)
    } catch (e) {
      message.error(formatError(e))
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    refresh()
    getBuilderEnv().then(setBuilderEnv).catch(() => {})
  }, [appId])

  const openCreate = () => {
    setCreating(true); setEditing(null)
    form.resetFields()
    form.setFieldsValue({
      service_code: '', name: '',
      port: app.port,
      health_check_url: app.health_check_url || '/actuator/health',
      jvm_args: app.jvm_args || '',
      systemd_user: app.systemd_user || '',
      deploy_mode: (app.deploy_mode || '') as 'systemd' | 'nohup' | 'docker' | '',
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
      port: row.port, health_check_url: row.health_check_url,
      jvm_args: row.jvm_args, env_vars: row.env_vars,
      systemd_user: row.systemd_user, java_path: row.java_path,
      deploy_mode: (row.deploy_mode || '') as 'systemd' | 'nohup' | 'docker' | '',
      dockerfile_template_id: row.dockerfile_template_id || dockerfiles.find(d => d.is_default)?.id || 0,
      docker_registry: row.docker_registry || '',
      docker_image_name: row.docker_image_name || '',
      docker_image_tag: row.docker_image_tag || '',
      docker_build_args: row.docker_build_args || '',
      docker_run_args: row.docker_run_args || '',
      startup_order: row.startup_order, optional: row.optional, enabled: row.enabled,
      nginx_host_id: row.nginx_host_id, nginx_upstream_name: row.nginx_upstream_name,
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
        health_check_url: editing.health_check_url,
        jvm_args: editing.jvm_args,
        env_vars: editing.env_vars,
        systemd_user: editing.systemd_user,
        java_path: editing.java_path,
        startup_order: editing.startup_order,
        optional: editing.optional,
        enabled: editing.enabled,
        nginx_host_id: editing.nginx_host_id,
        nginx_upstream_name: editing.nginx_upstream_name,
        active_group: editing.active_group,
        deploy_mode: editing.deploy_mode,
        docker_registry: editing.docker_registry,
        docker_image_name: editing.docker_image_name,
        docker_image_tag: editing.docker_image_tag,
        dockerfile_template_id: editing.dockerfile_template_id,
        dockerfile: editing.dockerfile,
        docker_build_args: editing.docker_build_args,
        docker_run_args: editing.docker_run_args,
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

  return (
    <>
      <Card
        title={app.app_type === 'spring-cloud' ? 'Dockerfile 模板' : 'Dockerfile 模板（单体 jar）'}
        size="small"
        style={{ marginBottom: 16 }}
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
        title={app.app_type === 'spring-cloud' ? '微服务列表' : '单体服务配置'}
        size="small"
        extra={app.app_type === 'spring-cloud' ? <Button type="primary" onClick={openCreate}>+ 新建 Service</Button> : null}
      >
        <Typography.Paragraph type="secondary" style={{ marginBottom: 12 }}>
          {app.app_type === 'spring-cloud' ? (
            <>
              多 service 应用在这里增删。Spring Cloud 项目可加 <code>eureka</code> / <code>gateway</code> / <code>user-service</code> 等。
              部署按 <strong>startup_order</strong> 分波，同波内并发。
            </>
          ) : (
            <>
              单体 App 使用自动创建的 <code>default</code> service。这里主要用于为该服务绑定 Dockerfile 模板。
            </>
          )}
        </Typography.Paragraph>
        <Table
        rowKey="id"
        size="small"
        loading={loading}
        dataSource={items}
        pagination={false}
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
          {app.app_type === 'spring-cloud' ? (
            <>
              <Form.Item
                name="service_code" label="service_code"
                rules={[{ required: true, pattern: /^[a-z][a-z0-9-]{1,49}$/,
                  message: '小写字母开头，2-50 位，仅小写字母/数字/连字符' }]}
                tooltip="systemd unit 命名一部分，创建后不可改"
              >
                <Input placeholder="eureka / gateway / user-service" disabled={!!editing} />
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
              <Form.Item name="health_check_url" label="健康检查 URL">
                <Input placeholder="/actuator/health" />
              </Form.Item>
              <Form.Item name="build_module" label="build_module" tooltip="multi-module 项目用，mvn -pl">
                <Input placeholder="user-service" />
              </Form.Item>
              <Form.Item name="build_jar_pattern" label="build_jar_pattern" tooltip="glob 在 workspace 下匹配 jar">
                <Input placeholder="user-service/target/*.jar" />
              </Form.Item>
              <Form.Item name="jvm_args" label="JVM 参数">
                <Input placeholder="-Xms512m -Xmx512m" />
              </Form.Item>
              <Form.Item name="env_vars" label="环境变量 (JSON)">
                <Input.TextArea rows={2} placeholder='{"SPRING_PROFILES_ACTIVE":"prod"}' />
              </Form.Item>
              <Form.Item
                name="deploy_mode"
                label="部署模式（覆盖 app 默认）"
                tooltip="留空 = 沿用 app 的 deploy_mode。systemd 需 root/sudo；nohup 免 root；docker 用目标主机 Docker 容器启动。"
              >
                <Select
                  allowClear
                  placeholder="留空 = 沿用 app 默认"
                  options={[
                    { value: 'systemd', label: 'systemd（推荐，crash 自愈）' },
                    { value: 'nohup', label: 'nohup（免 root，crash 不自愈）' },
                    { value: 'docker', label: 'Docker（目标主机容器）' },
                  ]}
                />
              </Form.Item>
            </>
          ) : (
            <Typography.Paragraph type="secondary" style={{ marginBottom: 12 }}>
              单体应用的 <code>default</code> service 继承应用基础配置；这里仅选择构建/部署时使用的 Dockerfile 模板。
            </Typography.Paragraph>
          )}
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
          {app.app_type === 'spring-cloud' && (
            <>
              <Space style={{ display: 'flex' }} align="start">
                <Form.Item name="systemd_user" label="systemd User" style={{ flex: 1 }}>
                  <Input placeholder="留空 = root" />
                </Form.Item>
                <Form.Item name="java_path" label="JavaPath 覆盖" style={{ flex: 1 }}>
                  <Input placeholder="留空 = 沿用 Host" />
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
            </>
          )}
        </Form>
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
            size="small"
            title="字段生成 Dockerfile"
            style={{ marginBottom: 12 }}
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
    </>
  )
}
