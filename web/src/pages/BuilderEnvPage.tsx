import { useEffect, useState } from 'react'
import { Alert, Button, Card, Descriptions, Form, Input, Space, Tag, Typography, message } from 'antd'

import type { BuilderEnv, BuilderEnvInput } from '../types'
import { detectBuilderEnv, getBuilderEnv, updateBuilderEnv } from '../api/builderEnv'
import { formatError } from '../api/client'

export default function BuilderEnvPage() {
  const [env, setEnv] = useState<BuilderEnv | null>(null)
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [detecting, setDetecting] = useState(false)
  const [form] = Form.useForm<BuilderEnvInput>()

  const refresh = async () => {
    setLoading(true)
    try {
      const e = await getBuilderEnv()
      setEnv(e)
      form.setFieldsValue({
        java_home: e.java_home,
        maven_home: e.maven_home,
        git_path: e.git_path,
      })
    } catch (e) { message.error(formatError(e)) }
    finally { setLoading(false) }
  }
  useEffect(() => { refresh() }, [])

  const save = async () => {
    try {
      const v = await form.validateFields()
      setSaving(true)
      const e = await updateBuilderEnv(v)
      setEnv(e)
      message.success('已保存。请点「检测」验证配置')
    } catch (e) {
      if ((e as any)?.errorFields) return
      message.error(formatError(e))
    } finally { setSaving(false) }
  }

  const detect = async () => {
    setDetecting(true)
    try {
      const e = await detectBuilderEnv()
      setEnv(e)
      if (e.valid) message.success('检测通过，构建环境就绪 ✓')
      else message.warning('检测未通过，请看下方诊断信息')
    } catch (e) { message.error(formatError(e)) }
    finally { setDetecting(false) }
  }

  return (
    <Card title={
      <Space>
        构建环境
        {env && (env.valid
          ? <Tag color="green">● 就绪</Tag>
          : <Tag color="red">○ 未就绪</Tag>)}
      </Space>
    } loading={loading} extra={<Button onClick={refresh}>刷新</Button>}>
      <Alert
        type="info" showIcon style={{ marginBottom: 16 }}
        message="swift-devops 服务器构建用的 Java / Maven / Git 路径"
        description={
          <>
            <div>这里配的是 <strong>swift-devops 进程所在主机</strong>的本机路径，给 mvn 子进程注入 JAVA_HOME / PATH。</div>
            <div>跟「主机管理」里每台部署主机的 java_path 是<strong>两回事</strong>（那个是部署目标机器上 jar 跑用的）。</div>
          </>
        }
      />

      <Form form={form} layout="vertical" style={{ maxWidth: 720 }}>
        <Form.Item
          name="java_home"
          label="JAVA_HOME"
          rules={[
            { required: true, message: '必填' },
            { pattern: /^\/.*$/, message: '必须是绝对路径（以 / 开头）' },
          ]}
          tooltip="JDK 安装目录的绝对路径。检测时会跑 <java_home>/bin/java -version"
        >
          <Input placeholder="如：/opt/jdk-17 或 /mnt/d/develop/jdk-17" />
        </Form.Item>
        <Form.Item
          name="maven_home"
          label="MAVEN_HOME"
          rules={[
            { required: true, message: '必填' },
            { pattern: /^\/.*$/, message: '必须是绝对路径（以 / 开头）' },
          ]}
          tooltip="Maven 安装目录的绝对路径。检测时会跑 <maven_home>/bin/mvn -v"
        >
          <Input placeholder="如：/opt/apache-maven-3.9.9 或 /mnt/d/develop/apache-maven-3.9.9" />
        </Form.Item>
        <Form.Item
          name="git_path"
          label="Git 可执行路径（可选）"
          tooltip="留空走 PATH 找 git；填写后用绝对路径，如 /usr/bin/git"
          rules={[
            { pattern: /^(\/.*)?$/, message: '必须是绝对路径或留空' },
          ]}
        >
          <Input placeholder="留空走 PATH；或填 /usr/bin/git" />
        </Form.Item>
        <Form.Item>
          <Space>
            <Button type="primary" onClick={save} loading={saving}>保存</Button>
            <Button onClick={detect} loading={detecting}>检测</Button>
          </Space>
        </Form.Item>
      </Form>

      {env && (env.detected_at || env.detect_message) && (
        <Card size="small" type="inner" title="上次检测结果" style={{ marginTop: 16 }}>
          <Descriptions size="small" column={1}>
            {env.detected_at && (
              <Descriptions.Item label="时间">
                {new Date(env.detected_at).toLocaleString()}
              </Descriptions.Item>
            )}
            {env.java_version && <Descriptions.Item label="Java"><code>{env.java_version}</code></Descriptions.Item>}
            {env.maven_version && <Descriptions.Item label="Maven"><code>{env.maven_version}</code></Descriptions.Item>}
            {env.git_version && <Descriptions.Item label="Git"><code>{env.git_version}</code></Descriptions.Item>}
            <Descriptions.Item label="状态">
              {env.valid ? <Tag color="green">✓ 全部通过</Tag> : <Tag color="red">✗ 检测失败</Tag>}
            </Descriptions.Item>
          </Descriptions>
          {env.detect_message && (
            <pre style={{
              background: '#fafafa', border: '1px solid #f0f0f0', padding: 10, marginTop: 12,
              borderRadius: 4, fontSize: 12, fontFamily: 'Consolas, Monaco, monospace',
              whiteSpace: 'pre-wrap',
            }}>{env.detect_message}</pre>
          )}
        </Card>
      )}

      {env && !env.valid && (
        <Alert
          type="warning" showIcon style={{ marginTop: 16 }}
          message="构建环境未就绪"
          description="未配置或检测失败时，任何应用的「⚙ 从仓库构建」按钮都会 disabled。请先把上面三项配好并点「检测」。"
        />
      )}
    </Card>
  )
}
