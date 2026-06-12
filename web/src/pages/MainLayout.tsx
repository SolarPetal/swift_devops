import { Layout, Menu, Button } from 'antd'
import {
  AppstoreOutlined,
  CloudServerOutlined,
  DashboardOutlined,
  KeyOutlined,
  LogoutOutlined,
  SettingOutlined,
} from '@ant-design/icons'
import { Outlet, useNavigate, useLocation } from 'react-router-dom'

import { useAuth } from '../store/auth'

const { Sider, Header, Content } = Layout
const logoSrc = '/swift_devops_logo.png'

const menuItems = [
  { key: '/overview', icon: <span className="nav-glyph"><DashboardOutlined /></span>, label: '总览' },
  { key: '/apps', icon: <span className="nav-glyph"><AppstoreOutlined /></span>, label: '应用' },
  { key: '/hosts', icon: <span className="nav-glyph"><CloudServerOutlined /></span>, label: '主机' },
  { key: '/credentials', icon: <span className="nav-glyph"><KeyOutlined /></span>, label: '凭证' },
  { key: '/settings/build-env', icon: <span className="nav-glyph"><SettingOutlined /></span>, label: '系统设置' },
]

const pageTitles: Record<string, string> = {
  '/overview': '交付运行总览',
  '/apps': '应用服务',
  '/hosts': '主机资源',
  '/credentials': '凭证治理',
  '/settings/build-env': '系统设置',
}

function resolveSelectedKey(pathname: string) {
  const exact = menuItems.find((item) => item.key === pathname)
  if (exact) return exact.key
  const prefix = menuItems
    .filter((item) => pathname.startsWith(`${item.key}/`))
    .sort((a, b) => b.key.length - a.key.length)[0]
  return prefix?.key ?? '/overview'
}

export default function MainLayout() {
  const nav = useNavigate()
  const loc = useLocation()
  const { user, logout } = useAuth()
  const selectedKey = resolveSelectedKey(loc.pathname)
  const pageTitle = pageTitles[selectedKey] ?? '控制台'

  return (
    <Layout className="app-shell">
      <Sider className="app-sider" width={248} collapsedWidth={76} collapsible breakpoint="lg">
        <div className="sider-brand">
          <span className="brand-mark">
            <img src={logoSrc} alt="Swift DevOps 标识" />
          </span>
          <div className="brand-copy">
            <div className="brand-title">Swift DevOps</div>
            <div className="brand-subtitle">Release Console</div>
          </div>
        </div>
        <Menu
          className="nav-menu"
          theme="dark"
          mode="inline"
          selectedKeys={[selectedKey]}
          items={menuItems}
          onClick={(e) => nav(e.key)}
        />
      </Sider>
      <Layout className="app-main">
        <Header className="app-header">
          <div className="header-title">
            <span className="header-eyebrow">Console</span>
            <span className="header-heading">{pageTitle}</span>
          </div>
          <div className="header-actions">
            <span className="user-chip" title={user || '未登录'}>
              <span className="user-avatar">{(user || 'U').slice(0, 1).toUpperCase()}</span>
              <span>{user}</span>
            </span>
            <Button icon={<LogoutOutlined />} onClick={() => { logout(); nav('/login', { replace: true }) }}>退出</Button>
          </div>
        </Header>
        <Content className="app-content">
          <Outlet />
        </Content>
      </Layout>
    </Layout>
  )
}
