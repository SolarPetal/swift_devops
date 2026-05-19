import { useState } from 'react'
import { Card, Form, Input, Button, message, Typography } from 'antd'
import { useNavigate } from 'react-router-dom'

import { login } from '../api/auth'
import { useAuth } from '../store/auth'
import { formatError } from '../api/client'

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
      nav('/hosts', { replace: true })
    } catch (e) {
      message.error(formatError(e))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div style={{ minHeight: '100vh', display: 'grid', placeItems: 'center', background: '#0f1115' }}>
      <Card style={{ width: 380 }} variant="borderless">
        <Typography.Title level={3} style={{ marginTop: 0, marginBottom: 4 }}>⚙️ Swift DevOps</Typography.Title>
        <Typography.Text type="secondary">Java 服务自动化运维平台</Typography.Text>
        <Form
          layout="vertical"
          onFinish={handle}
          initialValues={{ username: 'admin' }}
          style={{ marginTop: 24 }}
        >
          <Form.Item name="username" label="用户名" rules={[{ required: true }]}>
            <Input autoComplete="username" />
          </Form.Item>
          <Form.Item name="password" label="密码" rules={[{ required: true }]}>
            <Input.Password autoComplete="current-password" />
          </Form.Item>
          <Button type="primary" htmlType="submit" loading={loading} block>登录</Button>
        </Form>
      </Card>
    </div>
  )
}
