FROM node:22-alpine AS frontend
WORKDIR /build/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS backend
WORKDIR /build
COPY go.mod ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /magistry ./cmd/magistry

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && addgroup -S -g 10001 magistry && adduser -S -u 10001 -G magistry magistry
WORKDIR /app
COPY --from=backend /magistry /usr/local/bin/magistry
COPY --from=frontend /build/web/dist ./web/dist
COPY --from=registry:3 /bin/registry /usr/local/bin/registry
COPY docker/registry.yml /etc/distribution/config.yml
USER magistry
ENV LISTEN_ADDR=:5000 REGISTRY_URL=http://registry:5000 WEB_DIR=/app/web/dist
ENV DATA_DIR=/var/lib/magistry REGISTRY_DATA_DIR=/var/lib/registry REGISTRY_BINARY=/usr/local/bin/registry REGISTRY_CONFIG=/etc/distribution/config.yml
EXPOSE 5000
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s CMD wget -q -O /dev/null http://127.0.0.1:5000/healthz || exit 1
ENTRYPOINT ["magistry"]
