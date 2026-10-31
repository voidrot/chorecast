import { createI18n } from 'vue-i18n'
import en from './locales/en.json'

export type MessageSchema = typeof en

/** UI text lives in src/locales; English only for now, other locales can be added as files. */
export const i18n = createI18n<[MessageSchema], 'en'>({
  legacy: false,
  locale: 'en',
  fallbackLocale: 'en',
  messages: { en },
})
