#!/usr/bin/env python3
"""
Локальный helper для сборки release-артефактов Flutter-приложения.

Скрипт больше не меняет env на сервере и не обновляет main API.
После сборки файлы нужно загрузить через admin_web в раздел Releases.
"""

import argparse
import re
import subprocess
import sys
import zipfile
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parent.parent
PUBSPEC = PROJECT_ROOT / "pubspec.yaml"
ARTIFACTS_DIR = PROJECT_ROOT / "releases" / "artifacts"


def run(cmd: list[str], cwd: Path | None = None) -> tuple[str, str, int]:
    try:
        result = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            cwd=cwd or PROJECT_ROOT,
            timeout=1800,
        )
        return result.stdout or "", result.stderr or "", result.returncode
    except subprocess.TimeoutExpired:
        return "", "timeout", -1
    except FileNotFoundError:
        return "", f"command not found: {cmd[0]}", -1


def get_version_from_pubspec() -> str:
    text = PUBSPEC.read_text(encoding="utf-8")
    match = re.search(r"version:\s*([\d.]+)", text)
    if not match:
        raise SystemExit("Не найден version в flutter-app/pubspec.yaml")
    return match.group(1).strip()


def ensure_flutter_ready() -> None:
    out, err, code = run(["flutter", "--version"])
    if code != 0:
        raise SystemExit(f"Flutter не найден: {err or out}")


def build_android(version: str) -> Path:
    out, err, code = run(
        ["flutter", "build", "apk", "--release", "--split-per-abi", "--no-tree-shake-icons"]
    )
    if code != 0:
        raise SystemExit(f"flutter build apk failed: {err or out}")

    apk = PROJECT_ROOT / "build" / "app" / "outputs" / "flutter-apk" / "app-arm64-v8a-release.apk"
    if not apk.is_file():
        raise SystemExit("APK arm64-v8a не найден после сборки")

    ARTIFACTS_DIR.mkdir(parents=True, exist_ok=True)
    dest = ARTIFACTS_DIR / f"heyearth-{version}.apk"
    dest.write_bytes(apk.read_bytes())
    return dest


def build_windows(version: str) -> Path:
    out, err, code = run(["flutter", "build", "windows", "--release"])
    if code != 0:
        raise SystemExit(f"flutter build windows failed: {err or out}")

    release_dir = PROJECT_ROOT / "build" / "windows" / "x64" / "runner" / "Release"
    if not release_dir.is_dir():
        raise SystemExit("Каталог Release не найден после сборки Windows")

    ARTIFACTS_DIR.mkdir(parents=True, exist_ok=True)
    zip_path = ARTIFACTS_DIR / f"heyearth-{version}-windows.zip"
    with zipfile.ZipFile(zip_path, "w", zipfile.ZIP_DEFLATED) as archive:
        for file in release_dir.rglob("*"):
            if file.is_file():
                archive.write(file, file.relative_to(release_dir))
    return zip_path


def build_ios(version: str) -> Path:
    out, err, code = run(["flutter", "build", "ipa", "--release"])
    if code != 0:
        if "darwin" not in sys.platform:
            raise SystemExit("Сборка iOS возможна только на macOS с Xcode")
        raise SystemExit(f"flutter build ipa failed: {err or out}")

    ipa_dir = PROJECT_ROOT / "build" / "ios" / "ipa"
    ipa_files = list(ipa_dir.glob("*.ipa"))
    if not ipa_files:
        raise SystemExit("IPA-файл не найден в build/ios/ipa")

    ARTIFACTS_DIR.mkdir(parents=True, exist_ok=True)
    dest = ARTIFACTS_DIR / f"heyearth-{version}.ipa"
    dest.write_bytes(ipa_files[0].read_bytes())
    return dest


def main() -> None:
    parser = argparse.ArgumentParser(description="Локальная сборка release-артефактов HeYearth")
    parser.add_argument("--android", action="store_true", help="Собрать Android APK")
    parser.add_argument("--windows", action="store_true", help="Собрать Windows zip")
    parser.add_argument("--ios", action="store_true", help="Собрать iOS IPA")
    parser.add_argument("-v", "--version", metavar="VER", help="Версия X.Y.Z; должна совпадать с pubspec.yaml")
    args = parser.parse_args()

    if not args.android and not args.windows and not args.ios:
        parser.error("Укажите хотя бы один флаг: --android, --windows и/или --ios")

    ensure_flutter_ready()
    pubspec_version = get_version_from_pubspec()
    version = (args.version or pubspec_version).strip()
    if args.version and version != pubspec_version:
        raise SystemExit(
            f"Версия -v {version} не совпадает с pubspec.yaml (version: {pubspec_version})."
        )

    built: list[tuple[str, Path]] = []
    if args.android:
        built.append(("android", build_android(version)))
    if args.windows:
        built.append(("windows", build_windows(version)))
    if args.ios:
        built.append(("ios", build_ios(version)))

    print(f"\nГотово. Локально собраны артефакты версии {version}:")
    for platform, artifact in built:
        print(f"  {platform}: {artifact}")
    print("\nСледующий шаг: загрузить полученные файлы через admin_web -> Releases.")


if __name__ == "__main__":
    main()
