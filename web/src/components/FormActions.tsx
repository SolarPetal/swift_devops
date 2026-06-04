import { Button, Space } from 'antd'
import type { ReactNode } from 'react'

type FormActionsProps = {
  primaryText?: string
  secondaryText?: string
  cancelText?: string
  onPrimary?: () => void
  onSecondary?: () => void
  onCancel?: () => void
  primaryLoading?: boolean
  secondaryLoading?: boolean
  primaryDisabled?: boolean
  secondaryDisabled?: boolean
  hint?: ReactNode
}

export function FormActions({
  primaryText = '保存',
  secondaryText,
  cancelText,
  onPrimary,
  onSecondary,
  onCancel,
  primaryLoading,
  secondaryLoading,
  primaryDisabled,
  secondaryDisabled,
  hint,
}: FormActionsProps) {
  return (
    <div className="form-actions">
      <Space wrap>
        {onPrimary && (
          <Button type="primary" onClick={onPrimary} loading={primaryLoading} disabled={primaryDisabled}>
            {primaryText}
          </Button>
        )}
        {secondaryText && onSecondary && (
          <Button onClick={onSecondary} loading={secondaryLoading} disabled={secondaryDisabled}>
            {secondaryText}
          </Button>
        )}
        {cancelText && onCancel && <Button onClick={onCancel}>{cancelText}</Button>}
      </Space>
      {hint && <div className="form-actions-hint">{hint}</div>}
    </div>
  )
}
