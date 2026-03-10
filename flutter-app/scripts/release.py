#!/usr/bin/env python3
"""
Выкладка новой версии приложения на сайт (без Google Play / Microsoft Store).
Сборка локально, загрузка на сервер, обновление API и запись в releases/.

Использование:
  python3 scripts/release.py --android              # версия из pubspec.yaml
  python3 scripts/release.py --android -v 1.2.6     # явно указать версию (должна совпадать с pubspec)
  python3 scripts/release.py --windows -v 1.2.6
  python3 scripts/release.py --android --windows --ios -v 1.2.6

iOS-сборка возможна только на macOS с установленным Xcode. Требования: .env (DEPLOY_*), на сервере nginx location /releases/, heyearth-api с EnvironmentFile.
"""

import argparse
import json
import os
import re
import subprocess
import sys
from datetime import datetime
from pathlib import Path

# Корень Flutter-проекта (родитель папки scripts); pubspec.yaml и lib/ здесь
PROJECT_ROOT = Path(__file__).resolve().parent.parent
APP_DIR = PROJECT_ROOT
RELEASES_DIR = APP_DIR / "releases"
RELEASES_JSON = RELEASES_DIR / "releases.json"
PUBSPEC = APP_DIR / "pubspec.yaml"

# Сервер: каталог релизов и файл с переменными для API
REMOTE_RELEASES_DIR = "/opt/vpn-bot/releases"
REMOTE_APP_UPDATE_ENV = "/opt/vpn-bot/app-update.env"


def load_dotenv() -> dict:
    env_path = PROJECT_ROOT / ".env"
    result = {}
    if not env_path.is_file():
        return result
    with open(env_path) as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            if "=" in line:
                k, v = line.split("=", 1)
                result[k.strip()] = v.strip().strip('"').strip("'")
    return result


def run(cmd: list[str], cwd: Path | None = None, env: dict | None = None) -> tuple[str, str, int]:
    env = (os.environ.copy() if env is None else env) if env is not None else os.environ.copy()
    try:
        r = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            cwd=cwd or PROJECT_ROOT,
            env=env,
            timeout=600,
        )
        return (r.stdout or "", r.stderr or "", r.returncode)
    except subprocess.TimeoutExpired:
        return ("", "timeout", -1)
    except FileNotFoundError:
        return ("", "command not found", -1)


def run_ssh(host: str, user: str, password: str | None, remote_cmd: str) -> tuple[str, str, int]:
    target = f"{user}@{host}"
    cmd = [
        "ssh",
        "-o", "ConnectTimeout=15",
        "-o", "StrictHostKeyChecking=accept-new",
        "-o", "BatchMode=no",
        target,
        remote_cmd,
    ]
    env = os.environ.copy()
    if password:
        env["SSHPASS"] = password
        cmd = ["sshpass", "-e"] + cmd
    try:
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=60, env=env)
        return (r.stdout or "", r.stderr or "", r.returncode)
    except Exception as e:
        return ("", str(e), -1)


def run_scp(host: str, user: str, password: str | None, local_path: Path, remote_path: str) -> tuple[str, str, int]:
    target = f"{user}@{host}:{remote_path}"
    cmd = ["scp", "-o", "ConnectTimeout=15", "-o", "StrictHostKeyChecking=accept-new", str(local_path), target]
    env = os.environ.copy()
    if password:
        env["SSHPASS"] = password
        cmd = ["sshpass", "-e"] + cmd
    try:
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=120, env=env)
        return (r.stdout or "", r.stderr or "", r.returncode)
    except Exception as e:
        return ("", str(e), -1)


def get_version_from_pubspec() -> str:
    text = PUBSPEC.read_text(encoding="utf-8")
    m = re.search(r"version:\s*([\d.]+)", text)
    if not m:
        raise SystemExit("Не найден version в app/pubspec.yaml")
    return m.group(1).strip()


def load_releases_json() -> dict:
    if not RELEASES_JSON.is_file():
        return {"android": [], "windows": [], "macos": [], "linux": [], "ios": []}
    with open(RELEASES_JSON, encoding="utf-8") as f:
        data = json.load(f)
    for k in ("android", "windows", "macos", "linux", "ios"):
        if k not in data:
            data[k] = []
    return data


def save_releases_json(data: dict) -> None:
    RELEASES_DIR.mkdir(parents=True, exist_ok=True)
    with open(RELEASES_JSON, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2)


def write_release_note(platform: str, version: str) -> None:
    RELEASES_DIR.mkdir(parents=True, exist_ok=True)
    note_file = RELEASES_DIR / f"{platform}_{version}.txt"
    note_file.write_text(
        f"Версия {version} ({platform})\nВыложено: {datetime.now().strftime('%Y-%m-%d %H:%M')}\n",
        encoding="utf-8",
    )


def build_android(version: str) -> Path:
    # --split-per-abi: отдельный APK под каждую архитектуру (arm64 ~50–70 MB вместо ~190 MB universal)
    # --no-tree-shake-icons обходит IconTreeShakerException на WSL
    out, err, code = run(
        [
            "flutter", "build", "apk", "--release",
            "--split-per-abi",
            "--no-tree-shake-icons",
        ],
        cwd=APP_DIR,
    )
    if code != 0:
        raise SystemExit(f"flutter build apk failed: {err or out}")
    # Берём arm64-v8a — большинство современных устройств; объём в разы меньше universal
    base = APP_DIR / "build" / "app" / "outputs" / "flutter-apk"
    path = base / "app-arm64-v8a-release.apk"
    if not path.is_file():
        path = APP_DIR / "build" / "app" / "outputs" / "apk" / "release" / "app-arm64-v8a-release.apk"
    if not path.is_file():
        raise SystemExit("APK arm64-v8a не найден после сборки (ожидался --split-per-abi)")
    dest = base / f"heyearth-{version}.apk"
    if dest != path:
        dest.write_bytes(path.read_bytes())
    return dest


def build_ios(version: str) -> Path:
    """Сборка IPA для iOS. Работает только на macOS с Xcode."""
    out, err, code = run(["flutter", "build", "ipa", "--release"], cwd=APP_DIR, env=os.environ.copy())
    if code != 0:
        if "darwin" not in sys.platform and "mac" in (err + out).lower():
            raise SystemExit("Сборка iOS возможна только на macOS с Xcode. Запустите скрипт на Mac.")
        raise SystemExit(f"flutter build ipa failed: {err or out}")
    ipa_dir = APP_DIR / "build" / "ios" / "ipa"
    if not ipa_dir.is_dir():
        raise SystemExit("Каталог build/ios/ipa не найден после сборки.")
    ipa_files = list(ipa_dir.glob("*.ipa"))
    if not ipa_files:
        raise SystemExit("IPA-файл не найден в build/ios/ipa/")
    src = ipa_files[0]
    dest = ipa_dir / f"heyearth-{version}.ipa"
    if dest != src:
        dest.write_bytes(src.read_bytes())
    return dest


def build_windows(version: str) -> Path:
    out, err, code = run(["flutter", "build", "windows", "--release"], cwd=APP_DIR)
    if code != 0:
        raise SystemExit(f"flutter build windows failed: {err or out}")
    release_dir = APP_DIR / "build" / "windows" / "x64" / "runner" / "Release"
    if not release_dir.is_dir():
        raise SystemExit("Каталог Release не найден после сборки Windows")
    import zipfile
    zip_path = APP_DIR / "build" / "windows" / f"heyearth-{version}-windows.zip"
    with zipfile.ZipFile(zip_path, "w", zipfile.ZIP_DEFLated) as zf:
        for f in release_dir.rglob("*"):
            if f.is_file():
                zf.write(f, f.relative_to(release_dir))
    return zip_path


def main() -> None:
    parser = argparse.ArgumentParser(description="Выкладка релиза приложения на сайт")
    parser.add_argument("--android", action="store_true", help="Собрать и выложить APK (Android)")
    parser.add_argument("--windows", action="store_true", help="Собрать и выложить Windows (zip)")
    parser.add_argument("--ios", action="store_true", help="Собрать и выложить IPA (iOS; только на macOS с Xcode)")
    parser.add_argument("-v", "--version", metavar="VER", help="Версия (X.Y.Z); должна совпадать с pubspec.yaml")
    args = parser.parse_args()

    if not args.android and not args.windows and not args.ios:
        parser.error("Укажите хотя бы один флаг: --android, --windows и/или --ios")

    env = load_dotenv()
    host = (env.get("DEPLOY_HOST") or "").strip()
    user = (env.get("DEPLOY_USER") or "root").strip()
    password = (env.get("DEPLOY_SSH_PASSWORD") or "").strip() or None
    site_domain = (env.get("SITE_DOMAIN") or "heyearth.ru").strip()
    base_url = f"https://{site_domain}"

    if not host:
        raise SystemExit("Задайте DEPLOY_HOST в .env")

    pubspec_version = get_version_from_pubspec()
    version = (args.version or pubspec_version).strip()
    if args.version and version != pubspec_version:
        raise SystemExit(
            f"Версия -v {version} не совпадает с app/pubspec.yaml (version: {pubspec_version}). "
            "Измените pubspec.yaml или уберите -v."
        )

    releases = load_releases_json()
    platforms = []
    if args.android:
        platforms.append("android")
        if version in releases["android"]:
            raise SystemExit(f"Версия {version} уже выложена для Android (см. app/releases/). Увеличьте версию в pubspec.yaml.")
    if args.windows:
        platforms.append("windows")
        if version in releases["windows"]:
            raise SystemExit(f"Версия {version} уже выложена для Windows (см. app/releases/). Увеличьте версию в pubspec.yaml.")
    if args.ios:
        platforms.append("ios")
        if version in releases["ios"]:
            raise SystemExit(f"Версия {version} уже выложена для iOS (см. app/releases/). Увеличьте версию в pubspec.yaml.")

    built: list[tuple[str, Path, str]] = []  # (platform, local_path, remote_filename)

    for platform in platforms:
        if platform == "android":
            local_path = build_android(version)
            remote_name = f"heyearth-{version}.apk"
        elif platform == "windows":
            local_path = build_windows(version)
            remote_name = f"heyearth-{version}-windows.zip"
        else:  # ios
            local_path = build_ios(version)
            remote_name = f"heyearth-{version}.ipa"
        built.append((platform, local_path, remote_name))
        print(f"  [OK] Сборка {platform}: {local_path.name}")

    run_ssh(host, user, password, f"mkdir -p {REMOTE_RELEASES_DIR}")
    for platform, local_path, remote_name in built:
        remote_full = f"{REMOTE_RELEASES_DIR}/{remote_name}"
        out, err, code = run_scp(host, user, password, local_path, remote_full)
        if code != 0:
            raise SystemExit(f"Ошибка загрузки {remote_name}: {err or out}")
        print(f"  [OK] Загружено на сервер: {remote_name}")

    # Обновить app-update.env на сервере: сохраняем старые APP_UPDATE_* для платформ, которые не выкладываем сейчас
    out, err, code = run_ssh(host, user, password, f"cat {REMOTE_APP_UPDATE_ENV} 2>/dev/null || true")
    lines: list[str] = []
    kept_app_update: dict[str, str] = {}
    if code == 0 and out.strip():
        for line in out.strip().split("\n"):
            if "=" in line and not line.strip().startswith("#"):
                key = line.split("=", 1)[0].strip()
                if key.startswith("APP_UPDATE_"):
                    val = line.split("=", 1)[1].strip()
                    kept_app_update[key] = val
                    continue
            lines.append(line.rstrip())
    env_lines = [f"APP_UPDATE_VERSION={version}"]
    for platform, _, remote_name in built:
        url = f"{base_url}/releases/{remote_name}"
        if platform == "android":
            env_lines.append(f"APP_UPDATE_URL_ANDROID={url}")
        elif platform == "windows":
            env_lines.append(f"APP_UPDATE_URL_WINDOWS={url}")
        elif platform == "ios":
            env_lines.append(f"APP_UPDATE_URL_IOS={url}")
    overwritten = {"APP_UPDATE_VERSION", *(f"APP_UPDATE_URL_{p.upper()}" for p in platforms)}
    for key, val in kept_app_update.items():
        if key not in overwritten:
            env_lines.append(f"{key}={val}")
    content = "\n".join(lines + ["", "# Обновлено release.py"] + env_lines) + "\n"
    tmp_env = PROJECT_ROOT / ".release-app-update.env"
    try:
        tmp_env.write_text(content, encoding="utf-8")
        out2, err2, code2 = run_scp(host, user, password, tmp_env, REMOTE_APP_UPDATE_ENV)
        if code2 != 0:
            raise SystemExit(f"Не удалось записать app-update.env на сервер: {err2 or out2}")
    finally:
        if tmp_env.exists():
            tmp_env.unlink()
    run_ssh(host, user, password, "systemctl restart heyearth-api 2>/dev/null || true")
    print("  [OK] Обновлён app-update.env на сервере, heyearth-api перезапущен.")

    for platform in platforms:
        releases[platform] = sorted(releases[platform] + [version])
        write_release_note(platform, version)
    save_releases_json(releases)
    print(f"  [OK] Записано в app/releases/ (releases.json + заметки по версиям).")

    print(f"\nГотово. Версия {version} выложена для: {', '.join(platforms)}.")
    if "android" in platforms:
        print(f"  Android: {base_url}/releases/heyearth-{version}.apk")
    if "windows" in platforms:
        print(f"  Windows: {base_url}/releases/heyearth-{version}-windows.zip")
    if "ios" in platforms:
        print(f"  iOS: {base_url}/releases/heyearth-{version}.ipa")


if __name__ == "__main__":
    main()
