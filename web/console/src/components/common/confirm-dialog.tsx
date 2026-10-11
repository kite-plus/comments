import { useId, useState } from 'react'
import { useT } from '@/i18n'
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Spinner } from './index'

type ConfirmDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: React.ReactNode
  description: React.ReactNode
  confirmText?: React.ReactNode
  destructive?: boolean
  isLoading?: boolean
  onConfirm: () => void
  /** The reader must type this word before the button enables. */
  confirmWord?: string
  children?: React.ReactNode
}

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmText,
  destructive,
  isLoading,
  onConfirm,
  confirmWord,
  children,
}: ConfirmDialogProps) {
  const t = useT()
  const inputId = useId()
  const [typed, setTyped] = useState('')
  const blocked = !!confirmWord && typed.trim() !== confirmWord

  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (!next) setTyped('')
        onOpenChange(next)
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader className='text-start'>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription asChild>
            <div className='space-y-2'>{description}</div>
          </AlertDialogDescription>
        </AlertDialogHeader>
        {children}
        {confirmWord && (
          <div className='space-y-2'>
            <Label htmlFor={inputId}>{t('common.typeToConfirm', { word: confirmWord })}</Label>
            <Input
              id={inputId}
              value={typed}
              autoComplete='off'
              onChange={(event) => setTyped(event.target.value)}
            />
          </div>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={isLoading}>{t('common.cancel')}</AlertDialogCancel>
          <Button
            type='button'
            variant={destructive ? 'destructive' : 'default'}
            disabled={isLoading || blocked}
            onClick={onConfirm}
          >
            {isLoading && <Spinner />}
            {confirmText ?? t('common.confirm')}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
