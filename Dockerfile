FROM golang:1.23.8-alpine3.20 AS builder

WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/grumble ./cmd/grumble

FROM alpine:3.20

RUN addgroup -S grumble && adduser -S -G grumble -h /data grumble \
    && apk add --no-cache ca-certificates \
    && mkdir -p /data \
    && chown -R grumble:grumble /data

WORKDIR /data
COPY --from=builder /out/grumble /usr/local/bin/grumble

USER grumble

ENV DATA_DIR=/data

VOLUME ["/data"]
EXPOSE 7880/tcp
EXPOSE 64738/tcp

ENTRYPOINT ["/usr/local/bin/grumble","--datadir","/data"]
