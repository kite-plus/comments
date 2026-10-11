import { CircleAlert, CircleCheck, Info, TriangleAlert } from 'lucide-react'
import { Toaster as Sonner, type ToasterProps } from 'sonner'
import { useTheme } from '@/context/theme-provider'
import { useT } from '@/i18n'

/**
 * shadcn-admin's Toaster as Explore dresses it: each state has an icon, which
 * index.css tints under .app-toaster, and the close button sits on the
 * top-right corner.
 */
export function Toaster({ ...props }: ToasterProps) {
  const { theme = 'system' } = useTheme()
  const t = useT()

  return (
    <Sonner
      theme={theme as ToasterProps['theme']}
      className='app-toaster toaster group [&_div[data-content]]:w-full'
      position='top-center'
      offset={16}
      gap={8}
      visibleToasts={3}
      closeButton
      toastOptions={{ closeButtonAriaLabel: t('common.close') }}
      icons={{
        success: <CircleCheck size={16} />,
        info: <Info size={16} />,
        warning: <TriangleAlert size={16} />,
        error: <CircleAlert size={16} />,
      }}
      style={
        {
          '--normal-bg': 'var(--popover)',
          '--normal-text': 'var(--popover-foreground)',
          '--normal-border': 'var(--border)',
          '--border-radius': '12px',
          '--width': '340px',
          '--toast-close-button-start': 'unset',
          '--toast-close-button-end': '0',
          '--toast-close-button-transform': 'translate(35%, -35%)',
        } as React.CSSProperties
      }
      {...props}
    />
  )
}
