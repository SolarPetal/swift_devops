import { useEffect, useState } from 'react'
import { Modal, Form, Input, InputNumber, Radio, Select, Tabs, Typography, message, Button } from 'antd'

import type { App, AppInput, Host, GitCredential } from '../types'
import { createApp, updateApp, listBranches } from '../api/app'
import { listHosts } from '../api/host'
import { listGitCreds } from '../api/gitcred'
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
  const [creds, setCreds] = useState<GitCredential[]>([])
  const [branches, setBranches] = useState<string[]>([])
  const [loadingBranches, setLoadingBranches] = useState(false)
  const [activeTab, setActiveTab] = useState('basic')
  const isEdit = !!editing

  useEffect(() => {
    if (!open) return
    listHosts().then(setHosts).catch(() => {})
    listGitCreds().then(setCreds).catch(() => {})
    form.resetFields()
    setActiveTab('basic')
    if (editing) {
      form.setFieldsValue({
        app_code: editing.app_code,
        name: editing.name,
        app_type: editing.app_type || 'jar',
        build_mode: editing.build_mode || 'local-jar',
        git_url: editing.git_url,
        git_cred_id: editing.git_cred_id,
        build_module: editing.build_module,
        build_jar_pattern: editing.build_jar_pattern,
        deploy_path: editing.deploy_path,
        port: editing.port,
        health_check_url: editing.health_check_url,
        jvm_args: editing.jvm_args,
        env_vars: editing.env_vars,
        systemd_user: editing.systemd_user,
        java_path: editing.java_path,
        deploy_mode: editing.deploy_mode || 'systemd',
        nginx_host_id: editing.nginx_host_id || 0,
        nginx_upstream_name: editing.nginx_upstream_name,
        active_group: editing.active_group,
        docker_registry: editing.docker_registry || '',
        docker_image_name: editing.docker_image_name || '',
        docker_image_tag: editing.docker_image_tag || 'latest',
        dockerfile: editing.dockerfile || '',
        docker_build_args: editing.docker_build_args || '',
        docker_run_args: editing.docker_run_args || '',
      })
    } else {
      form.setFieldsValue({
        app_type: 'jar',
        build_mode: 'local-jar',
        port: 8080,
        health_check_url: '/actuator/health',
        jvm_args: '-Xms512m -Xmx512m',
        java_path: '',
        deploy_mode: 'systemd',
        git_url: '',
        git_cred_id: '',
        build_module: '',
        build_jar_pattern: '',
        nginx_host_id: 0,
        nginx_upstream_name: '',
        active_group: '',
        docker_registry: '',
        docker_image_name: '',
        docker_image_tag: 'latest',
        dockerfile: '',
        docker_build_args: '',
        docker_run_args: '',
      })
    }
  }, [open, editing, form])

  const fetchBranches = async () => {
    if (!editing?.id) {
      message.warning('请先保存应用后再获取分支')
      return
    }
    const gitUrl = form.getFieldValue('git_url')
    if (!gitUrl || gitUrl.trim() === '') {
      message.warning('请先填写 Git 仓库地址')
      return
    }

    setLoadingBranches(true)
    try {
      const credId = form.getFieldValue('git_cred_id')
      const credIdNum = credId ? parseInt(credId, 10) : undefined
      const branchList = await listBranches(editing.id, credIdNum)
      setBranches(branchList)
      if (branchList.length === 0) {
        message.info('未找到分支')
      } else {
        message.success(`获取到 ${branchList.length} 个分支`)
      }
    } catch (e: any) {
      message.error(formatError(e))
      setBranches([])
    } finally {
      setLoadingBranches(false)
    }
  }

  const submit = async () => {
    try {
      const v = await form.validateFields()
      // 前端再卡一道：nginx_host_id 与 nginx_upstream_name 同填同空
      const hasHost = !!v.nginx_host_id && v.nginx_host_id > 0
      const hasName = !!v.nginx_upstream_name && v.nginx_upstream_name.trim() !== ''
      if (hasHost !== hasName) {
        message.error('Nginx 主机和 upstream 名要么同时填，要么同时留空')
        setActiveTab('bluegreen')
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
    } catch (e: any) {
      // 校验失败时把焦点切到出错的 Tab
      if (e?.errorFields && e.errorFields.length > 0) {
        const errField = e.errorFields[0].name?.[0]
        const tabOf: Record<string, string> = {
          app_code: 'basic', name: 'basic', app_type: 'basic', build_mode: 'basic', deploy_mode: 'basic',
          deploy_path: 'basic', port: 'basic', health_check_url: 'basic',
          git_url: 'build', git_cred_id: 'build',
          build_module: 'build', build_jar_pattern: 'build',
          docker_registry: 'build', docker_image_name: 'build', docker_image_tag: 'build',
          dockerfile: 'build', docker_build_args: 'build',
          jvm_args: 'runtime', env_vars: 'runtime',
          systemd_user: 'runtime', java_path: 'runtime', docker_run_args: 'runtime',
          nginx_host_id: 'bluegreen', nginx_upstream_name: 'bluegreen', active_group: 'bluegreen',
        }
        if (errField && tabOf[errField]) setActiveTab(tabOf[errField])
        return
      }
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
                    label="应用类型"
                    rules={[{ required: true }]}
                    tooltip="单体应用：一个 Git 仓库编译出一个 jar；微服务应用：一个 Git 仓库编译出多个 jar（如 Spring Cloud 多模块项目）"
                  >
                    <Radio.Group>
                      <Radio.Button value="jar">单体应用（一个 jar）</Radio.Button>
                      <Radio.Button value="spring-cloud">微服务应用（多个 jar）</Radio.Button>
                    </Radio.Group>
                  </Form.Item>
                  {form.getFieldValue('app_type') === 'spring-cloud' && (
                    <Typography.Text type="warning" style={{ fontSize: 12, display: 'block', marginTop: -16, marginBottom: 16 }}>
                      💡 创建后请先在「微服务配置」Tab 添加各个微服务模块，再触发构建。
                    </Typography.Text>
                  )}

                  <Form.Item
                    name="build_mode"
                    label="构建方式"
                    rules={[{ required: true }]}
                    tooltip="本地 jar：Maven 打包 jar；本地 Docker：在 swift-devops 机器上 docker build 并推送到镜像仓库；远端 Docker：在远端构建机上 docker build 并推送到镜像仓库"
                  >
                    <Radio.Group>
                      <Radio.Button value="local-jar">本地 jar 构建</Radio.Button>
                      <Radio.Button value="local-docker">本地 Docker 镜像构建</Radio.Button>
                      <Radio.Button value="remote-docker">远端 Docker 镜像构建</Radio.Button>
                    </Radio.Group>
                  </Form.Item>

                  <Form.Item
                    name="deploy_mode"
                    label="部署方式"
                    rules={[{ required: true }]}
                    tooltip="systemd：在目标主机上用 systemd service 管理（需 root/sudo）；nohup：在目标主机上用 nohup java -jar 启动（免 root，crash 不自愈）；Docker：在目标主机上用 docker run 启动容器"
                  >
                    <Radio.Group>
                      <Radio.Button value="systemd">systemd（目标主机）</Radio.Button>
                      <Radio.Button value="nohup">nohup（目标主机）</Radio.Button>
                      <Radio.Button value="docker">Docker（目标主机）</Radio.Button>
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
                </>
              ),
            },
            {
              key: 'build',
              label: '构建',
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

                  {/* 分支选择器（Sprint X.11） */}
                  {isEdit && (
                    <Form.Item label="默认分支">
                      <Input.Group compact style={{ display: 'flex', gap: 8 }}>
                        <Select
                          placeholder="选择分支或手动输入"
                          showSearch
                          allowClear
                          style={{ flex: 1 }}
                          options={branches.map(b => ({ value: b, label: b }))}
                          notFoundContent={branches.length === 0 ? '点击右侧按钮获取分支' : '未找到'}
                          disabled={loadingBranches}
                        />
                        <Button
                          type="primary"
                          onClick={fetchBranches}
                          loading={loadingBranches}
                        >
                          获取分支
                        </Button>
                      </Input.Group>
                      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                        💡 获取远程仓库的分支列表，方便选择。触发构建时可以临时指定其他分支。
                      </Typography.Text>
                    </Form.Item>
                  )}

                  <Form.Item
                    name="build_module"
                    label="构建模块（multi-module 项目用，可选）"
                    tooltip="非空时给 mvn 加 -pl <module> -am，只编译该模块及其依赖，加速 + 避免多 jar 冲突。如 car-dealer-admin"
                  >
                    <Input placeholder="如：car-dealer-admin（留空构建整个项目）" />
                  </Form.Item>
                  <Form.Item
                    name="build_jar_pattern"
                    label="主 jar 路径模板（可选）"
                    tooltip="多模块项目命中多 jar 时显式指定。相对 git 仓库根目录的 glob，如 car-dealer-admin/target/*.jar"
                  >
                    <Input placeholder="如：car-dealer-admin/target/*.jar" />
                  </Form.Item>

                  {/* Docker 构建配置 */}
                  {(form.getFieldValue('build_mode') === 'local-docker' || form.getFieldValue('build_mode') === 'remote-docker') && (
                    <>
                      <Typography.Title level={5} style={{ marginTop: 24, marginBottom: 16 }}>Docker 镜像配置</Typography.Title>
                      <Form.Item
                        name="docker_registry"
                        label="镜像仓库地址"
                        tooltip="镜像推送的目标仓库，如 docker.io / harbor.example.com。构建完成后推送到此仓库，目标主机从此仓库拉取镜像。留空则镜像只在本地，无法推送到远端主机"
                      >
                        <Input placeholder="如：docker.io 或 harbor.example.com" />
                      </Form.Item>
                      <Form.Item
                        name="docker_image_name"
                        label="镜像名"
                        tooltip="镜像的名称，如 myapp/user-service。留空则自动生成为 <app_code>/<service_code>"
                      >
                        <Input placeholder="如：myapp/user-service（留空自动生成）" />
                      </Form.Item>
                      <Form.Item
                        name="docker_image_tag"
                        label="镜像标签"
                        tooltip="镜像的版本标签，如 latest / v1.0.0 / git-{sha}-{build_id}。支持变量替换"
                      >
                        <Input placeholder="latest" />
                      </Form.Item>
                      <Form.Item
                        name="dockerfile"
                        label="自定义 Dockerfile（可选）"
                        tooltip="留空则自动生成基于 eclipse-temurin:17-jre-alpine 的 Dockerfile。如需自定义（如多阶段构建、特殊依赖），可在此填写完整 Dockerfile 内容"
                      >
                        <Input.TextArea rows={6} placeholder="留空自动生成" />
                      </Form.Item>
                      <Form.Item
                        name="docker_build_args"
                        label="docker build 参数（可选）"
                        tooltip="传递给 docker build 的额外参数，如 --build-arg ENV=prod --no-cache"
                      >
                        <Input placeholder="如：--build-arg ENV=prod" />
                      </Form.Item>
                      <Typography.Text type="secondary" style={{ fontSize: 12, display: 'block', marginBottom: 16 }}>
                        💡 <strong>镜像流转路径</strong>：swift-devops 机器构建镜像 → 推送到镜像仓库 → 目标主机从仓库拉取 → 启动容器
                      </Typography.Text>
                    </>
                  )}

                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                    💡 没有凭证？去左侧菜单「🔑 Git 凭证」先创建一个，再回来选。<br />
                    💡 multi-module 项目命中多个 jar 时，填上「构建模块」或「主 jar 路径模板」其中一个即可。
                  </Typography.Text>
                </>
              ),
            },
            {
              key: 'runtime',
              label: '运行时',
              forceRender: true,
              children: (
                <>
                  <Form.Item
                    name="jvm_args"
                    label="JVM 启动参数"
                    hidden={form.getFieldValue('deploy_mode') === 'docker'}
                  >
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
                    hidden={form.getFieldValue('deploy_mode') === 'docker'}
                  >
                    <Input placeholder="留空 = 用 SSH 账号；如：deployer / java" />
                  </Form.Item>
                  <Form.Item
                    name="java_path"
                    label="Java 路径覆盖（可选）"
                    tooltip="留空 = 沿用主机的 java_path。仅当同台主机要跑不同 JDK 版本的应用时填写。两种格式都支持：JDK 目录或 java 可执行文件"
                    rules={[
                      {
                        pattern: /^(\/.*)?$/,
                        message: '必须以 / 开头（绝对路径），或留空走主机默认',
                      },
                    ]}
                    hidden={form.getFieldValue('deploy_mode') === 'docker'}
                  >
                    <Input placeholder="留空沿用主机配置；或填 /opt/java-17/bin/java" />
                  </Form.Item>

                  {/* Docker 部署配置 */}
                  {form.getFieldValue('deploy_mode') === 'docker' && (
                    <>
                      <Typography.Title level={5} style={{ marginTop: 24, marginBottom: 16 }}>Docker 部署配置</Typography.Title>
                      <Form.Item
                        name="docker_run_args"
                        label="docker run 参数"
                        tooltip="如 -p 8080:8080 -e ENV=prod --restart=unless-stopped，留空则自动生成"
                      >
                        <Input.TextArea rows={3} placeholder="留空自动生成，如：-d --restart=unless-stopped -p 8080:8080" />
                      </Form.Item>
                      <Typography.Text type="secondary" style={{ fontSize: 12, display: 'block', marginBottom: 16 }}>
                        💡 Docker 模式下，环境变量会自动转换为 -e 参数传给容器
                      </Typography.Text>
                    </>
                  )}

                  <Form.Item
                    name="env_vars"
                    label="环境变量 (JSON)"
                    tooltip='形如 {"SPRING_PROFILES_ACTIVE":"prod","LOG_LEVEL":"INFO"}'
                  >
                    <Input.TextArea rows={3} placeholder='{"SPRING_PROFILES_ACTIVE":"prod"}' />
                  </Form.Item>
                </>
              ),
            },
            {
              key: 'bluegreen',
              label: '蓝绿',
              forceRender: true,
              children: (
                <>
                  <Typography.Text type="secondary" style={{ display: 'block', marginBottom: 12, fontSize: 12 }}>
                    蓝绿部署（可选，留空则不启用）。两台主机分别打 blue / green 标签后才生效。
                  </Typography.Text>
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
                    tooltip="蓝绿部署成功后由系统更新；首次启用蓝绿前可手动声明现状（一般留空）"
                  >
                    <Radio.Group>
                      <Radio value="">未设置</Radio>
                      <Radio value="blue">Blue</Radio>
                      <Radio value="green">Green</Radio>
                    </Radio.Group>
                  </Form.Item>
                </>
              ),
            },
          ]}
        />
      </Form>
    </Modal>
  )
}
