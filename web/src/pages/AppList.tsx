import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Table, Button, Space, Modal, message, Card } from 'antd'

import type { App } from '../types'
import { deleteApp, listApps } from '../api/app'
import { formatError } from '../api/client'
import AppForm from './AppForm'
import { EmptyState, PageHeader, StatCard, StatGrid } from '../components/PageFrame'
import { StatusTag } from '../components/StatusTag'

export default function AppList() {
  const nav = useNavigate()
  const [data, setData] = useState<App[]>([])
  const [loading, setLoading] = useState(false)
  const [formOpen, setFormOpen] = useState(false)
  const [editing, setEditing] = useState<App | null>(null)

  const refresh = async () => {
    setLoading(true)
    try {
      setData(await listApps())
    } catch (e) {
      message.error(formatError(e))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { refresh() }, [])

  const handleDelete = (a: App) => {
    Modal.confirm({
      title: `确认删除应用 ${a.name}？`,
      content: `app_code: ${a.app_code}`,
      okType: 'danger',
      onOk: async () => {
        try {
          await deleteApp(a.id)
          message.success('已删除')
          refresh()
        } catch (e) {
          message.error(formatError(e))
        }
      },
    })
  }

  const gitConfigured = data.filter((a) => !!a.git_url).length
  const dockerMode = data.filter((a) => a.deploy_mode === 'docker' || a.build_mode === 'local-docker' || a.build_mode === 'remote-docker').length
  const ports = new Set(data.map((a) => a.port).filter(Boolean)).size

  return (
    <section className="page-shell">
      <PageHeader
        eyebrow="Applications"
        title="应用服务"
        description="应用按远端 Git 仓库建档，进入详情后扫描 Maven 模块、配置 service、构建制品并绑定部署主机。"
        actions={(
          <>
            <Button type="primary" onClick={() => { setEditing(null); setFormOpen(true) }}>新增应用</Button>
            <Button onClick={refresh} loading={loading}>刷新</Button>
          </>
        )}
      />

      <StatGrid>
        <StatCard label="Total" value={data.length} description="已建档应用" tone="info" />
        <StatCard label="Git Ready" value={gitConfigured} description="已配置 Git 仓库" tone="success" />
        <StatCard label="Docker" value={dockerMode} description="Docker 构建或部署模式" tone="warning" />
        <StatCard label="Ports" value={ports} description="服务端口去重计数" />
      </StatGrid>

      <Card className="surface-card">
        <div className="table-toolbar">
          <div className="table-toolbar-main">
            <Button type="primary" onClick={() => { setEditing(null); setFormOpen(true) }}>新增应用</Button>
            <Button onClick={refresh} loading={loading}>刷新列表</Button>
          </div>
          <div className="toolbar-hint">创建后进入详情页继续配置服务、制品和部署历史。</div>
        </div>
        <Table<App>
          rowKey="id"
          loading={loading}
          dataSource={data}
          pagination={false}
          locale={{
            emptyText: <EmptyState title="还没有应用" description="先创建一个 Git 仓库应用，然后扫描 Maven 模块生成 service。" />,
          }}
          columns={[
            { title: 'ID', dataIndex: 'id', width: 60 },
            { title: '代号', dataIndex: 'app_code', width: 170, render: (v) => <code>{v}</code> },
            { title: '名称', dataIndex: 'name' },
            { title: '模型', width: 120, render: () => <StatusTag tone="info">Git 仓库</StatusTag> },
            { title: '部署路径', dataIndex: 'deploy_path', ellipsis: true, render: (v) => <code>{v}</code> },
            { title: '健康检查', dataIndex: 'health_check_url', width: 180, ellipsis: true, render: (v) => v ? <code>{v}</code> : '-' },
            {
              title: '操作', width: 280,
              render: (_, a) => (
                <Space>
                  <Button size="small" type="primary" onClick={() => nav(`/apps/${a.id}`)}>管理部署</Button>
                  <Button size="small" onClick={() => { setEditing(a); setFormOpen(true) }}>编辑</Button>
                  <Button size="small" danger onClick={() => handleDelete(a)}>删除</Button>
                </Space>
              ),
            },
          ]}
        />
      </Card>
      <AppForm
        open={formOpen}
        editing={editing}
        onClose={() => setFormOpen(false)}
        onSaved={() => { setFormOpen(false); refresh() }}
      />
    </section>
  )
}
