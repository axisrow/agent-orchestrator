#!/bin/bash
# ao-sync.sh — синхронизировать fork/main с upstream origin/main: fetch → backup-тег →
# merge origin/main в дельту форка → быстрые проверки-ловушки → быстрые гейты.
#
# Модель синка — MERGE, не rebase (переход 2026-09-04): дельта форка живёт как
# один squash-коммит (+ новые форковые коммиты) поверх origin/main, каждый синк
# добавляет merge-коммит. История fork/main не переписывается: конфликты
# разрешаются один раз в merge-коммите и не воспроизводятся в следующих синках,
# force-push не нужен. (Сжатие дельты обратно в 1 коммит — отдельный ручной
# squash-пасс «время от времени», не часть синка.)
#
# Цель: ловить типичные отказы (коллизия номера миграции, забытый `npm install`
# после новой зависимости, рассинхрон regen-артефактов) ЗА СЕКУНДЫ, до того как
# они всплывут через 5 минут `npm run package` или после запуска демона.
#
# НЕ делает: push в fork (обычный fast-forward — решение пользователя),
# пересборку .app (это ~/bin/rebuild-ao.sh), разрешение реальных смысловых
# конфликтов (только останавливается и объясняет где).
#
# Запускать из корня репо agent-orchestrator (или откуда угодно — REPO_ROOT
# ищется по .git+frontend+backend, см. rebuild-ao.sh).

set -e
# NB: без `set -u` — та же причина, что в rebuild-ao.sh (macOS /bin/bash 3.2).

SCRIPT_DIR="$(cd "$(dirname "$0")" 2>/dev/null && pwd)"
REPO_ROOT=""
for cand in "$SCRIPT_DIR/.." "$SCRIPT_DIR" "$PWD" "$PWD/.."; do
  abs="$(cd "$cand" 2>/dev/null && pwd)" || continue
  # -e, а не -d: в git worktree `.git` — файл со ссылкой на общий gitdir.
  if [ -e "$abs/.git" ] && [ -d "$abs/frontend" ] && [ -d "$abs/backend" ]; then
    REPO_ROOT="$abs"; break
  fi
done
if [ -z "$REPO_ROOT" ]; then
  echo "!! Не нашёл корень репо (нужен каталог с .git + frontend/ + backend/)." >&2
  exit 1
fi
cd "$REPO_ROOT"

echo "==> репо: $REPO_ROOT (ветка: $(git rev-parse --abbrev-ref HEAD))"

# 1. Guard: рабочее дерево обязано быть чистым — иначе merge может всё смешать.
if [ -n "$(git status --porcelain)" ]; then
  echo "!! Рабочее дерево не чистое. Закоммить/застэшь изменения и запусти снова." >&2
  git status --short >&2
  exit 1
fi

# 2. fetch + backup-тег на текущий main (страховка перед merge).
#
# ВАЖНО: тег ставится ОДИН РАЗ на синк. Скрипт задуман перезапускаемым (после
# ручного разрешения конфликта merge), а иначе он на каждом запуске ставил бы
# тег на УЖЕ СМЁРЖЕННЫЙ HEAD, затирая единственную точку отката. Реальное
# состояние держим в маркер-файле, который живёт ровно на время незавершённого
# синка.
# git rev-parse --git-dir, а не "$REPO_ROOT/.git": в worktree последний —
# файл, и запись в него затёрла бы ссылку на общий gitdir.
BACKUP_MARKER="$(git rev-parse --git-dir)/ao-sync-backup-ref"
BACKUP_TAG="$(cat "$BACKUP_MARKER" 2>/dev/null || true)"
if [ -n "$BACKUP_TAG" ] && git rev-parse -q --verify "refs/tags/$BACKUP_TAG" >/dev/null; then
  echo "==> продолжаю начатый синк; backup-тег остаётся прежним: $BACKUP_TAG"
  echo "    ($(git rev-parse --short "$BACKUP_TAG") — состояние main ДО этого синка)"
  echo "==> резюме начатого синка — база уже зафиксирована тегом, fetch пропускаю."
else
  echo "==> git fetch origin..."
  git fetch origin
  [ -n "$BACKUP_TAG" ] && echo "!! маркер указывал на несуществующий тег $BACKUP_TAG — создаю новый." >&2 || true
  BACKUP_TAG="backup/pre-sync-$(date +%Y%m%d-%H%M)"
  # Без -f: если тег уже есть (два запуска в одну минуту), это тот же синк,
  # и перезаписывать его нельзя — иначе точка отката уедет на смёрженный HEAD.
  git tag "$BACKUP_TAG" main 2>/dev/null || true
  printf '%s\n' "$BACKUP_TAG" > "$BACKUP_MARKER"
  echo "==> backup-тег: $BACKUP_TAG (на случай отката: git reset --hard $BACKUP_TAG)"
fi

if git rev-parse --verify -q origin/main >/dev/null && [ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ]; then
  echo "==> уже на уровне origin/main, синкать нечего."
  rm -f "$BACKUP_MARKER"
  exit 0
fi

# 3. merge origin/main в дельту форка.
#    Миграционные конфликты (дубль номера дельты: наш коммит и upstream меняли
#    одну строку shippedMigrations в migrate_burned_versions_test.go) авто-разрешаем
#    фиксером; не-миграционные — прежнее ручное поведение.
FIXER="$HOME/bin/ao-fix-migration-collision.sh"
export GIT_EDITOR=true   # чтобы merge-коммит не открывал редактор

# Признак «миграционный конфликт»: среди конфликтующих файлов есть migrations/
# или migrate_. Тогда фиксер перенумерует дельту на свободный номер и добавит
# запись в ledger, после чего merge завершаем.
migration_conflict() {
  git diff --name-only --diff-filter=U | grep -Eq 'backend/internal/storage/sqlite/(migrations/|migrate_)'
}

echo "==> git merge origin/main..."
if git merge --no-edit -m "chore(sync): merge origin/main $(date +%Y-%m-%d)" origin/main; then
  :
else
  if migration_conflict; then
    echo "==> миграционный конфликт — автоперенумерация..."
    if ! "$FIXER" --no-test; then
      echo "!! автопочинка не справилась — дальше руками (см. сообщение фиксера)." >&2
      exit 1
    fi
    git add -A
    if ! git merge --continue; then
      cat >&2 <<'EOF'

!! git merge --continue не прошёл после автоперенумерации — вероятно, остались
   неразрешённые не-миграционные файлы. Разреши руками, затем:
     git add <файлы> && git merge --continue
   и запусти этот скрипт снова — он продолжит с шага проверки миграций.
   Откат: git merge --abort
EOF
      exit 1
    fi
    echo "==> merge завершён после автоперенумерации миграций."
  else
    cat >&2 <<'EOF'

!! Merge остановился на НЕ-миграционном конфликте. Это ожидаемо для дельты
   форка — разреши руками (git rerere уже включён и часть повторяющихся
   конфликтов решит сам), затем:
     git add <файлы> && git merge --continue
   и запусти этот скрипт снова — он продолжит с шага проверки миграций.
   Откат: git merge --abort
EOF
    exit 1
  fi
fi

# 3b. Settle: привести миграции дельты к политике нумерации (канонические 9000+
#     для fork-local, byte-identical дубли апстрима — удалить, ledger пересобрать
#     детерминированно из origin/main + fork-блока). Идемпотентно: на чистом
#     дереве ничего не меняет. Дрейф остаётся незакоммиченным — закоммить
#     отдельным chore-коммитом, как regen-артефакты ниже.
echo "==> settle миграций (канонические номера, пересборка ledger)..."
if ! "$FIXER" --settle --no-test; then
  echo "!! settle не справился — миграции дальше руками (см. сообщение фиксера)." >&2
  exit 1
fi
if git diff --name-only -- backend/internal/storage/sqlite/ | grep -q .; then
  echo "   !! settle изменил миграции/ledger — закоммить эти изменения отдельным chore-коммитом."
  git status --short -- backend/internal/storage/sqlite/
else
  echo "   OK, дрейфа нет"
fi

# 3c. Гейт на маркеры конфликта. Ловит класс «merge завершился УСПЕШНО, но
#     закоммитил мусор» (оба инцидента 2026-09-02: вложенные <<<<<<< в
#     migrate_activity_zone_test.go и в ledger уехали в main при зелёном синке).
#     Ограничение по расширениям исключает false-positive на setext-подчёркиваниях
#     (======= под заголовками) в markdown-доках. Дёшево: один git grep.
#     NB: git grep_exit 1 = «совпадений нет» — это ЗЕЛЁНЫЙ случай, поэтому
#     код возврата не гейтится, гейтится только непустой вывод.
check_conflict_markers() {
  git grep -nI -E '^(<{7} |={7}$|>{7} )' -- '*.go' '*.ts' '*.tsx' '*.json' '*.yaml' '*.yml' '*.sql' 2>/dev/null || true
}
echo "==> проверка маркеров конфликта в дереве..."
MARKERS="$(check_conflict_markers)"
if [ -n "$MARKERS" ]; then
  echo "!! В дереве остались маркеры конфликта (merge закоммитил мусор):" >&2
  printf '%s\n' "$MARKERS" >&2
  echo "   Почини вручную (выбрать сторону, убрать маркеры) и закоммить." >&2
  exit 1
fi
echo "   OK, маркеров нет"

# 3d. Проактивная проверка: не смержил ли апстрим уже одну из наших fork-local
#     миграций под своим номером (byte-identical содержимое под другим именем).
#     В норме МОЛЧИТ — этот кейс теперь автоматом разбирает settle (шаг 3b).
#     Если ругается — settle не справился (напр., suffix не в forkLocalMigrations),
#     удаление требует осмысленного коммита руками.
LEDGER_TEST="backend/internal/storage/sqlite/migrate_fork_reserved_range_test.go"
MIG_DIR_SYNC="backend/internal/storage/sqlite/migrations"
check_fork_migrations_not_merged_upstream() {
  [ -f "$LEDGER_TEST" ] || return 0
  local names
  names="$(sed -n '/var forkLocalMigrations = \[\]string{/,/^}/p' "$LEDGER_TEST" \
    | grep -oE '"[a-zA-Z_]+"' | tr -d '"')"
  [ -z "$names" ] && return 0
  local upstream_blobs
  upstream_blobs="$(git ls-tree origin/main -- "$MIG_DIR_SYNC/" | awk '{print $3}')"
  local name f blob
  for name in $names; do
    for f in "$MIG_DIR_SYNC"/*"$name"*.sql; do
      [ -f "$f" ] || continue
      blob="$(git hash-object "$f" 2>/dev/null || true)"
      if [ -n "$blob" ] && printf '%s\n' "$upstream_blobs" | grep -qx "$blob"; then
        cat >&2 <<EOF

!! forkLocalMigrations: "$name" ($f) байт-в-байт совпадает с файлом в origin/main.
   Похоже, апстрим смержил эту миграцию под своим номером. Вручную:
     1. git rm $f
     2. убрать "$name" из forkLocalMigrations в $LEDGER_TEST
     3. проверить/убрать соответствующую запись в shippedMigrations, если есть
     4. go -C backend test ./internal/storage/sqlite/ -run TestForkMigrationsUseReservedRange
EOF
      fi
    done
  done
}
echo "==> проверка forkLocalMigrations на совпадение с апстримом..."
check_fork_migrations_not_merged_upstream
echo "   OK"

# 3e. Детекция дублей коммитов ВНУТРИ дельты форка (subject встречается >1 раза).
#     Инцидент: та же логическая фича (per-role env profile, user-scope config)
#     оказалась переиграна в истории дважды — 11 subject-строк по 2 копии каждая
#     (обнаружено 2026-08-28). Порог: НЕ блокирует уже известный бардак, но
#     блокирует появление НОВОГО дубля: типичный симптом случайного повторного
#     cherry-pick/merge уже присутствующей feature-ветки.
#     NB: --no-merges обязателен — merge-коммиты синка имеют одинаковый
#     subject-паттерн и без фильтра каждый синк «находил бы» N-1 дублей.
DUPES_CACHE="$(git rev-parse --git-dir)/ao-sync-known-dupes-count"
KNOWN_DUPES="$(cat "$DUPES_CACHE" 2>/dev/null || echo 0)"

echo "==> проверка дублей коммитов в дельте..."
DUP_SUBJECTS="$(git log --no-merges origin/main..HEAD --format='%s' | sort | uniq -d)"
CURRENT_DUPES="$(printf '%s\n' "$DUP_SUBJECTS" | grep -c . || true)"

if [ "$CURRENT_DUPES" -gt 0 ]; then
  echo "   найдено $CURRENT_DUPES дублирующихся subject-строк (было известно: $KNOWN_DUPES):"
  printf '%s\n' "$DUP_SUBJECTS" | while IFS= read -r subj; do
    [ -z "$subj" ] && continue
    shas="$(git log --no-merges origin/main..HEAD --format='%H %s' | grep -F " $subj" | awk '{print $1}')"
    ids="$(for sha in $shas; do git show "$sha" | git patch-id --stable | awk '{print $1}'; done | sort -u | wc -l | tr -d ' ')"
    if [ "$ids" = "1" ]; then eq="patch-id EQUAL (безопасно squash/drop)"; else eq="patch-id DIFFERENT (нужна ручная сверка diff)"; fi
    echo "     - \"$subj\": $shas — $eq"
  done
  echo "   Разовая чистка: ~/bin/ao-list-duplicate-commits.sh --fix"
else
  echo "   OK, дублей subject-строк нет"
fi

if [ "$CURRENT_DUPES" -gt "$KNOWN_DUPES" ]; then
  cat >&2 <<EOF

!! Появился НОВЫЙ дубль коммита (было известно $KNOWN_DUPES, сейчас $CURRENT_DUPES).
   Похоже, feature-ветка задвоилась (повторный cherry-pick/merge уже
   присутствующей работы). Проверь список выше перед тем как продолжать.
EOF
  exit 1
fi
# DUPES_CACHE обновляется только в самом конце (шаг 8), вместе со снятием
# BACKUP_MARKER — т.е. только если весь sync успешно дошёл до конца.

# 4. Проверка коллизии миграций — САМЫЙ ДЕШЁВЫЙ и самый частый отказ этого форка
#    (дважды подряд: дубль номера, отсутствие записи в shippedMigrations ledger).
#    Гонять ТОЧЕЧНО, не весь пакет — доли секунды.
# NB: команда пишет в лог и проверяется по СОБСТВЕННОМУ коду возврата. Раньше
# было `if ! go test ... | tee file`, что проверяло код возврата tee — то есть
# коллизия миграции проходила через гейт молча.
MIG_LOG=/tmp/ao-sync-migrate-test.log
migration_tests_pass() {
  go -C backend test ./internal/storage/sqlite/ \
    -run 'TestMigrationVersionsAreUnique|TestMigrationVersionLedger' >"$MIG_LOG" 2>&1
}
# FIXER определён выше (шаг 3) — используется и здесь.

echo "==> проверка миграций (TestMigrationVersionsAreUnique + TestMigrationVersionLedger)..."
if ! migration_tests_pass; then
  cat "$MIG_LOG" >&2
  echo "" >&2
  echo "!! Миграции красные — пробую автопочинку ($(basename "$FIXER"))..." >&2

  [ -x "$FIXER" ] || { echo "!! $FIXER не найден — чини вручную." >&2; exit 1; }

  # Фиксер сам решает, коллизия ли это (наш номер занял upstream): переименует
  # наш файл на свободный номер и перепишет запись в shippedMigrations.
  # Поведенческие тесты он не трогает — они находят миграцию по имени через
  # migrationVersion(t, "<суффикс>").
  "$FIXER" || { cat >&2 <<'EOF'

!! Автопочинка не справилась — значит это не типовая коллизия номера.
   Ручной путь:
     1. ls backend/internal/storage/sqlite/migrations/*.sql | sort | tail -5
     2. git mv <наш_файл>_XXXX_*.sql <наш_файл>_<свободный>_*.sql
     3. переписать запись в shippedMigrations в
        backend/internal/storage/sqlite/migrate_burned_versions_test.go
   См. память ao-0041-migration-duplicate-fix.
EOF
    exit 1; }

  echo "==> автопочинка прошла; повторяю проверку миграций..."
  migration_tests_pass || { cat "$MIG_LOG" >&2; echo "!! после автопочинки всё ещё красные — дальше руками." >&2; exit 1; }
  echo "   OK (после автоперенумерации — ПРОВЕРЬ git diff перед коммитом)"
fi
rm -f "$MIG_LOG"
echo "   OK"

# 5. Регенерация артефактов — убирает ложные diff-конфликты между regen-файлами
#    (openapi.yaml, schema.ts, sqlc gen/*.go) и то, что реально изменилось в API.
echo "==> регенерация API-спеки (openapi.yaml + schema.ts)..."
npm run api >/tmp/ao-sync-regen.log 2>&1 || { echo "!! npm run api упал:" >&2; cat /tmp/ao-sync-regen.log >&2; exit 1; }
# Эти два файла помечены merge=ours в .git/info/attributes: при конфликте git
# молча оставляет НАШУ версию, а корректное содержимое восстанавливает regen
# выше. Поэтому непустой дифф здесь — нормальный и ОЖИДАЕМЫЙ исход после
# merge, а не тревога; важно его увидеть, иначе не отличить «regen ничего не
# поменял» от «regen починил то, что merge=ours оставил устаревшим».
if git diff --name-only -- backend/internal/httpd/apispec/openapi.yaml frontend/src/api/schema.ts | grep -q .; then
  echo "   !! regen изменил openapi.yaml/schema.ts — закоммить эти изменения отдельным chore-коммитом."
  git diff --stat -- backend/internal/httpd/apispec/openapi.yaml frontend/src/api/schema.ts
else
  echo "   OK, без изменений"
fi

# Три точки (merge-base), а не $BACKUP_TAG..HEAD: последнее сравнивает старый
# main с новым и включает ЛЮБУЮ апстримную правку queries/ за период — то есть
# почти всегда истинно, даже когда дельта форка queries/ не касалась. Три
# точки спрашивают только «поменяла ли ЭТА дельта queries/ относительно её же
# базы», что и должно гейтить дорогой regen.
if git diff --name-only origin/main...HEAD -- backend/internal/storage/sqlite/queries/ | grep -q .; then
  echo "==> дельта форка меняет queries/ — регенерация sqlc..."
  if ! npm run sqlc >/tmp/ao-sync-sqlc.log 2>&1; then
    # proxy.golang.org бывает недоступен (TLS handshake timeout) — ретраим через
    # локальный кэш модулей как file://-прокси: точная версия sqlc уже скачана
    # прошлыми синками и лежит в ~/go/pkg/mod.
    echo "   go run не смог скачать sqlc из сети — ретраю через локальный кэш модулей..."
    if ! GOPROXY="file://$HOME/go/pkg/mod/cache/download" npm run sqlc >/tmp/ao-sync-sqlc.log 2>&1; then
      echo "!! npm run sqlc упал (в том числе через локальный кэш):" >&2
      cat /tmp/ao-sync-sqlc.log >&2
      exit 1
    fi
  fi
  if git diff --name-only -- backend/internal/storage/sqlite/gen/ | grep -q .; then
    # gen/ не всегда воспроизводим regen'ом: апстрим вписывал артефакты руками
    # (issue #4621), поэтому regen может дать несобираемый дифф. Пробуем билд;
    # красный — откатываем regen и оставляем gen/ как был, синк продолжается.
    echo "   !! sqlc regen изменил gen/ — проверяю, что билд жив..."
    if ( cd backend && go build ./... ) >/tmp/ao-sync-regen-build.log 2>&1; then
      echo "   !! regen зелёный по билду — закоммить эти изменения отдельным chore-коммитом."
      git diff --stat -- backend/internal/storage/sqlite/gen/
    else
      echo "   !! после regen билд красный — откатываю regen gen/ (артефакты апстрима не воспроизводимы, см. #4621):"
      head -5 /tmp/ao-sync-regen-build.log | sed 's/^/        /'
      git restore -- backend/internal/storage/sqlite/gen/
      echo "   gen/ оставлен как был, синк продолжается."
    fi
    rm -f /tmp/ao-sync-regen-build.log
  else
    echo "   OK, без изменений"
  fi
fi

# 6. npm install — единственная защита от «upstream добавил зависимость,
#    её нет в node_modules» (стреляло: @fontsource-variable/geist и
#    @tanstack/react-virtual, каждая съела 5 минут package до отказа).
#    Гоняем ВСЕГДА, не по условию: зависимость может прийти и из апстрима
#    (origin/main), и из дельты, а любое diff-условие ловит лишь то, что
#    рядом с этой конкретной синк-сессией, а не «что вообще изменилось в
#    package.json с последнего install». npm install при актуальном lockfile
#    дёшев (~5с), а пропущенная зависимость стоит 5 минут сборки — всегда
#    гонять.
echo "==> npm install (frontend)..."
( cd frontend && npm install ) || { echo "!! npm install упал." >&2; exit 1; }

# 7. Быстрые гейты: whole-module gofmt (см. память ao-gofmt-whole-module-gate —
#    точечный gofmt на файлах не ловит рассинхрон после merge) + go build.
echo "==> gofmt -l . (весь backend-модуль)..."
FMT_OUT="$(cd backend && gofmt -l .)"
if [ -n "$FMT_OUT" ]; then
  echo "!! gofmt нашёл неотформатированные файлы:" >&2
  echo "$FMT_OUT" >&2
  echo "   Почини: (cd backend && gofmt -w \$FMT_OUT)" >&2
  exit 1
fi
echo "   OK"

echo "==> go build ./... (backend)..."
if ! ( cd backend && go build ./... ) 2>/tmp/ao-sync-build.log; then
  echo "!! go build упал:" >&2
  cat /tmp/ao-sync-build.log >&2
  exit 1
fi
rm -f /tmp/ao-sync-build.log
echo "   OK"

# 8. Итог + подсказки на следующие (осознанно ручные) шаги.
#    Синк дошёл до конца — снимаем маркер, чтобы СЛЕДУЮЩИЙ запуск завёл свежий
#    backup-тег, а не продолжал считать этот синк незавершённым. Сам тег
#    остаётся в репо как точка отката.
rm -f "$BACKUP_MARKER"
# Кэш дублей коммитов (шаг 3e) обновляем только здесь — синк дошёл до конца
# успешно, значит текущее число дублей становится новым «известным» порогом.
printf '%s\n' "$CURRENT_DUPES" > "$DUPES_CACHE"
echo "=========================================="
echo "==> синк готов. main = $(git rev-parse --short HEAD): $(git rev-list --count --no-merges origin/main..HEAD) коммитов дельты + $(git rev-list --count --merges origin/main..HEAD) merge-коммитов поверх origin/main."
echo "    Точка отката (состояние ДО синка): git reset --hard $BACKUP_TAG"
echo "    Дальше вручную (merge историю не переписывает — push обычный fast-forward):"
echo "      git push fork main"
echo "      ~/bin/rebuild-ao.sh"
echo ""
echo "    Перед push ветки в upstream-PR — полные гейты (golangci-lint + typecheck:e2e,"
echo "    те самые, которых нет здесь и на которых уже краснел CI):"
echo "      ~/bin/ao-pregate.sh"

# 9. Отчёт по mergeable-статусу моих открытых PR в апстриме.
#    Только отчёт — никаких rebase/push здесь: конфликт может быть безобидным
#    (regen-артефакты) или содержательным (см. случай #3910 — PR оказался
#    дублем уже смерженного апстримного фикса), решение всегда ручное.
#    Не блокирует синк (может упасть без gh/сети) — просто пропускается.
if command -v gh >/dev/null 2>&1; then
  echo ""
  echo "==> проверка mergeable-статуса открытых PR (Untrivial-ai/agent-orchestrator, author=axisrow)..."
  PR_JSON="$(gh pr list --repo Untrivial-ai/agent-orchestrator --author axisrow --state open \
    --json number,title,url,mergeable 2>/dev/null || true)"
  if [ -z "$PR_JSON" ] || [ "$PR_JSON" = "[]" ]; then
    echo "   нет открытых PR или gh недоступен — пропускаю."
  else
    # UNKNOWN означает «GitHub ещё не пересчитал» (обычно на старых/неактивных
    # PR) — не факт конфликта, форсируем пересчёт через REST для каждого номера.
    NUMBERS="$(printf '%s' "$PR_JSON" | jq -r '.[].number' 2>/dev/null || true)"
    if [ -z "$NUMBERS" ]; then
      echo "   не удалось разобрать список PR (нужен jq) — пропускаю."
    else
      CONFLICTING=""
      FAILED=""
      for n in $NUMBERS; do
        title="$(printf '%s' "$PR_JSON" | jq -r ".[] | select(.number==$n) | .title")"
        state="$(gh api "repos/Untrivial-ai/agent-orchestrator/pulls/$n" --jq '.mergeable_state' 2>/dev/null || echo "")"
        if [ "$state" = "dirty" ]; then
          url="$(printf '%s' "$PR_JSON" | jq -r ".[] | select(.number==$n) | .url")"
          CONFLICTING="$CONFLICTING#$n\t$title\t$url\n"
        elif [ -z "$state" ] || [ "$state" = "unknown" ]; then
          # Пустой/unknown state — gh упал или GitHub не посчитал. Молча считать
          # это «конфликтов нет» нельзя (инцидент: 11 реальных конфликтов при
          # пустом списке) — отдельный список, чтобы ложный OK был невозможен.
          FAILED="$FAILED#$n\t$title\n"
        fi
      done
      if [ -n "$CONFLICTING" ]; then
        echo "   !! конфликтуют с origin/main:"
        # %b, не %s: CONFLICTING хранит литеральные \t/\n, read сплитит по
        # настоящим табам — %s их не разворачивал и список печатался пустым.
        printf '%b' "$CONFLICTING" | while IFS=$'\t' read -r num title url; do
          [ -z "$num" ] && continue
          echo "      $num  $title"
          echo "          $url"
        done
        echo "   Разрешать вручную (rebase каждой ветки в отдельном worktree, force-with-lease push)."
      elif [ -z "$FAILED" ]; then
        echo "   OK, конфликтующих нет."
      fi
      if [ -n "$FAILED" ]; then
        echo "   !! не удалось проверить mergeable (ошибка gh или статус не посчитан):"
        printf '%b' "$FAILED" | while IFS=$'\t' read -r num title; do
          [ -z "$num" ] && continue
          echo "      $num  $title"
        done
        echo "   Сверь статусы напрямую: gh pr list --author axisrow --state open --json number,mergeable"
      fi
    fi
  fi
fi
