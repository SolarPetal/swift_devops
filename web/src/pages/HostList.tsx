import { useEffect, useState } from 'react'
import { Table, Button, Space, Tag, Modal, message, Typography, Card } from 'antd'

import type { Host, TestResult } from '../types'
import { deleteHost, listHosts, testHost } from '../api/host'
import { formatError } from '../api/client'
import HostForm from './HostForm'

const statusTag = (s: Host['status']) => {
  const conf: Record<Host['status'], { color: string; text: string }> = {
    online: { color: 'green', text: '● 在线' },
    offline: { color: 'red', text: '● 离线' },
    unknown: { color: 'default', text: '○ 未知' },
  }
  const c = conf[s] ?? conf.unknown
  return <Tag color={c.color}>{c.text}</Tag>
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

  return (
    <div>
      <Typography.Title level={4} style={{ marginTop: 0 }}>主机管理</Typography.Title>
      <Card>
        <Space style={{ marginBottom: 16 }}>
          <Button type="primary" onClick={() => { setEditing(null); setFormOpen(true) }}>+ 新增主机</Button>
          <Button onClick={refresh}>刷新</Button>
        </Space>
        <Table<Host>
          rowKey="id"
          loading={loading}
          dataSource={data}
          pagination={false}
          columns={[
            { title: 'ID', dataIndex: 'id', width: 60 },
            { title: '名称', dataIndex: 'name' },
            { title: 'IP', dataIndex: 'ip' },
            { title: '端口', dataIndex: 'port', width: 80 },
            { title: '认证', dataIndex: 'auth_type', width: 80 },
            { title: '用户', dataIndex: 'username' },
            { title: '分组', dataIndex: 'group_tag', width: 80 },
            { title: '状态', dataIndex: 'status', width: 100, render: statusTag },
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
    </div>
  )
}
