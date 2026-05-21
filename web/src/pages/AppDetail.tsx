import { useEffect, useMemo, useRef, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import {
  Typography, Card, Button, Space, Table, Tag, Modal,
  Form, Select, InputNumber, Input, message, Descriptions, Skeleton, Tabs,
  Drawer, Steps, Badge, Upload, Progress,
} from 'antd'

import type {
  App, Deployment, Host, Artifact, PipelineRun, RunSnapshot, StepResult, PipelineStage,
} from '../types'
import { getApp } from '../api/app'
import { listHosts } from '../api/host'
import { bindHost, listDeployments, unbindDeployment, updateDeploymentGroup } from '../api/deployment'
import { createArtifact, deleteArtifact, listArtifacts, uploadArtifact } from '../api/artifact'
import { cancelPipeline, deployApp, getPipeline, listPipelines, rollbackApp } from '../api/pipeline'
import { buildWSURL, issueWSTicket, type PipelineWSEvent } from '../api/ws'
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

// ---------- 主页 ----------

export default function AppDetail() {
  const { id } = useParams<{ id: string }>()
  const nav = useNavigate()
  const appId = Number(id)
  const [app, setApp] = useState<App | null>(null)
  const [loadingApp, setLoadingApp] = useState(false)

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

      <Card title="应用信息" size="small" style={{ marginBottom: 16 }}>
        <Descriptions size="small" column={2}>
          <Descriptions.Item label="类型">{app.app_type}</Descriptions.Item>
          <Descriptions.Item label="端口">{app.port}</Descriptions.Item>
          <Descriptions.Item label="部署路径" span={2}><code>{app.deploy_path}</code></Descriptions.Item>
          <Descriptions.Item label="健康检查">{app.health_check_url}</Descriptions.Item>
          <Descriptions.Item label="systemd 用户">
            {app.systemd_user
              ? <code>{app.systemd_user}</code>
              : <Typography.Text type="secondary">（沿用 SSH 账号）</Typography.Text>}
          </Descriptions.Item>
          <Descriptions.Item label="JVM 参数"><code>{app.jvm_args || '-'}</code></Descriptions.Item>
          <Descriptions.Item label="环境变量" span={2}><code>{app.env_vars || '-'}</code></Descriptions.Item>
        </Descriptions>
      </Card>

      <Card>
        <Tabs
          defaultActiveKey="hosts"
          items={[
            { key: 'hosts',     label: '主机绑定', children: <HostBindTab appId={appId} appPort={app.port} /> },
            { key: 'artifacts', label: '制品',     children: <ArtifactTab appId={appId} /> },
            { key: 'pipelines', label: '部署历史', children: <PipelineTab app={app} /> },
          ]}
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
      <Modal title="绑定主机" open={bindOpen} onOk={handleBind} onCancel={() => setBindOpen(false)} okText="绑定" cancelText="取消">
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

function ArtifactTab({ appId }: { appId: number }) {
  const [list, setList] = useState<Artifact[]>([])
  const [loading, setLoading] = useState(false)
  const [open, setOpen] = useState(false)
  const [form] = Form.useForm()

  // 上传 Modal 状态
  const [upOpen, setUpOpen] = useState(false)
  const [upForm] = Form.useForm()
  const [upFile, setUpFile] = useState<File | null>(null)
  const [upPercent, setUpPercent] = useState(0)
  const [uploading, setUploading] = useState(false)

  const refresh = async () => {
    setLoading(true)
    try { setList(await listArtifacts(appId)) }
    catch (e) { message.error(formatError(e)) }
    finally { setLoading(false) }
  }
  useEffect(() => { refresh() }, [appId])

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

  return (
    <>
      <Space style={{ marginBottom: 12 }}>
        <Button type="primary" onClick={openUpload}>↑ 上传文件</Button>
        <Button onClick={() => { form.resetFields(); setOpen(true) }}>+ 注册路径</Button>
        <Button onClick={refresh}>刷新</Button>
        <Typography.Text type="secondary">上传走 multipart 直传服务器；注册适合已经 scp 到服务器的旧 jar。</Typography.Text>
      </Space>
      <Table<Artifact>
        rowKey="id" loading={loading} dataSource={list} pagination={false}
        locale={{ emptyText: '还没有制品，点上方"↑ 上传文件"或"+ 注册路径"添加' }}
        columns={[
          { title: 'ID', dataIndex: 'id', width: 60 },
          { title: '版本', dataIndex: 'version_tag', width: 140, render: (v) => <Tag>{v}</Tag> },
          { title: '文件', dataIndex: 'file_name' },
          { title: '路径', dataIndex: 'file_path', ellipsis: true, render: (v) => <code>{v}</code> },
          { title: 'MD5', dataIndex: 'file_md5', width: 280, render: (v) => <code style={{ fontSize: 11 }}>{v}</code> },
          { title: '大小', dataIndex: 'file_size', width: 100, render: (n) => `${(n/1024).toFixed(1)} KB` },
          { title: '创建', dataIndex: 'created_at', width: 170, render: (s) => new Date(s).toLocaleString() },
          { title: '操作', width: 100, render: (_, a) => <Button danger size="small" onClick={() => handleDelete(a)}>删除</Button> },
        ]}
      />

      <Modal title="注册制品（本地 jar 路径）" open={open} onOk={handleAdd} onCancel={() => setOpen(false)} okText="注册" cancelText="取消">
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
        maskClosable={!uploading}
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
  const [loading, setLoading] = useState(false)
  const [open, setOpen] = useState(false)
  const [form] = Form.useForm()
  const [drawerRun, setDrawerRun] = useState<PipelineRun | null>(null)

  const refresh = async () => {
    setLoading(true)
    try {
      const [ps, as] = await Promise.all([listPipelines(app.id), listArtifacts(app.id)])
      setList(ps); setArts(as)
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
      const run = await deployApp(app.id, v.artifact_id, strategy, batchSize)
      message.success(`已触发：#${run.id}（${strategy}${batchSize ? `, batch=${batchSize}` : ''}）`)
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
          <div>每台已绑定主机会退回到自己的 <code>previous_artifact_id</code>。</div>
          <div style={{ marginTop: 8 }}>
            <Typography.Text type="secondary">
              没有 previous 的主机会被跳过；任一主机失败将停止后续。
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
        <Button type="primary" disabled={arts.length === 0}
          onClick={() => { form.resetFields(); form.setFieldsValue({ strategy: 'single', batch_size: 2 }); setOpen(true) }}>
          🚀 触发部署
        </Button>
        <Button danger onClick={handleRollback}>↩ 回滚</Button>
        <Button onClick={refresh}>刷新</Button>
        {arts.length === 0 && (
          <Typography.Text type="secondary">先去"制品"页注册一个 jar，再来触发部署。</Typography.Text>
        )}
      </Space>
      <Table<PipelineRun>
        rowKey="id" loading={loading} dataSource={list} pagination={false}
        locale={{ emptyText: '还没有部署记录' }}
        columns={[
          { title: '#', dataIndex: 'id', width: 60 },
          { title: '策略', dataIndex: 'strategy', width: 90, render: (v) => <Tag>{v}</Tag> },
          { title: '制品', dataIndex: 'artifact_id', width: 80, render: (id) => `#${id}` },
          { title: '状态', dataIndex: 'status', width: 110, render: (s) => pipeStatusTag(s) },
          { title: '触发人', dataIndex: 'triggered_by', width: 110 },
          { title: '开始', dataIndex: 'started_at', width: 170, render: (s) => s ? new Date(s).toLocaleString() : '-' },
          { title: '结束', dataIndex: 'finished_at', width: 170, render: (s) => s ? new Date(s).toLocaleString() : '-' },
          { title: '操作', width: 90, render: (_, r) => <Button size="small" onClick={() => setDrawerRun(r)}>详情</Button> },
        ]}
      />
      <Modal title={`触发部署 - ${app.name}`} open={open} onOk={handleDeploy} onCancel={() => setOpen(false)} okText="触发" cancelText="取消">
        <Form form={form} layout="vertical" initialValues={{ strategy: 'single', batch_size: 2 }}>
          <Form.Item name="artifact_id" label="选择制品" rules={[{ required: true }]}>
            <Select placeholder="选择要部署的版本"
              options={arts.map((a) => ({
                value: a.id,
                label: `#${a.id}  ${a.version_tag}  (${a.file_name})`,
              }))} />
          </Form.Item>
          <Form.Item name="strategy" label="部署策略" rules={[{ required: true }]}>
            <Select
              options={[
                { value: 'single',  label: 'single（顺序逐台、任一失败立刻停止）' },
                { value: 'rolling', label: 'rolling（分批并行、批级 fail-fast）' },
              ]}
            />
          </Form.Item>
          <Form.Item
            noStyle
            shouldUpdate={(prev, cur) => prev.strategy !== cur.strategy}
          >
            {({ getFieldValue }) =>
              getFieldValue('strategy') === 'rolling' ? (
                <Form.Item
                  name="batch_size"
                  label="批大小（每批同时部署的主机数）"
                  rules={[{ required: true, message: 'rolling 必须指定 batch_size' }]}
                >
                  <InputNumber min={1} max={50} style={{ width: 200 }} placeholder="2" />
                </Form.Item>
              ) : null
            }
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
  upload: '上传 jar',
  write_unit: '写 systemd unit',
  restart: 'systemctl restart',
  health: '健康探针',
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
