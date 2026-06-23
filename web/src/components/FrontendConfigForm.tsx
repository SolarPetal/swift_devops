import { Alert, Collapse, Form, Input, InputNumber, Select, Switch } from 'antd'
import type { FrontendAppConfig, FrontendAppConfigInput } from '../types'
import { FormActions } from './FormActions'

const { TextArea } = Input

type Props = {
  config: FrontendAppConfig | null
  saving: boolean
  onSave: (input: FrontendAppConfigInput) => Promise<void>
  onPreview?: () => void
}

export function FrontendConfigForm({ config, saving, onSave, onPreview }: Props) {
  const [form] = Form.useForm<FrontendAppConfigInput>()

  const handleSave = async () => {
    const values = await form.validateFields()
    await onSave({
      service_code: values.service_code || 'web',
      package_manager: values.package_manager || 'auto',
      install_command: values.install_command || '',
      build_command: values.build_command || '',
      dist_dir: values.dist_dir || 'dist',
      node_image: values.node_image || 'node:20-alpine',
      nginx_image: values.nginx_image || 'nginx:1.27-alpine',
      spa_fallback: Boolean(values.spa_fallback),
      container_name: values.container_name || '',
      target_port: Number(values.target_port || 80),
      dockerfile: values.dockerfile || '',
      nginx_config: values.nginx_config || '',
      docker_build_args: values.docker_build_args || '',
      docker_run_args: values.docker_run_args || '',
    })
  }

  return (
    <Form
      form={form}
      layout="vertical"
      initialValues={
        config || {
          service_code: 'web',
          package_manager: 'auto',
          build_command: 'npm run build',
          dist_dir: 'dist',
          node_image: 'node:20-alpine',
          nginx_image: 'nginx:1.27-alpine',
          spa_fallback: true,
          target_port: 80,
        }
      }
    >
      <Alert
        type="info"
        showIcon
        message="前端服务配置只需配置一次，后续可直接部署"
        style={{ marginBottom: 16 }}
      />

      <Form.Item
        label="Service Code"
        name="service_code"
        tooltip="标识前端服务，多个前端应用用不同 code 区分"
        rules={[{ required: true }]}
      >
        <Input placeholder="web" />
      </Form.Item>

      <Form.Item
        label="构建命令"
        name="build_command"
        tooltip="在 Node 容器内执行，产物输出到 dist 目录"
        rules={[{ required: true }]}
      >
        <Input placeholder="npm run build" />
      </Form.Item>

      <Form.Item
        label="产物目录"
        name="dist_dir"
        tooltip="构建产物所在目录，会拷贝到 Nginx 镜像"
        rules={[{ required: true }]}
      >
        <Input placeholder="dist" />
      </Form.Item>

      <Form.Item
        label="容器名"
        name="container_name"
        tooltip="前端容器名称，通常为 <app_code>-<service_code>"
        rules={[{ required: true }]}
      >
        <Input placeholder="demo-app-web" />
      </Form.Item>

      <Form.Item
        label="容器端口"
        name="target_port"
        tooltip="Nginx 容器内监听端口，通常为 80"
        rules={[{ required: true }]}
      >
        <InputNumber min={1} max={65535} style={{ width: '100%' }} />
      </Form.Item>

      <Form.Item
        label="SPA 模式"
        name="spa_fallback"
        valuePropName="checked"
        tooltip="前端路由 404 时回退到 index.html (适用于 Vue/React SPA)"
      >
        <Switch />
      </Form.Item>

      <Collapse ghost style={{ marginTop: 16 }}>
        <Collapse.Panel header="高级选项 (通常不需要修改)" key="advanced">
          <Form.Item label="包管理器" name="package_manager">
            <Select
              options={[
                { label: '自动检测 (推荐)', value: 'auto' },
                { label: 'npm', value: 'npm' },
                { label: 'pnpm', value: 'pnpm' },
                { label: 'yarn', value: 'yarn' },
              ]}
            />
          </Form.Item>

          <Form.Item
            label="安装命令"
            name="install_command"
            tooltip="留空则根据 package_manager 自动推断"
          >
            <Input placeholder="自动推断: npm ci / pnpm install --frozen-lockfile" />
          </Form.Item>

          <Form.Item label="Node 镜像" name="node_image">
            <Input placeholder="node:20-alpine" />
          </Form.Item>

          <Form.Item label="Nginx 镜像" name="nginx_image">
            <Input placeholder="nginx:1.27-alpine" />
          </Form.Item>

          <Form.Item
            label="自定义 Dockerfile"
            name="dockerfile"
            tooltip="留空使用默认多阶段构建模板"
          >
            <TextArea rows={6} placeholder="留空使用默认模板" />
          </Form.Item>

          <Form.Item
            label="自定义 Nginx 配置"
            name="nginx_config"
            tooltip="留空使用默认 SPA 配置"
          >
            <TextArea rows={6} placeholder="留空使用默认配置" />
          </Form.Item>

          <Form.Item
            label="Docker Build 参数"
            name="docker_build_args"
            tooltip="传递给 docker build 命令"
          >
            <Input placeholder="--build-arg NODE_ENV=production" />
          </Form.Item>

          <Form.Item
            label="Docker Run 参数"
            name="docker_run_args"
            tooltip="传递给 docker run 命令"
          >
            <Input placeholder="-e TZ=Asia/Shanghai" />
          </Form.Item>
        </Collapse.Panel>
      </Collapse>

      <FormActions
        onPrimary={handleSave}
        primaryText="保存配置"
        primaryLoading={saving}
        secondaryText={onPreview ? '预览模板' : undefined}
        onSecondary={onPreview}
      />
    </Form>
  )
}
