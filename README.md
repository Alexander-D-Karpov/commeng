# commeng

Headless-сервис постов и древовидных комментариев на Go с GraphQL API. Данные могут храниться в памяти или PostgreSQL. Проект предоставляет API, а не готовый интерфейс обсуждений.

**Демо:** [comment.akarpov.ru](https://comment.akarpov.ru/) · **GraphQL:** [comment.akarpov.ru/graphql](https://comment.akarpov.ru/graphql)

## Возможности

- Посты, вложенные комментарии, cursor-пагинация и поиск.
- Учётные записи и сессии, гостевые комментарии и идемпотентные лайки.
- Редактирование, мягкое удаление, модерация и управление комментированием.
- Уведомления об ответах и подписки на новые комментарии по `graphql-transport-ws`.
- Журнал событий для восстановления после разрыва соединения, миграции PostgreSQL, метрики и CLI.

Глубина хранения комментариев не ограничена; глубина одного GraphQL-запроса ограничена. Одна страница содержит только непосредственных детей поста или комментария.

## Быстрый старт

Требуется **Go 1.27.3**. Для запуска без базы:

```bash
make run
```

Сервер слушает `127.0.0.1:8080`; данные в памяти исчезают после остановки. Проверка:

```bash
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS http://127.0.0.1:8080/graphql \
  -H 'Content-Type: application/json' \
  --data '{"query":"{ posts(first: 10) { edges { node { id title } } } }"}'
```

Новая база пуста. GraphQL-схема доступна на `/schema.graphql`, Apollo Sandbox — на `/docs/`. Интерфейс Sandbox загружается с CDN Apollo; сам API от CDN не зависит.

## PostgreSQL и Docker Compose

```bash
docker compose up --build -d
curl -fsS http://127.0.0.1:8080/readyz
docker compose down
```

Compose запускает PostgreSQL, применяет миграции и запускает API на loopback. При обычном `down` volume базы сохраняется. Пароль PostgreSQL по умолчанию предназначен **только для разработки**; настройки и секреты задаются через `.env` по образцу [`.env.example`](.env.example).

Для отдельного PostgreSQL:

```bash
export DATABASE_URL='postgres://commeng:password@127.0.0.1:5432/commeng?sslmode=disable'
make migrate-up
make run-db
```

`sslmode=disable` подходит только для локальной доверенной сети. Для удалённой базы настройте TLS. При запуске сервер проверяет применённые миграции.

## API

| Адрес | Назначение |
| --- | --- |
| `POST /graphql` | GraphQL queries и mutations |
| `GET /graphql` с WebSocket Upgrade | GraphQL subscriptions |
| `POST /auth/visitor` | Получение гостевого токена для лайков |
| `GET /p/{post}/c/{comment}` | JSON со страницей и позицией комментария |
| `GET /healthz`, `GET /readyz` | Живость и готовность сервиса |
| `GET /schema.graphql`, `GET /docs/` | Схема и Apollo Sandbox |

### Регистрация и вход

```graphql
mutation {
  register(name: "Alice", email: "alice@example.org", password: "strong-password-123") {
    id name
  }
}
```

```graphql
mutation {
  login(email: "alice@example.org", password: "strong-password-123") {
    token user { id name }
  }
}
```

Передавайте полученный токен в `Authorization: Bearer <token>`. `logout` отзывает сессию. Для гостевых лайков сначала получите токен на `/auth/visitor`, затем передавайте его в `X-Visitor-Token`. Гостевой email не подтверждает личность и не даёт права редактировать комментарий.

### Посты и комментарии

Авторизованный пользователь может создать пост:

```graphql
mutation {
  createPost(input: {title: "Первый пост", body: "Текст публикации"}) {
    id title commentsEnabled
  }
}
```

Автор поста может запретить новые комментарии:

```graphql
mutation {
  setPostCommentsEnabled(postId: "1", enabled: false) { id commentsEnabled }
}
```

Чтобы снова разрешить комментарии, вызовите ту же мутацию с `enabled: true`.

Для гостевого комментария укажите имя и email; для ответа добавьте `parentId`:

```graphql
mutation {
  createComment(input: {
    postId: "1", name: "Reader",
    email: "reader@example.org", body: "Первый комментарий"
  }) { id parentId body permalink }
}
```

Чтение поста и первых комментариев:

```graphql
query {
  post(id: "1") {
    id title
    comments(first: 20, sort: NEWEST) {
      edges { cursor node { id body replyCount } }
      pageInfo { startCursor endCursor hasNextPage hasPreviousPage }
    }
  }
}
```

Для следующей страницы передайте `endCursor` как `after`, для предыдущей — `startCursor` как `before`. Ответы загружаются через `Comment.replies`, путь к корню — через `commentAncestors`, поиск страницы комментария — через `commentJump`. `first` принимает значения от 1 до 100.

Другие операции включают `likeComment`, `unlikeComment`, `editPost`, `editComment`, `deletePost`, `deleteComment`, `searchPosts`, `searchComments`, `notifications` и `moderateComment`. Все поля и аргументы описаны в [GraphQL SDL](internal/graph/schema.graphqls); готовые HTTP-запросы находятся в [examples/commeng.http](examples/commeng.http).

### Подписки

Подключитесь к `/graphql` по WebSocket с субпротоколом `graphql-transport-ws`, отправьте `connection_init`, дождитесь `connection_ack` и подпишитесь на пост:

```graphql
subscription {
  commentAdded(postId: "1") { id postId parentId body createdAt }
}
```

Доставка подписок best-effort. После обрыва клиент восстанавливает события через `commentEvents`, обрабатывает их по `sequence` и устраняет дубли с live-доставкой по `comment.id`. После очистки журнала устаревший курсор может вернуть `EVENT_HISTORY_EXPIRED`: нужно заново загрузить состояние обсуждения.

## Конфигурация

| Переменная | Назначение |
| --- | --- |
| `APP_ENV` | `development`, `test` или `production` |
| `STORAGE` | `memory` или `postgres` |
| `HTTP_ADDR` | Адрес HTTP-сервера; по умолчанию `:8080` |
| `DATABASE_URL` | DSN PostgreSQL |
| `PUBLIC_BASE_URL` | Публичный origin без пути; в production требуется HTTPS |
| `COMMENG_SIGNING_KEY` | HMAC-ключ в hex, минимум 32 байта; обязателен в production |
| `ALLOWED_ORIGINS` | Разрешённые браузерные origin через запятую |
| `TRUSTED_PROXY_CIDR` | Сеть доверенного reverse proxy |
| `PERMALINK_TEMPLATE` | Шаблон URL внешнего сайта с `{post}` и `{comment}` |
| `METRICS_ADDR` | Адрес отдельного приватного Prometheus listener |
| `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SAMPLE_RATE` | Необязательная трассировка OpenTelemetry |

Без signing key вне production создаётся временный ключ: гостевые токены и подписанные курсоры не переживут перезапуск. Нескольким экземплярам нужен одинаковый стабильный секрет. Для production настройте HTTPS, уникальные секреты, доверенный proxy, сетевые ограничения и резервное копирование.

## Миграции и обслуживание

```bash
make build
./bin/commeng help
make migrate-status
make migrate-new NAME=add_feature
./bin/commeng bootstrap-admin --name Admin --email admin@example.org --password-stdin
```

Применённые SQL-миграции менять нельзя: история проверяется по checksum. Откат и восстановление из бэкапа потенциально разрушают данные. Бэкап: `DATABASE_URL=... make backup`. Журнал событий автоматически не очищается; `./bin/commeng prune-events --days 90 --limit 10000` показывает dry-run, а `--yes` выполняет удаление.

## Проверки

```bash
make test
make race
make lint
make check
```

Для полной проверки PostgreSQL нужна **отдельная тестовая база**:

```bash
export TEST_DATABASE_URL='postgres://ci:password@127.0.0.1:5432/ci?sslmode=disable'
make check
make integration
make vuln
```

Интеграционные тесты создают изолированные схемы. `make check` включает сборку, форматирование, `go vet`, тесты, `-race`, линтер и покрытие: минимум 60% без PostgreSQL или 75% с `TEST_DATABASE_URL`. `make bootstrap` отдельно перегенерирует GraphQL-код и обновляет зависимости; обычная сборка этого не делает.

GitHub Actions запускает CI при push в `master`, pull request и вручную. Workflow релиза запускается для тегов `v*`.

Лицензия: [MIT](LICENSE).

## Результаты нагрузочной проверки

Замеры выполнены 3 ноября 2026 года на Intel Core i7-1260P: приложение с `GOMAXPROCS=4`, PostgreSQL 18.6 в контейнере и генератор запросов работали на одной машине. В тестовой базе было 25 постов и первоначально 3000 комментариев; во время записи число комментариев в одном посте выросло до 20 767. Каждый HTTP-сценарий длился 20–30 секунд, до 16 запросов выполнялись одновременно.

Для замера приложения использовался локальный тестовый режим с отключённым rate limiting, без Nginx и TLS. **p95** — время, быстрее которого завершились 95% измеренных запросов.

| Сценарий | Задано запросов/с | Выполнено запросов/с | p95 |
| --- | ---: | ---: | ---: |
| Чтение 20 комментариев из обсуждения с ~7800 комментариями | 600 | 599,6 | 11,9 мс |
| Чтение 20 корневых комментариев с вложенными ответами | 200 | 199,8 | 23,3 мс |
| Чтение страницы из 20 комментариев после позиции 10 000 | 200 | 199,9 | 13,0 мс |
| Чтение 100 предков комментария в ветке глубиной 500 | 200 | 200,0 | 4,2 мс |
| Запись комментариев в один пост | 100 | 100,0 | 11,3 мс |
| Запись комментариев в один пост | 120 | 119,0 | 291,0 мс |
| Запись комментариев в один пост, два прогона | 200 | 119–123 | 732–771 мс |

При целевых 120 записях/с генератор не отправил 29 из 3600 запланированных запросов; при 200/с — 1528–1602 из 4000, поскольку все 16 слотов были заняты. Это пропуски на стороне генератора, а не отказы сервера. Отправленные запросы записи завершились без ошибок. При смешанной нагрузке около 200 чтений и 20 записей в секунду p95 составил 12,5 мс и 10,1 мс соответственно.

В отдельной проверке 100 WebSocket-соединений получили все 1000 ожидаемых событий. p95 от начала мутации до получения события составил 20,7 мс, включая запись и локальную доставку.

При интенсивной записи в один пост наблюдалось ожидание блокировки строки поста в PostgreSQL: создание комментария использует `SELECT … FOR UPDATE`. Приведённые результаты относятся к короткому локальному тесту и не являются оценкой предельной нагрузки production-среды.
