import 'package:flutter/widgets.dart';

// Provides locale to the entire admin_web widget tree via InheritedWidget.
class AdminLocaleProvider extends InheritedWidget {
  const AdminLocaleProvider({
    super.key,
    required this.locale,
    required super.child,
  });

  final String locale;

  static String localeOf(BuildContext context) {
    final provider =
        context.dependOnInheritedWidgetOfExactType<AdminLocaleProvider>();
    return provider?.locale ?? 'en';
  }

  @override
  bool updateShouldNotify(AdminLocaleProvider old) => locale != old.locale;
}

/// Localization for admin_web (en / ru).
class AdminL10n {
  AdminL10n(this.locale);

  final String locale;

  static AdminL10n of(BuildContext context) =>
      AdminL10n(AdminLocaleProvider.localeOf(context));

  bool get isEn => locale == 'en';

  // ── General ──────────────────────────────────────────────────────────────────

  String get appTitle => 'HeYearth Admin';
  String get appSubtitle =>
      isEn ? 'Admin console' : 'Панель администратора';
  String get loading => isEn ? 'Loading…' : 'Загрузка…';
  String get save => isEn ? 'Save' : 'Сохранить';
  String get cancel => isEn ? 'Cancel' : 'Отмена';
  String get create => isEn ? 'Create' : 'Создать';
  String get edit => isEn ? 'Edit' : 'Редактировать';
  String get delete => isEn ? 'Delete' : 'Удалить';
  String get close => isEn ? 'Close' : 'Закрыть';
  String get retry => isEn ? 'Retry' : 'Повторить';
  String get upload => isEn ? 'Upload' : 'Загрузить';
  String get activate => isEn ? 'Activate' : 'Активировать';
  String get logout => isEn ? 'Logout' : 'Выйти';
  String get language => isEn ? 'Language' : 'Язык';
  String get theme => isEn ? 'Theme' : 'Тема';
  String get themeSystem => isEn ? 'System' : 'Системная';
  String get themeLight => isEn ? 'Light' : 'Светлая';
  String get themeDark => isEn ? 'Dark' : 'Тёмная';

  // ── Login ────────────────────────────────────────────────────────────────────

  String get loginSubtitle => isEn ? 'Same-origin admin console' : 'Панель управления';
  String get username => isEn ? 'Username' : 'Логин';
  String get password => isEn ? 'Password' : 'Пароль';
  String get login => isEn ? 'Login' : 'Войти';

  // ── Navigation sections ───────────────────────────────────────────────────────

  String get navDashboard => isEn ? 'Dashboard' : 'Дашборд';
  String get navUsers => isEn ? 'Users' : 'Пользователи';
  String get navGroups => isEn ? 'Groups' : 'Группы';
  String get navSubscriptions => isEn ? 'Subscriptions' : 'Подписки';
  String get navPromocodes => isEn ? 'Promocodes' : 'Промокоды';
  String get navPayments => isEn ? 'Payments' : 'Платежи';
  String get navServers => isEn ? 'Servers' : 'Серверы';
  String get navSshKeys => isEn ? 'SSH Keys' : 'SSH Ключи';
  String get navReleases => isEn ? 'Releases' : 'Релизы';
  String get navAdmins => isEn ? 'Admins' : 'Администраторы';
  String get navSettings => isEn ? 'Settings' : 'Настройки';

  // ── Dashboard ──────────────────────────────────────────────────────────────────

  String get dashboardTitle => isEn ? 'Dashboard' : 'Дашборд';
  String get dashUsers => isEn ? 'Users' : 'Пользователи';
  String get dashActiveUsers => isEn ? 'Active users' : 'Активных';
  String get dashServers => isEn ? 'Servers' : 'Серверы';
  String get dashOnlineServers => isEn ? 'Online servers' : 'Онлайн';
  String get dashGroups => isEn ? 'Groups' : 'Группы';
  String get dashPromocodes => isEn ? 'Promocodes' : 'Промокоды';
  String get dashPayments => isEn ? 'Payments' : 'Платежи';
  String get dashActiveReleases => isEn ? 'Active releases' : 'Активных релизов';
  String get dashAdmins => isEn ? 'Admins' : 'Администраторы';

  // ── Users ────────────────────────────────────────────────────────────────────

  String get usersTitle => isEn ? 'Users' : 'Пользователи';
  String get createUser => isEn ? 'Create user' : 'Создать пользователя';
  String get editUser => isEn ? 'Edit user' : 'Редактировать пользователя';
  String get deleteUser => isEn ? 'Delete user' : 'Удалить пользователя';
  String deleteUserConfirm(String userId) => isEn
      ? 'Delete user "$userId"? This action cannot be undone. Payment records will be preserved but unlinked.'
      : 'Удалить пользователя "$userId"? Это действие нельзя отменить. Платёжные записи сохранятся, но будут отвязаны.';
  String get noUsers => isEn ? 'No users found' : 'Пользователи не найдены';
  String get fieldUserId => isEn ? 'User ID' : 'User ID';
  String get fieldTelegramId => isEn ? 'Telegram ID' : 'Telegram ID';
  String get fieldRole => isEn ? 'Role' : 'Роль';
  String get fieldPreferredPlatform =>
      isEn ? 'Preferred platform (legacy/bot)' : 'Предпочтительная платформа (legacy/бот)';
  String get fieldPlatformNotSet => isEn ? '— not set —' : '— не задана —';
  String get fieldBlocked => isEn ? 'Blocked' : 'Заблокирован';
  String registeredDevicesLabel(int n) =>
      isEn ? 'Registered devices ($n)' : 'Зарегистрированные устройства ($n)';
  String get deviceLastSeen => isEn ? 'Last seen' : 'Последний вход';
  String get userRolePlan => isEn ? 'Role' : 'Роль';
  String get userPlan => isEn ? 'Plan' : 'План';
  String get blockedChip => isEn ? 'Blocked' : 'Заблокирован';

  // ── Groups ────────────────────────────────────────────────────────────────────

  String get groupsTitle => isEn ? 'Groups' : 'Группы';
  String get createGroup => isEn ? 'Create group' : 'Создать группу';
  String get editGroup => isEn ? 'Edit group' : 'Редактировать группу';
  String get noGroups => isEn ? 'No groups found' : 'Группы не найдены';
  String get fieldGroupName => isEn ? 'Name' : 'Название';
  String get fieldDescription => isEn ? 'Description' : 'Описание';
  String get fieldIsPremium => isEn ? 'Premium' : 'Премиум';
  String get fieldCompanyId => isEn ? 'Company ID' : 'Company ID';

  // ── Subscriptions ──────────────────────────────────────────────────────────────

  String get subscriptionsTitle => isEn ? 'Subscriptions' : 'Подписки';
  String get saveSubscription => isEn ? 'Save subscription' : 'Сохранить подписку';
  String get editSubscription => isEn ? 'Edit subscription' : 'Редактировать подписку';
  String get noSubscriptions => isEn ? 'No subscriptions found' : 'Подписки не найдены';
  String get fieldUserDbId => isEn ? 'User DB ID' : 'ID пользователя';
  String get fieldGroupId => isEn ? 'Group ID' : 'ID группы';
  String get fieldValidUntil => isEn ? 'Valid until (ISO)' : 'Действует до (ISO)';
  String get fieldAutoRenew => isEn ? 'Auto renew' : 'Авторенью';

  // ── Promocodes ──────────────────────────────────────────────────────────────────

  String get promocodesTitle => isEn ? 'Promocodes' : 'Промокоды';
  String get createPromocode => isEn ? 'Create promocode' : 'Создать промокод';
  String get editPromocode => isEn ? 'Edit promocode' : 'Редактировать промокод';
  String get noPromocodes => isEn ? 'No promocodes found' : 'Промокоды не найдены';
  String get fieldCode => isEn ? 'Code' : 'Код';
  String get fieldGroupIdOptional => isEn ? 'Group ID (optional)' : 'ID группы (необязательно)';
  String get fieldMaxUses => isEn ? 'Max uses' : 'Макс. использований';
  String get fieldIsActive => isEn ? 'Active' : 'Активен';

  // ── Payments ─────────────────────────────────────────────────────────────────

  String get paymentsTitle => isEn ? 'Payments' : 'Платежи';
  String get createPayment => isEn ? 'Create payment' : 'Создать платёж';
  String get editPayment => isEn ? 'Edit payment' : 'Редактировать платёж';
  String get noPayments => isEn ? 'No payments found' : 'Платежи не найдены';
  String get fieldAmount => isEn ? 'Amount' : 'Сумма';
  String get fieldStatus => isEn ? 'Status' : 'Статус';
  String get fieldProvider => isEn ? 'Provider' : 'Провайдер';

  // ── Servers ───────────────────────────────────────────────────────────────────

  String get serversTitle => isEn ? 'Servers' : 'Серверы';
  String get createServer => isEn ? 'Create server' : 'Создать сервер';
  String get editServer => isEn ? 'Edit server' : 'Редактировать сервер';
  String get noServers => isEn ? 'No servers found' : 'Серверы не найдены';
  String get fieldServerName => isEn ? 'Name' : 'Название';
  String get fieldHost => isEn ? 'Host' : 'Хост';
  String get fieldPort => isEn ? 'SSH Port' : 'SSH Порт';
  String get fieldSshUser => isEn ? 'SSH User' : 'SSH Пользователь';
  String get fieldSshPassword => isEn ? 'SSH Password (leave blank to keep)' : 'SSH Пароль (оставьте пустым, чтобы не менять)';
  String get fieldSshKey => isEn ? 'SSH Key' : 'SSH Ключ';
  String get fieldSshKeyNone => isEn ? '— use password —' : '— использовать пароль —';
  String get fieldLocation => isEn ? 'Location' : 'Локация';
  String get fieldTags => isEn ? 'Tags (comma-separated)' : 'Теги (через запятую)';
  String get serverRestartAgent => isEn ? 'Restart agent' : 'Перезапустить агента';
  String get serverReprovision => isEn ? 'Reprovision' : 'Перенастроить';
  String get serverSyncMetrics => isEn ? 'Sync metrics' : 'Синхронизировать метрики';
  String get serverAuthKey => isEn ? 'key' : 'ключ';
  String get serverAuthPassword => isEn ? 'password' : 'пароль';

  // ── SSH Keys ─────────────────────────────────────────────────────────────────

  String get sshKeysTitle => isEn ? 'SSH Keys' : 'SSH Ключи';
  String get addSshKey => isEn ? 'Add Key' : 'Добавить ключ';
  String get editSshKey => isEn ? 'Update SSH Key' : 'Обновить SSH ключ';
  String get deleteSshKey => isEn ? 'Delete SSH Key' : 'Удалить SSH ключ';
  String deleteSshKeyConfirm(String name) => isEn
      ? 'Delete SSH key "$name"? Servers using this key will fall back to password auth.'
      : 'Удалить SSH ключ "$name"? Серверы с этим ключом переключатся на авторизацию по паролю.';
  String get noSshKeys => isEn
      ? 'No SSH keys found. Add one to get started.'
      : 'SSH ключи не найдены. Добавьте первый ключ.';
  String get fieldKeyName => isEn ? 'Name' : 'Название';
  String get fieldPrivateKey => isEn ? 'Private key (PEM)' : 'Приватный ключ (PEM)';
  String get fieldPrivateKeyOptional =>
      isEn ? 'Private key (PEM, leave blank to keep)' : 'Приватный ключ (PEM, оставьте пустым, чтобы не менять)';
  String get labelFingerprint => isEn ? 'Fingerprint' : 'Отпечаток';

  // ── Releases ──────────────────────────────────────────────────────────────────

  String get releasesTitle => isEn ? 'Releases' : 'Релизы';
  String get uploadRelease => isEn ? 'Upload release' : 'Загрузить релиз';
  String get noReleases => isEn ? 'No releases uploaded' : 'Релизы не загружены';
  String get fieldVersion => isEn ? 'Version' : 'Версия';
  String get fieldPlatform => isEn ? 'Platform' : 'Платформа';
  String get fieldReleaseNotes => isEn ? 'Release notes' : 'Примечания к релизу';
  String get fieldMandatory => isEn ? 'Mandatory update' : 'Обязательное обновление';
  String get fieldMinSupportedVersion =>
      isEn ? 'Min supported version' : 'Минимальная поддерживаемая версия';
  String get fieldApkFile => isEn ? 'APK file' : 'APK файл';
  String get fieldFileSelected => isEn ? 'File selected' : 'Файл выбран';
  String get fieldSelectFile => isEn ? 'Select file' : 'Выбрать файл';
  String get releaseActive => isEn ? 'Active' : 'Активен';
  String get releaseInactive => isEn ? 'Inactive' : 'Неактивен';

  // ── Admins ────────────────────────────────────────────────────────────────────

  String get adminsTitle => isEn ? 'Admins' : 'Администраторы';
  String get createAdmin => isEn ? 'Create admin' : 'Создать администратора';
  String get editAdmin => isEn ? 'Edit admin' : 'Редактировать администратора';
  String get createRole => isEn ? 'Create role' : 'Создать роль';
  String get editRole => isEn ? 'Edit role' : 'Редактировать роль';
  String get sectionAdminUsers => isEn ? 'Admin users' : 'Пользователи-администраторы';
  String get sectionRoles => isEn ? 'Roles' : 'Роли';
  String get fieldDisplayName => isEn ? 'Display name' : 'Отображаемое имя';
  String get fieldNewPassword => isEn ? 'New password (leave blank to keep)' : 'Новый пароль (оставьте пустым, чтобы не менять)';
  String get fieldRoles => isEn ? 'Roles (comma-separated)' : 'Роли (через запятую)';
  String get fieldRoleKey => isEn ? 'Role key' : 'Ключ роли';
  String get fieldPermissions => isEn ? 'Permissions (comma-separated)' : 'Права (через запятую)';
  String get fieldAdminIsActive => isEn ? 'Active' : 'Активен';
  String get fieldIsSystem => isEn ? 'System role' : 'Системная роль';

  // ── Settings ──────────────────────────────────────────────────────────────────

  String get settingsTitle => isEn ? 'Settings' : 'Настройки';
  String get settingsAccount => isEn ? 'Account' : 'Аккаунт';
  String get settingsUsername => isEn ? 'Username' : 'Логин';
  String get settingsRoles => isEn ? 'Roles' : 'Роли';
  String get settingsPermissions => isEn ? 'Permissions' : 'Права';
  String get settingsTheme => isEn ? 'Theme' : 'Тема';
  String get settingsLanguage => isEn ? 'Interface language' : 'Язык интерфейса';
  String get settingsLogout => isEn ? 'Logout' : 'Выйти';
}
