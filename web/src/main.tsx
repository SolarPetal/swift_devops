import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { ConfigProvider, theme as antdTheme, App as AntdApp } from 'antd'
import zhCN from 'antd/locale/zh_CN'

import App from './App'
import './styles/app.css'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ConfigProvider
      locale={zhCN}
      theme={{
        algorithm: antdTheme.darkAlgorithm,
        token: {
          colorPrimary: '#2f7fc8',
          colorInfo: '#4b93c9',
          colorSuccess: '#40d692',
          colorWarning: '#fbbf24',
          colorError: '#ff6b6b',
          colorBgBase: '#07111f',
          colorBgContainer: '#0d1b2e',
          colorBgElevated: '#10233a',
          colorBorder: 'rgba(139, 180, 216, 0.18)',
          colorTextBase: '#edf7ff',
          colorTextSecondary: '#9fb3c8',
          borderRadius: 14,
          borderRadiusLG: 20,
          borderRadiusSM: 10,
          controlHeight: 38,
          controlHeightLG: 46,
          fontFamily:
            'HarmonyOS Sans SC, Microsoft YaHei UI, PingFang SC, Segoe UI, system-ui, -apple-system, sans-serif',
          wireframe: false,
        },
        components: {
          Layout: {
            bodyBg: '#07111f',
            headerBg: 'rgba(6, 17, 31, 0.72)',
            siderBg: '#07111f',
            triggerBg: '#06111f',
            triggerColor: '#aacbe5',
          },
          Menu: {
            darkItemBg: 'transparent',
            darkSubMenuItemBg: 'transparent',
            darkItemColor: '#9fb8cf',
            darkItemHoverBg: 'rgba(124, 190, 214, 0.07)',
            darkItemHoverColor: '#f3faff',
            darkItemSelectedBg: 'rgba(47, 127, 200, 0.28)',
            darkItemSelectedColor: '#ffffff',
            darkItemDisabledColor: 'rgba(157, 180, 199, 0.38)',
            itemBorderRadius: 15,
          },
          Card: {
            colorBgContainer: '#0d1b2e',
            borderRadiusLG: 22,
            headerBg: 'transparent',
          },
          Button: {
            borderRadius: 13,
            controlHeight: 38,
            controlHeightLG: 46,
            fontWeight: 700,
          },
          Input: {
            borderRadius: 13,
            controlHeight: 38,
            controlHeightLG: 46,
            activeShadow: '0 0 0 3px rgba(47, 127, 200, 0.12)',
          },
          Select: {
            borderRadius: 13,
            controlHeight: 38,
          },
          Table: {
            borderColor: 'rgba(139, 180, 216, 0.08)',
            headerBg: 'rgba(16, 35, 58, 0.92)',
            rowHoverBg: 'rgba(47, 127, 200, 0.07)',
          },
          Tag: {
            borderRadiusSM: 999,
          },
        },
      }}
    >
      <AntdApp>
        <BrowserRouter>
          <App />
        </BrowserRouter>
      </AntdApp>
    </ConfigProvider>
  </React.StrictMode>,
)
