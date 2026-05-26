# AWS End User Messaging — Guía de configuración

Esta guía describe cómo conectar Vendel con **AWS End User Messaging (AEUM)** — el sucesor de Pinpoint SMS and Voice v2 — para enviar SMS, RCS texto y short codes desde tu dashboard. Está pensada para un operador AWS-naïve; siguiéndola paso a paso el setup completo lleva menos de 30 minutos.

---

## Resumen

- **Soporta**: SMS por long code / toll-free / 10DLC / sender ID, short codes, RCS texto (con fallback SMS opcional cuando el pool lo incluye).
- **Out of scope MVP**: rich cards, carousels, media, suggested replies, métricas de coste en el dashboard.
- **Política de canal**: vive en AWS. El pool decide el canal y la identidad de origen por destinatario; Vendel solo manda texto.
- **Modelo en Vendel**: un único `sms_devices` virtual con `device_type="aws_aeum"`. Aparece como "AWS End User Messaging" en la lista de devices, no se puede editar ni borrar desde la UI.
- **Delivery status**: AEUM → ConfigurationSet → SNS topic → HTTPS subscription al endpoint público de Vendel. Sin Lambda intermedia.

---

## Prerrequisitos

- Cuenta AWS con permisos de admin (o capacidad de crear IAM users, pools, ConfigurationSets y topics SNS).
- Dominio HTTPS público apuntando a tu instancia de Vendel (necesario para que SNS pueda enviarte el callback de eventos). `http://` no funciona — SNS HTTPS subscription rechaza endpoints no cifrados.
- Subscripción activa en AEUM **o** una cuenta saliendo del sandbox para producción. En sandbox solo puedes enviar a números verificados.
- Acceso al `.env` de tu instancia de Vendel para inyectar las nuevas variables.

---

## Paso 1 — Crear IAM user con la policy mínima

1. AWS Console → **IAM → Users → Create user**.
2. Nombre: `vendel-aeum`.
3. Acceso: marcar **Programmatic access** (Access Key + Secret Key).
4. Adjuntar la siguiente policy inline (es lo único que Vendel necesita; el resto del flujo SNS no requiere permisos AWS en el lado de Vendel porque se valida con firma X.509):

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

5. Finalizar la creación y **guardar el Access Key ID y el Secret Access Key**. Solo se muestran una vez.

> **Nota**: si quieres restringir el `Resource`, puedes pegar el ARN del pool específico que vas a crear en el Paso 2. Para el MVP basta `*`.

---

## Paso 2 — Crear el Phone pool

El pool es la unidad de origination identity que AEUM usa para enrutar mensajes. Puede contener long codes, toll-free, 10DLC, sender IDs, short codes y RCS Agents.

1. AWS Console → **End User Messaging → Phone pools → Create pool**.
2. Elige un nombre descriptivo, por ejemplo `vendel-default-pool`.
3. Selecciona el tipo de número inicial que vas a añadir (puede ser uno aprobado/comprado previamente, o un toll-free de sandbox para pruebas).
4. Tras crear el pool, **copia su ARN** (formato `arn:aws:sms-voice:<region>:<account>:pool/<id>`). Lo necesitarás para `AEUM_ORIGINATION_IDENTITY_ARN`.
5. Si tienes más origination identities (otro número SMS, un short code, un sender ID), añádelas al mismo pool desde **End User Messaging → Phone numbers → Associate to pool**.

> **Por qué un pool y no una identity directa**: usar pool habilita fallback automático (p.ej. RCS → SMS) y deja a AWS gestionar sticky sending. Es la decisión recomendada para el MVP de Vendel.

---

## Paso 3 — Short code (opcional)

Si quieres usar short codes desde Vendel:

1. AWS Console → **End User Messaging → Phone numbers → Request short code**.
2. Sigue el flujo de aprobación de AWS (depende del país, puede tardar semanas).
3. Una vez aprobado, asócialo al pool del Paso 2 desde **Associate to pool**.

Vendel no necesita ninguna configuración adicional: el short code queda disponible automáticamente porque AWS lo selecciona cuando el destinatario es compatible.

---

## Paso 4 — RCS Agent (opcional)

Si quieres enviar RCS texto:

1. AWS Console → **End User Messaging → RCS Agents → Create agent**.
2. Completa la verificación de marca (logo, hero image, descripción, etc.). El proceso de aprobación con cada carrier puede tardar varios días.
3. Cuando el agent esté aprobado y activo, asócialo al pool del Paso 2.
4. (Recomendado) Mantén también un número SMS en el pool para que AWS haga fallback automático RCS → SMS cuando el destinatario no soporta RCS o no está alcanzable por RCS.

> **Importante**: Vendel solo envía texto plano. No expone rich cards, carousels ni suggested replies en el MVP.

---

## Paso 5 — Configuration Set

El ConfigurationSet es el contenedor de Event Destinations que reenvía eventos de delivery a tu SNS topic.

1. AWS Console → **End User Messaging → Configuration sets → Create**.
2. Nombre: por ejemplo `vendel-config-set`. **Anota este nombre exacto**: irá en `AEUM_CONFIGURATION_SET_NAME`.
3. Continúa sin añadir Event Destination todavía (lo enlazaremos en el Paso 6 después de crear el topic).

---

## Paso 6 — SNS topic + subscription

### 6.1 Crear el topic

1. AWS Console → **SNS → Topics → Create topic**.
2. Tipo: **Standard**.
3. Nombre: `vendel-sms-events`.
4. Crea el topic y copia su ARN.

### 6.2 Conectar el topic al ConfigurationSet

1. Vuelve a **End User Messaging → Configuration sets → `vendel-config-set` → Event destinations → Add destination**.
2. Tipo: **SNS topic**.
3. Selecciona el topic `vendel-sms-events`.
4. **Tipos de evento**: marca todos los `TEXT_*` (`TEXT_DELIVERED`, `TEXT_SUCCESSFUL`, `TEXT_BLOCKED`, `TEXT_INVALID`, `TEXT_TTL_EXPIRED`, `TEXT_CARRIER_UNREACHABLE`, `TEXT_UNREACHABLE`, `TEXT_CARRIER_BLOCKED`, `TEXT_UNKNOWN`, etc.) y los equivalentes RCS si tu cuenta los expone.
5. Guardar.

### 6.3 Suscribir Vendel al topic

1. AWS Console → **SNS → Topics → `vendel-sms-events` → Create subscription**.
2. **Protocol**: HTTPS.
3. **Endpoint**: `https://<tu-dominio-de-vendel>/api/webhooks/aws-aeum-events`.
4. **Enable raw message delivery**: dejarlo **desmarcado**. Vendel necesita el envelope SNS para validar la firma X.509.
5. Crear la suscripción. Quedará en estado `PendingConfirmation`.
6. Vendel, en cuanto arranque con `AEUM_ENABLED=true` (Paso 7), recibirá el `SubscriptionConfirmation` y lo confirmará automáticamente. La suscripción pasará a `Confirmed`.

---

## Paso 7 — Variables de entorno en Vendel

Edita el `.env` de tu instancia de Vendel y añade el bloque AEUM:

```bash
AEUM_ENABLED=true
AEUM_REGION=us-east-1
AWS_ACCESS_KEY_ID=AKIA...
AWS_SECRET_ACCESS_KEY=...
AEUM_ORIGINATION_IDENTITY_ARN=arn:aws:sms-voice:us-east-1:123456789012:pool/abc...
AEUM_CONFIGURATION_SET_NAME=vendel-config-set
AEUM_SNS_TOPIC_ARN=arn:aws:sns:us-east-1:123456789012:vendel-sms-events
AEUM_CHANNEL_MODE=auto
AEUM_DEVICE_NAME=AWS End User Messaging
```

Notas:

- `AEUM_REGION` debe coincidir con la región donde creaste pool y ConfigurationSet.
- `AEUM_ORIGINATION_POOL_ARN` es un alias legacy. Si por alguna razón usas ese nombre, Vendel lo lee solo cuando `AEUM_ORIGINATION_IDENTITY_ARN` está vacío.
- `AEUM_SNS_TOPIC_ARN` es **obligatoria**. El webhook hace fail-closed (HTTP 500) si está vacía. Razón: la firma SNS solo demuestra que el remitente es AWS; sin esta variable Vendel aceptaría eventos firmados de cualquier topic AWS (spoofing cruzado entre tenants). Configúrala con el ARN exacto del topic del Paso 6.
- `AEUM_CHANNEL_MODE` solo acepta `auto` en el MVP. El selector de canal vive en AWS.
- `AWS_ACCESS_KEY_ID` y `AWS_SECRET_ACCESS_KEY` son las credenciales del IAM user del Paso 1. El SDK Go las lee automáticamente.

Reinicia Vendel después de editar el `.env`.

---

## Paso 8 — Smoke test

1. Verifica en logs que Vendel arrancó con AEUM habilitado:

```bash
docker compose logs app | grep -i aeum
```

   Debes ver una línea indicando que `EnsureAEUMDevice` creó (o ya tenía) el device virtual.

2. Abre el dashboard `/devices`. Debe aparecer una fila **"AWS End User Messaging"** sin botones de Edit/Delete.

3. Manda un mensaje de prueba. Si tienes un device físico online, fuerza el envío vía AEUM pasando el `device_id` del AEUM device:

```bash
curl -X POST https://<tu-dominio>/api/sms/send \
  -H "Authorization: Bearer <API_KEY>" \
  -H "Content-Type: application/json" \
  -d '{
    "recipients": ["+15551234567"],
    "body": "Hello from AEUM",
    "device_id": "<id-del-aeum-device>"
  }'
```

   Si no tienes devices físicos, basta con omitir `device_id`: AEUM actúa como fallback.

4. En unos segundos deberías ver:
   - `sms_messages.status = sent` justo después de la llamada a AEUM.
   - `sms_messages.provider_message_id` poblado con el MessageId de AWS.
   - `sms_messages.status = delivered` cuando llegue el evento `TEXT_DELIVERED` vía SNS.
   - Webhooks `sms_sent` y `sms_delivered` disparados a tus subscribers, si los tienes configurados.

5. Verifica en AWS Console → SNS → `vendel-sms-events` → Subscriptions que la suscripción HTTPS está `Confirmed`.

---

## Troubleshooting

| Síntoma | Causa probable | Solución |
|---|---|---|
| La suscripción SNS sigue en `PendingConfirmation` tras varios minutos | El endpoint HTTPS no responde, devuelve 5xx, o la firma SNS no se valida | Revisa los logs de Vendel buscando `subscription confirm failed` o errores 4xx/5xx en `POST /api/webhooks/aws-aeum-events`. Asegúrate de que el dominio es público y tiene certificado TLS válido. |
| 401 en el webhook | `SigningCertURL` inválido o firma SNS incorrecta | Verifica que el host del `SigningCertURL` es `sns.<region>.amazonaws.com`. Si usas un proxy/CDN, asegúrate de que no está modificando el body antes de llegar al backend. |
| Mensajes nunca llegan al destinatario | El pool no contiene una origination identity aprobada para ese destino, o la cuenta está en sandbox | AWS Console → End User Messaging → Phone numbers: verifica que hay al menos una identity activa, registrada y asociada al pool. Si estás en sandbox, valida que el destinatario está en la lista de números verificados. |
| Mensajes en `status=sent` pero nunca pasan a `delivered` | El ConfigurationSet no tiene Event Destination apuntando al topic, o el topic no tiene la subscription confirmada | Repasa Paso 6.2 y 6.3. Manda otro mensaje y revisa CloudWatch del topic SNS para ver si AEUM está publicando eventos. |
| `ValidationException` en logs al enviar | `AEUM_ORIGINATION_IDENTITY_ARN` apunta a un pool que no existe en `AEUM_REGION`, o `AEUM_CONFIGURATION_SET_NAME` está mal escrito | Confirma región y nombres exactos. ARN y nombre del ConfigurationSet son case-sensitive. |
| `ThrottlingException` recurrente | Cuenta sandbox o spending limit muy bajo | AWS Console → End User Messaging → Account-level settings: revisa límites y solicita aumento si procede. |
| El device AEUM aparece duplicado | Bug improbable: `EnsureAEUMDevice` es idempotente | Si ocurre, borrar manualmente uno de los registros desde el admin de PocketBase. Reportar el caso como issue. |
| El device AEUM no aparece en la UI | `AEUM_ENABLED=false`, o falta alguna de las vars obligatorias | Revisar logs al arrancar. Vendel **no** crea el device si la configuración está incompleta. |

---

## Variables de entorno

| Variable | Obligatoria si AEUM habilitado | Default | Descripción |
|---|---|---|---|
| `AEUM_ENABLED` | sí | `false` | Maestra: habilita/deshabilita la integración |
| `AEUM_REGION` | sí | — | Región AWS (ej. `us-east-1`) |
| `AWS_ACCESS_KEY_ID` | sí | — | Leída por el SDK |
| `AWS_SECRET_ACCESS_KEY` | sí | — | Leída por el SDK |
| `AEUM_ORIGINATION_IDENTITY_ARN` | sí | — | ARN del pool recomendado |
| `AEUM_ORIGINATION_POOL_ARN` | no | — | Alias legacy opcional |
| `AEUM_CONFIGURATION_SET_NAME` | sí | — | Nombre del ConfigurationSet |
| `AEUM_SNS_TOPIC_ARN` | sí | — | ARN exacto del topic SNS. El webhook hace fail-closed (HTTP 500) si está vacía. Necesario porque la firma SNS solo prueba "vino de AWS", no "vino de nuestro topic". |
| `AEUM_CHANNEL_MODE` | no | `auto` | Solo `auto` en MVP |
| `AEUM_DEVICE_NAME` | no | `"AWS End User Messaging"` | Nombre visible en UI |

---

## Recomendaciones operativas

- **Spending limit**: en **End User Messaging → Account-level settings**, configura un *Monthly spend limit* para evitar facturas inesperadas. AWS te bloqueará el envío cuando alcances el umbral.
- **Sandbox vs producción**: solicita salida del sandbox AWS con suficiente antelación. El proceso suele tardar 1–2 días hábiles.
- **Pool por caso de uso**: si manejas múltiples marcas o casos de uso (marketing vs transaccional), considera crear pools separados y rotar `AEUM_ORIGINATION_IDENTITY_ARN` por instancia. En el MVP solo hay un pool por instancia de Vendel.
- **Monitoring**: la métrica más útil es la tasa `delivered / sent` por día. Si cae súbitamente, revisa CloudWatch del ConfigurationSet o el estado del pool.

---

## Límites del MVP

- Solo texto en RCS (sin rich cards, carousels, media ni suggested replies).
- `AEUM_CHANNEL_MODE` solo soporta `"auto"`. No hay forma de forzar SMS-only o RCS-only desde Vendel; esa política vive en el pool de AWS.
- No hay selección manual de short code por request. AWS elige la origination identity del pool.
- No hay dashboard de costos en Vendel. Usa el monitoring nativo de AWS.
- Sin migración asistida entre regiones: cambiar `AEUM_REGION` implica reconfigurar pool, ConfigurationSet y topic en la nueva región.
- Credenciales globales del operador (un único IAM user por instancia de Vendel). No hay credenciales por usuario final.

---

## Referencias AWS

- [End User Messaging — RCS overview](https://docs.aws.amazon.com/sms-voice/latest/userguide/rcs-overview.html)
- [RCS to SMS fallback with pools](https://docs.aws.amazon.com/sms-voice/latest/userguide/rcs-sms-fallback.html)
- [Phone number / origination identity types](https://docs.aws.amazon.com/sms-voice/latest/userguide/phone-number-types.html)
- [Requesting dedicated short codes](https://docs.aws.amazon.com/sms-voice/latest/userguide/phone-numbers-request-short-code.html)
- [SNS HTTPS endpoint subscription](https://docs.aws.amazon.com/sns/latest/dg/sns-http-https-endpoint-as-subscriber.html)
