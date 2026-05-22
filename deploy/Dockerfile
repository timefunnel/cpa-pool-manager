FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod .
RUN go mod download
COPY . .
RUN apk add --no-cache build-base sqlite-dev && \
    go mod tidy && \
    CGO_ENABLED=1 GOOS=linux go build -o /out/cpa-pool-manager ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata sqlite-libs
WORKDIR /app
COPY --from=builder /out/cpa-pool-manager /app/cpa-pool-manager
EXPOSE 18417
CMD ["/app/cpa-pool-manager"]
