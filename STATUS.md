# STATUS — прогресс разработки TapeNest

> Единственный источник истины о состоянии проекта между сеансами ИИ.
> Каждый сеанс: начал с чтения этого файла → работал → обновил этот файл.
> Легенда: `[ ]` не начато · `[~]` в работе · `[x]` завершено

_Последнее обновление: 2026-10-02 (YouTube Music — основной каталог, ADR 0012)_

## Текущее состояние

- **Этап:** 3. Поиск и воспроизведение идут в YouTube Music (ADR 0012); торренты
  (ADR 0011) остаются запасным путём, только если YouTube Music ничего не нашёл.
  Этап 2 — кроме Playwright-sidecar для куки. Следующий продуктовый — этап 4
  (CineNest, streaming-service). Индексеры в Prowlarr оператор добавляет сам.
- **Готовые артефакты:**
  - `services/api-gateway`: initData → JWT, refresh с ротацией, Redis rate limit, прокси-каркас.
    OpenAPI: `docs/api/gateway.openapi.yaml`.
  - `services/bot-service`: webhook, /start и /help (ru/en), web_app-кнопка, кнопка меню,
    ссылки → ack → gateway → download-service; прогресс правкой сообщения, файл ≤ 50 МБ
    отправляется видео, крупнее — ссылкой (поток `download:events`).
  - `services/download-service`: API + воркер (yt-dlp → MinIO), очередь Redis Streams,
    квоты, ретраи с эскалацией прокси. OpenAPI: `docs/api/download.openapi.yaml`.
  - `services/music-service`: API + воркер. Каталог синхронизируется из Navidrome; поиск
    pg_trgm; лайки, плейлисты, позиции; «Недавнее» и «Популярное»; волна-MVP; батчер
    play_events (Redis Streams → месячные партиции); стриминг — подписанные ссылки с Range
    через публичный маршрут gateway `/api/v1/stream/*`.
  - `services/reco-service`: API :8086 + воркер :8087 (Go, без Python). Три слоя:
    - долгосрочный профиль вкуса с экспоненциальным затуханием;
    - коллаборативная фильтрация (co-occurrence + implicit ALS);
    - аудио-признаки (ffmpeg + собственный DSP на Go).
    Ранжирование: смешивание источников, MMR, Thompson/ε, свежесть, режимы, объяснения.
    music-service ходит в него за батчами волны (таймаут + breaker, при отказе — прежняя
    эвристика). OpenAPI: `docs/api/reco.openapi.yaml`; офлайн-оценка — `docs/reco/`.
  - `apps/waveplayer`: реальные gateway и music-service (dev по умолчанию без моков;
    `VITE_USE_MOCKS=music|all` оставлены).
  - `apps/cinenest`: авторизация через реальный gateway; `/cinema/*` на моках до этапа 4;
    отдаётся по пути `/cinenest/`.
  - Контракт WavePlayer: `docs/api/waveplayer-contract.md` + `waveplayer.openapi.yaml`.
  - Контракт CineNest: `docs/api/cinema.openapi.yaml`.
- **Последний ADR:** 0010 (`docs/adr/`).
- **Dev-запуск:** `make miniapp-up` / `make miniapp-down` / `make stack-status`.
  - Процессы: PG/Redis/MinIO/Navidrome :4533 (нативно), gateway :8080, bot-service :8081,
    download-service :8082, download-worker (health :8083), music-service :8084,
    music-worker (health :8085), reco-service :8086, reco-worker (health :8087), Vite :5173, CineNest Vite :5174, ngrok, url-watch.
  - Демо-музыка: `make music-seed` — 134 трека CC0/public domain в 24 альбомах (Musopen и др.
    через Wikimedia Commons; лицензия каждого файла проверяется через Commons API), `$MUSIC_DIR/CREDITS.md`.
  - Перезапуск одного сервиса: `tools/dev/svc.sh restart <gateway|music|reco|…>`.
  - Один публичный URL: `/` → Vite, `/cinenest/` → CineNest, `/api` → gateway,
    `/tg/webhook` → бот, `/media/` → MinIO (ADR 0005, 0007, 0008).
  - Логи и pid-файлы: `/workspace/logs`.

## Чек-лист этапов (по разделу 16 ТЗ)

### Этап 0. Фундамент
- [x] Каркас monorepo (services/*, apps/*, deploy/, docs/adr/, tools/); `apps/deploy/{grafana,k8s}` → `deploy/`; корневой `.gitignore`, `README.md`
- [x] Makefile (dev/test/lint/build/infra-*/compose-config/initdata/miniapp-*) — `make help`
- [x] deploy/docker-compose.dev.yml (PG 16, Redis 7.4, MinIO, Navidrome, TorrServer; профили `observability` = Prometheus+Grafana, `edge` = Nginx; образы закреплены по digest) — проверено только `docker-compose config -q` (Docker на машине недоступен)
- [x] .env.example (только имена переменных)
- [x] CI (.github/workflows/ci.yml: фронтенд lint/typecheck/test/build, Go-матрица пропускается без go.mod, compose config, secrets-guard) — `actionlint` OK
- [x] tools/initdata-mock (Go, stdlib: подпись/валидация initData, тесты) — `make initdata`, `make go-test`
- [x] ADR 0001 фоновое аудио, 0002 дополнения контракта, 0003 образ MinIO, 0004 dev-моки и туннель
- [x] Фронтенд WavePlayer переписан с нуля (исправлены все известные баги старой версии, см. `apps/waveplayer/README.md`); lint 0 предупреждений, typecheck, 66 тестов Vitest, build — OK

### Этап 1. Ядро
- [x] api-gateway (Go 1.22, chi, pgx + sqlc, golang-migrate, cleanenv, go-redis):
  - initData HMAC по спецификации Telegram + свежесть `auth_date`;
  - upsert в `gateway.users` (UUID + `telegram_id`);
  - JWT access + opaque refresh в Redis (ротация, reuse → отзыв сессии, logout);
  - rate limit (Redis token bucket), request id, JSON-логи slog, CORS (с `*.ngrok-free.app`);
  - `/healthz`, `/readyz`;
  - прокси-каркас: music / download / streaming (501 — сервис не развёрнут, 503 — упал);
  - `/internal/v1/bot/downloads`;
  - покрытие 91 %, включая интеграционные тесты на реальных PG и Redis.
- [x] bot-service (Go):
  - webhook с проверкой `secret_token` и дедупликацией `update_id`;
  - /start и /help, RU/EN по `language_code`;
  - inline web_app-кнопка; setChatMenuButton, setMyCommands и setWebhook при старте;
  - ссылки YouTube / VK / RuTube → gateway (документированный 501 «появится на этапе 2»);
  - покрытие 96 %.
  - Long polling (`tools/dev/bot-dev`) удалён.
- [x] Dockerfile (multi-stage, distroless, nonroot, образы закреплены по digest) для обоих
  сервисов — hadolint OK; сборка не проверялась (нет Docker). Сервисы добавлены в compose
  (профиль `app`) и в nginx (`/tg/`).
- [x] CI:
  - golangci-lint v2.14;
  - `sqlc diff`;
  - Go-тесты с service-контейнерами PG и Redis и порогом покрытия 70 %;
  - hadolint;
  - compose config (профиль app).
- [x] Нативные PostgreSQL 16 и Redis 7.4 для dev без Docker (`make infra-native`): схемы и
  роли из `deploy/pg/init`.
- [x] WavePlayer на реальной авторизации:
  - Vite проксирует `/api` → gateway; работает через единый ngrok-URL;
  - MSW только для music-доменов;
  - `/me` при старте, 401 → refresh → повтор, при отзыве — повторный вход по initData;
  - прод-сборка без MSW.
- [x] Полировка UX WavePlayer:
  - скелетоны, пустые и ошибочные состояния с «Повторить» (501 и 503 различаются);
  - анимация переходов;
  - haptics; BackButton и MainButton (с fallback вне Telegram);
  - safe-area / content-safe-area и stable height через `--tg-viewport-*`;
  - focus-visible, aria-метки;
  - обложки моков, «волна»-винил.
  - Итог: 87 тестов, скриншоты `/workspace/shots/v2-*`.
- [x] Каркас CineNest (`apps/cinenest`, тот же стек, что у WavePlayer, плюс hls.js 1.5.20):
  - реальная авторизация через gateway; домен `/cinema/*` на MSW-моках до этапа 4
    (`VITE_USE_MOCKS=cinema`);
  - экраны: каталог (поиск, фильтр, «Продолжить просмотр», пагинация), «Смотреть позже»,
    карточка (версии фильма / сезон → серия → качество), плеер (прогрев TorrServer: %, пиры,
    скорость, ETA; продолжение с позиции; сохранение позиции 5 с + flush; fullscreen;
    следующая серия);
  - контракт `docs/api/cinema.openapi.yaml`;
  - отдаётся по префиксу `/cinenest/` на том же URL; в боте кнопка в /start и /help и команда
    `/cinema`; кнопка меню остаётся WavePlayer (ADR 0007);
  - Makefile (`fe-*` для обоих приложений, `cn-dev`) и CI-матрица фронтенда;
  - 101 тест; lint, typecheck и build OK; скриншоты `/workspace/shots/cinenest-*`.

### Этап 2. Скачивание (download-service)
- [x] NormalizeURL + 3-слойная дедупликация + unit-тесты (20+ кейсов):
  - слой 1 — активная задача пользователя; слой 2 — файл уже в MinIO → сразу `done`;
    слой 3 — redsync-лок на канонический URL;
  - SSRF-guard; плейлисты, live и DRM отклоняются.
- [x] Очередь Redis Streams (high/normal/low) + rate limit по доменам:
  - отложенные ретраи через ZSET → `low`; семафоры YT 50 / VK 100 / RuTube 200;
  - квоты на пользователя: 3 активных / 30 в сутки (429 + Retry-After);
  - reclaim задач упавшего воркера.
- [x] Обёртка yt-dlp (двухпроходно, парсер stderr → ErrorKind):
  - `-J` → выбор формата (≤720p mp4, лучший, что влезает в 50 МБ при ≥360p) → загрузка
    с прогрессом;
  - группа процессов убивается при отмене; deno как JS-рантайм.
- [~] Пулы прокси (datacenter/residential/mobile) + health-check — код и лестница эскалации
  готовы, пулы пустые (прямое соединение) — нужны подписки на прокси.
- [~] Куки-sidecar (Playwright, cron 6ч, Redis) — чтение из Redis и проверка устаревания
  готовы, ручной импорт `download-worker -import-cookies`; сам sidecar не написан (контракт
  в `tools/cookie-refresher/README.md`).
- [x] Загрузка в MinIO (multipart, streaming pipe):
  - lifecycle 7 дней;
  - presigned-ссылки на публичном origin `/media/…` (TTL 1 ч).
- [x] Бот end-to-end: ссылка → скачивание → уведомление:
  - ack «⏳ Ссылка принята» → прогресс правкой того же сообщения → sendVideo ответом
    на ссылку (≤ 50 МБ) или кнопка-ссылка (> 50 МБ); ошибки RU/EN;
  - поток `download:events`, at-least-once + SETNX-дедуп.
  - Проверено синтетикой; реальная ссылка от пользователя — открыто.
- [x] Gateway `/api/v1/downloads*` → download-service (вместо 501), SSE насквозь,
  внутренний токен ставит gateway.
- [x] Тесты:
  - покрытие download-service 85.7 % без БД / 91.5 % с БД; bot-service 94.7 %;
  - golangci-lint 0; `sqlc diff` OK.
- [x] Dockerfile (Debian slim + python3/ffmpeg/deno + yt-dlp по sha256; hadolint OK),
  compose-сервисы `download-service` и `download-worker`, OpenAPI, ADR 0008, README.

### Этап 3. WavePlayer-бэкенд (music-service)
- [x] Схема `music`: artists/albums/tracks (UUID, мягкое удаление), likes, playlists,
  playback_positions, recent_plays, play_events (RANGE по месяцам + BRIN + default),
  материализованное представление track_popularity; sqlc, golang-migrate
- [x] Каталог из Navidrome (воркер синхронизирует через Subsonic search3, защита от
  массового удаления) + поиск pg_trgm (GIN, word_similarity) + стриминг через Navidrome
  с HTTP Range/HEAD (`io.Copy`, breaker, health-ping; при отказе 503 `streaming` с
  `X-Degraded-Dependency`, каталог жив)
- [x] Подписанные ссылки: аудио (HMAC, TTL ≤ 1 ч, привязка к пользователю) и обложки
  (immutable); публичный маршрут gateway `/api/v1/stream/*` без JWT
- [x] Папки/плейлисты/лайки/позиции по контракту (400/404, изоляция пользователей,
  ≤ 200 плейлистов)
- [x] Батчер play_events: XREADGROUP до 500 событий, ack после коммита, pending/XAUTOCLAIM,
  идемпотентность по event_id; партиции на 2 месяца вперёд; обновление популярности
- [x] Волна-MVP: популярность + лайки + недавнее + реранк сессии (like/skip по артисту),
  без повторов и одного артиста подряд; сессии в Redis
- [x] Read-replica по флагу `DB_REPLICA_URL`
- [x] Gateway: музыкальные маршруты вместо 501; 503 от деградировавшей зависимости не
  открывает breaker
- [x] WavePlayer: dev по умолчанию без моков, медиа-URL разрешаются относительно
  `VITE_API_URL`
- [x] Демо-библиотека: 16 записей CC0 (лицензия проверяется через Commons API), обложки,
  CREDITS.md
- [x] Navidrome нативно (0.53.3, sha256 сверен); админ создаётся через `/auth/createAdmin`
- [x] Тесты (music-service 73.1 % без БД / 92.8 % с БД), golangci-lint 0, sqlc diff,
  Dockerfile (distroless, hadolint), compose (music-service + music-worker), CI (+ job
  валидации OpenAPI), OpenAPI 1.1.0, ADR 0009, README
- [ ] Интеграция с этапом 2 (извлечение аудио из скачанных видео, «добавить в музыку» в
  боте) — в ТЗ не описана, отложена (ADR 0009 п. 9)

### Этап reco. Персональная «Моя волна» (reco-service)
- [x] Отдельный `services/reco-service` (Go 1.22; API :8086 + воркер :8087), своя схема
  `reco` (профили, события, признаки, модели), sqlc, golang-migrate; Python-sidecar не нужен
  (ADR 0010)
- [x] Слой 1 — долгосрочный профиль: веса по артисту, альбому, жанру и тегу. Сигналы:
  - доля дослушивания, ранний пропуск < 30 с;
  - лайки, повторы, добавления в плейлист;
  - экспоненциальное затухание.
  Холодный старт — от лайков и популярности. like/skip в сессии меняют и краткосрочный,
  и долгосрочный профиль.
- [x] События из music-service: прослушивания — из `music:play_events` (своя consumer
  group), like/unlike/playlist_add/playlist_remove/skip/feedback волны — из
  `music:user_events`; воркер-ingest; бэкфилл истории
- [x] Слой 2 — CF: item-item co-occurrence + implicit ALS, пересчёт воркером каждые 15 мин,
  «похожие слушатели» подмешивают непрослушанное
- [x] Слой 3 — аудио-признаки при синхронизации каталога (ffmpeg → PCM → DSP на Go):
  - темп, энергия, громкость;
  - спектральные признаки (центроид, rolloff, flatness, flux), mel-профиль;
  - эмбеддинг, теги настроения;
  - соседи по косинусному сходству.
  Проанализировано 134 из 134 треков; решает холодный старт новых треков.
- [x] Ранжирование: смешивание источников с весами (`RECO_WEIGHTS`), MMR, без одного
  артиста подряд, Thompson sampling по источникам + ε-исследование, свежесть, без повторов
  в сессии. Режимы: для вас, спокойное, бодрое, незнакомое, любимое.
- [x] Объяснения («Потому что вам нравится «X»», «Нравится похожим слушателям» и т. п.)
  в API и в WavePage; там же чипы режимов
- [x] music-service → reco: таймаут + circuit breaker (gobreaker), fallback на эвристику
  ADR 0009 §6, заголовок `X-Wave-Strategy`, `readyz.reco`
- [x] Офлайн-оценка (`make reco-eval`, `docs/reco/evaluation.md`): синтетические
  пользователи, holdout, P/R/NDCG@10, покрытие, разнообразие, новизна, холодный старт,
  симуляция сессий, абляции. Среднее по 5 мирам:
  - NDCG@10 0.083 → 0.098 (+18 %), P@10 +33 %, R@10 +34 %;
  - покрытие каталога 0.40 → 0.78;
  - recall новых треков 0 → 0.19;
  - доля пропусков в сессии 0.130 → 0.073 (−44 %).
- [x] Каталог расширен до 134 треков CC0/PD (7 жанров)
- [x] Тесты: reco-service 93.7 % с БД / 85.0 % без; music-service 92.7 % / 75.5 %;
  WavePlayer 96 тестов. golangci-lint 0, sqlc diff, Dockerfile (Debian slim + ffmpeg,
  hadolint), compose, CI (ffmpeg), OpenAPI, ADR 0010, README.
- [x] e2e через ngrok `make e2e-reco` 27/27: холодный старт, профиль из сигналов,
  персональный батч, режимы, feedback, отказ reco → fallback → восстановление
- [x] Браузер через ngrok (Playwright, светлая и тёмная темы): чипы режимов, объяснения,
  аудио играет, ошибок в консоли нет — `/workspace/shots/v4-*.png`

### Этап 4. CineNest (streaming-service)
- [ ] TorrServer в compose + Go-клиент REST API
- [ ] HLS-прокси с переписыванием m3u8 + прокси сегментов
- [ ] Каталог/карточки/поиск/позиции просмотра
- [~] Фронтенд CineNest (hls.js, индикатор прогрева, продолжение просмотра) — UI готов на моках
  (этап 1); осталось переключить `/cinema/*` на streaming-service и отключить моки
- [ ] Поиск раздач (SourceProvider) + админ-CLI

### Этап 5. Готовность к запуску
- [ ] Мониторинг + алерты (Grafana)
- [ ] Хоррор-тест отказоустойчивости
- [ ] Нагрузочный смоук (k6)
- [ ] Модерация: репорты, hash-matching CSAM, DMCA-процедура
- [~] i18n (ru/en) — WavePlayer, CineNest и бот (ru/en словари) готовы
- [ ] Деплой на VPS (docker-compose + Nginx)

## Проблемы и блокеры

Формат: дата — проблема — статус.

- 2026-09-25 — Docker недоступен на dev-машине ИИ: compose проверен только статически (`docker-compose config -q`), контейнеры не запускались — открыто, проверить `make infra-up` на машине с Docker.
- 2026-09-25 — MinIO перестал публиковать свежие образы в Docker Hub; используется `pgsty/minio` (ADR 0003) — принять/пересмотреть.
- 2026-09-25 — ngrok-free: URL случайный (статический домен не определить только по authtoken), бот сам пересинхронизирует кнопку меню; поддержан `NGROK_DOMAIN` — задать бесплатный статический домен из дашборда ngrok.
- 2026-09-25 — ngrok-free показывает промежуточную страницу при первом открытии в браузере; внутри Telegram один раз нажать «Visit Site» — решится своим доменом/VPS.
- 2026-09-25 — `@telegram-apps/sdk` 2.11 требует поле `signature` в initData; `tools/initdata-mock` добавляет его (бэкенд валидирует по `hash`) — учтено.
- 2026-09-25 — golangci-lint в CI — решено (этап 1).
- 2026-09-25 — Docker недоступен → testcontainers не используем. Интеграционные тесты
  используют `TEST_DATABASE_URL` / `TEST_REDIS_URL` (локально нативные PG и Redis, в CI —
  service-контейнеры), ADR 0006 — решено.
- 2026-09-25 — ngrok-free: браузерный запрос скрипта Service Worker получает HTML-заглушку
  → на `*.ngrok-free.app` MSW работает через fetch-патч (без SW) — решено.
- 2026-09-25 — Скачивание: живьём проверен только YouTube; VK и RuTube — unit-тесты
  и фикстуры. Пулы прокси пустые, куки не заданы: при «confirm you're not a bot» YouTube
  задача упадёт с `bot_check` после ретраев — открыто (прокси/куки).
- 2026-09-25 — Образ download-service не собирался (нет Docker) — открыто, `make app-up`
  на машине с Docker.
- 2026-09-25 — Публичные ссылки на файлы > 50 МБ идут через ngrok-free: в браузере сначала
  промежуточная страница ngrok — решится своим доменом.
- 2026-09-25 — Реальный /start от пользователя через webhook ещё не пришёл (проверено
  синтетическим апдейтом через ngrok → бот → gateway → sendMessage; getWebhookInfo без
  ошибок) — открыто: нажать /start в @tapenest_bot.
- 2026-09-25 — Музыка: образ music-service не собирался (нет Docker) — открыто.
- 2026-09-25 — Музыка: Lidarr-стек (Prowlarr → Lidarr → qBittorrent) в dev не развёрнут;
  каталог — демо-библиотека CC0. Для прода смонтировать свою библиотеку в `/music` Navidrome
  или развернуть Lidarr-стек — открыто.
- 2026-09-25 — Музыка: без gapless и транскодирования (отдаётся оригинальный файл); фоновое
  воспроизведение в Telegram WebView ограничено платформой (ADR 0001) — принято.
- 2026-09-25 — Музыка: извлечение аудио из скачанных видео / «добавить в музыку» в боте в ТЗ
  нет — отложено (ADR 0009).
- 2026-09-25 — Инциденты с dev-паролем Navidrome (лог при автосоздании админа; токен
  Subsonic в URL ошибки) — исправлено в коде, пароль сменён, логи очищены.
- 2026-09-25 — reco: метрики офлайн-оценки синтетические — реальных данных почти нет
  (несколько dev-пользователей). Пересчитать на реальных логах, когда накопятся — открыто.
- 2026-09-25 — reco: образ reco-service не собирался (нет Docker) — открыто, `make app-up`.
- 2026-09-25 — reco: аудио-сходство тембральное (оркестр ↔ оркестр, фортепиано ↔ фортепиано),
  без нейросетевого эмбеддинга — принято (ADR 0010), при необходимости добавить модель.
- 2026-09-25 — reco: при ручном пропуске в волне приходят два сигнала (wave_skip и skip) —
  так задумано, веса маленькие — принято.
- 2026-09-25 — reco: e2e создаёт синтетических пользователей (tg 2000xxxxx), их лайки
  попадают в CF-модель dev-стенда — принято для dev.
- 2026-09-25 — Navidrome 0.53.3: после пересканирования с изменёнными жанрами бывает
  «FOREIGN KEY constraint failed» (`media_file_genres`). Обход: перезапуск Navidrome +
  полное пересканирование — открыто (обновить Navidrome).
- 2026-09-25 — Названия и авторы из Wikimedia Commons разбираются эвристически
  (`seed-music.py`); возможны неточности — принято.

## Журнал

- 2026-09-25 — ТЗ переписано по итогам аудита (MVP/Post-MVP, фиксация решений,
  новые разделы: бюджет, отладка, инициализация, STATUS.md, версии, модерация, i18n).
  Фронтенд WavePlayer готов и проверен (сборка, линт, smoke-тест).
- 2026-09-25 — Этап 0 выполнен: .gitignore, .env.example, Makefile, docker-compose.dev.yml,
  CI, tools/initdata-mock, ADR 0001–0004, контракт API в docs/api. Старый фронтенд удалён,
  WavePlayer написан заново (палитра TapeNest, авто-тема из Telegram, ru/en, 66 тестов).
  Мини-апп запущен через ngrok, кнопка меню @tapenest_bot → WavePlayer, dev-бот отвечает на /start.
- 2026-09-25 — Этап 1 (кроме каркаса CineNest):
  - api-gateway и bot-service на Go; миграции golang-migrate, sqlc;
  - нативные PG и Redis; golangci-lint и hadolint в CI;
  - dev-стек: один ngrok-URL для мини-аппа, API и webhook с авто-пересинхронизацией
    webhook и кнопки меню;
  - WavePlayer переведён на реальную авторизацию (e2e через ngrok: вход, /me, ротация
    refresh, reuse → 401, logout) и отполирован;
  - ADR 0005, 0006.
- 2026-09-25 — Каркас CineNest (этап 1 закрыт):
  - `apps/cinenest` (см. README): авторизация через реальный gateway, моки `/cinema/*`,
    плеер hls.js с прогревом и позицией;
  - `docs/api/cinema.openapi.yaml`;
  - путь `/cinenest/` через единый URL; бот: две web_app-кнопки и `/cinema`;
  - Makefile и CI для обоих приложений; ADR 0007.
- 2026-09-25 — Этап 2 (скачивание):
  - `services/download-service` (API + воркер), MinIO нативно (`tools/dev/infra-native.sh`);
  - gateway проксирует `/api/v1/downloads*`; бот: ack → прогресс → файл или ссылка;
  - Dockerfile, compose, OpenAPI `download.openapi.yaml`, ADR 0008.
  - e2e через ngrok (`tools/dev/e2e-downloads.sh`):
    - вход, 401, 400 `UNSUPPORTED_SOURCE`, 202;
    - SSE: probing → 37/74/89 % → done за ~6 с; mp4 12.9 МБ, 720p;
    - presigned `/media/…` → 200; дедуп → 200 за 0.7 с.
  - Бот (синтетика, без сообщений реальным пользователям):
    - webhook → ack → Telegram «chat not found» (ожидаемо);
    - `/internal/v1/bot/downloads` с фиктивным чатом → воркер (Big Buck Bunny 10.5 мин →
      480p 38.8 МБ за 7.5 с) → событие → бот пробует editMessageText/sendVideo → «chat not
      found» → событие отброшено без ретраев;
    - кэшированные ссылки → событие `cached`.
- 2026-09-25 — Этап 3 (music-service):
  - `services/music-service` (API :8084 + воркер :8085), Navidrome 0.53.3 нативно,
    `make music-seed` (16 записей CC0), `make e2e-music`;
  - gateway: `/api/v1/{tracks,playlists,wave,events}` → music-service, публичный
    `/api/v1/stream/*` (GET/HEAD) для подписанных медиа-ссылок, 503 с
    `X-Degraded-Dependency` не считается отказом breaker;
  - WavePlayer: dev по умолчанию на реальном бэкенде; Dockerfile, compose, CI (+ OpenAPI),
    `waveplayer.openapi.yaml` 1.1.0, `gateway.openapi.yaml` 1.2.0, ADR 0009.
  - Проверка через ngrok (`make e2e-music`, 30/30):
    - вход; popular/search/курсор; stream-url → GET 200 и Range 206
      (`bytes 1000-1999/824036`, audio/mpeg), подпись с ошибкой → 403; обложка → image/jpeg;
    - лайк/анлайк/liked; CRUD плейлиста; волна (10 + 6 треков, feedback);
    - позиция PUT/GET; track-listened → батчер → play_events → recent.
  - Отказ Navidrome: stream-url → 503 `streaming` (Retry-After 15, X-Degraded-Dependency),
    каталог и плейлисты 200, readyz `degraded`; после подъёма — снова 200.
  - Браузер (Playwright, Chrome, 390×844, ru, светлая и тёмная темы) через ngrok:
    - обложки грузятся (17–18 шт.); аудио играет (запросы 206 audio/mpeg); в консоли нет
      ошибок;
    - без заголовка ngrok медиа работают после разового «Visit site» (cookie
      `abuse_interstitial`).
    - Скриншоты: `/workspace/shots/v3-*.png`.
  - Инцидент: `ND_DEVAUTOCREATEADMINPASSWORD` вывел dev-пароль Navidrome в лог. Лог и данные
    удалены, пароль сменён, админ теперь создаётся через `/auth/createAdmin`.
  - Инцидент: в ошибке транспорта попал URL Subsonic с токеном (md5(пароль+соль)) — в два
    dev-лога. Добавлена редакция URL в ошибках, логи очищены, пароль сменён повторно.
- 2026-09-25 — Этап reco («Моя волна» вынесена в отдельный модуль):
  - `services/reco-service` (API :8086 + воркер :8087): профиль вкуса, CF (co-occurrence +
    ALS), аудио-признаки (ffmpeg + DSP на Go), ранжирование с MMR, Thompson/ε, режимами
    и объяснениями; ADR 0010; `docs/api/reco.openapi.yaml`; `docs/reco/`.
  - music-service: клиент reco с таймаутом и breaker, fallback на эвристику, внутренние
    выгрузки каталога и взаимодействий, поток `music:user_events`, `/events/track-skipped`.
  - WavePlayer: чипы режимов, подпись «почему этот трек», сообщение о раннем пропуске.
  - Каталог: 134 трека CC0/PD.
  - Офлайн-оценка (5 синтетических миров) против эвристики:
    - NDCG@10 +18 %, P@10 +33 %, R@10 +34 %;
    - покрытие ×1.9;
    - пропуски в сессии −44 %;
    - холодный старт новых треков 0 → 0.19.
  - Проверка: `make e2e-reco` через ngrok 27/27, включая отказ reco → `strategy=fallback`
    (≈0.7 с через туннель) → восстановление после half-open. Браузер: светлая и тёмная темы,
    ошибок в консоли нет. Скриншоты `/workspace/shots/v4-*.png`.

- 2026-10-02 — Невидимое скачивание музыки (acquisition-service):
  - Доделано то, что оборвалось: тесты сервиса (покрытие internal ≥ 70 %), golangci-lint
    без замечаний, OpenAPI, ADR 0011, README, Dockerfile и сервисы в compose, `make e2e-acquisition`.
  - Поиск в WavePlayer общий: библиотека и MusicBrainz выглядят как обычные треки.
    «Играть» или «в избранное» для отсутствующего файла запускает скачивание само;
    лайк сохраняется сразу, плеер показывает только загрузку (HTTP 202, потом звук).
  - Проверка только на легальном Torznab-заглушке (`tools/legal-indexer`): Internet Archive,
    лицензия CC0 / Public Domain / CC BY. Пиратские индексеры не подключались.
  - Как добавить свои индексеры и чем это грозит: `services/acquisition-service/README.md`.
  - Живой прогон `tools/dev/e2e-acquisition.sh` на `http://127.0.0.1:8080` (2026-10-02):
    запрос «ishizaka well tempered prelude» вернул трек, которого не было в библиотеке
    (`remote=true`), лайк 204 до прихода файла, `stream-url` сначала 202, затем 200 и
    байты 206. Раздача: Internet Archive `bach-well-tempered-clavier-book-1`
    (Public Domain Mark), индексер только «TapeNest Legal Test (CC0)».
    Goldberg уже лежал в библиотеке, поэтому на нём невидимое скачивание не проверялось.
    Публичный ngrok не поднимался: файла с токенами нет. На этой машине Postgres 17 и
    Redis 8 (в compose по-прежнему 16 и 7.4). Docker CLI нет, `docker compose config` не гонялся.

- 2026-10-02 — YouTube Music как основной источник (ADR 0012):
  - Поиск WavePlayer: библиотека, затем YouTube Music (треки, альбомы, артисты).
    Если YouTube Music ничего не вернул, срабатывает запасной путь MusicBrainz/торрент.
  - Воспроизведение: yt-dlp с `player_client=android`, поток проксируется, файл на диск
    не пишется. Лайк пишется в обычную строку `music.tracks` сразу.
  - Код Metrolist/InnerTune не копировался (GPL-3.0). Ключ innertube читается со страницы
    music.youtube.com в рантайме и в логи не попадает.
  - `MUSIC_SOURCES` (по умолчанию `youtube,torrent`) выключает источник без правки кода.
  - Проверка `tools/dev/e2e-ytm.sh` на `http://127.0.0.1:8080`: поиск «ishizaka variatio»
    вернул `remote=true` («Variatio 1. a 1 Clav.»), лайк 204, `stream-url` 200, аудио 206
    `video/mp4` (65 КиБ, файл на диск не сохранялся). UI не менялся.

