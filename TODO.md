# Runtime — задачи общего v2

Начальные локальные срезы сохранены как прототип. Все незакрытые пункты ниже
относятся к общему релизу Liapoldus v2 и не входят в приёмку v1. Версия
`contracts/v1` обозначает первую версию контракта самого плагина, а не этап
экосистемы.

- [x] Начальная строгая settings-схема и тестируемая проверка group/command/scope.
- [x] Начальный product Manifest с `runtime.invoke` и `runtime.http`; payload/response/error schemas и runtime registration ещё нужны.
- [ ] Полная JSON Schema validation при Reload, проверка model generation и atomic swap.
- [ ] Подключить к Plugin SDK Admin Surface уже реализованный bounded content-addressed WASM store; добавить operation status и потоковый E2E.
- [x] Изолированный вызов WASM через versioned JSON ABI с bounds из contract asset; без WASI/host imports, включая проверку module/ABI/result.
- [ ] Добавить разрешённые узкие host-функции, Domain access, cancellation/timeout adversarial vectors и полную sandbox приёмку.
- [ ] Product-owned peer `runtime.invoke`, Server HTTP terminal adapter и проверка caller identity.
- [ ] Host-вызовы Domain с tenant/site fencing и command entity ACL.
- [ ] Независимое масштабирование групп, digest parity и readiness fencing.
- [ ] Plugin SDK REST Reload/pull, mTLS и CLI ручного запуска.
- [ ] Сквозные E2E, sandbox adversarial tests, macOS/Linux gates; не объявлять плагин готовым до их прохождения.
