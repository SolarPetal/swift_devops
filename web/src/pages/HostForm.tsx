import { useEffect } from 'react'
import { Modal, Form, Input, InputNumber, Radio, message, Alert } from 'antd'

import type { Host, HostInput } from '../types'
import { createHost, updateHost } from '../api/host'
import { formatError } from '../api/client'

type Props = {
  open: boolean
  editing: Host | null
  onClose: () => void
  onSaved: () => void
}

export default function HostForm({ open, editing, onClose, onSaved }: Props) {
  const [form] = Form.useForm<HostInput>()
  const isEdit = !!editing

  useEffect(() => {
    if (!open) return
    form.resetFields()
    if (editing) {
      form.setFieldsValue({
        name: editing.name,
        ip: editing.ip,
        port: editing.port,
        auth_type: editing.auth_type,
        username: editing.username,
        group_tag: editing.group_tag,
        tags: editing.tags,
        java_path: editing.java_path,
      })
    } else {
      form.setFieldsValue({
        port: 22,
        auth_type: 'password',
        username: 'root',
        group_tag: '',
        java_path: '/usr/bin/java',
      })
    }
  }, [open, editing, form])

  const submit = async () => {
    try {
      const v = await form.validateFields()
      if (isEdit) {
        await updateHost(editing!.id, v)
        message.success('已更新')
      } else {
        await createHost(v)
        message.success('已创建')
      }
      onSaved()
    } catch (e) {
      // antd Form 校验失败的对象上有 errorFields，直接吞，Form 自己显示提示
      if ((e as any)?.errorFields) return
      message.error(formatError(e))
    }
  }

  return (
    <Modal
      title={isEdit ? `编辑主机 #${editing!.id}` : '新增主机'}
      open={open}
      onCancel={onClose}
      onOk={submit}
      okText="保存"
      cancelText="取消"
      width={560}
      destroyOnClose
      maskClosable={false}
      keyboard={false}
    >
      <Form form={form} layout="vertical">
        <Form.Item name="name" label="名称" rules={[{ required: true }]}>
          <Input placeholder="如：prod-web-01" />
        </Form.Item>
        <Form.Item name="ip" label="IP 地址" rules={[{ required: true }]}>
          <Input placeholder="如：192.168.1.10" />
        </Form.Item>
        <Form.Item name="port" label="SSH 端口" rules={[{ required: true }]}>
          <InputNumber min={1} max={65535} style={{ width: 160 }} />
        </Form.Item>
        <Form.Item name="username" label="SSH 用户" rules={[{ required: true }]}>
          <Input />
        </Form.Item>
        <Form.Item name="auth_type" label="认证方式" rules={[{ required: true }]}>
          <Radio.Group>
            <Radio.Button value="password">密码</Radio.Button>
            <Radio.Button value="key">SSH 私钥</Radio.Button>
          </Radio.Group>
        </Form.Item>
        <Form.Item shouldUpdate noStyle>
          {() => {
            const t = form.getFieldValue('auth_type')
            return t === 'password' ? (
              <Form.Item
                name="password"
                label={isEdit ? '密码（留空保留旧值）' : '密码'}
                rules={isEdit ? [] : [{ required: true }]}
              >
                <Input.Password />
              </Form.Item>
            ) : (
              <>
                <Form.Item
                  name="private_key"
                  label={isEdit ? '私钥 PEM（留空保留旧值）' : '私钥 PEM'}
                  rules={isEdit ? [] : [{ required: true }]}
                >
                  <Input.TextArea rows={5} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----..." />
                </Form.Item>
                <Form.Item name="passphrase" label="私钥口令（可选）">
                  <Input.Password />
                </Form.Item>
              </>
            )
          }}
        </Form.Item>
        <Form.Item name="group_tag" label="蓝绿分组（可选）">
          <Radio.Group>
            <Radio.Button value="">未分组</Radio.Button>
            <Radio.Button value="blue">Blue</Radio.Button>
            <Radio.Button value="green">Green</Radio.Button>
          </Radio.Group>
        </Form.Item>
        <Form.Item
          name="java_path"
          label="Java 可执行路径"
          tooltip="远端 Java 路径。两种都支持：JDK 安装目录（如 /usr/local/jdk-21.0.2）或 java 可执行文件（如 /usr/bin/java）。env_check 阶段会自动 resolve。默认 /usr/bin/java。"
          rules={[
            {
              pattern: /^(\/.*)?$/,
              message: '必须以 / 开头（绝对路径），或留空走默认',
            },
          ]}
        >
          <Input placeholder="/usr/bin/java 或 /usr/local/jdk-21.0.2（目录也行）" />
        </Form.Item>
        {isEdit && (
          <Alert
            type="info"
            showIcon
            message="如果修改了凭证，host_key 会被清空，下次测连会重新 TOFU。"
          />
        )}
      </Form>
    </Modal>
  )
}
