import { useEffect, useState } from 'react'
import { Button, Card, Modal, Form, Input, Radio, Space, Table, Tag, Typography, message } from 'antd'

import type { GitCredential, GitCredentialInput } from '../types'
import { createGitCred, deleteGitCred, listGitCreds, updateGitCred } from '../api/gitcred'
import { formatError } from '../api/client'

export default function GitCredList() {
  const [list, setList] = useState<GitCredential[]>([])
  const [loading, setLoading] = useState(false)
  const [formOpen, setFormOpen] = useState(false)
  const [editing, setEditing] = useState<GitCredential | null>(null)
  const [form] = Form.useForm<GitCredentialInput>()

  const refresh = async () => {
    setLoading(true)
    try { setList(await listGitCreds()) }
    catch (e) { message.error(formatError(e)) }
    finally { setLoading(false) }
  }
  useEffect(() => { refresh() }, [])

  const openNew = () => {
    setEditing(null)
    form.resetFields()
    form.setFieldsValue({ type: 'token', username: '' })
    setFormOpen(true)
  }
  const openEdit = (c: GitCredential) => {
    setEditing(c)
    form.resetFields()
    form.setFieldsValue({
      name: c.name,
      type: c.type,
      username: c.username,
      secret: '', // 编辑时留空保留旧值
    })
    setFormOpen(true)
  }

  const submit = async () => {
    try {
      const v = await form.validateFields()
      if (editing) {
        await updateGitCred(editing.id, v)
        message.success('已更新')
      } else {
        await createGitCred(v)
        message.success('已创建')
      }
      setFormOpen(false)
      refresh()
    } catch (e) {
      if ((e as any)?.errorFields) return
      message.error(formatError(e))
    }
  }

  const remove = (c: GitCredential) => {
    Modal.confirm({
      title: `删除凭证 ${c.name}？`,
      content: '历史构建中引用此凭证的记录不受影响，但新构建无法再选此凭证。',
      okType: 'danger',
      onOk: async () => {
        try {
          await deleteGitCred(c.id)
          message.success('已删除')
          refresh()
        } catch (e) { message.error(formatError(e)) }
      },
    })
  }

  return (
    <Card title="Git 凭证管理" extra={<Space>
      <Button type="primary" onClick={openNew}>+ 新增</Button>
      <Button onClick={refresh}>刷新</Button>
    </Space>}>
      <Table<GitCredential>
        rowKey="id"
        dataSource={list}
        loading={loading}
        pagination={false}
        locale={{ emptyText: '还没有凭证。点击「+ 新增」创建第一个 Git 凭证。' }}
        columns={[
          { title: 'ID', dataIndex: 'id', width: 60 },
          { title: '名称', dataIndex: 'name' },
          {
            title: '类型', dataIndex: 'type', width: 110,
            render: (t: string) => t === 'token'
              ? <Tag color="blue">HTTPS Token</Tag>
              : <Tag color="purple">SSH Key</Tag>,
          },
          { title: '用户名', dataIndex: 'username', width: 150,
            render: (u: string) => u ? <code>{u}</code> : <Typography.Text type="secondary">-</Typography.Text>,
          },
          {
            title: 'Secret', dataIndex: 'has_secret', width: 120,
            render: (h: boolean) => h
              ? <Tag color="green">● 已加密</Tag>
              : <Tag color="default">○ 未设置</Tag>,
          },
          { title: '创建时间', dataIndex: 'created_at', width: 170,
            render: (s: string) => new Date(s).toLocaleString(),
          },
          {
            title: '操作', width: 140,
            render: (_, c) => (
              <Space>
                <Button size="small" onClick={() => openEdit(c)}>编辑</Button>
                <Button size="small" danger onClick={() => remove(c)}>删除</Button>
              </Space>
            ),
          },
        ]}
      />

      <Modal
        title={editing ? `编辑凭证 #${editing.id}` : '新增 Git 凭证'}
        open={formOpen}
        onCancel={() => setFormOpen(false)}
        onOk={submit}
        okText="保存"
        cancelText="取消"
        width={560}
        destroyOnClose
        maskClosable={false}
        keyboard={false}
      >
        <Form form={form} layout="vertical">
          <Form.Item
            name="name"
            label="名称"
            rules={[{ required: true }]}
            tooltip="便于识别，建议含组织 / 仓库范围，如 github-org-readonly"
          >
            <Input placeholder="如：github-org-readonly" />
          </Form.Item>
          <Form.Item name="type" label="类型" rules={[{ required: true }]}>
            <Radio.Group>
              <Radio.Button value="token">HTTPS Token (PAT)</Radio.Button>
              <Radio.Button value="ssh_key">SSH 私钥</Radio.Button>
            </Radio.Group>
          </Form.Item>
          <Form.Item shouldUpdate noStyle>
            {() => {
              const t = form.getFieldValue('type')
              return (
                <>
                  <Form.Item
                    name="username"
                    label="用户名"
                    tooltip={t === 'token'
                      ? 'GitHub 一般是账号；GitLab 可填 oauth2'
                      : 'SSH 协议一般填 git'}
                  >
                    <Input placeholder={t === 'token' ? 'your-github-username' : 'git'} />
                  </Form.Item>
                  <Form.Item
                    name="secret"
                    label={editing
                      ? (t === 'token' ? 'Token（留空保留旧值）' : '私钥 PEM（留空保留旧值）')
                      : (t === 'token' ? 'Token' : '私钥 PEM')}
                    rules={editing ? [] : [{ required: true }]}
                  >
                    {t === 'token'
                      ? <Input.Password placeholder="ghp_xxxxxx" />
                      : <Input.TextArea rows={6} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----..." />}
                  </Form.Item>
                </>
              )
            }}
          </Form.Item>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            🔒 Secret 用 AES-GCM 加密存储（同主机凭证体系），界面始终不回显原文。
          </Typography.Text>
        </Form>
      </Modal>
    </Card>
  )
}
