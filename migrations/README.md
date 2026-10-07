# migrations

Миграции схемы PostgreSQL для [golang-migrate](https://github.com/golang-migrate/migrate).

Каждая миграция — пара файлов с последовательным номером:

- `NNNNNN_<имя>.up.sql` — применение изменений;
- `NNNNNN_<имя>.down.sql` — откат.

| Версия | Миграция | Что делает |
|---|---|---|
| 1 | `000001_create_metrics_table` | таблица `metrics`: gauge и counter в одной таблице, ключ `(id, mtype)` |

Файлы встроены в бинарник сервера через `embed` (`migrations.go`), и сервер применяет их при старте (`repository.ApplyMigrations`). Подробнее и команды CLI — в корневом `README.md`, раздел «Миграции».

Уже применённую миграцию не редактируйте: изменение схемы оформляется новой миграцией со следующим номером (`migrate create -ext sql -dir ./migrations -seq <имя>`).
