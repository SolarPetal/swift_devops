type LogTerminalProps = {
  text?: string
  loading?: boolean
  minHeight?: number
}

export function LogTerminal({ text, loading = false, minHeight }: LogTerminalProps) {
  return (
    <pre className={`runtime-log-box ${loading ? 'runtime-log-box--loading' : ''}`} style={minHeight ? { minHeight } : undefined}>
      {text || (loading ? '正在读取日志…' : '暂无日志')}
    </pre>
  )
}
