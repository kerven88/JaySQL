# syntax=docker/dockerfile:1
FROM node:22-bookworm-slim AS frontend
WORKDIR /src/www
COPY www/package.json www/package-lock.json ./
RUN npm ci
COPY www/ ./
COPY docs/manual/ /src/docs/manual/
RUN npm run build

FROM golang:1.23.12-bookworm AS backend
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN rm -rf web/dist
COPY --from=frontend /src/www/dist/ ./web/dist/
RUN CGO_ENABLED=0 go build -trimpath -o /out/goinsight ./cmd

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata curl \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --uid 10001 jaysql \
    && mkdir -p /opt/app/goinsight/logs /opt/app/goinsight/media \
    && chown -R jaysql:jaysql /opt/app/goinsight
WORKDIR /opt/app/goinsight
COPY --from=backend /out/goinsight ./goinsight
USER jaysql
EXPOSE 8083
ENTRYPOINT ["./goinsight"]
CMD ["-config", "/opt/app/goinsight/config.yaml"]
