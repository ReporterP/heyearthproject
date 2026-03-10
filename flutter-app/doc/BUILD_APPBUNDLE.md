# Сборка AAB для Google Play

## Команда

```bash
flutter build appbundle --no-tree-shake-icons
```

Или из корня `app/`:

```bash
./build_appbundle.sh
```

Результат: `build/app/outputs/bundle/release/app-release.aab`

## Зачем `--no-tree-shake-icons`

При сборке в WSL (когда Flutter установлен в Windows) может возникать ошибка `IconTreeShakerException: ConstFinder failure`. Флаг отключает tree-shaking иконок и позволяет сборке пройти. На размер приложения это влияет незначительно.

## Подпись

Сейчас в release используется debug-подпись (чтобы сборка работала «из коробки»). Для публикации в Google Play нужно настроить свой keystore и указать его в `android/app/build.gradle.kts` и `key.properties` — тогда первый загруженный AAB можно будет подписать через App signing by Google Play.
