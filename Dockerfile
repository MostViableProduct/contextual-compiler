FROM golang:1.23-alpine AS builder

RUN apk add --no-cache ca-certificates

WORKDIR /build

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /compiler ./cmd/compiler/

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /compiler /compiler
COPY --from=builder /build/config/examples/ /config/examples/

EXPOSE 8200

USER nonroot:nonroot

ENTRYPOINT ["/compiler"]
