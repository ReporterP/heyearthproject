import { Menu, Search } from 'lucide-react'

export default function MobileTopbar({ onOpenMenu, onOpenCmdK }) {
  return (
    <div className="md:hidden sticky top-0 z-30 h-12 px-3 flex items-center gap-2 bg-elevated border-b border-default">
      <button
        type="button"
        onClick={onOpenMenu}
        aria-label="Меню"
        className="p-2 -ml-1 rounded text-tertiary hover:text-primary hover:bg-hover"
      >
        <Menu className="w-5 h-5" />
      </button>
      <div className="font-semibold text-primary tracking-tight truncate flex-1">
        HiEarth Admin
      </div>
      <button
        type="button"
        onClick={onOpenCmdK}
        aria-label="Поиск"
        className="p-2 -mr-1 rounded text-tertiary hover:text-primary hover:bg-hover"
      >
        <Search className="w-5 h-5" />
      </button>
    </div>
  )
}
