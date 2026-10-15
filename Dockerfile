FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/relay ./cmd/relay \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/scheduler ./cmd/scheduler \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/seed ./cmd/seed

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata wget \
    && adduser -D -H -u 65532 app

COPY --from=build /out/api /out/relay /out/worker /out/scheduler /out/migrate /out/seed /usr/local/bin/

USER app
EXPOSE 8080
CMD ["api"]
