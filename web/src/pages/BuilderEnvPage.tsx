import { useEffect, useState } from 'react'
import { Alert, Button, Card, Descriptions, Form, Input, Typography, message } from 'antd'

import type { BuilderEnv, BuilderEnvInput } from '../types'
import { detectBuilderEnv, getBuilderEnv, updateBuilderEnv } from '../api/builderEnv'
import { formatError } from '../api/client'
import { FormActions } from '../components/FormActions'
import { PageHeader, StatCard, StatGrid } from '../components/PageFrame'
import { StatusTag } from '../components/StatusTag'

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
        maven_local_repo: e.maven_local_repo,
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
    <section className="page-shell">
      <PageHeader
        eyebrow="Builder Environment"
        title="构建环境"
        description="这里配置 swift-devops 服务器本机的 Java / Maven / Git 路径，用于从远端 Git 拉代码并执行 Maven 构建。"
        meta={env && (env.valid
          ? <StatusTag tone="success">构建环境就绪</StatusTag>
          : <StatusTag tone="warning">构建环境未就绪</StatusTag>)}
      />

      <StatGrid>
        <StatCard
          label="Status"
          value={env?.valid ? 'Ready' : 'Pending'}
          description={env?.detected_at ? `最近检测：${new Date(env.detected_at).toLocaleString()}` : '尚未完成检测'}
          tone={env?.valid ? 'success' : 'warning'}
        />
        <StatCard label="Java" value={env?.java_home ? 'Set' : '-'} description={env?.java_home || '未配置 JAVA_HOME'} />
        <StatCard label="Maven" value={env?.maven_home ? 'Set' : '-'} description={env?.maven_home || '未配置 MAVEN_HOME'} />
        <StatCard label="Git" value={env?.git_path ? 'Custom' : 'Default'} description={env?.git_path || '留空兜底 /usr/bin/git'} tone="info" />
      </StatGrid>

      <Card
        className="surface-card"
        loading={loading}
        title="路径配置"
        extra={<Button onClick={refresh} loading={loading}>刷新当前配置</Button>}
      >
        <Alert
          type="info" showIcon style={{ marginBottom: 16 }}
          message="构建环境是 swift-devops 进程所在主机的本机配置"
          description={
            <>
              <div>这里给 mvn 子进程注入 JAVA_HOME / PATH。</div>
              <div>它和「主机管理」里每台部署主机的 java_path 是<strong>两回事</strong>，那个是部署目标机器上 jar 跑用的。</div>
            </>
          }
        />

        <Form form={form} layout="vertical" style={{ maxWidth: 760 }}>
          <Form.Item
            name="java_home"
            label="JAVA_HOME"
            rules={[
              { required: true, message: '必填' },
              { pattern: /^\/[^\\]*$/, message: '必须是绝对路径（以 / 开头），且不能含反斜杠 \\' },
            ]}
            tooltip="JDK 安装目录的绝对路径。检测时会跑 <java_home>/bin/java -version"
          >
            <Input placeholder="如：/opt/jdk-17 或 /mnt/d/develop/jdk-17（注意全用 / 不用 \\）" />
          </Form.Item>
          <Form.Item
            name="maven_home"
            label="MAVEN_HOME"
            rules={[
              { required: true, message: '必填' },
              { pattern: /^\/[^\\]*$/, message: '必须是绝对路径（以 / 开头），且不能含反斜杠 \\' },
            ]}
            tooltip="Maven 安装目录的绝对路径。检测时会跑 <maven_home>/bin/mvn -v"
          >
            <Input placeholder="如：/opt/apache-maven-3.9.9 或 /mnt/d/develop/apache-maven-3.9.9" />
          </Form.Item>
          <Form.Item
            name="git_path"
            label="Git 可执行路径（可选）"
            tooltip="留空兜底 /usr/bin/git；填写后用绝对路径，如 /usr/bin/git"
            rules={[
              { pattern: /^(\/[^\\]*)?$/, message: '必须是绝对路径或留空，且不能含反斜杠' },
            ]}
          >
            <Input placeholder="留空兜底 /usr/bin/git；或填 /usr/bin/git" />
          </Form.Item>
          <Form.Item
            name="maven_local_repo"
            label="Maven 本地仓库（可选）"
            tooltip="留空 = 用 mvn settings.xml 里的 <localRepository>（推荐，能复用本机 maven 缓存）；非空必须绝对路径，触发构建时作为 -Dmaven.repo.local=<dir> 传给 mvn 覆盖 settings.xml"
            rules={[
              { pattern: /^(\/[^\\]*)?$/, message: '必须是绝对路径或留空，且不能含反斜杠' },
            ]}
          >
            <Input placeholder="留空 = 用 settings.xml 默认；或填 /home/wwkj/maven/maven_rep 之类的绝对路径" />
          </Form.Item>
          <FormActions
            primaryText="保存配置"
            secondaryText="检测环境"
            onPrimary={save}
            onSecondary={detect}
            primaryLoading={saving}
            secondaryLoading={detecting}
            hint="保存只更新路径配置；检测会真实调用 Java / Maven / Git，检测通过后才算构建环境就绪。"
          />
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
              <Descriptions.Item label="Maven 本地仓库">
                {env.maven_local_repo
                  ? <code>{env.maven_local_repo}</code>
                  : <Typography.Text type="secondary">（空 → 用 mvn settings.xml 里的 &lt;localRepository&gt;）</Typography.Text>}
              </Descriptions.Item>
              <Descriptions.Item label="状态">
                {env.valid ? <StatusTag tone="success">全部通过</StatusTag> : <StatusTag tone="danger">检测失败</StatusTag>}
              </Descriptions.Item>
            </Descriptions>
            {env.detect_message && (
              <pre className="preflight-box builder-detect-log">{env.detect_message}</pre>
            )}
          </Card>
        )}

        {env && !env.valid && (
          <Alert
            type="warning" showIcon style={{ marginTop: 16 }}
            message="构建环境未就绪"
            description="未配置或检测失败时，任何应用的「从仓库构建」按钮都会 disabled。请先把上面三项配好并点「检测」。"
          />
        )}
      </Card>
    </section>
  )
}
