#!/bin/bash
# rebuild-ao.sh — пересобрать и установить локальную самосборку agent-orchestrator.
# Цель: избавить пользователя от ручного цикла пересборки. Запускается агентом
# без подтверждения — это рутинный цикл самосборки из форка (см. память
# ao-local-build-update-procedure).
#
# Что делает: npm run package → ditto в /Applications → codesign adhoc →
# сборка CLI в ~/.local/bin/ao → прямой запуск бинаря (обход provenance/Gatekeeper)
# → poll ao status → готово.
#
# Предполагает: запуск из корня репо agent-orchestrator, на нужной ветке.
# Старые AO-процессы убивает сам (pkill). Данные durable в ~/.ao/data переживают.

set -e
# NB: намеренно без `set -u` — macOS /bin/bash 3.2 глючит с -u после subshell-команд
# (REPO_ROOT=$(...)), выдавая ложное «unbound variable». fail-fast через set -e достаточно.

# --- найти корень репо надёжно (по .git), НЕ доверяя cwd ---
# bash-сессия агента часто сидит в backend/, и `cd frontend` ломается. Идём от
# расположения скрипта вверх до каталога с .git, потом проверяем frontend/+backend.
SCRIPT_DIR="$(cd "$(dirname "$0")" 2>/dev/null && pwd)"
REPO_ROOT=""
for cand in "$SCRIPT_DIR/.." "$SCRIPT_DIR" "$PWD" "$PWD/.."; do
  abs="$(cd "$cand" 2>/dev/null && pwd)" || continue
  if [ -d "$abs/.git" ] && [ -d "$abs/frontend" ] && [ -d "$abs/backend" ]; then
    REPO_ROOT="$abs"; break
  fi
done
if [ -z "$REPO_ROOT" ]; then
  echo "!! Не нашёл корень репо (нужен каталог с .git + frontend/ + backend/)." >&2
  echo "   Запускай из корня agent-orchestrator или клади скрипт в <repo>/bin/." >&2
  exit 1
fi
APP_SRC="$REPO_ROOT/frontend/out/Agent Orchestrator-darwin-arm64/Agent Orchestrator.app"
APP_DST="/Applications/Agent Orchestrator.app"
AO_BIN="$HOME/.local/bin/ao"
PORT=3001

# Необязательный sanity-needle: строка, которая должна быть в свежем демоне
# (что ditto принёс именно нашу сборку, не устаревшую). Передавай через env:
#   SANITY=profileSource ~/bin/rebuild-ao.sh
# или позиционным аргументом. Без аргумента — дефолтный needle (текущая активная
# фича форка). ВАЖНО: needle должно быть реальным символом/строкой в скомпилированном
# бинаре (проверяется через `strings daemon/ao | grep`); внутренние имена полей вроде
# `GlobalWorkerPromptOverride` (тип systemPromptConfig) компилятор может не оставить
# как строку — используйте json-tag или экспортируемое имя (например WorkerPromptOverride,
# workerPromptOverride, blockedCandidate).
DEFAULT_SANITY_NEEDLE="UserConfigController"
SANITY_NEEDLE="${SANITY-}"
if [ -z "$SANITY_NEEDLE" ]; then
  SANITY_NEEDLE="${1-$DEFAULT_SANITY_NEEDLE}"
fi

echo "==> репо: $REPO_ROOT (ветка: $(git -C "$REPO_ROOT" rev-parse --abbrev-ref HEAD 2>/dev/null || echo '?'))"
if [ -n "$SANITY_NEEDLE" ]; then
  echo "==> sanity-check: в демоне должно быть «$SANITY_NEEDLE»"
fi

# Версия-стамп на время сборки, а не коммит: раньше frontend/package.json +
# package-lock.json стампились коммитом в дельту форка (0.10.<дата сборки>) —
# это два коммита, которые конфликтуют почти на каждом синке (upstream тоже
# правит package.json/lock часто), а после мержа/дропа их надо чистить
# вручную. Стамп только на время npm run package, откат — всегда, даже при
# ошибке (trap EXIT), чтобы working tree после пересборки оставался чистым.
STAMP_VERSION="0.10.$(date +%y%m%d)"
restore_package_json() {
  git -C "$REPO_ROOT" checkout -- frontend/package.json frontend/package-lock.json 2>/dev/null || true
}
trap restore_package_json EXIT

# 0. Убить старые AO-процессы (GUI + демон), иначе ditto/запуск нестабильны.
echo "==> останавливаю старые AO-процессы..."
OLD_DAEMON_PID="$(pgrep -f 'daemon/ao daemon' | head -1 || true)"
echo "   старый демон PID: ${OLD_DAEMON_PID:-<нет>}"
osascript -e 'quit app "Agent Orchestrator"' 2>/dev/null || true
sleep 1
pkill -f "MacOS/agent-orchestrator" 2>/dev/null || true
pkill -f "daemon/ao daemon" 2>/dev/null || true
sleep 1
# ao stop на всякий случай (если демон ещё жив на порту)
"$AO_BIN" stop 2>/dev/null || true

# 1. Собрать .app + демон (prepackage сам соберёт Go-демона в бандл).
echo "==> стамп версии (на время сборки): $STAMP_VERSION"
(cd "$REPO_ROOT/frontend" && npm version --no-git-tag-version "$STAMP_VERSION" >/dev/null)
echo "==> npm run package (сборка .app + Go-демона)..."
(cd "$REPO_ROOT/frontend" && npm run package)

# 2. ditto в /Applications (без rm -rf, без sudo — пользователь владелец).
echo "==> ditto → /Applications..."
ditto "$APP_SRC" "$APP_DST"

# 3. Переподписать (ditto ломает подпись на Sequoia → open молча не запускает).
# Стабильная self-signed identity (AO-Local-CodeSign в login.keychain), а не
# ad-hoc (--sign -): ad-hoc пересчитывает свой designated requirement на
# каждой сборке (weight = хэш кода), так что Keychain ACL для Electron
# safeStorage (cloud offering, #4434) видел каждую пересборку как новое
# приложение и просил пароль связки ключей заново каждый раз. Один и тот же
# сертификат-issuer даёт стабильный "identifier + certificate leaf" — Keychain
# узнаёт приложение между сборками. Если сертификат отсутствует в этой
# среде (свежая машина), падаем обратно на ad-hoc: подписанное приложение
# всё ещё запускается, только Keychain ACL не переживёт следующую пересборку.
CODESIGN_IDENTITY="AO-Local-CodeSign"
if ! security find-identity -v -p codesigning 2>/dev/null | grep -q "$CODESIGN_IDENTITY"; then
  echo "   !! identity '$CODESIGN_IDENTITY' не найдена в keychain — подписываю ad-hoc." >&2
  echo "      (Keychain ACL для safeStorage не переживёт следующую пересборку.)" >&2
  CODESIGN_IDENTITY="-"
fi
echo "==> codesign --force --deep --sign $CODESIGN_IDENTITY (fix подписи после ditto)..."
codesign --force --deep --sign "$CODESIGN_IDENTITY" "$APP_DST" 2>/dev/null || \
  echo "   (codesign warning — проигнорировано)"

# 4. CLI-бинарь (имя строго ao, иначе demon варнит про PATH pin). build:daemon
#    (внутри npm run package, шаг 1) уже собрал ./cmd/ao в бандл — копируем
#    его вместо повторной компиляции того же пакета.
BUNDLED_DAEMON="$APP_DST/Contents/Resources/daemon/ao"
echo "==> CLI → $AO_BIN (копия из бандла, без повторной сборки)..."
if [ -f "$BUNDLED_DAEMON" ]; then
  cp "$BUNDLED_DAEMON" "$AO_BIN"
  chmod +x "$AO_BIN"
else
  echo "   !! $BUNDLED_DAEMON не найден — собираю напрямую." >&2
  go -C "$REPO_ROOT/backend" build -o "$AO_BIN" ./cmd/ao
fi

# 5. Прямой запуск бинаря — 100% обход provenance/Gatekeeper/LaunchServices.
echo "==> запуск GUI (прямой exec бинаря, обход блок-листов)..."
"$APP_DST/Contents/MacOS/agent-orchestrator" >/dev/null 2>&1 &

# 6. Post-check: ждём демон ready +全套 проверок (PID сменился, порт тот же, healthz).
echo "==> жду демон ready (до 30с)..."
READY=0
for i in $(seq 1 30); do
  if curl -sf --max-time 2 "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1; then
    READY=1; break
  fi
  sleep 1
done

echo "==> ===== POST-CHECK (rebuild doctor) ====="
FAIL=0

# 6a. healthz green (источник правды, не ao status с устаревшим runfile)
if [ "$READY" = "1" ]; then
  echo "   [OK]   healthz на :$PORT отвечает"
else
  echo "   [FAIL] демон не ответил на healthz за 30с"; FAIL=1
fi

# 6b. порт :3001 держит демон (НЕ эфемерный — значит старый демон убит)
LISTENER_PID="$(lsof -nP -iTCP:$PORT -sTCP:LISTEN 2>/dev/null | awk 'NR>1{print $2}' | head -1)"
if [ -n "$LISTENER_PID" ]; then
  echo "   [OK]   порт :$PORT слушает PID $LISTENER_PID"
else
  echo "   [FAIL] порт :$PORT никем не слушается (демон ушёл на эфемерный? старый не убит?)"; FAIL=1
fi

# 6c. PID демона сменился (новый демон = свежая сборка в рантайме)
NEW_DAEMON_PID="$(pgrep -f 'daemon/ao daemon' | head -1 || true)"
if [ -n "$NEW_DAEMON_PID" ] && [ "$NEW_DAEMON_PID" != "$OLD_DAEMON_PID" ]; then
  echo "   [OK]   PID демона сменился: ${OLD_DAEMON_PID:-<нет>} → $NEW_DAEMON_PID"
elif [ -n "$NEW_DAEMON_PID" ]; then
  echo "   [WARN] PID демона НЕ сменился ($NEW_DAEMON_PID) — возможно, старый выжил"; FAIL=1
else
  echo "   [FAIL] демон-процесс не найден"; FAIL=1
fi

# 6d. healthz отдаёт тот же PID, что процесс (runfile/healthz синхрон)
HZ_PID="$(curl -sf --max-time 2 "http://127.0.0.1:$PORT/healthz" 2>/dev/null | grep -o '"pid":[0-9]*' | grep -o '[0-9]*')"
if [ -n "$HZ_PID" ] && [ "$HZ_PID" = "$NEW_DAEMON_PID" ]; then
  echo "   [OK]   healthz PID ($HZ_PID) = процессу демона"
else
  echo "   [WARN] healthz PID '$HZ_PID' ≠ демон-процессу '$NEW_DAEMON_PID' (гонка runfile?)"
fi

# 6e. sanity-needle в бинаре (ditto принёс нашу сборку)
if [ -n "$SANITY_NEEDLE" ]; then
  if strings "$APP_DST/Contents/Resources/daemon/ao" 2>/dev/null | grep -q "$SANITY_NEEDLE"; then
    echo "   [OK]   sanity: «$SANITY_NEEDLE» в бинаре — наша сборка"
  else
    echo "   [FAIL] sanity: «$SANITY_NEEDLE» НЕ найден — ditto принёс устаревшую сборку"; FAIL=1
  fi
fi

echo "==> ========================================"
"$AO_BIN" status 2>/dev/null || true
if [ "$FAIL" = "1" ]; then
  echo "!! POST-CHECK нашёл проблемы — пересборка могла не примениться. См. [FAIL]/[WARN] выше."
  exit 1
fi
echo "==> готово. (PID $NEW_DAEMON_PID, порт $PORT)"
exit 0
