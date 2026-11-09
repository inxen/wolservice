FROM golang:1.22-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG VERSION=0.7.0
ARG WOL_EMBEDDED_KEY=""
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X wol/internal/version.Version=${VERSION} ${WOL_EMBEDDED_KEY:+-X wol/internal/secrets.EmbeddedKey=${WOL_EMBEDDED_KEY}}" \
    -o /out/wol ./cmd/wol

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata iputils

WORKDIR /app
COPY --from=build /out/wol /usr/local/bin/wol
COPY configs/wol.example.yaml /app/wol.yaml

EXPOSE 8090
VOLUME ["/app"]

ENTRYPOINT ["wol", "-config", "/app/wol.yaml"]
