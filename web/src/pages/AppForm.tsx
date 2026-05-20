import { useEffect } from 'react'
import { Modal, Form, Input, InputNumber, Radio, message } from 'antd'

import type { App, AppInput } from '../types'
import { createApp, updateApp } from '../api/app'
import { formatError } from '../api/client'

type Props = {
  open: boolean
  editing: App | null
  onClose: () => void
  onSaved: () => void
}

export default function AppForm({ open, editing, onClose, onSaved }: Props) {
  const [form] = Form.useForm<AppInput>()
  const isEdit = !!editing

  useEffect(() => {
    if (!open) return
    form.resetFields()
    if (editing) {
      form.setFieldsValue({
        app_code: editing.app_code,
        name: editing.name,
        app_type: editing.app_type || 'jar',
        git_url: editing.git_url,
        git_cred_id: editing.git_cred_id,
        deploy_path: editing.deploy_path,
        port: editing.port,
        health_check_url: editing.health_check_url,
        jvm_args: editing.jvm_args,
        env_vars: editing.env_vars,
        systemd_user: editing.systemd_user,
      })
    } else {
      form.setFieldsValue({
        app_type: 'jar',
        port: 8080,
        health_check_url: '/actuator/health',
        jvm_args: '-Xms512m -Xmx512m',
      })
    }
  }, [open, editing, form])

  const submit = async () => {
    try {
      const v = await form.validateFields()
      if (isEdit) {
        await updateApp(editing!.id, v)
        message.success('已更新')
      } else {
        await createApp(v)
        message.success('已创建')
      }
      onSaved()
    } catch (e) {
      if ((e as any)?.errorFields) return
      message.error(formatError(e))
    }
  }

  return (
    <Modal
      title={isEdit ? `编辑应用 #${editing!.id}` : '新增应用'}
      open={open}
      onCancel={onClose}
      onOk={submit}
      okText="保存"
      cancelText="取消"
      width={620}
      destroyOnClose
    >
      <Form form={form} layout="vertical">
        <Form.Item
          name="app_code"
          label="应用代号 (app_code)"
          rules={[
            { required: true },
            {
              pattern: /^[a-z][a-z0-9-]{1,49}$/,
              message: '小写字母开头，2-50 位，仅小写字母/数字/连字符',
            },
          ]}
          tooltip="将作为 systemd 单元名：devops-<app_code>.service"
        >
          <Input placeholder="如：user-service" disabled={isEdit} />
        </Form.Item>
        <Form.Item name="name" label="应用名称" rules={[{ required: true }]}>
          <Input placeholder="如：用户服务" />
        </Form.Item>
        <Form.Item name="app_type" label="应用类型" rules={[{ required: true }]}>
          <Radio.Group>
            <Radio.Button value="jar">单体 Jar</Radio.Button>
            <Radio.Button value="spring-cloud">Spring Cloud</Radio.Button>
          </Radio.Group>
        </Form.Item>
        <Form.Item name="deploy_path" label="部署绝对路径" rules={[{ required: true }]}>
          <Input placeholder="如：/opt/apps/user-service" />
        </Form.Item>
        <Form.Item name="port" label="服务端口" rules={[{ required: true }]}>
          <InputNumber min={1} max={65535} style={{ width: 160 }} />
        </Form.Item>
        <Form.Item name="health_check_url" label="健康检查路径">
          <Input placeholder="/actuator/health" />
        </Form.Item>
        <Form.Item name="jvm_args" label="JVM 启动参数">
          <Input placeholder="-Xms512m -Xmx512m -XX:+UseG1GC" />
        </Form.Item>
        <Form.Item
          name="systemd_user"
          label="systemd 运行用户（可选）"
          tooltip="留空则用 SSH 登录账号（一般 root）；填写后写入 unit 的 User= 字段，比如 deployer / java"
          rules={[
            {
              pattern: /^[a-z_][a-z0-9_-]{0,31}$/,
              message: '小写字母/下划线开头，1-32 位，仅含小写字母/数字/下划线/连字符',
            },
          ]}
        >
          <Input placeholder="留空 = 用 SSH 账号；如：deployer / java" />
        </Form.Item>
        <Form.Item
          name="env_vars"
          label="环境变量 (JSON)"
          tooltip='形如 {"SPRING_PROFILES_ACTIVE":"prod","LOG_LEVEL":"INFO"}'
        >
          <Input.TextArea rows={3} placeholder='{"SPRING_PROFILES_ACTIVE":"prod"}' />
        </Form.Item>
        <Form.Item name="git_url" label="Git 仓库地址（可选）">
          <Input placeholder="https://github.com/org/repo.git" />
        </Form.Item>
        <Form.Item name="git_cred_id" label="Git 凭证 ID（可选）">
          <Input placeholder="后续 Git 凭证管理上来后再选" />
        </Form.Item>
      </Form>
    </Modal>
  )
}
