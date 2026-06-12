import { useState } from 'react'
import { Card, Form, Input, Button, message, Typography } from 'antd'
import { useNavigate } from 'react-router-dom'

import { login } from '../api/auth'
import { useAuth } from '../store/auth'
import { formatError } from '../api/client'

const logoSrc = '/swift_devops_logo.png'

export default function Login() {
  const [loading, setLoading] = useState(false)
  const nav = useNavigate()
  const setUser = useAuth((s) => s.setUser)

  const handle = async (values: { username: string; password: string }) => {
    setLoading(true)
    try {
      const { token, username } = await login(values.username, values.password)
      localStorage.setItem('token', token)
      setUser(username)
      message.success('登录成功')
      nav('/overview', { replace: true })
    } catch (e) {
      message.error(formatError(e))
    } finally {
      setLoading(false)
    }
  }

  return (
    <main className="login-page">
      <div className="login-shell">
        <section className="login-hero" aria-label="Swift DevOps 平台介绍">
          <div className="login-hero-brand">
            <span className="brand-mark">
              <img src={logoSrc} alt="Swift DevOps 标识" />
            </span>
            <span>Swift DevOps</span>
          </div>
          <Typography.Title className="login-title">
            用更清晰的方式<br />管理 <span className="accent">Java 交付</span>
          </Typography.Title>
          <p className="login-subtitle">
            统一接入远端 Git、Maven 模块扫描、Docker 构建与主机部署，把构建、发布、回滚和运行态观测收在一个控制台里。
          </p>
          <div className="login-metrics">
            <div className="login-metric">
              <strong>Git → Build</strong>
              <span>代码拉取、模块识别、构建命令统一编排。</span>
            </div>
            <div className="login-metric">
              <strong>Deploy</strong>
              <span>版本、部署历史、回滚目标关联清楚。</span>
            </div>
            <div className="login-metric">
              <strong>Runtime</strong>
              <span>部署成功和真实运行状态分层呈现。</span>
            </div>
          </div>
        </section>

        <Card className="login-card" variant="borderless">
          <div className="login-card-header">
            <div className="login-card-brand">
              <span className="brand-mark">
                <img src={logoSrc} alt="Swift DevOps 标识" />
              </span>
              <div>
                <Typography.Text className="login-kicker">Control Center</Typography.Text>
                <Typography.Title level={3} className="login-card-title">欢迎回来</Typography.Title>
              </div>
            </div>
            <Typography.Text className="login-card-desc">
              登录后从总览进入应用、主机、凭证与系统设置。
            </Typography.Text>
          </div>

          <Form
            className="login-form"
            layout="vertical"
            onFinish={handle}
            requiredMark={false}
          >
            <Form.Item name="username" label="用户名" rules={[{ required: true, message: '请输入用户名' }]}>
              <Input autoComplete="username" placeholder="请输入用户名" />
            </Form.Item>
            <Form.Item name="password" label="密码" rules={[{ required: true, message: '请输入密码' }]}>
              <Input.Password autoComplete="current-password" placeholder="请输入密码" />
            </Form.Item>
            <Button className="login-submit" type="primary" htmlType="submit" loading={loading} block>
              登录控制台
            </Button>
          </Form>
          <div className="login-footnote">建议只在可信网络环境下访问运维控制台，退出时及时清理登录态。</div>
        </Card>
      </div>
    </main>
  )
}
