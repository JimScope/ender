# Plan de integración: AWS End User Messaging en Vendel

SMS, short codes y RCS de texto con la menor complejidad posible.

> **Audiencia**: agente de implementación (Cursor, Claude Code, etc.) y revisores humanos.
> **Estado**: aprobado, listo para implementar empezando por Fase 0.
> **Principio guía**: KISS. Cada decisión debe ser la más simple posible que satisfaga el requisito; no anticipar features que no han sido pedidas.

---

## 1. Resumen ejecutivo

Añadir AWS End User Messaging (AEUM, antes "Pinpoint SMS and Voice v2") como proveedor alternativo de envío de mensajes de texto en Vendel, en paralelo a los dispositivos físicos (Android + módems USB) que existen hoy.

- **Servicio AWS**: End User Messaging vía SDK Go v2 `pinpointsmsvoicev2`.
- **Origination identity**: pool ARN recomendado (configurado en AWS por el operador). El pool puede contener números SMS, short codes y AWS RCS Agents.
- **Canales soportados en MVP**: SMS texto, SMS vía short code y RCS texto. Con pool compatible, AWS puede hacer fallback automático RCS → SMS.
- **Credenciales**: globales del operador (env vars). Diseño preparado para credenciales-por-usuario, pero **no implementado en este plan**.
- **Delivery status**: AEUM → ConfigurationSet → SNS topic → HTTPS subscription al endpoint de Vendel. Sin Lambda intermedia.
- **Modelo**: AEUM se representa como un `sms_devices` con `device_type="aws_aeum"`. Sin colecciones nuevas.
- **Plan hermano**: la implementación SMPP/short code center vive separada en `docs/smpp-provider-plan.md`.

### 1.1 Alcance de soporte

| Canal / identidad | Soporte en Vendel | Cómo se configura |
|---|---|---|
| SMS por long code / toll-free / 10DLC / sender ID | Completo para texto | Añadir identity al pool AEUM. |
| SMS por short code | Completo para texto | Solicitar/aprobar short code en AWS y añadirlo al pool AEUM. |
| RCS | Completo para texto | Crear/aprobar AWS RCS Agent y añadirlo al pool AEUM. |
| RCS con fallback SMS | Completo para texto | Pool con RCS Agent + identity SMS fallback. |
| RCS rich cards/media/suggested replies | Fuera de alcance | Requiere otra fase y probablemente otro modelo de payload. |

La API pública de Vendel sigue siendo simple: `POST /api/sms/send` y `POST /api/sms/send-template`. El usuario envía texto; AWS decide el canal/origen según el pool. Vendel guarda el canal final best-effort cuando AWS lo reporta.

---

## 2. Decisiones congeladas

| Tema | Decisión |
|---|---|
| Servicio | AWS End User Messaging (no SNS legacy `Publish`) |
| SDK | `github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2` |
| Origination | Pool ARN configurado por env var. Puede incluir short code, 10DLC, toll-free, sender ID y AWS RCS Agent. |
| RCS | Soportado como texto mediante `SendTextMessage` usando pool con AWS RCS Agent. No rich cards en este plan. |
| Short codes | Soportados como origination identity dentro del pool. Vendel no elige manualmente el short code; AWS enruta desde el pool. |
| Credenciales | Globales por env var; futura extensión por usuario diseñada pero no codeada |
| Webhook delivery | SNS HTTPS subscription al backend de Vendel; firma SNS validada |
| Persistencia | Extender `sms_devices` con valor `aws_aeum` en `device_type`. NO crear colección nueva |
| Cuota | Compartida con devices físicos (pasa por `IncrementSMSCount()`) |
| Política de selección | Si el caller pasa `device_id`, ese gana. Si no, round-robin entre devices físicos online del user, fallback a AEUM device si no hay físicos |
| Edición del AEUM device por el usuario final | Prohibida (read-only desde la UI; hook de PocketBase rechaza updates) |
| Wizard `AddDevice` para AEUM | NO incluido (el AEUM device lo crea el sistema al arrancar) |

---

## 3. Arquitectura actual relevante (para que Cursor entienda el codebase sin volver a investigar)

### 3.1 Flujo de envío SMS hoy

`backend/services/sms.go`:
- `SendSMS()` (línea 22): valida cuota → resuelve devices del usuario → crea registros `sms_messages` asignados round-robin → dispara `DispatchMessages()` en goroutine.
- `resolveDevices()` (línea 31): query SQL — devuelve devices con `fcm_token != ''` o `device_type = 'modem'`.
- Asignación round-robin: `device := devices[i%len(devices)]` (línea 151).
- `ProcessSMSAck()` (línea 190): procesa el reporte que envía el device físico vía `POST /api/sms/report`. Actualiza timestamps `sent_at`/`delivered_at` y dispara webhooks (`TriggerWebhooks` en línea 218).

### 3.2 Notificación a devices físicos

`backend/services/notification.go`:
- `DispatchMessages()` (línea 50): para Android, manda **FCM tickle** (data-only) con `{type: "tickle", count: N}` (línea 117-119). El device fetcha los mensajes via `GET /api/sms/pending`.
- Modems están excluidos de FCM (línea 72); usan otro mecanismo.

### 3.3 Endpoints públicos de envío

`backend/handlers/sms.go`:
- `POST /api/sms/send` (línea 33): payload `{recipients: ["+1..."], body, device_id?, group_ids?}`. Respuesta `{batch_id, message_ids, recipients_count, status: "accepted"}`.
- `POST /api/sms/send-template` (línea 72): igual pero con `template_id` + `variables`.
- Ambos: auth via `middleware.ResolveAuthOrAPIKey` (línea 34).

### 3.4 Modelo de datos

Colección `sms_devices` (migración `backend/migrations/1740000000_initial.go:87`):
- `name` (text, requerido)
- `phone_number` (text, requerido, max 20)
- `api_key` (text, hidden)
- `fcm_token` (text, hidden, max 500)
- `device_type` (select): hoy `{"android", "modem"}`. **Vamos a añadir `"aws_aeum"`**.
- `user` (relation, requerido). Permiso: `user = @request.auth.id`.

Colección `sms_messages` (mismo archivo de migración): contiene `status`, `sent_at`, `delivered_at`, `device` (relation), `error_message`. **Vamos a añadir `provider_message_id`, `provider_channel` y `provider_origination_identity`**.

### 3.5 Webhooks salientes (Vendel → cliente)

`backend/services/webhook.go`:
- Eventos disparados: `sms_sent`, `sms_delivered`, `sms_failed`, `sms_received`.
- `TriggerWebhooks()` (línea 436): busca `webhook_configs` del user, POSTea payload con HMAC-SHA256.
- Retry: `RetryFailedWebhooks` (línea 516), 5 reintentos con backoff exponencial.

### 3.6 Quota

`backend/services/quota.go`:
- `CheckSMSQuota()` (línea 62): valida `sms_sent_this_month < max_sms_per_month`, retorna 429 con `available`, `limit`, `upgrade_url`.
- `IncrementSMSCount()` (línea 201): UPDATE atómico.
- Reset mensual: `ResetMonthlyQuotas` (línea 250).

### 3.7 Provider pattern existente (referencia para SMS)

`backend/services/payment/provider.go` — interface `Provider` con métodos `Name()`, `DisplayName()`, `PaymentMethod()`, `IsConfigured()`, `CreateInvoice()`, `ParseWebhook()`. Implementaciones: `QvaPayProvider`, `StripeProvider`, `TronDealerProvider`. **Replicar la idea de interface pequeña para SMS, pero en `services/smsprovider/` para evitar ciclos de import.**

### 3.8 Frontend relevante

- Ruta de devices: `frontend/src/routes/_layout/devices.tsx`.
- Componente principal: `frontend/src/components/Devices/AddDevice.tsx` (wizard 4 pasos).
- Tipos: `frontend/src/types/collections.ts:30`, `DeviceType = "android" | "modem"`.
- Hook: `frontend/src/hooks/useDeviceList.ts`.
- Tabla y badges online/offline: `frontend/src/components/Devices/columns.tsx:37`.

### 3.9 Convenciones del proyecto (de `CLAUDE.md`)

- `main.go` debe permanecer fino (~80 líneas).
- Hooks → `backend/hooks/<dominio>.go`, función única `RegisterXxxHooks(app)`.
- Cron jobs → `backend/cronjobs/jobs.go` usando helper `register()`.
- Rutas → `backend/handlers/<dominio>.go`, función `RegisterXxxRoutes(se *core.ServeEvent)`.
- Servicios → `backend/services/<dominio>.go`, aceptan `core.App` (interface), no `*pocketbase.PocketBase`.
- Frontend: package manager **`bun`**, no npm/yarn.

---

## 4. Principios KISS aplicados (qué NO hacemos)

| Tentación | Decisión |
|---|---|
| Crear colección nueva `sms_providers` | NO. Extender `sms_devices`. |
| Interface con 5+ métodos (`SupportsCountry`, `EstimateCost`, etc.) | Solo 3: `Name()`, `IsConfigured()`, `Send()`. |
| Campo `provider_config` JSON en `sms_devices` | NO. Config global vía env vars. |
| Lambda + CloudWatch para webhooks | NO. SNS HTTPS subscription nativa. |
| Sistema de políticas configurables (device-first / aeum-first / etc.) | NO. Una sola política hardcodeada. |
| Selector UI de canal (`sms` / `rcs` / short code específico) | NO. Vendel usa `auto`; el pool AWS decide. |
| Colección nueva para canales | NO. `provider_channel` y `provider_origination_identity` bastan para reporting simple. |
| Tabla `provider_credentials` para "futuro por usuario" | NO. Solo aceptamos `UserID` en la firma. |
| Cifrado de secrets en BD | NO aplica (credenciales en env vars). |
| Retry / circuit breaker dedicado para AEUM | NO. AWS SDK ya hace retry con backoff. |
| Worker pool de envíos AEUM | NO. `go provider.Send()` simple; SDK es thread-safe. |
| Métrica de costos en dashboard | NO en este plan. Logging estructurado primero. |
| HMAC propio en el webhook | NO. Validamos firma X.509 que ya pone SNS. |
| Coste de envío persistido en BD | NO en este plan. |
| Métodos `Send` que retornen `Raw map[string]any` | NO. Tres campos: `ProviderMessageID`, `Status`, `ErrorMessage`. |
| Cambiar nombres públicos de rutas de `/sms` a `/messages` | NO. Mantener compatibilidad y simplicidad aunque AEUM pueda entregar RCS texto. |

---

## 5. Diseño de la abstracción `Provider`

### 5.1 Archivos `backend/services/smsprovider/*.go`

**Ubicación importante**: los providers viven en un subpaquete independiente (`backend/services/smsprovider`, `package smsprovider`). El orquestador actual (`backend/services/sms.go`, `package services`) importa ese subpaquete, pero el subpaquete **no** importa `vendel/services`.

Motivo: evitar ciclos de import. Persistencia en BD, cuotas, `TriggerWebhooks()` y dispatch físico siguen en `package services`; los providers solo hacen I/O externo y devuelven resultados puros.

```go
package smsprovider

import "context"

type Provider interface {
    Name() string
    IsConfigured() bool
    Send(ctx context.Context, req SendRequest) (*SendResult, error)
}

type SendRequest struct {
    MessageID string // ID en sms_messages (interno de Vendel)
    UserID    string // dueño del mensaje (para futura selección de credenciales)
    To        string // destinatario en E.164
    Body      string // cuerpo del SMS
    ChannelHint string // opcional: "auto" por defecto; futuro: "sms" | "rcs"
}

type SendResult struct {
    ProviderMessageID string // ID externo (ej. el MessageId que devuelve AEUM)
    Status            string // "sent" | "failed"
    ErrorMessage      string // poblado solo si Status == "failed"
    ProviderChannel   string // opcional: "auto" | "sms" | "rcs" | "unknown"
    OriginationIdentity string // opcional/best-effort si el provider lo conoce
}
```

**Convenciones:**
- `Status` solo admite `"sent"` o `"failed"`. Si AEUM acepta el mensaje, marcamos `"sent"`. El delivery webhook luego puede llevarlo a `"delivered"` o `"failed"` final.
- `ChannelHint` en este plan se envía como `"auto"` y no cambia la llamada AWS. AEUM decide SMS/RCS/short code desde el pool.
- `ProviderChannel` y `OriginationIdentity` son best-effort. Para AEUM con pool, el canal real puede conocerse solo después por eventos de delivery, si AWS lo incluye en el payload.
- `Provider.Send` es bloqueante. La concurrencia la maneja el caller con goroutines.
- `Provider` no toca la BD ni la cuota; eso lo orquesta `SendSMS()`.

### 5.2 DeviceProvider

**No crear un `DeviceProvider` que importe `services.DispatchMessages()` desde el subpaquete**, porque eso crearía ciclo. El flujo FCM/modem existente se mantiene orquestado directamente por `services.SendSMS()` y `services.DispatchMessages()`.

Para la Fase 0, el refactor consiste en:
- Crear `smsprovider.Provider`, `SendRequest` y `SendResult`.
- Dejar el dispatch físico como está.
- Preparar `SendSMS()` para particionar providers en Fase 3, sin cambiar comportamiento todavía.

### 5.3 Archivo `backend/services/smsprovider/aeum_provider.go`

Implementación AEUM. Estructura:

```go
type AEUMProvider struct {
    client          AEUMClient    // interface para mockear en tests
    originationIdentityARN string // pool ARN recomendado; puede ser direct identity si se acepta sin fallback
    configSetName   string
    enabled         bool
}

type AEUMClient interface {
    SendTextMessage(ctx context.Context, in *pinpointsmsvoicev2.SendTextMessageInput, opts ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.SendTextMessageOutput, error)
}

func NewAEUMProvider(cfg AEUMConfig) (*AEUMProvider, error) { ... }

func (p *AEUMProvider) Name() string { return "aws_aeum" }
func (p *AEUMProvider) IsConfigured() bool { return p.enabled && p.originationIdentityARN != "" }
func (p *AEUMProvider) Send(ctx context.Context, req SendRequest) (*SendResult, error) {
    input := &pinpointsmsvoicev2.SendTextMessageInput{
        DestinationPhoneNumber: aws.String(req.To),
        OriginationIdentity:    aws.String(p.originationIdentityARN),
        MessageBody:            aws.String(req.Body),
        ConfigurationSetName:   aws.String(p.configSetName),
        MessageType:            types.MessageTypeTransactional,
    }
    out, err := p.client.SendTextMessage(ctx, input)
    if err != nil {
        return &SendResult{Status: "failed", ErrorMessage: simplifyAWSError(err)}, nil
    }
    return &SendResult{
        ProviderMessageID: aws.ToString(out.MessageId),
        Status:            "sent",
        ProviderChannel:   "auto",
        OriginationIdentity: p.originationIdentityARN,
    }, nil
}
```

`simplifyAWSError(err)`: helper que mapea errores AWS conocidos a mensajes legibles cortos. Errores a contemplar: `ValidationException`, `ThrottlingException`, `ConflictException`, default genérico.

### 5.4 Archivo `backend/services/smsprovider/setup.go`

```go
// EnsureAEUMDevice crea (idempotente) un sms_devices con device_type="aws_aeum"
// cuando el operador habilita AEUM por env vars. Se invoca en OnServe.
func EnsureAEUMDevice(app core.App) error { ... }
```

Comportamiento:
- Si `AEUM_ENABLED != true`: no hace nada.
- Si ya existe un device con `device_type=aws_aeum`: no hace nada.
- Si no existe: lo crea. Campos:
  - `name`: `"AWS End User Messaging"` (o configurable por env `AEUM_DEVICE_NAME`).
  - `phone_number`: el sender ID o pool name (cosmético; no usado funcionalmente).
  - `user`: vacío (`""`) — el campo se hace opcional en la migración. Ver **Fase -1 §-1.1**.
  - `device_type`: `"aws_aeum"`.

**Crítico**: `app.Save()` pasa por `OnRecordCreate` hooks. La Fase -1 §-1.2 modifica el hook existente para saltar quota/api-key cuando `device_type=="aws_aeum"`. Sin esa excepción, este `app.Save()` rompe.

### 5.5 Soporte RCS y short codes en AEUM

AEUM soporta SMS, short codes y RCS text usando la misma llamada `SendTextMessage` cuando se envía contra un pool. Vendel no implementa selección manual por canal en MVP:

- El operador crea un pool por caso de uso en AWS.
- El pool puede contener un AWS RCS Agent y números SMS de fallback, incluyendo short code, 10DLC, toll-free o sender ID según país.
- Vendel configura `AEUM_ORIGINATION_IDENTITY_ARN` con el ARN del pool.
- Vendel llama `SendTextMessage` una sola vez. AWS elige RCS/SMS/origination identity según disponibilidad, sticky sending, prioridad del pool y fallback.
- Si el pool contiene RCS Agent + número SMS, AWS puede intentar RCS y caer a SMS automáticamente. Direct-to-RCS-agent no tiene fallback; por eso el MVP recomienda pool.
- Short codes no requieren lógica especial en Vendel si forman parte del pool.

**Contrato de Vendel para soporte completo de texto**:
- Vendel acepta y valida el body igual que hoy; no introduce un nuevo endpoint para RCS.
- Vendel persiste `provider_channel="auto"` justo después del `SendTextMessage` exitoso.
- Vendel persiste `provider_origination_identity=AEUM_ORIGINATION_IDENTITY_ARN` al aceptar el envío.
- Cuando lleguen eventos de delivery, Vendel intenta actualizar `provider_channel` a `"sms"` o `"rcs"` y `provider_origination_identity` a la identity final usada, si el payload lo trae.
- Si AWS no reporta canal final, Vendel mantiene `"auto"`; esto no es error.
- Short code queda completamente soportado como parte del pool: envío, delivery status y reporting best-effort de identity.

**Limitaciones deliberadas para mantener Vendel simple**:
- RCS es texto. No rich cards, carousels, suggested replies ni media.
- Vendel guarda `provider_channel="auto"` al aceptar el envío. Si los eventos de delivery incluyen canal final u origination identity final, Fase 4 debe poblar `provider_channel` y `provider_origination_identity` best-effort.
- No hay selector UI “RCS only” o “SMS only”. Esa política vive en AWS mediante el pool.
- No hay configuración en Vendel de agentes RCS ni short codes. Vendel solo necesita el ARN del pool.

---

## 6. Cambios al modelo de datos

> **Esta sección depende de las decisiones de Fase -1 (§-1.1 a §-1.6).** Si las correcciones no se aplican, la implementación falla por restricciones de schema y por hooks que cobran cuota.

### 6.1 Nueva migración

**File**: `backend/migrations/<timestamp>_aws_aeum_provider.go`

Cambios:

1. En `sms_devices.user` (RelationField): cambiar `Required: true` → `Required: false`. La obligatoriedad para devices físicos se traslada a hook (ver Fase -1 §-1.1).
2. En `sms_devices.device_type` (SelectField): añadir valor `"aws_aeum"` a la lista de valores permitidos (manteniendo `"android"` y `"modem"`).
3. Actualizar reglas de acceso de `sms_devices`:
   - `ListRule`: `user = @request.auth.id || device_type = "aws_aeum"`
   - `ViewRule`: `user = @request.auth.id || device_type = "aws_aeum"`
   - `CreateRule`: `user = @request.auth.id`
   - `UpdateRule`: `user = @request.auth.id && device_type != "aws_aeum"`
   - `DeleteRule`: `user = @request.auth.id && device_type != "aws_aeum"`
4. En `sms_messages`: añadir campo `provider_message_id` (TextField, opcional).
5. En `sms_messages`: añadir `provider_channel` (TextField opcional, valores esperados best-effort: `auto`, `sms`, `rcs`, `unknown`).
6. En `sms_messages`: añadir `provider_origination_identity` (TextField opcional, para pool/identity ARN o identidad final si el event payload la expone).
7. **Obligatorio, no opcional**: índice sobre `provider_message_id` (`idx_sms_messages_provider_message_id`, no único). Fase 4 hace lookups en cada webhook; sin índice = full table scan. Ver Fase -1 §-1.6.

**Backwards-compat**: los registros existentes de `sms_devices` ya tienen `user` poblado, así que cambiar el campo a opcional no rompe nada. Registros de `sms_messages` existentes no tienen `provider_message_id`, `provider_channel` ni `provider_origination_identity` (vacío), correcto.

### 6.2 Permisos extra (vía hooks, no schema)

Las reglas de UpdateRule/DeleteRule ya bloquean updates/deletes sobre `aws_aeum` desde la API REST de PocketBase. Pero para defensa en profundidad y para bloquear también el path admin/superuser, añadir hooks `OnRecordUpdate`/`OnRecordDelete` que rechacen explícitamente. Estos hooks viven junto a las correcciones de Fase -1 §-1.2 en `backend/hooks/devices.go`.

**CreateRule no basta para impedir que un usuario cree `device_type="aws_aeum"`**: un request con `user=@request.auth.id` puede cumplir la regla. El hook de create debe rechazar cualquier intento de crear `aws_aeum` desde la API normal. Solo `EnsureAEUMDevice()` (server-side bootstrap) puede crear el device global.

---

## 7. Plan por fases

### Fase -1 — Correcciones de contrato (mandatorias antes de tocar código)

**Origen**: review de Codex contra el código real validó puntos que harían fallar o ensuciar la implementación (schema, hooks, retry y paquetes). Estas decisiones quedan congeladas aquí.

> **No se escriben tests ni código en esta fase.** Esta fase solo cierra el contrato. Su "implementación" se reparte entre Fase 2 (migración, hooks) y Fase 3 (lógica de selección, dispatch, UPDATE inmediato).

#### -1.1 `sms_devices.user`: nullable (con obligatoriedad por hook para devices físicos)

**Problema confirmado**: `backend/migrations/1740000000_initial.go:99` define `user` con `Required: true`. El plan inicial proponía `user = NULL` para el AEUM device, lo que rompe la constraint.

**Decisión**:
- Migración Fase 2 cambia el campo a `Required: false`.
- Hook de `OnRecordCreate` añade la validación: si `device_type != "aws_aeum"` y `user == ""` → error 400 `"user is required for physical devices"`.

**Alternativas descartadas**:
- *Usuario "system" real*: añade un user fantasma, complica reglas de auth, no aporta nada. Anti-KISS.
- *AEUM device por usuario*: rompe la decisión §2 ("credenciales globales del operador"). Cada user terminaría con un device AEUM sin credenciales propias = UX confusa.

#### -1.2 Hooks de cuota: excepción para `aws_aeum`

**Problema confirmado**: `backend/hooks/devices.go:14` ejecuta `CheckDeviceQuota` + `IncrementDeviceCount` + `GenerateSecureKey` en **todo** `OnRecordCreate("sms_devices")`. `EnsureAEUMDevice()` rompería esta lógica (user vacío, contaminación de contadores).

**Decisión**: modificar `RegisterDeviceHooks` para bloquear creación pública de `aws_aeum` y saltar el bloque cuota/api-key cuando el bootstrap server-side lo crea:

```go
app.OnRecordCreateRequest("sms_devices").BindFunc(func(e *core.RecordRequestEvent) error {
    if e.Record.GetString("device_type") == "aws_aeum" {
        return apis.NewForbiddenError("AWS AEUM device is system-managed", nil)
    }
    return e.Next()
})

app.OnRecordCreate("sms_devices").BindFunc(func(e *core.RecordEvent) error {
    if e.Record.GetString("device_type") == "aws_aeum" {
        // Global, sin user, sin quota, sin api_key.
        // Solo asegurar nombre por defecto si vino vacío.
        if e.Record.GetString("name") == "" {
            e.Record.Set("name", "AWS End User Messaging")
        }
        return e.Next()
    }

    // Validación trasladada desde el schema (-1.1).
    userId := e.Record.GetString("user")
    if userId == "" {
        return apis.NewBadRequestError("user is required for physical devices", nil)
    }

    // ... resto del hook actual sin cambios (CheckDeviceQuota, IncrementDeviceCount,
    //     GenerateSecureKey, default device_type, Unhide api_key, rollback en error).
})

app.OnRecordDelete("sms_devices").BindFunc(func(e *core.RecordEvent) error {
    if err := e.Next(); err != nil {
        return err
    }
    if e.Record.GetString("device_type") == "aws_aeum" {
        return nil // sin decrement
    }
    return services.DecrementDeviceCount(e.App, e.Record.GetString("user"))
})
```

#### -1.3 `resolveDevices` debe permitir el AEUM device global

**Problema confirmado**: `backend/services/sms.go:65` rechaza si `device.user != userId`. El AEUM device (con `user=""`) es inaccesible aunque el caller pase su `device_id` explícito.

**Decisión**: añadir excepción explícita **solo para `aws_aeum`** (no abrir acceso a devices físicos ajenos):

```go
func resolveDevices(app core.App, userId, deviceId string, aeumProvider smsprovider.Provider) ([]*core.Record, error) {
    if deviceId != "" {
        device, err := app.FindRecordById("sms_devices", deviceId)
        if err != nil {
            return nil, fmt.Errorf("device not found: %w", err)
        }
        // AEUM global: cualquier user autenticado puede dirigir a él, pero solo
        // si el provider está realmente habilitado/configurado.
        if device.GetString("device_type") == "aws_aeum" {
            if aeumProvider == nil || !aeumProvider.IsConfigured() {
                return nil, fmt.Errorf("AWS End User Messaging is not configured")
            }
            return []*core.Record{device}, nil
        }
        if device.GetString("user") != userId {
            return nil, fmt.Errorf("device does not belong to user")
        }
        return []*core.Record{device}, nil
    }

    // Sin device_id: prioridad a devices físicos del user.
    records, err := app.FindRecordsByFilter(
        "sms_devices",
        "user = {:userId} && (fcm_token != '' || device_type = 'modem')",
        "-created", 0, 0,
        dbx.Params{"userId": userId},
    )
    if err == nil && len(records) > 0 {
        return records, nil
    }

    // Fallback: AEUM device global, si AEUM está habilitado/configurado.
    if aeumProvider == nil || !aeumProvider.IsConfigured() {
        return nil, nil
    }
    aeum, err := app.FindFirstRecordByFilter("sms_devices", "device_type = 'aws_aeum'")
    if err == nil && aeum != nil {
        return []*core.Record{aeum}, nil
    }
    return nil, nil
}
```

#### -1.4 Webhook: semántica correcta de retry para eventos sin match

**Problema confirmado**: SNS HTTP delivery **no reintenta** si el endpoint responde 2xx. El plan v2 decía "aceptar 200, AWS reintentará" — falso. El evento se perdería.

**Decisión**: dos caminos según frescura del evento:

```
Notification llega con messageId que no matchea ningún sms_messages.provider_message_id:
  - Parsear event.Timestamp del payload AEUM.
  - Si (now - event.Timestamp) < 5 min → responder 503.
    Razón: race plausible entre el UPDATE de Fase 3 y este webhook. SNS reintenta con
    backoff exponencial; el UPDATE habrá terminado al siguiente intento.
  - Si >= 5 min → log error con messageId, responder 200.
    Razón: el UPDATE debería haber ocurrido hace mucho. Algo está realmente mal;
    no tiene sentido reintentar indefinidamente.
    (Opcional: persistir en tabla `orphan_sns_events` para reconciliación manual.
    NO se crea en este plan; queda como nota.)
```

**Mitigación complementaria — UPDATE inmediato en Fase 3** para minimizar la ventana de race:

```go
// En la goroutine de dispatch AEUM:
result, err := aeumProvider.Send(ctx, req)
if err != nil { /* log y exit */ }

// PRIMERO: persistir provider_message_id. No hacer nada antes.
msg.Set("provider_message_id", result.ProviderMessageID)
msg.Set("provider_channel", result.ProviderChannel)
msg.Set("provider_origination_identity", result.OriginationIdentity)
msg.Set("status", result.Status) // "sent" o "failed"
if result.Status == "sent" {
    msg.Set("sent_at", types.NowDateTime())
} else {
    msg.Set("error_message", result.ErrorMessage)
}
if err := app.Save(msg); err != nil {
    app.Logger().Error("failed to persist provider result", "provider", aeumProvider.Name(), "msgId", msg.Id, "err", err)
    return
}

// DESPUÉS: webhooks salientes.
if result.Status == "sent" {
    TriggerWebhooks(app, msg.GetString("user"), msg, "sms_sent")
} else {
    TriggerWebhooks(app, msg.GetString("user"), msg, "sms_failed")
}
```

La ventana real de race así queda en milisegundos. Combinado con la lógica 5xx-si-reciente, no se pierden eventos.

#### -1.5 Separar dispatch FCM del dispatch AEUM

**Problema**: `notification.DispatchMessages` (`backend/services/notification.go:50`) es FCM/modem-específico. Mezclar AEUM ahí lo enturbia.

**Decisión**: en `services/sms.go`, después de crear los `sms_messages`, particionar:

```go
physicalMessages, aeumMessages := partitionByProvider(messages, app)

if len(physicalMessages) > 0 {
    routine.FireAndForget(func() { DispatchMessages(app, physicalMessages) })
}
if len(aeumMessages) > 0 {
    routine.FireAndForget(func() { DispatchProviderMessages(app, aeumProvider, aeumMessages) })
}
```

- `partitionByProvider` vive en `backend/services/sms_provider_dispatch.go` (`package services`).
- `DispatchProviderMessages` vive en `backend/services/sms_provider_dispatch.go` (`package services`).
- `smsprovider.Provider` y `AEUMProvider` viven en `backend/services/smsprovider/`.
- `notification.go` no se toca. Su responsabilidad sigue siendo solo FCM.

#### -1.6 Índice obligatorio en `provider_message_id`

**Problema**: Fase 4 hace `FindFirstRecordByFilter("sms_messages", "provider_message_id = {:id}")` por cada evento entrante. `sms_messages` crece sin parar; sin índice, full table scan en cada webhook.

**Decisión**: la migración de Fase 2 crea el índice:

```go
messages.AddIndex("idx_sms_messages_provider_message_id", false, "provider_message_id", "")
```

No único: en teoría AEUM no reusa MessageIds, pero defendemos contra bugs futuros.

#### -1.7 Tests específicos añadidos por estas correcciones

Estos tests son **bloqueantes** para cerrar la fase indicada:

**Fase 2:**
- Crear un device con `device_type=aws_aeum` no incrementa `sms_user_settings.device_count` de ningún user.
- Borrar el device AEUM no decrementa contadores.
- Crear un device físico sin `user` → 400 con mensaje claro.
- Crear un device `aws_aeum` vía API pública → 403; solo `EnsureAEUMDevice()` puede crearlo server-side.
- Update vía API a un device AEUM → 403 (regla + hook lo bloquean).
- Delete vía API de un device AEUM → 403.

**Fase 3:**
- Usuario sin devices físicos + `AEUM_ENABLED=false` → POST `/api/sms/send` retorna 404 "no device".
- Usuario sin devices físicos + AEUM on → envío vía AEUM, `provider_message_id` persistido.
- Usuario con device físico online + AEUM on (sin `device_id` explícito) → envío vía FCM (no AEUM).
- Usuario pasa el `device_id` del AEUM device explícitamente → envío vía AEUM.
- `provider_message_id` queda en la BD **antes** de que se disparen `TriggerWebhooks` (verificar orden vía mock).

**Fase 4:**
- Notification con `messageId` desconocido y `Timestamp` reciente (now − 30s) → 503.
- Notification con `messageId` desconocido y `Timestamp` viejo (now − 10min) → 200 + log.
- Notification con match correcto + `TEXT_DELIVERED` → 200, `status=delivered`, webhook `sms_delivered` disparado.
- Notification con match correcto + `TEXT_BLOCKED` → 200, `status=failed`, webhook `sms_failed` disparado.

---

### Fase 0 — Abstracción `Provider` (refactor sin cambio funcional)

**Objetivo**: introducir la interface y envolver el flujo actual sin cambiar su comportamiento.

**Archivos nuevos**:
- `backend/services/smsprovider/provider.go`

**Archivos a tocar**:
- `backend/services/sms.go` — importar el subpaquete y dejar preparado el tipo `smsprovider.Provider`, sin mover todavía el dispatch físico ni cambiar comportamiento.

**Criterio de aceptación**:
- `go build ./...` pasa.
- Todos los tests existentes pasan.
- Un envío manual (`POST /api/sms/send` con un device físico) funciona exactamente igual que antes.
- Diff revisable: el flujo de FCM no se modificó; solo se añadió la abstracción para providers externos.

**No hacer en esta fase**:
- No tocar el frontend.
- No añadir env vars de AWS.
- No tocar migraciones.

---

### Fase 1 — `AEUMProvider` (envío básico)

**Objetivo**: implementar el provider AEUM con tests unitarios. Sin integrarlo aún al flujo de `SendSMS()`.

**Dependencias** (`backend/go.mod`):
```
go get github.com/aws/aws-sdk-go-v2
go get github.com/aws/aws-sdk-go-v2/config
go get github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2
```

**Archivos nuevos**:
- `backend/services/smsprovider/aeum_provider.go` — `AEUMProvider`, `AEUMClient` interface, `simplifyAWSError`.
- `backend/services/smsprovider/aeum_provider_test.go` — tests unitarios con mock de `AEUMClient`.

**Tests mínimos** (un test por caso):
1. `Send` exitoso → retorna `ProviderMessageID` no vacío, `Status="sent"`.
2. `Send` con `ValidationException` → `Status="failed"`, mensaje legible.
3. `Send` con `ThrottlingException` → `Status="failed"`, mensaje legible.
4. `IsConfigured()` retorna `false` si `enabled=false` o `poolARN=""`.

**Criterio de aceptación**:
- Desde `backend/`: `go test ./services/smsprovider/...` pasa.
- Cobertura de los 4 casos arriba.
- Ningún test toca AWS real.

---

### Fase 2 — Migración + entidad "AEUM device"

**Objetivo**: añadir el valor `aws_aeum` al esquema, el campo `provider_message_id`, los hooks de protección, y el bootstrap del AEUM device.

**Archivos nuevos**:
- `backend/migrations/<timestamp>_aws_aeum_provider.go`
- `backend/services/smsprovider/setup.go` (función `EnsureAEUMDevice`).

**Archivos a tocar**:
- `backend/hooks/devices.go` (crear si no existe; añadir `RegisterDevicesHooks` con los OnRecordUpdate/Delete checks).
- `backend/main.go` — llamar `EnsureAEUMDevice(app)` en `OnServe`, y `hooks.RegisterDevicesHooks(app)` si no estaba ya.
- Modificar ListRule de `sms_devices` para permitir ver el AEUM device global (si optamos por `user=NULL`).

**Variables de entorno introducidas**:
- `AEUM_ENABLED` (`true|false`).
- `AEUM_REGION` (ej. `us-east-1`).
- `AEUM_ORIGINATION_IDENTITY_ARN` (ARN del pool recomendado; puede contener short code y AWS RCS Agent).
- `AEUM_ORIGINATION_POOL_ARN` (alias legacy opcional; si existe, cargarlo como fallback solo si `AEUM_ORIGINATION_IDENTITY_ARN` está vacío).
- `AEUM_CONFIGURATION_SET_NAME`.
- `AEUM_CHANNEL_MODE` (`auto`, default; documentar que solo `auto` se implementa en MVP).
- `AEUM_DEVICE_NAME` (opcional, default `"AWS End User Messaging"`).
- Estándar AWS: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` (leídas por el SDK directamente).

**Documentación**:
- Actualizar `.env` local del repo si existe con placeholders/comentarios seguros.
- Actualizar `.env.example` con las nuevas vars.
- Actualizar `CLAUDE.md` con el patrón `smsprovider`, `device_type="aws_aeum"` y soporte RCS/short codes vía pool.
- Actualizar `README.md` con configuración AEUM, variables, setup mínimo AWS, RCS/fallback SMS y short codes.

**Criterio de aceptación**:
- Arrancar Vendel con `AEUM_ENABLED=false`: no se crea AEUM device.
- Arrancar con `AEUM_ENABLED=true` y vars válidas: aparece un device `aws_aeum` en `sms_devices`. Reiniciar: no se crea uno segundo (idempotente).
- Intentar editar o borrar el AEUM device vía API o admin UI → 403.

---

### Fase 3 — Selección de provider en `SendSMS()`

**Objetivo**: integrar `AEUMProvider` en el flujo de envío. Implementa las correcciones de Fase -1 §-1.3 (selección), §-1.4 (UPDATE inmediato) y §-1.5 (dispatch separado).

**Archivos a tocar**:
- `backend/services/sms.go` — `SendSMS()` (línea 22) y `resolveDevices()` (línea 58).

**Archivos nuevos**:
- `backend/services/sms_provider_dispatch.go` — `partitionByProvider`, `DispatchProviderMessages` (`package services`).

**Lógica de selección** (`resolveDevices` reescrita según Fase -1 §-1.3):

```
resolveDevices(app, userId, deviceId, aeumProvider):
  if deviceId != "":
    device = FindRecordById(deviceId)
    if device.device_type == "aws_aeum":
      if !aeumProvider.IsConfigured(): error
      return [device] // global, OK
    if device.user != userId:
      error
    return [device]

  physical = FindRecordsByFilter("user={userId} && (fcm_token != '' || device_type='modem')")
  if len(physical) > 0:
    return physical

  // Fallback al AEUM global si AEUM está habilitado/configurado.
  if !aeumProvider.IsConfigured(): return nil
  aeum = FindFirstRecordByFilter("device_type='aws_aeum'")
  if aeum != nil:
    return [aeum]

  return nil // 404 al subir
```

**Dispatch separado** (Fase -1 §-1.5):

```
// Tras crear sms_messages y asignarles devices (round-robin existente):
physicalMessages, aeumMessages := partitionByProvider(messages, app)

if len(physicalMessages) > 0 {
    routine.FireAndForget(func() {
        DispatchMessages(app, physicalMessages) // sin cambios
    })
}
if len(aeumMessages) > 0 {
    routine.FireAndForget(func() {
        DispatchProviderMessages(app, aeumProvider, aeumMessages)
    })
}
```

**`DispatchProviderMessages`** (con UPDATE inmediato, Fase -1 §-1.4):

```go
func DispatchProviderMessages(app core.App, provider smsprovider.Provider, messages []*core.Record) {
    for _, m := range messages {
        req := smsprovider.SendRequest{
            MessageID: m.Id,
            UserID:    m.GetString("user"),
            To:        m.GetString("to"),
            Body:      m.GetString("body"),
            ChannelHint: "auto",
        }
        result, err := provider.Send(context.Background(), req)
        if err != nil {
            app.Logger().Error("provider send failed", "provider", provider.Name(), "msgId", m.Id, "err", err)
            continue
        }

        // CRÍTICO: persistir provider_message_id ANTES de disparar webhooks.
        m.Set("provider_message_id", result.ProviderMessageID)
        m.Set("provider_channel", result.ProviderChannel)
        m.Set("provider_origination_identity", result.OriginationIdentity)
        m.Set("status", result.Status)
        if result.Status == "sent" {
            m.Set("sent_at", types.NowDateTime())
        } else {
            m.Set("error_message", result.ErrorMessage)
        }
        if err := app.Save(m); err != nil {
            app.Logger().Error("persist provider result failed", "provider", provider.Name(), "msgId", m.Id, "err", err)
            continue
        }

        // Solo ahora: webhooks salientes.
        if result.Status == "sent" {
            TriggerWebhooks(app, m.GetString("user"), m, "sms_sent")
        } else {
            TriggerWebhooks(app, m.GetString("user"), m, "sms_failed")
        }
    }
}
```

**Criterio de aceptación** (todos los tests de Fase -1 §-1.7 "Fase 3" verdes):
- Con AEUM habilitado y `0 devices físicos`: POST `/api/sms/send` → envío real vía AEUM (sandbox AWS con número verificado).
- Con AEUM habilitado y `≥1 device físico online` (sin `device_id` en el request): envío vía FCM, no AEUM.
- Con `device_id` apuntando al AEUM device: envío vía AEUM aunque haya devices físicos.
- `sms_messages.provider_message_id` poblado **antes** de que `TriggerWebhooks` se invoque (test con spy en el orden de operaciones).
- `sms_messages.provider_channel` queda como `"auto"` al aceptar el envío AEUM; Fase 4 puede reemplazarlo por `"sms"` o `"rcs"` si el evento lo expone.
- Webhook `sms_sent` disparado tras envío exitoso vía AEUM.
- `notification.DispatchMessages` no fue modificado (test: greppear que el archivo solo cambia imports si acaso).

---

### Fase 4 — Delivery webhook (AEUM → SNS topic → Vendel)

**Objetivo**: recibir eventos de delivery de AEUM y actualizar el estado del `sms_messages`.

**Archivos nuevos**:
- `backend/handlers/aws_aeum_events.go` — `RegisterAEUMEventRoutes(se)`; recibe eventos transportados por SNS.
- `backend/services/smsprovider/sns_signature.go` — verificación de firma X.509 de SNS.
- `backend/services/smsprovider/sns_signature_test.go` — tests de verificación con fixtures.
- `backend/handlers/aws_aeum_events_test.go` — tests de los dos tipos de payload.

**Archivos a tocar**:
- `backend/main.go` — registrar las rutas.

**Endpoint**: `POST /api/webhooks/aws-aeum-events`

**Pseudocódigo**:

Aunque el endpoint se llama `aws-aeum-events`, el transporte es SNS. Por eso el header sigue siendo `x-amz-sns-message-type`, y la firma se valida con el certificado SNS indicado por `SigningCertURL`.

```go
func handleAEUMEvents(e *core.RequestEvent) error {
    body := readBody(e.Request)
    msgType := e.Request.Header.Get("x-amz-sns-message-type")

    // 1. Verificar firma SNS (X.509). Si falla → 401.
    if err := verifySNSSignature(body); err != nil {
        return apis.NewUnauthorizedError("invalid SNS signature", err)
    }

    switch msgType {
    case "SubscriptionConfirmation":
        // Hacer GET al SubscribeURL del payload para confirmar la suscripción.
        confirmSubscription(body.SubscribeURL)
        return e.JSON(200, map[string]string{"status": "confirmed"})

    case "Notification":
        event := parseAEUMEvent(body.Message)
        msg := findMessageByProviderID(event.MessageId)
        if msg == nil {
            // Fase -1 §-1.4: SNS NO reintenta si recibe 2xx. Lógica de frescura:
            eventTime := parseAEUMEventTimestamp(event)
            if time.Since(eventTime) < 5*time.Minute {
                // Race probable entre UPDATE de Fase 3 y este webhook.
                // 503 fuerza retry de SNS con backoff exponencial.
                return apis.NewApiError(503, "message not yet persisted; retry later", nil)
            }
            // Evento viejo: el UPDATE debería haber ocurrido. Algo está mal.
            app.Logger().Error("orphan SNS event", "messageId", event.MessageId, "eventTime", eventTime)
            return e.JSON(200, nil)
        }
        newStatus := mapAEUMEventToStatus(event.EventType)
        if newStatus == "" {
            // Eventos ignorados (TEXT_PENDING, TEXT_QUEUED, TEXT_SENT).
            return e.JSON(200, nil)
        }
        // Best-effort: si el payload AEUM incluye canal final o identity usada,
        // poblar provider_channel/provider_origination_identity.
        enrichProviderChannelFields(msg, event)
        updateMessageStatus(msg, newStatus, event)
        triggerCorrespondingWebhook(msg, newStatus)
        return e.JSON(200, nil)

    case "UnsubscribeConfirmation":
        // Loggear y aceptar; no esperado en operación normal.
        return e.JSON(200, nil)

    default:
        return apis.NewBadRequestError("unknown SNS message type", nil)
    }
}
```

**Mapeo de event types**:

| AEUM EventType | `sms_messages.status` | Webhook disparado |
|---|---|---|
| `TEXT_DELIVERED` | `delivered` | `sms_delivered` |
| `TEXT_SUCCESSFUL` | `delivered` | `sms_delivered` |
| `TEXT_TTL_EXPIRED` | `failed` | `sms_failed` |
| `TEXT_BLOCKED` | `failed` | `sms_failed` |
| `TEXT_CARRIER_UNREACHABLE` | `failed` | `sms_failed` |
| `TEXT_INVALID` | `failed` | `sms_failed` |
| `TEXT_UNKNOWN` | `failed` | `sms_failed` |
| `TEXT_UNREACHABLE` | `failed` | `sms_failed` |
| `TEXT_CARRIER_BLOCKED` | `failed` | `sms_failed` |
| `TEXT_PENDING` | (ignorar; no cambiar status) | ninguno |
| `TEXT_QUEUED` | (ignorar) | ninguno |
| `TEXT_SENT` | (ignorar; ya pusimos `sent` en Fase 3) | ninguno |

**RCS delivery events**:
- Si AWS usa event types RCS específicos en el payload de tu región/cuenta, mapear sus equivalentes delivered/success a `delivered`, failed/blocked/expired/unreachable a `failed`, y pending/queued/sent a ignorado.
- No inventar nombres de eventos en código. Implementar parser tolerante: primero mapear `TEXT_*`; luego, si el payload trae campos genéricos de canal/status, normalizar por `status`/`eventType`.
- Tests: incluir fixtures reales o representativos de AEUM para RCS si están disponibles en la cuenta AWS. Si no hay fixtures RCS, cubrir que eventos desconocidos se loggean y no rompen.

**Verificación de firma SNS** (resumen del algoritmo, ~50 líneas Go):
1. Validar que `SigningCertURL` es HTTPS y pertenece a un host SNS válido (`sns.<region>.amazonaws.com` o variante regional permitida por AWS).
2. Descargar el certificado X.509 (con cache para no repetir por request).
3. Construir la string canónica según el `Type` del mensaje (orden de campos definido por AWS).
4. Verificar la firma RSA-SHA256 contra el cert.

**Setup AWS** (documentar en `docs/aws-end-user-messaging-setup.md`):
1. AWS Console → End User Messaging → Phone Pools → crear pool, añadir números.
2. AWS Console → SNS → Topics → crear `vendel-sms-events`.
3. AWS Console → End User Messaging → Configuration Sets → crear `vendel-config-set`, añadir Event Destination apuntando al topic con todos los `TEXT_*` events.
4. AWS Console → SNS → Topic → Create subscription → Protocol: HTTPS, Endpoint: `https://<vendel-host>/api/webhooks/aws-aeum-events`.
5. Al arrancar Vendel con la URL pública configurada, SNS envía `SubscriptionConfirmation`; Vendel responde automáticamente.

**Criterio de aceptación** (incluye los tests obligatorios de Fase -1 §-1.7 "Fase 4"):
- Suscripción se auto-confirma sin intervención manual.
- Un envío real desde Fase 3 termina con `status=delivered` tras unos segundos (en sandbox AWS con número verificado).
- Notificación con firma inválida → 401, no se procesa.
- Notification con messageId desconocido y `event.Timestamp` reciente (<5 min) → **503** (no 200).
- Notification con messageId desconocido y `event.Timestamp` viejo (>=5 min) → 200 + log error.
- Notification con match + `TEXT_DELIVERED` o `TEXT_SUCCESSFUL` → `status=delivered`, webhook `sms_delivered` disparado.
- Notification con match + cualquier `TEXT_*` de fallo → `status=failed`, webhook `sms_failed` disparado.
- Notification con match + `TEXT_PENDING`/`TEXT_QUEUED`/`TEXT_SENT` → 200, sin cambios de estado.
- Si el payload AEUM expone canal final (`SMS`/`RCS`) u origination identity usada, se actualizan `provider_channel` y `provider_origination_identity` best-effort sin romper si esos campos no vienen.

---

### Fase 5 — Frontend

**Objetivo**: mostrar el AEUM device en la lista, con icono apropiado y badge de estado; no permitir edición/borrado desde la UI. Mantener el envío simple: el usuario selecciona "AWS End User Messaging" y escribe texto, sin tener que entender RCS, short codes o fallback.

**Archivos a tocar**:
- `frontend/src/types/collections.ts:30` — extender `DeviceType = "android" | "modem" | "aws_aeum"`.
- `frontend/src/components/Devices/columns.tsx:37` — añadir caso `aws_aeum`: icono `Cloud` de lucide, label `"AWS End User Messaging"`, badge "Activo" si está habilitado. Subtexto opcional corto: `"SMS / RCS texto / short codes"`. Sin badge online/offline (no aplica).
- `frontend/src/components/Devices/DeviceActionsMenu.tsx` (si existe) — ocultar Edit/Delete cuando `device.device_type === "aws_aeum"`.
- `frontend/src/routes/_layout/devices.tsx` — sin cambios funcionales; debe seguir listando todos los devices que devuelve la API (que ahora incluye el AEUM device global).

**NO modificar**:
- `frontend/src/components/Devices/AddDevice.tsx` — el wizard no añade AWS AEUM. El AEUM device lo crea el sistema. Esto es una decisión congelada en §2.

**Donde aparece la selección de device para enviar SMS** (si existe una página de envío manual): permitir seleccionar el AEUM device si el user lo ve en su lista.

**No añadir controles de canal en la UI**:
- No selector SMS/RCS.
- No selector de short code.
- No toggles de fallback.
- La configuración vive en AWS y Vendel solo muestra el gateway AEUM como provider simple.

**Comando recordatorio**: el package manager de este proyecto es **`bun`**.

**Criterio de aceptación**:
- En `/devices` aparece una fila "AWS End User Messaging" con icono Cloud cuando AEUM está habilitado.
- La fila comunica de forma breve que soporta SMS, RCS texto y short codes sin añadir configuración en pantalla.
- El menú de acciones de esa fila no muestra Edit ni Delete.
- TypeScript compila (`bun run build`).
- Biome pasa (`bun run lint`).

---

### Fase 6 — Tests E2E + documentación

**Archivos nuevos**:
- `docs/aws-end-user-messaging-setup.md` — guía paso a paso para el operador (AWS Console, env vars, validación).
- `frontend/tests/aws-aeum-device.spec.ts` (Playwright) — un test que verifica la presencia del AEUM device en la lista cuando el backend lo expone.

**Archivos a tocar**:
- `.env` — añadir placeholders/comentarios locales seguros para AEUM, sin secretos reales.
- `.env.example` — documentar todas las variables AEUM nuevas.
- `CLAUDE.md` — añadir sección breve sobre el patrón Provider en SMS, `device_type="aws_aeum"`, RCS/short codes vía pool y cómo añadir nuevos providers.
- `README.md` — añadir guía operativa de AEUM: variables, phone pool, short codes, RCS Agent, fallback SMS, delivery events y límites del MVP.

**Criterio de aceptación**:
- Documentación lo suficientemente completa para que un operador AWS-naïve pueda configurar todo en <30 min.
- `.env`, `.env.example`, `CLAUDE.md` y `README.md` quedan actualizados con las nuevas variables e información necesaria.
- `README.es.md` queda actualizado si el README en español sigue vigente.
- Se crea checklist/issue para actualizar `vendel-sdk-js`, `vendel-sdk-python`, `vendel-sdk-go` y `vendel-mcp`.
- El test E2E corre verde con el backend arrancado.

---

## 8. Variables de entorno

| Variable | Obligatoria si AEUM habilitado | Default | Descripción |
|---|---|---|---|
| `AEUM_ENABLED` | sí | `false` | Maestra: habilita/deshabilita toda la integración AEUM |
| `AEUM_REGION` | sí | — | Región AWS (ej. `us-east-1`) |
| `AWS_ACCESS_KEY_ID` | sí | — | Leída por el SDK |
| `AWS_SECRET_ACCESS_KEY` | sí | — | Leída por el SDK |
| `AEUM_ORIGINATION_IDENTITY_ARN` | sí | — | ARN del pool recomendado. El pool puede contener AWS RCS Agent, short code y números SMS fallback. |
| `AEUM_ORIGINATION_POOL_ARN` | no | — | Alias legacy opcional; cargar como fallback si `AEUM_ORIGINATION_IDENTITY_ARN` está vacío. |
| `AEUM_CONFIGURATION_SET_NAME` | sí | — | Nombre del ConfigurationSet en AEUM (para event destinations) |
| `AEUM_CHANNEL_MODE` | no | `auto` | Solo `auto` en MVP: AWS decide RCS/SMS/origination desde el pool. |
| `AEUM_DEVICE_NAME` | no | `"AWS End User Messaging"` | Nombre visible del AEUM device en la UI |

**Regla de simplicidad**: no añadir variables separadas como `AEUM_ENABLE_RCS` o `AEUM_SHORT_CODE`. Si RCS o short code están en el pool, Vendel los soporta automáticamente. La única configuración necesaria en Vendel es el ARN del pool/identity y el ConfigurationSet.

**Mínimo IAM policy** para el usuario AWS (documentar):
```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["sms-voice:SendTextMessage"],
      "Resource": "*"
    }
  ]
}
```

---

## 9. Checklist de setup AWS (para el operador, documentado en `docs/aws-end-user-messaging-setup.md`)

1. [ ] Crear IAM user `vendel-aeum` con la policy mínima de arriba.
2. [ ] Generar Access Key + Secret; guardar en `.env` de Vendel.
3. [ ] AWS Console → End User Messaging → Phone Pools → crear pool por caso de uso.
4. [ ] Para SMS normal: añadir al pool al menos un número/origination identity aprobado.
5. [ ] Para short code: solicitar/aprobar el short code y añadirlo al pool.
6. [ ] Para RCS: crear/aprobar AWS RCS Agent y añadirlo al pool junto con un número SMS fallback si se quiere fallback automático.
7. [ ] Copiar el ARN del pool a `AEUM_ORIGINATION_IDENTITY_ARN`.
8. [ ] AWS Console → SNS → Topics → crear `vendel-sms-events`.
9. [ ] AWS Console → End User Messaging → Configuration Sets → crear `vendel-config-set`.
10. [ ] En el ConfigurationSet, añadir Event Destination → SNS topic `vendel-sms-events` → seleccionar eventos `TEXT_*` y cualquier evento RCS equivalente disponible en la consola/API.
11. [ ] Copiar el nombre del ConfigurationSet a `AEUM_CONFIGURATION_SET_NAME`.
12. [ ] AWS Console → SNS → Topic `vendel-sms-events` → Create subscription → Protocol HTTPS, Endpoint `https://<vendel-host>/api/webhooks/aws-aeum-events`.
13. [ ] Si la cuenta está en sandbox: AWS Console → End User Messaging → solicitar salida del sandbox antes de producción.
14. [ ] (Recomendado) En End User Messaging, configurar un **Monthly spend limit** para evitar facturas inesperadas.
15. [ ] Arrancar Vendel con `AEUM_ENABLED=true`. La suscripción SNS se auto-confirma en el primer mensaje.

---

## 10. Referencias AWS para RCS y short codes

- RCS overview: `https://docs.aws.amazon.com/sms-voice/latest/userguide/rcs-overview.html`
- RCS to SMS fallback with pools: `https://docs.aws.amazon.com/sms-voice/latest/userguide/rcs-sms-fallback.html`
- Phone number/origination identity types, including short codes: `https://docs.aws.amazon.com/sms-voice/latest/userguide/phone-number-types.html`
- Requesting dedicated short codes: `https://docs.aws.amazon.com/sms-voice/latest/userguide/phone-numbers-request-short-code.html`

---

## 11. API pública, SDKs y MCP

### 11.1 API pública

Mantener compatibilidad absoluta con clientes existentes. No renombrar endpoints ni métodos aunque AEUM pueda entregar RCS texto.

Endpoints existentes:
- `POST /api/sms/send`
- `POST /api/sms/send-template`

Payload actual sigue válido:

```json
{
  "recipients": ["+15551234567"],
  "body": "Hola",
  "device_id": "optional",
  "group_ids": []
}
```

Añadir campos opcionales:

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
- En MVP solo se implementa `"auto"`.
- Si el caller envía `"sms"` o `"rcs"`, responder 400 con mensaje claro: `"channel only supports auto in this version"`.
- No añadir `short_code` al payload. El short code vive en el pool AWS.
- No añadir endpoint `/api/rcs/send`. RCS texto usa el mismo endpoint.

Respuesta: mantener campos actuales y añadir solo campos opcionales, nunca obligatorios:

```json
{
  "batch_id": "...",
  "message_ids": ["..."],
  "recipients_count": 1,
  "status": "accepted",
  "provider": "aws_aeum",
  "channel": "auto"
}
```

Si hay riesgo de romper SDKs tipados por respuesta estricta, dejar `provider` y `channel` fuera de la respuesta inicial y exponerlos solo al consultar `sms_messages`. KISS preferido: aceptar `channel` en request, persistir en message, no depender de respuesta extendida.

### 11.2 `sms_messages` expuesto por API

Actualizar tipos/serialización para incluir:
- `provider_message_id?: string`
- `provider_channel?: "auto" | "sms" | "rcs" | "unknown"`
- `provider_origination_identity?: string`

Estos campos son informativos. Las integraciones no deben necesitarlos para enviar.

### 11.3 SDKs oficiales

Repos externos a actualizar después del backend:

| SDK | Repo | Cambios |
|---|---|---|
| JavaScript/TypeScript | `vendel-sdk-js` | `sendSMS({ channel?: "auto" })`, `sendTemplate({ channel?: "auto" })`, tipos de `SMSMessage` con campos provider. |
| Python | `vendel-sdk-python` | Parámetro opcional `channel="auto"` en send, modelo de mensaje con campos provider. |
| Go | `vendel-sdk-go` | `SendSMSRequest.Channel string \`json:"channel,omitempty"\``, constantes de canal, campos provider en `SMSMessage`. |

Reglas para SDKs:
- No romper llamadas existentes.
- `channel` debe ser opcional.
- Documentar que `channel="auto"` permite SMS/RCS texto/short code según pool AEUM.
- No agregar método `sendRCS` todavía.
- No agregar parámetro `shortCode`.
- Tests de SDK: llamadas antiguas siguen serializando igual; llamadas con `channel:"auto"` serializan el campo.

### 11.4 MCP (`vendel-mcp`)

Actualizar el servidor MCP externo `vendel-mcp` después del backend:

- Tool de envío SMS mantiene el mismo nombre.
- Añadir argumento opcional `channel` con default `"auto"`.
- Descripción del tool: “Send a text message. Depending on configured gateways, Vendel may deliver it as SMS, RCS text, short code SMS, SMPP, or physical-device SMS.”
- No exponer short code ni RCS-specific controls.
- Tool/schema de lectura de mensajes debe devolver `provider_channel` y `provider_origination_identity` si existen.
- Actualizar README del MCP con ejemplos:
  - envío normal sin `channel`
  - envío con `channel: "auto"`
  - explicación de que RCS/short code dependen del pool AEUM.

### 11.5 Documentación de usuario

Actualizar en este repo:
- `README.md`: sección “Gateways externos” con AWS AEUM, SMS/RCS texto/short codes, env vars, limitaciones.
- `README.es.md`: misma sección en español si se mantiene paridad.
- `docs/aws-end-user-messaging-setup.md`: guía operador paso a paso.
- `CLAUDE.md`: notas para agentes sobre `smsprovider`, `channel=auto`, `aws_aeum`, SDKs y MCP.
- `.env` y `.env.example`: variables nuevas con comentarios seguros.

---

## 12. Diseño para "credenciales por usuario" (futuro, NO implementar en este plan)

Para que el día de mañana puedas añadir credenciales-por-usuario sin refactor de firmas:

1. **`SendRequest.UserID` ya está**: el resolver de credenciales lo usará para hacer query a la BD.
2. **Refactor futuro mínimo**: crear `UserAEUMProvider` con la misma interface. Selector elige `GlobalAEUMProvider` o `UserAEUMProvider(userID)` según haya credenciales del user.
3. **Persistencia futura**: nueva colección `sms_provider_credentials` con `user`, `provider_type`, `access_key`, `secret_key_encrypted`, `region`, `pool_arn`, `config_set_name`. Cifrado simétrico al guardar (AES-GCM con clave maestra en env).
4. **NO crear esta colección hoy**. Ni siquiera vacía.

Este diseño es deliberadamente fino: la única "deuda anticipada" es aceptar `UserID` en `SendRequest`. Coste hoy: 0.

---

## 13. Riesgos y mitigaciones

| Riesgo | Mitigación |
|---|---|
| AEUM Sandbox limita a números verificados | Documentar en setup; operador sale de sandbox antes de producción |
| URL pública de Vendel no es HTTPS o no es accesible | Pre-check al startup: si `AEUM_ENABLED=true` pero no hay URL pública configurada, log error claro y fallar el startup |
| Firma SNS no validada → spoofing del webhook | Validar firma X.509 en Fase 4, sin opt-out |
| Phone pool vacío → 100% de envíos fallan | Log claro del error de AEUM; no intentar reintento agresivo |
| Costos disparados | Spend limit configurado **en AWS Console**, no en código |
| Race condition: webhook llega antes que el UPDATE de Fase 3 que pone `provider_message_id` | Si el evento sin match es reciente: responder 503 para forzar retry de SNS. Si es viejo: 200 + log error. |
| Cache del certificado X.509: fuga si AWS rota | Cache con TTL corto (ej. 1h) por URL del cert |
| `Unsubscribe` accidental del SNS topic | Política IAM del topic restringe Unsubscribe a admins; documentar |

---

## 14. Criterios de aceptación globales (cuando todas las fases están hechas)

- [ ] `go build ./...` pasa.
- [ ] `bun run build` pasa.
- [ ] `bun run lint` pasa.
- [ ] Tests unitarios de `services/smsprovider/...` pasan (incluye AEUMProvider y verificación de firma) y tests de dispatch en `services` pasan.
- [ ] Tests E2E pasan.
- [ ] Con `AEUM_ENABLED=false`: comportamiento idéntico al pre-cambios (regresión cero).
- [ ] Con `AEUM_ENABLED=true` y devices físicos online: comportamiento idéntico al pre-cambios.
- [ ] Con `AEUM_ENABLED=true` sin devices físicos: envío exitoso vía AEUM en cuenta AWS sandbox con número verificado.
- [ ] Con pool que contiene short code aprobado: Vendel puede enviar texto sin cambios de API ni UI.
- [ ] Con pool que contiene AWS RCS Agent aprobado: Vendel puede enviar RCS texto; si el pool tiene fallback SMS, AWS puede caer a SMS sin intervención de Vendel.
- [ ] Delivery webhook actualiza `sms_messages.status` correctamente para los 3 caminos (`delivered`, `failed`, ignorados).
- [ ] `provider_channel` queda como `auto` al aceptar y se actualiza best-effort a `sms`/`rcs` si AEUM lo reporta.
- [ ] `provider_origination_identity` guarda el pool/identity usado best-effort.
- [ ] El AEUM device aparece en la UI con icono y badge, no editable, no eliminable.
- [ ] `docs/aws-end-user-messaging-setup.md` permite a un operador AWS-naïve completar el setup en <30 min.
- [ ] `.env`, `.env.example`, `CLAUDE.md` y `README.md` documentan AEUM, RCS, short codes y las variables nuevas.
- [ ] API mantiene compatibilidad: payloads antiguos de `/api/sms/send` y `/api/sms/send-template` siguen funcionando.
- [ ] `channel` opcional default `auto` está documentado y testeado.
- [ ] SDKs oficiales JS/Python/Go tienen issue/PR o checklist de actualización con tipos provider y `channel`.
- [ ] `vendel-mcp` tiene issue/PR o checklist de actualización para `channel` opcional y campos provider.

**Tests bloqueantes específicos de Fase -1 (no opcionales)**:

- [ ] Crear device físico sin `user` → 400 con mensaje claro (Fase -1 §-1.1).
- [ ] Crear/borrar AEUM device no altera `device_count` (Fase -1 §-1.2).
- [ ] Crear AEUM device vía API pública → 403; solo bootstrap server-side lo crea (Fase -1 §-1.2).
- [ ] Update/delete del AEUM device vía API → 403 (regla + hook) (Fase -1 §-1.2).
- [ ] Usuario sin devices físicos + AEUM off → 404 "no device" (Fase -1 §-1.3).
- [ ] Usuario sin devices físicos + AEUM on → envío vía AEUM (Fase -1 §-1.3).
- [ ] `device_id` apuntando al AEUM device aunque el `user` sea distinto → permitido (Fase -1 §-1.3).
- [ ] `provider_message_id` persistido **antes** de `TriggerWebhooks` (Fase -1 §-1.4).
- [ ] Webhook con messageId desconocido reciente → 503 (Fase -1 §-1.4).
- [ ] Webhook con messageId desconocido viejo → 200 + log (Fase -1 §-1.4).
- [ ] `notification.DispatchMessages` no modificado para AEUM (Fase -1 §-1.5).
- [ ] Índice `idx_sms_messages_provider_message_id` existe en BD (Fase -1 §-1.6).

---

## 15. Out of scope (explícitamente NO en este plan)

- Credenciales-por-usuario.
- Cifrado de secrets en BD.
- Dashboard de costos / métricas de costo en UI.
- Múltiples phone pools / multi-región.
- Implementar otros providers (SMPP, Twilio, MessageBird, Vonage). SMPP tiene su propio plan en `docs/smpp-provider-plan.md`.
- MMS.
- RCS rich cards, media, carousels, suggested replies o acciones enriquecidas.
- Selección manual de canal/origination identity por request.
- Métodos SDK específicos `sendRCS` / `sendShortCode`.
- Reintentos personalizados para envíos AEUM (el SDK ya hace exponential backoff).
- UI de configuración de AEUM (todo por env vars).
- Tests de carga / benchmark de AEUM.
- Migración del AEUM device entre regiones AWS.

---

## 16. Glosario

- **AEUM**: AWS End User Messaging. Servicio AWS sucesor de Pinpoint SMS and Voice v2 y de SNS SMS legacy. El SDK Go conserva el nombre `pinpointsmsvoicev2`.
- **Phone pool**: agrupación de números de origen en AEUM. Permite balancear envíos y aplicar configuraciones uniformes.
- **ConfigurationSet**: en AEUM, conjunto de reglas que aplica a envíos (event destinations, defaults). Equivalente a un "preset" de envío.
- **Event Destination**: salida de eventos de un ConfigurationSet. Puede ser SNS topic, EventBridge, Kinesis, CloudWatch.
- **SubscriptionConfirmation**: mensaje que SNS envía al endpoint HTTPS cuando se crea la suscripción. El endpoint debe hacer GET al `SubscribeURL` para confirmar.
- **Origination identity**: el "from" del SMS desde el punto de vista de AEUM. Puede ser un número (long code, short code, toll-free) o un Sender ID, agrupados en pools.
- **AWS RCS Agent**: identidad RCS verificada en AWS. En MVP se usa dentro de un pool para RCS texto con fallback SMS.
- **Short code**: origination identity SMS de alto volumen. En Vendel se usa dentro del pool AEUM; no requiere lógica especial si AWS lo enruta.
- **`sms_devices` con `device_type=aws_aeum`**: la representación interna de AEUM como "device virtual" en la BD de Vendel. Un único registro global, no editable por el user.
---

## 17. Orden de ejecución sugerido

0. **Fase -1**: NO produce commits propios — sus decisiones quedan repartidas:
   - §-1.1, §-1.2, §-1.6 → se materializan en la migración + hooks de **Fase 2**.
   - §-1.3, §-1.4, §-1.5 → se materializan en `resolveDevices` y `DispatchProviderMessages` de **Fase 3**.
   - §-1.7 (tests) → se distribuyen en las fases 2, 3 y 4 respectivamente.
   - Antes de codear: re-leer Fase -1 entera y marcar mentalmente cada decisión.
1. Fase 0 → commit y revisar.
2. Fase 1 → commit y revisar (tests verdes).
3. Fase 2 → commit y revisar (migración aplicada en local, AEUM device aparece, hooks bloquean update/delete, cuotas intactas).
4. Fase 3 → commit y revisar (envío real funcionando en sandbox AWS, partición FCM/AEUM limpia, UPDATE de `provider_message_id` ocurre antes del TriggerWebhooks).
5. Fase 4 → commit y revisar (delivery webhook funcional end-to-end, semántica 503/200 verificada).
6. Fase 5 → commit y revisar (UI completa).
7. Fase 6 → commit final con docs y tests E2E.
8. Post-backend → actualizar SDKs oficiales y `vendel-mcp` según §11.

Cada commit debe seguir las convenciones del repo (formato Biome aplicado en el mismo commit que el código, español en mensajes, atribución única autor a menos que se indique lo contrario).
