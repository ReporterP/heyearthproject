import { createContext, useContext, useState, useCallback } from 'react'
import en from './en.js'
import ru from './ru.js'

const langs = { en, ru }
const I18nContext = createContext()

export function I18nProvider({ children }) {
  const [locale, setLocaleState] = useState(() => localStorage.getItem('admin_locale') || 'ru')

  const setLocale = useCallback((l) => {
    setLocaleState(l)
    localStorage.setItem('admin_locale', l)
  }, [])

  const t = langs[locale] || langs.ru

  return (
    <I18nContext.Provider value={{ t, locale, setLocale }}>
      {children}
    </I18nContext.Provider>
  )
}

export function useT() {
  return useContext(I18nContext)
}
