#!/usr/bin/env bash
# ao-review-remote.sh — выполнить команду внутри clihost-контейнера (Dokku) для ревью-пайплайна.
# Использование: ao-review-remote.sh '<shell command>'
# Контейнер: dokku@78.47.183.125, приложение clihost-axisrow, процесс web.
# Транспорт: base64 → echo … | base64 -d | bash (stdin через dokku enter не проходит,
# а base64 свободен от кавычек — безопасно для вложенного квотинга).
# Правило пайплайна: только чтение. Никаких записей, push, install внутри контейнера.
set -euo pipefail
cmd_b64=$(printf '%s' "$1" | base64)
exec ssh -o ConnectTimeout=10 dokku@78.47.183.125 "dokku enter clihost-axisrow web bash -c \"echo $cmd_b64 | base64 -d | bash\""
