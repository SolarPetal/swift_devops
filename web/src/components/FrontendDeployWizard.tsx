import { useState } from 'react'
import { Alert, Button, Checkbox, Collapse, Form, Input, InputNumber, Modal, Select, Segmented, Space, Switch } from 'antd'
import { GlobalOutlined, LinkOutlined } from '@ant-design/icons'
import type { App, FrontendAppConfig, FrontendDeployInput, Host } from '../types'
import { FormActions } from './FormActions'

type DeployMode = 'domain' | 'direct'

type Props = {
  app: App
  config: FrontendAppConfig | null
  hosts: Host[]
  open: boolean
  onCancel: () => void
  onSubmit: (input: FrontendDeployInput) => Promise<void>
  deploying: boolean
}

export function FrontendDeployWizard({ app, config, hosts, open, onCancel, onSubmit, deploying }: Props) {
  const [form] = Form.useForm<FrontendDeployInput & { deploy_mode: DeployMode }>()
  const [deployMode, setDeployMode] = useState<DeployMode>('domain')

  const selectedHostId = Form.useWatch('host_id', form)
  const selectedHost = hosts.find((h) => h.id === selectedHostId)
  const hostPort = Form.useWatch('host_port', form)
  const domain = Form.useWatch('domain', form)
  const https = Form.useWatch('https', form)

  const handleModeChange = (mode: DeployMode) => {
    setDeployMode(mode)
    if (mode === 'direct') {
      form.setFieldsValue({
        domain: '',
        https: false,
        cert_path: '',
        key_path: '',
        apply_gateway: false,
        force_recreate_gateway: false,
        host_port: hostPort || 8080,
      })
    } else {
      form.setFieldsValue({
        host_port: undefined,
        apply_gateway: true,
      })
    }
  }

  const handleSubmit = async () => {
    const values = await form.validateFields()
    const payload: FrontendDeployInput = {
      host_id: values.host_id,
      service_code: values.service_code || config?.service_code || 'web',
      git_ref: values.git_ref || app.git_ref || 'main',
    }

    if (values.cred_id && Number(values.cred_id) > 0) {
      payload.cred_id = Number(values.cred_id)
    }

    if (deployMode === 'domain') {
      payload.domain = String(values.domain || '').trim()
      payload.https = Boolean(values.https)
      if (payload.https) {
        payload.cert_path = values.cert_path || ''
        payload.key_path = values.key_path || ''
      }
      payload.apply_gateway = Boolean(values.apply_gateway)
      payload.force_recreate_gateway = Boolean(values.force_recreate_gateway)
    } else {
      payload.host_port = Number(values.host_port || 8080)
    }

    await onSubmit(payload)
  }

  return (
    <Modal
      title="部署前端应用"
      open={open}
      onCancel={onCancel}
      footer={null}
      width={680}
      maskClosable={false}
      keyboard={false}
    >
      <Form
        form={form}
        layout="vertical"
        initialValues={{
          deploy_mode: 'domain',
          host_id: hosts[0]?.id,
          service_code: config?.service_code || 'web',
          git_ref: app.git_ref || 'main',
          cred_id: Number(app.git_cred_id || 0),
          host_port: 8080,
          https: false,
          apply_gateway: true,
          force_recreate_gateway: false,
        }}
      >
        <Segmented
          value={deployMode}
          onChange={handleModeChange}
          options={[
            { label: '有域名部署 (生产)', value: 'domain', icon: <GlobalOutlined /> },
            { label: '无域名测试 (直连)', value: 'direct', icon: <LinkOutlined /> },
          ]}
          block
          style={{ marginBottom: 16 }}
        />

        {deployMode === 'domain' ? (
          <>
            <Alert
              type="info"
              showIcon
              message="有域名部署会通过 Gateway 容器统一暴露 80/443 端口"
              style={{ marginBottom: 16 }}
            />

            <Form.Item label="目标主机" name="host_id" rules={[{ required: true }]}>
              <Select
                options={hosts.map((h) => ({
                  label: `${h.name} (${h.ip})`,
                  value: h.id,
                }))}
                placeholder="选择部署主机"
              />
            </Form.Item>

            <Form.Item
              label="域名"
              name="domain"
              rules={[{ required: true, message: '请输入域名' }]}
              tooltip="外网访问域名，需要 DNS 解析到该主机 IP"
            >
              <Input placeholder="app.example.com" />
            </Form.Item>

            <Form.Item
              label="Git Ref"
              name="git_ref"
              tooltip="分支、标签或 commit SHA"
            >
              <Input placeholder="main" />
            </Form.Item>

            <Form.Item label="HTTPS" name="https" valuePropName="checked">
              <Switch />
            </Form.Item>

            {https && (
              <>
                <Form.Item
                  label="证书路径"
                  name="cert_path"
                  rules={[{ required: true }]}
                  tooltip="目标主机上的绝对路径"
                >
                  <Input placeholder="/etc/nginx/certs/example.com.crt" />
                </Form.Item>
                <Form.Item
                  label="私钥路径"
                  name="key_path"
                  rules={[{ required: true }]}
                >
                  <Input placeholder="/etc/nginx/certs/example.com.key" />
                </Form.Item>
              </>
            )}

            <Form.Item
              label="立即生效"
              name="apply_gateway"
              valuePropName="checked"
              tooltip="取消勾选则只更新 route 配置，需要手动 Apply Gateway 才生效"
            >
              <Checkbox>部署完成后自动 Apply Gateway (推荐)</Checkbox>
            </Form.Item>

            <Collapse ghost>
              <Collapse.Panel header="Gateway 高级选项" key="gateway">
                <Form.Item
                  label="强制重建 Gateway"
                  name="force_recreate_gateway"
                  valuePropName="checked"
                  tooltip="Gateway 容器启动失败时勾选此项"
                >
                  <Checkbox>强制删除并重建 Gateway 容器</Checkbox>
                </Form.Item>
              </Collapse.Panel>
            </Collapse>
          </>
        ) : (
          <>
            <Alert
              type="warning"
              showIcon
              message="无域名部署仅适合测试环境，通过 IP:端口 直接访问容器"
              style={{ marginBottom: 16 }}
            />

            <Form.Item label="目标主机" name="host_id" rules={[{ required: true }]}>
              <Select
                options={hosts.map((h) => ({
                  label: `${h.name} (${h.ip})`,
                  value: h.id,
                }))}
              />
            </Form.Item>

            <Form.Item
              label="宿主机端口"
              name="host_port"
              rules={[{ required: true }]}
              tooltip="前端容器会绑定到宿主机此端口"
            >
              <InputNumber min={1024} max={65535} placeholder="8080" style={{ width: '100%' }} />
            </Form.Item>

            <Form.Item label="Git Ref" name="git_ref" tooltip="分支、标签或 commit SHA">
              <Input placeholder="main" />
            </Form.Item>

            <Alert
              type="info"
              showIcon
              message={
                <span>
                  部署成功后，访问地址：
                  <code style={{ marginLeft: 8 }}>
                    http://{selectedHost?.ip || '<host_ip>'}:{hostPort || 8080}
                  </code>
                </span>
              }
              style={{ marginTop: 16 }}
            />
          </>
        )}

        <FormActions
          onPrimary={handleSubmit}
          primaryText="开始部署"
          primaryLoading={deploying}
          cancelText="取消"
          onCancel={onCancel}
        />
      </Form>
    </Modal>
  )
}
