import { Layout, Menu, Button, Typography } from 'antd'
import { Outlet, useNavigate, useLocation } from 'react-router-dom'

import { useAuth } from '../store/auth'

const { Sider, Header, Content } = Layout

// 用 emoji 当 icon，省一个 @ant-design/icons 依赖
const items = [
  { key: '/hosts', label: '🖥  主机' },
  { key: '/apps', label: '📦  应用' },
  { key: '/git-creds', label: '🔑  Git 凭证' },
  { key: '/builder-env', label: '⚙  构建环境' },
  { key: '/artifacts', label: '🧱  制品', disabled: true },
  { key: '/pipelines', label: '🚀  流水线', disabled: true },
  { key: '/monitor', label: '📈  监控', disabled: true },
]

export default function MainLayout() {
  const nav = useNavigate()
  const loc = useLocation()
  const { user, logout } = useAuth()

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Sider collapsible breakpoint="lg">
        <div style={{ height: 56, color: '#fff', display: 'grid', placeItems: 'center', fontWeight: 600 }}>
          ⚙️ Swift DevOps
        </div>
        <Menu
          theme="dark"
          mode="inline"
          selectedKeys={[loc.pathname]}
          items={items}
          onClick={(e) => nav(e.key)}
        />
      </Sider>
      <Layout>
        <Header style={{ display: 'flex', justifyContent: 'flex-end', alignItems: 'center', padding: '0 24px' }}>
          <Typography.Text style={{ marginRight: 16 }}>{user}</Typography.Text>
          <Button onClick={() => { logout(); nav('/login', { replace: true }) }}>退出</Button>
        </Header>
        <Content style={{ padding: 24 }}>
          <Outlet />
        </Content>
      </Layout>
    </Layout>
  )
}
