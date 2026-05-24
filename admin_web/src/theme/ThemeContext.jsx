import { createContext, useContext, useEffect, useState } from 'react'

const THEME_KEY  = 'hiearth-theme'
const ACCENT_KEY = 'hiearth-accent'

const DEFAULT_THEME  = 'linear'
const DEFAULT_ACCENT = 'blue'

const ThemeContext = createContext({
  theme: DEFAULT_THEME,
  accent: DEFAULT_ACCENT,
  setTheme: () => {},
  setAccent: () => {},
})

export function ThemeProvider({ children }) {
  const [theme, setThemeState]   = useState(() => localStorage.getItem(THEME_KEY)  || DEFAULT_THEME)
  const [accent, setAccentState] = useState(() => localStorage.getItem(ACCENT_KEY) || DEFAULT_ACCENT)

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme)
    localStorage.setItem(THEME_KEY, theme)
  }, [theme])

  useEffect(() => {
    document.documentElement.setAttribute('data-accent', accent)
    localStorage.setItem(ACCENT_KEY, accent)
  }, [accent])

  return (
    <ThemeContext.Provider value={{ theme, accent, setTheme: setThemeState, setAccent: setAccentState }}>
      {children}
    </ThemeContext.Provider>
  )
}

export const useTheme = () => useContext(ThemeContext)
