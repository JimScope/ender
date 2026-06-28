# Stage 1: Build frontend
FROM oven/bun:1 AS frontend-builder
WORKDIR /app/frontend

COPY frontend/package.json frontend/bun.lock ./
RUN ["bun", "install", "--frozen-lockfile"]

COPY frontend/ ./
ENV NODE_ENV=production
RUN ["bun", "run", "build"]

# Stage 2: Build Go binary
FROM golang:1.26-alpine AS go-builder
RUN apk add --no-cache tzdata
WORKDIR /app

COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ .
COPY --from=frontend-builder /app/frontend/dist ./ui/dist/
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/vendel -ldflags="-s -w -X main.version=docker" .

# Stage 3: Runtime
FROM alpine:3.22
RUN apk add --no-cache ca-certificates
WORKDIR /app

COPY --from=go-builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=go-builder /app/vendel /app/vendel

EXPOSE 8090

ENTRYPOINT ["/app/vendel"]
CMD ["serve", "--http=0.0.0.0:8090"]
