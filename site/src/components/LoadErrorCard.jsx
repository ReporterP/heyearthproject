// A page that could not load its data says so (cause + what to do) instead of showing
// empty states that look like «у вас ничего нет».
export default function LoadErrorCard({ message, onRetry }) {
  return (
    <div className="max-w-xl mx-auto px-6 py-24">
      <div role="alert" className="card p-8 text-center">
        <p className="serif text-xl font-semibold mb-2">Не получилось загрузить</p>
        <p className="text-text-secondary text-sm mb-6">{message}</p>
        <button className="btn-primary" onClick={onRetry || (() => window.location.reload())}>Повторить</button>
      </div>
    </div>
  )
}
