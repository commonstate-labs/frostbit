import { createI18n } from 'vue-i18n'
import en from './locales/en'
import zh from './locales/zh'

const messages = {
  en,
  zh
}

const savedLang = localStorage.getItem('dex_explorer_lang')
const navLang = navigator.language.toLowerCase()
const defaultLang = savedLang || (navLang.startsWith('zh') ? 'zh' : 'en') || 'en'

const i18n = createI18n({
  legacy: false, 
  locale: defaultLang,
  fallbackLocale: 'en',
  messages,
})

export default i18n
