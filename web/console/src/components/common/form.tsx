import { cn } from '@/lib/utils'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Label } from '@/components/ui/label'

/** A label, the control, then a hint or an error tied to it by id. */
export function Field({
  id,
  label,
  hint,
  error,
  children,
  className,
}: {
  id: string
  label: React.ReactNode
  hint?: React.ReactNode
  error?: string | null
  children: React.ReactNode
  className?: string
}) {
  return (
    <div className={cn('space-y-2', className)}>
      <Label htmlFor={id}>{label}</Label>
      {children}
      {error ? (
        <p id={`${id}-error`} className='text-sm text-destructive' role='alert'>
          {error}
        </p>
      ) : hint ? (
        <p id={`${id}-hint`} className='text-sm text-muted-foreground'>
          {hint}
        </p>
      ) : null}
    </div>
  )
}

/** aria props for the control inside a Field. */
export function describedBy(id: string, error?: string | null, hint?: React.ReactNode) {
  return {
    id,
    'aria-invalid': error ? true : undefined,
    'aria-describedby': error ? `${id}-error` : hint ? `${id}-hint` : undefined,
  }
}

export function Section({
  title,
  description,
  children,
  footer,
  className,
  tone,
}: {
  title: React.ReactNode
  description?: React.ReactNode
  children?: React.ReactNode
  footer?: React.ReactNode
  className?: string
  tone?: 'danger'
}) {
  return (
    <Card className={cn('gap-4', tone === 'danger' && 'border-destructive/40', className)}>
      <CardHeader>
        <CardTitle className={cn('text-base', tone === 'danger' && 'text-destructive')}>
          <h2>{title}</h2>
        </CardTitle>
        {description && <CardDescription>{description}</CardDescription>}
      </CardHeader>
      {children && <CardContent className='space-y-5'>{children}</CardContent>}
      {footer && <CardFooter className='flex flex-wrap justify-end gap-2'>{footer}</CardFooter>}
    </Card>
  )
}
