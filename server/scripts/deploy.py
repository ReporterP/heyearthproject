# -*- coding: utf-8 -*-
# Запуск Go-деплоя. Требуется Go. Из корня проекта:
#   python scripts/deploy.py --fresh | --update | --restart | --site
# или: cd backend && go run ./cmd/deploy --fresh
import os
import shutil
import subprocess
import sys

# Корень проекта (родитель папки scripts)
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
args = [a for a in sys.argv[1:] if a in ("--fresh", "--update", "--restart", "--site")]
if not args:
    print("Укажи один из флагов: --fresh, --update, --restart, --site")
    sys.exit(1)

go_cmd = shutil.which("go")
if not go_cmd:
    print("Ошибка: Go не найден. Установи Go (https://go.dev/dl/) и добавь в PATH.")
    sys.exit(1)

backend_dir = os.path.join(ROOT, "backend")
rc = subprocess.call([go_cmd, "run", "./cmd/deploy", *args], cwd=backend_dir)
sys.exit(rc)
