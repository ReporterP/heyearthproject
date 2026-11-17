import { PageHeader } from '../components/ui/index.js'
import WebProxyCard from '../components/telegram/WebProxyCard.jsx'
import ClassicMTProtoCard from '../components/telegram/ClassicMTProtoCard.jsx'

/**
 * MTProtoPage — «Telegram» (route /mtproto kept for bookmarks): the WEB proxy on the Hub first (the primary
 * path), the classic MTProto on the exits under it (the fallback for iOS and old Telegram versions).
 * Each card loads, saves and fails on its own.
 */
export default function MTProtoPage() {
  return (
    <div className="max-w-2xl space-y-4">
      <PageHeader title="Telegram"
        subtitle="Прокси для Telegram в кабинете подписчика: WEB-прокси и классический MTProto (запасной)." />
      <WebProxyCard />
      <ClassicMTProtoCard />
    </div>
  )
}
