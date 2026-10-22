# Двухэтапная сборка: в первом образе есть Go и исходники,
# во второй попадает только готовый бинарник (~10 МБ вместо ~800 МБ).
FROM golang:1.25.1-alpine AS build
WORKDIR /src
# Сначала только go.mod/go.sum: Docker закэширует скачанные зависимости,
# и при правке кода они не будут качаться заново.
COPY go.mod ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
EXPOSE 8080
# nonroot: процесс работает не от root, так безопаснее.
USER nonroot:nonroot
ENTRYPOINT ["/api"]
