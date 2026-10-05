# Runtime — целевой плагин v2

Runtime — отдельный плагин исполнения объявленных WASM-команд. Core хранит
точные байты JSON-конфигурации, вызывает Plugin SDK REST `Reload`; экземпляр
Runtime сам получает поколение, валидирует его и атомарно активирует после
проверки всех указанных module digests. [Нормативная схема настроек](../../../contracts/v1/settings.schema.json)
принадлежит этому репозиторию.

Каждая Runtime-группа — отдельная единица развёртывания и масштабирования.
Группа имеет явный `groupId`, ссылку на Domain instance, immutable WASM
artifact digest и список команд. Каждая команда допускает только перечисленные
tenant/site scopes и сущности; host передаёт доверенный scope из caller context,
а не из произвольного WASM payload.

WASM запускается через wazero с [версионированным JSON input/output ABI](../../../contracts/v1/wasm-abi.json):
module экспортирует `memory`, `alloc(length) → pointer` и
`invoke(pointer, length) → i64`, где старшие 32 бита результата содержат
длину, младшие — адрес JSON-ответа. Вызов ограничен временем, объёмом
input/output и памятью значениями из ABI contract. Исполнение идёт без
WASI, прямых network и filesystem функций. Узкие host-функции проверяют права
перед Domain или другими разрешёнными peer-вызовами. `runtime.invoke` —
product-owned peer method; Server может связать HTTP route с отдельным
terminal adapter. Одна транзакция ограничена одним Domain batch, не всей
цепочкой между плагинами.

Сейчас реализованы проверка базовой конфигурации, bounded приём artifact с
SHA-256 и компиляцией WASM, а также изолированный JSON ABI-вызов без host
функций. Подключение приёма к Plugin SDK, продуктовые host-функции и
peer-вызовы остаются в [TODO](../../../TODO.md); целевое
поведение документа не означает текущую production readiness. Все незавершённые
пункты относятся к общему v2; `contracts/v1` — версия собственного контракта
плагина, не обещание релиза Liapoldus v1.
