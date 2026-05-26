<p align="center">
  <img src="img/vendel-icon.png" alt="Vendel" width="80" height="80" />
</p>

<h1 align="center">Vendel</h1>

<p align="center">Plataforma SMS Gateway</p>

<p align="center">
  <a href="https://vendel.cc">Website</a> &middot;
  <a href="https://app.vendel.cc">Dashboard</a> &middot;
  <a href="./README.md">Read in English</a>
</p>

<p align="center">
  <a href="https://www.producthunt.com/products/vendel?embed=true&amp;utm_source=badge-featured&amp;utm_medium=badge&amp;utm_campaign=badge-vendel" target="_blank" rel="noopener noreferrer"><img alt="Vendel - An open source SMS gateway for your own devices | Product Hunt" width="250" height="54" src="https://api.producthunt.com/widgets/embed-image/v1/featured.svg?post_id=1119265&amp;theme=dark&amp;t=1775751459800"></a>
</p>

Vendel es una plataforma full-stack para gestión y envío de SMS a través de dispositivos conectados. Permite enviar mensajes SMS usando dispositivos registrados (teléfonos Android o módems) como gateways, con gestión de cuotas, webhooks y soporte para múltiples usuarios.

<p align="center">
  <img src="img/homepage.png" alt="Vendel Homepage" width="800" />
</p>

## Stack Tecnológico

### Backend
- **Framework**: [PocketBase](https://pocketbase.io/) (Go)
- **Base de datos**: SQLite (embebido)
- **Autenticación**: JWT (built-in), OAuth2 (Google, GitHub)
- **Push Notifications**: Firebase Cloud Messaging (FCM)
- **Email**: Soporte SMTP integrado, Mailcatcher para dev local
- **Admin**: Dashboard de PocketBase en `/_/`

### Frontend
- **Framework**: React 19 con TypeScript
- **Build**: Vite
- **Estado**: TanStack Query + TanStack Router
- **Estilos**: Tailwind CSS + shadcn/ui
- **Formularios**: React Hook Form + Zod
- **Cliente API**: PocketBase JS SDK
- **Tests E2E**: Playwright

### Infraestructura
- **Hosting Backend**: Render
- **Hosting Frontend**: Cloudflare Pages
- **Contenedores**: Docker & Docker Compose
- **CI/CD**: GitHub Actions
- **Releases**: Tag `v*` → Imagen Docker en GHCR + binarios Go vía GoReleaser

## Funcionalidades Principales

### SMS
- Envío de SMS individuales y masivos
- Distribución round-robin entre dispositivos
- Cola de mensajes cuando no hay dispositivos online
- Tracking de estado (pending, queued, processing, sent, delivered, failed)
- Historial y reportes de SMS
- Soporte para SMS entrantes

### Dispositivos
- Registro de dispositivos con API keys únicas
- Gestión de tokens FCM para push notifications
- Monitoreo de estado de dispositivos

### Cuotas y Planes
- Múltiples planes de suscripción
- Tracking de cuota mensual de SMS
- Límites de dispositivos por plan
- Reset automático mensual de cuota (cron)

### Webhooks
- Suscripciones de webhooks configurables por tipo de evento
- Eventos soportados: `sms_received`, `sms_sent`, `sms_delivered`, `sms_failed`
- Payloads firmados con HMAC-SHA256 y JSON con claves ordenadas

### Pagos
- Abstracción de proveedores de pago (QvaPay)
- Gestión de ciclo de vida de suscripciones
- Flujos de pago por factura y autorización

### Integraciones
- API keys múltiples por usuario
- Códigos QR para onboarding de dispositivos
- API pública para sistemas externos

## Proveedores SMS externos

Vendel puede entregar SMS a través de gateways externos además de teléfonos Android físicos y módems USB. Actualmente AWS End User Messaging (AEUM) es el primer proveedor externo soportado.

Cuando `AEUM_ENABLED=true`, Vendel crea un dispositivo virtual global "AWS End User Messaging" que:
- Aparece en la lista de dispositivos de cada usuario (solo lectura).
- Actúa como fallback cuando el usuario no tiene dispositivos físicos online.
- Puede dirigirse explícitamente vía `device_id` en `POST /api/sms/send`.

El pool de AWS al que apuntes puede incluir short codes, sender IDs, números 10DLC y RCS Agents. Vendel llama a `SendTextMessage` una vez por destinatario; AWS elige el canal y la origination identity desde el pool.

Los eventos de delivery vuelven vía SNS HTTPS subscription a `/api/webhooks/aws-aeum-events`; Vendel actualiza `sms_messages.status` y dispara los webhooks estándar `sms_delivered` / `sms_failed`.

Configuración: ver [`docs/aws-end-user-messaging-setup.md`](docs/aws-end-user-messaging-setup.md).

Limitaciones del MVP:
- RCS solo texto — sin rich cards, carousels, media ni suggested replies.
- La selección de canal vive en AWS (el pool decide); Vendel solo expone `channel=auto`.
- El costo no se trackea en Vendel; configura un límite mensual de gasto en la consola de AWS.

## Estructura del Proyecto

```
vendel/
├── backend/                    # Go + PocketBase API
│   ├── main.go                 # Setup de PocketBase, hooks, cron, rutas
│   ├── go.mod / go.sum
│   ├── handlers/               # Rutas API custom (sms, planes, webhooks)
│   ├── services/               # Lógica de negocio (SMS, FCM, cuota, suscripciones)
│   │   └── payment/            # Proveedor de pago (QvaPay)
│   ├── middleware/              # Auth por API key, modo mantenimiento
│   └── migrations/             # Definiciones de colecciones + datos semilla
├── frontend/                   # App React
│   ├── src/
│   │   ├── routes/             # Páginas (TanStack Router)
│   │   ├── components/         # Componentes React
│   │   ├── hooks/              # Hooks custom (PocketBase SDK)
│   │   └── lib/pocketbase.ts   # Cliente PocketBase
│   └── tests/                  # Tests Playwright
├── modem-agent/                # Agente Go para módems USB (AT commands)
├── Dockerfile                  # Multi-stage (node + go + alpine)
├── docker-compose.yml
├── litestream.yml              # Config de replicación Litestream (opt-in)
├── entrypoint.sh               # Startup condicional (con/sin Litestream)
└── .env                        # Variables de entorno
```

## Inicio Rápido

### Requisitos Previos
- Docker y Docker Compose
- Go 1.23+ (para dev local del backend)
- Node.js 24+ (para dev local del frontend)

### Desarrollo con Docker Compose (Recomendado)

```bash
# Iniciar la app
docker compose up -d

# Ver logs
docker compose logs -f app
```

**Servicios disponibles:**
| Servicio | URL |
|----------|-----|
| App (API + Frontend) | http://localhost:8090 |
| PocketBase Admin | http://localhost:8090/_/ |
| Mailcatcher | http://localhost:1080 |

### Desarrollo Manual

#### Backend
```bash
cd backend

# Ejecutar servidor de desarrollo
go run . serve --http=0.0.0.0:8090

# Compilar binario
go build -o vendel .
./vendel serve --http=0.0.0.0:8090
```

#### Frontend
```bash
cd frontend

# Instalar dependencias
npm install

# Servidor de desarrollo
npm run dev

# Build
npm run build

# Tests E2E
npx playwright test
```

#### Modem Agent

El modem agent permite usar módems USB LTE/4G/5G con tarjeta SIM física como gateways de SMS — sin necesidad de un teléfono Android.

```bash
cd modem-agent

# Configurar módems (formato: api_key:command_port[:notify_port], separados por coma)
export VENDEL_URL=http://localhost:8090
export MODEMS="tu_api_key_del_dispositivo:/dev/ttyUSB0:/dev/ttyUSB1"

# Ejecutar
go run .
```

## Configuración

### Variables de Entorno

Crea un archivo `.env` en la raíz del proyecto:

```env
# Core
ENVIRONMENT=local
FIRST_SUPERUSER=admin@vendel.cc
FIRST_SUPERUSER_PASSWORD=changethis

# Firebase (push notifications)
FIREBASE_SERVICE_ACCOUNT_JSON=<json-de-firebase>

# OAuth (opcional)
GOOGLE_CLIENT_ID=
GOOGLE_CLIENT_SECRET=
GITHUB_CLIENT_ID=
GITHUB_CLIENT_SECRET=

# Pago (QvaPay)
QVAPAY_APP_ID=
QVAPAY_APP_SECRET=

# Seguridad
WEBHOOK_ENCRYPTION_KEY=         # Clave AES para secretos de webhooks

# SMTP (por defecto localhost:1025 para mailcatcher en dev)
SMTP_HOST=
SMTP_PORT=
SMTP_USERNAME=
SMTP_PASSWORD=

# Backup (Litestream - opcional)
LITESTREAM_REPLICA_URL=         # ej. s3://my-bucket/vendel/data
LITESTREAM_ACCESS_KEY_ID=
LITESTREAM_SECRET_ACCESS_KEY=

# URLs
APP_URL=http://localhost:8090
FRONTEND_URL=http://localhost:5173    # Usar el valor de APP_URL en producción
```

## Testing

### Frontend
```bash
# Tests E2E
npx playwright test

# Modo UI
npx playwright test --ui
```

## Despliegue

- **Backend**: Desplegado en [Render](https://render.com) (despliegue manual)
- **Frontend**: Desplegado en [Cloudflare Pages](https://pages.cloudflare.com) (despliegue manual)
- **Releases**: Crear un tag (`git tag v0.1.0 && git push --tags`) para publicar imagen Docker en GHCR y compilar binarios Go vía GoReleaser
- **Modem Agent**: Crear un tag (`git tag modem-agent/v0.1.0 && git push --tags`) para compilar binarios del modem agent

## Repositorios Relacionados

| Repositorio | Descripción |
|-------------|-------------|
| [vendel-homepage](https://github.com/JimScope/vendel-homepage) | Landing page y sistema de diseño |
| [vendel-android](https://github.com/JimScope/vendel-android) | App Android (gateway de dispositivo) |
| [vendel-mcp](https://github.com/JimScope/vendel-mcp) | Servidor MCP para asistentes de IA |
| [vendel-sdk-js](https://github.com/JimScope/vendel-sdk-js) | SDK JavaScript/TypeScript (`vendel-sdk` en npm) |
| [vendel-sdk-python](https://github.com/JimScope/vendel-sdk-python) | SDK Python (`vendel-sdk` en PyPI) |
| [vendel-sdk-go](https://github.com/JimScope/vendel-sdk-go) | SDK Go (Go modules) |

## Licencia

MIT License
