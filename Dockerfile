FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm test -- --run && npm run build

FROM golang:1.27-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY --from=web /src/web/dist/ ./internal/webassets/dist/
ARG VERSION=dev
RUN mkdir -p /out/data /out/backups && CGO_ENABLED=0 go test ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/controlpanel ./cmd/controlpanel

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=backend /out/controlpanel /controlpanel
COPY --from=backend --chown=65532:65532 /out/data /data
COPY --from=backend --chown=65532:65532 /out/backups /backups
VOLUME ["/data", "/backups"]
EXPOSE 8080
ENTRYPOINT ["/controlpanel"]
