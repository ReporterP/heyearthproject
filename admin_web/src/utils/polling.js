// startPolling runs ref.current() every intervalMs while visible() is true and returns a stop
// function (an effect cleanup). It reads ref.current on every tick, so a page that re-renders with
// new filters/page/sort is reloaded with them — a callback captured once at mount reloaded the
// list with the filters of the first render.
export function startPolling(ref, intervalMs, visible = () => true) {
  const id = setInterval(() => {
    if (visible()) ref.current()
  }, intervalMs)
  return () => clearInterval(id)
}

// requestSequencer: next() marks a new request and returns isCurrent() for it; an answer whose
// request is no longer the latest must not be applied (a slow poll with old filters finishing
// after a new filter's answer).
export function requestSequencer() {
  let seq = 0
  return function next() {
    const mine = ++seq
    return () => mine === seq
  }
}
