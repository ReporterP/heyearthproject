# P3.1 — PDF Счёт + Акт для B2B

> Beads: `heyearthproject-dyrc`. Brainstorm: 2026-05-21.
> Контекст: [docs/research/b2b-map.md](../../research/b2b-map.md).

## Цель

Закрыть блокер бухгалтерии B2B-клиентов: после создания B2B-счёта клиент получает
PDF «Счёт на оплату», после фактического поступления оплаты — PDF «Акт оказания услуг».
Оба документа приходят на e-mail вложением и доступны для повторного скачивания
из портала `/business/dashboard`.

## Не-цели

- НДС, счёт-фактура, УПД (HiEarth = ИП на УСН без НДС).
- Печать (М.П.) — оставлено как место в layout, но изображение не используется.
- Облачное хранилище (S3) — позже, если объёмы вырастут.
- Шаблонизатор HTML — layout кодом в gofpdf.
- Ежемесячный сводный акт — 1 платёж = 1 акт.

## Архитектура

Новый пакет `server/backend/internal/docgen`:

```
docgen/
  fonts/                       (go:embed *.ttf — DejaVu Sans Regular + Bold)
  base.go                      common helpers: header, footer, rubles-in-words, money fmt
  invoice.go                   func GenerateInvoice(InvoiceData) (path, error)
  act.go                       func GenerateAct(ActData) (path, error)
  storage.go                   path helpers, dir mkdir, idempotent existence check
```

Сопутствующие функции в `internal/biz/` (новые):
- `biz.LoadRequisites() (docgen.Requisites, error)` — читает все `legal_*` ключи из `app_settings` одним запросом, возвращает структуру.
- `biz.CreatePaymentAct(tenantID, paymentID int, amount float64) (actID int, actNumber string, err error)` — вставка в `payment_acts`, генерация номера `{YYYY}-{tenant_id}-{seq}` (seq = autoincrement id).

Публичный API пакета `docgen`:

```go
type Requisites struct {
    CompanyName, INN, OGRNIP, Address, Phone, Email string
    BankName, BIK, Account, CorrAccount string
    SignaturePath string  // optional, "" → текст
}

type Party struct {
    LegalName, INN, KPP, Address string
}

type LineItem struct {
    Name string
    Qty float64
    Unit string  // "услуга", "мес." …
    Price float64
}

type InvoiceData struct {
    Number string
    Date time.Time
    Supplier Requisites
    Buyer Party
    Items []LineItem
    Comment string
}

type ActData struct {
    Number string
    Date time.Time
    InvoiceNumber string         // ссылка на оригинальный счёт
    InvoiceDate time.Time
    Supplier Requisites
    Buyer Party
    Items []LineItem
}

func GenerateInvoice(data InvoiceData) (filePath string, err error)
func GenerateAct(data ActData) (filePath string, err error)
```

Обе функции:
1. Считают `filePath` по нумерации (см. ниже).
2. Если файл уже существует — возвращают путь (идемпотентность).
3. Иначе строят PDF gofpdf'ом, пишут в `*.tmp`, атомарно `rename`.
4. Возвращают абсолютный путь.

## Точки вызова

**Счёт** — в `handleCreateInvoice` (`cmd/api/invoice_handlers.go`)
сразу после `INSERT INTO invoice_records`:

```go
reqs, _ := biz.LoadRequisites()                  // из app_settings
path, err := docgen.GenerateInvoice(docgen.InvoiceData{
    Number: fmt.Sprintf("%04d-%d-%d", date.Year(), tenantID, invoiceRecordID),
    ...
})
if err == nil {
    db.Exec("UPDATE invoice_records SET document_path=$1 WHERE id=$2", path, invoiceRecordID)
}
// best-effort: ошибку логируем, но не блокируем создание счёта
```

E-mail с прикреплённым счётом — отдельная функция `sendTenantInvoiceIssuedEmail`
(создаётся в этой задаче), вызывается из того же handler'а.

**Акт** — в `processPaymentEvent` (`cmd/api/payment_handlers.go`) для
`kind IN ('b2b_invoice', 'tenant_topup')`, сразу после `CreditTenantWallet`:

```go
actID, err := biz.CreatePaymentAct(tenantID, paymentID, amount)
if err == nil {
    path, err2 := docgen.GenerateAct(docgen.ActData{...})
    if err2 == nil {
        db.Exec("UPDATE payment_acts SET act_path=$1 WHERE id=$2", path, actID)
    }
}
// акт пристёгивается к существующему sendTenantCreditEmail
```

Для `b2b_invoice` — также `UPDATE invoice_records SET act_path=…` (связь по
payment_id из metadata).

## Схема БД

Миграция `000039_b2b_pdf_documents.up.sql` + down:

```sql
-- Расширение app_settings: банковские реквизиты + подпись
INSERT INTO app_settings (key, value) VALUES
    ('legal_ogrnip', ''),
    ('legal_bank_name', ''),
    ('legal_bank_bik', ''),
    ('legal_bank_account', ''),
    ('legal_bank_corr_account', ''),
    ('legal_signature_path', '')
ON CONFLICT (key) DO NOTHING;

-- Поле для акта в денормализованной истории счетов
ALTER TABLE invoice_records
    ADD COLUMN IF NOT EXISTS act_path TEXT;

-- Акт для платежей tenant_topup, где нет invoice_records
CREATE TABLE IF NOT EXISTS payment_acts (
    id SERIAL PRIMARY KEY,
    payment_id INT NOT NULL UNIQUE REFERENCES payments(id) ON DELETE CASCADE,
    tenant_id INT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    act_number TEXT NOT NULL,
    act_path TEXT,
    amount NUMERIC(12,2) NOT NULL,
    issued_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_payment_acts_tenant ON payment_acts(tenant_id);
```

Down — `DROP TABLE payment_acts`, `ALTER … DROP COLUMN act_path`,
`DELETE FROM app_settings WHERE key LIKE 'legal_bank_%'`.

## Хранилище

Корень: `/opt/heyearth/invoices/` (создаётся в `cmd/deploy` `--main`, owner
`heyearth:heyearth`, chmod 750).

Структура:
```
/opt/heyearth/invoices/
  {tenant_id}/
    {YYYY}/
      invoice-{YYYY}-{tenant_id}-{invoice_record_id}.pdf
      act-{YYYY}-{tenant_id}-{payment_act_id}.pdf
```

Файлы chmod 640, owner `heyearth`. Backup — текущая стратегия `cmd/deploy --backup`
расширяется на этот каталог (отдельная подзадача в плане).

Подпись: `/opt/heyearth/assets/signature.png` (приватный). Upload через админку
многочастной формой, см. ниже.

## Endpoints

### Новые

1. `GET /api/v1/business/invoices/{id}/document?type=invoice|act`
   - Auth: JWT user + проверка `tenant_member(invoice.tenant_id, user_id)`.
   - Роли: любой member может скачать (`user` включительно — нужно подтверждение оплаты).
   - Ответ: `application/pdf`, `Content-Disposition: inline; filename="…"`.
   - 404 если запись есть, а файла нет → fallback regenerate, иначе 500.

2. `POST /api/admin/settings/upload-signature`
   - Auth: admin + permission `settings.write`.
   - Multipart `signature` (PNG only, ≤ 2 MB, ≥ 200×100 px).
   - Сохраняет в `/opt/heyearth/assets/signature.png`, пишет путь в
     `app_settings.legal_signature_path`.

3. `GET /api/admin/payment-acts?tenant_id=&from=&to=` (опционально, для админки)
   - Список актов, пагинация. Для P6.4 (1С) будет полезно.

### Изменения существующих

- `GET /api/v1/business/me` — добавить `documents.invoice_path_available` /
  `documents.act_path_available` булевым в каждый invoice/transaction. Frontend
  показывает кнопку «Скачать PDF».

## Шрифты

DejaVu Sans Book + DejaVu Sans Bold (`.ttf`, freely licensed). Кладутся
в `internal/docgen/fonts/`, читаются через `//go:embed *.ttf` и регистрируются
`pdf.AddUTF8FontFromBytes`. Размер +~700 KB к бинарю (приемлемо).

Если позже понадобятся Italic / монокод — добавляются тем же путём.

## Layout

### Счёт на оплату

A4 portrait, поля 15 mm.

```
┌────────────────────────────────────────────────────────┐
│  Образец платёжного поручения [мини-схема банка]       │
│  ─────────────────────────────────────────────────     │
│  Поставщик:  ИП Степанов А.В., ИНН …, ОГРНИП …         │
│              Адрес: …                                  │
│              Тел: …  E-mail: …                         │
│  Банк:       … БИК … р/с … к/с …                       │
├────────────────────────────────────────────────────────┤
│           СЧЁТ № 2026-5-127 от 21.05.2026             │
├────────────────────────────────────────────────────────┤
│  Заказчик:   ООО «Ромашка», ИНН 7707083893             │
│              Адрес: …                                  │
├────────────────────────────────────────────────────────┤
│  № │ Наименование              │ Кол-во │ Ед. │ Цена │ Сумма │
│  1 │ Услуги по предоставлению  │   1    │ усл.│  …   │  …    │
│    │ защищённого канала связи  │        │     │      │       │
├────────────────────────────────────────────────────────┤
│                                       Итого:    XXX ₽  │
│                                       НДС не облагается│
│                                       (УСН доходы)     │
│                                                        │
│  Сумма прописью: Двадцать тысяч рублей 00 копеек      │
│                                                        │
│  Поставщик:  [подпись.png или "Степанов А.В."] МП     │
│                                                        │
│  Счёт действителен 5 банковских дней.                  │
└────────────────────────────────────────────────────────┘
```

### Акт

Тот же header. Тело:

```
                АКТ № 2026-5-198 от 25.05.2026
       об оказании услуг по счёту № 2026-5-127 от 21.05.2026

Исполнитель:  ИП Степанов А.В. …
Заказчик:     ООО «Ромашка» …

Исполнитель оказал, а Заказчик принял следующие услуги:

│ № │ Наименование │ Кол-во │ Ед. │ Цена │ Сумма │
│ 1 │ …            │  1     │ усл.│  …   │  …    │

                                       Итого: XXX ₽
                                       НДС не облагается.

Услуги оказаны в полном объёме, в установленный срок и с надлежащим
качеством. Стороны претензий не имеют.

Исполнитель                              Заказчик
[подпись]                                ________________________
ИП Степанов А.В.                         (М.П., подпись, расшифровка)
```

## Email вложения

Расширение `internal/email.Message`:

```go
type Attachment struct {
    Filename string
    Content  []byte
    MimeType string
}
type Message struct {
    To, Subject, Body string
    Attachments []Attachment
}
```

`doSend` — MIME `multipart/mixed`, в части `text/plain` (или `text/html`)
основное тело, в `application/pdf` base64-кодированные вложения с
`Content-Disposition: attachment; filename="…"`.

Шаблон письма «Счёт выставлен»:
- Subject: «Счёт № {number} от HiEarth»
- Body: ссылка на оплату YooKassa + reminder что во вложении PDF
- Attachment: invoice PDF

Шаблон «Оплата получена» (`sendTenantCreditEmail`) — добавить attachment с актом.

## Sum-in-words

Простая реализация в `docgen/base.go` (отдельная функция `rublesInWords(amount float64) string`).
Не тащим внешний пакет — кириллица + рубли + копейки укладывается в ~150 строк
табличной логики. Тестируется тремя кейсами: 1, 1234, 1000000.

## Error handling

| Ситуация | Поведение |
|---|---|
| `docgen.Generate…` вернул ошибку | ERROR в лог, e-mail уходит без вложения, в теле строка «PDF будет доступен в портале» |
| Реквизиты неполные (`legal_bank_*` пустые) | `handleCreateInvoice` возвращает 503 с понятным сообщением «Реквизиты компании не настроены. Свяжитесь с поддержкой.» |
| Подпись не загружена (`legal_signature_path=""`) | Layout рендерит текстовую подпись |
| Конкурентная генерация одного PDF | Файл-маркер не нужен — функция идемпотентна: пишем во временный файл, потом atomic rename; если файл уже есть, второй вызов сразу видит и возвращает |
| Скачивание чужого документа | 403 (проверка `tenant_member`) |
| Документ есть в БД, файла на диске нет | Регенерация на лету в handler'е (fallback), ошибка → 500 с инструкцией |

## Frontend changes

`site/src/pages/BusinessDashboardPage.jsx` (минимум для P3.1):
- В блоке «История транзакций» рядом с каждой `direction='credit'` записью, у которой есть связанный `payment_acts.act_path` (приходит в `documents.act_available` из `/api/v1/business/me`), — кнопка-иконка «Скачать акт» → `/api/v1/business/invoices/{record_id}/document?type=act`.
- Полноценная страница «История счетов» — отложена на P4.1.

`admin_web/src/pages/SettingsPage.jsx`:
- Новые карточки в секции «Юридические сведения»: ОГРНИП, Банк, БИК, р/с, к/с.
- Новая карточка «Подпись» — upload PNG (preview + remove).

`site/src/pages/BusinessInvoicePage.jsx`:
- На success-экране показываем ссылку «Скачать счёт PDF» (после успешного `POST /api/v1/business/create-invoice` сервер возвращает `invoice_record_id` и `document_url`).

## Verification (без unit-тестов)

В порядке готовности:

1. `go build ./...` — компиляция docgen + всех правок.
2. Локально на dev: `psql` миграция применилась чисто, `\d invoice_records`,
   `\d payment_acts` показывают новые поля.
3. Через admin UI заполнить `legal_bank_*` тестовыми значениями, upload подписи.
4. Создать тестовый счёт через `/business/invoice` (INN валидный, тестовая компания).
5. Проверить:
   - Файл создан: `ls /opt/heyearth/invoices/{tid}/2026/invoice-*.pdf`
   - PDF открывается, кириллица читается, реквизиты заполнены, сумма прописью корректна.
   - E-mail приходит на адрес с вложением.
6. Оплатить тестовым YooKassa payment method (demo card).
7. Проверить:
   - `payment_acts` запись создана, `act_path` заполнен.
   - PDF акта в storage, открывается.
   - E-mail «Оплата получена» содержит вложение с актом.
8. `curl -H 'Authorization: Bearer ...' .../api/v1/business/invoices/127/document?type=invoice` → 200 + PDF.
9. С чужим JWT — 403. Без JWT — 401.
10. После выкладки на prod (`cmd/deploy --update-main`) — `--status` зелёный, smoke-тест через тестовый tenant.

## Migration plan

Phased rollout:

- **P3.1.A — Schema + storage skeleton.** Миграция 000039, `docgen` пакет с no-op
  заглушками, директории на диске. Релиз. Безопасно — нигде не вызывается.
- **P3.1.B — Generation + email attachments.** Реализация PDF, расширение
  `email.Message`, точки вызова. Релиз. На этом этапе клиенты начинают
  получать письма с PDF.
- **P3.1.C — Portal endpoints + UI.** `GET …/document`, кнопки в портале и
  админке, upload подписи. Релиз.
- **P3.1.D — Backup integration.** `cmd/deploy --backup` пакует `/opt/heyearth/invoices/`.

Каждая под-фаза — отдельный коммит и `deploy --update-main`. На каждом этапе
работает то, что было раньше.

## Open questions

(Закрытые в брейнштурме: тип документа, технология, хранилище, набор реквизитов, частота акта.)

- Поле «Адрес заказчика» в счёте: брать из DaData в P3.2, до того — необязательное (пустая строка). Счёт без адреса заказчика юридически валиден.
- Нумерация счетов: сейчас предложено `{YYYY}-{tenant_id}-{seq}`. Бухгалтерия некоторых клиентов требует сквозную нумерацию. Если возникнет — добавим `invoice_serial` SEQUENCE отдельно. Сейчас не делаем.
- Подписание e-mail (DKIM/DMARC) — уже работает на Mailcow, отдельной задачи нет.

## Ссылки

- Beads: `heyearthproject-dyrc`
- B2B map: `docs/research/b2b-map.md`
- gofpdf: https://github.com/jung-kurt/gofpdf (форк `go-pdf/fpdf` поддерживается)
- DejaVu fonts: https://dejavu-fonts.github.io/ (Bitstream Vera Public License)
