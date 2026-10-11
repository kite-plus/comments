import { createContext, useCallback, useContext, useMemo, useState } from 'react'
import { setRequestLocale } from '@/api/client'
import type { Locale } from '@/api/types'
import { readPreference, writePreference } from '@/lib/storage'
import { en } from './en'
import { zhCN, type MessageKey } from './zh-CN'

export type { MessageKey }
export type Lang = Locale

const STORAGE_KEY = 'kc-lang'

type Vars = Record<string, string | number>

const catalogs: Record<Lang, Record<string, string>> = { 'zh-CN': zhCN, en }

export function detectLang(languages: readonly string[] = navigator.languages ?? []): Lang {
  const saved = readPreference(STORAGE_KEY)
  if (saved === 'zh-CN' || saved === 'en') return saved
  for (const tag of languages) {
    const lower = tag.toLowerCase()
    if (lower.startsWith('zh')) return 'zh-CN'
    if (lower.startsWith('en')) return 'en'
  }
  return 'zh-CN'
}

export function hasSavedLang() {
  const saved = readPreference(STORAGE_KEY)
  return saved === 'zh-CN' || saved === 'en'
}

/** Looks up a message; {name} placeholders take vars, and en picks key_one for count 1. */
export function translate(lang: Lang, key: MessageKey, vars?: Vars): string {
  const catalog = catalogs[lang]
  let message = catalog[key] ?? zhCN[key] ?? key
  if (lang === 'en' && vars && vars.count === 1 && catalog[`${key}_one`]) {
    message = catalog[`${key}_one`]
  }
  if (!vars) return message
  return message.replace(/\{(\w+)\}/g, (match, name: string) =>
    name in vars ? String(vars[name]) : match
  )
}

export type T = (key: MessageKey, vars?: Vars) => string

type I18nContextValue = {
  lang: Lang
  /** Remembers the choice in this browser. */
  setLang: (lang: Lang) => void
  /** Follows the account's language unless this browser already chose one. */
  adoptAccountLang: (lang: Lang) => void
  t: T
  formatDateTime: (iso: string) => string
  formatDate: (iso: string) => string
  formatRelative: (iso: string, now?: number) => string
  formatNumber: (n: number) => string
}

const I18nContext = createContext<I18nContextValue | null>(null)

function apply(lang: Lang) {
  document.documentElement.lang = lang
  setRequestLocale(lang)
}

const RELATIVE_UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 365 * 24 * 3600],
  ['month', 30 * 24 * 3600],
  ['week', 7 * 24 * 3600],
  ['day', 24 * 3600],
  ['hour', 3600],
  ['minute', 60],
]

export function I18nProvider({
  children,
  initialLang,
}: {
  children: React.ReactNode
  initialLang?: Lang
}) {
  const [lang, setLangState] = useState<Lang>(() => {
    const initial = initialLang ?? detectLang()
    apply(initial)
    return initial
  })

  const setLang = useCallback((next: Lang) => {
    writePreference(STORAGE_KEY, next)
    apply(next)
    setLangState(next)
  }, [])

  const adoptAccountLang = useCallback((next: Lang) => {
    if (hasSavedLang()) return
    apply(next)
    setLangState(next)
  }, [])

  const value = useMemo<I18nContextValue>(() => {
    const dateTime = new Intl.DateTimeFormat(lang, { dateStyle: 'medium', timeStyle: 'short' })
    const date = new Intl.DateTimeFormat(lang, { dateStyle: 'medium' })
    const relative = new Intl.RelativeTimeFormat(lang, { numeric: 'auto' })
    const number = new Intl.NumberFormat(lang)
    return {
      lang,
      setLang,
      adoptAccountLang,
      t: (key, vars) => translate(lang, key, vars),
      formatDateTime: (iso) => dateTime.format(new Date(iso)),
      formatDate: (iso) => date.format(new Date(iso)),
      formatRelative: (iso, now = Date.now()) => {
        const seconds = Math.round((new Date(iso).getTime() - now) / 1000)
        const abs = Math.abs(seconds)
        if (abs < 60) return translate(lang, 'time.justNow')
        for (const [unit, size] of RELATIVE_UNITS) {
          if (abs >= size) return relative.format(Math.trunc(seconds / size), unit)
        }
        return relative.format(Math.trunc(seconds / 60), 'minute')
      },
      formatNumber: (n) => number.format(n),
    }
  }, [lang, setLang, adoptAccountLang])

  return <I18nContext value={value}>{children}</I18nContext>
}

export function useI18n() {
  const context = useContext(I18nContext)
  if (!context) throw new Error('useI18n must be used within I18nProvider')
  return context
}

export function useT() {
  return useI18n().t
}
