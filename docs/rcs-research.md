# Investigación: integración de RCS en Vendel

> Fecha de la investigación: 2026-05-15
> Estado: análisis técnico, sin código todavía
> Autor: Jimmy Angel Pérez Díaz (con asistencia de Claude)

## Resumen ejecutivo (TL;DR)

- **No existe una API oficial de Android (`ImsRcsManager`) que permita a una app de terceros enviar mensajes RCS.** Solo expone consultas de capacidad y registro; no tiene `sendMessage()`.
- **El API oculta de Google Messages (`EXTERNAL_MESSAGING_API`) existe pero está restringida por verificación de firma del APK** a un allowlist (Samsung Continuity). Esquivarla requiere root + signature spoofing + reverse-engineering del AIDL, frágil y caro de mantener.
- **La ruta legal vía RBM (RCS Business Messaging)** funciona pero cambia el modelo de Vendel: pasas de gateway distribuido a marca verificada por carriers + agregador. Descartado por el usuario.
- **Ruta elegida**: usar `mautrix/gmessages` (`pkg/libgm`), un cliente Go reverse-engineered del protocolo de Google Messages for Web. Activamente mantenido, con releases mensuales y soporte RCS bidireccional completo (envío, recepción, replies, reacciones, typing, lectura, media, grupos).
- **Caveat crítico vigente hoy**: tras la retirada del QR pairing por Google, el método GAIA está deslogueando cada 1–2 horas (issue #57). Hay fork de la comunidad (`rusty4444/gmessages-matrix`) y PR #59 abierto con fix. Hasta que se merge, no es production-ready con la versión upstream.
- **Licencia**: `mautrix/gmessages` es AGPL-3.0. Para preservar la licencia de Vendel hay que aislar `libgm` en un proceso separado (`gmessages-agent/`) que se comunica con el backend por API local, similar al patrón actual del `modem-agent`.

---

## 1. Por qué RCS es difícil

RCS no es como SMS. SMS es un protocolo de capa de red (SS7/MAP) que cualquier baseband o modem puede emitir. RCS es esencialmente un cliente IMS/SIP + HTTPS que requiere:

1. **Aprovisionamiento contra el servidor RCS del operador** (o contra Jibe de Google si el operador delega).
2. **Registro IMS con credenciales de la línea**.
3. **Un cliente RCS instalado y firmado como app del sistema o reemplazo del cliente RCS del fabricante.**

Por eso un modem USB no puede enviar RCS como envía SMS, y una app de Android instalada por el usuario tampoco.

### 1.1 `ImsRcsManager` (API 30+)

Pese al nombre prometedor, `android.telephony.ims.ImsRcsManager` solo expone:

- `getRegistrationState()` — saber si el dispositivo está registrado en IMS para RCS.
- `registerImsRegistrationCallback()` — escuchar cambios de registro.
- `getUceAdapter()` — descubrir capacidades RCS de otros contactos (User Capability Exchange).
- `isAvailable(capability, radioTech)` — comprobar si una capacidad RCS está disponible.

**No tiene `sendMessage(...)`.** Y aun lo poco que expone requiere permisos no concedibles a apps de terceros:

- `READ_PRIVILEGED_PHONE_STATE`
- `PERFORM_IMS_SINGLE_REGISTRATION`
- **Carrier privileges** (firma cargada en la SIM del operador)

En la práctica, solo apps de sistema o firmadas por el operador los cumplen.

### 1.2 `EXTERNAL_MESSAGING_API` (Google Messages)

Existe un permiso oculto: `com.google.android.apps.messaging.EXTERNAL_MESSAGING_API`. Permite a otra app delegar envío SMS/MMS/RCS en Google Messages. Pero:

- Hay **verificación de firma del APK** vía `PackageManager.GET_SIGNING_CERTIFICATES`, no solo de package name.
- El allowlist es Samsung Continuity (`com.samsung.android.mdecservice`).
- Existe el flag `allow_any_app_to_connect_do_not_use_in_public_builds` con valor `false` en builds firmadas por Google.

Sortearlo requeriría:

1. Root + Magisk + Zygisk + LSPosed.
2. Módulo de signature spoofing (`CorePatch`, `XSpoofSignatures`, `Haruka`).
3. Reverse-engineering del AIDL oculto entre `com.google.android.apps.messaging` y `com.samsung.android.mdecservice`.
4. Mantenimiento continuo: Google rota el AIDL en cada release mayor.

Descartado por brittle y caro de mantener.

### 1.3 RBM (RCS Business Messaging)

API oficial de Google: `https://rcsbusinessmessaging.googleapis.com`. Requiere:

- Service Account con OAuth2.
- Registrar un *agent* y pasar verificación de marca → carriers → Google (semanas a meses).
- Persona jurídica, branding, dirección verificable.

Pricing referencia (vía agregadores tier-1, mayo 2026):

| Proveedor | Texto | Multimedia |
|---|---|---|
| Twilio | ~$0.0083 | ~$0.022 |
| Sinch | desde $0.0078 | variable |
| Vonage | setup $5 000 + $1 000/mes (Conversational Commerce) | — |

Descartado por el usuario (no quiere business API).

---

## 2. Rutas no oficiales evaluadas

### 2.1 Selenium / Playwright sobre Messages for Web

Browser headless emparejado al teléfono. Funciona y envía RCS real. Pero:

- Google está eliminando QR pairing en 2026 → habrá que automatizar login OAuth.
- Frágil al scraping del DOM.
- Pesado en recursos (browser headless por gateway).
- Mantenimiento continuo.

Reemplazada por la ruta del cliente nativo (`libgm`), estrictamente mejor en todo.

### 2.2 LSPosed inyectado dentro de Google Messages

Módulo Xposed que se inyecta en el proceso `com.google.android.apps.messaging` y llama a clases internas para enviar RCS, saltando el chequeo de allowlist desde dentro.

- Requiere root + Magisk + LSPosed en cada gateway.
- Nombres de método ofuscados → hay que mapear con Smali + Frida.
- Cada release de Google Messages puede romperlo.
- Adiós a Play Integrity.

Viable solo si Vendel acepta gateways rooteados dedicados. Plan B.

### 2.3 Spoof de firma para `EXTERNAL_MESSAGING_API`

Cumple los requisitos formales para invocar el AIDL, pero el AIDL no es público — hay que reconstruirlo desde Smali. Y el flag `allow_any_app_to_connect_do_not_use_in_public_builds` sigue en `false`, así que además hay que parchear el APK de Google Messages. **No la recomiendo: cuesta lo mismo que la ruta LSPosed con peor resultado.**

### 2.4 APK patcheado de Google Messages

Recompilar Google Messages con el allowlist eliminado, firmar con clave propia, distribuir. Hay que rehacerlo cada release (semanal-mensual). Rompe Play Integrity de cualquier app del device. Pierdes actualizaciones automáticas. Descartado.

### 2.5 Cliente RCS-e directo (`rcsjta` style)

Implementar la pila RCS-e GSMA contra el operador. Requiere:

- Aprovisionamiento HTTP config del operador + credenciales IMS.
- Correr como app de sistema o reemplazo del cliente RCS de fábrica.

`android-rcs/rcsjta` existe pero su última actividad significativa es de 2017-2019. Sin uso práctico hoy.

---

## 3. Ruta elegida: `mautrix/gmessages` (`pkg/libgm`)

Cliente Go del protocolo de Google Messages for Web, reverse-engineered y activamente mantenido por Tulir Asokan (mautrix). Permite que cualquier proceso actúe como "device emparejado" igual que lo hace un browser en messages.google.com/web.

### 3.1 Estado del repo (verificación, 2026-05-15)

| Métrica | Valor |
|---|---|
| Lenguaje | Go 99.4% |
| Licencia | AGPL-3.0 (con `LICENSE.exceptions`) |
| Última versión | v26.04 (2026-04-16) |
| Cadencia de releases | Mensual, día 16 |
| `pushed_at` | 2026-05-13 |
| Stars | 147 |
| Issues abiertos | 13 |
| Mínimo Go | 1.25.0 |
| Versionado | Calendario desde v25.10 |

### 3.2 Estructura del paquete `pkg/libgm`

```
pkg/libgm/
├── client.go          ← Client, AuthData, evento loop
├── methods.go         ← API pública de envío/lectura
├── pair.go            ← QR pairing (deprecating en 2026)
├── pair_google.go     ← GAIA (Google account) pairing
├── http.go            ← HTTP transport con cookies + SAPISID
├── longpoll.go        ← Long-poll para recibir
├── session_handler.go ← Sesión persistente y reconexión
├── media.go           ← Upload/download media RCS
├── event_handler.go   ← Decryption + dispatch a EventHandler
├── crypto/            ← AES-CTR + JWK (serializable)
├── events/            ← Event structs (ClientReady, GaiaLoggedOut...)
└── gmproto/           ← .proto + .pb.go generados
```

**`libgm` está desacoplado del puente Matrix.** Puede importarse como librería Go independiente desde cualquier binario:

```go
import "go.mau.fi/mautrix-gmessages/pkg/libgm"
```

### 3.3 API pública confirmada (`methods.go`)

| Método | Para qué |
|---|---|
| `SendMessage(*gmproto.SendMessageRequest)` | Enviar texto / media a una conversación |
| `SendReaction(*gmproto.SendReactionRequest)` | Reaccionar a un mensaje RCS |
| `DeleteMessage(messageID string)` | Borrar un mensaje (own device only) |
| `MarkRead(convID, msgID string)` | Marcar leído |
| `SetTyping(convID string, sim *SIMPayload)` | Typing indicator |
| `GetOrCreateConversation(...)` | Iniciar 1:1 o grupo |
| `ListConversations(count, folder)` | Hidratar inbox |
| `FetchMessages(convID, count, cursor)` | Backfill paginado |
| `GetFullSizeImage(...)` | Pedir versión completa de imagen |
| `UpdateConversation(...)` | Archivar, borrar, marcar spam |
| `ListContacts()` / `ListTopContacts()` | Resolución de contactos |

### 3.4 Modelo de datos relevante (`gmproto/conversations.proto`)

```protobuf
enum ConversationType {
    UNKNOWN_CONVERSATION_TYPE = 0;
    SMS = 1;
    RCS = 2;
}
```

Hay status codes ricos diferenciando RCS, RCS E2EE, MMS, SMS:

- `OUTGOING_FAILED_RECIPIENT_LOST_RCS`
- `OUTGOING_FAILED_RECIPIENT_DID_NOT_DECRYPT`
- `TOMBSTONE_PROTOCOL_SWITCH_TO_RCS`
- `TOMBSTONE_PROTOCOL_SWITCH_TO_ENCRYPTED_RCS`
- `TOMBSTONE_ENCRYPTED_ONE_ON_ONE_RCS_CREATED`
- (y varios más)

El protocolo distingue de forma nativa SMS, RCS normal, RCS E2EE y MMS.

---

## 4. Verificación de envío RCS

`SendMessage` envía un `ActionType_SEND_MESSAGE` por el canal cifrado al teléfono pareado. Google Messages en el teléfono lo procesa **igual que si el usuario escribiera desde el web**:

- Si el destinatario tiene RCS habilitado y compatible → se envía como **RCS real** (incluyendo E2EE en Universal Profile 2.x).
- Si no → fallback automático a SMS/MMS por parte de Google Messages, no por nuestro código.

**No se pide RCS explícitamente.** El cliente envía un "mensaje lógico" y el teléfono decide el transporte según settings y capacidades del destinatario. Es el mismo modelo de Messages for Web.

**Implicación**: no controlamos directamente "envía esto como SMS aunque el destinatario tenga RCS"; lo decide la app del teléfono. Si necesitamos esa granularidad habría que tocar settings de Google Messages remotamente, lo cual `libgm` sí permite (`UpdateSettings`).

---

## 5. Verificación de recepción RCS

`libgm` mantiene una conexión HTTP long-polling con los servidores de Google. Cuando llega un evento, `decryptInternalMessage` decodifica con `AESCTRHelper` y dispara `triggerEvent(...)`.

### 5.1 Eventos disponibles (`events/ready.go`)

- `events.PairSuccessful{PhoneID, QRData}` — pairing OK
- `events.ClientReady{SessionID, Conversations}` — sesión lista con snapshot
- `events.AccountChange` — cambio de cuenta Google
- `events.GaiaLoggedOut` — deslogueado
- `events.AuthTokenRefreshed` — token Tachyon renovado
- `events.NoDataReceived` — heartbeat sin datos
- `events.PhoneNotResponding` / `events.PhoneRespondingAgain`
- `events.PingFailed{Error, ErrorCount}`
- `events.ListenFatalError` / `events.ListenTemporaryError` / `events.ListenRecovered`

Mensajes nuevos llegan como `gmproto.UpdateEvents`.

### 5.2 Sub-features RCS confirmados (todos `[x]` en ROADMAP.md)

Dirección Google Messages → Matrix (= dirección "recepción" para Vendel):

- [x] Texto plano
- [x] Media / archivos
- [x] Replies (citas)
- [x] Reacciones
- [x] Typing notifications
- [x] Read receipts en 1:1
- [x] Read receipts en grupos
- [x] Mensajes borrados (own device only)

Dirección Matrix → Google Messages (= dirección "envío" para Vendel): mismo set + `Reactions`, `Typing`, `Read receipts` confirmados.

Misceláneo:

- [x] Creación automática de portales tras login
- [x] Creación automática al recibir mensaje
- [x] Avatares de grupos RCS (añadidos en v26.02, feb 2026)
- [x] Creación de grupos (añadida en v0.7.0, sept 2025)

---

## 6. Pairing y autenticación

### 6.1 QR pairing (`pair.go`) — siendo retirado

`StartLogin()` → `RegisterPhoneRelay()` → genera QR → usuario lo escanea en su Google Messages → llega `RPCPairData_Paired` → `completePairing(data)`.

Google está eliminando QR pairing en 2026.

### 6.2 GAIA / Google Account (`pair_google.go`) — método actual

Más complejo. Hace OAuth contra `SignInGaiaURL` con el `RefreshKey`, deriva el `TachyonAuthToken` y registra el device como `messages-web-<sessionID>`. Es el método que sobrevive.

### 6.3 Serialización de la sesión

`AuthData` es un struct JSON-serializable con:

```go
type AuthData struct {
    RequestCrypto    *crypto.AESCTRHelper
    RefreshKey       *crypto.JWK
    Browser          *gmproto.Device
    Mobile           *gmproto.Device
    TachyonAuthToken []byte
    TachyonExpiry    time.Time
    TachyonTTL       int64
    SessionID        uuid.UUID
    DestRegID        uuid.UUID
    PairingID        uuid.UUID
    Cookies          map[string]string
    // ...
}
```

→ Permite pairing una vez, serializar a JSON y guardar (PocketBase / Android `EncryptedSharedPreferences`). Encaja con el modelo de credenciales por device que Vendel ya tiene.

### 6.4 Problema crítico vigente: issue #57

Reportado 2026-04-26, **abierto** a fecha de esta investigación.

> "longest is 2h of session until the connection is lost/timedout/unauthorized. On my devices messages app, it still shows 'mautrix-messages' as paired device, but displayed 'last active time' is from the day, I tried last time. In the meanwhile I'd have received several messages, so it is kind of linked and registered, but within matrix it's not linked anymore."

Múltiples usuarios confirman desconexión cada 1–2 horas tras la retirada del QR pairing por Google.

**Mitigaciones reportadas**:

1. Hacer el login vía la herramienta `mautrix-manager` → varios usuarios reportan sesiones estables "una semana o más". Sugiere que el bug está en la captura inicial de cookies, no en el sostenimiento.
2. Fork `rusty4444/gmessages-matrix` con patch → "12 horas+ estable". PR #59 abierto en upstream 2026-05-02, **no mergeado aún**.

**Implicación para Vendel**: hoy no es production-ready con la versión upstream v26.04 sin aplicar el patch del fork o adoptar el flujo de login de `mautrix-manager`.

---

## 7. Histórico relevante (CHANGELOG)

- **v26.04** (2026-04-16): fix ghosts names, configurable phone ping interval.
- **v26.02** (2026-02-16): Go 1.25, group avatar support en grupos RCS.
- **v26.01** (2026-01-16): media embebida en mensajes recibidos.
- **v25.11** (2025-11-16): mejores mensajes de error de login, sin límite MMS.
- **v25.10** (2025-10-16): switch a calendar versioning.
- **v0.7.0** (2025-09-16): typing notifications bidireccional + read receipts en grupos + creación de grupos.
- **v0.6.5** (2025-08-16): deprecó legacy provisioning API.
- **v0.6.0** (2024-12-16): re-autenticación de logins Google expirados sin re-pair.

Soporte RCS pleno y maduro (no es POC).

---

## 8. Arquitectura propuesta para Vendel

### 8.1 Restricción de licencia

`libgm` es **AGPL-3.0**. Si Vendel embebe `pkg/libgm` directamente:

- El backend de Vendel se vuelve AGPL-3.0.
- Obligación de publicar fuente a cualquier usuario que interactúe por red.

**Salida**: correr `libgm` como **proceso separado** que expone API local (HTTP/gRPC) al backend Vendel. Preserva la frontera AGPL ↔ Vendel mientras el binario AGPL no esté linkeado estáticamente al backend.

Esto encaja con el patrón actual del `modem-agent`.

### 8.2 Estructura propuesta

```
vendel/
├── backend/                           ← Vendel core, licencia actual
│   └── services/sms/
│       └── transports/
│           ├── fcm.go                 (existente)
│           └── gmessages.go           (nuevo: cliente HTTP del agent)
│
├── gmessages-agent/                   ← NUEVO, AGPL-3.0, aislado
│   ├── main.go                        (importa pkg/libgm, expone API local)
│   ├── go.mod                         (require go.mau.fi/mautrix-gmessages)
│   ├── handlers/
│   │   ├── pair.go                    (inicia pairing GAIA)
│   │   ├── send.go                    (POST /send → libgm.SendMessage)
│   │   └── events.go                  (long-poll → webhook a Vendel)
│   └── Dockerfile                     (perfil compose nuevo)
│
└── modem-agent/                       ← sin cambios
```

### 8.3 Flujo end-to-end

1. **Onboarding**: el operador empareja su Google Account contra el agent (POST `/pair`). El agent ejecuta el flow GAIA y devuelve `AuthData` JSON cifrado.
2. **Persistencia**: el `AuthData` se guarda en `devices.auth_blob` (campo nuevo, cifrado en reposo).
3. **Envío**: Vendel core recibe job de SMS → si `device.type == "gmessages"` → llama al agent (`POST /send {to, body, conversation_id?}`).
4. **Carga de cliente**: el agent carga `AuthData`, instancia `*libgm.Client`, ejecuta `SendMessage`, devuelve `message_id` + `status`.
5. **Eventos entrantes**: el agent mantiene long-poll. Cuando llega un mensaje, golpea webhook de Vendel (`POST /api/sms/incoming`) reutilizando lo que ya existe.
6. **Reconexión**: el agent monitorea `events.GaiaLoggedOut` y notifica a Vendel para que ponga el device en `needs_reauth`.

### 8.4 Dónde corre el agent

**Opción A — En el servidor Vendel** (centralizado):

- Una instancia por device gateway, cargando el `AuthData` correspondiente.
- Más simple, no requiere instalar nada en el teléfono del operador.
- Pero: cuenta Google del operador queda alojada en infraestructura Vendel (privacidad/responsabilidad).

**Opción B — En el teléfono Android del operador** (distribuido, consistente con el modelo actual):

- Binario Go compilado para `android/arm64`, empaquetado como asset del APK Kotlin de Vendel Agent.
- La app Android lo arranca como proceso hijo (Foreground Service) y se comunica por Unix socket o localhost.
- La cuenta Google se queda en el teléfono.
- Igual de distribuido que el `modem-agent` actual.

**Recomendación: Opción B**. Mantiene la semántica "device físico = teléfono real con SIM real" que Vendel ya implementa, y la cuenta Google no sale del control del operador.

### 8.5 ¿Por qué no `gomobile bind`?

`gomobile bind` requiere que la API exportada sea "gomobile-compatible": primitivos, strings, bytes; no soporta directamente structs complejos con punteros recursivos. `libgm` tiene mucha estructura compleja que no se traduce 1:1.

Compilar el binario Go directamente para `android/arm64` y arrancarlo como proceso hijo es más limpio: la app Kotlin queda como UI + notificaciones + lifecycle, y el agent hace todo el trabajo de protocolo.

---

## 9. Riesgos honestos

1. **Issue #57 sin resolver upstream**: hay que vivir con el fork o adoptar el flujo de `mautrix-manager` hasta que se merge el fix. **No desplegar sin esto en mente.**
2. **Cualquier release de Google Messages puede romperlo**: la cadencia mensual de mautrix/gmessages refleja exactamente eso. Necesario pipeline CI que haga pull de upstream, recompile el agent y lo redistribuya.
3. **AGPL**: aislar `gmessages-agent` como proceso separado es **obligatorio** si Vendel quiere mantener otra licencia.
4. **TOS de Google**: a escala humana es invisible; a escala A2P masivo Google detecta y banea cuentas. Feature personal/SOHO, no para campañas masivas.
5. **Login UX**: el pairing GAIA requiere captura de cookies de una sesión Google logueada en browser. El proyecto `mautrix-manager` o el "cookie cURL paste" son los métodos en uso. Diseñar onboarding cuidadoso, no es trivial.
6. **Granularidad de protocolo**: el cliente delega RCS-vs-SMS al teléfono pareado. Si Vendel quiere forzar SMS aunque haya RCS, debe manipular `UpdateSettings` o aceptar el comportamiento por defecto.

---

## 10. Próximos pasos

### Validación inmediata

1. **POC standalone** en `/tmp/gmessages-poc/`: binario Go importando `pkg/libgm`, hacer pairing GAIA con una cuenta de prueba y enviar un mensaje RCS de prueba. Validar in-vivo que el protocolo sigue funcionando hoy y que el bug del issue #57 efectivamente aparece o no (probar tanto upstream como fork de `rusty4444`).
2. Si funciona: diseñar el contrato HTTP/gRPC entre `gmessages-agent` y `backend/services/sms`.

### Integración

3. Crear `gmessages-agent/` con el binario Go embebiendo `libgm` y exponiendo API local.
4. Añadir transport `gmessages` en `backend/services/sms/`.
5. Migración: añadir campo `type` enum (`modem`, `android_fcm`, `android_gmessages`) y `auth_blob` cifrado en colección `devices`.
6. UI en `frontend/src/routes/`: flujo de onboarding del device gmessages (importación de cookies, monitoreo de estado).
7. CI pipeline: tracking de upstream `mautrix/gmessages`, rebuild automático, semver pinning.

### Producto

8. Documentar la decisión de NO usar la business API y la naturaleza unofficial del transporte gmessages (compliance interno).
9. Plan de contingencia: si Google rompe el protocolo definitivamente, fallback a la opción Selenium (más frágil pero rescatable rápido) o a LSPosed (rooted devices).

---

## 11. Referencias

### Documentación oficial Android / Google

- [`ImsRcsManager` — Android Developers](https://developer.android.com/reference/android/telephony/ims/ImsRcsManager)
- [RCS Business Messaging API — Google for Developers](https://developers.google.com/business-communications/rcs-business-messaging/reference/rest)
- [Turn on RCS chats in Google Messages — Google Support](https://support.google.com/messages/answer/7189714)

### Investigación reverse engineering

- [Google Messages has a hidden RCS API — XDA Developers](https://www.xda-developers.com/google-messages-rcs-api-third-party-apps/)
- [Google Messages preps 'External Messaging' permission — 9to5Google](https://9to5google.com/2021/07/09/google-messages-permission-samsung-message-continuity/)
- [Google Messages for web removing QR code pairing — 9to5Google](https://9to5google.com/2026/03/23/google-messages-web-qr-removal/)
- [Google generalizes 'Messages for web' to 'Device Pairing' — 9to5Google](https://9to5google.com/2021/05/31/google-messages-for-web-device-pairing/)

### Proyectos open source

- [mautrix/gmessages — GitHub](https://github.com/mautrix/gmessages)
- [ROADMAP.md](https://github.com/mautrix/gmessages/blob/main/ROADMAP.md) | [CHANGELOG.md](https://github.com/mautrix/gmessages/blob/main/CHANGELOG.md)
- [`pkg/libgm/methods.go` — API pública](https://github.com/mautrix/gmessages/blob/main/pkg/libgm/methods.go)
- [`pkg/libgm/pair_google.go` — Flujo GAIA](https://github.com/mautrix/gmessages/blob/main/pkg/libgm/pair_google.go)
- [`pkg/libgm/gmproto/conversations.proto` — Definiciones SMS/RCS](https://github.com/mautrix/gmessages/blob/main/pkg/libgm/gmproto/conversations.proto)
- [Issue #57 — Google pairing logs out every 1–2h](https://github.com/mautrix/gmessages/issues/57)
- [PR #59 — Improves timeout so 401 polling error not as common](https://github.com/mautrix/gmessages/pull/59)
- [Fork `rusty4444/gmessages-matrix` — Session stability fix](https://github.com/rusty4444/gmessages-matrix)
- [`mautrix-manager` — Alternative login flow](https://github.com/mautrix/manager)
- [OpenMessage — Desktop client built on libgm](https://github.com/MaxGhenis/openmessage)
- [`android-rcs/rcsjta` — RCS-e stack histórico](https://github.com/android-rcs/rcsjta)

### Signature spoofing / LSPosed (Plan B)

- [LSPosed/CorePatch — Disable signature verification](https://github.com/LSPosed/CorePatch)
- [rushiiMachine/XSpoofSignatures](https://github.com/rushiiMachine/XSpoofSignatures)
- [Haruka — Signature Spoofing module — XDA](https://xdaforums.com/t/module-haruka-signature-spoofing-for-microg-on-any-rom.4744233/)
- [Signature Spoofing — microG Wiki](https://github.com/microg/GmsCore/wiki/Signature-Spoofing)
