import { createI18n } from 'vue-i18n'
import zhCN from './locales/zh-CN.yaml'
import en from './locales/en.yaml'

function getDefaultLocale(): string {
  // Try to read saved preference
  const saved = localStorage.getItem('fileengine-locale')
  if (saved && ['zh-CN', 'en'].includes(saved)) return saved

  // Try browser language
  try {
    const lang = navigator.language || (navigator as any).userLanguage || ''
    if (lang.startsWith('en')) return 'en'
  } catch {
    // ignore
  }

  // Default to Chinese
  return 'zh-CN'
}

const i18n = createI18n({
  legacy: false,
  locale: getDefaultLocale(),
  fallbackLocale: 'zh-CN',
  messages: {
    'zh-CN': zhCN,
    en,
  },
})

export function setLocale(locale: string) {
  ;(i18n.global.locale as any).value = locale
  localStorage.setItem('fileengine-locale', locale)
  document.documentElement.setAttribute('lang', locale === 'zh-CN' ? 'zh' : 'en')
}

export function getCurrentLocale(): string {
  return (i18n.global.locale as any).value
}

export default i18n
