import { useEffect, useState } from 'react'
import { Table, Button, Space, Tag, Modal, message, Card } from 'antd'
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons'

import type { Host, TestResult } from '../types'
import { deleteHost, listHosts, testHost } from '../api/host'
import { formatError } from '../api/client'
import HostForm from './HostForm'
import { EmptyState, PageHeader, StatCard, StatGrid } from '../components/PageFrame'
import { StatusTag } from '../components/StatusTag'

const statusTag = (s: Host['status']) => {
  if (s === 'online') return <StatusTag tone="success">在线</StatusTag>
  if (s === 'offline') return <StatusTag tone="danger">离线</StatusTag>
  return <StatusTag>未知</StatusTag>
}

export default function HostList() {
  const [data, setData] = useState<Host[]>([])
  const [loading, setLoading] = useState(false)
  const [formOpen, setFormOpen] = useState(false)
  const [editing, setEditing] = useState<Host | null>(null)

  const refresh = async () => {
    setLoading(true)
    try {
      setData(await listHosts())
    } catch (e) {
      message.error(formatError(e))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { refresh() }, [])

  const handleTest = async (h: Host) => {
    const hide = message.loading(`正在测试 ${h.name} ...`, 0)
    try {
      const r: TestResult = await testHost(h.id)
      hide()
      if (r.ok) {
        message.success(`✓ ${h.name} 连通 · 延迟 ${r.latency_ms} ms`)
      } else {
        message.error(`✗ ${h.name} 失败：${r.error}`)
      }
      refresh()
    } catch (e) {
      hide()
      message.error(formatError(e))
    }
  }

  const handleDelete = (h: Host) => {
    Modal.confirm({
      title: `确认删除主机 ${h.name}？`,
      content: `IP: ${h.ip}`,
      okType: 'danger',
      onOk: async () => {
        try {
          await deleteHost(h.id)
          message.success('已删除')
          refresh()
        } catch (e) {
          message.error(formatError(e))
        }
      },
    })
  }

  const onlineCount = data.filter((h) => h.status === 'online').length
  const offlineCount = data.filter((h) => h.status === 'offline').length
  const runningInstances = data.reduce((sum, h) => sum + (h.running_instance_count || 0), 0)

  return (
    <section className="page-shell">
      <PageHeader
        compact
        eyebrow="Hosts"
        title="主机资源"
        description="管理用于部署 Java 服务的目标主机，连通性、SSH 凭证和运行实例数量会直接影响发布成功率。"
        actions={(
          <>
            <Button type="primary" icon={<PlusOutlined />} onClick={() => { setEditing(null); setFormOpen(true) }}>新增主机</Button>
            <Button icon={<ReloadOutlined />} onClick={refresh} loading={loading}>刷新</Button>
          </>
        )}
      />

      <StatGrid>
        <StatCard label="Total" value={data.length} description="已纳管主机" tone="info" />
        <StatCard label="Online" value={onlineCount} description="最近一次测连在线" tone="success" />
        <StatCard label="Offline" value={offlineCount} description="需要排查 SSH / 网络" tone={offlineCount > 0 ? 'danger' : 'default'} />
        <StatCard label="Runtime" value={runningInstances} description="远端运行实例计数" tone="warning" />
      </StatGrid>

      <Card className="surface-card">
        <div className="table-toolbar">
          <div className="toolbar-hint">建议新增后先测连；凭证变更会触发重新 TOFU。</div>
        </div>
        <Table<Host>
          rowKey="id"
          loading={loading}
          dataSource={data}
          pagination={data.length > 20 ? { pageSize: 20, size: 'small' } : false}
          locale={{
            emptyText: <EmptyState title="还没有主机" description="先新增一台部署目标主机，再绑定到应用服务。" />,
          }}
          columns={[
            { title: 'ID', dataIndex: 'id', width: 60 },
            { title: '名称', dataIndex: 'name' },
            { title: 'IP', dataIndex: 'ip', render: (v) => <code>{v}</code> },
            { title: '端口', dataIndex: 'port', width: 80 },
            { title: '认证', dataIndex: 'auth_type', width: 90, render: (v) => <Tag>{v}</Tag> },
            { title: '用户', dataIndex: 'username' },
            { title: '状态', dataIndex: 'status', width: 110, render: statusTag },
            {
              title: '运行实例',
              dataIndex: 'running_instance_count',
              width: 110,
              align: 'center',
              render: (n: number) => (
                <StatusTag tone={n > 0 ? 'success' : 'neutral'}>{n || 0}</StatusTag>
              ),
            },
            {
              title: '操作', width: 240,
              render: (_, h) => (
                <Space>
                  <Button size="small" onClick={() => handleTest(h)}>测连</Button>
                  <Button size="small" onClick={() => { setEditing(h); setFormOpen(true) }}>编辑</Button>
                  <Button size="small" danger onClick={() => handleDelete(h)}>删除</Button>
                </Space>
              ),
            },
          ]}
        />
      </Card>
      <HostForm
        open={formOpen}
        editing={editing}
        onClose={() => setFormOpen(false)}
        onSaved={() => { setFormOpen(false); refresh() }}
      />
    </section>
  )
}
