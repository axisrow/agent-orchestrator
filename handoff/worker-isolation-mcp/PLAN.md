# План: изоляция воркеров AO (MCP-split + лог развилок в issue)

> **ОБНОВЛЕНО 2026-06-26 под dev-сборку ReverbCode.** План был BLOCKED на `@aoagents/ao@0.9.5`.
> После апдейта на dev-сборку (Go-бэкенд) **половина разблокировалась нативно**, половина — нет.
> Аудит кода `backend/internal` подтверждён. Ниже — только актуальный остаток.

## Что СТАЛО нативным (вычеркнуто из плана)

Эти куски больше делать НЕ нужно — они уже в коде новой сборки:

- ✅ **Per-role agent / model / permissions** (воркер ≠ оркестратор).
  `ProjectConfig.Worker` / `.Orchestrator` (`RoleOverride{Harness, AgentConfig{Model, Permissions}}`)
  — `backend/internal/domain/projectconfig.go:70`, мердж в `session_manager/manager.go:340`.
  Настраивается через `ao project set-config <p> --worker-agent … --orchestrator-agent …`.
- ✅ **Per-role system prompt**. `buildSystemPrompt` ветвится по роли сессии
  (`manager.go:954`), переисчисляется при restore. Отдельный orchestrator/worker промпт —
  встроен. (Поля `customInstructions` пока НЕТ — оно в open-PR #1382.)
- ✅ **Изоляция env / permissions оркестратора от воркера** достигается per-role
  `AgentConfig` — отдельный костыль с обнулением `env` в worker-settings больше не требуется
  для модели/permissions/агента.

→ Старый раздел «два шаблона settings + обнуление orchestrator-env + per-worktree
override как workaround» **устарел** в части модели/permissions/агента. Остаётся только MCP
и хук-логирование (ниже).

## Что ОСТАЛОСЬ (реальный остаток)

### 1. MCP-изоляция воркеров — ❌ всё ещё НЕТ. **Главный пункт.**

Аудит: MCP-конфига в системе нет вообще — ни в `AgentConfig` (только `Model`,
`Permissions` — `domain/agentconfig.go`), ни в `ProjectConfig`. claude-code адаптер
(`adapters/agent/claudecode/claudecode.go`) **не передаёт** `--mcp-config` / `--settings` /
`--setting-sources`. Воркер читает глобальный `~/.claude/settings.json` оркестратора целиком,
со всеми MCP-серверами.

**Цель:** воркеры по умолчанию без MCP (лёгкие/быстрые), с опциональным явным MCP-набором
для задач, которым нужны инструменты (codegraph, браузер).

**Запрошено апстриму:** issue **#2195** (создан 2026-06-26) — «Let workers use a different
MCP server set than the orchestrator».

**Два пути реализации:**

- **(A) Нативно, по образцу droid.** `adapters/agent/droid/droid.go` УЖЕ умеет
  `--settings <path>` + пишет process-scoped runtime settings-файл (`permissionSettingsArgs`).
  Это готовый шаблон: добавить в claude-code адаптер аналог — `--mcp-config <path>` (и/или
  `--settings`), а в `RoleOverride.AgentConfig` — поле под MCP-набор. «Без MCP» = пустой/
  отсутствующий конфиг; «с MCP» = подложенный per-session файл. Самодостаточный вклад,
  контрибутируемый в апстрим (ровно как и планировалось — «help develop upstream»).
- **(B) Workaround без правки Go.** Per-worktree `.mcp.json` + минимальный
  `.claude/settings.json`, раздаваемый через `postCreate`/symlink проектного конфига. claude
  читает их по discovery. Менее чисто, но не требует форка адаптера. Подходит как временная
  мера, пока (A)/апстрим не готовы.

Рекомендация: **(A)** как основной вклад; **(B)** — если нужно «здесь и сейчас» до мержа.

### 2. Хук-логирование развилок (AskUserQuestion → GitHub issue) — ⚠️ ЧАСТИЧНО.

Скрипты `log-askuserquestion-{pre,post}.sh` (в этом каталоге) — **готовы и рабочие**:
PRE пишет вопрос+варианты, POST — выбранный ответ; issue из ветки `feat/issue-N`;
no-op вне воркер-ветки; never-block.

**Блокер:** claude-code адаптер вешает только `SessionStart / UserPromptSubmit / Stop /
Notification / SessionEnd` (`adapters/agent/claudecode/hooks.go:62`). **`PreToolUse` /
`PostToolUse` у claude-code НЕТ** — то есть AO не на что нативно повесить наши скрипты.
Хуки ставятся per-agent-type, не per-role.

**Пути:**
- **(A) Workaround:** вписать наши Pre/PostToolUse-хуки прямо в per-worktree
  `.claude/settings.json` воркера (claude их подхватит сам, мимо AO). AO домержит свои хуки
  поверх — порядок проверить на 1 воркере (известные баги инъекции: #2001 malformed при
  существующих SessionStart, #2091/#2160 `$CLAUDE_PROJECT_DIR`).
- **(B) Нативно:** расширить claude-code адаптер, чтобы поддерживал Pre/PostToolUse и умел
  ставить хуки per-role (воркер логирует развилки в issue, оркестратор — нет). Зависит от
  того, как ляжет MCP/settings-механизм из п.1 (тот же `--settings`-канал).

**Известное ограничение** (claude-code #50728): в чистом headless (`claude -p`)
AskUserQuestion авто-резолвится пусто. Наши воркеры — tmux с живым stdin, меню реально
показывается → хук срабатывает. **Проверить на 1 воркере.**

## Связанная активность апстрима (следить, не дублировать)

Тема живая, ровно наши пункты в работе:
- Closed (в апстриме, могут быть ещё не в graft): #222/#219 per-role model, #1100
  tool-configurability, #500/#462 auto-accept MCP.
- Open PR: **#2126** конфигурируемые permissions оркестратора, **#2091**/**#2160** фиксы
  инъекции claude-code хуков (`$CLAUDE_PROJECT_DIR`), **#1382** `customInstructions`,
  **#2117** `--model` на спавн.
- Наш issue: **#2195** (MCP per role) — отслеживать ответ мейнтейнеров перед тем, как пилить
  (A): возможно, они задизайнят механизм иначе.

## Артефакты в этом каталоге
- `log-askuserquestion-pre.sh` / `-post.sh` — готовые хук-скрипты (всё ещё актуальны для п.2).
- `README.md` — исходный контекст блокера (для истории; модель/permissions-часть устарела).
- `PLAN.md` — этот файл.

## Verification (на 1 воркере, потом раскатка)
1. **Сначала проверить базу:** через `ao project set-config` задать воркеру отдельный
   agent/model/permissions; заспавнить 1 воркер; убедиться, что per-role override применился
   (это уже нативно — должно просто работать).
2. **MCP:** реализовать выбранный путь (A или B); на без-MCP воркере MCP-инструменты
   недоступны (лёгкая сессия), на with-MCP — доступны. Проверить, что MCP оркестратора НЕ
   течёт в дефолтного воркера.
3. **Хук развилок:** спровоцировать AskUserQuestion → PRE записал вопрос+варианты в issue,
   POST — выбранный ответ. Проверить no-op вне воркер-ветки и never-block (gh-ошибка не
   валит инструмент).
4. Только после зелёной проверки на 1 воркере — делать без-MCP дефолтом для новых воркеров.

## Порядок / триггер возобновления
- **Триггер уже наступил:** dev-сборка стоит, per-role config работает. Можно начинать.
- Перед реализацией нативного пути (A) — **дождаться реакции на #2195**: если мейнтейнеры
  возьмут MCP-конфиг сами или предложат форму поля, делать поверх их дизайна, не форкаться.
- Если нужно «здесь и сейчас» — workaround (B) для MCP + (A) для хуков, на одном воркере,
  без раскатки до проверки.
