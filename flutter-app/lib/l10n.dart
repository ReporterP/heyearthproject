import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:heyearth_vpn/app_state.dart';

/// Локализация приложения (ru / en).
class L10n {
  L10n(this.locale);
  final Locale locale;

  static L10n of(BuildContext context) {
    final state = context.watch<AppState>();
    return L10n(Locale(state.locale));
  }

  bool get isEn => locale.languageCode == 'en';

  // ── Navigation ──────────────────────────────────────────────────────────────

  String get navHome => isEn ? 'Home' : 'Главная';
  String get navSettings => isEn ? 'Settings' : 'Настройки';

  // ── App general ─────────────────────────────────────────────────────────────

  String get appTitle => 'HeYearth';
  String get subtitle => isEn
      ? 'One account — all devices. Tap the button to connect.'
      : 'Один аккаунт — все устройства. Нажмите кнопку и подключитесь.';

  // ── Auth ────────────────────────────────────────────────────────────────────

  String get firstLaunch => isEn ? 'First launch' : 'Первый запуск';
  String get chooseHowToContinue =>
      isEn ? 'Choose how to continue:' : 'Выберите, как продолжить:';
  String get imNewUser => isEn ? "I'm a new user" : 'Я новый пользователь';
  String get iHaveId => isEn ? 'I already have an ID' : 'У меня уже есть ID';
  String get enterYourId => isEn ? 'Enter your ID' : 'Введите ваш ID';
  String get heyearthId => 'HeYearth ID';
  String get cancel => isEn ? 'Cancel' : 'Отмена';
  String get signIn => isEn ? 'Sign in' : 'Войти';
  String get accountRestored =>
      isEn ? 'Account restored.' : 'Аккаунт восстановлен.';
  String get error => isEn ? 'Error' : 'Ошибка';

  String get signInWithIdFromDevice => isEn
      ? 'Sign in with ID from another device'
      : 'Войти по ID с другого устройства';

  String get signInWithHeyearthId =>
      isEn ? 'Sign in with HeYearth ID' : 'Войти по HeYearth ID';
  String get pasteHeyearthId =>
      isEn ? 'Paste your HeYearth ID' : 'Вставьте ваш HeYearth ID';
  String get loginFailed =>
      isEn ? 'Failed to log in.' : 'Не удалось войти.';
  String get login => isEn ? 'Sign in' : 'Войти';
  String get registerFailed =>
      isEn ? 'Failed to register.' : 'Не удалось зарегистрироваться.';

  // ── Welcome screen (new anonymous auth flow) ──────────────────────────────

  String get welcomeTitle => 'HeYearth';
  String get welcomeDesc => isEn
      ? 'Secure VPN for all your devices. Get your HeYearth ID to start.'
      : 'Безопасный VPN для всех ваших устройств. Получите HeYearth ID, чтобы начать.';
  String get getHeyearthId =>
      isEn ? 'Get HeYearth ID' : 'Получить HeYearth ID';
  String get alreadyHaveId =>
      isEn ? 'I already have an ID' : 'У меня уже есть ID';
  String get saveYourIdWarning => isEn
      ? 'Save your ID! Without it you will lose access to your account.'
      : 'Сохраните ваш ID! Без него вы потеряете доступ к аккаунту.';
  String get idOrUsername =>
      isEn ? 'ID or username' : 'ID или имя пользователя';
  String get passwordOptional =>
      isEn ? 'Password (optional)' : 'Пароль (необязательно)';
  String get registering =>
      isEn ? 'Registering...' : 'Регистрация...';

  // ── Account screen ────────────────────────────────────────────────────────

  String get accountScreenTitle =>
      isEn ? 'Account' : 'Аккаунт';
  String get yourHeyearthId =>
      isEn ? 'Your HeYearth ID' : 'Ваш HeYearth ID';
  String get usernameLabel =>
      isEn ? 'Username' : 'Имя пользователя';
  String get usernameHint =>
      isEn ? 'Set a username' : 'Задайте имя пользователя';
  String get saveButton =>
      isEn ? 'Save' : 'Сохранить';
  String get usernameSaved =>
      isEn ? 'Username saved.' : 'Имя пользователя сохранено.';
  String get cloudPasswordLabel =>
      isEn ? 'Cloud Password' : 'Облачный пароль';
  String get newPasswordHint =>
      isEn ? 'New password' : 'Новый пароль';
  String get confirmPasswordHint =>
      isEn ? 'Confirm password' : 'Подтвердите пароль';
  String get currentPasswordHint =>
      isEn ? 'Current password' : 'Текущий пароль';
  String get passwordMismatch =>
      isEn ? 'Passwords do not match.' : 'Пароли не совпадают.';
  String get passwordSaved =>
      isEn ? 'Password saved.' : 'Пароль сохранён.';
  String get subscriptionSection =>
      isEn ? 'Subscription' : 'Подписка';
  String get noActiveSubscription =>
      isEn ? 'No active subscription.' : 'Нет активной подписки.';
  String get payButton =>
      isEn ? 'Pay' : 'Оплатить';
  String get connectionModesSection =>
      isEn ? 'Connection Modes' : 'Режимы подключения';
  String get vpnMode => 'VPN';
  String get socks5Mode => 'SOCKS5';
  String get httpMode => 'HTTP';
  String get copyUri =>
      isEn ? 'Copy URI' : 'Скопировать URI';
  String get uriCopied =>
      isEn ? 'URI copied to clipboard.' : 'URI скопирован в буфер обмена.';

  // ── Account / profile panel ─────────────────────────────────────────────────

  String get accountManagedByServer => isEn
      ? 'Account, access and connection are managed by HeYearth Server.'
      : 'Аккаунт, доступ и подключение управляются через HeYearth Server.';
  String get idCopied => isEn ? 'ID copied' : 'ID скопирован';
  String get profileLabel => isEn ? 'Profile' : 'Профиль';
  String get planLabel => isEn ? 'Plan' : 'План';
  String get platformLabel => isEn ? 'Platform' : 'Платформа';
  String get statusLabel => isEn ? 'Status' : 'Статус';
  String get validUntilLabel => isEn ? 'Until' : 'До';
  String get signOut => isEn ? 'Sign out' : 'Выйти';
  String get updateAccount => isEn ? 'Update account' : 'Обновить аккаунт';
  String get updatingAccount => isEn ? 'Updating...' : 'Обновление...';
  String get myHeyearthId => isEn ? 'My HeYearth ID' : 'Мой HeYearth ID';
  String get accountsSection => isEn ? 'Accounts' : 'Аккаунты';
  String get addAccount => isEn ? 'Add Account' : 'Добавить аккаунт';
  String get addAccountHint =>
      isEn ? 'HeYearth ID or username' : 'HeYearth ID или имя пользователя';
  String get currentAccount => isEn ? 'Current' : 'Текущий';
  String get switchAccount => isEn ? 'Switch' : 'Переключить';
  String get removeAccount => isEn ? 'Remove' : 'Удалить';

  // ── Profile screen ──────────────────────────────────────────────────────────

  String get profileScreenTitle =>
      isEn ? 'Profile & Access' : 'Профиль и доступ';
  String get profileSection => isEn ? 'Profile' : 'Профиль';
  String get accessSection => isEn ? 'Access' : 'Доступ';
  String get accessPlan => isEn ? 'Plan' : 'План';
  String get accessValidUntil => isEn ? 'Valid until' : 'Действует до';
  String get accessConnection => isEn ? 'Connection' : 'Подключение';
  String get subscriptionsSection => isEn ? 'Subscriptions' : 'Подписки';
  String get noSubscriptions =>
      isEn ? 'No subscriptions found.' : 'Подписки пока не найдены.';
  String get untilLabel => isEn ? 'Until' : 'До';
  String get paymentsSection => isEn ? 'Payments' : 'Платежи';
  String get noPayments => isEn ? 'No payments yet.' : 'Платежей пока нет.';
  String get paymentStatus => isEn ? 'Status' : 'Статус';
  String get paymentDate => isEn ? 'Date' : 'Дата';
  String get promoCodeSection => isEn ? 'Promo Code' : 'Промокод';
  String get promoCodeHint => isEn ? 'Enter code' : 'Введите код';
  String get applyButton => isEn ? 'Apply' : 'Применить';
  String get promoApplied =>
      isEn ? 'Promo code applied.' : 'Промокод применён.';
  String get promoFailed =>
      isEn ? 'Failed to apply promo code.' : 'Не удалось применить промокод.';

  // ── VPN / connection ─────────────────────────────────────────────────────────

  String get connecting => isEn ? 'Connecting...' : 'Подключение...';
  String get connectedTraffic => isEn
      ? 'Connected. Traffic goes through HeYearth.'
      : 'Подключено. Трафик идёт через HeYearth.';
  String get tapToEnable => isEn
      ? 'Tap the button to enable HeYearth.'
      : 'Нажмите кнопку, чтобы включить HeYearth.';
  String get turnOffHeyearth =>
      isEn ? 'Turn off HeYearth' : 'Выключить HeYearth';
  String get turnOnHeyearth => isEn ? 'Turn on HeYearth' : 'Включить HeYearth';
  String get connectionUnavailable =>
      isEn ? 'Connection not available.' : 'Подключение недоступно.';

  // ── Updates ──────────────────────────────────────────────────────────────────

  String get checkForUpdates =>
      isEn ? 'Check for updates' : 'Проверить обновления';
  String get checkingUpdates =>
      isEn ? 'Checking for updates...' : 'Проверка обновлений...';
  String get youHaveLatestVersion =>
      isEn ? 'You have the latest version.' : 'У вас последняя версия.';
  String latestOnServer(String ver) => isEn
      ? 'Server has $ver. You have the latest version.'
      : 'На сервере $ver. У вас последняя версия.';
  String get updateAvailable =>
      isEn ? 'Update available' : 'Доступно обновление';
  String versionAvailable(String ver) => isEn
      ? 'Version $ver is available for download.'
      : 'Версия $ver доступна для скачивания.';
  String get later => isEn ? 'Later' : 'Позже';
  String get download => isEn ? 'Download' : 'Скачать';
  String versionNoLink(String ver) => isEn
      ? 'Version $ver is available. Download link for your platform is not set up yet.'
      : 'Доступна версия $ver. Ссылка для вашей платформы пока не настроена.';
  String get updateRequired =>
      isEn ? 'This update is required to continue.' : 'Это обновление обязательно для продолжения работы.';
  String minSupportedVersionLabel(String ver) =>
      isEn ? 'Minimum supported version: $ver' : 'Минимально поддерживаемая версия: $ver';
  String get updateIsMandatory =>
      isEn ? 'This update is mandatory.' : 'Это обновление обязательно.';

  // ── Settings ─────────────────────────────────────────────────────────────────

  String get restoreById => isEn ? 'Sign in by ID' : 'Войти по ID';
  String get pasteHeyearthIdHint =>
      isEn ? 'Paste your HeYearth ID' : 'Вставьте ваш HeYearth ID';

  String get theme => isEn ? 'Theme' : 'Тема';
  String get system => isEn ? 'System' : 'Система';
  String get light => isEn ? 'Light' : 'Светлая';
  String get dark => isEn ? 'Dark' : 'Тёмная';

  String get language => isEn ? 'Language' : 'Язык';
  String get langRu => 'Русский';
  String get langEn => 'English';

  String get version => isEn ? 'Version' : 'Версия';
  String versionLabel(String ver) => isEn ? 'Version $ver' : 'Версия $ver';

  // ── Platform unsupported ─────────────────────────────────────────────────────

  String get unsupportedTitle => isEn
      ? 'HeYearth in this app works only on Android, iOS and Windows.'
      : 'HeYearth в этом приложении работает только на Android, iOS и Windows.';
  String get unsupportedHint => isEn
      ? 'On phone or tablet open the app — there is a single "Turn on HeYearth" button.'
      : 'На телефоне или планшете откройте приложение — там одна кнопка «Включить HeYearth».';

  String get initXray => isEn ? 'Initializing Xray...' : 'Инициализация Xray...';
  String get waitLaunchHeyearth =>
      isEn ? 'Please wait, launching HeYearth.' : 'Подождите, запуск HeYearth.';

  // ── Error messages (app_state) ───────────────────────────────────────────────

  String get serverNoResponse =>
      isEn ? 'Server did not respond.' : 'Сервер не ответил.';
  String get serverNoConfig =>
      isEn ? 'Server did not return connection config.' : 'Сервер не вернул конфиг подключения.';
  String get vpnUnsupported =>
      isEn ? 'HeYearth is only available on Android, iOS and Windows.' : 'HeYearth доступен только на Android, iOS и Windows.';
  String mandatoryUpdateRequiredMsg(String ver) => isEn
      ? 'Mandatory update required to $ver.'
      : 'Требуется обязательное обновление до $ver.';
  String get versionUnsupported =>
      isEn ? 'Current version is no longer supported. Update the app.' : 'Текущая версия больше не поддерживается. Обнови приложение.';
  String get accessBlocked =>
      isEn ? 'Access blocked. Contact support.' : 'Доступ заблокирован. Обратись в поддержку.';
  String get currentConnectionUnavailable =>
      isEn ? 'Connection is not available right now.' : 'Сейчас подключение недоступно.';

  // ── gRPC error → user-friendly message ──────────────────────────────────────

  String grpcErrorToMessage(dynamic e) {
    final raw = e.toString();
    final clean = raw.replaceFirst(RegExp(r'^Exception:?\s*'), '');

    if (clean.contains('NOT_FOUND') ||
        clean.contains('code: 5,') ||
        clean.contains('not found')) {
      return isEn
          ? 'Account not found. Check the entered data.'
          : 'Аккаунт не найден. Проверьте введённые данные.';
    }

    if (clean.contains('UNAVAILABLE') ||
        clean.contains('code: 14,') ||
        clean.contains('HandshakeException') ||
        clean.contains('SocketException') ||
        clean.contains('Connection refused') ||
        clean.contains('Connection reset') ||
        clean.contains('Failed host lookup')) {
      return isEn
          ? 'Server is unavailable. Check your internet connection.'
          : 'Сервер недоступен. Проверь подключение к интернету.';
    }

    if (clean.contains('DEADLINE_EXCEEDED') ||
        clean.contains('code: 4,') ||
        clean.contains('TimeoutException') ||
        clean.contains('timed out')) {
      return isEn
          ? 'Request timed out. Check your connection and try again.'
          : 'Таймаут запроса. Проверь соединение и попробуй снова.';
    }

    if (clean.contains('PERMISSION_DENIED') ||
        clean.contains('code: 7,') ||
        clean.contains('UNAUTHENTICATED') ||
        clean.contains('code: 16,')) {
      return isEn ? 'Access denied.' : 'Доступ запрещён.';
    }

    if (clean.contains('INVALID_ARGUMENT') || clean.contains('code: 3,')) {
      return isEn
          ? 'Invalid request. Check the entered data.'
          : 'Неверный запрос. Проверь введённые данные.';
    }

    if (clean.contains('RESOURCE_EXHAUSTED') || clean.contains('code: 8,')) {
      return isEn
          ? 'Too many requests. Try again later.'
          : 'Слишком много запросов. Попробуй позже.';
    }

    if (clean.contains('ALREADY_EXISTS') || clean.contains('code: 6,')) {
      return isEn
          ? 'This item already exists.'
          : 'Такой объект уже существует.';
    }

    if (clean.contains('TLSV1') ||
        clean.contains('certificate') ||
        clean.contains('TLS') ||
        clean.contains('SSL')) {
      return isEn
          ? 'Secure connection error. Check the server address.'
          : 'Ошибка защищённого соединения. Проверь адрес сервера.';
    }

    if (clean.contains('gRPC Error') || clean.contains('GrpcError')) {
      return isEn
          ? 'Server error. Try again.'
          : 'Ошибка сервера. Попробуй снова.';
    }

    return clean;
  }

  String translateError(String? msg) {
    if (msg == null || msg.isEmpty) return '';
    if (msg.contains('gRPC Error') ||
        msg.contains('GrpcError') ||
        msg.contains('HandshakeException') ||
        msg.contains('SocketException') ||
        msg.contains('TimeoutException') ||
        msg.contains('UNAVAILABLE') ||
        msg.contains('NOT_FOUND') ||
        msg.contains('DEADLINE_EXCEEDED') ||
        msg.contains('PERMISSION_DENIED') ||
        msg.contains('UNAUTHENTICATED') ||
        msg.contains('TLSV1') ||
        msg.contains('SSL')) {
      return grpcErrorToMessage(msg);
    }
    if (!isEn) return msg;
    if (msg.contains('HeYearth доступен только на Android'))
      return vpnUnsupported;
    if (msg.contains('Сервер недоступен'))
      return 'Server unavailable. Check internet and that the site is reachable.';
    if (msg.contains('Таймаут') && msg.contains('Сервер не отвечает'))
      return 'Timeout. Server is not responding.';
    if (msg.contains('Сервер не вернул конфиг')) return serverNoConfig;
    if (msg.contains('Не удалось применить промокод')) return promoFailed;
    if (msg.contains('Сервер не ответил')) return serverNoResponse;
    if (msg.contains('Доступ заблокирован')) return accessBlocked;
    if (msg.contains('Сейчас подключение недоступно'))
      return currentConnectionUnavailable;
    if (msg.contains('Требуется обязательное обновление')) {
      final ver =
          RegExp(r'до (.+)\.$').firstMatch(msg)?.group(1) ?? '';
      return mandatoryUpdateRequiredMsg(ver);
    }
    if (msg.contains('Текущая версия больше не поддерживается'))
      return versionUnsupported;
    if (msg.contains('Не удалось зарегистрироваться'))
      return registerFailed;
    if (msg.contains('Не удалось войти'))
      return loginFailed;
    return msg;
  }
}
