import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Table, Button, Space, Modal, message, Typography, Card, Tag } from 'antd'

import type { App } from '../types'
import { deleteApp, listApps } from '../api/app'
import { formatError } from '../api/client'
import AppForm from './AppForm'

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

  return (
    <div>
      <Typography.Title level={4} style={{ marginTop: 0 }}>应用管理</Typography.Title>
      <Card>
        <Space style={{ marginBottom: 16 }}>
          <Button type="primary" onClick={() => { setEditing(null); setFormOpen(true) }}>+ 新增应用</Button>
          <Button onClick={refresh}>刷新</Button>
        </Space>
        <Table<App>
          rowKey="id"
          loading={loading}
          dataSource={data}
          pagination={false}
          columns={[
            { title: 'ID', dataIndex: 'id', width: 60 },
            { title: '代号', dataIndex: 'app_code', width: 160, render: (v) => <code>{v}</code> },
            { title: '名称', dataIndex: 'name' },
            { title: '类型', dataIndex: 'app_type', width: 110, render: (v) => <Tag>{v}</Tag> },
            { title: '端口', dataIndex: 'port', width: 80 },
            { title: '部署路径', dataIndex: 'deploy_path', ellipsis: true },
            { title: '健康检查', dataIndex: 'health_check_url', width: 180, ellipsis: true },
            {
              title: '操作', width: 260,
              render: (_, a) => (
                <Space>
                  <Button size="small" type="link" onClick={() => nav(`/apps/${a.id}`)}>管理部署</Button>
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
    </div>
  )
}
