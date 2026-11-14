import { describe, expect, it } from 'vitest'
import { darkOverrides, lightOverrides } from '@/theme'
import { contrastRatio } from '@/theme/contrast'

const WHITE = '#ffffff'
const DARK_PAGE = '#18181c'

describe('theme contrast (WCAG AA, 4.5:1)', () => {
  it('light: white text on the primary button colour is readable', () => {
    const primary = lightOverrides.common?.primaryColor ?? ''
    expect(contrastRatio(WHITE, primary)).toBeGreaterThanOrEqual(4.5)
  })

  it('light: primary-coloured text is readable on the white page', () => {
    const primary = lightOverrides.common?.primaryColor ?? ''
    expect(contrastRatio(primary, WHITE)).toBeGreaterThanOrEqual(4.5)
  })

  it('dark: dark text on the primary button colour is readable', () => {
    const primary = darkOverrides.common?.primaryColor ?? ''
    const text = darkOverrides.Button?.textColorPrimary ?? ''
    expect(contrastRatio(text, primary)).toBeGreaterThanOrEqual(4.5)
  })

  it('dark: primary-coloured text is readable on the dark page', () => {
    const primary = darkOverrides.common?.primaryColor ?? ''
    expect(contrastRatio(primary, DARK_PAGE)).toBeGreaterThanOrEqual(4.5)
  })
})
