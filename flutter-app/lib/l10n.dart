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

  String get navHome => isEn ? 'Home' : 'Главная';
  String get navSettings => isEn ? 'Settings' : 'Настройки';

  String get appTitle => isEn ? 'HeYearth' : 'HeYearth';
  String get subtitle => isEn
      ? 'One account — all devices. Tap the button to connect.'
      : 'Один аккаунт — все устройства. Нажмите кнопку и подключитесь.';

  String get firstLaunch => isEn ? 'First launch' : 'Первый запуск';
  String get chooseHowToContinue => isEn ? 'Choose how to continue:' : 'Выберите, как продолжить:';
  String get imNewUser => isEn ? "I'm a new user" : 'Я новый пользователь';
  String get iHaveId => isEn ? 'I already have an ID' : 'У меня уже есть ID';
  String get enterYourId => isEn ? 'Enter your ID' : 'Введите ваш ID';
  String get heyearthId => isEn ? 'HeYearth ID' : 'HeYearth ID';
  String get cancel => isEn ? 'Cancel' : 'Отмена';
  String get signIn => isEn ? 'Sign in' : 'Войти';
  String get accountRestored => isEn ? 'Account restored.' : 'Аккаунт восстановлен.';
  String get error => isEn ? 'Error' : 'Ошибка';

  String get signInWithIdFromDevice => isEn
      ? 'Sign in with ID from another device'
      : 'Войти по ID с другого устройства';
  String get checkForUpdates => isEn ? 'Check for updates' : 'Проверить обновления';
  String get checkingUpdates => isEn ? 'Checking for updates…' : 'Проверка обновлений…';
  String get youHaveLatestVersion => isEn ? 'You have the latest version.' : 'У вас последняя версия.';
  String latestOnServer(String ver) =>
      isEn ? 'Server has $ver. You have the latest version.' : 'На сервере $ver. У вас последняя версия.';
  String get updateAvailable => isEn ? 'Update available' : 'Доступно обновление';
  String versionAvailable(String ver) =>
      isEn ? 'Version $ver is available for download.' : 'Версия $ver доступна для скачивания.';
  String get later => isEn ? 'Later' : 'Позже';
  String get download => isEn ? 'Download' : 'Скачать';
  String versionNoLink(String ver) => isEn
      ? 'Version $ver is available. Download link for your platform is not set up yet.'
      : 'Доступна версия $ver. Ссылка для вашей платформы пока не настроена.';

  String get restoreById => isEn ? 'Sign in by ID' : 'Войти по ID';
  String get pasteHeyearthId => isEn ? 'Paste your HeYearth ID' : 'Вставьте ваш HeYearth ID';

  String get connecting => isEn ? 'Connecting…' : 'Подключение...';
  String get connectedTraffic => isEn
      ? 'Connected. Traffic goes through HeYearth.'
      : 'Подключено. Трафик идёт через HeYearth.';
  String get tapToEnable => isEn
      ? 'Tap the button to enable HeYearth.'
      : 'Нажмите кнопку, чтобы включить HeYearth.';
  String get turnOffHeyearth => isEn ? 'Turn off HeYearth' : 'Выключить HeYearth';
  String get turnOnHeyearth => isEn ? 'Turn on HeYearth' : 'Включить HeYearth';

  String get myHeyearthId => isEn ? 'My HeYearth ID' : 'Мой HeYearth ID';
  String get idCopied => isEn ? 'ID copied' : 'ID скопирован';

  String get onlyForSelectedServices => isEn
      ? 'Only for selected services'
      : 'Только для выбранных сервисов';
  String get onlyForSelectedHint => isEn
      ? 'In this mode only traffic to selected services goes through HeYearth; the rest is direct.'
      : 'В этом режиме через HeYearth идёт только трафик к выбранным сервисам; остальное — напрямую.';

  String get theme => isEn ? 'Theme' : 'Тема';
  String get system => isEn ? 'System' : 'Система';
  String get light => isEn ? 'Light' : 'Светлая';
  String get dark => isEn ? 'Dark' : 'Тёмная';

  String get language => isEn ? 'Language' : 'Язык';
  String get langRu => 'Русский';
  String get langEn => 'English';

  String get version => isEn ? 'Version' : 'Версия';
  String versionLabel(String ver) => isEn ? 'Version $ver' : 'Версия $ver';

  String get unsupportedTitle => isEn
      ? 'HeYearth in this app works only on Android, iOS and Windows.'
      : 'HeYearth в этом приложении работает только на Android, iOS и Windows.';
  String get unsupportedHint => isEn
      ? 'On phone or tablet open the app — there is a single "Turn on HeYearth" button.'
      : 'На телефоне или планшете откройте приложение — там одна кнопка «Включить HeYearth».';

  String get initXray => isEn ? 'Initializing Xray…' : 'Инициализация Xray…';
  String get waitLaunchHeyearth => isEn ? 'Please wait, launching HeYearth.' : 'Подождите, запуск HeYearth.';
  String get modeSelectedOnly => isEn ? '"Selected only" mode' : 'Режим «только выбранные»';

  /// Переводит известные сообщения об ошибках (из app_state); остальные возвращает как есть.
  String translateError(String? msg) {
    if (msg == null || msg.isEmpty) return '';
    if (!isEn) return msg;
    if (msg.contains('HeYearth доступен только на Android')) return 'HeYearth is only available on Android, iOS and Windows.';
    if (msg.contains('Сервер недоступен')) return 'Server unavailable. Check internet and that the site is reachable.';
    if (msg.contains('Таймаут') && msg.contains('Сервер не отвечает')) return 'Timeout. Server is not responding.';
    if (msg.contains('Сервер не вернул конфиг')) return 'Server did not return config. Try again.';
    return msg;
  }
}
