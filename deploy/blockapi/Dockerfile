FROM golang:1.26.2-alpine3.23 AS builder

WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /blockapi .

FROM alpine:3.20

ENV TZ=Asia/Shanghai
RUN apk add --no-cache tzdata ca-certificates

COPY --from=builder /blockapi /usr/local/bin/blockapi

EXPOSE 8080
ENTRYPOINT ["blockapi"]
