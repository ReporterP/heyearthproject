export default function SiteFooter() {
  return (
    <footer className="border-t border-white/5 py-10 text-center text-xs text-text-secondary">
      <div className="max-w-6xl mx-auto px-6">
        <p>&copy; {new Date().getFullYear()} HeyEarth. Все права защищены.</p>
      </div>
    </footer>
  )
}
