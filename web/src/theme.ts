import { createLocale, enUS, type GlobalThemeOverrides } from 'naive-ui'

const fontFamily = 'system-ui, sans-serif'

/**
 * Light theme: the brand is darkened so white text on filled buttons passes WCAG AA (4.5:1).
 * Matches the Android light palette.
 */
export const lightOverrides: GlobalThemeOverrides = {
  common: {
    fontFamily,
    primaryColor: '#047857',
    primaryColorHover: '#065f46',
    primaryColorPressed: '#064e3b',
    primaryColorSuppl: '#047857',
    successColor: '#047857',
    successColorHover: '#047857',
    successColorPressed: '#047857',
    successColorSuppl: '#047857',
    warningColor: '#b45309',
    warningColorHover: '#b45309',
    warningColorPressed: '#b45309',
    warningColorSuppl: '#b45309',
  },
  Button: {
    textColorPrimary: '#ffffff',
    textColorHoverPrimary: '#ffffff',
    textColorPressedPrimary: '#ffffff',
    textColorFocusPrimary: '#ffffff',
  },
}

/**
 * Dark theme: the brand is lightened and filled buttons use dark text.
 * Matches the Android dark palette.
 */
export const darkOverrides: GlobalThemeOverrides = {
  common: {
    fontFamily,
    primaryColor: '#6ee7b7',
    primaryColorHover: '#a7f3d0',
    primaryColorPressed: '#34d399',
    primaryColorSuppl: '#6ee7b7',
  },
  Button: {
    textColorPrimary: '#022c22',
    textColorHoverPrimary: '#022c22',
    textColorPressedPrimary: '#022c22',
    textColorFocusPrimary: '#022c22',
  },
}

/** Naive's English strings, minus its generic placeholders: our labels say what to enter. */
export const naiveLocale = createLocale(
  { Input: { placeholder: '' }, InputNumber: { placeholder: '' }, Select: { placeholder: '' } },
  enUS,
)
