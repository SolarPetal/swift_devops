import { useEffect, useState } from 'react'
import { Modal, Form, Input, InputNumber, Radio, Select, Divider, Typography, message } from 'antd'

import type { App, AppInput, Host } from '../types'
import { createApp, updateApp } from '../api/app'
import { listHosts } from '../api/host'
import { formatError } from '../api/client'

type Props = {
  open: boolean
  editing: App | null
  onClose: () => void
  onSaved: () => void
}

export default function AppForm({ open, editing, onClose, onSaved }: Props) {
  const [form] = Form.useForm<AppInput>()
  const [hosts, setHosts] = useState<Host[]>([])
  const isEdit = !!editing

  useEffect(() => {
    if (!open) return
    listHosts().then(setHosts).catch(() => {})
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
        java_path: editing.java_path,
        nginx_host_id: editing.nginx_host_id || 0,
        nginx_upstream_name: editing.nginx_upstream_name,
        active_group: editing.active_group,
      })
    } else {
      form.setFieldsValue({
        app_type: 'jar',
        port: 8080,
        health_check_url: '/actuator/health',
        jvm_args: '-Xms512m -Xmx512m',
        java_path: '',
        nginx_host_id: 0,
        nginx_upstream_name: '',
        active_group: '',
      })
    }
  }, [open, editing, form])

  const submit = async () => {
    try {
      const v = await form.validateFields()
      // 前端再卡一道：nginx_host_id 与 nginx_upstream_name 同填同空
      const hasHost = !!v.nginx_host_id && v.nginx_host_id > 0
      const hasName = !!v.nginx_upstream_name && v.nginx_upstream_name.trim() !== ''
      if (hasHost !== hasName) {
        message.error('Nginx 主机和 upstream 名要么同时填，要么同时留空')
        return
      }
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
          name="java_path"
          label="Java 路径覆盖（可选）"
          tooltip="留空 = 沿用主机的 java_path。仅当同台主机要跑不同 JDK 版本的应用时填写。"
          rules={[
            {
              pattern: /^(\/.*)?$/,
              message: '必须以 / 开头（绝对路径），或留空走主机默认',
            },
          ]}
        >
          <Input placeholder="留空沿用主机配置；或填 /opt/java-17/bin/java" />
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

        <Divider orientation="left" plain>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            蓝绿部署（可选，留空则不启用）
          </Typography.Text>
        </Divider>
        <Form.Item
          name="nginx_host_id"
          label="Nginx 主机"
          tooltip="选择跑 nginx 的主机；启用蓝绿时必填"
        >
          <Select
            allowClear
            placeholder="不启用蓝绿请留空"
            options={[
              { value: 0, label: '— 不启用 —' },
              ...hosts.map((h) => ({
                value: h.id,
                label: `${h.name} (${h.ip})${h.status === 'online' ? ' ● 在线' : ''}`,
              })),
            ]}
          />
        </Form.Item>
        <Form.Item
          name="nginx_upstream_name"
          label="Upstream 名"
          tooltip="将写入 /etc/nginx/conf.d/swift-devops-<app_code>.conf 中的 upstream <name> { ... }"
          rules={[
            {
              pattern: /^([A-Za-z][A-Za-z0-9_-]{1,99})?$/,
              message: '字母开头，2-100 位，仅含字母/数字/下划线/连字符',
            },
          ]}
        >
          <Input placeholder="如：user-svc-backend（留空表示不启用）" />
        </Form.Item>
        <Form.Item
          name="active_group"
          label="当前活跃组"
          tooltip="蓝绿部署成功后由系统更新；首次启用蓝绿前可手动声明现状（一般留空，让首次蓝绿默认部署到 blue）"
        >
          <Radio.Group>
            <Radio value="">未设置</Radio>
            <Radio value="blue">Blue</Radio>
            <Radio value="green">Green</Radio>
          </Radio.Group>
        </Form.Item>
      </Form>
    </Modal>
  )
}
