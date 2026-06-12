import { useEffect, useRef } from 'react'

type LogTerminalProps = {
  text?: string
  loading?: boolean
  minHeight?: number
  className?: string
}

export function LogTerminal({ text, loading = false, minHeight, className }: LogTerminalProps) {
  const boxRef = useRef<HTMLPreElement>(null)
  // 实时日志增量 append 时自动跟随到底部；用户向上翻阅后停止跟随，再拖回底部恢复
  const stickToBottomRef = useRef(true)

  const handleScroll = () => {
    const el = boxRef.current
    if (!el) return
    stickToBottomRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 32
  }

  useEffect(() => {
    const el = boxRef.current
    if (el && stickToBottomRef.current) {
      el.scrollTop = el.scrollHeight
    }
  }, [text])

  return (
    <pre
      ref={boxRef}
      onScroll={handleScroll}
      className={`runtime-log-box ${loading ? 'runtime-log-box--loading' : ''} ${className || ''}`}
      style={minHeight ? { minHeight } : undefined}
    >
      {text || (loading ? '正在读取日志…' : '暂无日志')}
    </pre>
  )
}
