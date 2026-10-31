import { createLocale, enUS, type GlobalThemeOverrides } from 'naive-ui'

/** Brand colors shared by light and dark themes (Tailwind emerald). */
export const themeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: '#10b981',
    primaryColorHover: '#34d399',
    primaryColorPressed: '#059669',
    primaryColorSuppl: '#10b981',
    fontFamily: 'system-ui, sans-serif',
  },
}

/** Naive's English strings, minus its generic placeholders: our labels say what to enter. */
export const naiveLocale = createLocale(
  { Input: { placeholder: '' }, Select: { placeholder: '' } },
  enUS,
)
