import { useState } from 'react'
import { useT } from '../i18n/index.jsx'

export default function SearchPaginationBar({ search, onSearch, page, totalPages, onPageChange, loading }) {
  const { t } = useT()
  const [input, setInput] = useState(search)

  function handleSubmit(e) {
    e.preventDefault()
    onSearch(input)
  }

  return (
    <div className="flex flex-wrap items-center gap-3 mb-4">
      <form onSubmit={handleSubmit} className="flex items-center gap-2 flex-1 min-w-[200px]">
        <input
          type="text"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder={t.search || 'Search\u2026'}
          className="flex-1 px-3 py-1.5 rounded-lg bg-canvas border border-default text-primary placeholder-text-secondary text-sm focus:border-accent focus:ring-1 focus:ring-accent outline-none"
        />
        <button
          type="submit"
          disabled={loading}
          className="px-3 py-1.5 rounded-lg bg-accent text-white text-sm hover:bg-accent-hover transition disabled:opacity-50"
        >
          {t.search || 'Search'}
        </button>
      </form>
      {totalPages > 1 && (
        <div className="flex items-center gap-1">
          <button
            disabled={page <= 1}
            onClick={() => onPageChange(page - 1)}
            className="px-2 py-1 rounded text-sm text-secondary hover:text-primary disabled:opacity-30 transition"
          >
            &laquo;
          </button>
          <span className="text-sm text-secondary px-2">
            {page} / {totalPages}
          </span>
          <button
            disabled={page >= totalPages}
            onClick={() => onPageChange(page + 1)}
            className="px-2 py-1 rounded text-sm text-secondary hover:text-primary disabled:opacity-30 transition"
          >
            &raquo;
          </button>
        </div>
      )}
    </div>
  )
}
