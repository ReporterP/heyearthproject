#!/usr/bin/env python3
"""
Диагностика и исправление доступности HTTP/HTTPS на сервере по SSH.
Запуск локально: python3 server_http_diagnose_and_fix.py [--fix]
Подключается к серверу по SSH (хост/пользователь/пароль из .env или --host/--user).
Одно подключение (ControlMaster) на весь запуск — без повторного ввода пароля.
Пароль из .env (DEPLOY_SSH_PASSWORD): нужен sshpass (apt install sshpass).
"""

import argparse
import os
import re
import subprocess
import sys

# Глобальные параметры SSH-сессии (один мастер на весь запуск)
_ssh_control_path: str | None = None
_ssh_host = ""
_ssh_user = ""
_ssh_password: str | None = None
_ssh_master_started = False
_ssh_master_failed = False  # при True используем прямое SSH для каждой команды
_ssh_warned_no_sshpass = False


def load_dotenv(project_root: str) -> dict:
    """Грузит .env из корня проекта. Возвращает dict KEY -> value."""
    env_path = os.path.join(project_root, ".env")
    result = {}
    if not os.path.isfile(env_path):
        return result
    try:
        with open(env_path) as f:
            for line in f:
                line = line.strip()
                if not line or line.startswith("#"):
                    continue
                if "=" in line:
                    k, v = line.split("=", 1)
                    result[k.strip()] = v.strip().strip('"').strip("'")
    except OSError:
        pass
    return result


def _ssh_base_opts(host: str, user: str) -> list[str]:
    """Базовые опции SSH (без ControlPath)."""
    return [
        "ssh",
        "-o", "ConnectTimeout=15",
        "-o", "StrictHostKeyChecking=accept-new",
        "-o", "BatchMode=no",
    ]


def _ensure_ssh_master() -> bool:
    """Запустить мастер-сессию SSH один раз (ControlMaster). Возвращает True при успехе."""
    global _ssh_master_started, _ssh_master_failed, _ssh_warned_no_sshpass
    if _ssh_master_started or not _ssh_control_path:
        return _ssh_master_started
    target = f"{_ssh_user}@{_ssh_host}"
    cmd = _ssh_base_opts(_ssh_host, _ssh_user) + [
        "-o", "ControlMaster=yes",
        "-o", f"ControlPath={_ssh_control_path}",
        "-o", "ControlPersist=120",
        target,
        "true",
    ]
    env = os.environ.copy()
    if _ssh_password:
        env["SSHPASS"] = _ssh_password
        run_cmd = ["sshpass", "-e"] + cmd
    else:
        run_cmd = cmd
    try:
        r = subprocess.run(run_cmd, capture_output=True, text=True, timeout=25, env=env)
        _ssh_master_started = r.returncode == 0
        if not _ssh_master_started:
            _ssh_master_failed = True
    except (subprocess.TimeoutExpired, FileNotFoundError) as e:
        _ssh_master_started = False
        _ssh_master_failed = True
        if isinstance(e, FileNotFoundError) and _ssh_password and not _ssh_warned_no_sshpass:
            _ssh_warned_no_sshpass = True
            print("  [WARN] Для пароля из .env нужен sshpass: apt install sshpass", file=sys.stderr)
    return _ssh_master_started


def _close_ssh_master() -> None:
    """Закрыть мастер-сессию SSH."""
    global _ssh_master_started
    if not _ssh_control_path or not _ssh_master_started:
        return
    target = f"{_ssh_user}@{_ssh_host}"
    cmd = _ssh_base_opts(_ssh_host, _ssh_user) + [
        "-o", "ControlMaster=no",
        "-o", f"ControlPath={_ssh_control_path}",
        "-O", "exit",
        target,
    ]
    try:
        subprocess.run(cmd, capture_output=True, timeout=5)
    except Exception:
        pass
    _ssh_master_started = False


def run_ssh(host: str, user: str, remote_cmd: str, timeout: int = 20) -> tuple[str, str, int]:
    """Выполнить команду на сервере по SSH. ControlMaster или прямое SSH (при неудаче мастера)."""
    global _ssh_control_path, _ssh_host, _ssh_user, _ssh_master_failed, _ssh_warned_no_sshpass
    use_multiplex = (
        _ssh_control_path
        and (_ssh_host, _ssh_user) == (host, user)
        and not _ssh_master_failed
    )
    if use_multiplex and _ensure_ssh_master():
        target = f"{user}@{host}"
        cmd = _ssh_base_opts(host, user) + [
            "-o", "ControlMaster=no",
            "-o", f"ControlPath={_ssh_control_path}",
            target,
            remote_cmd,
        ]
        env = os.environ.copy()
        try:
            r = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout, env=env)
            return (r.stdout or "", r.stderr or "", r.returncode)
        except subprocess.TimeoutExpired:
            return ("", "timeout", -2)
        except FileNotFoundError:
            return ("", "ssh not found", -1)
    # Прямое SSH: при неудаче мастера или без мультиплексирования (пароль из .env через sshpass)
    target = f"{user}@{host}"
    cmd = _ssh_base_opts(host, user) + [target, remote_cmd]
    env = os.environ.copy()
    if _ssh_password:
        env["SSHPASS"] = _ssh_password
        cmd = ["sshpass", "-e"] + cmd
    try:
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout, env=env)
        return (r.stdout or "", r.stderr or "", r.returncode)
    except subprocess.TimeoutExpired:
        return ("", "timeout", -2)
    except FileNotFoundError:
        if _ssh_password and not _ssh_warned_no_sshpass:
            _ssh_warned_no_sshpass = True
            print("  [WARN] Для пароля из .env нужен sshpass: apt install sshpass", file=sys.stderr)
        err = "sshpass не найден (apt install sshpass)" if _ssh_password else "ssh not found"
        return ("", err, -1)


def section(title: str) -> None:
    print(f"\n{'='*60}\n{title}\n{'='*60}")


def ok(msg: str) -> None:
    print(f"  [OK] {msg}")


def fail(msg: str) -> None:
    print(f"  [FAIL] {msg}")


def warn(msg: str) -> None:
    print(f"  [WARN] {msg}")


def info(msg: str) -> None:
    print(f"  [INFO] {msg}")


def diagnose_ss_ports(host: str, user: str) -> dict:
    """Проверка на сервере: кто слушает 80 и 443."""
    section("Порты 80 и 443 (на сервере)")
    out, err, code = run_ssh(host, user, "ss -tlnp 2>/dev/null || netstat -tlnp 2>/dev/null")
    if code != 0 and not out:
        fail(f"Не удалось выполнить ss/netstat по SSH: {err or 'no output'}")
        return {"80": None, "443": None, "80_bind": "", "443_bind": ""}
    result = {"80": None, "443": None, "80_bind": "", "443_bind": ""}
    for line in out.splitlines():
        if ":80 " in line or ".80 " in line:
            result["80"] = line.strip()
            if "0.0.0.0:80" in line or "*:80" in line or "[::]:80" in line:
                result["80_bind"] = "0.0.0.0"
            elif "127.0.0.1:80" in line:
                result["80_bind"] = "127.0.0.1"
        if ":443 " in line or ".443 " in line:
            result["443"] = line.strip()
            if "0.0.0.0:443" in line or "*:443" in line or "[::]:443" in line:
                result["443_bind"] = "0.0.0.0"
            elif "127.0.0.1:443" in line:
                result["443_bind"] = "127.0.0.1"
    if result["80"]:
        if result["80_bind"] == "0.0.0.0":
            ok("Порт 80 слушается снаружи (0.0.0.0/[::])")
        else:
            fail("Порт 80 только на localhost — с интернета недоступен")
    else:
        fail("Порт 80 никто не слушает.")
    if result["443"]:
        if result["443_bind"] == "0.0.0.0":
            ok("Порт 443 слушается снаружи (0.0.0.0/[::])")
        else:
            fail("Порт 443 только на localhost")
    else:
        warn("Порт 443 никто не слушает (HTTPS может быть не настроен).")
    return result


def diagnose_ufw(host: str, user: str) -> dict:
    """Проверка ufw на сервере."""
    section("Фаервол (ufw) на сервере")
    out, err, code = run_ssh(host, user, "sudo ufw status 2>/dev/null")
    full = out + err
    if code != 0 and "command not found" in full.lower():
        info("ufw не установлен на сервере или нет sudo. Проверьте iptables вручную.")
        return {"active": False, "80_allowed": None, "443_allowed": None}
    if code != 0 and "password" in full.lower():
        warn("sudo ufw status запросил пароль. Настройте NOPASSWD для sudo или запускайте под root.")
        return {"active": None, "80_allowed": None, "443_allowed": None}
    active = "active" in full.lower()
    if active:
        ok("ufw активен")
    else:
        warn("ufw не активен.")
    allow_80 = "80" in full and ("ALLOW" in full or "разрешено" in full)
    allow_443 = "443" in full and ("ALLOW" in full or "разрешено" in full)
    if active:
        if allow_80:
            ok("80/tcp разрешён в ufw")
        else:
            fail("80/tcp не разрешён в ufw — при --fix выполним: ufw allow 80/tcp")
        if allow_443:
            ok("443/tcp разрешён в ufw")
        else:
            fail("443/tcp не разрешён в ufw — при --fix выполним: ufw allow 443/tcp")
    return {"active": active, "80_allowed": allow_80, "443_allowed": allow_443}


def diagnose_nginx_config(host: str, user: str) -> list[str]:
    """Проверка конфигов nginx на сервере (listen)."""
    section("Конфиг Nginx (listen) на сервере")
    out, err, code = run_ssh(
        host, user,
        "sudo cat /etc/nginx/sites-enabled/* 2>/dev/null; sudo cat /etc/nginx/nginx.conf 2>/dev/null; sudo cat /etc/nginx/conf.d/*.conf 2>/dev/null"
    )
    text = out + err
    issues = []
    if re.search(r"listen\s+127\.0\.0\.1\s*:\s*80", text):
        issues.append("В конфиге nginx: listen 127.0.0.1:80 — заменить на listen 80; для доступа с интернета.")
    if re.search(r"listen\s+127\.0\.0\.1\s*:\s*443", text):
        issues.append("В конфиге nginx: listen 127.0.0.1:443 — заменить на listen 443 ssl;")
    if "listen 80;" in text or "listen [::]:80" in text:
        ok("Есть listen 80 на все интерфейсы")
    if issues:
        for i in issues:
            fail(i)
    else:
        ok("Критичных listen 127.0.0.1 для 80/443 не найдено.")
    return issues


def diagnose_nginx_running(host: str, user: str) -> bool:
    """Nginx запущен и отвечает на localhost на сервере."""
    section("Nginx: процесс и ответ на сервере")
    out, err, code = run_ssh(host, user, "systemctl is-active nginx 2>/dev/null || true")
    if "active" not in (out or "").strip().lower():
        out2, _, code2 = run_ssh(host, user, "pgrep -x nginx 2>/dev/null || true")
        if code2 != 0 and not out2.strip():
            fail("Nginx не запущен на сервере (systemctl status nginx).")
            return False
    ok("Nginx запущен.")
    out, err, code = run_ssh(host, user, "curl -sS -o /dev/null -w '%{http_code}' --connect-timeout 3 http://127.0.0.1/ 2>/dev/null || echo 000")
    code_str = (out or "").strip() or "000"
    if code_str.isdigit() and code_str != "000":
        ok(f"Локальный HTTP на сервере (127.0.0.1): код {code_str}")
    else:
        warn("Локальный curl http://127.0.0.1 на сервере не удался — проверьте виртуальные хосты nginx.")
    return True


def diagnose_local_curl_on_server(host: str, user: str) -> None:
    """Проверка с самого сервера: curl localhost."""
    section("Проверка с самого сервера (curl localhost)")
    for scheme in ["http", "https"]:
        out, err, code = run_ssh(
            host, user,
            f"curl -sS -o /dev/null -w '%{{http_code}}' --connect-timeout 3 -k {scheme}://127.0.0.1/ 2>/dev/null || echo 000"
        )
        code_str = (out or "").strip() or "000"
        if code_str.isdigit() and code_str != "000":
            ok(f"{scheme}://127.0.0.1/ на сервере — код {code_str}")
        else:
            fail(f"{scheme}://127.0.0.1/ на сервере — недоступен или таймаут")


def apply_fix_ufw(host: str, user: str, state: dict) -> bool:
    """На сервере: ufw allow 80/443, reload."""
    if state.get("80_allowed") and state.get("443_allowed"):
        return True
    out, err, code = run_ssh(
        host, user,
        "sudo ufw allow 80/tcp && sudo ufw allow 443/tcp && sudo ufw reload"
    )
    if code != 0:
        fail(f"Не удалось применить ufw на сервере: {err or out}")
        return False
    ok("На сервере выполнено: ufw allow 80/tcp, 443/tcp, ufw reload")
    return True


def apply_fix_nginx_reload(host: str, user: str) -> bool:
    """На сервере: nginx -t и systemctl reload nginx."""
    out, err, code = run_ssh(host, user, "sudo nginx -t 2>&1")
    if code != 0:
        fail(f"nginx -t на сервере не прошёл: {err or out}")
        return False
    ok("На сервере: nginx -t прошёл.")
    out, err, code = run_ssh(host, user, "sudo systemctl reload nginx 2>&1")
    if code != 0:
        warn(f"systemctl reload nginx на сервере: {err or out}. Выполните вручную по SSH.")
        return False
    ok("На сервере выполнено: systemctl reload nginx")
    return True


def main() -> None:
    global _ssh_control_path, _ssh_host, _ssh_user, _ssh_password
    project_root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    env = load_dotenv(project_root)
    default_host = env.get("DEPLOY_HOST", "").strip()
    default_user = env.get("DEPLOY_USER", "root").strip()
    default_password = env.get("DEPLOY_SSH_PASSWORD", "").strip() or None

    parser = argparse.ArgumentParser(description="Диагностика и исправление HTTP/HTTPS на сервере по SSH")
    parser.add_argument("--host", default=default_host or None, help="Сервер (по умолчанию из .env DEPLOY_HOST)")
    parser.add_argument("--user", default=default_user, help="SSH-пользователь (по умолчанию из .env DEPLOY_USER)")
    parser.add_argument("--fix", action="store_true", help="Применить исправления на сервере (ufw allow 80/443, reload nginx)")
    args = parser.parse_args()

    host = (args.host or "").strip()
    user = (args.user or "root").strip()
    if not host:
        print("Ошибка: укажите --host или задайте DEPLOY_HOST в .env", file=sys.stderr)
        sys.exit(1)

    # Одно SSH-подключение на весь запуск: пароль из .env (DEPLOY_SSH_PASSWORD) или один запрос при первом вызове
    safe = re.sub(r"[^a-zA-Z0-9.-]", "_", host) + "_" + re.sub(r"[^a-zA-Z0-9.-]", "_", user)
    _ssh_control_path = os.path.join(os.environ.get("TMPDIR", "/tmp"), f"heyearth_ssh_{safe}")
    _ssh_host = host
    _ssh_user = user
    _ssh_password = default_password

    section("Диагностика доступности HTTP/HTTPS на сервере (по SSH)")
    info(f"Подключение: {user}@{host}" + (" (пароль из .env)" if _ssh_password else ""))

    try:
        port_state = diagnose_ss_ports(host, user)
        ufw_state = diagnose_ufw(host, user)
        nginx_issues = diagnose_nginx_config(host, user)
        diagnose_nginx_running(host, user)
        diagnose_local_curl_on_server(host, user)

        if args.fix:
            section("Применение исправлений на сервере (--fix)")
            if not ufw_state.get("80_allowed") or not ufw_state.get("443_allowed"):
                apply_fix_ufw(host, user, ufw_state)
            else:
                info("80/443 уже разрешены в ufw, пропуск.")
            apply_fix_nginx_reload(host, user)
            if nginx_issues:
                info("Исправьте listen в конфигах nginx на сервере вручную (см. вывод выше), затем: sudo systemctl reload nginx")
        else:
            if not ufw_state.get("80_allowed") or not ufw_state.get("443_allowed") or nginx_issues:
                info("Запустите с --fix чтобы применить исправления на сервере (ufw + reload nginx).")

        section("Итог")
        print("  Если с сервера (curl localhost) всё ок, а с вашего ПК без VPN — нет,")
        print("  вероятна блокировка у провайдера. Варианты: другой канал, CDN (Cloudflare), доступ по VPN.")
        print()
    finally:
        _close_ssh_master()


if __name__ == "__main__":
    main()
