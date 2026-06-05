import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Modal, Form, Input, Select, Tabs, Typography, message } from 'antd'

import type { App, AppInput, GitCredential } from '../types'
import { createApp, updateApp } from '../api/app'
import { listGitCreds } from '../api/gitcred'
import { formatError } from '../api/client'

type Props = {
  open: boolean
  editing: App | null
  onClose: () => void
  onSaved: () => void
}

function normalizeRefs(value?: string[], fallback?: string) {
  const out: string[] = []
  const add = (ref?: string) => {
    const v = (ref || '').trim()
    if (!v || out.includes(v)) return
    out.push(v)
  }
  add(fallback)
  ;(value || []).forEach(add)
  if (out.length === 0) out.push('main')
  return out
}

export default function AppForm({ open, editing, onClose, onSaved }: Props) {
  const [form] = Form.useForm<AppInput>()
  const nav = useNavigate()
  const [creds, setCreds] = useState<GitCredential[]>([])
  const [activeTab, setActiveTab] = useState('basic')
  const isEdit = !!editing

  useEffect(() => {
    if (!open) return
    listGitCreds().then(setCreds).catch(() => {})
    form.resetFields()
    setActiveTab('basic')
    if (editing) {
      const refs = normalizeRefs(editing.git_refs, editing.git_ref || 'main')
      form.setFieldsValue({
        app_code: editing.app_code,
        name: editing.name,
        app_type: editing.app_type || 'jar',
        git_url: editing.git_url,
        git_cred_id: editing.git_cred_id,
        git_ref: refs[0],
        git_refs: refs,
        deploy_path: editing.deploy_path,
        health_check_url: editing.health_check_url,
      })
    } else {
      form.setFieldsValue({
        app_type: 'jar',
        health_check_url: '/actuator/health',
        git_url: '',
        git_cred_id: '',
        git_ref: 'main',
        git_refs: ['main'],
      })
    }
  }, [open, editing, form])

  const submit = async () => {
    try {
      const v = await form.validateFields()
      const refs = normalizeRefs(v.git_refs, v.git_ref || 'main')
      const selectedRef = (v.git_ref || '').trim()
      const preservedValues: any = isEdit ? {
        build_mode: editing!.build_mode || 'local-jar',
        build_module: editing!.build_module || '',
        build_jar_pattern: editing!.build_jar_pattern || '',
        jvm_args: editing!.jvm_args || '',
        env_vars: editing!.env_vars || '',
        systemd_user: editing!.systemd_user || '',
        java_path: editing!.java_path || '',
        deploy_mode: editing!.deploy_mode || 'systemd',
        docker_registry: editing!.docker_registry || '',
        docker_image_name: editing!.docker_image_name || '',
        docker_image_tag: editing!.docker_image_tag || 'latest',
        dockerfile: editing!.dockerfile || '',
        docker_build_args: editing!.docker_build_args || '',
        docker_container_name: editing!.docker_container_name || '',
        docker_run_args: editing!.docker_run_args || '',
      } : {
        build_mode: 'local-jar',
        build_module: '',
        build_jar_pattern: '',
        jvm_args: '',
        env_vars: '',
        systemd_user: '',
        java_path: '',
        deploy_mode: 'systemd',
        docker_registry: '',
        docker_image_name: '',
        docker_image_tag: 'latest',
        dockerfile: '',
        docker_build_args: '',
        docker_container_name: '',
        docker_run_args: '',
      }
      const payload = {
        ...preservedValues,
        ...v,
        app_type: v.app_type || (isEdit ? (editing!.app_type || 'jar') : 'jar'),
        git_ref: refs.includes(selectedRef) ? selectedRef : refs[0],
        git_refs: refs,
      }
      if (isEdit) {
        await updateApp(editing!.id, payload)
        message.success('已更新')
      } else {
        const created = await createApp(payload)
        message.success('已创建，正在进入应用详情')
        onSaved()
        nav(`/apps/${created.id}`)
        return
      }
      onSaved()
    } catch (e: any) {
      // 校验失败时把焦点切到出错的 Tab
      if (e?.errorFields && e.errorFields.length > 0) {
        const errField = e.errorFields[0].name?.[0]
        const tabOf: Record<string, string> = {
          app_code: 'basic', name: 'basic', app_type: 'basic',
          deploy_path: 'basic', health_check_url: 'basic',
          git_url: 'build', git_cred_id: 'build', git_ref: 'build',
        }
        if (errField && tabOf[errField]) setActiveTab(tabOf[errField])
        return
      }
      message.error(formatError(e))
    }
  }

  return (
    <Modal
      title={isEdit ? `编辑应用 #${editing!.id}` : '新增应用（轻量建档）'}
      open={open}
      onCancel={onClose}
      onOk={submit}
      okText="保存"
      cancelText="取消"
      width={720}
      destroyOnClose
      maskClosable={false}
      keyboard={false}
    >
      <Form form={form} layout="vertical">
        <Tabs
          activeKey={activeTab}
          onChange={setActiveTab}
          items={[
            {
              key: 'basic',
              label: '基本信息',
              forceRender: true,
              children: (
                <>
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
                  <Form.Item
                    name="app_type"
                    label="项目类型"
                    rules={[{ required: true, message: '请选择项目类型' }]}
                    tooltip="Java 项目统一通过扫描 Maven pom.xml 发现 service；前端项目走源码 Docker build + 网关容器转发。"
                  >
                    <Select
                      disabled={isEdit}
                      options={[
                        { value: 'jar', label: 'Java Maven 项目（扫描 pom.xml）' },
                        { value: 'frontend', label: '前端 SPA 应用（React / Vue）' },
                      ]}
                    />
                  </Form.Item>
                  <Typography.Text type="secondary" style={{ fontSize: 12, display: 'block', marginBottom: 16 }}>
                    💡 应用按 Git 仓库轻量建档；Java 应用创建后去「服务」扫描 Maven 模块，前端应用也按「服务 / 运行 / 构建 / 部署」完成配置和发布。
                  </Typography.Text>

                  <Form.Item name="deploy_path" label="部署绝对路径" rules={[{ required: true }]}>
                    <Input placeholder="如：/opt/apps/user-service" />
                  </Form.Item>
                  <Form.Item
                    name="health_check_url"
                    label={isEdit ? '应用级默认健康检查路径' : '默认健康检查路径（可选）'}
                    tooltip="多 service 场景以 service 健康检查为准；这里作为默认兜底。"
                  >
                    <Input placeholder="/actuator/health" />
                  </Form.Item>
                  {!isEdit && (
                    <Typography.Text type="secondary" style={{ fontSize: 12, display: 'block' }}>
                    💡 创建后进入「应用详情 / 服务」扫描 Maven 模块并确认各 service 端口。
                    </Typography.Text>
                  )}
                </>
              ),
            },
            {
              key: 'build',
              label: '代码源',
              forceRender: true,
              children: (
                <>
                  <Form.Item
                    name="git_url"
                    label="Git 仓库地址"
                    tooltip="留空则不能从仓库构建，只能通过上传 jar / 注册本地路径"
                  >
                    <Input placeholder="https://github.com/org/repo.git" />
                  </Form.Item>
                  <Form.Item
                    name="git_cred_id"
                    label="Git 凭证"
                    tooltip="私有仓库选对应凭证；公网仓 / 走本机 SSH 默认 key 时选无凭证"
                  >
                    <Select
                      placeholder="选凭证"
                      allowClear
                      options={[
                        { value: '', label: '— 无凭证（公网 / 本机 SSH 默认）—' },
                        ...creds.map((c) => ({
                          value: String(c.id),
                          label: `${c.name} (${c.type === 'token' ? 'HTTPS Token' : 'SSH Key'})`,
                        })),
                      ]}
                    />
                  </Form.Item>
                  <Form.Item
                    name="git_refs"
                    label="构建 Ref 列表"
                    tooltip="用户自行维护可构建的分支 / Tag / Commit；后续构建从这里下拉选择"
                    rules={[
                      {
                        validator: (_, value) =>
                          normalizeRefs(value).length > 0
                            ? Promise.resolve()
                            : Promise.reject(new Error('请至少配置一个构建 Ref')),
                      },
                    ]}
                  >
                    <Select
                      mode="tags"
                      tokenSeparators={[',', ' ', '\n']}
                      placeholder="输入后回车，例如 main、dev、release/v1、v1.0.0"
                      onChange={(value) => {
                        const refs = normalizeRefs(value)
                        const current = form.getFieldValue('git_ref')
                        if (!refs.includes(current)) {
                          form.setFieldsValue({ git_ref: refs[0] || 'main' })
                        }
                      }}
                    />
                  </Form.Item>
                  <Form.Item shouldUpdate={(prev, cur) => prev.git_refs !== cur.git_refs} noStyle>
                    {() => {
                      const refs = normalizeRefs(form.getFieldValue('git_refs'), form.getFieldValue('git_ref') || 'main')
                      return (
                        <Form.Item
                          name="git_ref"
                          label="默认构建 Ref"
                          tooltip="打开构建弹窗时默认选中的 Ref"
                          rules={[{ required: true, message: '请选择默认构建 Ref' }]}
                        >
                          <Select
                            showSearch
                            options={refs.map(ref => ({ value: ref, label: ref }))}
                            placeholder="选择默认 Ref"
                          />
                        </Form.Item>
                      )
                    }}
                  </Form.Item>
                  <Typography.Text type="secondary" style={{ fontSize: 12, display: 'block', marginTop: -18, marginBottom: 18 }}>
                    💡 这里仅维护代码源和可构建 Ref。构建模块、Jar 匹配、Dockerfile 模板和镜像参数，请在「应用详情」配置。
                  </Typography.Text>
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                    💡 没有凭证？去左侧菜单「凭证」先创建一个，再回来选。<br />
                    💡 新建应用会先完成轻量建档，保存后自动进入详情页继续配置运行时与部署。
                  </Typography.Text>
                </>
              ),
            },
          ].filter((item) => item.key === 'basic' || item.key === 'build')}
        />
      </Form>
    </Modal>
  )
}
