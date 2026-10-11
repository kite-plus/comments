import { Languages, Monitor, Moon, Sun } from 'lucide-react'
import { useTheme, type Theme } from '@/context/theme-provider'
import { translate, useI18n, type Lang } from '@/i18n'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

/** Each language is listed by its own name, from its own catalog. */
export const LANGS: { value: Lang; label: string }[] = (['zh-CN', 'en'] as const).map((value) => ({
  value,
  label: translate(value, 'lang.name'),
}))

const THEMES: { value: Theme; icon: React.ElementType }[] = [
  { value: 'system', icon: Monitor },
  { value: 'light', icon: Sun },
  { value: 'dark', icon: Moon },
]

export function LanguageSwitch() {
  const { lang, setLang, t } = useI18n()
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant='ghost' size='icon' className='rounded-full' aria-label={t('lang.label')}>
          <Languages className='size-[1.15rem]' />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align='end'>
        <DropdownMenuLabel>{t('lang.label')}</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={lang} onValueChange={(value) => setLang(value as Lang)}>
          {LANGS.map((item) => (
            <DropdownMenuRadioItem key={item.value} value={item.value} lang={item.value}>
              {item.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export function ThemeSwitch() {
  const { theme, setTheme } = useTheme()
  const { t } = useI18n()
  const Icon = THEMES.find((item) => item.value === theme)?.icon ?? Monitor
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant='ghost'
          size='icon'
          className='rounded-full'
          aria-label={`${t('theme.label')}: ${t(`theme.${theme}`)}`}
        >
          <Icon className='size-[1.15rem]' />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align='end'>
        <DropdownMenuLabel>{t('theme.label')}</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={theme} onValueChange={(value) => setTheme(value as Theme)}>
          {THEMES.map(({ value, icon: ItemIcon }) => (
            <DropdownMenuRadioItem key={value} value={value}>
              <ItemIcon />
              {t(`theme.${value}`)}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
