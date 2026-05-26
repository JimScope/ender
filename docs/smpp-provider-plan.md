# Plan de integración: SMPP / Short Code Center en Vendel

> **Audiencia**: agente de implementación (Claude Code, Cursor, etc.) y revisores humanos.
> **Estado**: listo para implementar después de revisar las variables reales del SMSC.
> **Principio guía**: KISS. Soportar un único SMSC/short code center global primero; no diseñar multi-tenant SMPP hasta que haya un requisito real.

---

## 1. Resumen ejecutivo

Añadir SMPP como proveedor alternativo de envío SMS en Vendel para interactuar con un SMSC o short code center. SMPP no se comporta como AWS AEUM: no es un request HTTP aislado, sino una sesión TCP persistente con bind, keepalive, reconexión, control de ventana, delivery receipts (DLR) y, opcionalmente, mensajes entrantes mobile-originated (MO).

- **Proveedor**: `smpp` representado como `sms_devices.device_type="smpp"`.
- **Conexión**: global del operador, configurada por env vars.
- **Modo inicial**: un único bind `transceiver` global, suficiente para enviar MT, recibir DLR y recibir MO si el SMSC lo habilita.
- **Cuota**: compartida con devices físicos y AEUM, pasa por `CheckSMSQuota()` e `IncrementSMSCount()`.
- **Persistencia**: reutilizar `sms_messages.provider_message_id` para el `message_id` devuelto por el SMSC.
- **UI**: device virtual read-only en `/devices`, seleccionable al enviar SMS.
- **Plan hermano**: AWS End User Messaging vive en `docs/aws-end-user-messaging-plan.md`.

---

## 2. Decisiones congeladas

| Tema | Decisión |
|---|---|
| Biblioteca Go | Usar `github.com/fiorix/go-smpp/v2/smpp` salvo que al implementar se detecte bloqueo real. Verificar API exacta en código antes de escribir tests. |
| Bind | `transceiver` por defecto para soportar MT + DLR + MO en una sola conexión. |
| Credenciales | Globales por env vars. No credenciales por usuario en este plan. |
| Persistencia | Extender `sms_devices.device_type` con `"smpp"`. No crear `sms_provider_connections` todavía. |
| Device virtual | Un único `sms_devices` global con `device_type="smpp"` y `user=""`. |
| Selección | Si `device_id` apunta a SMPP, gana. Si no hay devices físicos y SMPP está configurado, fallback a SMPP. |
| Estado inicial | `Send()` exitoso marca `sms_messages.status="sent"`; DLR puede cambiar a `delivered` o `failed`. |
| Incoming MO | Soportado en MVP si el bind transceiver recibe `deliver_sm` con texto no-DLR. Se guarda como `message_type="incoming"` y dispara `sms_received`. |
| Reconnect | Sí, con loop de conexión y backoff simple. No circuit breaker configurable en este plan. |
| Multiples SMSC / rutas | Fuera de alcance. Un solo SMSC global. |

---

## 3. Arquitectura actual relevante

La arquitectura base es la misma del plan AEUM:

- `backend/services/sms.go`: `SendSMS()`, `resolveDevices()`, `createMessageRecords()`, `ProcessSMSAck()`.
- `backend/services/notification.go`: `DispatchMessages()` para Android/FCM y módems. **No mezclar SMPP aquí**.
- `backend/services/webhook.go`: `TriggerWebhooks(app, userId, message, event)`.
- `backend/hooks/devices.go`: actualmente aplica cuota/API key a todo `sms_devices`; debe saltar virtual providers (`aws_aeum`, `smpp`).
- `sms_messages.to`: destinatario del SMS saliente.
- `sms_messages.provider_message_id`: debe existir para relacionar DLR del SMSC con mensajes internos. Si aún no existe, esta integración lo crea.

**Regla de paquetes**:
- Providers externos viven en `backend/services/smsprovider` (`package smsprovider`).
- Orquestación, BD, cuotas y webhooks viven en `backend/services` (`package services`).
- `smsprovider` no importa `vendel/services`, para evitar ciclos.

---

## 4. Principios KISS aplicados

| Tentación | Decisión |
|---|---|
| Crear tabla `sms_provider_connections` desde el inicio | NO. Un único SMPP global usa env vars y device virtual. |
| Ruteo por país/prefijo/cliente | NO. Fallback simple igual que AEUM. |
| Múltiples binds transmitter/receiver | NO en MVP. Usar `transceiver`. |
| Worker pool complejo | NO. Usar el mecanismo de ventana/concurrencia de la librería SMPP cuando aplique. |
| SMPP por usuario | NO. Se diseña con `UserID` en `SendRequest`, pero no se implementan credenciales por usuario. |
| UI de configuración SMPP | NO. Todo por env vars. |
| Tabla de DLR crudos | NO. Log estructurado primero; guardar solo estado final en `sms_messages`. |
| Concatenación UDH/manual | NO en MVP. Enviar texto simple; segmentación avanzada queda para una fase posterior si el SMSC no la maneja. |

---

## 5. Abstracción de providers

### 5.1 Interface base

Si `backend/services/smsprovider/provider.go` ya existe por AEUM, reutilizarlo. Si no existe, crearlo:

```go
package smsprovider

import "context"

type Provider interface {
    Name() string
    IsConfigured() bool
    Send(ctx context.Context, req SendRequest) (*SendResult, error)
}

type SendRequest struct {
    MessageID string
    UserID    string
    To        string
    Body      string
    ChannelHint string // opcional; SMPP lo ignora en MVP
}

type SendResult struct {
    ProviderMessageID string
    Status            string // "sent" | "failed"
    ErrorMessage      string
    ProviderChannel   string // para SMPP: "sms"
    OriginationIdentity string // short code/source_addr usado
}
```

### 5.2 Interfaces opcionales para SMPP

SMPP necesita un provider vivo. Añadir estas interfaces en `backend/services/smsprovider/managed.go`:

```go
package smsprovider

import "context"

type ManagedProvider interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
}

type DeliveryReporter interface {
    Reports() <-chan DeliveryReport
}

type IncomingReceiver interface {
    Incoming() <-chan IncomingMessage
}

type DeliveryReport struct {
    ProviderMessageID string
    Status            string // "delivered" | "failed"
    ErrorMessage      string
    RawStatus         string
}

type IncomingMessage struct {
    From string
    To   string
    Body string
}
```

### 5.3 Responsabilidad de cada paquete

`smsprovider.SMPPProvider`:
- Carga config.
- Mantiene conexión SMPP.
- Hace bind/reconnect.
- Envía `submit_sm`.
- Publica DLR por canal `Reports()`.
- Publica MO por canal `Incoming()`.
- No toca PocketBase.
- No dispara webhooks.
- No incrementa cuotas.

`services`:
- Selecciona device/provider.
- Crea `sms_messages`.
- Llama `DispatchProviderMessages()`.
- Persiste `provider_message_id`.
- Consume DLR/MO y actualiza BD.
- Dispara `TriggerWebhooks()`.

---

## 6. Modelo de datos

### 6.1 Migración

**File**: `backend/migrations/<timestamp>_smpp_provider.go`

Cambios:

1. En `sms_devices.user`: si sigue `Required: true`, cambiar a `Required: false`. La obligatoriedad para devices físicos se valida por hook.
2. En `sms_devices.device_type`: añadir `"smpp"` manteniendo `"android"`, `"modem"` y cualquier otro provider ya existente (`"aws_aeum"`).
3. Actualizar reglas de `sms_devices`:
   - `ListRule`: `user = @request.auth.id || device_type = "aws_aeum" || device_type = "smpp"`
   - `ViewRule`: igual que ListRule.
   - `CreateRule`: `user = @request.auth.id`
   - `UpdateRule`: `user = @request.auth.id && device_type != "aws_aeum" && device_type != "smpp"`
   - `DeleteRule`: igual que UpdateRule.
4. En `sms_messages`: si no existe, añadir `provider_message_id` (TextField opcional).
5. En `sms_messages`: si no existe, añadir `provider_channel` (TextField opcional). Para SMPP usar `"sms"`.
6. En `sms_messages`: si no existe, añadir `provider_origination_identity` (TextField opcional). Para SMPP usar `SMPP_SOURCE_ADDR`.
7. Si no existe, crear índice no único `idx_sms_messages_provider_message_id` sobre `provider_message_id`.

### 6.2 Hook de devices

Modificar `backend/hooks/devices.go` para tratar providers virtuales:

```go
func isVirtualProviderDevice(record *core.Record) bool {
    switch record.GetString("device_type") {
    case "aws_aeum", "smpp":
        return true
    default:
        return false
    }
}
```

Reglas:
- `OnRecordCreateRequest`: rechazar `device_type="smpp"` desde API pública con 403. Solo bootstrap server-side puede crearlo.
- `OnRecordCreate`: si es virtual provider, saltar quota, device_count y api_key.
- `OnRecordCreate`: si no es virtual provider y `user==""`, devolver 400.
- `OnRecordUpdate`/`OnRecordDelete`: rechazar virtual providers para cubrir admin/superuser path.
- `OnRecordDelete`: no decrementar `device_count` para virtual providers.

---

## 7. Configuración

### 7.1 Variables de entorno

| Variable | Obligatoria si SMPP habilitado | Default | Descripción |
|---|---|---|---|
| `SMPP_ENABLED` | sí | `false` | Habilita el provider SMPP. |
| `SMPP_ADDR` | sí | — | Host:puerto del SMSC, ej. `smsc.example.com:2775`. |
| `SMPP_SYSTEM_ID` | sí | — | Usuario/system_id del bind. |
| `SMPP_PASSWORD` | sí | — | Password del bind. |
| `SMPP_SYSTEM_TYPE` | no | `""` | System type requerido por algunos SMSC. |
| `SMPP_SOURCE_ADDR` | sí | — | Short code/originator. |
| `SMPP_SOURCE_TON` | no | `3` | TON origen. Para short code suele ser `3` (Network Specific), confirmar con SMSC. |
| `SMPP_SOURCE_NPI` | no | `0` | NPI origen. Confirmar con SMSC. |
| `SMPP_DEST_TON` | no | `1` | TON destino. Para MSISDN internacional suele ser `1`. |
| `SMPP_DEST_NPI` | no | `1` | NPI destino. Para E.164 suele ser `1`. |
| `SMPP_BIND_TYPE` | no | `transceiver` | En MVP usar `transceiver`. |
| `SMPP_ENQUIRE_LINK_INTERVAL` | no | `30s` | Keepalive SMPP. |
| `SMPP_RECONNECT_INITIAL_DELAY` | no | `2s` | Backoff inicial de reconexión. |
| `SMPP_RECONNECT_MAX_DELAY` | no | `60s` | Backoff máximo. |
| `SMPP_WINDOW_SIZE` | no | `10` | Máximo de submits concurrentes/in-flight si la librería lo soporta. |
| `SMPP_DEVICE_NAME` | no | `"SMPP Short Code"` | Nombre visible del device virtual. |

### 7.2 `.env.example`

Agregar las variables con comentarios. No poner secretos reales.

Al terminar la implementación, actualizar también:
- `.env` local del repo, si existe, con placeholders/comentarios seguros para SMPP.
- `.env.example` con todas las variables SMPP.
- `CLAUDE.md` con el patrón `smsprovider`, `ManagedProvider`, DLR/MO y `device_type="smpp"`.
- `README.md` con configuración SMPP, variables, TON/NPI, DLR, MO, lifecycle/reconnect y límites del MVP.

---

## 8. Diseño del `SMPPProvider`

### 8.1 Archivo `backend/services/smsprovider/smpp_config.go`

```go
type SMPPConfig struct {
    Enabled              bool
    Addr                 string
    SystemID             string
    Password             string
    SystemType           string
    SourceAddr           string
    SourceTON            uint8
    SourceNPI            uint8
    DestTON              uint8
    DestNPI              uint8
    EnquireLinkInterval  time.Duration
    ReconnectInitialDelay time.Duration
    ReconnectMaxDelay     time.Duration
    WindowSize           int
}

func LoadSMPPConfigFromEnv() SMPPConfig { ... }
func (c SMPPConfig) IsConfigured() bool {
    return c.Enabled && c.Addr != "" && c.SystemID != "" && c.Password != "" && c.SourceAddr != ""
}
```

### 8.2 Archivo `backend/services/smsprovider/smpp_provider.go`

Estructura sugerida:

```go
type SMPPProvider struct {
    cfg      SMPPConfig
    tx       submitter // interface local para mockear
    reports  chan DeliveryReport
    incoming chan IncomingMessage
    ready    atomic.Bool
    stop     context.CancelFunc
}

func NewSMPPProvider(cfg SMPPConfig) *SMPPProvider { ... }
func (p *SMPPProvider) Name() string { return "smpp" }
func (p *SMPPProvider) IsConfigured() bool { return p.cfg.IsConfigured() }
func (p *SMPPProvider) Reports() <-chan DeliveryReport { return p.reports }
func (p *SMPPProvider) Incoming() <-chan IncomingMessage { return p.incoming }
```

`Send()`:

```go
func (p *SMPPProvider) Send(ctx context.Context, req SendRequest) (*SendResult, error) {
    if !p.IsConfigured() {
        return &SendResult{Status: "failed", ErrorMessage: "SMPP is not configured"}, nil
    }
    if !p.ready.Load() {
        return &SendResult{Status: "failed", ErrorMessage: "SMPP bind is not ready"}, nil
    }

    id, err := p.tx.Submit(ctx, SubmitRequest{
        SourceAddr: p.cfg.SourceAddr,
        DestAddr:   req.To,
        Body:       req.Body,
        SourceTON:  p.cfg.SourceTON,
        SourceNPI:  p.cfg.SourceNPI,
        DestTON:    p.cfg.DestTON,
        DestNPI:    p.cfg.DestNPI,
    })
    if err != nil {
        return &SendResult{Status: "failed", ErrorMessage: simplifySMPPError(err)}, nil
    }
    return &SendResult{
        ProviderMessageID: id,
        Status: "sent",
        ProviderChannel: "sms",
        OriginationIdentity: p.cfg.SourceAddr,
    }, nil
}
```

### 8.3 Interfaces de test

No testear contra un SMSC real. Crear interfaces locales:

```go
type submitter interface {
    Submit(ctx context.Context, req SubmitRequest) (messageID string, err error)
}

type SubmitRequest struct {
    SourceAddr string
    DestAddr   string
    Body       string
    SourceTON  uint8
    SourceNPI  uint8
    DestTON    uint8
    DestNPI    uint8
}
```

Luego adaptar `go-smpp` detrás de esa interface en un archivo pequeño (`smpp_client.go`). Los tests mockean `submitter`.

---

## 9. Bootstrap y lifecycle

### 9.1 Crear device virtual

**File**: `backend/services/smsprovider/setup_smpp.go`

```go
func EnsureSMPPDevice(app core.App) error { ... }
```

Comportamiento:
- Si `SMPP_ENABLED != true`: no hace nada.
- Si ya existe `sms_devices.device_type="smpp"`: no hace nada.
- Si no existe: crea device global:
  - `name`: `SMPP_DEVICE_NAME` o `"SMPP Short Code"`.
  - `phone_number`: `SMPP_SOURCE_ADDR`.
  - `user`: `""`.
  - `device_type`: `"smpp"`.

### 9.2 Arranque en `main.go`

En `OnServe`:

```go
smppProvider := smsprovider.NewSMPPProvider(smsprovider.LoadSMPPConfigFromEnv())
if smppProvider.IsConfigured() {
    if err := smsprovider.EnsureSMPPDevice(se.App); err != nil { return err }
    services.StartManagedProvider(se.App, smppProvider)
    services.StartProviderReportConsumer(se.App, smppProvider)
    services.StartProviderIncomingConsumer(se.App, smppProvider)
}
```

Mantener `main.go` fino: implementar `StartManagedProvider` en `backend/services/provider_lifecycle.go`. Ese helper crea un `context.WithCancel(context.Background())`, llama `provider.Start(ctx)` en goroutine si la implementación bloquea, y guarda/cierra el cancelador si PocketBase expone un hook de shutdown usable.

### 9.3 Shutdown

Si PocketBase expone hook de shutdown usable en este repo, llamar `smppProvider.Stop(ctx)`. Si no, `Start()` debe terminar al cancelarse el contexto raíz.

---

## 10. Integración con `SendSMS()`

### 10.0 Prerrequisito: refactor de `resolveDevices()` (HACER ANTES de codear SMPP)

Cuando AEUM entró al codebase se dejó `backend/services/sms.go::resolveDevices()` con una rama hardcoded `if device_type == "aws_aeum"`. Si se añade SMPP sin tocar esa función, la rama se duplica:

```go
if device.device_type == "aws_aeum" { ... }
if device.device_type == "smpp"    { ... }   // duplicado
```

El registry y `partitionByProvider` ya están generalizados (`backend/services/smsprovider/registry.go` + `backend/services/sms_provider_dispatch.go`). Falta cerrar el último call-site.

**Refactor a aplicar antes de empezar SMPP**:

1. Cambiar la firma de `resolveDevices` para aceptar un acceso genérico al registry. Una de:
   - `func resolveDevices(app core.App, userId, deviceId string) ([]*core.Record, error)` — interna; lee directamente del registry `smsprovider.Get(...)`.
   - O pasar una `ProviderRegistry` como dependencia inyectada si se quiere testear con mocks.
2. Reemplazar las dos ramas hardcoded por consultas al registry:
   ```go
   if p := smsprovider.Get(device.GetString("device_type")); p != nil {
       if !p.IsConfigured() {
           return nil, fmt.Errorf("%s is not configured", p.Name())
       }
       return []*core.Record{device}, nil
   }
   ```
3. En el fallback (sin `device_id`), iterar el registry y devolver el primer device virtual cuyo provider esté configurado. Si querés un orden estable, usá una lista `[]string{"aws_aeum","smpp"}` declarada al lado del registry.
4. Borrar el parámetro `aeumProvider smsprovider.Provider` y la lógica AEUM-específica.

Esto es ~30 líneas, autocontenido, debe ir como commit propio antes del primer commit SMPP.

### 10.1 Selección de device/provider

Actualizar `resolveDevices()` para aceptar providers externos:

```go
func resolveDevices(app core.App, userId, deviceId string, providers ProviderRegistry) ([]*core.Record, error)
```

Interface mínima del registry:

```go
type ProviderRegistry interface {
    Get(name string) smsprovider.Provider
    FallbackOrder() []string
}
```

KISS si solo están AEUM y SMPP:

```
if device_id != "":
  device = FindRecordById(device_id)
  if device_type in ["aws_aeum", "smpp"]:
    provider = registry.Get(device_type)
    if provider == nil || !provider.IsConfigured(): error
    return [device]
  require device.user == userId
  return [device]

physical = devices del user con fcm_token != '' o type='modem'
if len(physical) > 0: return physical

for provider type in fallback order ["aws_aeum", "smpp"]:
  if provider configured and virtual device exists: return [device]

return nil
```

**Orden de fallback recomendado**:
1. Devices físicos del usuario.
2. AEUM si existe y está configurado.
3. SMPP si existe y está configurado.

Si no se implementa AEUM en esta rama, SMPP queda como único fallback externo.

### 10.2 Dispatch

Reutilizar `DispatchProviderMessages()` de AEUM si ya existe. Si no existe, crearlo en `backend/services/sms_provider_dispatch.go`.

Debe:
- Construir `smsprovider.SendRequest` con `To: m.GetString("to")`.
- **`Body`: usar `services.GetRecordBody(m)`, NO `m.GetString("body")`.** El body se persiste cifrado at-rest (prefijo `fenc:`) por los hooks de encryption. Sin desencriptar, el provider recibe `fenc:...` y eso se envía como texto cifrado al destinatario. `GetRecordBody` (definido en `services/bodyencrypt.go`) maneja la desencriptación y cae al raw value si el row predates la migración de encryption. Este bug se introdujo en la implementación inicial AEUM y se corrigió retroactivamente; aplica igual a SMPP.
- Llamar `provider.Send()`.
- Persistir `provider_message_id` antes de disparar webhooks.
- Persistir `provider_channel` y `provider_origination_identity` si el provider los devuelve.
- Setear `sent_at` cuando `Status=="sent"`.
- Setear `error_message` cuando `Status=="failed"`.
- Disparar `TriggerWebhooks(app, userId, m, "sms_sent"|"sms_failed")`.

---

## 11. Delivery receipts (DLR)

### 11.1 Normalización

Crear helper en `backend/services/provider_reports.go`:

```go
func ApplyProviderDeliveryReport(app core.App, report smsprovider.DeliveryReport) error {
    msg, err := app.FindFirstRecordByFilter(
        "sms_messages",
        "provider_message_id = {:id}",
        dbx.Params{"id": report.ProviderMessageID},
    )
    if err != nil {
        app.Logger().Warn("provider DLR without matching message", "provider_message_id", report.ProviderMessageID)
        return nil
    }

    switch report.Status {
    case "delivered":
        msg.Set("status", "delivered")
        msg.Set("delivered_at", types.NowDateTime())
        app.Save(msg)
        TriggerWebhooks(app, msg.GetString("user"), msg, "sms_delivered")
    case "failed":
        msg.Set("status", "failed")
        msg.Set("error_message", report.ErrorMessage)
        app.Save(msg)
        TriggerWebhooks(app, msg.GetString("user"), msg, "sms_failed")
    }
    return nil
}
```

El webhook de AEUM puede refactorizarse después para reutilizar este helper, pero no bloquear la implementación SMPP en ese refactor.

### 11.2 Mapeo SMPP DLR

Parsear estados típicos de receipts:

| SMPP stat | Vendel status | Webhook |
|---|---|---|
| `DELIVRD` | `delivered` | `sms_delivered` |
| `EXPIRED` | `failed` | `sms_failed` |
| `DELETED` | `failed` | `sms_failed` |
| `UNDELIV` | `failed` | `sms_failed` |
| `REJECTD` | `failed` | `sms_failed` |
| `UNKNOWN` | `failed` | `sms_failed` |
| `ACCEPTD` | ignorar | ninguno |
| `ENROUTE` | ignorar | ninguno |

Si el SMSC usa formato propio, añadir parser tolerante y loggear el receipt crudo en debug/warn sin persistirlo.

---

## 12. Incoming SMS (MO)

Si el SMSC entrega MO por `deliver_sm`:

1. Detectar si el `deliver_sm` es DLR o MO.
2. Si es MO, publicar `smsprovider.IncomingMessage`.
3. Consumidor en `services` crea `sms_messages`:
   - `user`: decidir owner. En MVP, usar `SMPP_OWNER_USER_ID` si se define. Si no se define, no habilitar MO y loggear error claro.
   - `device`: id del device virtual `smpp`.
   - `to`: short code (`SMPP_SOURCE_ADDR` o `IncomingMessage.To`).
   - `from_number`: `IncomingMessage.From`.
   - `body`: texto.
   - `status`: `received`.
   - `message_type`: `incoming`.
   - `webhook_sent`: `false`.
4. Disparar `TriggerWebhooks(app, ownerUserID, record, "sms_received")`.

**Variable adicional para MO**:
- `SMPP_OWNER_USER_ID`: user que recibirá incoming MO y cuyos webhooks se disparan. Obligatoria si `SMPP_ENABLE_MO=true`.
- `SMPP_ENABLE_MO`: default `false`.

KISS: si no hay owner, no guardar MO. Evita crear mensajes sin usuario.

---

## 13. Frontend

Archivos:
- `frontend/src/types/collections.ts`: `DeviceType = "android" | "modem" | "aws_aeum" | "smpp"` (mantener valores existentes según rama).
- `frontend/src/components/Devices/columns.tsx`: icono `RadioTower` o `Network` de lucide, label `"SMPP Short Code"`, badge `"Activo"` si está configurado.
- `frontend/src/components/Devices/DeviceActionsMenu.tsx`: ocultar Edit/Delete para `device_type === "smpp"`.
- `frontend/src/components/Sms/SendSMS.tsx`: la lista de devices ya sale de `useDeviceList`; asegurar que SMPP se pueda seleccionar.
- No modificar `AddDevice.tsx` para crear SMPP; el device lo crea bootstrap.

Criterio:
- `/devices` muestra SMPP device cuando `SMPP_ENABLED=true`.
- No muestra acciones de edición/borrado.
- En modal de envío SMS, SMPP aparece como opción si el backend lo lista.
- `bun run build` y `bun run lint` pasan.

---

## 14. API pública, SDKs y MCP

### 14.1 API pública

Mantener los endpoints existentes:
- `POST /api/sms/send`
- `POST /api/sms/send-template`

No crear `/api/smpp/send`. SMPP es un gateway/provider, no una API pública separada.

Payload existente sigue válido. Añadir campos opcionales compatibles:

```json
{
  "recipients": ["+15551234567"],
  "body": "Hola",
  "device_id": "optional",
  "group_ids": [],
  "channel": "auto"
}
```

Reglas:
- `channel` es opcional y default `"auto"`.
- Para SMPP, `channel` se ignora y el resultado se reporta como `provider_channel="sms"`.
- Si se implementa validación central, aceptar `"auto"` y rechazar otros valores hasta que haya soporte real.
- Para forzar SMPP, el caller usa `device_id` del device virtual `smpp`.
- No exponer credenciales SMPP, TON/NPI o short code en la API pública.

### 14.2 `sms_messages` expuesto por API

Actualizar tipos/serialización para incluir:
- `provider_message_id?: string`
- `provider_channel?: "auto" | "sms" | "rcs" | "unknown"`
- `provider_origination_identity?: string`

Para SMPP:
- `provider_channel="sms"`.
- `provider_origination_identity=SMPP_SOURCE_ADDR`.
- `provider_message_id` es el `message_id` del SMSC.

### 14.3 SDKs oficiales

Repos externos a actualizar después del backend:

| SDK | Repo | Cambios |
|---|---|---|
| JavaScript/TypeScript | `vendel-sdk-js` | `sendSMS({ channel?: "auto" })`, `sendTemplate({ channel?: "auto" })`, tipos de `SMSMessage` con provider fields. |
| Python | `vendel-sdk-python` | Parámetro opcional `channel="auto"`, modelo de mensaje con provider fields. |
| Go | `vendel-sdk-go` | `SendSMSRequest.Channel string \`json:"channel,omitempty"\``, provider fields en `SMSMessage`. |

Reglas para SDKs:
- No romper llamadas existentes.
- No agregar método `sendSMPP`.
- Documentar que `device_id` puede apuntar a un gateway SMPP virtual si el operador lo habilitó.
- Tests de SDK: payload antiguo sigue igual; payload con `channel:"auto"` serializa correctamente; modelos parsean provider fields.

### 14.4 MCP (`vendel-mcp`)

Actualizar el servidor MCP externo `vendel-mcp` después del backend:

- Tool de envío mantiene el mismo nombre.
- Añadir argumento opcional `channel` default `"auto"`.
- Documentar que `device_id` puede forzar SMPP si se conoce el device virtual.
- Tool/schema de lectura de mensajes devuelve `provider_channel`, `provider_origination_identity` y `provider_message_id`.
- No exponer env vars ni credenciales SMPP por MCP.
- README del MCP debe explicar que SMPP es configurado por el operador en Vendel.

### 14.5 Documentación

Actualizar:
- `README.md`: sección SMPP gateway, configuración mínima, DLR, MO y limitaciones.
- `README.es.md`: misma información si se mantiene paridad.
- `docs/smpp-provider-setup.md`: guía operador paso a paso.
- `CLAUDE.md`: patrón `ManagedProvider`, DLR/MO consumers, `device_type="smpp"`, SDKs y MCP.
- `.env` y `.env.example`: variables SMPP.

---

## 15. Plan por fases

### Fase 0 — Base compartida

Objetivo: dejar lista la abstracción de providers externos sin cambiar comportamiento.

Archivos:
- `backend/services/smsprovider/provider.go`
- `backend/services/smsprovider/managed.go`

Criterio:
- `go build ./...` pasa desde `backend/`.
- Envíos por devices físicos siguen igual.

### Fase 1 — Migración y device virtual SMPP

Archivos:
- `backend/migrations/<timestamp>_smpp_provider.go`
- `backend/services/smsprovider/setup_smpp.go`
- `backend/hooks/devices.go`
- `.env`
- `.env.example`
- `CLAUDE.md`
- `README.md`

Criterio:
- `SMPP_ENABLED=false`: no se crea device.
- `SMPP_ENABLED=true`: se crea un solo `sms_devices` global `device_type="smpp"`.
- Crear/editar/borrar SMPP vía API pública → 403.
- Crear/borrar SMPP no altera `device_count`.

### Fase 2 — SMPPProvider unitario

Archivos:
- `backend/services/smsprovider/smpp_config.go`
- `backend/services/smsprovider/smpp_provider.go`
- `backend/services/smsprovider/smpp_client.go`
- `backend/services/smsprovider/smpp_provider_test.go`

Dependencia:
```
cd backend
go get github.com/fiorix/go-smpp/v2
```

Tests:
- `IsConfigured=false` si faltan envs críticas.
- `Send()` falla legible si bind no está listo.
- `Send()` exitoso retorna `ProviderMessageID` y `Status="sent"`.
- Error submit → `Status="failed"` con mensaje corto.
- Parser DLR: `DELIVRD`, `UNDELIV`, `REJECTD`, `ENROUTE`.

### Fase 3 — Lifecycle y consumidores

Archivos:
- `backend/services/provider_lifecycle.go`
- `backend/services/provider_reports.go`
- `backend/services/provider_incoming.go`
- `backend/main.go`

Criterio:
- Al arrancar con SMPP configurado, se intenta bind y se loggea estado.
- Reconnect con backoff si cae el bind.
- DLR por canal actualiza `sms_messages`.
- MO se guarda solo si `SMPP_ENABLE_MO=true` y `SMPP_OWNER_USER_ID` existe.

### Fase 4 — Integración con envío

Archivos:
- `backend/services/sms.go`
- `backend/services/sms_provider_dispatch.go`
- tests en `backend/services/sms_test.go` o nuevo archivo.

Criterio:
- Sin devices físicos + SMPP on → envío por SMPP.
- Con device físico + SMPP on sin `device_id` → físico gana.
- `device_id` SMPP explícito → SMPP gana.
- `provider_message_id` se guarda antes de `sms_sent`.
- Si SMPP no está listo, mensaje queda `failed` y dispara `sms_failed`.

### Fase 5 — Frontend y docs

Archivos:
- `frontend/src/types/collections.ts`
- `frontend/src/components/Devices/columns.tsx`
- `frontend/src/components/Devices/DeviceActionsMenu.tsx`
- `docs/smpp-provider-setup.md`
- `.env`
- `.env.example`
- `CLAUDE.md`
- `README.md`
- `README.es.md` si se mantiene paridad

Criterio:
- UI muestra device SMPP read-only.
- Docs explican env vars, TON/NPI, bind test, DLR y MO.
- `.env`, `.env.example`, `CLAUDE.md` y `README.md` quedan actualizados con las nuevas variables e información necesaria.
- Se crea checklist/issue para actualizar `vendel-sdk-js`, `vendel-sdk-python`, `vendel-sdk-go` y `vendel-mcp`.
- `bun run build`, `bun run lint`, `go build ./...` pasan.

---

## 16. Tests bloqueantes

- [ ] `go build ./...` desde `backend`.
- [ ] `go test ./...` desde `backend`.
- [ ] `bun run build` desde `frontend`.
- [ ] `bun run lint` desde `frontend`.
- [ ] Device SMPP virtual se crea idempotente.
- [ ] Hooks bloquean create/update/delete SMPP vía API.
- [ ] `Send()` unitario no toca SMSC real.
- [ ] Fallback usa SMPP solo si no hay devices físicos y provider está configurado.
- [ ] DLR `DELIVRD` actualiza a `delivered` y dispara webhook.
- [ ] DLR `UNDELIV`/`REJECTD` actualiza a `failed` y dispara webhook.
- [ ] MO crea `sms_messages.message_type="incoming"` solo con owner configurado.
- [ ] `.env`, `.env.example`, `CLAUDE.md` y `README.md` documentan SMPP, DLR/MO, TON/NPI y variables nuevas.
- [ ] API mantiene compatibilidad con payloads antiguos de `/api/sms/send`.
- [ ] `channel` opcional default `auto` no rompe SMPP y queda documentado.
- [ ] SDKs oficiales y `vendel-mcp` tienen checklist/issue de actualización.

---

## 17. Out of scope

- Múltiples SMSC.
- Rutas por país/prefijo/cliente.
- Credenciales SMPP por usuario.
- UI de configuración.
- Balanceo/failover entre SMSC.
- Cost analytics.
- Tabla persistente de DLR crudos.
- MMS/RCS.
- Segmentación avanzada/UDH configurable.
- Método SDK específico `sendSMPP`.
- Exponer configuración SMPP por MCP.

---

## 18. Glosario

- **SMPP**: Short Message Peer-to-Peer, protocolo usado para intercambiar SMS con SMSC.
- **SMSC**: Short Message Service Center.
- **Short code**: número corto usado como origen/destino de campañas o servicios.
- **MT**: mobile-terminated, SMS enviado desde Vendel hacia un teléfono.
- **MO**: mobile-originated, SMS enviado por un teléfono hacia el short code.
- **DLR**: delivery receipt, reporte de entrega.
- **Bind transceiver**: conexión SMPP que puede enviar y recibir.
- **TON/NPI**: Type of Number / Numbering Plan Indicator, parámetros SMPP para interpretar direcciones.
