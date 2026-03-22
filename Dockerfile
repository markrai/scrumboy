FROM golang:1.22-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/scrumboy ./cmd/scrumboy

FROM alpine:3.20

RUN mkdir -p /data
ENV BIND_ADDR=:8080 \
    DATA_DIR=/data \
    SQLITE_PATH=/data/app.db \
    SQLITE_BUSY_TIMEOUT_MS=5000 \
    SQLITE_JOURNAL_MODE=WAL \
    SQLITE_SYNCHRONOUS=FULL

VOLUME ["/data"]
EXPOSE 8080

COPY --from=build /out/scrumboy /scrumboy
ENTRYPOINT ["/scrumboy"]
