# HeYearth + Flutter: не обфусцировать классы, нужные для отражения и плагинов
-keep class io.flutter.app.** { *; }
-keep class io.flutter.plugin.** { *; }
-keep class com.wisecodex.flutter_v2ray.** { *; }
-keep class com.example.heyearth_vpn.** { *; }

# Сохранить нативные методы
-keepclasseswithmembernames class * {
    native <methods>;
}

# Общие правила для Android
-dontwarn org.conscrypt.**
-dontwarn org.bouncycastle.**
